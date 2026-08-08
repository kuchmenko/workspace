package agent

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"codeberg.org/kuchmenko/workspace/internal/config"
	"codeberg.org/kuchmenko/workspace/internal/git"
	"codeberg.org/kuchmenko/workspace/internal/layout"
	"codeberg.org/kuchmenko/workspace/internal/tui"
)

type lifecycleScopeKind int

const (
	lifecycleProject lifecycleScopeKind = iota
	lifecycleGroup
	lifecycleGlobal
	lifecycleWorktree
)

type lifecycleAction int

const (
	lifecycleChoose lifecycleAction = iota
	lifecycleArchiveProjects
	lifecycleArchiveOldWorktrees
	lifecycleArchiveWorktree
	lifecycleDeleteWorktree
)

type lifecyclePhase int

const (
	lifecycleSelect lifecyclePhase = iota
	lifecycleThreshold
	lifecycleReview
	lifecycleTypedConfirm
	lifecycleResult
)

type lifecycleScope struct {
	kind          lifecycleScopeKind
	project       *Project
	group         string
	workspaceRoot string
	worktree      *Worktree
}

type WorktreeArchivePlan struct {
	Threshold  time.Duration
	Considered int
	Eligible   []worktreeCandidate
	Recent     int
	Main       int
	Dirty      int
	Unpushed   int
}

type worktreeCandidate struct {
	WorkspaceRoot string
	ProjectID     string
	Project       *Project
	Worktree      Worktree
}

type lifecycleModel struct {
	scope       lifecycleScope
	action      lifecycleAction
	phase       lifecyclePhase
	input       string
	errorText   string
	plan        WorktreeArchivePlan
	message     string
	parentSheet *sheet
}

func ParseArchiveThreshold(value string) (time.Duration, error) {
	v := strings.TrimSpace(value)
	units := []struct {
		suffix string
		value  time.Duration
	}{{"month", 30 * 24 * time.Hour}, {"w", 7 * 24 * time.Hour}, {"d", 24 * time.Hour}, {"h", time.Hour}}
	for _, unit := range units {
		if !strings.HasSuffix(v, unit.suffix) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(v, unit.suffix))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("threshold must be a positive integer followed by h, d, w, or month")
		}
		if uint64(n) > uint64(math.MaxInt64)/uint64(unit.value) {
			return 0, fmt.Errorf("threshold is too large")
		}
		return time.Duration(n) * unit.value, nil
	}
	return 0, fmt.Errorf("threshold must use h, d, w, or month (for example 72h, 1w, 1month)")
}

func (m *Model) openLifecycle(scope lifecycleScope) {
	m.lifecycle = &lifecycleModel{scope: scope, action: lifecycleChoose, phase: lifecycleSelect}
	m.mode = viewLifecycle
}

func (m *Model) openWorktreeArchive(project *Project, wt *Worktree) {
	m.openLifecycle(lifecycleScope{kind: lifecycleWorktree, project: project, worktree: wt})
	m.lifecycle.action = lifecycleArchiveWorktree
	m.prepareLifecycle()
}

func (m *Model) openWorktreeDelete(project *Project, wt *Worktree) {
	m.openLifecycle(lifecycleScope{kind: lifecycleWorktree, project: project, worktree: wt})
	m.lifecycle.action = lifecycleDeleteWorktree
	m.prepareLifecycle()
}

func (m *Model) prepareLifecycle() {
	lm := m.lifecycle
	lm.errorText = ""
	switch lm.action {
	case lifecycleArchiveProjects:
		lm.phase = lifecycleReview
		lm.message = fmt.Sprintf("Archive %d project(s)? Files, repositories, worktrees, and branches remain untouched.", len(m.lifecycleProjects(lm.scope)))
	case lifecycleArchiveOldWorktrees:
		lm.phase = lifecycleThreshold
		lm.input = ""
	case lifecycleArchiveWorktree:
		if err := validateArchiveWorktree(lm.scope.project, lm.scope.worktree); err != nil {
			lm.phase, lm.errorText = lifecycleResult, err.Error()
			return
		}
		lm.phase = lifecycleReview
		lm.message = "Archive local checkout " + lm.scope.worktree.Branch + "? Local and remote branches are preserved."
	case lifecycleDeleteWorktree:
		if err := validateDeleteWorktree(lm.scope.project, lm.scope.worktree); err != nil {
			lm.phase, lm.errorText = lifecycleResult, err.Error()
			return
		}
		lm.phase = lifecycleTypedConfirm
		lm.input = ""
		lm.message = "Type the exact branch name to delete checkout, remote branch, and local branch:"
	}
}

