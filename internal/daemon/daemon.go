package daemon

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Daemon struct {
	config   *DaemonConfig
	listener net.Listener
	logger   *log.Logger
	quit     chan struct{}
	wg       sync.WaitGroup

	reconcilers map[string]*Reconciler
	watcher     *Watcher
}

// Run starts the daemon in the foreground (blocking).
func Run() error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	logFile, logger, err := openDaemonLog()
	if err != nil {
		return err
	}
	defer logFile.Close()
	socketPath, ln, err := openDaemonSocket()
	if err != nil {
		return err
	}
	d := &Daemon{
		config:      cfg,
		listener:    ln,
		logger:      logger,
		quit:        make(chan struct{}),
		reconcilers: make(map[string]*Reconciler),
	}
	if err := d.writePID(); err != nil {
		ln.Close()
		return err
	}
	defer d.cleanupPID()
	logger.Printf("daemon started (pid %d, socket %s)", os.Getpid(), socketPath)
	logger.Printf("watching %d workspace(s)", len(cfg.Workspaces))

	d.startReconcilers(cfg)
	d.startWatcher(cfg)
	d.installSignalHandler()
	d.startAcceptLoop()

	<-d.quit
	d.wg.Wait()
	logger.Println("daemon stopped")
	return nil
}

// openDaemonLog opens the append-only daemon log file and returns
// it alongside a logger writing to it. Caller is responsible for
// closing the file (typical: defer).
func openDaemonLog() (*os.File, *log.Logger, error) {
	logPath, err := LogPath()
	if err != nil {
		return nil, nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("opening log: %w", err)
	}
	return logFile, log.New(logFile, "", log.LstdFlags), nil
}

// openDaemonSocket resolves the IPC socket path and binds a
// listener on it. Returns both so the caller can record the path
// in startup logs.
func openDaemonSocket() (string, net.Listener, error) {
	socketPath, err := SocketPath()
	if err != nil {
		return "", nil, err
	}
	ln, err := listenSocket(socketPath)
	if err != nil {
		return "", nil, err
	}
	return socketPath, ln, nil
}

// startReconcilers spins up one reconciler per registered workspace.
// Each goroutine is owned by the Daemon's startWorkspace helper.
func (d *Daemon) startReconcilers(cfg *DaemonConfig) {
	for _, ws := range cfg.Workspaces {
		d.startWorkspace(ws)
	}
}

// startWatcher launches the filesystem watcher goroutine.
// Watching is best-effort: it amplifies the reconciler ticks but
// the daemon stays correct without it.
func (d *Daemon) startWatcher(cfg *DaemonConfig) {
	d.watcher = NewWatcher(d.logger)
	for _, ws := range cfg.Workspaces {
		d.watcher.Add(ws.Root)
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.watcher.Run(d.quit)
	}()
}

// installSignalHandler subscribes a goroutine to SIGINT / SIGTERM
// that triggers the orderly Shutdown path on receipt.
func (d *Daemon) installSignalHandler() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		d.Shutdown()
	}()
}

// startAcceptLoop takes the listener and serves IPC connections in
// a goroutine until d.quit is closed. Accept errors during normal
// shutdown are swallowed (signaled via select on d.quit).
func (d *Daemon) startAcceptLoop() {
	d.wg.Add(1)
	go d.runAcceptLoop()
}

// runAcceptLoop is the goroutine body. Pulled out so the for/select
// nesting doesn't push startAcceptLoop's cognitive complexity over
// the threshold; the loop itself is straightforward but `for` plus
// `select` plus `if err` plus `case <-d.quit` add up fast.
func (d *Daemon) runAcceptLoop() {
	defer d.wg.Done()
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			if d.shouldStopAccept() {
				return
			}
			d.logger.Printf("accept error: %v", err)
			continue
		}
		go d.handleConnection(conn)
	}
}

// shouldStopAccept reports whether the Accept loop should terminate.
// True when d.quit has been closed; false during normal operation.
// Non-blocking: a transient error path checks quit-state without
// blocking on the channel.
func (d *Daemon) shouldStopAccept() bool {
	select {
	case <-d.quit:
		return true
	default:
		return false
	}
}

func (d *Daemon) startWorkspace(ws WorkspaceEntry) {
	d.logger.Printf("workspace: %s (auto_sync=%v auto_bootstrap=%v)", ws.Root, ws.AutoSync, ws.AutoBootstrapEnabled())

	intervalStr := ws.PollInterval
	if intervalStr == "" {
		intervalStr = "5m"
	}
	interval := parseInterval(intervalStr)

	r := NewReconciler(ws.Root, interval, d.logger)
	r.SetAutoBootstrap(ws.AutoBootstrapEnabled())
	d.reconcilers[ws.Root] = r

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		r.Run(d.quit)
	}()
}

func (d *Daemon) handleNotify(workspace, event string) {
	d.logger.Printf("notify: workspace=%s event=%s", workspace, event)
	switch event {
	case "config_changed":
		if r, ok := d.reconcilers[workspace]; ok {
			// Run async so the IPC handler returns immediately.
			go r.Tick()
		}
	}
}

// parseInterval parses a duration string like "5m" or "1h30m". Falls back
// to 5 minutes on parse error.
func parseInterval(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil || d < time.Minute {
		return 5 * time.Minute
	}
	return d
}

func (d *Daemon) Shutdown() {
	d.logger.Println("shutting down...")
	close(d.quit)
	d.listener.Close()
	if d.watcher != nil {
		d.watcher.Close()
	}
}

func (d *Daemon) writePID() error {
	path, err := PidPath()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func (d *Daemon) cleanupPID() {
	path, _ := PidPath()
	os.Remove(path)
	socketPath, _ := SocketPath()
	os.Remove(socketPath)
}

// IsRunning checks if a daemon process is alive.
func IsRunning() (int, bool) {
	path, err := PidPath()
	if err != nil {
		return 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return pid, false
	}
	// Signal 0 checks if process exists
	err = proc.Signal(syscall.Signal(0))
	return pid, err == nil
}

// StartBackground starts the daemon as a background process.
func StartBackground() (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}

	// Resolve symlinks to get actual binary
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return 0, err
	}

	logPath, err := LogPath()
	if err != nil {
		return 0, err
	}

	proc, err := os.StartProcess(exe, []string{exe, "daemon", "run"}, &os.ProcAttr{
		Dir:   "/",
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		return 0, fmt.Errorf("starting daemon: %w", err)
	}

	// Detach
	proc.Release()

	fmt.Printf("  Daemon started (pid %d)\n", proc.Pid)
	fmt.Printf("  Log: %s\n", logPath)
	return proc.Pid, nil
}
