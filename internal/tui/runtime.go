package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

type ProgramOption func(*programConfig)

type programConfig struct {
	altScreen      bool
	withoutSignals bool
	ctx            interface{}
}

func WithAltScreen() ProgramOption { return func(c *programConfig) { c.altScreen = true } }
func WithoutSignalHandler() ProgramOption {
	return func(c *programConfig) { c.withoutSignals = true }
}

type Program struct {
	p   *tea.Program
	bt  *teaWrapper
	cfg programConfig
}

func NewProgram(m Model, opts ...ProgramOption) *Program {
	cfg := programConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	w := &teaWrapper{m: m}
	teaOpts := []tea.ProgramOption{}
	if cfg.altScreen {
		teaOpts = append(teaOpts, tea.WithAltScreen())
	}
	if cfg.withoutSignals {
		teaOpts = append(teaOpts, tea.WithoutSignalHandler())
	}
	if cfg.ctx != nil {
		if ctx, ok := cfg.ctx.(context.Context); ok {
			teaOpts = append(teaOpts, tea.WithContext(ctx))
		}
	}
	return &Program{p: tea.NewProgram(w, teaOpts...), bt: w, cfg: cfg}
}

func (p *Program) Run() (Model, error) {
	result, err := p.p.Run()
	if err != nil {
		return nil, err
	}
	if w, ok := result.(*teaWrapper); ok {
		return w.m, nil
	}
	return nil, nil
}

func (p *Program) Send(msg Msg) { p.p.Send(msg) }

type teaWrapper struct {
	m Model
}

func (w *teaWrapper) Init() tea.Cmd {
	return liftCmd(w.m.Init())
}

func (w *teaWrapper) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	our := translateIn(msg)
	updated, cmd := w.m.Update(our)
	w.m = updated
	return w, liftCmd(cmd)
}

func (w *teaWrapper) View() string {
	return w.m.View()
}

func liftCmd(c Cmd) tea.Cmd {
	if c == nil {
		return nil
	}
	return func() tea.Msg {
		return translateOut(c())
	}
}

func translateIn(msg tea.Msg) Msg {
	switch m := msg.(type) {
	case tea.KeyMsg:
		return keyMsgFromBubbletea(m)
	case tea.WindowSizeMsg:
		return WindowSizeMsg{Width: m.Width, Height: m.Height}
	}
	return msg
}

func translateOut(msg Msg) tea.Msg {
	switch m := msg.(type) {
	case QuitMsg:
		return tea.Quit()
	case batchMsg:
		teaCmds := make([]tea.Cmd, len(m))
		for i, c := range m {
			teaCmds[i] = liftCmd(c)
		}
		return tea.Batch(teaCmds...)()
	}
	return msg
}

var teaToOwnKeyType = map[tea.KeyType]KeyType{
	tea.KeyRunes:     KeyRunes,
	tea.KeySpace:     KeySpace,
	tea.KeyEnter:     KeyEnter,
	tea.KeyEsc:       KeyEsc,
	tea.KeyTab:       KeyTab,
	tea.KeyShiftTab:  KeyShiftTab,
	tea.KeyBackspace: KeyBackspace,
	tea.KeyUp:        KeyUp,
	tea.KeyDown:      KeyDown,
	tea.KeyLeft:      KeyLeft,
	tea.KeyRight:     KeyRight,
	tea.KeyHome:      KeyHome,
	tea.KeyEnd:       KeyEnd,
	tea.KeyPgUp:      KeyPgUp,
	tea.KeyPgDown:    KeyPgDn,
	tea.KeyDelete:    KeyDelete,
	tea.KeyCtrlC:     KeyCtrlC,
	tea.KeyCtrlD:     KeyCtrlD,
}

func keyMsgFromBubbletea(m tea.KeyMsg) KeyMsg {
	out := KeyMsg{Runes: m.Runes, Alt: m.Alt}
	if t, ok := teaToOwnKeyType[m.Type]; ok {
		out.Type = t
	}
	if out.Type == KeyCtrlC || out.Type == KeyCtrlD {
		out.Ctrl = true
	}
	return out
}
