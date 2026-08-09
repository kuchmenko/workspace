package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
	"github.com/kuchmenko/workspace/internal/tui"
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
	lifecycleResult
)

type lifecycleScope struct {
	kind                 lifecycleScopeKind
	project              *Project
	group, workspaceRoot string
	worktree             *Worktree
	worktrees            []*Worktree
}
type lifecycleModel struct {
	scope                     lifecycleScope
	action                    lifecycleAction
	phase                     lifecyclePhase
	input, errorText, message string
	details                   []string
	scroll                    int
	plan                      WorktreeArchivePlan
	parentSheet               *sheet
}
type worktreeCandidate struct {
	WorkspaceRoot, ProjectID string
	Project                  *Project
	Worktree                 Worktree
}
type WorktreeArchivePlan struct {
	Threshold                                time.Duration
	Considered                               int
	Eligible                                 []worktreeCandidate
	Recent, Main, Dirty, Protected, Unpushed int
}

func (m *Model) openLifecycle(scope lifecycleScope) {
	m.lifecycle = &lifecycleModel{scope: scope}
	m.mode = viewLifecycle
}
func (m *Model) openWorktreeArchive(p *Project, wt *Worktree) {
	m.openWorktreeArchiveMany(p, []*Worktree{wt})
}
func (m *Model) openWorktreeArchiveMany(p *Project, worktrees []*Worktree) {
	m.openLifecycle(lifecycleScope{kind: lifecycleWorktree, project: p, worktree: worktrees[0], worktrees: worktrees})
	m.lifecycle.action = lifecycleArchiveWorktree
	m.prepareLifecycle()
}
func (m *Model) openWorktreeDelete(p *Project, wt *Worktree) {
	m.openWorktreeDeleteMany(p, []*Worktree{wt})
}
func (m *Model) openWorktreeDeleteMany(p *Project, worktrees []*Worktree) {
	m.openLifecycle(lifecycleScope{kind: lifecycleWorktree, project: p, worktree: worktrees[0], worktrees: worktrees})
	m.lifecycle.action = lifecycleDeleteWorktree
	m.prepareLifecycle()
}
func (m *Model) prepareLifecycle() {
	lm := m.lifecycle
	switch lm.action {
	case lifecycleArchiveProjects:
		lm.phase = lifecycleReview
		lm.message = fmt.Sprintf("Archive %d project(s)? Files remain untouched.", len(m.lifecycleProjects(lm.scope)))
	case lifecycleArchiveOldWorktrees:
		lm.phase = lifecycleThreshold
	case lifecycleArchiveWorktree:
		if err := validateWorktreeTargets(lm.scope.project, lm.scope.worktrees); err != nil {
			lm.phase, lm.errorText = lifecycleResult, err.Error()
		} else {
			lm.phase = lifecycleReview
			lm.message = fmt.Sprintf("Archive %d checkouts and preserve branches?", len(lm.scope.worktrees))
			if len(lm.scope.worktrees) == 1 {
				lm.message = "Archive checkout and preserve branches?"
			}
			if dirtyWorktreeCount(lm.scope.worktrees) > 0 {
				lm.message = "WARNING: uncommitted changes will be discarded. Archive checkout and preserve branches?"
				if len(lm.scope.worktrees) > 1 {
					lm.message = fmt.Sprintf("WARNING: %d selected checkouts have uncommitted changes that will be discarded. Archive %d checkouts and preserve branches?", dirtyWorktreeCount(lm.scope.worktrees), len(lm.scope.worktrees))
				}
			}
		}
	case lifecycleDeleteWorktree:
		if err := validateWorktreeTargets(lm.scope.project, lm.scope.worktrees); err != nil {
			lm.phase, lm.errorText = lifecycleResult, err.Error()
		} else {
			lm.phase = lifecycleReview
			lm.message = fmt.Sprintf("Delete %d checkouts and their local/remote branches?", len(lm.scope.worktrees))
			if len(lm.scope.worktrees) == 1 {
				lm.message = "Delete checkout and local/remote branches?"
			}
			if dirtyWorktreeCount(lm.scope.worktrees) > 0 {
				lm.message = "WARNING: uncommitted changes will be discarded. Delete checkout and local/remote branches?"
				if len(lm.scope.worktrees) > 1 {
					lm.message = fmt.Sprintf("WARNING: %d selected checkouts have uncommitted changes that will be discarded. Delete %d checkouts and their local/remote branches?", dirtyWorktreeCount(lm.scope.worktrees), len(lm.scope.worktrees))
				}
			}
		}
	}
}

