package tui

import (
	"context"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type TextInput struct{ ti textinput.Model }

func NewTextInput() TextInput {
	return TextInput{ti: textinput.New()}
}

func (t TextInput) Update(msg Msg) (TextInput, Cmd) {
	next, cmd := t.ti.Update(teaMsg(msg))
	return TextInput{ti: next}, ownCmd(cmd)
}

func (t TextInput) View() string             { return t.ti.View() }
func (t TextInput) Value() string            { return t.ti.Value() }
func (t TextInput) Focused() bool            { return t.ti.Focused() }
func (t *TextInput) Focus() Cmd              { return ownCmd(t.ti.Focus()) }
func (t *TextInput) Blur()                   { t.ti.Blur() }
func (t *TextInput) SetValue(v string)       { t.ti.SetValue(v) }
func (t *TextInput) SetPlaceholder(p string) { t.ti.Placeholder = p }
func (t *TextInput) SetPrompt(p string)      { t.ti.Prompt = p }
func (t *TextInput) SetCharLimit(n int)      { t.ti.CharLimit = n }
func (t *TextInput) SetWidth(w int)          { t.ti.Width = w }

type SpinnerStyle struct{ s spinner.Spinner }

var DotSpinner = SpinnerStyle{s: spinner.Dot}
var LineSpinner = SpinnerStyle{s: spinner.Line}

type Spinner struct{ sp spinner.Model }

func NewSpinner() Spinner {
	return Spinner{sp: spinner.New()}
}

func (s Spinner) Update(msg Msg) (Spinner, Cmd) {
	next, cmd := s.sp.Update(teaMsg(msg))
	return Spinner{sp: next}, ownCmd(cmd)
}

func (s Spinner) View() string         { return s.sp.View() }
func (s Spinner) Tick() Msg            { return s.sp.Tick() }
func (s *Spinner) SetStyle(st SpinnerStyle) { s.sp.Spinner = st.s }
func (s *Spinner) SetTextStyle(st Style)    { s.sp.Style = st.s }

type SpinnerTickMsg = spinner.TickMsg

func Sequence(cmds ...Cmd) Cmd {
	teaCmds := make([]tea.Cmd, len(cmds))
	for i, c := range cmds {
		teaCmds[i] = liftCmd(c)
	}
	c := tea.Sequence(teaCmds...)
	return ownCmd(c)
}

func WithContext(ctx context.Context) ProgramOption {
	return func(c *programConfig) { c.ctx = ctx }
}

func teaMsg(m Msg) tea.Msg {
	switch v := m.(type) {
	case KeyMsg:
		return keyMsgToBubbletea(v)
	case WindowSizeMsg:
		return tea.WindowSizeMsg{Width: v.Width, Height: v.Height}
	}
	return m
}

func ownCmd(c tea.Cmd) Cmd {
	if c == nil {
		return nil
	}
	return func() Msg { return translateIn(c()) }
}

var ownToTeaKeyType = map[KeyType]tea.KeyType{
	KeyRunes:      tea.KeyRunes,
	KeySpace:     tea.KeySpace,
	KeyEnter:     tea.KeyEnter,
	KeyEsc:       tea.KeyEsc,
	KeyTab:       tea.KeyTab,
	KeyShiftTab:  tea.KeyShiftTab,
	KeyBackspace: tea.KeyBackspace,
	KeyUp:        tea.KeyUp,
	KeyDown:      tea.KeyDown,
	KeyLeft:      tea.KeyLeft,
	KeyRight:     tea.KeyRight,
	KeyHome:      tea.KeyHome,
	KeyEnd:       tea.KeyEnd,
	KeyPgUp:      tea.KeyPgUp,
	KeyPgDn:      tea.KeyPgDown,
	KeyDelete:    tea.KeyDelete,
	KeyCtrlC:     tea.KeyCtrlC,
	KeyCtrlD:     tea.KeyCtrlD,
}

func keyMsgToBubbletea(m KeyMsg) tea.KeyMsg {
	out := tea.KeyMsg{Runes: m.Runes, Alt: m.Alt}
	if t, ok := ownToTeaKeyType[m.Type]; ok {
		out.Type = t
	}
	return out
}
