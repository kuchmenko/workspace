package agent

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kuchmenko/workspace/internal/tui"
)

func testJobContext(runner *operationRunner, id string, events chan tui.Msg) *jobContext {
	return &jobContext{runner: runner, id: id, events: events}
}

func waitSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for " + name)
	}
}

type jobTestPump struct {
	t    *testing.T
	msgs chan tui.Msg
}

func newJobTestPump(t *testing.T) *jobTestPump {
	t.Helper()
	return &jobTestPump{t: t, msgs: make(chan tui.Msg, 32)}
}

func (p *jobTestPump) start(cmd tui.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		value := reflect.ValueOf(msg)
		if value.IsValid() && value.Kind() == reflect.Slice && value.Type().PkgPath() == "github.com/kuchmenko/workspace/internal/tui" {
			for i := 0; i < value.Len(); i++ {
				p.start(value.Index(i).Interface().(tui.Cmd))
			}
			return
		}
		if msg != nil {
			p.msgs <- msg
		}
	}()
}

func (p *jobTestPump) update(m *Model) tui.Msg {
	p.t.Helper()
	select {
	case msg := <-p.msgs:
		_, cmd := m.Update(msg)
		p.start(cmd)
		return msg
	case <-time.After(2 * time.Second):
		p.t.Fatal("timed out waiting for job event")
		return nil
	}
}

func (p *jobTestPump) terminal(m *Model, jobs int) {
	p.t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		terminal := len(m.jobs) == jobs
		for _, job := range m.jobs {
			terminal = terminal && job.State != jobQueued && job.State != jobRunning
		}
		if terminal {
			return
		}
		select {
		case msg := <-p.msgs:
			_, cmd := m.Update(msg)
			p.start(cmd)
		case <-deadline:
			p.t.Fatal("jobs did not reach terminal state")
		}
	}
}

func TestOverlappingBulkJobsInverseAcquisitionCannotDeadlock(t *testing.T) {
	m := NewModel(nil)
	pump := newJobTestPump(t)
	submit := func(order []string) tui.Cmd {
		return m.submitJob("bulk", len(order), func(ctx *jobContext) jobResult {
			var result jobResult
			for _, project := range order {
				ctx.withProject("/ws", project, "", func() {
					child := jobResult{Outcomes: []targetOutcome{{Target: project, Kind: targetSuccess, Detail: "done"}}}
					ctx.finishChild(child, false)
					result.Outcomes = append(result.Outcomes, child.Outcomes...)
				})
			}
			return result
		})
	}
	pump.start(submit([]string{"A", "B"}))
	pump.start(submit([]string{"B", "A"}))
	pump.terminal(m, 2)
	for _, job := range m.jobs {
		if job.State != jobComplete || job.Completed != 2 {
			t.Fatalf("job = %+v", job)
		}
	}
}

func TestSameProjectMutationDoesNotOverlapThroughChildUIAck(t *testing.T) {
	runner := newOperationRunner()
	events := make(chan tui.Msg)
	ctx := testJobContext(runner, "J1", events)
	secondEntered := make(chan struct{})
	firstDone := make(chan struct{})
	go ctx.withProject("/ws", "A", "", func() {
		ctx.finishChild(jobResult{Outcomes: []targetOutcome{{Target: "A", Kind: targetSuccess}}}, false)
		close(firstDone)
	})
	event := (<-events).(jobEvent)
	go func() {
		unlock := runner.lockProject("/ws", "A", "")
		close(secondEntered)
		unlock()
	}()
	select {
	case <-secondEntered:
		t.Fatal("same project overlapped before child ack")
	default:
	}
	acknowledgeJob(event)
	waitSignal(t, firstDone, "first project release")
	waitSignal(t, secondEntered, "second project entry")
}

func TestIndependentSameWorkspaceProjectGitPhasesOverlap(t *testing.T) {
	runner := newOperationRunner()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	for _, id := range []string{"A", "B"} {
		go func(id string) {
			unlock := runner.lockProject("/ws", id, "")
			entered <- struct{}{}
			<-release
			unlock()
		}(id)
	}
	waitSignal(t, entered, "project A")
	waitSignal(t, entered, "project B")
	close(release)
}