func validateWorktreeTargets(p *Project, worktrees []*Worktree) error {
	if len(worktrees) == 0 {
		return fmt.Errorf("no worktrees selected")
	}
	for _, wt := range worktrees {
		if err := validateWorktreeTarget(p, wt); err != nil {
			return err
		}
	}
	return nil
}

func dirtyWorktreeCount(worktrees []*Worktree) int {
	dirty := 0
	for _, wt := range worktrees {
		if worktreeDirty(wt) {
			dirty++
		}
	}
	return dirty
}
func (m *Model) lifecycleProjects(scope lifecycleScope) []worktreeCandidate {
	var out []worktreeCandidate
	for wi := range m.workspaces {
		ws := &m.workspaces[wi]
		for pi := range ws.Projects {
			p := &ws.Projects[pi]
			if scope.kind == lifecycleProject && (scope.project == nil || p.WorkspaceRoot != scope.project.WorkspaceRoot || p.ID != scope.project.ID) {
				continue
			}
			if scope.kind == lifecycleGroup && (ws.Root != scope.workspaceRoot || p.Group != scope.group) {
				continue
			}
			out = append(out, worktreeCandidate{WorkspaceRoot: ws.Root, ProjectID: p.ID, Project: p})
		}
	}
	return out
}
func BuildWorktreeArchivePlan(projects []worktreeCandidate, threshold time.Duration, now time.Time, load func(string) ([]Worktree, error)) WorktreeArchivePlan {
	plan := WorktreeArchivePlan{Threshold: threshold}
	cutoff := now.Add(-threshold)
	for _, p := range projects {
		wts, _ := load(p.Project.Path)
		for _, wt := range wts {
			plan.Considered++
			c := p
			c.Worktree = wt
			switch {
			case wt.IsMain:
				plan.Main++
			case wt.Dirty:
				plan.Dirty++
			case protectedBranch(p.Project, wt.Branch):
				plan.Protected++
			case !worktreePublishedFresh(p.Project, &wt):
				plan.Unpushed++
			default:
				recent := wt.LastActiveAt
				if a := p.Project.BranchActivity[wt.Branch]; a.After(recent) {
					recent = a
				}
				if recent.IsZero() || !recent.Before(cutoff) {
					plan.Recent++
				} else {
					plan.Eligible = append(plan.Eligible, c)
				}
			}
		}
	}
	return plan
}

type branchPublication struct{ LocalOID, RemoteOID string }

func refreshWorktreePublication(p *Project, wt *Worktree) (branchPublication, error) {
	if p == nil || wt == nil || wt.Branch == "" {
		return branchPublication{}, fmt.Errorf("worktree branch is unavailable")
	}
	bare := layout.BarePath(p.Path)
	remote, err := git.FetchRemoteBranch(bare, "origin", wt.Branch)
	if err != nil {
		return branchPublication{}, err
	}
	local := git.RevParse(bare, "refs/heads/"+wt.Branch)
	ahead, _, exists := git.AheadBehindRemote(bare, wt.Branch, "origin")
	if local == "" || !exists || ahead != 0 {
		return branchPublication{}, fmt.Errorf("branch is local-only or has %d unpushed commit(s)", ahead)
	}
	return branchPublication{local, remote}, nil
}
func worktreePublishedFresh(p *Project, wt *Worktree) bool {
	_, err := refreshWorktreePublication(p, wt)
	return err == nil
}
func validateWorktreeTarget(p *Project, wt *Worktree) error {
	if p == nil || wt == nil || wt.IsMain {
		return fmt.Errorf("cannot archive main worktree")
	}
	return nil
}

