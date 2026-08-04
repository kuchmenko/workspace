package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFixedSchemaDoesNotPersistInput(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	secret := "project-name https://user:token@example.com/repo feat/private --filter=private"
	RecordCommand("ws "+secret, false, Failure, time.Second)
	RecordAddProjectsRegistered(3)

	data, err := os.ReadFile(filepath.Join(state, "ws", "metrics.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "token") || strings.Contains(string(data), "private") {
		t.Fatalf("metrics persisted input: %s", data)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 || fields["version"] != float64(schemaVersion) || fields["commands"] == nil || fields["events"] == nil {
		t.Fatalf("unexpected top-level schema: %#v", fields)
	}
}

func TestMalformedStateIsReset(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	path := filepath.Join(state, "ws", "metrics.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"project":"private","unknown":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	RecordAddInvoked()
	counters, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if counters.Events.AddInvoked != 1 {
		t.Fatalf("add invoked = %d, want 1", counters.Events.AddInvoked)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "unknown") {
		t.Fatalf("malformed fields retained: %s", data)
	}
}

func TestCommandClassification(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	RecordCommand("ws sync resolve", true, Canceled, 99*time.Millisecond)
	RecordCommand("ws add", false, Success, 100*time.Millisecond)
	RecordCommand("ws doctor", false, Failure, time.Second)
	RecordCommand("ws status", true, Success, 10*time.Second)

	counters, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if counters.Commands.Sync.Invoked != 1 || counters.Commands.Sync.TTY != 1 || counters.Commands.Sync.Canceled != 1 || counters.Commands.Sync.Duration.Under100ms != 1 {
		t.Fatalf("sync classification = %+v", counters.Commands.Sync)
	}
	if counters.Commands.Add.Headless != 1 || counters.Commands.Add.Success != 1 || counters.Commands.Add.Duration.Under1s != 1 {
		t.Fatalf("add classification = %+v", counters.Commands.Add)
	}
	if counters.Commands.Doctor.Failure != 1 || counters.Commands.Doctor.Duration.Under10s != 1 {
		t.Fatalf("doctor classification = %+v", counters.Commands.Doctor)
	}
	if counters.Commands.Status.TTY != 1 || counters.Commands.Status.Duration.AtLeast10s != 1 {
		t.Fatalf("status classification = %+v", counters.Commands.Status)
	}
}

func TestEventCounters(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	RecordExplorerInvoked()
	RecordExplorerShellOpened()
	RecordExplorerWorktreeCreated()
	RecordExplorerFavoriteChanged()
	RecordAddInvoked()
	RecordAddProjectsRegistered(2)
	RecordAliasManaged()
	RecordAliasStateGenerated()
	RecordDoctorInvoked()
	RecordDoctorActionableFound(3)
	RecordDoctorFixApplied(2)

	counters, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	want := Events{1, 1, 1, 1, 1, 2, 1, 1, 1, 3, 2}
	if counters.Events != want {
		t.Fatalf("events = %+v, want %+v", counters.Events, want)
	}
}
