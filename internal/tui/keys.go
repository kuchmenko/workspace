package tui

import "strings"

type KeyType int

const (
	KeyRunes KeyType = iota
	KeyEnter
	KeyEsc
	KeyTab
	KeyShiftTab
	KeyBackspace
	KeySpace
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyDelete
	KeyCtrlC
	KeyCtrlD
	KeyCtrlU
	KeyCtrlF
	KeyCtrlB
)

type KeyMsg struct {
	Type  KeyType
	Runes []rune
	Alt   bool
	Ctrl  bool
}

var keyNames = map[KeyType]string{
	KeyEnter:     "enter",
	KeyEsc:       "esc",
	KeyTab:       "tab",
	KeyShiftTab:  "shift+tab",
	KeyBackspace: "backspace",
	KeySpace:     "space",
	KeyUp:        "up",
	KeyDown:      "down",
	KeyLeft:      "left",
	KeyRight:     "right",
	KeyHome:      "home",
	KeyEnd:       "end",
	KeyPgUp:      "pgup",
	KeyPgDn:      "pgdn",
	KeyDelete:    "delete",
	KeyCtrlC:     "ctrl+c",
	KeyCtrlD:     "ctrl+d",
	KeyCtrlU:     "ctrl+u",
	KeyCtrlF:     "ctrl+f",
	KeyCtrlB:     "ctrl+b",
}

func (k KeyMsg) String() string {
	var b strings.Builder
	if k.Alt {
		b.WriteString("alt+")
	}
	if k.Ctrl && k.Type != KeyCtrlC && k.Type != KeyCtrlD && k.Type != KeyCtrlU && k.Type != KeyCtrlF && k.Type != KeyCtrlB {
		b.WriteString("ctrl+")
	}
	if k.Type == KeyRunes {
		b.WriteString(string(k.Runes))
	} else if name, ok := keyNames[k.Type]; ok {
		b.WriteString(name)
	}
	return b.String()
}

type WindowSizeMsg struct {
	Width  int
	Height int
}
