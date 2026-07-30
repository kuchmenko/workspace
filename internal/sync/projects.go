package sync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/conflict"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

func (r *Runner) syncProject(name string, project *config.Project, machine string, touched *bool) error {
	mainPath := filepath.Join(r.root, project.Path)
	barePath := layout.BarePath(mainPath)
	state, diagnostic := classifyProject(mainPath, barePath)
	planned := ProjectPlan{
		Name:       name,
		State:      state,
		MainPath:   mainPath,
		BarePath:   barePath,
		Diagnostic: diagnostic,
		Snapshot:   snapshotProject(*project),
		MirrorURLs: make(map[string]string),
	}
	base := projectNetworkBase(r.root, planned)
	planned.OriginURL, _ = git.ResolveRemoteURL(project.Remote, base)
	selectedMirrors := make(map[string]bool, len(project.Mirrors))
	for mirror, url := range project.Mirrors {
		selectedMirrors[mirror] = true
		planned.MirrorURLs[mirror], _ = git.ResolveRemoteURL(url, base)
	}
	result := r.syncPlannedProject(context.Background(), planned, project, machine, touched, selectedMirrors, nil, nil)
	if result.Status == ResultFailed {
		return errors.New(result.Diagnostic)
	}
	return nil
}

func (r *Runner) syncPlannedProject(ctx context.Context, planned ProjectPlan, project *config.Project, machine string, touched *bool, selectedMirrors map[string]bool, report *Report, onEvent func(Event)) OperationResult {
	switch planned.State {
	case ProjectMissing:
		return r.cloneMissingContext(ctx, planned, project, touched, selectedMirrors, report, onEvent)
	case ProjectNeedsMigration:
		r.recordProjectConflict(planned.Name, "", conflict.KindNeedsMigration, planned.Diagnostic)
		r.addConflictResult(report, planned.Name, "", conflict.KindNeedsMigration, planned.Diagnostic, onEvent)
		return OperationResult{Status: ResultSkipped, Operation: "project-sync", Project: planned.Name, Reason: SkipState, Diagnostic: planned.Diagnostic}
	case ProjectBlocked:
		r.recordProjectConflict(planned.Name, "", conflict.KindPathBlocked, planned.Diagnostic)
		r.addConflictResult(report, planned.Name, "", conflict.KindPathBlocked, planned.Diagnostic, onEvent)
		return OperationResult{Status: ResultSkipped, Operation: "project-sync", Project: planned.Name, Reason: SkipState, Diagnostic: planned.Diagnostic}
	}
	return r.syncPresentProject(ctx, planned, project, machine, touched, selectedMirrors, report, onEvent)
}

func (r *Runner) syncPresentProject(ctx context.Context, planned ProjectPlan, project *config.Project, machine string, touched *bool, selectedMirrors map[string]bool, report *Report, onEvent func(Event)) OperationResult {
	if err := exactOrigin(planned.BarePath, planned.Snapshot.Remote); err != nil {
		return OperationResult{Status: ResultSkipped, Operation: "project-sync", Project: planned.Name, Reason: SkipPlanChanged, Diagnostic: err.Error()}
	}
	if !git.HasFetchRefspec(planned.BarePath) {
		if err := git.SetFetchRefspec(planned.BarePath); err != nil {
			return failedProject(planned.Name, "repair-fetch-refspec", err)
		}
	}
	if err := git.FetchURLContext(ctx, planned.BarePath, planned.OriginURL); err != nil {
		return failedProject(planned.Name, "fetch", err)
	}
	r.syncMirrorsContext(ctx, planned.Name, project, planned.BarePath, planned.MirrorURLs, selectedMirrors, report, onEvent)
	if err := ctx.Err(); err != nil {
		return canceledProject(planned.Name, "worktrees", err)
	}
	if report != nil {
		report.start(Event{Project: planned.Name, Operation: "worktrees"}, onEvent)
	}
	worktrees, err := git.WorktreeList(planned.BarePath)
	if err != nil {
		return failedProject(planned.Name, "worktree-list", err)
	}
	for _, worktree := range worktrees {
		if err := ctx.Err(); err != nil {
			return canceledProject(planned.Name, "worktrees", err)
		}
		r.syncWorktreeContext(ctx, planned.Name, project, machine, planned.MainPath, planned.OriginURL, worktree, touched, report, onEvent)
	}
	r.checkOrphanedBranches(planned.Name, project, planned.BarePath, report, onEvent)
	return OperationResult{Status: ResultSuccess, Operation: "project-sync", Project: planned.Name}
}

