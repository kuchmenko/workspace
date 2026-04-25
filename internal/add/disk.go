package add

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/git"
)

// DiskSource walks the workspace's category directories looking for git
// repositories that are not yet registered in workspace.toml. It is the
// successor of `ws scan` (which Track A keeps but Phase 4 deletes once
// this source is wired in).
//
// Behavior is intentionally identical to scan's directory walk:
//
//   - Roots: personal/, work/, playground/, researches/, tools/.
//     Override via DiskSource{Roots: ...} for tests; nil → defaults.
//   - Skip directory entries that are hidden (start with `.`) or carry
//     the worktree-infrastructure suffixes `.bare` / `-wt-*` so we don't
//     report a project's own bare clone or extra worktrees as orphans.
//   - Recurse one level deeper (work/<org>/<repo> shape) when a top-level
//     entry isn't itself a git repo. No deeper — the workspace layout
//     never nests projects beyond two segments.
//
// The source pulls a remote URL via `git remote get-url origin` for each
// found repo; an empty result is OK and surfaces as a Suggestion with
// no RemoteURL (the TUI's edit screen lets the user fill it in).
type DiskSource struct {
	// WsRoot is required.
	WsRoot string

	// Known is the set of `Project.Path` values already in workspace.toml.
	// We match against this set to filter out registered projects.
	Known map[string]bool

	// Roots overrides the default scan directories. nil → DefaultDiskRoots.
	// Useful in tests and on workspaces with non-standard layouts.
	Roots []string
}

// DefaultDiskRoots are the category directories scanned by the disk
// source when DiskSource.Roots is nil. Mirrors what `ws scan` walks.
var DefaultDiskRoots = []string{"personal", "work", "playground", "researches", "tools"}

// NewDiskSource is the convenience constructor. Builds the Known set
// from a Workspace so callers don't have to.
func NewDiskSource(wsRoot string, ws *config.Workspace) *DiskSource {
	known := make(map[string]bool)
	if ws != nil {
		for _, p := range ws.Projects {
			known[p.Path] = true
		}
	}
	return &DiskSource{WsRoot: wsRoot, Known: known}
}

func (*DiskSource) Name() string { return "disk" }

func (s *DiskSource) FetchSuggestions(ctx context.Context) ([]Suggestion, error) {
	if s.WsRoot == "" {
		return nil, errors.New("DiskSource: empty WsRoot")
	}
	roots := s.Roots
	if roots == nil {
		roots = DefaultDiskRoots
	}

	var out []Suggestion
	for _, dir := range roots {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		absDir := filepath.Join(s.WsRoot, dir)
		if _, err := os.Stat(absDir); os.IsNotExist(err) {
			continue
		}

		if err := s.walk(ctx, absDir, &out); err != nil {
			// Walk-level errors are non-fatal: ENOENT/EACCES on a single
			// subdir shouldn't abort the whole scan. Mirror scan's
			// stderr-warn behavior by logging would require a logger;
			// instead we silently skip — the source contract is "best
			// effort".
			continue
		}
	}
	return out, nil
}

// walk handles one root and one optional level of recursion. A repo at
// the top level is reported directly; a non-repo dir is descended once
// to catch the work/<org>/<repo> shape used by some workspaces.
func (s *DiskSource) walk(ctx context.Context, absDir string, out *[]Suggestion) error {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() || s.skipName(entry.Name()) {
			continue
		}
		entryPath := filepath.Join(absDir, entry.Name())

		if git.IsRepo(entryPath) {
			s.maybeAdd(entryPath, out)
			continue
		}

		// One-level recursion for org-grouped layouts.
		subEntries, err := os.ReadDir(entryPath)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !sub.IsDir() || s.skipName(sub.Name()) {
				continue
			}
			subPath := filepath.Join(entryPath, sub.Name())
			if git.IsRepo(subPath) {
				s.maybeAdd(subPath, out)
			}
		}
	}
	return nil
}

// maybeAdd appends a Suggestion for absPath unless it's already in the
// known-paths set. The suggestion's Name is the directory leaf, which
// the TUI/Register honor unless the user overrides via the edit screen.
func (s *DiskSource) maybeAdd(absPath string, out *[]Suggestion) {
	relPath, err := filepath.Rel(s.WsRoot, absPath)
	if err != nil {
		return
	}
	if s.Known[relPath] {
		return
	}
	remote, _ := git.RemoteURL(absPath)
	name := filepath.Base(absPath)

	*out = append(*out, Suggestion{
		Name:      name,
		RemoteURL: remote,
		Sources:   []SourceKind{SourceDisk},
		DiskPath:  absPath,
	})
}

// skipName matches directory entries we should not descend into:
//   - hidden (`.git`, `.cache`, etc.)
//   - bare repos belonging to a registered project (`<name>.bare`)
//   - extra worktrees of a registered project (`<name>-wt-*`)
//
// The `.bare` and `-wt-` filters intentionally use string ops, not
// fs.Stat checks, so disconnected leftover dirs (whose parent project
// got deleted) are still skipped — they are not the user's intent.
func (s *DiskSource) skipName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	if strings.HasSuffix(name, ".bare") {
		return true
	}
	if strings.Contains(name, "-wt-") {
		return true
	}
	return false
}
