package agent

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/daemon"
	"github.com/kuchmenko/workspace/internal/git"
)

// StampLaunchFromPath records "this machine just launched into a
// project at `cwd`" by bumping the per-branch activity timestamp in
// workspace.toml. Called immediately before syscall.Exec into claude
// or $SHELL, both from the TUI and from the non-interactive `ws agent
// launch / shell / resume` subcommands.
//
// All failures are best-effort: the launch never fails because of a
// stamp error. The caller logs to stderr and proceeds with exec —
// activity tracking is a UX nicety, never a hard requirement.
//
// Resolution sequence:
//
//  1. Walk up from cwd to find workspace.toml.
//  2. Find the project whose path matches cwd (main worktree exact
//     match OR sibling worktree under `<projPath>-wt-<machine>-...`).
//  3. Read the current branch at cwd via `git rev-parse`.
//  4. Project.StampActivity(branch, machine, now); Save; notify daemon.
//
// Returns nil unless something genuinely surprising fails (a Save
// error after a successful Load). Returning nil does NOT mean a
// stamp happened — many launches are no-ops (e.g. shell into a
// non-workspace path, unrecognized branch).
func StampLaunchFromPath(cwd string) error {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	wsRoot, ok := config.FindRootFrom(abs)
	if !ok {
		return nil
	}
	ws, err := config.Load(wsRoot)
	if err != nil {
		return nil
	}
	projID, proj := findProjectByPath(ws, wsRoot, abs)
	if proj == nil {
		return nil
	}
	branch, err := git.CurrentBranch(abs)
	if err != nil || branch == "" {
		return nil
	}
	machine := loadMachineName()
	if machine == "" {
		return nil
	}
	if !proj.StampActivity(branch, machine, time.Now()) {
		return nil
	}
	ws.Projects[projID] = *proj
	if err := config.Save(wsRoot, ws); err != nil {
		return err
	}
	notifyDaemon(wsRoot)
	return nil
}

// loadMachineName returns the configured machine name, or "" if
// unconfigured. We never prompt here — a missing machine_name means
// the user has not yet run any interactive ws command that would set
// it, and we simply skip the stamp rather than block the launch.
func loadMachineName() string {
	mc, err := config.LoadMachineConfig()
	if err != nil || mc == nil {
		return ""
	}
	return mc.MachineName
}

// findProjectByPath returns the project whose worktree (main or
// sibling) contains `abs`. The match is structural: main worktree if
// abs == join(wsRoot, p.Path) or a subpath of it; sibling worktree
// if abs starts with `<projPath>-wt-`. Returns (id, *Project) on hit,
// ("", nil) on miss. The returned pointer aliases a freshly copied
// Project so the caller can mutate it before writing back to the map.
func findProjectByPath(ws *config.Workspace, wsRoot, abs string) (string, *config.Project) {
	abs = filepath.Clean(abs)
	for id, p := range ws.Projects {
		projPath := filepath.Clean(filepath.Join(wsRoot, p.Path))
		if abs == projPath || strings.HasPrefix(abs, projPath+string(filepath.Separator)) {
			cp := p
			return id, &cp
		}
		wtPrefix := projPath + "-wt-"
		if abs == strings.TrimSuffix(wtPrefix, "-") {
			continue
		}
		if strings.HasPrefix(abs, wtPrefix) {
			cp := p
			return id, &cp
		}
	}
	return "", nil
}

// notifyDaemon shortens the wait until the reconciler observes the
// new workspace.toml. Best-effort: if the daemon is down or the IPC
// socket is missing, the next scheduled tick still picks up the
// change from disk.
func notifyDaemon(wsRoot string) {
	c, err := daemon.Dial()
	if err != nil {
		return
	}
	defer c.Close()
	_ = c.Notify(wsRoot, "config_changed")
}
