package alias

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
)


const (
	// markerStart and markerEnd delimit the ws-managed block in the user's rc file.
	markerStart = "# >>> ws aliases >>>"
	markerEnd   = "# <<< ws aliases <<<"
)

// StateFilePath returns the path to the generated zsh aliases file.
// Honors $XDG_STATE_HOME, falls back to ~/.local/state/ws/aliases.zsh.
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

// WriteStateFile regenerates the alias state file from the workspace.
// Safe to call on every workspace save.
//
// We never delete the state file when the workspace has zero aliases —
// the state file is a single global resource shared across every workspace
// root, and removing it on save would let an unrelated empty workspace
// blow away aliases owned by another one. Empty workspaces just write an
// empty (header-only) file, which is a no-op for the shell.
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

// InstallZshrc inserts a sourcing block into ~/.zshrc that loads the state file.
// Idempotent: re-running is a no-op once the block is present.
// Returns true if the rc file was modified.
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
