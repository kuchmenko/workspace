package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Language icons (Nerd Font codepoints). Kept in one block so they
// scan as a palette when reading the file.
const (
	iconGo         = "" //  nf-seti-go
	iconRust       = "" //  nf-dev-rust
	iconPython     = "" //  nf-seti-python
	iconNode       = "" //  nf-dev-nodejs_small
	iconTypeScript = "" //  nf-seti-typescript
	iconJavaScript = "" //  nf-dev-javascript_badge
	iconRuby       = "" //  nf-dev-ruby
	iconJava       = "" //  nf-dev-java
	iconCSharp     = "" //  nf-seti-c_sharp
	iconDocker     = "" //  nf-linux-docker
	iconShell      = "" //  nf-oct-terminal
	iconMarkdown   = "" //  nf-seti-markdown
)

// projectIconCache memoizes DetectLanguage per absolute project path.
// The detection walks the project dir once at first render and is
// stable for the session — language doesn't change between tree
// refreshes. Invalidation isn't wired yet because the rare
// "added go.mod mid-session" case isn't worth the extra plumbing.
var projectIconCache sync.Map // map[string]string

// DetectIcon returns the Nerd Font glyph that best matches the
// project at `path`. Detection prefers ecosystem marker files in a
// fixed priority order (go.mod beats Dockerfile beats *.sh fallback)
// so a Go project that also ships a Dockerfile reads as Go. Returns
// the generic iconProject when no marker fires.
func DetectIcon(path string) string {
	if path == "" {
		return iconProject
	}
	if v, ok := projectIconCache.Load(path); ok {
		return v.(string)
	}
	icon := detectIconUncached(path)
	projectIconCache.Store(path, icon)
	return icon
}

// markerFiles is the priority-ordered list of marker file → icon
// mappings. First hit wins. Multi-marker languages list every
// canonical file (pyproject.toml AND requirements.txt for Python,
// package.json AND yarn.lock for Node) so neither order nor presence
// of a specific tooling flavor changes detection.
var markerFiles = []struct {
	file string
	icon string
}{
	{"go.mod", iconGo},
	{"Cargo.toml", iconRust},
	{"pyproject.toml", iconPython},
	{"requirements.txt", iconPython},
	{"setup.py", iconPython},
	{"tsconfig.json", iconTypeScript},
	{"Gemfile", iconRuby},
	{"pom.xml", iconJava},
	{"build.gradle", iconJava},
	{"build.gradle.kts", iconJava},
	{"package.json", iconNode}, // after tsconfig so TS wins over JS+TS
}

// suffixIcons is the fallback scan: when no marker file fires, we
// look for the first top-level file with a recognized extension.
// Keeps the loop cheap — the project dir is read once at most.
var suffixIcons = []struct {
	suffix string
	icon   string
}{
	{".csproj", iconCSharp},
	{".sln", iconCSharp},
	{".rs", iconRust},
	{".go", iconGo},
	{".ts", iconTypeScript},
	{".tsx", iconTypeScript},
	{".js", iconJavaScript},
	{".py", iconPython},
	{".rb", iconRuby},
	{".java", iconJava},
	{".cs", iconCSharp},
	{".sh", iconShell},
	{".bash", iconShell},
	{".zsh", iconShell},
}

func detectIconUncached(path string) string {
	// Pass 1: marker files in priority order. cheap (one stat per
	// marker) and disambiguates polyglot repos correctly.
	for _, m := range markerFiles {
		if _, err := os.Stat(filepath.Join(path, m.file)); err == nil {
			return m.icon
		}
	}
	// Pass 2: Dockerfile and Markdown are weak signals — they show
	// up as the project icon only when no real language marker exists.
	if _, err := os.Stat(filepath.Join(path, "Dockerfile")); err == nil {
		return iconDocker
	}

	// Pass 3: scan the top-level directory once for known extensions.
	// Bail out the moment we hit the first match — order in suffixIcons
	// determines tie-breaks.
	entries, err := os.ReadDir(path)
	if err != nil {
		return iconProject
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		for _, s := range suffixIcons {
			if strings.HasSuffix(name, s.suffix) {
				return s.icon
			}
		}
	}

	// Pass 4: a lonely README.md is at least *something* recognizable.
	if _, err := os.Stat(filepath.Join(path, "README.md")); err == nil {
		return iconMarkdown
	}
	return iconProject
}
