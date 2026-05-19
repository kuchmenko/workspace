package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Field struct {
	Name      string
	Value     string
	Validator func(string) error
}

type FormSubmittedMsg struct {
	Values map[string]string
}

type FormCancelledMsg struct{}

type ModalForm struct {
	title   string
	fields  []Field
	focus   int
	errMsg  string
	palette Palette
}

func NewModalForm(palette Palette, title string, fields []Field) ModalForm {
	return ModalForm{palette: palette, title: title, fields: fields}
}

func (f ModalForm) Init() Cmd { return nil }

func (f ModalForm) Update(msg Msg) (Model, Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return f, nil
	}
	switch key.String() {
	case "esc":
		return f, func() Msg { return FormCancelledMsg{} }
	case "tab", "down":
		f.focus = (f.focus + 1) % len(f.fields)
		f.errMsg = ""
	case "shift+tab", "up":
		f.focus = (f.focus - 1 + len(f.fields)) % len(f.fields)
		f.errMsg = ""
	case "enter":
		return f.trySubmit()
	case "backspace":
		v := f.fields[f.focus].Value
		if len(v) > 0 {
			f.fields[f.focus].Value = v[:len(v)-1]
		}
	default:
		if len(key.Runes) > 0 {
			f.fields[f.focus].Value += string(key.Runes)
		}
	}
	return f, nil
}

func (f ModalForm) trySubmit() (Model, Cmd) {
	for _, fd := range f.fields {
		if fd.Validator == nil {
			continue
		}
		if err := fd.Validator(fd.Value); err != nil {
			f.errMsg = fd.Name + ": " + err.Error()
			return f, nil
		}
	}
	values := make(map[string]string, len(f.fields))
	for _, fd := range f.fields {
		values[fd.Name] = fd.Value
	}
	return f, func() Msg { return FormSubmittedMsg{Values: values} }
}

func (f ModalForm) View() string {
	var b strings.Builder
	if f.title != "" {
		b.WriteString(f.palette.Title.Render(f.title))
		b.WriteString("\n\n")
	}
	for i, fd := range f.fields {
		label := fd.Name + ": "
		if i == f.focus {
			b.WriteString(f.palette.Accent.Render(label) + fd.Value + "█")
		} else {
			b.WriteString(f.palette.Dim.Render(label) + fd.Value)
		}
		b.WriteByte('\n')
	}
	if f.errMsg != "" {
		b.WriteByte('\n')
		b.WriteString(f.palette.Error.Render(f.errMsg))
	}
	return b.String()
}
