package metrics

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const schemaVersion = 1

type Counters struct {
	Version  int             `json:"version"`
	Commands CommandFamilies `json:"commands"`
	Events   Events          `json:"events"`
}

type CommandFamilies struct {
	Explorer  CommandCounters `json:"explorer"`
	Add       CommandCounters `json:"add"`
	Alias     CommandCounters `json:"alias"`
	Doctor    CommandCounters `json:"doctor"`
	Sync      CommandCounters `json:"sync"`
	Worktree  CommandCounters `json:"worktree"`
	Workspace CommandCounters `json:"workspace"`
	Setup     CommandCounters `json:"setup"`
	Create    CommandCounters `json:"create"`
	Bootstrap CommandCounters `json:"bootstrap"`
	Migrate   CommandCounters `json:"migrate"`
	Status    CommandCounters `json:"status"`
	Scan      CommandCounters `json:"scan"`
	Path      CommandCounters `json:"path"`
	Favorite  CommandCounters `json:"favorite"`
	Auth      CommandCounters `json:"auth"`
	Docs      CommandCounters `json:"docs"`
	Other     CommandCounters `json:"other"`
}

type CommandCounters struct {
	Invoked  uint64           `json:"invoked"`
	TTY      uint64           `json:"tty"`
	Headless uint64           `json:"headless"`
	Success  uint64           `json:"success"`
	Failure  uint64           `json:"failure"`
	Canceled uint64           `json:"canceled"`
	Duration DurationCounters `json:"duration"`
}

type DurationCounters struct {
	Under100ms uint64 `json:"under_100ms"`
	Under1s    uint64 `json:"under_1s"`
	Under10s   uint64 `json:"under_10s"`
	AtLeast10s uint64 `json:"at_least_10s"`
}

type Events struct {
	ExplorerInvoked         uint64 `json:"explorer_invoked"`
	ExplorerShellOpened     uint64 `json:"explorer_shell_opened"`
	ExplorerWorktreeCreated uint64 `json:"explorer_worktree_created"`
	ExplorerFavoriteChanged uint64 `json:"explorer_favorite_changed"`
	AddInvoked              uint64 `json:"add_invoked"`
	AddProjectsRegistered   uint64 `json:"add_projects_registered"`
	AliasManaged            uint64 `json:"alias_managed"`
	AliasStateGenerated     uint64 `json:"alias_state_generated"`
	DoctorInvoked           uint64 `json:"doctor_invoked"`
	DoctorActionableFound   uint64 `json:"doctor_actionable_found"`
	DoctorFixApplied        uint64 `json:"doctor_fix_applied"`
}

type Outcome uint8

const (
	Success Outcome = iota
	Failure
	Canceled
)

func RecordCommand(path string, tty bool, outcome Outcome, duration time.Duration) {
	update(func(c *Counters) {
		command := family(c, path)
		increment(&command.Invoked, 1)
		if tty {
			increment(&command.TTY, 1)
		} else {
			increment(&command.Headless, 1)
		}
		switch outcome {
		case Success:
			increment(&command.Success, 1)
		case Canceled:
			increment(&command.Canceled, 1)
		default:
			increment(&command.Failure, 1)
		}
		switch {
		case duration < 100*time.Millisecond:
			increment(&command.Duration.Under100ms, 1)
		case duration < time.Second:
			increment(&command.Duration.Under1s, 1)
		case duration < 10*time.Second:
			increment(&command.Duration.Under10s, 1)
		default:
			increment(&command.Duration.AtLeast10s, 1)
		}
	})
}

func RecordExplorerInvoked()     { event(func(e *Events) *uint64 { return &e.ExplorerInvoked }, 1) }
func RecordExplorerShellOpened() { event(func(e *Events) *uint64 { return &e.ExplorerShellOpened }, 1) }
func RecordExplorerWorktreeCreated() {
	event(func(e *Events) *uint64 { return &e.ExplorerWorktreeCreated }, 1)
}
func RecordExplorerFavoriteChanged() {
	event(func(e *Events) *uint64 { return &e.ExplorerFavoriteChanged }, 1)
}
func RecordAddInvoked() { event(func(e *Events) *uint64 { return &e.AddInvoked }, 1) }
func RecordAddProjectsRegistered(count int) {
	event(func(e *Events) *uint64 { return &e.AddProjectsRegistered }, uint64(max(count, 0)))
}
func RecordAliasManaged()        { event(func(e *Events) *uint64 { return &e.AliasManaged }, 1) }
func RecordAliasStateGenerated() { event(func(e *Events) *uint64 { return &e.AliasStateGenerated }, 1) }
func RecordDoctorInvoked()       { event(func(e *Events) *uint64 { return &e.DoctorInvoked }, 1) }
func RecordDoctorActionableFound(count int) {
	event(func(e *Events) *uint64 { return &e.DoctorActionableFound }, uint64(max(count, 0)))
}
func RecordDoctorFixApplied(count int) {
	event(func(e *Events) *uint64 { return &e.DoctorFixApplied }, uint64(max(count, 0)))
}

func Read() (Counters, error) {
	path, err := statePath()
	if err != nil {
		return Counters{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Counters{}, err
	}
	return decode(data), nil
}

func event(selectCounter func(*Events) *uint64, amount uint64) {
	if amount == 0 {
		return
	}
	update(func(c *Counters) { increment(selectCounter(&c.Events), amount) })
}

func update(change func(*Counters)) {
	path, err := statePath()
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	counters := Counters{Version: schemaVersion}
	if data, readErr := os.ReadFile(path); readErr == nil {
		counters = decode(data)
	}
	change(&counters)
	write(path, counters)
}

func decode(data []byte) Counters {
	var counters Counters
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&counters) != nil || counters.Version != schemaVersion {
		return Counters{Version: schemaVersion}
	}
	return counters
}

func write(path string, counters Counters) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".metrics-*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	_ = tmp.Chmod(0o600)
	err = json.NewEncoder(tmp).Encode(counters)
	closeErr := tmp.Close()
	if err == nil && closeErr == nil {
		_ = os.Rename(tmpName, path)
	}
}

func statePath() (string, error) {
	if state := os.Getenv("XDG_STATE_HOME"); state != "" {
		return filepath.Join(state, "ws", "metrics.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ws", "metrics.json"), nil
}

func family(c *Counters, path string) *CommandCounters {
	byName := map[string]*CommandCounters{
		"explorer":  &c.Commands.Explorer,
		"add":       &c.Commands.Add,
		"alias":     &c.Commands.Alias,
		"doctor":    &c.Commands.Doctor,
		"sync":      &c.Commands.Sync,
		"worktree":  &c.Commands.Worktree,
		"workspace": &c.Commands.Workspace,
		"setup":     &c.Commands.Setup,
		"create":    &c.Commands.Create,
		"bootstrap": &c.Commands.Bootstrap,
		"migrate":   &c.Commands.Migrate,
		"status":    &c.Commands.Status,
		"scan":      &c.Commands.Scan,
		"path":      &c.Commands.Path,
		"favorite":  &c.Commands.Favorite,
		"auth":      &c.Commands.Auth,
		"docs":      &c.Commands.Docs,
	}
	parts := strings.Fields(path)
	if len(parts) == 1 && parts[0] == "ws" {
		return byName["explorer"]
	}
	if len(parts) > 1 && parts[0] == "ws" {
		if command := byName[parts[1]]; command != nil {
			return command
		}
	}
	return &c.Commands.Other
}

func increment(counter *uint64, amount uint64) {
	if math.MaxUint64-*counter < amount {
		*counter = math.MaxUint64
		return
	}
	*counter += amount
}
