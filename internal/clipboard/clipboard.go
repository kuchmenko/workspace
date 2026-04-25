// Package clipboard reads system-clipboard contents via whichever
// platform tool is available. It is used by the `ws add` TUI (Track B,
// issue #20) as one of the suggestion sources.
//
// The package is deliberately minimal:
//
//   - Read(ctx) attempts the detected tool with the given context as
//     both a cancellation signal and a timeout.
//
//   - Absence of a tool is not an error state — it returns ErrUnavailable,
//     which callers treat as "clipboard source silently unavailable".
//     This mirrors how the `ws add` gather path degrades: one source
//     failing never blocks the others.
//
//   - We do NOT filter contents here. Raw clipboard text comes out; the
//     caller decides whether it looks like a git URL. Keeping this
//     boundary clean means the regex policy lives with the `ws add`
//     suggestion code where it belongs, not in a "clipboard" package
//     that is conceptually OS-level.
//
// Linux: prefers wl-paste (Wayland) when $WAYLAND_DISPLAY is set and
// wl-paste is on PATH, else xclip when $DISPLAY is set and xclip is on
// PATH. macOS: pbpaste. Other platforms: ErrUnavailable.
package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ErrUnavailable is returned when no supported clipboard tool is
// present on this platform. Callers treat this as "feature not
// available here" rather than failure.
var ErrUnavailable = errors.New("no clipboard tool available")

// Reader is the exported interface so tests (and the future `ws add`
// gather code) can inject a fake. The default implementation lives at
// DefaultReader below.
type Reader interface {
	Read(ctx context.Context) (string, error)
}

// Read is the package-level convenience wrapper around DefaultReader.Read.
// It detects the platform tool at call time, not at package init, so
// missing tools can be installed mid-session and picked up on the next
// invocation.
func Read(ctx context.Context) (string, error) {
	return DefaultReader.Read(ctx)
}

// DefaultReader is the production Reader. Use it as the zero-config
// choice; tests substitute their own implementation.
var DefaultReader Reader = systemReader{}

// systemReader is the concrete dispatcher. It is stateless — each
// Read call probes the environment fresh.
type systemReader struct{}

func (systemReader) Read(ctx context.Context) (string, error) {
	tool, args, err := detect()
	if err != nil {
		return "", err
	}
	return runTool(ctx, tool, args...)
}

// detect returns the command + args of the clipboard tool for the
// current platform, or ErrUnavailable if nothing is usable.
//
// The detector is pure (no side effects beyond env/filesystem stat),
// so it's safe to call repeatedly and cheap to test.
func detect() (string, []string, error) {
	switch runtime.GOOS {
	case "linux":
		// Wayland first: if the compositor is running wl-paste should
		// exist. WAYLAND_DISPLAY is the idiomatic probe — checking for
		// XDG_SESSION_TYPE would also work but is less reliable across
		// distros.
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if p, err := exec.LookPath("wl-paste"); err == nil {
				return p, []string{"--no-newline"}, nil
			}
		}
		if os.Getenv("DISPLAY") != "" {
			if p, err := exec.LookPath("xclip"); err == nil {
				return p, []string{"-o", "-selection", "clipboard"}, nil
			}
		}
		return "", nil, ErrUnavailable

	case "darwin":
		if p, err := exec.LookPath("pbpaste"); err == nil {
			return p, nil, nil
		}
		return "", nil, ErrUnavailable

	default:
		return "", nil, ErrUnavailable
	}
}

// runTool executes cmd with args and returns trimmed stdout. ctx
// controls cancellation (either an explicit cancel or a deadline).
func runTool(ctx context.Context, cmd string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, cmd, args...)
	out, err := c.Output()
	if err != nil {
		// Distinguish "ctx cancelled / deadline" from "tool failed".
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%s: %s", cmd, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("%s: %w", cmd, err)
	}
	// Most tools include a trailing newline by default; wl-paste
	// --no-newline suppresses it, pbpaste emits no newline. xclip does
	// include one. Trim universally so callers see a clean value.
	return strings.TrimRight(string(out), "\n"), nil
}

// Detect exposes the current platform's (tool, args) pair and availability
// for diagnostics. Returns the command path (not just the base name) so
// callers can show "clipboard: /usr/bin/wl-paste" in status output.
func Detect() (tool string, args []string, err error) {
	return detect()
}
