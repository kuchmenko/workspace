package cli

import (
	"context"
	"io"
	"log"
	"strings"
	"time"

	workspacesync "github.com/kuchmenko/workspace/internal/sync"
	"github.com/kuchmenko/workspace/internal/tui"
)

type syncStage int

const (
	syncProbing syncStage = iota
	syncReview
	syncConfirm
	syncRunning
	syncCanceling
	syncFinished
)

type syncRowKind int

const (
	syncSourceRow syncRowKind = iota
	syncWorkspaceRow
	syncProjectRow
	syncMirrorRow
)

type syncReviewRow struct {
	kind      syncRowKind
	id        string
	sourceKey string
	label     string
	state     workspacesync.ProjectState
}

type syncProbeEventMsg struct {
	event workspacesync.ProbeEvent
}

type syncProbeDoneMsg struct {
	report workspacesync.ProbeReport
}

type syncRunEventMsg struct {
	event workspacesync.Event
}

type syncRunDoneMsg struct {
	report workspacesync.Report
}

type syncSignalMsg struct{}
type syncTickMsg time.Time

type syncModel struct {
	parent            context.Context
	root              string
	plan              workspacesync.Plan
	stage             syncStage
	probeContext      context.Context
	probeMessages     chan tui.Msg
	runMessages       chan tui.Msg
	cancelProbe       context.CancelFunc
	cancelRun         context.CancelFunc
	probes            workspacesync.ProbeReport
	selection         workspacesync.Selection
	rows              []syncReviewRow
	cursor            int
	width             int
	height            int
	probeStarted      int
	probeFinished     int
	currentProbe      string
	probeStatuses     map[string]workspacesync.ProbeStatus
	errorText         string
	startedAt         time.Time
	elapsed           time.Duration
	currentProject    string
	currentOp         string
	completed         int
	total             int
	selectedProjects  map[string]bool
	completedProjects map[string]bool
	recent            []workspacesync.Event
	counters          syncCounts
	report            workspacesync.Report
	canceled          bool
}

func newSyncModel(parent context.Context, root string, plan workspacesync.Plan) syncModel {
	probeCtx, cancel := context.WithCancel(parent)
	return syncModel{
		parent:        parent,
		root:          root,
		plan:          plan,
		stage:         syncProbing,
		probeContext:  probeCtx,
		probeMessages: make(chan tui.Msg, 1),
		cancelProbe:   cancel,
		probeStatuses: make(map[string]workspacesync.ProbeStatus),
	}
}

func (m syncModel) Init() tui.Cmd {
	return tui.Batch(m.startProbe(), m.waitProbeMessage(), m.waitSignal())
}

func (m syncModel) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch value := msg.(type) {
	case tui.WindowSizeMsg:
		m.width, m.height = value.Width, value.Height
		return m, nil
	case syncSignalMsg:
		return m.cancel()
	case tui.KeyMsg:
		return m.updateKey(value)
	default:
		return m.updateProgress(msg)
	}
}

func (m syncModel) updateProgress(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch value := msg.(type) {
	case syncProbeEventMsg:
		m.updateProbeEvent(value.event)
		return m, m.waitProbeMessage()
	case syncProbeDoneMsg:
		return m.finishProbe(value.report)
	case syncRunEventMsg:
		m.updateRunEvent(value.event)
		return m, m.waitRunMessage()
	case syncRunDoneMsg:
		m.report = value.report
		m.canceled = value.report.Canceled
		m.stage = syncFinished
		m.elapsed = time.Since(m.startedAt)
		return m, nil
	case syncTickMsg:
		if m.stage == syncRunning || m.stage == syncCanceling {
			m.elapsed = time.Since(m.startedAt)
			return m, syncTick()
		}
		return m, nil
	}
	return m, nil
}

func (m syncModel) updateKey(key tui.KeyMsg) (tui.Model, tui.Cmd) {
	if key.String() == "ctrl+c" {
		return m.cancel()
	}
	switch m.stage {
	case syncReview:
		return m.updateReviewKey(key)
	case syncConfirm:
		return m.updateConfirmKey(key)
	case syncFinished:
		return m, tui.Quit
	}
	return m, nil
}

