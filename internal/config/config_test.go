package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// writeWS writes contents to <dir>/workspace.toml, returning the directory.
func writeWS(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "workspace.toml"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write workspace.toml: %v", err)
	}
	return dir
}

// readWS returns the bytes currently on disk at <dir>/workspace.toml.
func readWS(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "workspace.toml"))
	if err != nil {
		t.Fatalf("read workspace.toml: %v", err)
	}
	return string(b)
}

func TestLoad_MigratesLegacyOwned(t *testing.T) {
	const legacy = `
[meta]
version = 1
root = "/ws"

[daemon]
poll_interval = "5m"
stale_threshold = "30d"
auto_sync = true
watch_dirs = true

[projects.app]
remote = "git@github.com:me/app.git"
path = "personal/app"
status = "active"
category = "personal"
default_branch = "main"

[[projects.app.autopush.owned]]
branch = "feat/foo"
machine = "linux"
since = "2026-04-08T13:59:04Z"

[[projects.app.autopush.owned]]
branch = "fix/bar"
machine = "archlinux"
since = "2026-04-09T10:00:00Z"
`
	dir := writeWS(t, legacy)
	ws, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	app, ok := ws.Projects["app"]
	if !ok {
		t.Fatal("project app missing")
	}
	if app.LegacyAutopush != nil {
		t.Errorf("LegacyAutopush should be nil after migration, got %+v", app.LegacyAutopush)
	}
	if len(app.Branches) != 2 {
		t.Fatalf("want 2 migrated branches, got %d: %+v", len(app.Branches), app.Branches)
	}
	// Sort by name for stable comparison.
	sort.Slice(app.Branches, func(i, j int) bool { return app.Branches[i].Name < app.Branches[j].Name })
	want := []BranchMeta{
		{
			Name:              "feat/foo",
			Machines:          []string{"linux"},
			LastActiveMachine: "linux",
			LastActiveAt:      "2026-04-08T13:59:04Z",
			CreatedBy:         "linux",
			CreatedAt:         "2026-04-08T13:59:04Z",
		},
		{
			Name:              "fix/bar",
			Machines:          []string{"archlinux"},
			LastActiveMachine: "archlinux",
			LastActiveAt:      "2026-04-09T10:00:00Z",
			CreatedBy:         "archlinux",
			CreatedAt:         "2026-04-09T10:00:00Z",
		},
	}
	if !reflect.DeepEqual(app.Branches, want) {
		t.Errorf("mismatched migration\n got: %+v\nwant: %+v", app.Branches, want)
	}
}

func TestLoad_MigratesLegacyBranchesStringList_GCdOnSave(t *testing.T) {
	const legacy = `
[meta]
version = 1
root = "/ws"

[daemon]
poll_interval = "5m"
stale_threshold = "30d"
auto_sync = true
watch_dirs = true

[projects.app]
remote = "git@github.com:me/app.git"
path = "personal/app"
status = "active"
category = "personal"

[projects.app.autopush]
branches = ["chore/legacy-1", "chore/legacy-2"]
`
	dir := writeWS(t, legacy)
	ws, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	app := ws.Projects["app"]
	if len(app.Branches) != 2 {
		t.Fatalf("want 2 entries post-migration, got %d", len(app.Branches))
	}
	// Both have empty Machines.
	for _, b := range app.Branches {
		if len(b.Machines) != 0 {
			t.Errorf("branch %q: want empty machines, got %v", b.Name, b.Machines)
		}
	}

	// Save → Load round-trip drops them entirely (GC).
	if err := Save(dir, ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ws2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load #2: %v", err)
	}
	if len(ws2.Projects["app"].Branches) != 0 {
		t.Errorf("legacy string branches should be GC'd on Save, got %+v", ws2.Projects["app"].Branches)
	}
	// And the legacy autopush block must be gone from disk too.
	body := readWS(t, dir)
	if strings.Contains(body, "autopush") {
		t.Errorf("on-disk file still references autopush:\n%s", body)
	}
}

func TestLoad_IsIdempotent(t *testing.T) {
	const legacy = `
[meta]
version = 1
root = "/ws"
[daemon]
poll_interval = "5m"
stale_threshold = "30d"
auto_sync = true
watch_dirs = true

[projects.app]
remote = "git@github.com:me/app.git"
path = "personal/app"
status = "active"
category = "personal"

[[projects.app.autopush.owned]]
branch = "feat/keeper"
machine = "linux"
since = "2026-04-08T13:59:04Z"
`
	dir := writeWS(t, legacy)
	ws1, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Save(dir, ws1); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ws2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load #2: %v", err)
	}
	if err := Save(dir, ws2); err != nil {
		t.Fatalf("Save #2: %v", err)
	}
	ws3, err := Load(dir)
	if err != nil {
		t.Fatalf("Load #3: %v", err)
	}
	if !reflect.DeepEqual(ws2.Projects["app"].Branches, ws3.Projects["app"].Branches) {
		t.Errorf("Load is not idempotent across two save cycles")
	}
}

