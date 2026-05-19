package alias

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
)

func ShellConflict(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return path, true
}

func Generate(name string, taken map[string]struct{}) string {
	base := generateBase(name)
	if base == "" {
		base = name
	}
	if _, clash := taken[base]; !clash {
		return base
	}
	for i := 2; i < 1000; i++ {
		cand := base + itoa(i)
		if _, clash := taken[cand]; !clash {
			return cand
		}
	}
	return base
}

func generateBase(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	parts := splitParts(name)
	if len(parts) >= 2 {
		return multiPartName(parts)
	}
	return consonantSqueeze(name)
}

func multiPartName(parts []string) string {
	if len(parts) == 2 && len(parts[0]) <= 4 && len(parts[1]) <= 4 {
		return parts[0] + parts[1]
	}
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteByte(p[0])
	}
	return b.String()
}

func consonantSqueeze(name string) string {
	var b strings.Builder
	b.WriteByte(name[0])
	for i := 1; i < len(name) && b.Len() < 5; i++ {
		c := name[i]
		if !isVowel(c) {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func splitParts(s string) []string {
	return strings.FieldsFunc(s, isSeparator)
}

func isSeparator(r rune) bool {
	return r == '-' || r == '_'
}

func isVowel(c byte) bool {
	switch c {
	case 'a', 'e', 'i', 'o', 'u', 'y':
		return true
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

const (
	markerStart = "# >>> ws aliases >>>"
	markerEnd   = "# <<< ws aliases <<<"
)

func StateFilePath() (string, error) {
	if env := os.Getenv("XDG_STATE_HOME"); env != "" {
		return filepath.Join(env, "ws", "aliases.zsh"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ws", "aliases.zsh"), nil
}

func WriteStateFile(ws *config.Workspace, root string) error {
	path, err := StateFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	var content string
	if ws == nil || len(ws.Aliases) == 0 {
		content = "# ws aliases — generated, do not edit\n"
	} else {
		resolved := ResolveAll(ws, root)
		content = RenderZsh(resolved)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func InstallZshrc() (bool, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, "", err
	}
	rc := filepath.Join(home, ".zshrc")
	statePath, err := StateFilePath()
	if err != nil {
		return false, rc, err
	}

	existing, err := os.ReadFile(rc)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, rc, err
	}
	if strings.Contains(string(existing), markerStart) {
		return false, rc, nil
	}

	block := fmt.Sprintf("\n%s\n[ -f %q ] && source %q\n%s\n",
		markerStart, statePath, statePath, markerEnd)

	f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, rc, err
	}
	defer f.Close()
	if _, err := f.WriteString(block); err != nil {
		return false, rc, err
	}
	return true, rc, nil
}

type TargetKind int

const (
	TargetUnknown TargetKind = iota
	TargetProject
	TargetGroup
	TargetRoot
)

const RootTarget = "."

func (k TargetKind) String() string {
	switch k {
	case TargetProject:
		return "project"
	case TargetGroup:
		return "group"
	case TargetRoot:
		return "root"
	}
	return "unknown"
}

type Resolved struct {
	Name   string
	Target string
	Kind   TargetKind
	Path   string
}

func ResolveAll(ws *config.Workspace, root string) []Resolved {
	out := make([]Resolved, 0, len(ws.Aliases))
	for name, target := range ws.Aliases {
		r, err := resolveTarget(ws, root, name, target)
		if err != nil {
			r = Resolved{Name: name, Target: target, Kind: TargetUnknown}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func resolveTarget(ws *config.Workspace, root, name, target string) (Resolved, error) {
	if target == RootTarget {
		return Resolved{
			Name:   name,
			Target: target,
			Kind:   TargetRoot,
			Path:   root,
		}, nil
	}
	if proj, ok := ws.Projects[target]; ok {
		return Resolved{
			Name:   name,
			Target: target,
			Kind:   TargetProject,
			Path:   filepath.Join(root, proj.Path),
		}, nil
	}
	if _, ok := ws.Groups[target]; ok {
		return Resolved{
			Name:   name,
			Target: target,
			Kind:   TargetGroup,
			Path:   filepath.Join(root, target),
		}, nil
	}
	return Resolved{}, fmt.Errorf("alias %q points to unknown target %q", name, target)
}

func RenderZsh(resolved []Resolved) string {
	var b strings.Builder
	b.WriteString("# ws aliases — generated, do not edit\n")
	for _, r := range resolved {
		if r.Kind == TargetUnknown || r.Path == "" {
			continue
		}
		fmt.Fprintf(&b, "alias %s=%s\n", r.Name, zshQuote("cd "+r.Path))
	}
	return b.String()
}

func zshQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