func failedProject(name, operation string, err error) OperationResult {
	return OperationResult{Status: ResultFailed, Operation: operation, Project: name, Diagnostic: err.Error()}
}

func canceledProject(name, operation string, err error) OperationResult {
	return OperationResult{Status: ResultCanceled, Operation: operation, Project: name, Reason: SkipCanceled, Diagnostic: err.Error()}
}

func (r *Runner) syncWorktreeContext(ctx context.Context, name string, project *config.Project, machine, mainPath, originURL string, worktree git.Worktree, touched *bool, report *Report, onEvent func(Event)) {
	if worktree.Bare || worktree.Detached || worktree.Branch == "" || git.HasIndexLock(worktree.Path) {
		return
	}
	if worktree.Path == mainPath {
		r.syncMainWorktreeContext(ctx, name, originURL, worktree, report, onEvent)
		return
	}
	if machine == "" || project.LookupBranch(worktree.Branch) == nil {
		return
	}
	ahead, _, hasUpstream := git.AheadBehind(worktree.Path, worktree.Branch)
	if hasUpstream && ahead > 0 && project.TouchActive(worktree.Branch, machine, time.Now()) {
		*touched = true
	}
}

func (r *Runner) syncMainWorktreeContext(ctx context.Context, name, originURL string, worktree git.Worktree, report *Report, onEvent func(Event)) {
	if git.IsDirty(worktree.Path) {
		return
	}
	ahead, behind, hasOriginBranch := git.AheadBehindRemote(worktree.Path, worktree.Branch, "origin")
	if !hasOriginBranch {
		return
	}
	if behind > 0 && ahead == 0 {
		if err := git.FastForwardURLBranchContext(ctx, worktree.Path, originURL, worktree.Branch); err != nil {
			if ctx.Err() != nil {
				return
			}
			r.recordProjectConflict(name, worktree.Branch, conflict.KindMainDivergence, err.Error())
			r.addConflictResult(report, name, worktree.Branch, conflict.KindMainDivergence, err.Error(), onEvent)
			return
		}
		_ = r.clearProjectConflict(name, worktree.Branch, conflict.KindMainDivergence)
		return
	}
	if ahead > 0 && behind > 0 {
		diagnostic := fmt.Sprintf("ahead %d, behind %d; main worktree should not be diverged", ahead, behind)
		r.recordProjectConflict(name, worktree.Branch, conflict.KindMainDivergence, diagnostic)
		r.addConflictResult(report, name, worktree.Branch, conflict.KindMainDivergence, diagnostic, onEvent)
	}
}

func (r *Runner) checkOrphanedBranches(name string, project *config.Project, barePath string, report *Report, onEvent func(Event)) {
	for _, branch := range project.Branches {
		if branch.LastPushedAt == "" || git.HasRemoteBranch(barePath, "origin", branch.Name) {
			_ = r.clearProjectConflict(name, branch.Name, conflict.KindBranchOrphan)
			continue
		}
		diagnostic := fmt.Sprintf("origin ref refs/remotes/origin/%s missing post-fetch (last pushed by %s at %s)", branch.Name, branch.LastPushedMachine, branch.LastPushedAt)
		r.recordProjectConflict(name, branch.Name, conflict.KindBranchOrphan, diagnostic)
		r.addConflictResult(report, name, branch.Name, conflict.KindBranchOrphan, diagnostic, onEvent)
	}
}