func worktreeDirty(wt *Worktree) bool {
	return wt != nil && (wt.Dirty || git.IsDirty(wt.Path))
}

type WorktreeArchiveResult struct {
	CheckoutRemoved, MetadataReleased bool
	ProjectPath                       string
	Err                               error
}

func ArchiveWorktree(p *Project, wt *Worktree, root string, force bool) WorktreeArchiveResult {
	liveProject, liveWorktree, err := revalidateLifecycleWorktree(root, p, wt)
	if err != nil {
		return WorktreeArchiveResult{Err: err}
	}
	if err := validateWorktreeTarget(liveProject, liveWorktree); err != nil {
		return WorktreeArchiveResult{Err: err}
	}
	if !force && worktreeDirty(liveWorktree) {
		return WorktreeArchiveResult{Err: fmt.Errorf("cannot archive dirty worktree")}
	}
	if err := git.WorktreeRemove(layout.BarePath(liveProject.Path), liveWorktree.Path, force); err != nil {
		return WorktreeArchiveResult{Err: err}
	}
	r := WorktreeArchiveResult{CheckoutRemoved: true, ProjectPath: liveProject.Path}
	r.Err = releaseWorktreeOwnership(root, liveProject.ID, liveWorktree.Branch)
	r.MetadataReleased = r.Err == nil
	return r
}

type WorktreeArchiveExecution struct {
	Archived, Skipped, Failed int
	Details                   []string
	RemovedProjectPaths       []string
	AffectedProjects          []ProjectIdentity
}

func ExecuteWorktreeArchivePlan(plan WorktreeArchivePlan) WorktreeArchiveExecution {
	var r WorktreeArchiveExecution
	for _, c := range plan.Eligible {
		fresh, reason := revalidateWorktreeArchiveCandidate(c, plan.Threshold, time.Now())
		if reason != "" {
			r.Skipped++
			r.Details = append(r.Details, c.Project.Name+"/"+c.Worktree.Branch+": "+reason)
			continue
		}
		a := ArchiveWorktree(c.Project, fresh, c.WorkspaceRoot, false)
		if a.CheckoutRemoved {
			r.RemovedProjectPaths = append(r.RemovedProjectPaths, a.ProjectPath)
			r.AffectedProjects = append(r.AffectedProjects, ProjectIdentity{c.WorkspaceRoot, c.ProjectID})
		}
		if a.Err != nil {
			r.Failed++
			detail := a.Err.Error()
			if a.CheckoutRemoved {
				detail = "checkout removed / ownership metadata remains: " + detail
			}
			r.Details = append(r.Details, detail)
		} else {
			r.Archived++
		}
	}
	return r
}
func revalidateWorktreeArchiveCandidate(c worktreeCandidate, threshold time.Duration, now time.Time) (*Worktree, string) {
	wts, err := LoadWorktrees(c.Project.Path)
	if err != nil {
		return nil, err.Error()
	}
	var fresh *Worktree
	for i := range wts {
		if wts[i].Path == c.Worktree.Path {
			fresh = &wts[i]
			break
		}
	}
	if fresh == nil {
		return nil, "worktree is missing"
	}
	if fresh.Branch != c.Worktree.Branch {
		return nil, "checkout branch changed after review"
	}
	if fresh.Dirty {
		return nil, "worktree became dirty"
	}
	ws, err := config.Load(c.WorkspaceRoot)
	if err != nil {
		return nil, "registry reload failed: " + err.Error()
	}
	p, ok := ws.Projects[c.ProjectID]
	if !ok {
		return nil, "project missing"
	}
	if protectedBranch(&Project{DefaultBranch: p.DefaultBranch}, fresh.Branch) {
		return nil, "worktree branch is protected"
	}
	recent := fresh.LastActiveAt
	if a := branchActivity(p.Branches)[fresh.Branch]; a.After(recent) {
		recent = a
	}
	if recent.IsZero() || !recent.Before(now.Add(-threshold)) {
		return nil, "worktree is now recent"
	}
	if _, err := refreshWorktreePublication(c.Project, fresh); err != nil {
		return nil, err.Error()
	}
	return fresh, ""
}