func TestSameWorkspaceRegistrySnapshotAckPhasesDoNotOverlap(t *testing.T) {
	m := NewModel(nil)
	pump := newJobTestPump(t)
	firstEvent := make(chan struct{})
	secondAttempting := make(chan struct{})
	secondEntered := make(chan struct{})
	pump.start(m.submitJob("first", 1, func(ctx *jobContext) jobResult {
		ctx.withRegistry("/ws", func() {
			close(firstEvent)
			ctx.finishChild(jobResult{Outcomes: []targetOutcome{{Target: "first", Kind: targetSuccess}}}, false)
		})
		return jobResult{Outcomes: []targetOutcome{{Target: "first", Kind: targetSuccess}}}
	}))
	waitSignal(t, firstEvent, "first child event")
	pump.start(m.submitJob("second", 1, func(ctx *jobContext) jobResult {
		close(secondAttempting)
		ctx.withRegistry("/ws", func() { close(secondEntered) })
		return jobResult{Outcomes: []targetOutcome{{Target: "second", Kind: targetSuccess}}}
	}))
	waitSignal(t, secondAttempting, "second registry attempt")
	select {
	case <-secondEntered:
		t.Fatal("second registry phase entered before first child was applied")
	default:
	}
	for {
		msg := pump.update(m)
		if stream, ok := msg.(waitJobStreamMsg); ok {
			if event, ok := stream.event.(jobEvent); ok && event.child != nil && event.id == m.jobs[0].ID {
				break
			}
		}
	}
	waitSignal(t, secondEntered, "second registry entry after acknowledgement")
	pump.terminal(m, 2)
}

func TestCrossWorkspaceProjectsOverlap(t *testing.T) {
	runner := newOperationRunner()
	unlock := runner.lockProject("/one", "A", "")
	defer unlock()
	entered := make(chan struct{})
	go func() {
		release := runner.lockProject("/two", "A", "")
		close(entered)
		release()
	}()
	waitSignal(t, entered, "cross-workspace project")
}

func TestLiveProgressObservedBeforeBlockedOperationCompletes(t *testing.T) {
	m := NewModel(nil)
	pump := newJobTestPump(t)
	release := make(chan struct{})
	done := make(chan struct{})
	pump.start(m.submitJob("progress", 1, func(ctx *jobContext) jobResult {
		ctx.progress("working")
		<-release
		close(done)
		return jobResult{Outcomes: []targetOutcome{{Target: "work", Kind: targetSuccess}}}
	}))
	for m.jobs[0].Current != "working" {
		pump.update(m)
	}
	if m.jobs[0].State != jobRunning {
		t.Fatalf("job = %+v", m.jobs[0])
	}
	select {
	case <-done:
		t.Fatal("operation completed before release")
	default:
	}
	close(release)
	pump.terminal(m, 1)
	waitSignal(t, done, "operation completion")
}

func TestForeignAndStaleChildFinalEventsAreAcknowledged(t *testing.T) {
	m := NewModel(nil)
	for _, event := range []jobEvent{
		{runner: newOperationRunner(), id: "foreign", child: &jobResult{}, ack: make(chan struct{})},
		{runner: m.jobsRunner, id: "stale", child: &jobResult{}, ack: make(chan struct{})},
	} {
		m.applyJobEvent(event)
		select {
		case <-event.ack:
		default:
			t.Fatal("child event was not acknowledged")
		}
	}
}