func (m *Model) lifecycleProjects(scope lifecycleScope) []worktreeCandidate {
	var result []worktreeCandidate
	for wi := range m.workspaces {
		ws := &m.workspaces[wi]
		for pi := range ws.Projects {
			p := &ws.Projects[pi]
			if scope.kind == lifecycleProject && (scope.project == nil || p.Path != scope.project.Path) {
				continue
			}
			if scope.kind == lifecycleGroup && (ws.Root != scope.workspaceRoot || p.Group != scope.group) {
				continue
			}
			result = append(result, worktreeCandidate{WorkspaceRoot: ws.Root, ProjectID: p.ID, Project: p})
		}
	}
	return result
}

func BuildWorktreeArchivePlan(projects []worktreeCandidate, threshold time.Duration, now time.Time, load func(string) []Worktree) WorktreeArchivePlan {
	plan := WorktreeArchivePlan{Threshold: threshold}
	cutoff := now.Add(-threshold)
	for _, project := range projects {
		for _, wt := range load(project.Project.Path) {
			plan.Considered++
			candidate := project
			candidate.Worktree = wt
			switch {
			case wt.IsMain:
				plan.Main++
			case wt.Dirty:
				plan.Dirty++
			case !worktreePublishedFresh(project.Project, &wt):
				plan.Unpushed++
			default:
				recent := wt.LastActiveAt
				if active := project.Project.BranchActivity[wt.Branch]; active.After(recent) {
					recent = active
				}
				if recent.IsZero() || !recent.Before(cutoff) {
					plan.Recent++
				} else {
					plan.Eligible = append(plan.Eligible, candidate)
				}
			}
		}
	}
	return plan
}

func worktreePublishedFresh(project *Project, wt *Worktree) bool {
	_, err := refreshWorktreePublication(project, wt)
	return err == nil
}

type branchPublication struct {
	LocalOID  string
	RemoteOID string
}

func refreshWorktreePublication(project *Project, wt *Worktree) (branchPublication, error) {
	if project == nil || wt == nil || wt.Branch == "" {
		return branchPublication{}, fmt.Errorf("worktree branch is unavailable")
	}
	bare := layout.BarePath(project.Path)
	remoteOID, err := git.FetchRemoteBranch(bare, "origin", wt.Branch)
	if err != nil {
		return branchPublication{}, err
	}
	localOID := git.RevParse(bare, "refs/heads/"+wt.Branch)
	if localOID == "" {
		return branchPublication{}, fmt.Errorf("local branch is unavailable")
	}
	ahead, _, remoteExists := git.AheadBehindRemote(bare, wt.Branch, "origin")
	if !remoteExists || ahead != 0 {
		return branchPublication{}, fmt.Errorf("branch is local-only or has %d unpushed commit(s)", ahead)
	}
	return branchPublication{LocalOID: localOID, RemoteOID: remoteOID}, nil
}

func validateArchiveWorktree(project *Project, wt *Worktree) error {
	if project == nil || wt == nil || wt.IsMain {
		return fmt.Errorf("cannot archive main worktree")
	}
	if wt.Dirty || git.IsDirty(wt.Path) {
		return fmt.Errorf("cannot archive dirty worktree")
	}
	return nil
}

func validateDeleteWorktree(project *Project, wt *Worktree) error {
	if err := validateArchiveWorktree(project, wt); err != nil {
		return err
	}
	branch := wt.Branch
	if branch == "main" || branch == "master" || branch == "dev" || branch == project.DefaultBranch {
		return fmt.Errorf("cannot delete protected branch %s", branch)
	}
	if _, err := refreshWorktreePublication(project, wt); err != nil {
		return fmt.Errorf("cannot delete worktree: %w", err)
	}
	return nil
}