type WorktreeDeleteResult struct {
	CheckoutRemoved, BranchesDeleted, MetadataReleased bool
	Message, Detail                                    string
}

func DeleteWorktreeDestructive(p *Project, wt *Worktree, root string) WorktreeDeleteResult {
	liveProject, liveWorktree, err := revalidateLifecycleWorktree(root, p, wt)
	if err != nil {
		return WorktreeDeleteResult{Message: "Checkout unchanged.", Detail: err.Error()}
	}
	if err := validateWorktreeTarget(liveProject, liveWorktree); err != nil {
		return WorktreeDeleteResult{Message: "Checkout unchanged.", Detail: err.Error()}
	}
	bare := layout.BarePath(liveProject.Path)
	localOID := git.RevParse(bare, "refs/heads/"+liveWorktree.Branch)
	if localOID == "" {
		return WorktreeDeleteResult{Message: "Checkout unchanged.", Detail: "local branch is unavailable"}
	}
	remoteOID := git.RevParse(bare, "refs/remotes/origin/"+liveWorktree.Branch)
	if err := git.WorktreeRemove(bare, liveWorktree.Path, true); err != nil {
		return WorktreeDeleteResult{Message: "Checkout unchanged.", Detail: err.Error()}
	}
	result := WorktreeDeleteResult{CheckoutRemoved: true}
	if git.RevParse(bare, "refs/heads/"+liveWorktree.Branch) != localOID {
		result.Message = "Checkout removed; local and remote branches remain."
		result.Detail = "local branch changed after verification"
		return result
	}
	var details []string
	remoteDeleted := remoteOID == ""
	if remoteOID != "" {
		if err := git.DeleteRemoteBranch(bare, "origin", liveWorktree.Branch, remoteOID); err != nil {
			details = append(details, "remote branch remains: "+err.Error())
		} else {
			remoteDeleted = true
		}
	}
	localDeleted := false
	if err := git.DeleteLocalBranch(bare, liveWorktree.Branch, localOID); err != nil {
		details = append(details, "local branch remains: "+err.Error())
	} else {
		localDeleted = true
	}
	if err := releaseWorktreeOwnership(root, liveProject.ID, liveWorktree.Branch); err != nil {
		details = append(details, "ownership metadata remains: "+err.Error())
	} else {
		result.MetadataReleased = true
	}
	result.BranchesDeleted = localDeleted && remoteDeleted
	if localDeleted && remoteDeleted && len(details) == 0 {
		result.Message = "Deleted checkout and branches."
		return result
	}
	result.Message = "Checkout deleted; some branch state remains."
	result.Detail = strings.Join(details, "; ")
	return result
}