func TestSave_DropsEmptyMachinesEntries(t *testing.T) {
	dir := t.TempDir()
	ws := &Workspace{
		Meta:     Meta{Version: 1, Root: dir},
		Daemon:   Daemon{PollInterval: "5m", StaleThreshold: "30d", AutoSync: true, WatchDirs: true},
		Groups:   map[string]Group{},
		Aliases:  map[string]string{},
		Projects: map[string]Project{
			"app": {
				Remote:   "git@github.com:me/app.git",
				Path:     "personal/app",
				Status:   StatusActive,
				Category: CategoryPersonal,
				Branches: []BranchMeta{
					{Name: "feat/keep", Machines: []string{"linux"}, CreatedBy: "linux"},
					{Name: "chore/drop"},
					{Name: "feat/keep-2", Machines: []string{"archlinux"}, CreatedBy: "archlinux"},
				},
			},
		},
	}
	if err := Save(dir, ws); err != nil {
		t.Fatalf("Save: %v", err)
	}
	body := readWS(t, dir)
	if strings.Contains(body, "chore/drop") {
		t.Errorf("empty-machines entry chore/drop should not be in saved file:\n%s", body)
	}
	// In-memory must NOT have been mutated by Save.
	if len(ws.Projects["app"].Branches) != 3 {
		t.Errorf("Save mutated in-memory state; expected 3 entries, got %d", len(ws.Projects["app"].Branches))
	}

	// Re-load: only the kept entries should be present.
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := loaded.Projects["app"].Branches
	if len(got) != 2 {
		t.Fatalf("want 2 entries on disk, got %d: %+v", len(got), got)
	}
}

func TestClaimBranch_FirstClaim(t *testing.T) {
	p := Project{}
	changed, isNew := p.ClaimBranch("feat/foo", "linux")
	if !changed || !isNew {
		t.Errorf("first claim: want changed=true isNew=true, got changed=%v isNew=%v", changed, isNew)
	}
	if len(p.Branches) != 1 {
		t.Fatalf("want 1 branch, got %d", len(p.Branches))
	}
	b := p.Branches[0]
	if b.Name != "feat/foo" || b.CreatedBy != "linux" || b.LastActiveMachine != "linux" {
		t.Errorf("first claim sets origin + active: %+v", b)
	}
	if !reflect.DeepEqual(b.Machines, []string{"linux"}) {
		t.Errorf("machines: want [linux], got %v", b.Machines)
	}
}

func TestClaimBranch_SecondMachine_AppendsAndDoesNotOverwriteOrigin(t *testing.T) {
	p := Project{}
	p.ClaimBranch("feat/foo", "linux")
	original := p.Branches[0].CreatedAt
	time.Sleep(time.Millisecond) // ensure RFC3339-second resolution may tick
	changed, isNew := p.ClaimBranch("feat/foo", "archlinux")
	if !changed || isNew {
		t.Errorf("second claim: want changed=true isNew=false, got changed=%v isNew=%v", changed, isNew)
	}
	b := p.Branches[0]
	if b.CreatedBy != "linux" {
		t.Errorf("CreatedBy must not change on second claim: got %q", b.CreatedBy)
	}
	if b.CreatedAt != original {
		t.Errorf("CreatedAt must not change on second claim: was %q now %q", original, b.CreatedAt)
	}
	if b.LastActiveMachine != "archlinux" {
		t.Errorf("LastActiveMachine: want archlinux, got %q", b.LastActiveMachine)
	}
	if !reflect.DeepEqual(b.Machines, []string{"archlinux", "linux"}) {
		t.Errorf("machines should be sorted/deduped, got %v", b.Machines)
	}
}

func TestClaimBranch_RepeatSameMachine_NoOpOnMachines(t *testing.T) {
	p := Project{}
	p.ClaimBranch("feat/foo", "linux")
	first := p.Branches[0].Machines
	changed, _ := p.ClaimBranch("feat/foo", "linux")
	// `changed=true` because LastActiveAt may bump; what matters is Machines stays the same.
	_ = changed
	if !reflect.DeepEqual(p.Branches[0].Machines, first) {
		t.Errorf("re-claim by same machine should not duplicate machines slice, got %v", p.Branches[0].Machines)
	}
}

