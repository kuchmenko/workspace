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

	logPath, err := LogPath()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening log: %w", err)
	}
	defer logFile.Close()

	logger := log.New(logFile, "", log.LstdFlags)

	socketPath, err := SocketPath()
	if err != nil {
		return err
	}

	ln, err := listenSocket(socketPath)
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

	// Write PID file
	if err := d.writePID(); err != nil {
		ln.Close()
		return err
	}
	defer d.cleanupPID()

	logger.Printf("daemon started (pid %d, socket %s)", os.Getpid(), socketPath)
	logger.Printf("watching %d workspace(s)", len(cfg.Workspaces))

	// Start per-workspace components
	for _, ws := range cfg.Workspaces {
		d.startWorkspace(ws)
	}

	// Start filesystem watcher
	d.watcher = NewWatcher(d.logger)
	for _, ws := range cfg.Workspaces {
		d.watcher.Add(ws.Root)
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.watcher.Run(d.quit)
	}()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		d.Shutdown()
	}()

	// Accept connections
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-d.quit:
					return
				default:
					logger.Printf("accept error: %v", err)
					continue
				}
			}
			go d.handleConnection(conn)
		}
	}()

	<-d.quit
	d.wg.Wait()
	logger.Println("daemon stopped")
	return nil
}

func (d *Daemon) startWorkspace(ws WorkspaceEntry) {
	d.logger.Printf("workspace: %s (auto_sync=%v)", ws.Root, ws.AutoSync)

	intervalStr := ws.PollInterval
	if intervalStr == "" {
		intervalStr = "5m"
	}
	interval := parseInterval(intervalStr)

	r := NewReconciler(ws.Root, interval, d.logger)
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