func (m syncModel) updateReviewKey(key tui.KeyMsg) (tui.Model, tui.Cmd) {
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor+1 < len(m.rows) {
			m.cursor++
		}
	case "space":
		m.toggleCurrent()
	case "c":
		m.toggleConversion()
	case "enter":
		m.errorText = ""
		m.stage = syncConfirm
	case "esc", "q":
		m.canceled = true
		return m, tui.Quit
	}
	return m, nil
}

func (m syncModel) updateConfirmKey(key tui.KeyMsg) (tui.Model, tui.Cmd) {
	switch key.String() {
	case "enter", "y":
		return m.beginRun()
	case "esc", "n":
		m.stage = syncReview
	}
	return m, nil
}

func (m syncModel) cancel() (tui.Model, tui.Cmd) {
	switch m.stage {
	case syncProbing:
		m.canceled = true
		m.cancelProbe()
		m.stage = syncCanceling
		return m, nil
	case syncRunning:
		m.canceled = true
		m.cancelRun()
		m.stage = syncCanceling
		return m, nil
	case syncCanceling:
		return m, nil
	case syncFinished:
		return m, tui.Quit
	default:
		m.canceled = true
		return m, tui.Quit
	}
}

func (m syncModel) startProbe() tui.Cmd {
	return func() tui.Msg {
		report := workspacesync.Probe(m.probeContext, m.plan, func(event workspacesync.ProbeEvent) {
			m.probeMessages <- syncProbeEventMsg{event: event}
		})
		m.probeMessages <- syncProbeDoneMsg{report: report}
		return nil
	}
}

func (m syncModel) waitProbeMessage() tui.Cmd {
	return func() tui.Msg { return <-m.probeMessages }
}

func (m syncModel) waitSignal() tui.Cmd {
	return func() tui.Msg {
		<-m.parent.Done()
		return syncSignalMsg{}
	}
}

func (m *syncModel) updateProbeEvent(event workspacesync.ProbeEvent) {
	if event.Kind == workspacesync.ProbeStarted {
		m.probeStarted++
		m.currentProbe = event.URL
		return
	}
	m.probeFinished++
	m.probeStatuses[event.EndpointID] = event.Result.Status
}

func (m syncModel) finishProbe(report workspacesync.ProbeReport) (tui.Model, tui.Cmd) {
	m.probes = report
	if m.canceled || m.parent.Err() != nil {
		m.canceled = true
		return m, tui.Quit
	}
	m.selection = workspacesync.NewSelection(m.plan, report)
	m.rows = buildSyncRows(m.plan)
	m.stage = syncReview
	m.currentProbe = ""
	return m, nil
}

func buildSyncRows(plan workspacesync.Plan) []syncReviewRow {
	targets := make(map[string]workspacesync.Target, len(plan.Targets))
	for _, target := range plan.Targets {
		targets[target.ID] = target
	}
	var rows []syncReviewRow
	for _, source := range plan.SourceGroups {
		rows = append(rows, syncReviewRow{kind: syncSourceRow, id: source.Key, sourceKey: source.Key, label: source.Key})
		for _, targetID := range source.TargetIDs {
			target := targets[targetID]
			rows = append(rows, syncTargetRow(plan, target, source.Key))
		}
	}
	return rows
}

func syncTargetRow(plan workspacesync.Plan, target workspacesync.Target, source string) syncReviewRow {
	row := syncReviewRow{id: target.ID, sourceKey: source}
	switch target.Role {
	case workspacesync.TargetWorkspaceOrigin:
		row.kind, row.label = syncWorkspaceRow, "workspace registry"
	case workspacesync.TargetProjectOrigin:
		row.kind, row.label = syncProjectRow, target.Project
		for _, project := range plan.Projects {
			if project.Name == target.Project {
				row.state = project.State
				break
			}
		}
	case workspacesync.TargetMirror:
		row.kind, row.label = syncMirrorRow, target.Project+" / "+target.Mirror
	}
	return row
}

