package agent

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/kuchmenko/workspace/internal/tui"
)

type lifecycleProgressMsg struct {
	job     *lifecycleModel
	label   string
	detail  string
	started bool
}

type lifecycleDoneMsg struct {
	job    *lifecycleModel
	result lifecycleRunResult
}

type lifecyclePlanDoneMsg struct {
	job  *lifecycleModel
	plan WorktreeArchivePlan
}

type lifecycleRefreshDoneMsg struct {
	job              *lifecycleModel
	workspaces       map[string]WorkspaceData
	worktreeDetails  map[string][]Worktree
	errors           []string
	archivedProjects []ProjectIdentity
}

type lifecycleRunResult struct {
	message          string
	errorText        string
	targetKind       targetOutcomeKind
	affectedProjects []ProjectIdentity
	archivedProjects []ProjectIdentity
}

type lifecycleChildResult struct {
	outcome targetOutcome
	result  lifecycleRunResult
}

type ownershipRelease struct {
	id, branch string
}

func (m *Model) startLifecycleJob() tui.Cmd {
	lm := m.lifecycle
	returnFlash := m.lifecycleReturnFlash
	m.lifecycleReturnFlash = nil
	total := m.lifecycleJobTotal(lm)
	projects := cloneLifecycleProjects(m.lifecycleProjects(lm.scope))
	copyModel := cloneLifecycleModel(lm)
	m.lifecycle = nil
	m.mode = viewList
	m.sheet = lm.parentSheet
	cmd := m.submitJob(lifecycleActionLabel(lm.action), total, func(ctx *jobContext) jobResult {
		return runLifecycleAsync(copyModel, projects, m.debugLog, ctx)
	})
	if returnFlash != nil {
		m.restoreFlashRefresh(*returnFlash)
	}
	return cmd
}

func cloneLifecycleModel(lm *lifecycleModel) *lifecycleModel {
	copyModel := *lm
	copyModel.scope.worktrees = make([]*Worktree, len(lm.scope.worktrees))
	for i, worktree := range lm.scope.worktrees {
		if worktree != nil {
			copied := *worktree
			copyModel.scope.worktrees[i] = &copied
		}
	}
	if lm.scope.project != nil {
		project := cloneLifecycleProjects([]worktreeCandidate{{Project: lm.scope.project}})[0].Project
		copyModel.scope.project = project
	}
	copyModel.plan.Eligible = cloneLifecycleProjects(lm.plan.Eligible)
	return &copyModel
}

func (m *Model) lifecycleJobTotal(lm *lifecycleModel) int {
	switch lm.action {
	case lifecycleArchiveProjects:
		return len(m.lifecycleProjects(lm.scope))
	case lifecycleArchiveOldWorktrees:
		return len(lm.plan.Eligible)
	case lifecycleArchiveWorktree, lifecycleDeleteWorktree:
		return len(lm.scope.worktrees)
	default:
		return 0
	}
}

func waitLifecycleMessage(lm *lifecycleModel) tui.Cmd {
	return func() tui.Msg { return <-lm.messages }
}

func (m *Model) updateLifecycleProgress(msg lifecycleProgressMsg) (tui.Model, tui.Cmd) {
	if msg.job != m.lifecycleJob {
		return m, nil
	}
	if msg.started {
		msg.job.current = msg.label
	} else {
		msg.job.completed++
		msg.job.details = append(msg.job.details, msg.label+": "+msg.detail)
		msg.job.scroll = max(0, len(m.lifecycleBodyFor(msg.job))-m.lifecycleBodyRows())
	}
	return m, waitLifecycleMessage(msg.job)
}