func revalidateLifecycleWorktree(root string, reviewedProject *Project, reviewedWorktree *Worktree) (*Project, *Worktree, error) {
	if reviewedProject == nil || reviewedWorktree == nil {
		return nil, nil, fmt.Errorf("worktree is unavailable")
	}
	ws, err := config.Load(root)
	if err != nil {
		return nil, nil, err
	}
	project, ok := ws.Projects[reviewedProject.ID]
	if !ok {
		return nil, nil, fmt.Errorf("project missing")
	}
	mainPath, err := layout.ProjectPath(root, project.Path)
	if err != nil {
		return nil, nil, err
	}
	if filepath.Clean(mainPath) != filepath.Clean(reviewedProject.Path) {
		return nil, nil, fmt.Errorf("project path changed after review")
	}
	liveProject := &Project{ID: reviewedProject.ID, Name: reviewedProject.Name, WorkspaceRoot: root, Path: mainPath, DefaultBranch: project.DefaultBranch, BranchActivity: branchActivity(project.Branches)}
	worktrees, err := LoadWorktrees(mainPath)
	if err != nil {
		return nil, nil, err
	}
	for i := range worktrees {
		if filepath.Clean(worktrees[i].Path) == filepath.Clean(reviewedWorktree.Path) {
			if worktrees[i].Branch != reviewedWorktree.Branch {
				return nil, nil, fmt.Errorf("checkout branch changed after review")
			}
			return liveProject, &worktrees[i], nil
		}
	}
	return nil, nil, fmt.Errorf("worktree is missing")
}
func (m *Model) updateLifecycle(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	lm := m.lifecycle
	key := msg.String()
	if key == "esc" {
		m.closeLifecycle()
		return m, nil
	}
	if lm.phase == lifecycleReview || lm.phase == lifecycleResult {
		rows := m.lifecycleBodyRows()
		maxScroll := max(0, len(m.lifecycleBody())-rows)
		switch key {
		case "j", "down":
			lm.scroll = min(maxScroll, lm.scroll+1)
			return m, nil
		case "k", "up":
			lm.scroll = max(0, lm.scroll-1)
			return m, nil
		case "g", "home":
			lm.scroll = 0
			return m, nil
		case "G", "end":
			lm.scroll = maxScroll
			return m, nil
		case "ctrl+d", "ctrl+f", "pgdn":
			lm.scroll = min(maxScroll, lm.scroll+max(1, rows/2))
			return m, nil
		case "ctrl+u", "ctrl+b", "pgup":
			lm.scroll = max(0, lm.scroll-max(1, rows/2))
			return m, nil
		}
	}
	switch lm.phase {
	case lifecycleSelect:
		m.updateLifecycleSelect(key)
	case lifecycleThreshold:
		m.updateLifecycleThreshold(msg)
	case lifecycleReview:
		switch key {
		case "enter", "y":
			m.executeLifecycle()
		case "n":
			m.closeLifecycle()
		}
	}
	return m, nil
}

func (m *Model) closeLifecycle() {
	parent := m.lifecycle.parentSheet
	if parent != nil {
		parent.pendingDel = nil
	}
	m.lifecycle = nil
	m.mode = viewList
	m.sheet = parent
	if parent != nil {
		parent.rebuild(m)
	}
}

func (m *Model) updateLifecycleSelect(key string) {
	switch key {
	case "a", "1":
		m.lifecycle.action = lifecycleArchiveProjects
		m.prepareLifecycle()
	case "w", "2":
		m.lifecycle.action = lifecycleArchiveOldWorktrees
		m.prepareLifecycle()
	}
}

func (m *Model) updateLifecycleThreshold(msg tui.KeyMsg) {
	lm := m.lifecycle
	key := msg.String()
	if key == "backspace" && len(lm.input) > 0 {
		lm.input = lm.input[:len(lm.input)-1]
	} else if key == "enter" {
		d, err := ParseArchiveThreshold(lm.input)
		if err != nil {
			lm.errorText = err.Error()
			return
		}
		lm.plan = BuildWorktreeArchivePlan(m.lifecycleProjects(lm.scope), d, time.Now(), LoadWorktrees)
		lm.phase = lifecycleReview
	} else if len(msg.Runes) > 0 {
		lm.input += string(msg.Runes)
	}
}

