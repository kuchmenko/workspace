package daemon

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/kuchmenko/workspace/internal/git"
)

type Watcher struct {
	fsw    *fsnotify.Watcher
	logger *log.Logger
	mu     sync.Mutex
	// debounce: track recently seen events to avoid duplicates
	seen map[string]time.Time
}

func NewWatcher(logger *log.Logger) *Watcher {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Printf("watcher: failed to create: %v", err)
		return &Watcher{logger: logger, seen: make(map[string]time.Time)}
	}
	return &Watcher{fsw: fsw, logger: logger, seen: make(map[string]time.Time)}
}

// Add watches a workspace root directory for new git repos.
func (w *Watcher) Add(root string) {
	if w.fsw == nil {
		return
	}
	for _, dir := range topLevelGroupDirs(root) {
		if err := w.fsw.Add(dir); err != nil {
			w.logger.Printf("watcher: cannot watch %s: %v", dir, err)
		}
	}
}

// topLevelGroupDirs lists immediate children of `root` that are
// candidates for the watcher: directories, non-dotfile, non-empty.
// Errors reading the root return an empty slice — the caller treats
// the watcher as best-effort.
func topLevelGroupDirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, filepath.Join(root, e.Name()))
	}
	return out
}

func (w *Watcher) Run(quit <-chan struct{}) {
	if w.fsw == nil {
		<-quit
		return
	}

	for {
		select {
		case <-quit:
			return
		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create == 0 {
				continue
			}
			w.handleCreate(event.Name)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.logger.Printf("watcher: error: %v", err)
		}
	}
}

func (w *Watcher) handleCreate(path string) {
	// Debounce: ignore if we saw this path in the last second
	w.mu.Lock()
	if last, ok := w.seen[path]; ok && time.Since(last) < time.Second {
		w.mu.Unlock()
		return
	}
	w.seen[path] = time.Now()
	w.mu.Unlock()

	// Check if it's a directory
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return
	}

	// Give git init a moment to complete
	time.Sleep(500 * time.Millisecond)

	if !git.IsRepo(path) {
		return
	}

	w.logger.Printf("watcher: new git repo detected: %s", path)
}

func (w *Watcher) Close() {
	if w.fsw != nil {
		_ = w.fsw.Close()
	}
}
