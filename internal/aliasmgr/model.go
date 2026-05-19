package aliasmgr

import (
	"sort"
	"time"

	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/tui"
)

type step int

const (
	stepManage step = iota
	stepConfirm
)

type itemKind int

const (
	kindProject itemKind = iota
	kindGroup
	kindRoot
)

type item struct {
	name    string
	kind    itemKind
	alias   string
	checked bool
}

type Result struct {
	Confirmed bool
	Canceled  bool
	Aliases   map[string]string
}

type Model struct {
	ws            *config.Workspace
	root          string
	step          step
	width         int
	height        int
	items         []item
	cursor        int
	offset        int
	search        tui.TextInput
	editing       bool
	editInput     tui.TextInput
	editTarget    int
	result        Result
	stepChangedAt time.Time
}

func New(ws *config.Workspace, root string) Model {
	items := buildItems(ws)

	search := tui.NewTextInput()
	search.SetPlaceholder("type to search...")
	search.SetCharLimit(60)

	edit := tui.NewTextInput()
	edit.SetCharLimit(32)

	return Model{
		ws:        ws,
		root:      root,
		items:     items,
		search:    search,
		editInput: edit,
	}
}

func buildItems(ws *config.Workspace) []item {
	aliasFor := make(map[string]string, len(ws.Aliases))
	for n, t := range ws.Aliases {
		aliasFor[t] = n
	}

	var items []item

	{
		rootAlias := aliasFor[alias.RootTarget]
		items = append(items, item{
			name:    alias.RootTarget,
			kind:    kindRoot,
			alias:   rootAlias,
			checked: rootAlias != "",
		})
	}
	for name := range ws.Projects {
		a := aliasFor[name]
		items = append(items, item{
			name:    name,
			kind:    kindProject,
			alias:   a,
			checked: a != "",
		})
	}
	for name := range ws.Groups {
		a := aliasFor[name]
		items = append(items, item{
			name:    name,
			kind:    kindGroup,
			alias:   a,
			checked: a != "",
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].kind == kindRoot {
			return true
		}
		if items[j].kind == kindRoot {
			return false
		}

		if items[i].checked != items[j].checked {
			return items[i].checked
		}
		if items[i].kind != items[j].kind {
			return items[i].kind < items[j].kind
		}
		return items[i].name < items[j].name
	})
	return items
}

func (m Model) Init() tui.Cmd {
	return m.search.Focus()
}

func (m Model) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tui.KeyMsg:
		if !m.stepChangedAt.IsZero() && time.Since(m.stepChangedAt) < 100*time.Millisecond {
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			m.result = Result{Canceled: true}
			return m, tui.Quit
		}
	}

	switch m.step {
	case stepManage:
		return m.updateManage(msg)
	case stepConfirm:
		return m.updateConfirm(msg)
	}
	return m, nil
}

func (m Model) View() string {
	switch m.step {
	case stepManage:
		return m.viewManage()
	case stepConfirm:
		return m.viewConfirm()
	}
	return ""
}

func (m Model) GetResult() Result { return m.result }

func (it item) generationSeed() string {
	if it.kind == kindRoot {
		return "workspace"
	}
	return it.name
}

func (m Model) buildAliasMap() map[string]string {
	out := make(map[string]string)
	taken := make(map[string]struct{})

	for _, it := range m.items {
		if !it.checked || it.alias == "" {
			continue
		}
		taken[it.alias] = struct{}{}
		out[it.alias] = it.name
	}

	for _, it := range m.items {
		if !it.checked || it.alias != "" {
			continue
		}
		gen := alias.Generate(it.generationSeed(), taken)
		taken[gen] = struct{}{}
		out[gen] = it.name
	}
	return out
}

var (
	titleStyle = tui.NewStyle().Bold(true).
			Foreground(tui.Color("15")).
			Background(tui.Color("6")).
			Padding(0, 1)

	selectedStyle  = tui.NewStyle().Foreground(tui.Color("6"))
	dimStyle       = tui.NewStyle().Foreground(tui.Color("8"))
	cursorStyle    = tui.NewStyle().Foreground(tui.Color("6")).Bold(true)
	checkStyle     = tui.NewStyle().Foreground(tui.Color("2"))
	uncheckStyle   = tui.NewStyle().Foreground(tui.Color("8"))
	warnStyle      = tui.NewStyle().Foreground(tui.Color("3"))
	errStyle       = tui.NewStyle().Foreground(tui.Color("1"))
	helpStyle      = tui.NewStyle().Foreground(tui.Color("8"))
	groupNameStyle = tui.NewStyle().Foreground(tui.Color("4")).Bold(true)
	rootNameStyle  = tui.NewStyle().Foreground(tui.Color("5")).Bold(true)
)