func TestReleaseBranch_DropsMachine_KeepsEntryWhenOthersRemain(t *testing.T) {
	p := Project{}
	p.ClaimBranch("feat/foo", "linux")
	p.ClaimBranch("feat/foo", "archlinux")
	changed, removed := p.ReleaseBranch("feat/foo", "archlinux")
	if !changed || removed {
		t.Errorf("release one of two: want changed=true removed=false, got changed=%v removed=%v", changed, removed)
	}
	if !reflect.DeepEqual(p.Branches[0].Machines, []string{"linux"}) {
		t.Errorf("machines after release: want [linux], got %v", p.Branches[0].Machines)
	}
	// LastActiveMachine was archlinux; releasing it should clear the field.
	if p.Branches[0].LastActiveMachine != "" {
		t.Errorf("LastActiveMachine should clear when active machine is released, got %q", p.Branches[0].LastActiveMachine)
	}
}

func TestReleaseBranch_DropsEntryWhenLastMachine(t *testing.T) {
	p := Project{}
	p.ClaimBranch("feat/foo", "linux")
	changed, removed := p.ReleaseBranch("feat/foo", "linux")
	if !changed || !removed {
		t.Errorf("release last machine: want changed=true removed=true, got changed=%v removed=%v", changed, removed)
	}
	if len(p.Branches) != 0 {
		t.Errorf("entry should be gone, got %+v", p.Branches)
	}
}

func TestReleaseBranch_UnknownBranch_NoOp(t *testing.T) {
	p := Project{}
	changed, removed := p.ReleaseBranch("ghost", "linux")
	if changed || removed {
		t.Errorf("unknown branch: want changed=false removed=false, got changed=%v removed=%v", changed, removed)
	}
}

func TestTouchActive_BumpsTimestamp(t *testing.T) {
	p := Project{}
	p.ClaimBranch("feat/foo", "linux")
	when := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	if !p.TouchActive("feat/foo", "archlinux", when) {
		t.Error("TouchActive should report changed")
	}
	b := p.LookupBranch("feat/foo")
	if b.LastActiveMachine != "archlinux" || b.LastActiveAt != "2026-05-08T12:00:00Z" {
		t.Errorf("touch did not stick: %+v", b)
	}
	// CreatedBy must not move.
	if b.CreatedBy != "linux" {
		t.Errorf("TouchActive must never overwrite CreatedBy, got %q", b.CreatedBy)
	}
}

func TestTouchActive_UnknownBranch_NoOp(t *testing.T) {
	p := Project{}
	if p.TouchActive("ghost", "linux", time.Now()) {
		t.Error("TouchActive on unknown branch should be no-op")
	}
}

func TestValidate_DetectsDuplicateBranchNames(t *testing.T) {
	ws := &Workspace{
		Projects: map[string]Project{
			"app": {
				Branches: []BranchMeta{
					{Name: "feat/foo", Machines: []string{"linux"}},
					{Name: "feat/bar", Machines: []string{"archlinux"}},
					{Name: "feat/foo", Machines: []string{"archlinux"}}, // dup
				},
			},
			"lib": {
				Branches: []BranchMeta{
					{Name: "feat/x", Machines: []string{"linux"}},
				},
			},
		},
	}
	issues := ws.Validate()
	if len(issues) != 1 {
		t.Fatalf("want 1 duplicate issue, got %d: %+v", len(issues), issues)
	}
	if issues[0].Project != "app" || issues[0].Branch != "feat/foo" || issues[0].Kind != ValidationDuplicateBranch {
		t.Errorf("unexpected issue: %+v", issues[0])
	}
}

func TestValidate_NoDuplicates_ReturnsEmpty(t *testing.T) {
	ws := &Workspace{
		Projects: map[string]Project{
			"app": {Branches: []BranchMeta{
				{Name: "a", Machines: []string{"linux"}},
				{Name: "b", Machines: []string{"linux"}},
			}},
		},
	}
	if got := ws.Validate(); len(got) != 0 {
		t.Errorf("want empty, got %+v", got)
	}
}

func TestSyncEnabled_DefaultsTrue(t *testing.T) {
	if !((Project{}).SyncEnabled()) {
		t.Error("default SyncEnabled should be true")
	}
	f := false
	if (Project{AutoSync: &f}).SyncEnabled() {
		t.Error("AutoSync=false should disable sync")
	}
}
