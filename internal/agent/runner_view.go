package agent

import (
	"crypto/sha1"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/runner"
	"github.com/kuchmenko/workspace/internal/tui"
)

type runnerRefreshMsg struct {
	infos []runner.Info
	err   error
}

type runnerRefreshTickMsg struct{}

type runnerForm struct {
	target     config.RunnerConfig
	originalID string
	field      int
	remote     bool
	error      string
}

type runnerConfirmation struct {
	definition  config.RunnerConfig
	external    *runner.Info
	action      string
	force       bool
	returnMode  viewMode
	returnSheet *sheet
}

func (m *Model) confirmSelectedReplacement(force bool) {
	info := m.selectedRunner()
	if info == nil || info.Status != runner.StatusOccupied {
		return
	}
	target := info.Definition
	if target.ID == "" {
		var found bool
		target, found = m.runnerTargetForPath(info.Path)
		if !found {
			target = config.RunnerConfig{Path: info.Path}
		}
	}
	external := *info
	m.runnerConfirm = &runnerConfirmation{definition: target, external: &external, action: "replace", force: force, returnMode: m.mode, returnSheet: m.sheet}
	m.mode = viewRunnerConfirm
}

func runnerRefreshTick() tui.Cmd {
	return tui.Tick(2*time.Second, func(time.Time) tui.Msg { return runnerRefreshTickMsg{} })
}

func refreshRunners() tui.Cmd {
	return func() tui.Msg {
		infos, err := runner.List()
		return runnerRefreshMsg{infos: infos, err: err}
	}
}

func (m *Model) applyRunnerRefresh(msg runnerRefreshMsg) {
	if msg.err != nil {
		if m.statusMsg == "" {
			m.statusMsg = "runners: " + msg.err.Error()
		}
		return
	}
	m.runnerInfos = msg.infos
	if m.runnerCursor >= len(msg.infos) {
		m.runnerCursor = max(0, len(msg.infos)-1)
	}
	if m.sheet != nil {
		m.sheet.rebuild(m)
	}
}

func (m *Model) loadRunnerInfos() {
	infos, err := runner.List()
	m.applyRunnerRefresh(runnerRefreshMsg{infos: infos, err: err})
}

func (m *Model) openRunnerView() {
	m.runnerReturnMode, m.runnerReturnSheet = m.mode, m.sheet
	m.mode, m.sheet = viewRunners, nil
	m.loadRunnerInfos()
}

func (m *Model) closeRunnerView() {
	m.mode, m.sheet = m.runnerReturnMode, m.runnerReturnSheet
	m.runnerReturnMode, m.runnerReturnSheet = viewList, nil
}

func (m *Model) selectedRunner() *runner.Info {
	if m.runnerCursor < 0 || m.runnerCursor >= len(m.runnerInfos) {
		return nil
	}
	return &m.runnerInfos[m.runnerCursor]
}

func (m *Model) openRunnerForTarget(target config.RunnerConfig, path string) (tui.Model, tui.Cmd) {
	info, found := m.targetRunnerInfo(target, path)
	if !found {
		m.openRunnerForm(target)
		return m, nil
	}
	if info.Status == runner.StatusOccupied {
		m.statusMsg = "external runner is attached · open Runners to replace it"
		return m, nil
	}
	if info.Status == runner.StatusRunning {
		m.statusMsg = info.Definition.ID + " is already running"
		return m, nil
	}
	return m, m.runnerJob("start", info.Definition, false)
}

func (m *Model) updateRunners(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	switch msg.String() {
	case "q", "esc", "h", "left":
		m.closeRunnerView()
	case "j", "down":
		m.runnerCursor = min(max(0, len(m.runnerInfos)-1), m.runnerCursor+1)
	case "k", "up":
		m.runnerCursor = max(0, m.runnerCursor-1)
	case "g", "home":
		m.runnerCursor = 0
	case "G", "end":
		m.runnerCursor = max(0, len(m.runnerInfos)-1)
	case "enter":
		m.confirmSelectedReplacement(false)
	case "s":
		return m.startSelectedRunner()
	case "r":
		m.confirmSelectedRunner("restart", false)
	case "x":
		m.confirmSelectedRunner("shutdown", false)
	case "X":
		m.forceSelectedRunner()
	case "d":
		m.confirmSelectedRunner("forget", false)
	case "e":
		m.editSelectedRunner()
	case "p":
		m.openRunnerPrefix()
	}
	return m, nil
}

func (m *Model) editSelectedRunner() {
	info := m.selectedRunner()
	if info == nil || info.Definition.ID == "" {
		return
	}
	m.editRunner(info.Definition, info.Status)
}