func (m *Model) finishLifecycleJob(msg lifecycleDoneMsg) (tui.Model, tui.Cmd) {
	if msg.job != m.lifecycleJob {
		return m, nil
	}
	lm := msg.job
	lm.phase = lifecycleResult
	lm.current = ""
	lm.message = msg.result.message
	lm.errorText = msg.result.errorText
	lm.scroll = 0
	roots := map[string]bool{}
	for _, id := range msg.result.archivedProjects {
		roots[id.WorkspaceRoot] = true
	}
	seen := map[ProjectIdentity]bool{}
	for _, id := range msg.result.affectedProjects {
		if !seen[id] {
			roots[id.WorkspaceRoot] = true
			seen[id] = true
		}
	}
	if len(roots) == 0 {
		lm.phase = lifecycleResult
		m.logLifecycle("job finished action=%s completed=%d total=%d result=%q error=%q", lifecycleActionLabel(lm.action), lm.completed, lm.total, lm.message, lm.errorText)
		return m, nil
	}
	lm.phase = lifecycleRefreshing
	return m, func() tui.Msg {
		refreshed := make(map[string]WorkspaceData, len(roots))
		details := map[string][]Worktree{}
		var errors []string
		for root := range roots {
			workspace, diagnostics := loadOneWorkspace(root)
			for _, diagnostic := range diagnostics {
				errors = append(errors, root+": "+diagnostic)
			}
			if workspace != nil {
				refreshed[root] = *workspace
				for _, project := range workspace.Projects {
					worktrees, err := LoadWorktrees(project.Path)
					if err != nil {
						errors = append(errors, project.Path+": inspect worktrees: "+err.Error())
						continue
					}
					details[project.Path] = worktrees
				}
			}
		}
		return lifecycleRefreshDoneMsg{job: lm, workspaces: refreshed, worktreeDetails: details, errors: errors, archivedProjects: msg.result.archivedProjects}
	}
}

func (m *Model) finishLifecycleRefresh(msg lifecycleRefreshDoneMsg) (tui.Model, tui.Cmd) {
	if m.lifecycleJob != msg.job || msg.job.phase != lifecycleRefreshing {
		return m, nil
	}
	flash := m.captureFlashRefresh()
	refreshedRoots := map[string]bool{}
	for root, workspace := range msg.workspaces {
		for i := range m.workspaces {
			if m.workspaces[i].Root != root {
				continue
			}
			for _, project := range m.workspaces[i].Projects {
				m.wtCache.Invalidate(project.Path)
			}
			m.workspaces[i] = workspace
			for _, project := range workspace.Projects {
				if details, ok := msg.worktreeDetails[project.Path]; ok {
					m.wtCache.SeedDetails(project.Path, details)
				} else {
					m.wtCache.SeedInventory(project.Path, project.WorktreeInventory)
				}
			}
			refreshedRoots[root] = true
			break
		}
	}
	var fallbackArchived []ProjectIdentity
	for _, id := range msg.archivedProjects {
		if !refreshedRoots[id.WorkspaceRoot] {
			fallbackArchived = append(fallbackArchived, id)
		}
	}
	if len(fallbackArchived) > 0 {
		m.removeArchivedProjects(fallbackArchived)
	}
	if len(msg.errors) > 0 {
		if msg.job.errorText != "" {
			msg.job.errorText += "; "
		}
		msg.job.errorText += "refresh: " + strings.Join(msg.errors, "; ")
	}
	m.cancelFlashForLifecycleRefresh()
	m.rebuildItems()
	m.reconcileLifecycleUI(msg.job)
	m.restoreFlashRefresh(flash)
	msg.job.phase = lifecycleResult
	msg.job.scroll = 0
	m.logLifecycle("job finished action=%s completed=%d total=%d result=%q error=%q", lifecycleActionLabel(msg.job.action), msg.job.completed, msg.job.total, msg.job.message, msg.job.errorText)
	return m, nil
}

func (m *Model) reconcileLifecycleUI(lm *lifecycleModel) {
	m.sheet = m.reconcileLifecycleSheet(m.sheet)
	m.formReturnSheet = m.reconcileLifecycleSheet(m.formReturnSheet)
	if m.formReturnFlash != nil {
		m.formReturnFlash.returnSheet = m.reconcileLifecycleSheet(m.formReturnFlash.returnSheet)
	}
	if m.lifecycleReturnFlash != nil {
		m.lifecycleReturnFlash.returnSheet = m.reconcileLifecycleSheet(m.lifecycleReturnFlash.returnSheet)
	}
	if m.activityReturnFlash != nil {
		m.activityReturnFlash.returnSheet = m.reconcileLifecycleSheet(m.activityReturnFlash.returnSheet)
	}
	if lm != nil {
		lm.parentSheet = m.reconcileLifecycleSheet(lm.parentSheet)
	}
	if m.popupProj != nil {
		m.popupProj = m.findLifecycleProject(m.popupProj.WorkspaceRoot, m.popupProj.ID)
		if m.popupProj == nil && (m.mode == viewEditProject || m.mode == viewNewWorktree) {
			m.mode = viewList
		}
	}
	m.reconcilePaletteAfterRefresh()
}

