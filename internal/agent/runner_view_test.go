package agent

import (
	"strings"
	"testing"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/runner"
	"github.com/kuchmenko/workspace/internal/tui"
)

func TestRunnerTargetsFollowExplorerHierarchy(t *testing.T) {
	project := &Project{ID: "workspace", WorkspaceRoot: "/dev", Path: "/dev/personal/workspace"}
	m := &Model{workspaces: []WorkspaceData{{Name: "shared", Root: "/dev"}}}
	group := m.groupRunnerTarget("/dev", "personal")
	if group.Workspace != "shared" || group.Group != "personal" {
		t.Fatalf("group target = %#v", group)
	}
	main := m.worktreeRunnerTarget(project, &Worktree{Path: project.Path, IsMain: true})
	if main.Workspace != "shared" || main.Project != "workspace" || main.Worktree != "" {
		t.Fatalf("main target = %#v", main)
	}
	feature := m.worktreeRunnerTarget(project, &Worktree{Path: "/dev/workspace-feature", Branch: "feat/runner"})
	if feature.Worktree != "feat/runner" {
		t.Fatalf("feature target = %#v", feature)
	}
}

func TestContextualRunnerCommandsReflectState(t *testing.T) {
	target := config.RunnerConfig{Workspace: "shared", Project: "workspace"}
	m := &Model{}
	commands := m.runnerCommands("PROJECT", target, "/dev/workspace")
	if !hasPaletteAction(commands, "runner-create") {
		t.Fatalf("stopped unconfigured target actions = %#v", commands)
	}
	m.runnerInfos = []runner.Info{{Definition: config.RunnerConfig{ID: "arch-workspace", Workspace: "shared", Project: "workspace"}, Status: runner.StatusRunning, Path: "/dev/workspace", PID: 42}}
	commands = m.runnerCommands("PROJECT", target, "/dev/workspace")
	if !hasPaletteAction(commands, "runner-restart") || !hasPaletteAction(commands, "runner-shutdown") {
		t.Fatalf("running target actions = %#v", commands)
	}
}

func TestUnmanagedRunnerIsReadOnly(t *testing.T) {
	for _, info := range []runner.Info{
		{Status: runner.StatusOccupied, Path: "/dev/personal", PID: 42},
		{Definition: config.RunnerConfig{ID: "arch-personal", Path: "/dev/personal"}, Status: runner.StatusOccupied, Path: "/dev/personal", PID: 42},
	} {
		m := &Model{mode: viewRunners, runnerInfos: []runner.Info{info}}
		m.confirmSelectedRunner("shutdown", false)
		if m.mode != viewRunners || m.runnerConfirm != nil || m.statusMsg != "unmanaged runners are read-only" {
			t.Fatalf("unmanaged action changed state: mode=%v confirm=%#v status=%q", m.mode, m.runnerConfirm, m.statusMsg)
		}
	}
}

func TestRunnerViewListsProcessesAndShowsContextualActions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", "/home/user")
	info := runner.Info{Definition: config.RunnerConfig{ID: "arch-dotfiles", Workspace: "shared", Project: "dotfiles"}, Status: runner.StatusRunning, Path: "/dev/personal/dotfiles", PID: 42}
	m := &Model{runnerInfos: []runner.Info{info}}
	m.width, m.height = 120, 24
	view := m.viewRunners()
	for _, text := range []string{"dotfiles", "arch-dotfiles", "running", "r restart", "x shutdown"} {
		if !strings.Contains(view, text) {
			t.Fatalf("runner view does not contain %q", text)
		}
	}
}

func TestStoppedRunnerCanBeEditedOrRemoved(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	info := runner.Info{Definition: config.RunnerConfig{ID: "arch-old", Path: "/dev/project"}, Status: runner.StatusStopped, Path: "/dev/project"}
	m := NewModel(nil)
	m.mode, m.runnerInfos = viewRunners, []runner.Info{info}
	if footer := m.runnerFooter(); !strings.Contains(footer, "e edit ID") || !strings.Contains(footer, "d remove") {
		t.Fatalf("stopped runner footer = %q", footer)
	}
	commands := m.runnerPaletteCommands()
	if !hasPaletteAction(commands, "runner-edit") || !hasPaletteAction(commands, "runner-forget") {
		t.Fatalf("stopped runner commands = %#v", commands)
	}
	m.editSelectedRunner()
	if m.mode != viewRunnerForm || m.runnerForm == nil || m.runnerForm.originalID != "arch-old" || m.runnerID.Value() != "arch-old" {
		t.Fatalf("runner editor = mode %v form %#v ID %q", m.mode, m.runnerForm, m.runnerID.Value())
	}
}

