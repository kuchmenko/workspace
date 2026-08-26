package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
)

type MachineConfig struct {
	MachineName    string `toml:"machine_name"`
	RunnerIDPrefix string `toml:"runner_id_prefix,omitempty"`

	WorkspaceRoots []string       `toml:"workspace_roots,omitempty"`
	ExplorerView   string         `toml:"explorer_view,omitempty"`
	RecentOrder    string         `toml:"recent_order,omitempty"`
	Runners        []RunnerConfig `toml:"runners,omitempty"`
}

type RunnerConfig struct {
	ID                    string `toml:"id"`
	Workspace             string `toml:"workspace,omitempty"`
	Group                 string `toml:"group,omitempty"`
	Project               string `toml:"project,omitempty"`
	Worktree              string `toml:"worktree,omitempty"`
	Path                  string `toml:"path,omitempty"`
	RemoteControlTerminal bool   `toml:"remote_control_terminal,omitempty"`
}

const (
	ExplorerViewProjects = "projects"
	ExplorerViewRecent   = "recent"
	RecentOrderAsc       = "asc"
	RecentOrderDesc      = "desc"
)

type legacyDaemonConfig struct {
	Workspaces []struct {
		Root string `toml:"root"`
	} `toml:"workspace"`
}

var machineNameSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)
var runnerHostnameLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func legacyDaemonProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}

func SanitizeMachineName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = machineNameSanitizer.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func MachineConfigPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ws", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ws", "config.toml"), nil
}

func EnsureLegacyDaemonStopped() error {
	pid, err := liveLegacyDaemonPID()
	if err != nil {
		return err
	}
	if pid == 0 {
		return nil
	}
	return fmt.Errorf("legacy ws daemon is still running (PID %d); run 'systemctl --user disable --now ws-daemon.service' or 'kill %d', then retry", pid, pid)
}

func liveLegacyDaemonPID() (int, error) {
	path, err := MachineConfigPath()
	if err != nil {
		return 0, err
	}
	pidPath := filepath.Join(filepath.Dir(path), "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading legacy daemon pid %s: %w", pidPath, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid legacy daemon pid in %s", pidPath)
	}
	if !legacyDaemonProcessAlive(pid) {
		return 0, nil
	}
	return pid, nil
}

func LoadMachineConfig() (*MachineConfig, error) {
	path, err := MachineConfigPath()
	if err != nil {
		return nil, err
	}
	var cfg MachineConfig
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := normalizeMachineConfig(&cfg); err != nil {
		return nil, err
	}
	if err := migrateLegacyDaemonConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func SaveMachineConfig(cfg *MachineConfig) error {
	cleaned := *cfg
	cleaned.WorkspaceRoots = append([]string(nil), cfg.WorkspaceRoots...)
	cleaned.Runners = append([]RunnerConfig(nil), cfg.Runners...)
	if err := normalizeMachineConfig(&cleaned); err != nil {
		return err
	}
	path, err := MachineConfigPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".config.toml-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := toml.NewEncoder(f).Encode(&cleaned); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func AddWorkspaceRoot(root string) (string, error) {
	cfg, err := LoadMachineConfig()
	if err != nil {
		return "", err
	}
	canonical, err := canonicalWorkspaceRoot(root)
	if err != nil {
		return "", err
	}
	for _, existing := range cfg.WorkspaceRoots {
		if existing == canonical {
			return canonical, nil
		}
	}
	cfg.WorkspaceRoots = append(cfg.WorkspaceRoots, canonical)
	if err := SaveMachineConfig(cfg); err != nil {
		return "", err
	}
	return canonical, nil
}

func RemoveWorkspaceRoot(root string) (string, error) {
	cfg, err := LoadMachineConfig()
	if err != nil {
		return "", err
	}
	canonical, err := canonicalWorkspaceRoot(root)
	if err != nil {
		return "", err
	}
	filtered := make([]string, 0, len(cfg.WorkspaceRoots))
	found := false
	for _, existing := range cfg.WorkspaceRoots {
		if existing == canonical {
			found = true
			continue
		}
		filtered = append(filtered, existing)
	}
	if !found {
		return "", fmt.Errorf("workspace %q is not registered", canonical)
	}
	cfg.WorkspaceRoots = filtered
	if err := SaveMachineConfig(cfg); err != nil {
		return "", err
	}
	return canonical, nil
}

func ListWorkspaceRoots() ([]string, error) {
	cfg, err := LoadMachineConfig()
	if err != nil {
		return nil, err
	}
	return append([]string(nil), cfg.WorkspaceRoots...), nil
}

func normalizeMachineConfig(cfg *MachineConfig) error {
	cfg.RunnerIDPrefix = strings.TrimSpace(cfg.RunnerIDPrefix)
	if cfg.RunnerIDPrefix != "" && !validRunnerID(cfg.RunnerIDPrefix) {
		return errors.New("runner_id_prefix must be a valid hostname")
	}
	if cfg.ExplorerView == "" {
		cfg.ExplorerView = ExplorerViewRecent
	}
	if cfg.ExplorerView == "language" {
		cfg.ExplorerView = ExplorerViewProjects
	}
	if cfg.ExplorerView != ExplorerViewProjects && cfg.ExplorerView != ExplorerViewRecent {
		return fmt.Errorf("invalid explorer_view %q", cfg.ExplorerView)
	}
	if cfg.RecentOrder == "" {
		cfg.RecentOrder = RecentOrderDesc
	}
	if cfg.RecentOrder != RecentOrderAsc && cfg.RecentOrder != RecentOrderDesc {
		return fmt.Errorf("invalid recent_order %q", cfg.RecentOrder)
	}
	roots := make([]string, 0, len(cfg.WorkspaceRoots))
	for _, root := range cfg.WorkspaceRoots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		canonical, err := canonicalWorkspaceRoot(root)
		if err != nil {
			return err
		}
		roots = append(roots, canonical)
	}
	sort.Strings(roots)
	cfg.WorkspaceRoots = roots[:dedupeSorted(roots)]
	return normalizeRunners(cfg)
}

