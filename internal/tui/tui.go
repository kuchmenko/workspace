// Package tui owns every interactive primitive used by the workspace
// CLI. It is the only place in the codebase that imports a TUI
// framework. The Msg / Cmd / Model / Style types are nominally
// distinct from bubbletea's, so a future eviction is a pure
// internal/tui rewrite with no consumer changes.
package tui

type Msg interface{}

type Cmd func() Msg

type Model interface {
	Init() Cmd
	Update(msg Msg) (Model, Cmd)
	View() string
}

type QuitMsg struct{}

func Quit() Msg { return QuitMsg{} }

func Batch(cmds ...Cmd) Cmd {
	var live []Cmd
	for _, c := range cmds {
		if c != nil {
			live = append(live, c)
		}
	}
	if len(live) == 0 {
		return nil
	}
	if len(live) == 1 {
		return live[0]
	}
	return func() Msg {
		return batchMsg(live)
	}
}

type batchMsg []Cmd
