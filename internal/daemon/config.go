package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/kuchmenko/workspace/internal/auth"
)

type WorkspaceEntry struct {
	Root         string `toml:"root"`
	AutoSync     bool   `toml:"auto_sync"`
	PollInterval string `toml:"poll_interval,omitempty"`
	// AutoBootstrap controls whether the daemon clones missing projects on
	// each tick. Pointer so we can distinguish "unset" (default true) from
	// an explicit false written to daemon.toml.
	AutoBootstrap *bool `toml:"auto_bootstrap,omitempty"`
	// PushCooldown coalesces consecutive auto-sync commits of workspace.toml
	// into a single squashed commit. Empty string → default 1h; "0" disables
	// coalescing (legacy behaviour: push every commit immediately). Parsed
	// via time.ParseDuration; unparseable values fall back to the default.
	PushCooldown string `toml:"push_cooldown,omitempty"`
}

// DefaultPushCooldown is the daemon's default coalescing window when the
// workspace entry does not override it. One hour keeps git history almost
// free of auto-sync noise on machines that edit workspace.toml frequently,
// while still bounding the worst-case "my change isn't on origin yet" gap.
const DefaultPushCooldown = time.Hour

// ResolvedPushCooldown returns the duration parsed from PushCooldown, with
// DefaultPushCooldown as the fallback for empty/invalid values. The literal
// "0" (zero duration) is honoured as "disable coalescing".
func (w WorkspaceEntry) ResolvedPushCooldown() time.Duration {
	if w.PushCooldown == "" {
		return DefaultPushCooldown
	}
	d, err := time.ParseDuration(w.PushCooldown)
	if err != nil {
		return DefaultPushCooldown
	}
	return d
}

// AutoBootstrapEnabled reports whether auto-clone of missing projects is on.
// Defaults to true when the field is unset.
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