func (m *Model) updateLifecycle(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	lm := m.lifecycle
	key := msg.String()
	if key == "esc" {
		parent := lm.parentSheet
		m.mode, m.lifecycle = viewList, nil
		if parent != nil {
			parent.rebuild(m)
		}
		m.sheet = parent
		return m, nil
	}
	if lm.phase == lifecycleSelect {
		switch key {
		case "a", "1":
			lm.action = lifecycleArchiveProjects
			m.prepareLifecycle()
		case "w", "2":
			lm.action = lifecycleArchiveOldWorktrees
			m.prepareLifecycle()
		}
		return m, nil
	}
	if lm.phase == lifecycleThreshold || lm.phase == lifecycleTypedConfirm {
		switch key {
		case "backspace":
			if len(lm.input) > 0 {
				lm.input = lm.input[:len(lm.input)-1]
			}
		case "enter":
			if lm.phase == lifecycleThreshold {
				threshold, err := ParseArchiveThreshold(lm.input)
				if err != nil {
					lm.errorText = err.Error()
					return m, nil
				}
				lm.plan = BuildWorktreeArchivePlan(m.lifecycleProjects(lm.scope), threshold, time.Now(), LoadWorktrees)
				lm.phase = lifecycleReview
			} else if lm.input != lm.scope.worktree.Branch {
				lm.errorText = "confirmation must exactly match " + lm.scope.worktree.Branch
			} else {
				m.executeLifecycle()
			}
		default:
			if len(msg.Runes) > 0 {
				lm.input += string(msg.Runes)
			}
		}
		return m, nil
	}
	if lm.phase == lifecycleReview && (key == "y" || key == "enter") {
		m.executeLifecycle()
	}
	return m, nil
}

func (m *Model) executeLifecycle() {
	lm := m.lifecycle
	switch lm.action {
	case lifecycleArchiveProjects:
		result := ArchiveProjects(m.lifecycleProjects(lm.scope))
		m.removeArchivedProjects(result.Succeeded)
		if lm.scope.kind == lifecycleProject && len(result.Succeeded) > 0 {
			lm.parentSheet = nil
		}
		lm.message = fmt.Sprintf("Archived %d project(s); %d workspace(s) failed.", len(result.Succeeded), len(result.Failures))
		lm.errorText = strings.Join(result.Failures, "; ")
	case lifecycleArchiveWorktree:
		result := ArchiveWorktree(lm.scope.project, lm.scope.worktree, m.workspaceRootFor(lm.scope.project))
		if result.CheckoutRemoved {
			m.wtCache.Invalidate(lm.scope.project.Path)
		}
		if result.Err != nil {
			lm.errorText = result.Err.Error()
			if result.CheckoutRemoved {
				lm.message = "Checkout removed; ownership metadata remains."
			} else {
				lm.message = "Checkout unchanged."
			}
		} else {
			m.reloadProjectBranchActivity(lm.scope.project)
			lm.message = "Archived worktree; branch preserved."
		}
	case lifecycleArchiveOldWorktrees:
		result := ExecuteWorktreeArchivePlan(lm.plan)
		for _, p := range m.lifecycleProjects(lm.scope) {
			m.wtCache.Invalidate(p.Project.Path)
			m.reloadProjectBranchActivity(p.Project)
		}
		lm.message = fmt.Sprintf("Archived %d, skipped %d, failed %d.", result.Archived, lm.plan.Considered-len(lm.plan.Eligible)+result.Skipped, result.Failed)
		if len(result.Details) > 0 {
			lm.errorText = strings.Join(result.Details, "; ")
		}
	case lifecycleDeleteWorktree:
		lm.message, lm.errorText = DeleteWorktreeDestructive(lm.scope.project, lm.scope.worktree, m.workspaceRootFor(lm.scope.project))
		m.wtCache.Invalidate(lm.scope.project.Path)
		if lm.errorText == "" {
			m.reloadProjectBranchActivity(lm.scope.project)
		}
	}
	lm.phase = lifecycleResult
	m.rebuildItems()
}

type ProjectIdentity struct{ WorkspaceRoot, ProjectID string }
type ProjectArchiveResult struct {
	Succeeded []ProjectIdentity
	Failures  []string
}

