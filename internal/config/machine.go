package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

type MachineConfig struct {
	MachineName string `toml:"machine_name"`
}

var machineNameSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

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
