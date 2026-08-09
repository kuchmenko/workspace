package agent

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kuchmenko/workspace/internal/tui"
)

type jobState string

const (
	jobQueued   jobState = "queued"
	jobRunning  jobState = "running"
	jobComplete jobState = "completed"
	jobPartial  jobState = "partially completed"
	jobFailed   jobState = "failed"
)

type targetOutcomeKind string

const (
	targetSuccess targetOutcomeKind = "success"
	targetPartial targetOutcomeKind = "partial"
	targetFailed  targetOutcomeKind = "failed"
	targetSkipped targetOutcomeKind = "skipped/unchanged"
)

type targetOutcome struct {
	Target string
	Kind   targetOutcomeKind
	Detail string
}

type jobResult struct {
	Summary          string
	Error            string
	Details          []string
	Outcomes         []targetOutcome
	AffectedProjects []ProjectIdentity
	ArchivedProjects []ProjectIdentity
}

func (r jobResult) state() jobState {
	success, partial, failed, skipped := 0, 0, 0, 0
	for _, outcome := range r.Outcomes {
		switch outcome.Kind {
		case targetSuccess:
			success++
		case targetPartial:
			partial++
		case targetFailed:
			failed++
		case targetSkipped:
			skipped++
		}
	}
	if partial > 0 || success > 0 && (failed > 0 || skipped > 0) {
		return jobPartial
	}
	if failed > 0 {
		return jobFailed
	}
	return jobComplete
}

type explorerJob struct {
	ID, Label, Current              string
	State                           jobState
	Completed, Total                int
	QueuedAt, StartedAt, FinishedAt time.Time
	Summary, Error                  string
	Details                         []string
}

type jobSnapshot struct {
	workspaces       map[string]WorkspaceData
	worktreeDetails  map[string][]Worktree
	errors           []string
	archivedProjects []ProjectIdentity
}

type jobEvent struct {
	runner   *operationRunner
	id       string
	started  bool
	current  string
	outcome  *targetOutcome
	child    *jobResult
	result   *jobResult
	snapshot *jobSnapshot
	ack      chan struct{}
}

type operationRunner struct {
	id       atomic.Uint64
	mu       sync.Mutex
	registry map[string]*sync.Mutex
	projects map[string]*sync.Mutex
}

func newOperationRunner() *operationRunner {
	return &operationRunner{registry: map[string]*sync.Mutex{}, projects: map[string]*sync.Mutex{}}
}

func (r *operationRunner) nextID() string { return fmt.Sprintf("J%04d", r.id.Add(1)) }

