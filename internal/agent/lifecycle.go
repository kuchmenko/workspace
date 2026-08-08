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
	lifecycleTypedConfirm
	lifecycleResult
)

type lifecycleScope struct {
	kind                 lifecycleScopeKind
	project              *Project
	group, workspaceRoot string
	worktree             *Worktree
}
type lifecycleModel struct {
	scope                     lifecycleScope
	action                    lifecycleAction
	phase                     lifecyclePhase
	input, errorText, message string
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
	m.openLifecycle(lifecycleScope{kind: lifecycleWorktree, project: p, worktree: wt})
	m.lifecycle.action = lifecycleArchiveWorktree
	m.prepareLifecycle()
}
func (m *Model) openWorktreeDelete(p *Project, wt *Worktree) {
	m.openLifecycle(lifecycleScope{kind: lifecycleWorktree, project: p, worktree: wt})
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
		if err := validateArchiveWorktree(lm.scope.project, lm.scope.worktree); err != nil {
			lm.phase, lm.errorText = lifecycleResult, err.Error()
		} else {
			lm.phase = lifecycleReview
			lm.message = "Archive checkout and preserve branches?"
		}
	case lifecycleDeleteWorktree:
		if err := validateDeleteWorktree(lm.scope.project, lm.scope.worktree); err != nil {
			lm.phase, lm.errorText = lifecycleResult, err.Error()
		} else {
			lm.phase = lifecycleTypedConfirm
			lm.message = "Type the exact branch name:"
		}
	}
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
func validateArchiveWorktree(p *Project, wt *Worktree) error {
	if p == nil || wt == nil || wt.IsMain {
		return fmt.Errorf("cannot archive main worktree")
	}
	if wt.Dirty || git.IsDirty(wt.Path) {
		return fmt.Errorf("cannot archive dirty worktree")
	}
	return nil
}

type WorktreeArchiveResult struct {
	CheckoutRemoved, MetadataReleased bool
	ProjectPath                       string
	Err                               error
}

func ArchiveWorktree(p *Project, wt *Worktree, root string) WorktreeArchiveResult {
	liveProject, liveWorktree, err := revalidateLifecycleWorktree(root, p, wt)
	if err != nil {
		return WorktreeArchiveResult{Err: err}
	}
	if err := validateArchiveWorktree(liveProject, liveWorktree); err != nil {
		return WorktreeArchiveResult{Err: err}
	}
	if err := git.WorktreeRemove(layout.BarePath(liveProject.Path), liveWorktree.Path, false); err != nil {
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
		a := ArchiveWorktree(c.Project, fresh, c.WorkspaceRoot)
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
func DeleteWorktreeDestructive(p *Project, wt *Worktree, root string) (string, string) {
	liveProject, liveWorktree, err := revalidateLifecycleWorktree(root, p, wt)
	if err != nil {
		return "checkout unchanged", err.Error()
	}
	if err := validateArchiveWorktree(liveProject, liveWorktree); err != nil {
		return "", err.Error()
	}
	if protectedBranch(liveProject, liveWorktree.Branch) {
		return "", "cannot delete protected branch " + liveWorktree.Branch
	}
	pub, err := refreshWorktreePublication(liveProject, liveWorktree)
	if err != nil {
		return "checkout unchanged", err.Error()
	}
	bare := layout.BarePath(liveProject.Path)
	if err := git.WorktreeRemove(bare, liveWorktree.Path, false); err != nil {
		return "checkout unchanged", err.Error()
	}
	if git.RevParse(bare, "refs/heads/"+liveWorktree.Branch) != pub.LocalOID {
		return "Checkout removed; local and remote branches remain.", "local branch changed after verification"
	}
	if err := git.DeleteRemoteBranch(bare, "origin", liveWorktree.Branch, pub.RemoteOID); err != nil {
		return "Checkout removed; local and remote branches remain.", err.Error()
	}
	if err := git.DeleteLocalBranch(bare, liveWorktree.Branch, pub.LocalOID); err != nil {
		return "Checkout and remote branch removed; local branch remains.", err.Error()
	}
	if err := releaseWorktreeOwnership(root, liveProject.ID, liveWorktree.Branch); err != nil {
		return "Branches removed; ownership metadata remains.", err.Error()
	}
	return "Deleted checkout and branches.", ""
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
	switch lm.phase {
	case lifecycleSelect:
		m.updateLifecycleSelect(key)
	case lifecycleThreshold:
		m.updateLifecycleThreshold(msg)
	case lifecycleTypedConfirm:
		m.updateLifecycleTypedConfirm(msg)
	case lifecycleReview:
		if key == "y" || key == "enter" {
			m.executeLifecycle()
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

func (m *Model) updateLifecycleTypedConfirm(msg tui.KeyMsg) {
	lm := m.lifecycle
	key := msg.String()
	if key == "backspace" && len(lm.input) > 0 {
		lm.input = lm.input[:len(lm.input)-1]
	} else if key == "enter" && lm.input != lm.scope.worktree.Branch {
		lm.errorText = "confirmation must exactly match " + lm.scope.worktree.Branch
	} else if key == "enter" {
		m.executeLifecycle()
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
		r := ArchiveWorktree(lm.scope.project, lm.scope.worktree, root)
		if r.CheckoutRemoved {
			m.wtCache.Invalidate(lm.scope.project.Path)
		}
		if r.Err != nil {
			lm.errorText = r.Err.Error()
			lm.message = "Checkout unchanged."
			if r.CheckoutRemoved {
				lm.message = "Checkout removed; ownership metadata remains."
			}
		} else {
			lm.message = "Archived worktree; branch preserved."
		}
		if r.CheckoutRemoved {
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
		lm.message, lm.errorText = DeleteWorktreeDestructive(lm.scope.project, lm.scope.worktree, root)
		m.wtCache.Invalidate(lm.scope.project.Path)
		if strings.HasPrefix(lm.message, "Deleted checkout") {
			m.appendMetadataRefreshError(lm, ProjectIdentity{root, lm.scope.project.ID})
		}
	}
	lm.phase = lifecycleResult
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
