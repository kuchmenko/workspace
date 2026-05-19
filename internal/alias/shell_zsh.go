package alias

import (
	"fmt"
	"strings"
)

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
