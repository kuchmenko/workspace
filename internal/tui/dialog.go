package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

type ConfirmedMsg struct{}
type CancelledMsg struct{}

type ConfirmDialog struct {
	prompt  string
	palette Palette
}

func NewConfirmDialog(palette Palette, prompt string) ConfirmDialog {
	return ConfirmDialog{palette: palette, prompt: prompt}
}

func (d ConfirmDialog) Init() Cmd { return nil }

func (d ConfirmDialog) Update(msg Msg) (Model, Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	switch key.String() {
	case "y", "Y", "enter":
		return d, func() Msg { return ConfirmedMsg{} }
	case "n", "N", "esc":
		return d, func() Msg { return CancelledMsg{} }
	}
	return d, nil
}

func (d ConfirmDialog) View() string {
	return d.palette.Title.Render(d.prompt) + "  " + d.palette.Dim.Render("[y/N]")
}
