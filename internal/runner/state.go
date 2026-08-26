package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
)

type runtimeState struct {
	ID        string `json:"id"`
	PID       int    `json:"pid"`
	StartTime uint64 `json:"start_time"`
	Cwd       string `json:"cwd"`
}

func stateRoot() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "ws", "runners"), nil
}

func statePath(id string) (string, error) {
	root, err := stateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, strings.ToLower(id)+".json"), nil
}

func LogPath(id string) (string, error) {
	root, err := stateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, strings.ToLower(id)+".log"), nil
}

func loadState(id string) (runtimeState, error) {
	path, err := statePath(id)
	if err != nil {
		return runtimeState{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return runtimeState{}, err
	}
	var state runtimeState
	if err := json.Unmarshal(data, &state); err != nil {
		return runtimeState{}, fmt.Errorf("parse runner state: %w", err)
	}
	return state, nil
}

func saveState(state runtimeState) error {
	path, err := statePath(state.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".runner-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func removeState(id string) error {
	path, err := statePath(id)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func SaveDefinition(def config.RunnerConfig) error {
	machine, err := config.LoadMachineConfig()
	if err != nil {
		return err
	}
	if _, found := FindByTarget(machine.Runners, def); found {
		return errors.New("runner target is already configured")
	}
	machine.Runners = append(machine.Runners, def)
	return config.SaveMachineConfig(machine)
}

func RemoveDefinition(id string) error {
	machine, err := config.LoadMachineConfig()
	if err != nil {
		return err
	}
	filtered := make([]config.RunnerConfig, 0, len(machine.Runners))
	found := false
	for _, def := range machine.Runners {
		if strings.EqualFold(def.ID, id) {
			found = true
			continue
		}
		filtered = append(filtered, def)
	}
	if !found {
		return fmt.Errorf("runner %q is not configured", id)
	}
	machine.Runners = filtered
	if err := config.SaveMachineConfig(machine); err != nil {
		return err
	}
	return removeState(id)
}

func RenameDefinition(oldID, newID string) error {
	machine, err := config.LoadMachineConfig()
	if err != nil {
		return err
	}
	index := -1
	for i, def := range machine.Runners {
		if strings.EqualFold(def.ID, oldID) {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("runner %q is not configured", oldID)
	}
	status := Inspect(machine.Runners[index]).Status
	if status != StatusStopped && status != StatusMissing {
		return errors.New("runner must be stopped before editing its ID")
	}
	machine.Runners[index].ID = newID
	if err := config.SaveMachineConfig(machine); err != nil {
		return err
	}
	if !strings.EqualFold(oldID, newID) {
		return removeState(oldID)
	}
	return nil
}