func (m *Model) cancelFlashForLifecycleRefresh() {
	if m.mode != viewFlash {
		return
	}
	m.flashQuery.Blur()
	if m.savedExpanded != nil {
		m.expanded = m.savedExpanded
	}
	m.mode = viewList
	m.flashGlobal = false
	m.flashMatches = nil
	m.flashLabels = nil
	m.savedItems = nil
	m.savedExpanded = nil
}

func (m *Model) reconcileLifecycleSheet(s *sheet) *sheet {
	if s == nil {
		return nil
	}
	s.parent = m.reconcileLifecycleSheet(s.parent)
	if s.mode == sheetProject && s.target != nil {
		target := m.findLifecycleProject(s.target.WorkspaceRoot, s.target.ID)
		if target == nil {
			return s.parent
		}
		s.target = target
		if _, ok := m.wtCache.details[s.target.Path]; !ok {
			return s.parent
		}
	}
	s.rebuild(m)
	return s
}

func (m *Model) findLifecycleProject(root, id string) *Project {
	for wi := range m.workspaces {
		if m.workspaces[wi].Root != root {
			continue
		}
		for pi := range m.workspaces[wi].Projects {
			if m.workspaces[wi].Projects[pi].ID == id {
				return &m.workspaces[wi].Projects[pi]
			}
		}
	}
	return nil
}

func (m *Model) finishLifecyclePlan(msg lifecyclePlanDoneMsg) (tui.Model, tui.Cmd) {
	if m.lifecycleJob != msg.job || msg.job.phase != lifecyclePlanning {
		return m, nil
	}
	msg.job.plan = msg.plan
	msg.job.phase = lifecycleReview
	msg.job.message = fmt.Sprintf("Archive %d worktree(s) older than %s?", len(msg.plan.Eligible), msg.plan.Threshold)
	m.logLifecycle("planning finished threshold=%s considered=%d eligible=%d", msg.plan.Threshold, msg.plan.Considered, len(msg.plan.Eligible))
	return m, nil
}

