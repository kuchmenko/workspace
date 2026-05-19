package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fsnotify/fsnotify"
	"github.com/kuchmenko/workspace/internal/auth"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
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

func (d *Daemon) startReconcilers(cfg *DaemonConfig) {
	for _, ws := range cfg.Workspaces {
		d.startWorkspace(ws)
	}
}

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

func (d *Daemon) installSignalHandler() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		d.Shutdown()
	}()
}

func (d *Daemon) startAcceptLoop() {
	d.wg.Add(1)
	go d.runAcceptLoop()
}

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
	r.SetPushCooldown(ws.ResolvedPushCooldown())
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
			go r.Tick()
		}
	}
}

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

	err = proc.Signal(syscall.Signal(0))
	return pid, err == nil
}

func StartBackground() (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}

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

	proc.Release()

	fmt.Printf("  Daemon started (pid %d)\n", proc.Pid)
	fmt.Printf("  Log: %s\n", logPath)
	return proc.Pid, nil
}

type WorkspaceEntry struct {
	Root         string `toml:"root"`
	AutoSync     bool   `toml:"auto_sync"`
	PollInterval string `toml:"poll_interval,omitempty"`

	AutoBootstrap *bool `toml:"auto_bootstrap,omitempty"`

	PushCooldown string `toml:"push_cooldown,omitempty"`
}

const DefaultPushCooldown = time.Hour

func (w WorkspaceEntry) ResolvedPushCooldown() time.Duration {
	if w.PushCooldown == "" {
		return DefaultPushCooldown
	}
	if w.PushCooldown == "0" {
		return 0
	}
	d, err := time.ParseDuration(w.PushCooldown)
	if err != nil {
		return DefaultPushCooldown
	}
	return d
}

func (w WorkspaceEntry) AutoBootstrapEnabled() bool {
	if w.AutoBootstrap == nil {
		return true
	}
	return *w.AutoBootstrap
}

type DaemonSettings struct {
	LogLevel string `toml:"log_level"`
	Socket   string `toml:"socket"`
}

type DaemonConfig struct {
	Daemon     DaemonSettings   `toml:"daemon"`
	Workspaces []WorkspaceEntry `toml:"workspace"`
}

func ConfigDir() (string, error) {
	return auth.ConfigDir()
}

func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.toml"), nil
}

func SocketPath() (string, error) {
	cfg, err := LoadConfig()
	if err == nil && cfg.Daemon.Socket != "" {
		return expandHome(cfg.Daemon.Socket), nil
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.sock"), nil
}

func PidPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.pid"), nil
}

func LogPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.log"), nil
}

func LoadConfig() (*DaemonConfig, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	var cfg DaemonConfig
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

func SaveConfig(cfg *DaemonConfig) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "daemon.toml")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

func RegisterWorkspace(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	for _, w := range cfg.Workspaces {
		if w.Root == abs {
			return fmt.Errorf("workspace %q already registered", abs)
		}
	}
	cfg.Workspaces = append(cfg.Workspaces, WorkspaceEntry{
		Root:     abs,
		AutoSync: true,
	})
	return SaveConfig(cfg)
}

func UnregisterWorkspace(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	filtered := cfg.Workspaces[:0]
	found := false
	for _, w := range cfg.Workspaces {
		if w.Root == abs {
			found = true
			continue
		}
		filtered = append(filtered, w)
	}
	if !found {
		return fmt.Errorf("workspace %q not registered", abs)
	}
	cfg.Workspaces = filtered
	return SaveConfig(cfg)
}

func defaultConfig() *DaemonConfig {
	return &DaemonConfig{
		Daemon: DaemonSettings{
			LogLevel: "info",
		},
	}
}