func (m *syncModel) toggleCurrent() {
	if len(m.rows) == 0 {
		return
	}
	row := m.rows[m.cursor]
	var err error
	switch row.kind {
	case syncSourceRow:
		err = m.selection.ToggleSource(row.id)
	case syncProjectRow:
		err = m.selection.ToggleProject(targetProject(m.plan, row.id))
	default:
		err = m.selection.ToggleTarget(row.id)
	}
	if err != nil {
		m.errorText = err.Error()
		return
	}
	m.errorText = ""
}

func (m *syncModel) toggleConversion() {
	if len(m.rows) == 0 {
		return
	}
	row := m.rows[m.cursor]
	if row.kind != syncProjectRow && row.kind != syncWorkspaceRow {
		return
	}
	if _, selected := m.selection.Conversion(row.id); selected {
		m.selection.RemoveConversion(row.id)
		m.errorText = ""
		return
	}
	if err := m.selection.SelectConversion(row.id); err != nil {
		m.errorText = err.Error()
		return
	}
	m.errorText = ""
}

func targetProject(plan workspacesync.Plan, targetID string) string {
	for _, target := range plan.Targets {
		if target.ID == targetID {
			return target.Project
		}
	}
	return ""
}

func (m syncModel) beginRun() (tui.Model, tui.Cmd) {
	runCtx, cancel := context.WithCancel(m.parent)
	m.cancelRun = cancel
	m.runMessages = make(chan tui.Msg, 1)
	m.stage = syncRunning
	m.startedAt = time.Now()
	m.elapsed = 0
	m.selectedProjects = make(map[string]bool)
	for _, project := range m.selection.SelectedProjects() {
		m.selectedProjects[project] = true
	}
	m.completedProjects = make(map[string]bool)
	m.total = len(m.selectedProjects)
	return m, tui.Batch(m.startRun(runCtx), m.waitRunMessage(), syncTick())
}

func (m syncModel) startRun(ctx context.Context) tui.Cmd {
	return func() tui.Msg {
		runner := workspacesync.NewRunner(m.root, log.New(io.Discard, "", 0))
		report := runner.RunContext(ctx, m.selection, func(event workspacesync.Event) {
			m.runMessages <- syncRunEventMsg{event: event}
		})
		m.runMessages <- syncRunDoneMsg{report: report}
		return nil
	}
}

func (m syncModel) waitRunMessage() tui.Cmd {
	return func() tui.Msg { return <-m.runMessages }
}

func syncTick() tui.Cmd {
	return func() tui.Msg {
		return syncTickMsg(<-time.After(200 * time.Millisecond))
	}
}

func (m *syncModel) updateRunEvent(event workspacesync.Event) {
	if event.Kind == workspacesync.EventStarted {
		m.currentProject = event.Project
		m.currentOp = event.Operation
		if event.Mirror != "" {
			m.currentProject += "/" + event.Mirror
		}
		return
	}
	if (event.Kind == workspacesync.EventProject || event.Kind == workspacesync.EventSkipped) && m.selectedProjects[event.Project] && !m.completedProjects[event.Project] {
		m.completedProjects[event.Project] = true
		m.completed++
	}
	if event.Kind != workspacesync.EventConflict {
		m.counters.add(event.Status)
	}
	m.recent = append(m.recent, event)
	if len(m.recent) > 6 {
		m.recent = m.recent[len(m.recent)-6:]
	}
}

func runSyncTUI(parent context.Context, root string, plan workspacesync.Plan, output io.Writer) error {
	programCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	program := tui.NewProgram(newSyncModel(parent, root, plan), tui.WithAltScreen(), tui.WithContext(programCtx), tui.WithoutSignalHandler())
	result, err := program.Run()
	if err != nil {
		return err
	}
	model, ok := result.(syncModel)
	if !ok {
		return nil
	}
	text, code := renderSyncTUIResult(model)
	_, _ = io.WriteString(output, text)
	if code != 0 {
		return ExitError{Code: code}
	}
	return nil
}

func renderSyncTUIResult(model syncModel) (string, int) {
	if model.canceled && model.startedAt.IsZero() {
		return "Sync canceled before execution\n", syncExitCanceled
	}
	var output strings.Builder
	writeSyncInteractiveSummary(&output, model.report)
	if model.canceled {
		return output.String(), syncExitCanceled
	}
	return output.String(), classifySyncReport(model.report)
}
