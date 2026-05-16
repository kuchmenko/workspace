package add

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// truncate caps s at n characters with a trailing ellipsis when
// truncation occurs. Operates on bytes, which is wrong for any
// non-ASCII repo description but acceptable as a stop-gap; the
// fallout (a cut mid-rune) is cosmetic only.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// relativeTime renders a time.Time as a short "Nd ago" string. Used in
// the selection preview to give a quick "is this repo active" cue.
func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}

func emit(msg tea.Msg) tea.Cmd {
	return func() tea.Msg { return msg }
}

func parseRepoNameFromURL(url string) string {
	// Lightweight wrapper around git.ParseRepoName to avoid a dep
	// loop into internal/git for code that doesn't otherwise need it.
	url = strings.TrimSpace(url)
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimSuffix(url, "/")
	if i := strings.LastIndexAny(url, "/:"); i >= 0 {
		return url[i+1:]
	}
	return url
}

func addPad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func renderSourceChips(srcs []SourceKind) string {
	if len(srcs) == 0 {
		return ""
	}
	var parts []string
	for _, k := range srcs {
		parts = append(parts, addChip.Render("["+k.String()+"]"))
	}
	return strings.Join(parts, " ")
}

func shortURL(s Suggestion) string {
	if s.RemoteURL != "" {
		return s.RemoteURL
	}
	if s.DiskPath != "" {
		return s.DiskPath
	}
	return ""
}

// renderSourceChipsLive turns the model's accumulated per-source
// outcomes into a single status line. Used both in the gathering
// view (when the user is staring at the spinner) and in the browse
// view (where it lives above the tree as a status bar).
//
// Color rules:
//
//	green (2): source returned a non-empty result
//	dim   (8): source returned 0 (empty but successful)
//	amber (3): source errored
func renderSourceChipsLive(outcomes []SourceOutcome) string {
	var chips []string
	for _, o := range outcomes {
		var color string
		var label string
		switch {
		case o.Err != nil:
			color = "3"
			label = fmt.Sprintf("%s:err (%s)", o.Name, sourceErrHint(o.Err))
		case o.Count == 0:
			color = "8"
			label = fmt.Sprintf("%s:0", o.Name)
		default:
			color = "2"
			label = fmt.Sprintf("%s:%d", o.Name, o.Count)
		}
		chips = append(chips, lipgloss.NewStyle().
			Foreground(lipgloss.Color(color)).Render(label))
	}
	return strings.Join(chips, "  ")
}

// sourceErrHint summarizes a per-source error into a one-or-two-word
// chip suffix. Keeps the gather chips readable on narrow terminals
// without burying the user in stack-trace prose.
//
// Errors in the source pipeline are wrapped as `<source>: <inner>` or
// even `<source>: <middle>: <inner>` (clipboard wraps the binary path,
// github wraps "github source", etc). The fallback strips those
// prefixes and shows the deepest cause — that's the actionable bit
// the user wants to read.
func sourceErrHint(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case strings.Contains(msg, "ErrNotAuthed"), strings.Contains(msg, "not authed"):
		return "no auth"
	case strings.Contains(strings.ToLower(msg), "rate limit"),
		strings.Contains(msg, "API rate limit"):
		return "rate-limited"
	case strings.Contains(strings.ToLower(msg), "401"),
		strings.Contains(strings.ToLower(msg), "unauthorized"):
		return "401 expired?"
	case strings.Contains(msg, "Nothing is copied"),
		strings.Contains(msg, "No selection"):
		return "empty"
	}
	// Fallback: drop everything up to and including the LAST `: ` so
	// "/sbin/wl-paste: failed to bind" → "failed to bind". Cap at 24
	// chars, single line.
	tail := msg
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		tail = strings.TrimSpace(msg[i+2:])
	}
	tail = strings.ReplaceAll(tail, "\n", " ")
	if len(tail) > 24 {
		tail = tail[:24]
	}
	return tail
}
