// Package tui consolidates TUI primitives shared across the workspace
// CLI's interactive flows (agent, add, aliasmgr, bootstrap, migrate,
// create, setup). The package is the eviction seam: callers import
// these aliases instead of bubbletea/lipgloss directly, so a future PR
// can swap the underlying implementation without touching consumers.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type (
	Msg   = tea.Msg
	Cmd   = tea.Cmd
	Model = tea.Model
	Style = lipgloss.Style
)

func Batch(cmds ...Cmd) Cmd { return tea.Batch(cmds...) }
func Quit() Msg             { return tea.Quit() }
