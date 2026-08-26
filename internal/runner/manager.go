package runner

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
)

type Status string

const (
	StatusRunning  Status = "running"
	StatusStopped  Status = "stopped"
	StatusOccupied Status = "unmanaged"
	StatusMissing  Status = "missing"
)

type Info struct {
	Definition config.RunnerConfig
	Status     Status
	Path       string
	PID        int
	StartTime  uint64
	Detail     string
}

func List() ([]Info, error) {
	machine, err := config.LoadMachineConfig()
	if err != nil {
		return nil, err
	}
	processes, err := discoverAmpProcesses()
	if err != nil {
		return nil, err
	}
	infos := make([]Info, 0, len(machine.Runners))
	managed := make(map[int]bool, len(machine.Runners))
	for _, def := range machine.Runners {
		info := inspect(def, processes)
		infos = append(infos, info)
		if info.PID > 0 {
			managed[info.PID] = true
		}
	}
	for _, process := range processes {
		if !managed[process.PID] {
			infos = append(infos, Info{Status: StatusOccupied, Path: process.Cwd, PID: process.PID, StartTime: process.StartTime, Detail: "unmanaged Amp runner"})
		}
	}
	sort.SliceStable(infos, func(i, j int) bool {
		left, right := infos[i].Definition.ID, infos[j].Definition.ID
		if left == "" {
			left = infos[i].Path
		}
		if right == "" {
			right = infos[j].Path
		}
		return left < right
	})
	return infos, nil
}

func Inspect(def config.RunnerConfig) Info {
	processes, err := discoverAmpProcesses()
	if err != nil {
		return Info{Definition: def, Status: StatusStopped, Detail: err.Error()}
	}
	return inspect(def, processes)
}

func inspect(def config.RunnerConfig, processes []processInfo) Info {
	info := Info{Definition: def, Status: StatusStopped}
	state, stateErr := loadState(def.ID)
	if stateErr == nil {
		for _, process := range processes {
			if process.PID == state.PID && process.StartTime == state.StartTime && (process.Cwd == "" || process.Cwd == state.Cwd) {
				info.Status, info.Path, info.PID, info.StartTime = StatusRunning, state.Cwd, process.PID, process.StartTime
				return info
			}
		}
	}
	path, err := Resolve(def)
	if err != nil {
		info.Status, info.Detail = StatusMissing, err.Error()
		return info
	}
	info.Path = path
	for _, process := range processes {
		if process.Cwd == path {
			info.Status, info.PID, info.StartTime = StatusOccupied, process.PID, process.StartTime
			info.Detail = "directory is occupied by an unmanaged Amp runner"
			return info
		}
	}
	return info
}

func ShutdownExternal(info Info, force bool) error {
	if info.Status != StatusOccupied || info.PID <= 0 || info.StartTime == 0 {
		return errors.New("external runner identity is unavailable")
	}
	processes, err := discoverAmpProcesses()
	if err != nil {
		return err
	}
	for _, process := range processes {
		if process.PID == info.PID && process.StartTime == info.StartTime && process.Cwd == info.Path {
			return stopExternalProcess(runtimeState{PID: info.PID, StartTime: info.StartTime, Cwd: info.Path}, force)
		}
	}
	return errors.New("external runner changed before replacement")
}

func Start(def config.RunnerConfig) (Info, error) {
	info := Inspect(def)
	switch info.Status {
	case StatusRunning:
		return info, nil
	case StatusOccupied:
		return info, errors.New(info.Detail)
	case StatusMissing:
		return info, errors.New(info.Detail)
	}
	if err := startProcess(def, info.Path); err != nil {
		return Inspect(def), err
	}
	started := Inspect(def)
	if started.Status != StatusRunning {
		if started.Detail != "" {
			return started, errors.New(started.Detail)
		}
		return started, errors.New("amp runner exited during startup")
	}
	return started, nil
}

func Shutdown(def config.RunnerConfig, force bool) error {
	info := Inspect(def)
	switch info.Status {
	case StatusStopped, StatusMissing:
		return removeState(def.ID)
	case StatusOccupied:
		return errors.New("refusing to stop an unmanaged Amp runner")
	}
	state, err := loadState(def.ID)
	if err != nil {
		return err
	}
	if err := stopProcess(state, force); err != nil {
		return err
	}
	return removeState(def.ID)
}

func Restart(def config.RunnerConfig, force bool) (Info, error) {
	if err := Shutdown(def, force); err != nil {
		return Inspect(def), err
	}
	return Start(def)
}

func Definition(id string) (config.RunnerConfig, error) {
	machine, err := config.LoadMachineConfig()
	if err != nil {
		return config.RunnerConfig{}, err
	}
	for _, def := range machine.Runners {
		if equalID(def.ID, id) {
			return def, nil
		}
	}
	return config.RunnerConfig{}, fmt.Errorf("runner %q is not configured", id)
}

func equalID(a, b string) bool {
	return strings.EqualFold(a, b)
}

func IsUnsupported(err error) bool {
	return errors.Is(err, os.ErrInvalid)
}
