// Package clipboard reads system-clipboard contents via whichever
// platform tool is available. It is used by the `ws add` TUI as one
// of the suggestion sources.
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

var ErrUnavailable = errors.New("no clipboard tool available")

type Reader interface {
	Read(ctx context.Context) (string, error)
}

var DefaultReader Reader = systemReader{}

type systemReader struct{}

func (systemReader) Read(ctx context.Context) (string, error) {
	tool, args, err := detect()
	if err != nil {
		return "", err
	}
	return runTool(ctx, tool, args...)
}

func detect() (string, []string, error) {
	switch runtime.GOOS {
	case "linux":
		return detectLinuxClipboard()
	case "darwin":
		return detectDarwinClipboard()
	}
	return "", nil, ErrUnavailable
}

func detectLinuxClipboard() (string, []string, error) {
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
}

func detectDarwinClipboard() (string, []string, error) {
	if p, err := exec.LookPath("pbpaste"); err == nil {
		return p, nil, nil
	}
	return "", nil, ErrUnavailable
}

func runTool(ctx context.Context, cmd string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, cmd, args...)
	out, err := c.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("%s: %s", cmd, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("%s: %w", cmd, err)
	}

	return strings.TrimRight(string(out), "\n"), nil
}