func runLifecycle(lm *lifecycleModel, projects []worktreeCandidate, logger *log.Logger, emit func(string, string, bool)) lifecycleRunResult {
	startedAt := time.Now()
	logf := func(format string, args ...any) {
		if logger != nil {
			logger.Printf(format, args...)
		}
	}
	logResult := func(action, label, outcome, detail string, started time.Time) {
		logf("lifecycle action=%s target=%q outcome=%s duration=%s detail=%q", action, label, outcome, time.Since(started).Round(time.Millisecond), detail)
	}

	switch lm.action {
	case lifecycleArchiveProjects:
		r := ArchiveProjects(projects, func(id ProjectIdentity, started bool, outcome string) {
			emit(id.ProjectID, outcome, started)
			if !started {
				logf("lifecycle action=archive-project target=%q outcome=%q", id.ProjectID, outcome)
			}
		})
		logf("lifecycle action=archive-projects duration=%s archived=%d failed=%d", time.Since(startedAt).Round(time.Millisecond), len(r.Succeeded), r.Failed)
		return lifecycleRunResult{
			message:          fmt.Sprintf("Archived %d project(s); %d failed.", len(r.Succeeded), r.Failed),
			errorText:        strings.Join(r.Failures, "; "),
			archivedProjects: r.Succeeded,
		}
	case lifecycleArchiveWorktree:
		root := lm.scope.project.WorkspaceRoot
		archived, partial, failed := 0, 0, 0
		result := lifecycleRunResult{}
		for _, wt := range lm.scope.worktrees {
			label := worktreeDisplayName(*wt)
			emit(label, "", true)
			started := time.Now()
			r := ArchiveWorktree(lm.scope.project, wt, root, true)
			detail := "archived"
			if r.Err != nil && r.CheckoutRemoved {
				partial++
				detail = "partial: checkout removed; metadata remains: " + r.Err.Error()
			} else if r.Err != nil {
				failed++
				detail = "unchanged: " + r.Err.Error()
			} else {
				archived++
			}
			if r.CheckoutRemoved {
				result.affectedProjects = append(result.affectedProjects, ProjectIdentity{root, lm.scope.project.ID})
			}
			emit(label, detail, false)
			errorDetail := ""
			if r.Err != nil {
				errorDetail = r.Err.Error()
			}
			logResult("archive", label, detail, errorDetail, started)
		}
		if len(lm.scope.worktrees) == 1 && failed == 0 && partial == 0 {
			result.message = "Archived worktree; branch preserved."
		} else {
			result.message = fmt.Sprintf("Archived %d worktree(s); %d partial; %d failed.", archived, partial, failed)
		}
		return result
	case lifecycleArchiveOldWorktrees:
		r := ExecuteWorktreeArchivePlan(lm.plan, func(c worktreeCandidate, started bool, outcome string) {
			label := c.Project.Name + "/" + c.Worktree.Branch
			emit(label, outcome, started)
			if !started {
				logf("lifecycle action=archive-old target=%q outcome=%q", label, outcome)
			}
		})
		return lifecycleRunResult{
			message:          fmt.Sprintf("Archived %d, skipped %d, failed %d.", r.Archived, r.Skipped, r.Failed),
			errorText:        strings.Join(r.Details, "; "),
			affectedProjects: r.AffectedProjects,
		}
	case lifecycleDeleteWorktree:
		root := lm.scope.project.WorkspaceRoot
		deleted, partial, failed := 0, 0, 0
		result := lifecycleRunResult{}
		singleMessage, singleDetail := "", ""
		for _, wt := range lm.scope.worktrees {
			label := worktreeDisplayName(*wt)
			emit(label, "", true)
			started := time.Now()
			r := DeleteWorktreeDestructive(lm.scope.project, wt, root)
			singleMessage, singleDetail = r.Message, r.Detail
			detail := "deleted"
			switch {
			case r.CheckoutRemoved && r.BranchesDeleted && r.MetadataReleased:
				deleted++
			case r.CheckoutRemoved:
				partial++
				detail = "partial: " + r.Detail
			default:
				failed++
				detail = "unchanged: " + r.Detail
			}
			if r.CheckoutRemoved {
				result.affectedProjects = append(result.affectedProjects, ProjectIdentity{root, lm.scope.project.ID})
			}
			emit(label, detail, false)
			logResult("delete", label, detail, r.Detail, started)
		}
		if len(lm.scope.worktrees) == 1 {
			result.message, result.errorText = singleMessage, singleDetail
		} else {
			result.message = fmt.Sprintf("Deleted %d worktree(s); %d partial; %d failed.", deleted, partial, failed)
		}
		return result
	default:
		return lifecycleRunResult{message: "Nothing to do."}
	}
}

