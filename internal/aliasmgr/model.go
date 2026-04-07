package aliasmgr

import (
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kuchmenko/workspace/internal/alias"
	"github.com/kuchmenko/workspace/internal/config"
)

type step int

const (
	stepManage step = iota
	stepConfirm
)

// kind of an item in the manage list.
type itemKind int

const (
	kindProject itemKind = iota
	kindGroup
	kindRoot
)

type item struct {
	name     string // project or group key
	kind     itemKind
	alias    string // current alias name (empty if not aliased)
	checked  bool   // selected to have an alias
}

// Result is returned to the caller after the TUI exits.
type Result struct {
	Confirmed bool
	Cancelled bool
	Aliases   map[string]string
}

type Model struct {
	ws            *config.Workspace
	root          string
	step          step
	width         int
	height        int
	items         []item
	tabFilter     int // 0=all, 1=projects, 2=groups
	cursor        int
	offset        int
	search        textinput.Model
	editing       bool
	editInput     textinput.Model
	editTarget    int // index in items being edited
	result        Result
	stepChangedAt time.Time
}

func New(ws *config.Workspace, root string) Model {
	items := buildItems(ws)

	search := textinput.New()
	search.Placeholder = "type to search..."
	search.CharLimit = 60

	edit := textinput.New()
	edit.CharLimit = 32

	return Model{
		ws:        ws,
		root:      root,
		items:     items,
		search:    search,
		editInput: edit,
	}
}

func buildItems(ws *config.Workspace) []item {
	// Reverse map alias→target so we can fill `alias` field per item.
	aliasFor := make(map[string]string, len(ws.Aliases))
	for n, t := range ws.Aliases {
		aliasFor[t] = n
	}

	var items []item
	// Synthetic workspace-root row, always present.
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
		// Root row pinned to the top.
		if items[i].kind == kindRoot {
			return true
		}
		if items[j].kind == kindRoot {
			return false
		}
		// aliased first, then by name
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

func (m Model) Init() tea.Cmd {
	return m.search.Focus()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if !m.stepChangedAt.IsZero() && time.Since(m.stepChangedAt) < 100*time.Millisecond {
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			m.result = Result{Cancelled: true}
			return m, tea.Quit
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

// GetResult returns the model's result after Quit.
func (m Model) GetResult() Result { return m.result }

// generationSeed returns the string used to derive an auto-generated alias.
// For a synthetic root row we don't want to feed "." into Generate.
func (it item) generationSeed() string {
	if it.kind == kindRoot {
		return "workspace"
	}
	return it.name
}

// buildAliasMap collects checked items, generating names for ones the user
// did not edit explicitly.
func (m Model) buildAliasMap() map[string]string {
	out := make(map[string]string)
	taken := make(map[string]struct{})
	// Pass 1: explicit names
	for _, it := range m.items {
		if !it.checked || it.alias == "" {
			continue
		}
		taken[it.alias] = struct{}{}
		out[it.alias] = it.name
	}
	// Pass 2: generated names
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

// Styles
var (
	titleStyle = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("6")).
			Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	checkStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	uncheckStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	activeTab     = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("6")).Padding(0, 1)
	inactiveTab = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Padding(0, 1)
)