func TestRunningRunnerIDCannotBeEdited(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	info := runner.Info{Definition: config.RunnerConfig{ID: "arch-running", Path: "/dev/project"}, Status: runner.StatusRunning, Path: "/dev/project", PID: 42}
	m := NewModel(nil)
	m.mode, m.runnerInfos = viewRunners, []runner.Info{info}
	m.editSelectedRunner()
	if m.mode != viewRunners || m.runnerForm != nil || m.statusMsg != "stop the runner before editing its ID" {
		t.Fatalf("running runner editor = mode %v form %#v status %q", m.mode, m.runnerForm, m.statusMsg)
	}
}

func TestRunnerRowsUseCompactPathsAndAlignedColumns(t *testing.T) {
	t.Setenv("HOME", "/home/user")
	infos := []runner.Info{
		{Status: runner.StatusOccupied, Path: "/home/user/.config/dotfiles", PID: 364256},
		{Definition: config.RunnerConfig{ID: "arch-midget"}, Status: runner.StatusRunning, Path: "/home/user/development/personal/midget", PID: 1848031},
	}
	columns := runnerListColumns(infos)
	external, externalPath := runnerRowLines(infos[0], string(infos[0].Status), columns, 120)
	managed, managedPath := runnerRowLines(infos[1], string(infos[1].Status), columns, 120)
	if strings.Index(external, "unmanaged") != strings.Index(managed, "running") {
		t.Fatalf("status columns differ:\n%q\n%q", external, managed)
	}
	if strings.LastIndex(external, "364256")+len("364256") != strings.LastIndex(managed, "1848031")+len("1848031") {
		t.Fatalf("PID columns differ:\n%q\n%q", external, managed)
	}
	if externalPath != "    .config/dotfiles" || managedPath != "    development/personal/midget" {
		t.Fatalf("compact paths = %q, %q", externalPath, managedPath)
	}
}

func TestRunnerJobStatusIsVisibleUntilTheJobFinishes(t *testing.T) {
	info := runner.Info{Definition: config.RunnerConfig{ID: "arch-midget"}, Status: runner.StatusRunning, Path: "/home/user/midget", PID: 42}
	m := &Model{}
	_ = m.runnerJob("restart", info.Definition, false)
	if got := m.runnerOperationStatus(info); got != "restarting" {
		t.Fatalf("queued runner status = %q", got)
	}
	m.jobs[0].State = jobRunning
	if got := m.runnerOperationStatus(info); got != "restarting" {
		t.Fatalf("running runner status = %q", got)
	}
	m.jobs[0].State = jobComplete
	if got := m.runnerOperationStatus(info); got != "running" {
		t.Fatalf("completed runner status = %q", got)
	}
}

func TestRunnerRefreshUpdatesVisibleProcessesAndSchedulesNextRefresh(t *testing.T) {
	m := &Model{mode: viewRunners, runnerInfos: []runner.Info{{Status: runner.StatusOccupied, Path: "/old", PID: 1}}}
	next := runner.Info{Status: runner.StatusOccupied, Path: "/new", PID: 2}
	model, cmd := m.Update(runnerRefreshMsg{infos: []runner.Info{next}})
	updated := model.(*Model)
	if cmd == nil || len(updated.runnerInfos) != 1 || updated.runnerInfos[0].PID != 2 {
		t.Fatalf("refresh result = infos %#v, scheduled %t", updated.runnerInfos, cmd != nil)
	}
}

func TestAttachWorkspaceTargetUsesConfiguredPrefixWithoutPathInput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveMachineConfig(&config.MachineConfig{MachineName: "archlinux", RunnerIDPrefix: "arch"}); err != nil {
		t.Fatal(err)
	}
	project := Project{ID: "tkach", Name: "tkach", WorkspaceRoot: "/dev", Path: "/dev/tkach"}
	m := NewModel([]WorkspaceData{{Name: "shared", Root: "/dev", Projects: []Project{project}}})
	m.openRunnerForm(m.projectRunnerTarget(&project))
	if m.mode != viewRunnerForm || m.runnerForm == nil {
		t.Fatalf("attach did not open runner form: mode=%v form=%#v", m.mode, m.runnerForm)
	}
	if got := m.runnerID.Value(); got != "arch-tkach" {
		t.Fatalf("suggested runner ID = %q", got)
	}
	if m.runnerForm.target.Path != "" || m.runnerForm.target.Project != "tkach" {
		t.Fatalf("runner target = %#v", m.runnerForm.target)
	}
}