func RunnerIDPrefix(cfg *MachineConfig) string {
	if cfg != nil && cfg.RunnerIDPrefix != "" {
		return cfg.RunnerIDPrefix
	}
	if cfg != nil && cfg.MachineName != "" {
		return cfg.MachineName
	}
	return DefaultMachineName()
}

func normalizeRunners(cfg *MachineConfig) error {
	ids := make(map[string]bool, len(cfg.Runners))
	targets := make(map[string]bool, len(cfg.Runners))
	for i := range cfg.Runners {
		runner := &cfg.Runners[i]
		runner.ID = strings.TrimSpace(runner.ID)
		runner.Workspace = strings.TrimSpace(runner.Workspace)
		runner.Group = strings.TrimSpace(runner.Group)
		runner.Project = strings.TrimSpace(runner.Project)
		runner.Worktree = strings.TrimSpace(runner.Worktree)
		runner.Path = strings.TrimSpace(runner.Path)
		if err := validateRunnerConfig(*runner); err != nil {
			return fmt.Errorf("runner %q: %w", runner.ID, err)
		}
		id := strings.ToLower(runner.ID)
		if ids[id] {
			return fmt.Errorf("duplicate runner id %q", runner.ID)
		}
		ids[id] = true
		target := runnerTargetKey(*runner)
		if targets[target] {
			return fmt.Errorf("runner %q duplicates another runner target", runner.ID)
		}
		targets[target] = true
	}
	sort.Slice(cfg.Runners, func(i, j int) bool {
		return strings.ToLower(cfg.Runners[i].ID) < strings.ToLower(cfg.Runners[j].ID)
	})
	return nil
}

func validateRunnerConfig(runner RunnerConfig) error {
	if !validRunnerID(runner.ID) {
		return errors.New("id must be a valid hostname")
	}
	if runner.Path != "" {
		if runner.Workspace != "" || runner.Group != "" || runner.Project != "" || runner.Worktree != "" {
			return errors.New("path cannot be combined with a workspace target")
		}
		return nil
	}
	if runner.Workspace == "" {
		return errors.New("workspace is required for group, project, and worktree targets")
	}
	if runner.Group != "" {
		if runner.Project != "" || runner.Worktree != "" {
			return errors.New("group cannot be combined with project or worktree")
		}
		return nil
	}
	if runner.Project == "" {
		return errors.New("group, project, or path is required")
	}
	return nil
}

func validRunnerID(id string) bool {
	if id == "" || len(id) > 253 {
		return false
	}
	for _, label := range strings.Split(id, ".") {
		if !runnerHostnameLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func runnerTargetKey(runner RunnerConfig) string {
	if runner.Path != "" {
		return "path\x00" + runner.Path
	}
	return strings.Join([]string{"workspace", runner.Workspace, runner.Group, runner.Project, runner.Worktree}, "\x00")
}

func canonicalWorkspaceRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	}
	return abs, nil
}

func dedupeSorted(values []string) int {
	if len(values) == 0 {
		return 0
	}
	n := 1
	for _, value := range values[1:] {
		if value == values[n-1] {
			continue
		}
		values[n] = value
		n++
	}
	return n
}

func migrateLegacyDaemonConfig(cfg *MachineConfig) error {
	path, err := MachineConfigPath()
	if err != nil {
		return err
	}
	legacyPath := filepath.Join(filepath.Dir(path), "daemon.toml")
	var legacy legacyDaemonConfig
	if _, err := toml.DecodeFile(legacyPath, &legacy); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("parsing %s: %w", legacyPath, err)
	}
	for _, workspace := range legacy.Workspaces {
		cfg.WorkspaceRoots = append(cfg.WorkspaceRoots, workspace.Root)
	}
	if err := normalizeMachineConfig(cfg); err != nil {
		return err
	}
	if err := SaveMachineConfig(cfg); err != nil {
		return err
	}
	return removeLegacyDaemonConfig(legacyPath)
}

func removeLegacyDaemonConfig(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func DefaultMachineName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	s := SanitizeMachineName(h)
	if s == "" {
		return "unknown"
	}
	return s
}
