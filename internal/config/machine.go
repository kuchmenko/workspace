package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// MachineConfig holds per-machine settings stored in ~/.config/ws/config.toml.
// This is intentionally separate from the workspace TOML so that machine
// identity travels with the user, not with any particular workspace clone.
type MachineConfig struct {
	// MachineName is used as the <machine> segment in the wt/<machine>/<topic>
	// branch convention. It must be a short, stable, filesystem-safe identifier
	// (lowercase letters, digits, and dashes).
	MachineName string `toml:"machine_name"`
}

var machineNameSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

// SanitizeMachineName lowercases and replaces unsafe characters with dashes
// so the result is safe to embed in branch names and filesystem paths.
func SanitizeMachineName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = machineNameSanitizer.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// MachineConfigPath returns the canonical location of the machine config file.
// Honors $XDG_CONFIG_HOME, falls back to ~/.config/ws/config.toml.
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

// LoadMachineConfig reads the machine config from disk. Returns an empty
// (zero-valued) config without error if the file does not exist — callers
// are expected to prompt the user and call SaveMachineConfig in that case.
func LoadMachineConfig() (*MachineConfig, error) {
	path, err := MachineConfigPath()
	if err != nil {
		return nil, err
	}
	var cfg MachineConfig
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &cfg, nil
	} else if err != nil {
		return nil, err
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

// SaveMachineConfig writes the machine config, creating parent dirs as needed.
func SaveMachineConfig(cfg *MachineConfig) error {
	path, err := MachineConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// DefaultMachineName produces a fallback machine name from os.Hostname().
// The result is sanitized but still may need user confirmation — hostnames
// like "ivans-MacBook-Pro.local" are technically valid but ugly in git history.
func DefaultMachineName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	// Strip common .local suffix and trailing domain pieces.
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	s := SanitizeMachineName(h)
	if s == "" {
		return "unknown"
	}
	return s
}