func ArchiveProjects(projects []worktreeCandidate) ProjectArchiveResult {
	byRoot := map[string][]string{}
	for _, p := range projects {
		byRoot[p.WorkspaceRoot] = append(byRoot[p.WorkspaceRoot], p.ProjectID)
	}
	roots := make([]string, 0, len(byRoot))
	for root := range byRoot {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	result := ProjectArchiveResult{}
	for _, root := range roots {
		ids := byRoot[root]
		sort.Strings(ids)
		ws, err := config.Load(root)
		if err != nil {
			result.Failures = append(result.Failures, root+": "+err.Error())
			continue
		}
		var changed []string
		for _, id := range ids {
			p, ok := ws.Projects[id]
			if ok && p.Status != config.StatusArchived {
				p.Status = config.StatusArchived
				ws.Projects[id] = p
				changed = append(changed, id)
			}
		}
		if err := config.Save(root, ws); err != nil {
			result.Failures = append(result.Failures, root+": "+err.Error())
			continue
		}
		for _, id := range changed {
			result.Succeeded = append(result.Succeeded, ProjectIdentity{root, id})
		}
	}
	return result
}

type WorktreeArchiveResult struct {
	CheckoutRemoved, MetadataReleased bool
	Err                               error
}

func ArchiveWorktree(project *Project, wt *Worktree, workspaceRoot string) WorktreeArchiveResult {
	if err := validateArchiveWorktree(project, wt); err != nil {
		return WorktreeArchiveResult{Err: err}
	}
	if err := git.WorktreeRemove(layout.BarePath(project.Path), wt.Path, false); err != nil {
		return WorktreeArchiveResult{Err: err}
	}
	result := WorktreeArchiveResult{CheckoutRemoved: true}
	result.Err = releaseWorktreeOwnership(workspaceRoot, project.ID, wt.Branch)
	result.MetadataReleased = result.Err == nil
	return result
}

type WorktreeArchiveExecution struct {
	Archived int
	Skipped  int
	Failed   int
	Details  []string
}

func ExecuteWorktreeArchivePlan(plan WorktreeArchivePlan) WorktreeArchiveExecution {
	result := WorktreeArchiveExecution{}
	for i := range plan.Eligible {
		candidate := &plan.Eligible[i]
		fresh, reason := revalidateWorktreeArchiveCandidate(*candidate, plan.Threshold, time.Now())
		if reason != "" {
			result.Skipped++
			result.Details = append(result.Details, candidate.Project.Name+"/"+candidate.Worktree.Branch+": skipped: "+reason)
			continue
		}
		archive := ArchiveWorktree(candidate.Project, fresh, candidate.WorkspaceRoot)
		if archive.Err != nil {
			result.Failed++
			state := "checkout unchanged"
			if archive.CheckoutRemoved {
				state = "checkout removed; ownership metadata remains"
			}
			result.Details = append(result.Details, candidate.Project.Name+"/"+candidate.Worktree.Branch+": "+state+": "+archive.Err.Error())
			continue
		}
		result.Archived++
	}
	return result
}

func revalidateWorktreeArchiveCandidate(candidate worktreeCandidate, threshold time.Duration, now time.Time) (*Worktree, string) {
	var fresh *Worktree
	for _, wt := range LoadWorktrees(candidate.Project.Path) {
		if wt.Path == candidate.Worktree.Path {
			copy := wt
			fresh = &copy
			break
		}
	}
	if fresh == nil {
		return nil, "worktree is missing"
	}
	if fresh.Dirty {
		return nil, "worktree became dirty"
	}
	ws, err := config.Load(candidate.WorkspaceRoot)
	if err != nil {
		return nil, "registry reload failed: " + err.Error()
	}
	stored, ok := ws.Projects[candidate.ProjectID]
	if !ok {
		return nil, "project is missing from registry"
	}
	recent := fresh.LastActiveAt
	if active := branchActivity(stored.Branches)[fresh.Branch]; active.After(recent) {
		recent = active
	}
	if recent.IsZero() || !recent.Before(now.Add(-threshold)) {
		return nil, "worktree is now recent"
	}
	if _, err := refreshWorktreePublication(candidate.Project, fresh); err != nil {
		return nil, "publication is no longer safe: " + err.Error()
	}
	return fresh, ""
}

func DeleteWorktreeDestructive(project *Project, wt *Worktree, workspaceRoot string) (string, string) {
	if err := validateArchiveWorktree(project, wt); err != nil {
		return "", err.Error()
	}
	branch := wt.Branch
	if branch == "main" || branch == "master" || branch == "dev" || branch == project.DefaultBranch {
		return "", "cannot delete protected branch " + branch
	}
	bare := layout.BarePath(project.Path)
	publication, err := refreshWorktreePublication(project, wt)
	if err != nil {
		return "checkout unchanged", "remote branch not verified: " + err.Error()
	}
	live, err := config.Load(workspaceRoot)
	if err != nil {
		return "checkout unchanged", "project reload failed: " + err.Error()
	}
	liveProject, ok := live.Projects[project.ID]
	if !ok {
		return "checkout unchanged", "project reload failed: project missing from registry"
	}
	if branch == "main" || branch == "master" || branch == "dev" || branch == liveProject.DefaultBranch {
		return "checkout unchanged", "cannot delete protected branch " + branch
	}
	if err := git.WorktreeRemove(bare, wt.Path, false); err != nil {
		return "", "checkout unchanged: " + err.Error()
	}
	if git.RevParse(bare, "refs/heads/"+branch) != publication.LocalOID {
		return "Checkout removed; local and remote branches remain.", "local branch changed after verification"
	}
	if err := git.DeleteRemoteBranch(bare, "origin", wt.Branch, publication.RemoteOID); err != nil {
		return "Checkout removed; local and remote branches remain.", err.Error()
	}
	if err := git.DeleteLocalBranch(bare, wt.Branch, publication.LocalOID); err != nil {
		return "Checkout and remote branch removed; local branch remains.", err.Error()
	}
	if err := releaseWorktreeOwnership(workspaceRoot, project.ID, wt.Branch); err != nil {
		return "Checkout, remote branch, and local branch removed; ownership metadata remains.", err.Error()
	}
	return "Deleted checkout, remote branch, local branch, and ownership metadata.", ""
}

func releaseWorktreeOwnership(root, projectID, branch string) error {
	if root == "" || projectID == "" {
		return fmt.Errorf("workspace or project identity is missing")
	}
	mc, err := config.LoadMachineConfig()
	if err != nil {
		return err
	}
	if mc.MachineName == "" {
		return fmt.Errorf("machine name is empty")
	}
	machine := mc.MachineName
	ws, err := config.Load(root)
	if err != nil {
		return err
	}
	project, ok := ws.Projects[projectID]
	if !ok {
		return fmt.Errorf("project %s missing from registry", projectID)
	}
	if changed, _ := project.ReleaseBranch(branch, machine); changed {
		ws.Projects[projectID] = project
		return config.Save(root, ws)
	}
	return nil
}

func (m *Model) reloadProjectBranchActivity(project *Project) {
	root := m.workspaceRootFor(project)
	ws, err := config.Load(root)
	if err != nil {
		return
	}
	stored, ok := ws.Projects[project.ID]
	if !ok {
		return
	}
	project.BranchActivity = branchActivity(stored.Branches)
}

func (m *Model) removeArchivedProjects(successes []ProjectIdentity) {
	success := map[ProjectIdentity]bool{}
	for _, id := range successes {
		success[id] = true
	}
	for wi := range m.workspaces {
		kept := m.workspaces[wi].Projects[:0]
		for pi := range m.workspaces[wi].Projects {
			p := &m.workspaces[wi].Projects[pi]
			archive := success[ProjectIdentity{m.workspaces[wi].Root, p.ID}]
			if !archive {
				kept = append(kept, *p)
			}
		}
		m.workspaces[wi].Projects = kept
	}
}

func (m *Model) viewLifecycle() string {
	lm := m.lifecycle
	lines := []string{"Lifecycle / maintenance", ""}
	switch lm.phase {
	case lifecycleSelect:
		lines = append(lines, "1 / a  Archive all active projects", "2 / w  Archive old worktrees", "", "esc  back")
	case lifecycleThreshold:
		lines = append(lines, "Age threshold (h/d/w/month):", lm.input+"█", "", "Examples: 72h · 1w · 1month", "enter  review   esc  back")
	case lifecycleReview:
		if lm.action == lifecycleArchiveOldWorktrees {
			p := lm.plan
			lines = append(lines, fmt.Sprintf("Threshold: %s", lm.input), fmt.Sprintf("Considered %d · eligible %d · recent %d", p.Considered, len(p.Eligible), p.Recent), fmt.Sprintf("Main %d · dirty %d · unpushed %d", p.Main, p.Dirty, p.Unpushed), "", "y / enter  confirm once   esc  cancel")
		} else {
			lines = append(lines, lm.message, "", "y / enter  confirm   esc  cancel")
		}
	case lifecycleTypedConfirm:
		lines = append(lines, lm.message, lm.input+"█", "", "enter  delete   esc  cancel")
	case lifecycleResult:
		if lm.message != "" {
			lines = append(lines, lm.message)
		}
		lines = append(lines, "", "esc  close")
	}
	if lm.errorText != "" {
		lines = append(lines, "", "Error: "+lm.errorText)
	}
	innerW := 68
	if m.width < 76 {
		innerW = m.width - 8
	}
	content := popupBorderStyle.Render(popupItemStyle.Width(innerW).Render(strings.Join(lines, "\n")))
	return tui.Place(m.width, m.height, tui.Center, tui.Center, content, tui.WithWhitespaceBackground(tui.Color("234")))
}