func TestSameWorkspaceSnapshotsCannotApplyInReverseOrder(t *testing.T) {
	m := NewModel([]WorkspaceData{{Root: "/ws"}})
	pump := newJobTestPump(t)
	firstReady := make(chan struct{})
	secondAttempting := make(chan struct{})
	submit := func(group string, ready, attempting chan struct{}) tui.Cmd {
		return m.submitJob(group, 1, func(ctx *jobContext) jobResult {
			if attempting != nil {
				close(attempting)
			}
			ctx.withRegistry("/ws", func() {
				if ready != nil {
					close(ready)
				}
				snapshot := jobSnapshot{workspaces: map[string]WorkspaceData{"/ws": {Root: "/ws", Groups: []string{group}}}, worktreeDetails: map[string][]Worktree{}}
				ack := make(chan struct{})
				ctx.events <- jobEvent{runner: ctx.runner, id: ctx.id, child: &jobResult{Outcomes: []targetOutcome{{Target: group, Kind: targetSuccess}}}, snapshot: &snapshot, ack: ack}
				<-ack
			})
			return jobResult{Outcomes: []targetOutcome{{Target: group, Kind: targetSuccess}}}
		})
	}
	pump.start(submit("first", firstReady, nil))
	waitSignal(t, firstReady, "first concurrent snapshot")
	pump.start(submit("second", nil, secondAttempting))
	waitSignal(t, secondAttempting, "second concurrent snapshot attempt")
	var applied []string
	for len(applied) < 2 {
		msg := pump.update(m)
		if stream, ok := msg.(waitJobStreamMsg); ok {
			if event, ok := stream.event.(jobEvent); ok && event.snapshot != nil {
				applied = append(applied, event.snapshot.workspaces["/ws"].Groups[0])
			}
		}
	}
	pump.terminal(m, 2)
	if strings.Join(applied, ",") != "first,second" {
		t.Fatalf("snapshot application order = %v", applied)
	}
	if got := m.workspaces[0].Groups; len(got) != 1 || got[0] != "second" {
		t.Fatalf("workspace groups = %v", got)
	}
}

func TestBulkParentTerminalStatesAndExactlyOneOutcomePerTarget(t *testing.T) {
	tests := []struct {
		name  string
		want  jobState
		kinds []targetOutcomeKind
	}{
		{"completed", jobComplete, []targetOutcomeKind{targetSuccess, targetSuccess}},
		{"partial", jobPartial, []targetOutcomeKind{targetSuccess, targetFailed}},
		{"failed", jobFailed, []targetOutcomeKind{targetFailed, targetFailed}},
		{"all-skipped", jobComplete, []targetOutcomeKind{targetSkipped, targetSkipped}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel(nil)
			pump := newJobTestPump(t)
			pump.start(m.submitJob("bulk", len(tt.kinds), func(ctx *jobContext) jobResult {
				result := jobResult{}
				for i, kind := range tt.kinds {
					target := fmt.Sprintf("target-%d", i)
					outcome := targetOutcome{Target: target, Kind: kind, Detail: string(kind)}
					ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}}, false)
					result.Outcomes = append(result.Outcomes, outcome)
				}
				return result
			}))
			pump.terminal(m, 1)
			job := m.jobs[0]
			if job.State != tt.want || job.Completed != len(tt.kinds) || job.Total != len(tt.kinds) || len(job.Details) != len(tt.kinds) {
				t.Fatalf("job = %+v", job)
			}
			for i, detail := range job.Details {
				if !strings.HasPrefix(detail, fmt.Sprintf("target-%d: ", i)) {
					t.Fatalf("details = %v", job.Details)
				}
			}
		})
	}
}

func TestChildSnapshotErrorSurvivesSuccessfulParent(t *testing.T) {
	m := NewModel(nil)
	pump := newJobTestPump(t)
	missing := t.TempDir() + "/missing"
	pump.start(m.submitJob("snapshot", 1, func(ctx *jobContext) jobResult {
		outcome := targetOutcome{Target: "project", Kind: targetSuccess, Detail: "mutated"}
		ctx.finishChild(jobResult{Outcomes: []targetOutcome{outcome}, AffectedProjects: []ProjectIdentity{{WorkspaceRoot: missing, ProjectID: "project"}}}, true)
		return jobResult{Summary: "done", Outcomes: []targetOutcome{outcome}}
	}))
	pump.terminal(m, 1)
	job := m.jobs[0]
	if job.State != jobPartial || !strings.Contains(job.Error, "refresh:") {
		t.Fatalf("job = %+v", job)
	}
}