func TestGroupRunnerIDDoesNotIncludeDisplaySigil(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveMachineConfig(&config.MachineConfig{MachineName: "archlinux", RunnerIDPrefix: "arch"}); err != nil {
		t.Fatal(err)
	}
	m := NewModel([]WorkspaceData{{Name: "shared", Root: "/dev", Groups: []string{"game-source-reference"}}})
	if got := m.suggestRunnerID(m.groupRunnerTarget("/dev", "game-source-reference")); got != "arch-game-source-reference" {
		t.Fatalf("suggested group runner ID = %q", got)
	}
}

func TestExternalRunnerCanBeReplacedWhenPathMatchesWorkspaceTarget(t *testing.T) {
	project := Project{ID: "dotfiles", Name: "dotfiles", WorkspaceRoot: "/dev", Path: "/dev/dotfiles"}
	info := runner.Info{Status: runner.StatusOccupied, Path: project.Path, PID: 42, StartTime: 100}
	m := &Model{mode: viewRunners, workspaces: []WorkspaceData{{Name: "shared", Root: "/dev", Projects: []Project{project}}}, runnerInfos: []runner.Info{info}}
	m.confirmSelectedReplacement(false)
	if m.mode != viewRunnerConfirm || m.runnerConfirm == nil || m.runnerConfirm.external.PID != 42 {
		t.Fatalf("replacement confirmation = mode %v, confirm %#v", m.mode, m.runnerConfirm)
	}
	if m.runnerConfirm.definition.Project != "dotfiles" {
		t.Fatalf("replacement target = %#v", m.runnerConfirm.definition)
	}
	if footer := m.runnerFooter(); !strings.Contains(footer, "Enter replace external") {
		t.Fatalf("external runner footer = %q", footer)
	}
}

func TestExternalRunnerCanBeReplacedAtUnregisteredPath(t *testing.T) {
	info := runner.Info{Status: runner.StatusOccupied, Path: "/home/user/.config/dotfiles", PID: 42, StartTime: 100}
	m := &Model{mode: viewRunners, runnerInfos: []runner.Info{info}}
	m.confirmSelectedReplacement(false)
	if m.mode != viewRunnerConfirm || m.runnerConfirm == nil {
		t.Fatalf("replacement confirmation = mode %v, confirm %#v", m.mode, m.runnerConfirm)
	}
	if m.runnerConfirm.definition.Path != info.Path {
		t.Fatalf("replacement target = %#v", m.runnerConfirm.definition)
	}
	if footer := m.runnerFooter(); !strings.Contains(footer, "Enter replace external") {
		t.Fatalf("external runner footer = %q", footer)
	}
	if commands := m.runnerPaletteCommands(); !hasPaletteAction(commands, "runner-replace") {
		t.Fatalf("external runner commands = %#v", commands)
	}
}

func TestProjectRunnerDirectKeyOpensCreationForm(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveMachineConfig(&config.MachineConfig{MachineName: "archlinux", RunnerIDPrefix: "arch"}); err != nil {
		t.Fatal(err)
	}
	project := Project{ID: "tkach", Name: "tkach", WorkspaceRoot: "/dev", Path: "/dev/tkach"}
	m := NewModel([]WorkspaceData{{Name: "shared", Root: "/dev", Projects: []Project{project}}})
	m.items = []listItem{{kind: KindProject, workspaceRoot: "/dev", project: &project, path: project.Path}}
	_, _ = m.updateList(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'r'}})
	if m.mode != viewRunnerForm || m.runnerID.Value() != "arch-tkach" {
		t.Fatalf("direct runner action = mode %v, ID %q", m.mode, m.runnerID.Value())
	}
}

func hasPaletteAction(commands []paletteCommand, action string) bool {
	for _, command := range commands {
		if command.action == action {
			return true
		}
	}
	return false
}