func (r *Runner) cloneMissingContext(ctx context.Context, planned ProjectPlan, project *config.Project, touched *bool, selectedMirrors map[string]bool, report *Report, onEvent func(Event)) OperationResult {
	cloneProject := *project
	cloneProject.Remote = planned.OriginURL
	cloneProject.Mirrors = nil
	result, err := git.CloneIntoLayoutContext(ctx, r.root, planned.Name, &cloneProject, git.CloneOptions{Logf: r.logger.Printf})
	if err != nil {
		return r.cloneFailure(planned.Name, err, report, onEvent)
	}
	_ = r.clearProjectConflict(planned.Name, "", conflict.KindCloneFailed)
	_ = r.clearProjectConflict(planned.Name, "", conflict.KindNeedsBootstrap)
	if project.DefaultBranch != result.DefaultBranch {
		project.DefaultBranch = result.DefaultBranch
	}
	if err := git.SetRemoteURL(planned.BarePath, planned.Snapshot.Remote); err != nil {
		return failedProject(planned.Name, "restore-origin-config", err)
	}
	*touched = true
	if err := exactOrigin(planned.BarePath, planned.Snapshot.Remote); err != nil {
		return OperationResult{Status: ResultSkipped, Operation: "clone", Project: planned.Name, Reason: SkipPlanChanged, Diagnostic: err.Error()}
	}
	if err := git.FetchURLContext(ctx, planned.BarePath, planned.OriginURL); err != nil {
		return failedProject(planned.Name, "fetch", err)
	}
	r.syncMirrorsContext(ctx, planned.Name, project, planned.BarePath, planned.MirrorURLs, selectedMirrors, report, onEvent)
	if err := ctx.Err(); err != nil {
		return canceledProject(planned.Name, "mirrors", err)
	}
	return OperationResult{Status: ResultSuccess, Operation: "clone", Project: planned.Name}
}

func exactOrigin(repository, expected string) error {
	actual, err := git.ConfiguredRemoteURL(repository, "origin")
	if err != nil || !git.RemoteBindingExact(repository, "origin", expected) {
		return fmt.Errorf("origin changed after preflight: got %q, want %q", git.RedactRemote(actual), git.RedactRemote(expected))
	}
	return nil
}

func (r *Runner) cloneFailure(name string, err error, report *Report, onEvent func(Event)) OperationResult {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return canceledProject(name, "clone", err)
	}
	kind := conflict.KindCloneFailed
	diagnostic := err.Error()
	switch {
	case errors.Is(err, git.ErrNeedsBootstrap):
		kind = conflict.KindNeedsBootstrap
		diagnostic = "default branch could not be auto-detected; run `ws bootstrap " + name + "`"
	case errors.Is(err, git.ErrPathBlocked):
		kind = conflict.KindPathBlocked
		diagnostic = "non-repo files at project path; clean up manually and re-run"
	case errors.Is(err, git.ErrNeedsMigration), errors.Is(err, git.ErrAlreadyCloned):
		return OperationResult{Status: ResultSkipped, Operation: "clone", Project: name, Reason: SkipState, Diagnostic: err.Error()}
	}
	r.recordProjectConflict(name, "", kind, diagnostic)
	r.addConflictResult(report, name, "", kind, diagnostic, onEvent)
	if kind != conflict.KindCloneFailed {
		return OperationResult{Status: ResultSkipped, Operation: "clone", Project: name, Reason: SkipState, Diagnostic: diagnostic}
	}
	return failedProject(name, "clone", err)
}

func (r *Runner) addConflictResult(report *Report, project, branch string, kind conflict.Kind, diagnostic string, onEvent func(Event)) {
	if report == nil {
		return
	}
	result := OperationResult{Status: ResultFailed, Operation: string(kind), Project: project, Branch: branch, Diagnostic: diagnostic}
	report.Conflicts = append(report.Conflicts, result)
	report.add(Event{Kind: EventConflict, Status: ResultFailed, Project: project, Branch: branch, Operation: string(kind), Diagnostic: diagnostic}, onEvent)
}