func TestJobsViewReturnsToSheetAndWindowsHistoryBeyondTerminal(t *testing.T) {
	m := NewModel(nil)
	m.width, m.height = 100, 10
	m.jobsReturnSheet = &sheet{mode: sheetGroup}
	for i := 0; i < 20; i++ {
		m.jobs = append(m.jobs, &explorerJob{ID: string(rune('A' + i)), Label: fmt.Sprintf("history-%02d", i), State: jobComplete, Total: 1, Completed: 1, Summary: "terminal"})
	}
	m.mode = viewJobs
	m.jobsSelectedID = m.jobs[len(m.jobs)-1].ID
	view := m.viewJobs()
	if !strings.Contains(view, "history-19") || strings.Contains(view, "history-00") {
		t.Fatalf("history window did not follow cursor: %q", view)
	}
	m.updateJobs(tui.KeyMsg{Type: tui.KeyEsc})
	if m.mode != viewList || m.sheet == nil || m.sheet.mode != sheetGroup {
		t.Fatalf("jobs return mode=%v sheet=%#v", m.mode, m.sheet)
	}
}

func TestActivityAttentionFeedDetailsAndIDStableSelection(t *testing.T) {
	now := time.Now()
	m := NewModel(nil)
	m.width, m.height = 100, 12
	m.jobs = []*explorerJob{
		{ID: "old", Label: "older", State: jobPartial, QueuedAt: now.Add(-time.Minute), Outcomes: []targetOutcome{{Target: "feat/x", Kind: targetPartial, Detail: "remote branch remains"}}},
		{ID: "new", Label: "newest", State: jobRunning, QueuedAt: now},
	}
	m.openActivity(nil)
	if token := m.activityAttentionToken(); token != "A:1▶" {
		t.Fatalf("attention token = %q", token)
	}
	view := m.viewJobs()
	if strings.Index(view, "newest") > strings.Index(view, "older") || strings.Contains(view, "remote branch remains") {
		t.Fatalf("feed order or inline details = %q", view)
	}
	m.updateJobs(tui.KeyMsg{Type: tui.KeyDown})
	if m.jobsSelectedID != "old" {
		t.Fatalf("selected ID = %q", m.jobsSelectedID)
	}
	m.jobs = append(m.jobs, &explorerJob{ID: "append", Label: "appended", State: jobQueued, QueuedAt: now.Add(time.Second)})
	if m.activityCursor(m.activityJobs()) != 2 || m.jobsSelectedID != "old" {
		t.Fatalf("selection moved after append: id=%q cursor=%d", m.jobsSelectedID, m.activityCursor(m.activityJobs()))
	}
	m.updateJobs(tui.KeyMsg{Type: tui.KeyEnter})
	detail := m.viewJobs()
	if !strings.Contains(detail, "completed with issues") || !strings.Contains(detail, "remote branch remains") {
		t.Fatalf("activity detail = %q", detail)
	}
	m.updateJobs(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'q'}})
	if m.jobsDetail {
		t.Fatal("q did not return from detail to feed")
	}
}

func TestActivitySearchRestoresSelectedAction(t *testing.T) {
	m := NewModel(nil)
	m.jobs = []*explorerJob{{ID: "old", Label: "older"}, {ID: "new", Label: "newer"}}
	m.openActivity(nil)
	m.jobsSelectedID = "old"
	m.updateJobs(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'/'}})
	m.updateJobs(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune("new")})
	m.updateJobs(tui.KeyMsg{Type: tui.KeyEnter})
	m.updateJobs(tui.KeyMsg{Type: tui.KeyRunes, Runes: []rune{'q'}})
	if m.jobsSelectedID != "old" || m.activitySearch {
		t.Fatalf("activity search restored id=%q active=%v", m.jobsSelectedID, m.activitySearch)
	}
}

func TestProjectLockMutualExclusionCounter(t *testing.T) {
	runner := newOperationRunner()
	var active atomic.Int32
	var overlap atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := runner.lockProject("/ws", "A", "")
			if active.Add(1) != 1 {
				overlap.Store(true)
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
			unlock()
		}()
	}
	wg.Wait()
	if overlap.Load() {
		t.Fatal("same project mutations overlapped")
	}
}