func (m *Model) editRunner(def config.RunnerConfig, status runner.Status) {
	if status != runner.StatusStopped && status != runner.StatusMissing {
		m.statusMsg = "stop the runner before editing its ID"
		return
	}
	m.runnerReturnMode, m.runnerReturnSheet = m.mode, m.sheet
	m.runnerForm = &runnerForm{target: def, originalID: def.ID}
	m.runnerID.SetValue(def.ID)
	m.runnerID.Focus()
	m.mode, m.sheet = viewRunnerForm, nil
}

func (m *Model) startSelectedRunner() (tui.Model, tui.Cmd) {
	info := m.selectedRunner()
	if info == nil || info.Definition.ID == "" || info.Status == runner.StatusOccupied {
		return m, nil
	}
	return m, m.runnerJob("start", info.Definition, false)
}

func (m *Model) forceSelectedRunner() {
	if info := m.selectedRunner(); info != nil && info.Status == runner.StatusOccupied {
		m.confirmSelectedReplacement(true)
		return
	}
	m.confirmSelectedRunner("shutdown", true)
}

func (m *Model) confirmSelectedRunner(action string, force bool) {
	info := m.selectedRunner()
	if info == nil {
		m.statusMsg = "no runner is attached"
		return
	}
	if info.Definition.ID == "" {
		m.statusMsg = "unmanaged runners are read-only"
		return
	}
	if info.Status == runner.StatusOccupied && action != "forget" {
		m.statusMsg = "unmanaged runners are read-only"
		return
	}
	if action == "forget" && info.Status == runner.StatusRunning {
		m.statusMsg = "shut down the runner before forgetting it"
		return
	}
	m.confirmRunner(info.Definition, action, force)
}

func (m *Model) confirmRunner(def config.RunnerConfig, action string, force bool) {
	m.runnerConfirm = &runnerConfirmation{definition: def, action: action, force: force, returnMode: m.mode, returnSheet: m.sheet}
	m.mode = viewRunnerConfirm
}

func (m *Model) updateRunnerConfirm(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	confirm := m.runnerConfirm
	if confirm == nil {
		m.mode = viewRunners
		return m, nil
	}
	switch msg.String() {
	case "y", "enter":
		m.runnerConfirm = nil
		m.mode, m.sheet = confirm.returnMode, confirm.returnSheet
		if confirm.action == "replace" {
			return m, m.replaceExternalRunnerJob(confirm.definition, *confirm.external, confirm.force)
		}
		return m, m.runnerJob(confirm.action, confirm.definition, confirm.force)
	case "n", "q", "esc":
		m.runnerConfirm = nil
		m.mode, m.sheet = confirm.returnMode, confirm.returnSheet
	}
	return m, nil
}

func (m *Model) replaceExternalRunnerJob(target config.RunnerConfig, external runner.Info, force bool) tui.Cmd {
	return m.submitRunnerJob("replace external runner", external.Path, "replacing", func(ctx *jobContext) jobResult {
		outcome := targetOutcome{Target: external.Path, Kind: targetSuccess, Detail: "external runner stopped"}
		ctx.withRunner(external.Path, func() {
			if err := runner.ShutdownExternal(external, force); err != nil {
				outcome.Kind, outcome.Detail = targetFailed, err.Error()
				return
			}
			if target.ID != "" {
				started, err := runner.Start(target)
				if err != nil {
					outcome.Kind, outcome.Detail = targetFailed, err.Error()
					return
				}
				outcome.Detail = "managed runner started · PID " + strconv.Itoa(started.PID)
			}
		})
		result := jobResult{Summary: "external runner stopped", Error: outcomeError(outcome), Outcomes: []targetOutcome{outcome}, RefreshRunners: true}
		if outcome.Kind == targetSuccess && target.ID == "" {
			result.RunnerFormTarget = &target
		}
		return result
	})
}

func (m *Model) openRunnerForm(target config.RunnerConfig) {
	m.runnerReturnMode, m.runnerReturnSheet = m.mode, m.sheet
	m.runnerForm = &runnerForm{target: target}
	m.runnerID.SetValue(m.suggestRunnerID(target))
	m.runnerID.Focus()
	m.mode, m.sheet = viewRunnerForm, nil
}

func (m *Model) updateRunnerForm(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	form := m.runnerForm
	if form == nil {
		m.restoreRunnerForm()
		return m, nil
	}
	fields := 2
	if form.originalID != "" {
		fields = 1
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.restoreRunnerForm()
		return m, nil
	case "tab", "down":
		form.field = (form.field + 1) % fields
		m.focusRunnerFormField()
		return m, nil
	case "shift+tab", "up":
		form.field = (form.field + fields - 1) % fields
		m.focusRunnerFormField()
		return m, nil
	case " ":
		if form.field == fields-1 {
			form.remote = !form.remote
			return m, nil
		}
	case "enter":
		if form.field < fields-1 {
			form.field++
			m.focusRunnerFormField()
			return m, nil
		}
		return m.submitRunnerForm()
	}
	var cmd tui.Cmd
	if form.field == 0 {
		m.runnerID, cmd = m.runnerID.Update(msg)
	}
	return m, cmd
}