func (m *Model) executeLifecycle() {
	lm := m.lifecycle
	switch lm.action {
	case lifecycleArchiveProjects:
		r := ArchiveProjects(m.lifecycleProjects(lm.scope))
		m.removeArchivedProjects(r.Succeeded)
		if len(r.Succeeded) > 0 {
			lm.parentSheet = nil
		}
		lm.message = fmt.Sprintf("Archived %d project(s); %d failed.", len(r.Succeeded), len(r.Failures))
		lm.errorText = strings.Join(r.Failures, "; ")
	case lifecycleArchiveWorktree:
		root := m.workspaceRootFor(lm.scope.project)
		archived, partial, failed := 0, 0, 0
		var details []string
		for _, wt := range lm.scope.worktrees {
			r := ArchiveWorktree(lm.scope.project, wt, root, true)
			if r.CheckoutRemoved {
				m.wtCache.Invalidate(lm.scope.project.Path)
			}
			if r.Err != nil {
				if r.CheckoutRemoved {
					partial++
					details = append(details, worktreeDisplayName(*wt)+": partial: checkout removed; metadata remains: "+r.Err.Error())
				} else {
					failed++
					details = append(details, worktreeDisplayName(*wt)+": unchanged: "+r.Err.Error())
				}
			} else {
				archived++
				details = append(details, worktreeDisplayName(*wt)+": archived")
			}
		}
		if len(lm.scope.worktrees) == 1 && failed == 0 && partial == 0 {
			lm.message = "Archived worktree; branch preserved."
		} else {
			lm.message = fmt.Sprintf("Archived %d worktree(s); %d partial; %d failed.", archived, partial, failed)
		}
		lm.details = details
		if archived+partial > 0 {
			m.appendMetadataRefreshError(lm, ProjectIdentity{root, lm.scope.project.ID})
		}
	case lifecycleArchiveOldWorktrees:
		r := ExecuteWorktreeArchivePlan(lm.plan)
		for _, path := range r.RemovedProjectPaths {
			m.wtCache.Invalidate(path)
		}
		lm.message = fmt.Sprintf("Archived %d, skipped %d, failed %d.", r.Archived, r.Skipped, r.Failed)
		lm.errorText = strings.Join(r.Details, "; ")
		seen := map[ProjectIdentity]bool{}
		for _, id := range r.AffectedProjects {
			if !seen[id] {
				m.appendMetadataRefreshError(lm, id)
				seen[id] = true
			}
		}
	case lifecycleDeleteWorktree:
		root := m.workspaceRootFor(lm.scope.project)
		checkoutRemoved := false
		if len(lm.scope.worktrees) == 1 {
			r := DeleteWorktreeDestructive(lm.scope.project, lm.scope.worktree, root)
			lm.message, lm.errorText = r.Message, r.Detail
			checkoutRemoved = r.CheckoutRemoved
		} else {
			deleted, partial, failed := 0, 0, 0
			var details []string
			for _, wt := range lm.scope.worktrees {
				r := DeleteWorktreeDestructive(lm.scope.project, wt, root)
				checkoutRemoved = checkoutRemoved || r.CheckoutRemoved
				switch {
				case r.CheckoutRemoved && r.BranchesDeleted && r.MetadataReleased:
					deleted++
					details = append(details, worktreeDisplayName(*wt)+": deleted")
				case r.CheckoutRemoved:
					partial++
					details = append(details, worktreeDisplayName(*wt)+": partial: "+r.Detail)
				default:
					failed++
					details = append(details, worktreeDisplayName(*wt)+": unchanged: "+r.Detail)
				}
			}
			lm.message = fmt.Sprintf("Deleted %d worktree(s); %d partial; %d failed.", deleted, partial, failed)
			lm.details = details
		}
		m.wtCache.Invalidate(lm.scope.project.Path)
		if checkoutRemoved {
			m.appendMetadataRefreshError(lm, ProjectIdentity{root, lm.scope.project.ID})
		}
	}
	lm.phase = lifecycleResult
	lm.scroll = 0
	m.rebuildItems()
}

func (m *Model) appendMetadataRefreshError(lm *lifecycleModel, id ProjectIdentity) {
	if err := m.reloadProjectMetadata(id.WorkspaceRoot, id.ProjectID); err != nil {
		if lm.errorText != "" {
			lm.errorText += "; "
		}
		lm.errorText += "metadata refresh failed: " + err.Error()
	}
}
func (m *Model) removeArchivedProjects(ids []ProjectIdentity) {
	set := map[ProjectIdentity]bool{}
	for _, id := range ids {
		set[id] = true
	}
	for wi := range m.workspaces {
		kept := make([]Project, 0, len(m.workspaces[wi].Projects))
		for _, p := range m.workspaces[wi].Projects {
			if !set[ProjectIdentity{m.workspaces[wi].Root, p.ID}] {
				kept = append(kept, p)
			}
		}
		m.workspaces[wi].Projects = kept
	}
}