func runLifecycleAsync(lm *lifecycleModel, projects []worktreeCandidate, logger *log.Logger, ctx *jobContext) jobResult {
	targets := lifecycleTargets(lm, projects)
	results := make(chan lifecycleChildResult, len(targets))
	grouped := map[string][]worktreeCandidate{}
	for _, target := range targets {
		key := target.WorkspaceRoot + "\x00" + target.ProjectID
		grouped[key] = append(grouped[key], target)
	}
	var workers sync.WaitGroup
	for _, projectTargets := range grouped {
		projectTargets := projectTargets
		workers.Add(1)
		go func() {
			defer workers.Done()
			first := projectTargets[0]
			ctx.withProject(first.WorkspaceRoot, first.ProjectID, first.projectPath(), func() {
				runLifecycleProject(lm, projectTargets, logger, ctx, results)
			})
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()
	job := jobResult{}
	for child := range results {
		job.Outcomes = append(job.Outcomes, child.outcome)
		job.AffectedProjects = append(job.AffectedProjects, child.result.affectedProjects...)
		job.ArchivedProjects = append(job.ArchivedProjects, child.result.archivedProjects...)
		if child.result.errorText != "" {
			job.Details = append(job.Details, child.result.errorText)
		}
	}
	job.Summary = summarizeOutcomes(job.Outcomes)
	return job
}

func lifecycleTargets(lm *lifecycleModel, projects []worktreeCandidate) []worktreeCandidate {
	switch lm.action {
	case lifecycleArchiveProjects:
		return projects
	case lifecycleArchiveOldWorktrees:
		return lm.plan.Eligible
	default:
		targets := make([]worktreeCandidate, 0, len(lm.scope.worktrees))
		for _, worktree := range lm.scope.worktrees {
			targets = append(targets, worktreeCandidate{WorkspaceRoot: lm.scope.project.WorkspaceRoot, ProjectID: lm.scope.project.ID, Project: lm.scope.project, Worktree: *worktree})
		}
		return targets
	}
}

func (c worktreeCandidate) projectPath() string {
	if c.Project == nil {
		return ""
	}
	return c.Project.Path
}

func runLifecycleProject(lm *lifecycleModel, targets []worktreeCandidate, logger *log.Logger, ctx *jobContext, results chan<- lifecycleChildResult) {
	children := make([]lifecycleChildResult, 0, len(targets))
	releases := make([]ownershipRelease, 0, len(targets))
	if lm.action != lifecycleArchiveProjects {
		for _, target := range targets {
			ctx.progress(lifecycleTargetLabel(lm.action, target))
			children = append(children, runLifecycleTarget(lm, target, logger, func(id, branch string) error {
				releases = append(releases, ownershipRelease{id, branch})
				return nil
			}))
		}
	}
	root := targets[0].WorkspaceRoot
	ctx.withRegistry(root, func() {
		if lm.action == lifecycleArchiveProjects {
			for _, target := range targets {
				ctx.progress(lifecycleTargetLabel(lm.action, target))
				children = append(children, runLifecycleTarget(lm, target, logger, nil))
			}
		} else if err := releaseWorktreeOwnershipBatch(root, releases); err != nil {
			for i := range children {
				if len(children[i].result.affectedProjects) > 0 {
					children[i].result.errorText = strings.Trim(strings.Join([]string{children[i].result.errorText, err.Error()}, "; "), "; ")
					children[i].outcome.Kind, children[i].outcome.Detail = lifecycleTargetOutcome(lm.action, children[i].result)
				}
			}
		}
		childJob := jobResult{}
		for _, child := range children {
			childJob.Outcomes = append(childJob.Outcomes, child.outcome)
			childJob.AffectedProjects = append(childJob.AffectedProjects, child.result.affectedProjects...)
			childJob.ArchivedProjects = append(childJob.ArchivedProjects, child.result.archivedProjects...)
		}
		ctx.finishChild(childJob, len(childJob.AffectedProjects)+len(childJob.ArchivedProjects) > 0)
	})
	for _, child := range children {
		results <- child
	}
}

func runLifecycleTarget(lm *lifecycleModel, target worktreeCandidate, logger *log.Logger, release func(string, string) error) lifecycleChildResult {
	copyModel := cloneLifecycleModel(lm)
	copyModel.scope.project = target.Project
	copyModel.scope.worktree = &target.Worktree
	copyModel.scope.worktrees = []*Worktree{&target.Worktree}
	copyModel.plan.Eligible = []worktreeCandidate{target}
	var result lifecycleRunResult
	if lm.action == lifecycleArchiveProjects {
		result = runLifecycle(copyModel, []worktreeCandidate{target}, logger, func(string, string, bool) {})
	} else {
		result = runLifecycleTargetMutation(copyModel, target, logger, release)
	}
	kind, detail := lifecycleTargetOutcome(lm.action, result)
	outcome := targetOutcome{Target: lifecycleTargetLabel(lm.action, target), Kind: kind, Detail: detail}
	return lifecycleChildResult{outcome: outcome, result: result}
}

func runLifecycleTargetMutation(lm *lifecycleModel, target worktreeCandidate, logger *log.Logger, release func(string, string) error) lifecycleRunResult {
	result := lifecycleRunResult{}
	switch lm.action {
	case lifecycleArchiveOldWorktrees:
		fresh, reason, err := revalidateWorktreeArchiveCandidate(target, lm.plan.Threshold, time.Now())
		if err != nil {
			result.errorText = err.Error()
			result.targetKind = targetFailed
			return result
		}
		if reason != "" {
			result.errorText = reason
			result.targetKind = targetSkipped
			return result
		}
		archived := archiveWorktree(target.Project, fresh, target.WorkspaceRoot, false, release)
		if archived.CheckoutRemoved {
			result.affectedProjects = []ProjectIdentity{{target.WorkspaceRoot, target.ProjectID}}
		}
		if archived.Err != nil {
			result.errorText = archived.Err.Error()
		}
		result.message = "Archived worktree; branch preserved."
	case lifecycleArchiveWorktree:
		archived := archiveWorktree(target.Project, &target.Worktree, target.WorkspaceRoot, true, release)
		if archived.CheckoutRemoved {
			result.affectedProjects = []ProjectIdentity{{target.WorkspaceRoot, target.ProjectID}}
		}
		if archived.Err != nil {
			result.errorText = archived.Err.Error()
		}
		result.message = "Archived worktree; branch preserved."
	case lifecycleDeleteWorktree:
		deleted := deleteWorktreeDestructive(target.Project, &target.Worktree, target.WorkspaceRoot, release)
		if deleted.CheckoutRemoved {
			result.affectedProjects = []ProjectIdentity{{target.WorkspaceRoot, target.ProjectID}}
		}
		result.message, result.errorText = deleted.Message, deleted.Detail
	}
	if logger != nil {
		logger.Printf("lifecycle action=%s target=%q outcome=%q", lifecycleActionLabel(lm.action), lifecycleTargetLabel(lm.action, target), result.message)
	}
	return result
}

func lifecycleTargetOutcome(action lifecycleAction, result lifecycleRunResult) (targetOutcomeKind, string) {
	if result.targetKind != "" {
		return result.targetKind, result.errorText
	}
	switch action {
	case lifecycleArchiveProjects:
		if len(result.archivedProjects) > 0 {
			return targetSuccess, "archived"
		}
		if result.errorText == "" {
			return targetSkipped, "unchanged: already archived"
		}
		return targetFailed, result.errorText
	case lifecycleArchiveOldWorktrees:
		if len(result.affectedProjects) > 0 && result.errorText != "" {
			return targetPartial, result.errorText
		}
		if len(result.affectedProjects) > 0 {
			return targetSuccess, "archived"
		}
		if result.errorText != "" {
			return targetSkipped, result.errorText
		}
		return targetSkipped, "unchanged"
	default:
		if len(result.affectedProjects) > 0 && result.errorText != "" {
			return targetPartial, result.errorText
		}
		if len(result.affectedProjects) > 0 {
			return targetSuccess, result.message
		}
		return targetFailed, result.errorText
	}
}

func lifecycleTargetLabel(action lifecycleAction, target worktreeCandidate) string {
	if action == lifecycleArchiveProjects {
		return target.ProjectID
	}
	name := target.ProjectID
	if target.Project != nil && target.Project.Name != "" {
		name = target.Project.Name
	}
	return name + "/" + target.Worktree.Branch
}

func summarizeOutcomes(outcomes []targetOutcome) string {
	counts := map[targetOutcomeKind]int{}
	for _, outcome := range outcomes {
		counts[outcome.Kind]++
	}
	return fmt.Sprintf("%d succeeded, %d partial, %d failed, %d skipped.", counts[targetSuccess], counts[targetPartial], counts[targetFailed], counts[targetSkipped])
}

func lifecycleActionLabel(action lifecycleAction) string {
	switch action {
	case lifecycleArchiveProjects:
		return "archive-projects"
	case lifecycleArchiveOldWorktrees:
		return "archive-old-worktrees"
	case lifecycleArchiveWorktree:
		return "archive-worktrees"
	case lifecycleDeleteWorktree:
		return "delete-worktrees"
	default:
		return "choose"
	}
}

func cloneLifecycleProjects(projects []worktreeCandidate) []worktreeCandidate {
	cloned := make([]worktreeCandidate, len(projects))
	for i, candidate := range projects {
		cloned[i] = candidate
		if candidate.Project == nil {
			continue
		}
		project := *candidate.Project
		project.BranchActivity = make(map[string]time.Time, len(candidate.Project.BranchActivity))
		for branch, active := range candidate.Project.BranchActivity {
			project.BranchActivity[branch] = active
		}
		project.WorktreeInventory = append([]Worktree(nil), candidate.Project.WorktreeInventory...)
		cloned[i].Project = &project
	}
	return cloned
}