func (m *Model) focusRunnerFormField() {
	m.runnerID.Blur()
	if m.runnerForm.field == 0 {
		m.runnerID.Focus()
	}
}

func (m *Model) submitRunnerForm() (tui.Model, tui.Cmd) {
	form := m.runnerForm
	def := form.target
	def.ID = strings.TrimSpace(m.runnerID.Value())
	if form.originalID != "" {
		if err := runner.RenameDefinition(form.originalID, def.ID); err != nil {
			form.error = err.Error()
			return m, nil
		}
		m.restoreRunnerForm()
		m.statusMsg = "saved runner ID " + def.ID
		return m, nil
	}
	def.RemoteControlTerminal = form.remote
	if err := runner.SaveDefinition(def); err != nil {
		form.error = err.Error()
		return m, nil
	}
	m.restoreRunnerForm()
	return m, m.runnerJob("start", def, false)
}

func (m *Model) restoreRunnerForm() {
	m.runnerID.Blur()
	m.runnerForm = nil
	m.mode, m.sheet = m.runnerReturnMode, m.runnerReturnSheet
	m.runnerReturnMode, m.runnerReturnSheet = viewList, nil
	m.loadRunnerInfos()
}

func (m *Model) runnerJob(action string, def config.RunnerConfig, force bool) tui.Cmd {
	return m.submitRunnerJob(action+" runner "+def.ID, def.ID, runnerActionStatus(action), func(ctx *jobContext) jobResult {
		outcome := targetOutcome{Target: def.ID, Kind: targetSuccess}
		ctx.withRunner(def.ID, func() {
			var err error
			switch action {
			case "start":
				var info runner.Info
				info, err = runner.Start(def)
				if err == nil {
					outcome.Detail = "running · PID " + strconv.Itoa(info.PID)
				}
			case "restart":
				var info runner.Info
				info, err = runner.Restart(def, force)
				if err == nil {
					outcome.Detail = "restarted · PID " + strconv.Itoa(info.PID)
				}
			case "shutdown":
				err = runner.Shutdown(def, force)
				if err == nil {
					outcome.Detail = "stopped"
				}
			case "forget":
				err = runner.RemoveDefinition(def.ID)
				if err == nil {
					outcome.Detail = "forgotten"
				}
			}
			if err != nil {
				outcome.Kind, outcome.Detail = targetFailed, err.Error()
			}
		})
		return jobResult{Summary: action + " runner", Error: outcomeError(outcome), Outcomes: []targetOutcome{outcome}, RefreshRunners: true}
	})
}

func (m *Model) submitRunnerJob(label, key, status string, run func(*jobContext) jobResult) tui.Cmd {
	cmd := m.submitJob(label, 1, run)
	job := m.jobs[len(m.jobs)-1]
	job.RunnerKey, job.RunnerStatus = key, status
	return cmd
}

func runnerActionStatus(action string) string {
	switch action {
	case "start":
		return "starting"
	case "restart":
		return "restarting"
	case "shutdown":
		return "stopping"
	case "forget":
		return "forgetting"
	default:
		return action
	}
}

func (m *Model) runnerOperationStatus(info runner.Info) string {
	for i := len(m.jobs) - 1; i >= 0; i-- {
		job := m.jobs[i]
		if job.State != jobQueued && job.State != jobRunning {
			continue
		}
		if job.RunnerKey == info.Path || info.Definition.ID != "" && strings.EqualFold(job.RunnerKey, info.Definition.ID) {
			return job.RunnerStatus
		}
	}
	return string(info.Status)
}

func (m *Model) suggestRunnerID(target config.RunnerConfig) string {
	machine, _ := config.LoadMachineConfig()
	prefix := config.RunnerIDPrefix(machine)
	label := runnerTargetLabel(target)
	if target.Group != "" {
		label = target.Group
	} else if target.Path != "" {
		label = filepath.Base(strings.TrimSpace(target.Path))
	}
	id := config.SanitizeMachineName(prefix + "-" + label)
	if len(id) > 63 {
		sum := sha1.Sum([]byte(id))
		id = strings.TrimRight(id[:54], "-") + fmt.Sprintf("-%x", sum[:4])
	}
	used := map[string]bool{}
	if machine != nil {
		for _, def := range machine.Runners {
			used[strings.ToLower(def.ID)] = true
		}
	}
	base := id
	for suffix := 2; used[strings.ToLower(id)]; suffix++ {
		id = base + "-" + strconv.Itoa(suffix)
	}
	return id
}

func runnerTargetLabel(target config.RunnerConfig) string {
	if target.Path != "" {
		return target.Path
	}
	if target.Group != "" {
		return "@" + target.Group
	}
	if target.Worktree != "" {
		return target.Project + "/" + target.Worktree
	}
	return target.Project
}
