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

type DiskSource struct {
	WsRoot string

	Known map[string]bool

	Roots []string
}

var DefaultDiskRoots = []string{"personal", "work", "playground", "researches", "tools"}

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
			continue
		}
	}
	return out, nil
}

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
