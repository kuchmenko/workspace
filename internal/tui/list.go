package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type ListItem interface {
	Title() string
	FilterValue() string
}

type FilterableList struct {
	items      []ListItem
	filtered   []int
	cursor     int
	offset     int
	height     int
	filter     string
	filterMode bool
	palette    Palette
}

func NewFilterableList(palette Palette, items []ListItem) FilterableList {
	l := FilterableList{palette: palette, height: 10}
	l.SetItems(items)
	return l
}

func (l *FilterableList) SetItems(items []ListItem) {
	l.items = items
	l.refilter()
	if l.cursor >= len(l.filtered) {
		l.cursor = max(0, len(l.filtered)-1)
	}
}

func (l *FilterableList) SetHeight(h int) {
	if h < 1 {
		h = 1
	}
	l.height = h
}

func (l FilterableList) Cursor() int { return l.cursor }

func (l FilterableList) Selected() (ListItem, bool) {
	if l.cursor < 0 || l.cursor >= len(l.filtered) {
		return nil, false
	}
	return l.items[l.filtered[l.cursor]], true
}

func (l FilterableList) FilterMode() bool { return l.filterMode }
func (l FilterableList) Filter() string   { return l.filter }

func (l FilterableList) Init() Cmd { return nil }

func (l FilterableList) Update(msg Msg) (Model, Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return l, nil
	}
	if l.filterMode {
		switch key.String() {
		case "esc":
			l.filterMode = false
			l.filter = ""
			l.refilter()
		case "enter":
			l.filterMode = false
		case "backspace":
			if len(l.filter) > 0 {
				l.filter = l.filter[:len(l.filter)-1]
				l.refilter()
			}
		default:
			if len(key.Runes) == 1 {
				l.filter += string(key.Runes)
				l.refilter()
			}
		}
		return l, nil
	}
	switch key.String() {
	case "/":
		l.filterMode = true
	case "down", "j":
		if l.cursor < len(l.filtered)-1 {
			l.cursor++
		}
	case "up", "k":
		if l.cursor > 0 {
			l.cursor--
		}
	case "g", "home":
		l.cursor = 0
	case "G", "end":
		l.cursor = max(0, len(l.filtered)-1)
	}
	l.clampOffset()
	return l, nil
}

func (l FilterableList) View() string {
	if len(l.filtered) == 0 {
		if l.filterMode || l.filter != "" {
			return l.palette.Dim.Render("no matches for /" + l.filter)
		}
		return l.palette.Dim.Render("(empty)")
	}
	var b strings.Builder
	end := l.offset + l.height
	if end > len(l.filtered) {
		end = len(l.filtered)
	}
	for i := l.offset; i < end; i++ {
		item := l.items[l.filtered[i]]
		line := item.Title()
		if i == l.cursor {
			line = l.palette.Selected.Render("▌ " + line)
		} else {
			line = "  " + l.palette.Item.Render(line)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if l.filterMode || l.filter != "" {
		b.WriteString(l.palette.Accent.Render("/" + l.filter))
	}
	return b.String()
}

func (l *FilterableList) refilter() {
	l.filtered = l.filtered[:0]
	q := strings.ToLower(l.filter)
	for i, it := range l.items {
		if q == "" || strings.Contains(strings.ToLower(it.FilterValue()), q) {
			l.filtered = append(l.filtered, i)
		}
	}
	if l.cursor >= len(l.filtered) {
		l.cursor = max(0, len(l.filtered)-1)
	}
	l.clampOffset()
}

func (l *FilterableList) clampOffset() {
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+l.height {
		l.offset = l.cursor - l.height + 1
	}
	if l.offset < 0 {
		l.offset = 0
	}
}
