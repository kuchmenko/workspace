package alias

import (
	"fmt"
	"strings"
)

// RenderZsh produces a zsh-compatible block of `alias` declarations
// for every resolvable entry. Unresolvable aliases are skipped silently —
// they will be cleaned up next time the user opens the TUI or runs archive.
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

// zshQuote single-quotes a string for zsh, escaping embedded single quotes.
func zshQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
