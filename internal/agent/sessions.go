package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session is a Claude Code session discovered from ~/.claude/projects.
type Session struct {
	ID      string
	Title   string // first user message, truncated
	Cwd     string // original working directory
	Updated time.Time
}

// LoadSessions scans ~/.claude/projects for sessions whose cwd matches
// any of the given paths. Returns sessions sorted by most-recent first.
func LoadSessions(paths []string) []Session {
	claudeRoot := claudeProjectsDir()
	if claudeRoot == "" {
		return nil
	}

	// Build lookup: encoded-cwd → original path.
	pathLookup := make(map[string]string, len(paths))
	for _, p := range paths {
		encoded := encodeCwd(p)
		pathLookup[encoded] = p
	}

	var sessions []Session

	entries, err := os.ReadDir(claudeRoot)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		origPath, ok := pathLookup[entry.Name()]
		if !ok {
			continue
		}

		dirPath := filepath.Join(claudeRoot, entry.Name())
		files, err := filepath.Glob(filepath.Join(dirPath, "*.jsonl"))
		if err != nil {
			continue
		}

		for _, f := range files {
			id := strings.TrimSuffix(filepath.Base(f), ".jsonl")
			info, err := os.Stat(f)
			if err != nil {
				continue
			}

			title := extractTitle(f)
			if title == "" {
				title = "(untitled)"
			}

			sessions = append(sessions, Session{
				ID:      id,
				Title:   title,
				Cwd:     origPath,
				Updated: info.ModTime(),
			})
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Updated.After(sessions[j].Updated)
	})
	return sessions
}

// extractTitle reads the first "type":"user" message from a JSONL file
// and returns the content (truncated to 60 chars).
func extractTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if !strings.Contains(string(line), `"type":"user"`) {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &entry); err != nil || entry.Type != "user" {
			continue
		}
		// Content can be string or array of objects.
		var text string
		if err := json.Unmarshal(entry.Message.Content, &text); err != nil {
			var parts []struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(entry.Message.Content, &parts); err == nil && len(parts) > 0 {
				text = parts[0].Text
			}
		}
		if len(text) > 60 {
			text = text[:57] + "…"
		}
		return text
	}
	return ""
}

// encodeCwd converts a filesystem path to the format Claude Code uses
// for directory names in ~/.claude/projects: slashes replaced with
// dashes.
func encodeCwd(path string) string {
	return strings.ReplaceAll(path, "/", "-")
}

func claudeProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".claude", "projects")
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}

// FindSession searches all sessions in ~/.claude/projects for one
// matching the given ID. Returns nil if not found.
func FindSession(id string) *Session {
	claudeRoot := claudeProjectsDir()
	if claudeRoot == "" {
		return nil
	}

	entries, err := os.ReadDir(claudeRoot)
	if err != nil {
		return nil
	}

	target := id + ".jsonl"
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		f := filepath.Join(claudeRoot, entry.Name(), target)
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		// Decode cwd from directory name (dashes back to slashes).
		cwd := strings.ReplaceAll(entry.Name(), "-", "/")
		// Verify the path exists — prevents false positives from
		// ambiguous dash-to-slash decoding.
		if _, err := os.Stat(cwd); err != nil {
			continue
		}
		return &Session{
			ID:      id,
			Title:   extractTitle(f),
			Cwd:     cwd,
			Updated: info.ModTime(),
		}
	}
	return nil
}

// SessionCache is a lazy, map-based cache for Claude Code sessions.
// Sessions are loaded from disk on first access for a given path and
// then served from memory. Invalidation is explicit — call Invalidate
// after operations that may create new sessions.
type SessionCache struct {
	data map[string][]Session // mainPath → sessions
}

// NewSessionCache creates an empty session cache.
func NewSessionCache() *SessionCache {
	return &SessionCache{data: make(map[string][]Session)}
}

// Get returns sessions for the given mainPath, loading from disk on
// first access and caching the result.
func (c *SessionCache) Get(mainPath string) []Session {
	if sessions, ok := c.data[mainPath]; ok {
		return sessions
	}
	sessions := LoadSessions([]string{mainPath})
	c.data[mainPath] = sessions
	return sessions
}

// Count returns the number of sessions for the given mainPath.
func (c *SessionCache) Count(mainPath string) int {
	return len(c.Get(mainPath))
}

// Invalidate removes cached sessions for a path, forcing a reload
// on the next Get call.
func (c *SessionCache) Invalidate(mainPath string) {
	delete(c.data, mainPath)
}

// TimeAgo returns a human-readable relative time string.
func TimeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
}