func expandHome(path string) string {
	if len(path) > 1 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

func findGitRoot(dir string) string {
	for {
		if git.IsRepo(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func isClean(repoPath, file string) bool {
	cmd := exec.Command("git", "-C", repoPath, "status", "--porcelain", file)
	out, err := cmd.Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) == ""
}

func runIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s in %s: %s", name, strings.Join(args, " "), dir, strings.TrimSpace(string(out)))
	}
	return nil
}

func ensureUnionMerge(repoRoot, tomlAbs string) error {
	rel, err := filepath.Rel(repoRoot, tomlAbs)
	if err != nil {
		return err
	}
	attrPath := filepath.Join(repoRoot, ".gitattributes")
	wantLine := rel + " merge=union"
	existing, err := os.ReadFile(attrPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == wantLine {
			return nil
		}
	}
	f, err := os.OpenFile(attrPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		_, _ = f.WriteString("\n")
	}
	_, err = f.WriteString(wantLine + "\n")
	return err
}

func loadMachineName() string {
	mc, err := config.LoadMachineConfig()
	if err != nil || mc == nil {
		return ""
	}
	return mc.MachineName
}

func machineHostname() string {
	if name := loadMachineName(); name != "" {
		return name
	}
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

type Watcher struct {
	fsw    *fsnotify.Watcher
	logger *log.Logger
	mu     sync.Mutex

	seen map[string]time.Time
}

func NewWatcher(logger *log.Logger) *Watcher {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Printf("watcher: failed to create: %v", err)
		return &Watcher{logger: logger, seen: make(map[string]time.Time)}
	}
	return &Watcher{fsw: fsw, logger: logger, seen: make(map[string]time.Time)}
}

func (w *Watcher) Add(root string) {
	if w.fsw == nil {
		return
	}
	for _, dir := range topLevelGroupDirs(root) {
		if err := w.fsw.Add(dir); err != nil {
			w.logger.Printf("watcher: cannot watch %s: %v", dir, err)
		}
	}
}

func topLevelGroupDirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, filepath.Join(root, e.Name()))
	}
	return out
}

func (w *Watcher) Run(quit <-chan struct{}) {
	if w.fsw == nil {
		<-quit
		return
	}
	for {
		if !w.dispatchOne(quit) {
			return
		}
	}
}

func (w *Watcher) dispatchOne(quit <-chan struct{}) bool {
	select {
	case <-quit:
		return false
	case event, ok := <-w.fsw.Events:
		if !ok {
			return false
		}
		if event.Op&fsnotify.Create != 0 {
			w.handleCreate(event.Name)
		}
		return true
	case err, ok := <-w.fsw.Errors:
		if !ok {
			return false
		}
		w.logger.Printf("watcher: error: %v", err)
		return true
	}
}

func (w *Watcher) handleCreate(path string) {
	w.mu.Lock()
	if last, ok := w.seen[path]; ok && time.Since(last) < time.Second {
		w.mu.Unlock()
		return
	}
	w.seen[path] = time.Now()
	w.mu.Unlock()

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return
	}

	time.Sleep(500 * time.Millisecond)

	if !git.IsRepo(path) {
		return
	}

	w.logger.Printf("watcher: new git repo detected: %s", path)
}

func (w *Watcher) Close() {
	if w.fsw != nil {
		_ = w.fsw.Close()
	}
}

type Request struct {
	Cmd       string `json:"cmd"`
	Workspace string `json:"workspace,omitempty"`
	Event     string `json:"event,omitempty"`
}

type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

type StatusData struct {
	Running    bool             `json:"running"`
	Workspaces []WorkspaceEntry `json:"workspaces"`
	PID        int              `json:"pid"`
}

func listenSocket(socketPath string) (net.Listener, error) {
	if _, err := os.Stat(socketPath); err == nil {
		os.Remove(socketPath)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", socketPath, err)
	}

	os.Chmod(socketPath, 0o600)
	return ln, nil
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		writeResponse(conn, Response{OK: false, Error: "invalid request"})
		return
	}

	switch req.Cmd {
	case "status":
		writeResponse(conn, Response{
			OK: true,
			Data: StatusData{
				Running:    true,
				Workspaces: d.config.Workspaces,
				PID:        os.Getpid(),
			},
		})

	case "notify":
		if req.Workspace == "" || req.Event == "" {
			writeResponse(conn, Response{OK: false, Error: "workspace and event required"})
			return
		}
		d.handleNotify(req.Workspace, req.Event)
		writeResponse(conn, Response{OK: true})

	case "stop":
		writeResponse(conn, Response{OK: true})
		d.Shutdown()

	default:
		writeResponse(conn, Response{OK: false, Error: fmt.Sprintf("unknown command: %s", req.Cmd)})
	}
}

func writeResponse(conn net.Conn, resp Response) {
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	conn.Write(data)
}
