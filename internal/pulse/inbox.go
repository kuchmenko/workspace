package pulse

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
	"github.com/kuchmenko/workspace/internal/layout"
)

// InboxCommit is one commit on the local machine that exists ahead of
// its upstream — i.e. something the user wrote and committed but has
// not yet pushed.
type InboxCommit struct {
	SHA     string
	Subject string
	Author  string
	Time    time.Time
}

// InboxEntry is one worktree with at least one unpushed commit.
type InboxEntry struct {
	Project   string
	Branch    string
	Worktree  string // absolute path
	Ahead     int
	NoUpstream bool // branch has no upstream tracking ref at all
	Commits   []InboxCommit
}

// InboxSnapshot is the result of one inbox scan. Read-only — the
// inbox tab does not mutate git.
type InboxSnapshot struct {
	GeneratedAt time.Time
	Total       int // sum of Ahead across all entries
	Entries     []InboxEntry
	CollectedIn time.Duration
}

// CollectInbox walks every active project in ws, lists its worktrees,
// and for each worktree with unpushed commits builds an InboxEntry.
// Strictly local: no network, no GitHub. The point is "what did I
// forget to push from this machine, possibly from another sitting".
//
// Worktrees on a branch with no upstream are still surfaced (with
// NoUpstream=true) so the user sees them — those are usually the
// freshly-created wt/<machine>/<topic> branches that haven't had
// their first push yet.
func CollectInbox(ws *config.Workspace, wsRoot string) InboxSnapshot {
	start := time.Now()
	snap := InboxSnapshot{GeneratedAt: start}

	for name, proj := range ws.Projects {
		if proj.Status != config.StatusActive {
			continue
		}
		mainPath := filepath.Join(wsRoot, proj.Path)
		barePath := layout.BarePath(mainPath)
		wts, err := git.WorktreeList(barePath)
		if err != nil {
			continue
		}
		for _, wt := range wts {
			if wt.Bare || wt.Detached || wt.Branch == "" {
				continue
			}
			entry := scanWorktree(name, wt.Path, wt.Branch)
			if entry == nil {
				continue
			}
			snap.Entries = append(snap.Entries, *entry)
			snap.Total += entry.Ahead
		}
	}

	// Sort by Ahead desc, then Project asc, then Branch asc — most
	// "in danger of forgetting" floats to the top.
	sort.Slice(snap.Entries, func(i, j int) bool {
		a, b := snap.Entries[i], snap.Entries[j]
		if a.Ahead != b.Ahead {
			return a.Ahead > b.Ahead
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.Branch < b.Branch
	})
	snap.CollectedIn = time.Since(start)
	return snap
}

// scanWorktree returns an InboxEntry if the worktree has unpushed
// commits, OR if it's a wt/* branch with no upstream (i.e. a fresh
// `ws worktree new` that hasn't had its first push yet — exactly the
// thing pulse.Inbox should remind you about).
//
// Default branches (main/master/dev/...) without upstream are skipped:
// in the bare+worktree layout these never have an `origin/<default>`
// ref configured, but they're also never the user's WIP — they're
// the synchronization target. Showing them as "no upstream" entries
// would just clutter the inbox with 50+ irrelevant rows.
func scanWorktree(project, wtPath, branch string) *InboxEntry {
	if !git.HasUpstream(wtPath, branch) {
		// Only WIP branches (wt/*) without upstream are interesting.
		if !strings.HasPrefix(branch, "wt/") {
			return nil
		}
		return &InboxEntry{
			Project:    project,
			Branch:     branch,
			Worktree:   wtPath,
			NoUpstream: true,
		}
	}
	ahead, _, _ := git.AheadBehind(wtPath, branch)
	if ahead == 0 {
		return nil
	}
	commits := readUnpushed(wtPath)
	return &InboxEntry{
		Project:  project,
		Branch:   branch,
		Worktree: wtPath,
		Ahead:    ahead,
		Commits:  commits,
	}
}

// readUnpushed runs `git log @{u}..HEAD` and parses the output. The
// format is a NUL-delimited per-record stream so we can safely embed
// commit subjects with any character. On any error returns nil — the
// caller still has the Ahead count from AheadBehind.
func readUnpushed(wtPath string) []InboxCommit {
	cmd := exec.Command("git", "-C", wtPath, "log",
		"@{u}..HEAD", "--pretty=format:%h%x1f%s%x1f%an%x1f%cI%x00")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var commits []InboxCommit
	for _, rec := range strings.Split(string(out), "\x00") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x1f", 4)
		if len(parts) != 4 {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, parts[3])
		commits = append(commits, InboxCommit{
			SHA:     parts[0],
			Subject: parts[1],
			Author:  parts[2],
			Time:    ts,
		})
	}
	return commits
}