func (r *operationRunner) lock(set map[string]*sync.Mutex, key string) func() {
	r.mu.Lock()
	lock := set[key]
	if lock == nil {
		lock = &sync.Mutex{}
		set[key] = lock
	}
	r.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (r *operationRunner) lockProject(root, id, path string) func() {
	key := root + "\x00" + id
	if id == "" {
		key = root + "\x00" + path
	}
	return r.lock(r.projects, key)
}

func (r *operationRunner) lockRegistry(root string) func() { return r.lock(r.registry, root) }

type jobContext struct {
	runner *operationRunner
	id     string
	events chan<- tui.Msg
}

func (c *jobContext) progress(current string) {
	c.events <- jobEvent{runner: c.runner, id: c.id, current: current}
}

func (c *jobContext) finishChild(result jobResult, snapshot bool) {
	var state *jobSnapshot
	if snapshot {
		loaded := loadJobSnapshot(result)
		state = &loaded
	}
	ack := make(chan struct{})
	c.events <- jobEvent{runner: c.runner, id: c.id, child: &result, snapshot: state, ack: ack}
	<-ack
}

func (c *jobContext) withProject(root, id, path string, run func()) {
	unlock := c.runner.lockProject(root, id, path)
	defer unlock()
	run()
}

func (c *jobContext) withRegistry(root string, run func()) {
	unlock := c.runner.lockRegistry(root)
	defer unlock()
	run()
}

func (m *Model) submitJob(label string, total int, run func(*jobContext) jobResult) tui.Cmd {
	if m.jobsRunner == nil {
		m.jobsRunner = newOperationRunner()
	}
	id := m.jobsRunner.nextID()
	job := &explorerJob{ID: id, Label: label, State: jobQueued, Total: total, QueuedAt: time.Now()}
	m.jobs = append(m.jobs, job)
	m.logJob(id, "queued label=%q total=%d", label, total)
	events := make(chan tui.Msg)
	runner := m.jobsRunner
	producer := func() tui.Msg {
		events <- jobEvent{runner: runner, id: id, started: true}
		ctx := &jobContext{runner: runner, id: id, events: events}
		result := run(ctx)
		events <- jobEvent{runner: runner, id: id, result: &result}
		close(events)
		return nil
	}
	stream := jobStreamMsg{runner: runner, id: id, events: events}
	return tui.Batch(producer, waitJobStream(stream))
}

func loadJobSnapshot(result jobResult) jobSnapshot {
	snapshot := jobSnapshot{workspaces: map[string]WorkspaceData{}, worktreeDetails: map[string][]Worktree{}, archivedProjects: result.ArchivedProjects}
	roots := map[string]bool{}
	for _, project := range append(append([]ProjectIdentity(nil), result.AffectedProjects...), result.ArchivedProjects...) {
		roots[project.WorkspaceRoot] = true
	}
	for root := range roots {
		workspace, diagnostics := loadOneWorkspace(root)
		snapshot.errors = append(snapshot.errors, diagnostics...)
		if workspace == nil {
			continue
		}
		snapshot.workspaces[root] = *workspace
		for _, project := range workspace.Projects {
			worktrees, err := LoadWorktrees(project.Path)
			if err != nil {
				snapshot.errors = append(snapshot.errors, err.Error())
				continue
			}
			snapshot.worktreeDetails[project.Path] = worktrees
		}
	}
	return snapshot
}

type jobStreamMsg struct {
	runner *operationRunner
	id     string
	events <-chan tui.Msg
}

type waitJobStreamMsg struct {
	runner *operationRunner
	id     string
	events <-chan tui.Msg
	event  tui.Msg
}

func waitJobStream(stream jobStreamMsg) tui.Cmd {
	return func() tui.Msg {
		event, ok := <-stream.events
		if !ok {
			return nil
		}
		return waitJobStreamMsg{stream.runner, stream.id, stream.events, event}
	}
}

func (m *Model) findJob(id string) *explorerJob {
	for _, job := range m.jobs {
		if job.ID == id {
			return job
		}
	}
	return nil
}

func (m *Model) jobsRunning() bool {
	for _, job := range m.jobs {
		if job.State == jobQueued || job.State == jobRunning {
			return true
		}
	}
	return false
}

func acknowledgeJob(msg jobEvent) {
	if msg.ack != nil {
		close(msg.ack)
	}
}

func (m *Model) applyJobEvent(msg jobEvent) {
	if msg.runner != m.jobsRunner {
		acknowledgeJob(msg)
		return
	}
	job := m.findJob(msg.id)
	if job == nil {
		acknowledgeJob(msg)
		return
	}
	if msg.started {
		job.State, job.StartedAt = jobRunning, time.Now()
		m.logJob(job.ID, "running")
		return
	}
	if msg.result != nil {
		state := msg.result.state()
		preservedError := job.Error
		job.Summary = msg.result.Summary
		job.Error = strings.Trim(strings.Join([]string{preservedError, msg.result.Error}, "; "), "; ")
		if preservedError != "" && state == jobComplete {
			state = jobPartial
		}
		job.State = state
		job.Details = append(job.Details, msg.result.Details...)
		job.FinishedAt = time.Now()
		m.logJob(job.ID, "finished state=%s summary=%q error=%q", job.State, job.Summary, job.Error)
		acknowledgeJob(msg)
		return
	}
	if msg.child != nil {
		for _, outcome := range msg.child.Outcomes {
			job.Completed++
			job.Current = outcome.Target
			job.Details = append(job.Details, outcome.Target+": "+outcome.Detail)
		}
		if msg.snapshot != nil {
			m.applyJobSnapshot(*msg.snapshot, job)
		}
		acknowledgeJob(msg)
		return
	}
	job.Current = msg.current
	if msg.outcome != nil {
		job.Completed++
		job.Details = append(job.Details, msg.outcome.Target+": "+msg.outcome.Detail)
	}
}

func (m *Model) applyJobSnapshot(snapshot jobSnapshot, job *explorerJob) {
	refreshed := map[string]bool{}
	for root, workspace := range snapshot.workspaces {
		for i := range m.workspaces {
			if m.workspaces[i].Root != root {
				continue
			}
			for _, project := range m.workspaces[i].Projects {
				m.wtCache.Invalidate(project.Path)
			}
			m.workspaces[i] = workspace
			for _, project := range workspace.Projects {
				if details, ok := snapshot.worktreeDetails[project.Path]; ok {
					m.wtCache.SeedDetails(project.Path, details)
				} else {
					m.wtCache.SeedInventory(project.Path, project.WorktreeInventory)
				}
			}
			refreshed[root] = true
			break
		}
	}
	var fallback []ProjectIdentity
	for _, project := range snapshot.archivedProjects {
		if !refreshed[project.WorkspaceRoot] {
			fallback = append(fallback, project)
		}
	}
	if len(fallback) > 0 {
		m.removeArchivedProjects(fallback)
	}
	if len(snapshot.errors) > 0 {
		job.Error = strings.Trim(strings.Join([]string{job.Error, "refresh: " + strings.Join(snapshot.errors, "; ")}, "; "), "; ")
	}
	m.cancelFlashForLifecycleRefresh()
	m.rebuildItems()
	returnSheetWasCurrent := m.jobsReturnSheet != nil && m.jobsReturnSheet == m.sheet
	m.sheet = m.reconcileLifecycleSheet(m.sheet)
	if returnSheetWasCurrent {
		m.jobsReturnSheet = m.sheet
	} else {
		m.jobsReturnSheet = m.reconcileLifecycleSheet(m.jobsReturnSheet)
	}
	if m.lifecycle != nil {
		m.lifecycle.parentSheet = m.reconcileLifecycleSheet(m.lifecycle.parentSheet)
	}
	if m.popupProj != nil {
		m.popupProj = m.findLifecycleProject(m.popupProj.WorkspaceRoot, m.popupProj.ID)
		if m.popupProj == nil && (m.mode == viewEditProject || m.mode == viewNewWorktree) {
			m.mode = viewList
		}
	}
}

func (m *Model) jobsStrip() string {
	if len(m.jobs) == 0 {
		return ""
	}
	active, queued := 0, 0
	for _, job := range m.jobs {
		if job.State == jobRunning {
			active++
		}
		if job.State == jobQueued {
			queued++
		}
	}
	latest := m.jobs[len(m.jobs)-1]
	status := latest.Summary
	if latest.State == jobRunning {
		status = fmt.Sprintf("%d/%d %s", latest.Completed, latest.Total, latest.Current)
	} else if latest.Error != "" {
		status = latest.Error
	}
	return fmt.Sprintf(" Jobs %d active · %d queued · %s %s · %s · A:open", active, queued, latest.ID, latest.State, status)
}

func (m *Model) logJob(id, format string, args ...any) {
	if m.debugLog != nil {
		m.debugLog.Printf("job id=%s %s", id, fmt.Sprintf(format, args...))
	}
}
