package alias

import (
	"fmt"
	"sort"
	"strings"
	"time"

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

type ManagerResult struct {
	Confirmed bool
	Canceled  bool
	Aliases   map[string]string
}

type ManagerModel struct {
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
	editError     string
	result        ManagerResult
	stepChangedAt time.Time
}

func NewManagerModel(ws *config.Workspace, root string) ManagerModel {
	items := buildItems(ws)

	search := tui.NewTextInput()
	search.SetPlaceholder("type to search...")
	search.SetCharLimit(60)

	edit := tui.NewTextInput()
	edit.SetCharLimit(32)

	return ManagerModel{
		ws:        ws,
		root:      root,
		items:     items,
		search:    search,
		editInput: edit,
	}
}

func buildItems(ws *config.Workspace) []item {
	aliasFor := make(map[string]string, len(ws.Aliases))
	names := make([]string, 0, len(ws.Aliases))
	for name := range ws.Aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := ws.Aliases[name]
		if _, exists := aliasFor[target]; !exists {
			aliasFor[target] = name
		}
	}

	var items []item

	{
		rootAlias := aliasFor[RootTarget]
		items = append(items, item{
			name:    RootTarget,
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

func (m ManagerModel) Init() tui.Cmd {
	return m.search.Focus()
}

func (m ManagerModel) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
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
			m.result = ManagerResult{Canceled: true}
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

func (m ManagerModel) View() string {
	switch m.step {
	case stepManage:
		return m.viewManage()
	case stepConfirm:
		return m.viewConfirm()
	}
	return ""
}

func (m ManagerModel) GetResult() ManagerResult { return m.result }

func (it item) generationSeed() string {
	if it.kind == kindRoot {
		return "workspace"
	}
	return it.name
}

func (m ManagerModel) buildAliasMap() map[string]string {
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
		gen := Generate(it.generationSeed(), taken)
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

func (m ManagerModel) updateConfirm(msg tui.Msg) (tui.Model, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			m.result = ManagerResult{
				Confirmed: true,
				Aliases:   m.buildAliasMap(),
			}
			return m, tui.Quit
		case "n", "N":
			m.result = ManagerResult{Canceled: true}
			return m, tui.Quit
		case "esc":
			m.step = stepManage
			m.stepChangedAt = time.Now()
			return m, nil
		}
	}
	return m, nil
}

func (m ManagerModel) viewConfirm() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" ws alias "))
	b.WriteString("  Confirm\n\n")

	aliases := m.buildAliasMap()
	if len(aliases) == 0 {
		b.WriteString("  " + dimStyle.Render("no aliases configured") + "\n\n")
		b.WriteString(helpStyle.Render("  Save empty? "))
		b.WriteString(selectedStyle.Render("y"))
		b.WriteString(helpStyle.Render("/"))
		b.WriteString(dimStyle.Render("n"))
		b.WriteString(helpStyle.Render("  (esc back)"))
		return b.String()
	}

	tmp := &config.Workspace{
		Projects: m.ws.Projects,
		Groups:   m.ws.Groups,
		Aliases:  aliases,
	}
	resolved := ResolveAll(tmp, m.root)

	sort.Slice(resolved, func(i, j int) bool { return resolved[i].Name < resolved[j].Name })

	for _, r := range resolved {
		nameCol := selectedStyle.Render(fmt.Sprintf("%-12s", r.Name))
		path := r.Path
		warning := ""
		if r.Kind == TargetUnknown {
			path = errStyle.Render("(broken target)")
		}
		if conflictPath, conflict := ShellConflict(r.Name); conflict {
			warning = warnStyle.Render(fmt.Sprintf("  ⚠ shadows %s", conflictPath))
		}
		fmt.Fprintf(&b, "  %s  →  %s%s\n",
			nameCol, dimStyle.Render(path), warning)
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render(fmt.Sprintf("  Save %d aliases to the workspace registry? ", len(aliases))))
	b.WriteString(selectedStyle.Render("y"))
	b.WriteString(helpStyle.Render("/"))
	b.WriteString(dimStyle.Render("n"))
	b.WriteString(helpStyle.Render("  (esc back)"))
	return b.String()
}

type treeRow struct {
	itemIdx int
	prefix  string
}

func (m ManagerModel) itemIndex() map[string]int {
	out := make(map[string]int, len(m.items))
	for i, it := range m.items {
		out[itemKey(it.kind, it.name)] = i
	}
	return out
}

func itemKey(k itemKind, name string) string {
	return fmt.Sprintf("%d/%s", k, name)
}

func (m ManagerModel) buildTree() []treeRow {
	idx := m.itemIndex()
	q := strings.ToLower(strings.TrimSpace(m.search.Value()))

	grouped := make(map[string][]string)
	var ungrouped []string
	for name, p := range m.ws.Projects {
		if p.Group != "" {
			if _, ok := m.ws.Groups[p.Group]; ok {
				grouped[p.Group] = append(grouped[p.Group], name)
				continue
			}
		}
		ungrouped = append(ungrouped, name)
	}
	for _, list := range grouped {
		sort.Strings(list)
	}
	sort.Strings(ungrouped)

	groupNames := make([]string, 0, len(m.ws.Groups))
	for g := range m.ws.Groups {
		groupNames = append(groupNames, g)
	}
	sort.Strings(groupNames)

	matches := func(name string) bool {
		if q == "" {
			return true
		}
		return strings.Contains(strings.ToLower(name), q)
	}

	var rows []treeRow
	rootIdx, hasRoot := idx[itemKey(kindRoot, RootTarget)]
	if hasRoot {
		rows = append(rows, treeRow{itemIdx: rootIdx, prefix: ""})
	}

	type visibleGroup struct {
		name     string
		projects []string
	}
	var visGroups []visibleGroup
	for _, g := range groupNames {
		groupMatches := matches(g)
		var keep []string
		for _, p := range grouped[g] {
			if groupMatches || matches(p) {
				keep = append(keep, p)
			}
		}
		if groupMatches || len(keep) > 0 {
			if groupMatches && q != "" && len(keep) == 0 {
				keep = append([]string{}, grouped[g]...)
			}
			if q == "" {
				keep = append([]string{}, grouped[g]...)
			}
			visGroups = append(visGroups, visibleGroup{name: g, projects: keep})
		}
	}

	var visUngrouped []string
	for _, p := range ungrouped {
		if matches(p) {
			visUngrouped = append(visUngrouped, p)
		}
	}

	totalTop := len(visGroups) + len(visUngrouped)
	pos := 0
	for _, vg := range visGroups {
		isLastTop := pos == totalTop-1
		branch := "├── "
		if isLastTop {
			branch = "└── "
		}
		if gi, ok := idx[itemKey(kindGroup, vg.name)]; ok {
			rows = append(rows, treeRow{itemIdx: gi, prefix: branch})
		}

		childIndent := "│   "
		if isLastTop {
			childIndent = "    "
		}
		for j, p := range vg.projects {
			isLastChild := j == len(vg.projects)-1
			childBranch := childIndent + "├── "
			if isLastChild {
				childBranch = childIndent + "└── "
			}
			if pi, ok := idx[itemKey(kindProject, p)]; ok {
				rows = append(rows, treeRow{itemIdx: pi, prefix: childBranch})
			}
		}
		pos++
	}
	for _, p := range visUngrouped {
		isLastTop := pos == totalTop-1
		branch := "├── "
		if isLastTop {
			branch = "└── "
		}
		if pi, ok := idx[itemKey(kindProject, p)]; ok {
			rows = append(rows, treeRow{itemIdx: pi, prefix: branch})
		}
		pos++
	}

	return rows
}

func (m ManagerModel) maxVisible() int {
	h := m.height - 8
	if h < 5 {
		h = 5
	}
	return h
}

func (m ManagerModel) updateManage(msg tui.Msg) (tui.Model, tui.Cmd) {
	if m.editing {
		return m.updateEditing(msg)
	}

	if key, ok := msg.(tui.KeyMsg); ok {
		rows := m.buildTree()
		switch key.String() {
		case "esc":
			m.result = ManagerResult{Canceled: true}
			return m, tui.Quit
		case "enter":
			m.step = stepConfirm
			m.stepChangedAt = time.Now()
			return m, nil
		case "up", "ctrl+p":
			if m.cursor > 0 {
				m.cursor--
				if m.cursor < m.offset {
					m.offset = m.cursor
				}
			}
			return m, nil
		case "down", "ctrl+n":
			if m.cursor < len(rows)-1 {
				m.cursor++
				if m.cursor >= m.offset+m.maxVisible() {
					m.offset = m.cursor - m.maxVisible() + 1
				}
			}
			return m, nil
		case " ":
			if len(rows) > 0 && m.cursor < len(rows) {
				idx := rows[m.cursor].itemIdx
				m.items[idx].checked = !m.items[idx].checked
				if !m.items[idx].checked {
					m.items[idx].alias = ""
				}
			}
			return m, nil
		case "e":
			if len(rows) > 0 && m.cursor < len(rows) {
				idx := rows[m.cursor].itemIdx
				m.editTarget = idx
				m.editing = true
				cur := m.items[idx].alias
				if cur == "" {
					taken := m.takenNames(idx)
					cur = Generate(m.items[idx].generationSeed(), taken)
				}
				m.editInput.SetValue(cur)
				m.editInput.CursorEnd()
				return m, m.editInput.Focus()
			}
			return m, nil
		}
	}

	var cmd tui.Cmd
	prev := m.search.Value()
	m.search, cmd = m.search.Update(msg)
	if m.search.Value() != prev {
		m.cursor = 0
		m.offset = 0
	}
	return m, cmd
}

func (m ManagerModel) updateEditing(msg tui.Msg) (tui.Model, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "enter":
			name := strings.TrimSpace(m.editInput.Value())
			if err := ValidateName(name); err != nil {
				m.editError = err.Error()
				return m, nil
			}
			if _, exists := m.takenNames(m.editTarget)[name]; exists {
				m.editError = fmt.Sprintf("alias %q is already assigned", name)
				return m, nil
			}
			m.items[m.editTarget].alias = name
			m.items[m.editTarget].checked = true
			m.editError = ""
			m.editing = false
			return m, nil
		case "esc":
			m.editError = ""
			m.editing = false
			return m, nil
		}
	}
	var cmd tui.Cmd
	m.editInput, cmd = m.editInput.Update(msg)
	m.editError = ""
	return m, cmd
}

func (m ManagerModel) takenNames(skip int) map[string]struct{} {
	taken := make(map[string]struct{})
	for i, it := range m.items {
		if i == skip || !it.checked || it.alias == "" {
			continue
		}
		taken[it.alias] = struct{}{}
	}
	return taken
}

func (m ManagerModel) viewManage() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" ws alias "))
	b.WriteString("  Manage aliases\n\n")

	b.WriteString("  " + m.search.View() + "\n\n")

	rows := m.buildTree()
	if len(rows) == 0 {
		b.WriteString("  " + dimStyle.Render("no items match") + "\n")
	}
	maxV := m.maxVisible()
	end := m.offset + maxV
	if end > len(rows) {
		end = len(rows)
	}

	const aliasW = 14

	for vi := m.offset; vi < end; vi++ {
		row := rows[vi]
		idx := row.itemIdx
		it := m.items[idx]
		isCursor := vi == m.cursor

		cursor := "  "
		if isCursor {
			cursor = cursorStyle.Render("> ")
		}
		check := uncheckStyle.Render("○")
		if it.checked {
			check = checkStyle.Render("●")
		}

		var aliasRaw, aliasStyled string
		switch {
		case m.editing && idx == m.editTarget:
			aliasStyled = padRight(m.editInput.View(), aliasW, len(m.editInput.Value()))
		case it.alias != "":
			aliasRaw = it.alias
			aliasStyled = padRight(selectedStyle.Render(aliasRaw), aliasW, len(aliasRaw))
		case it.checked:

			aliasRaw = Generate(it.generationSeed(), m.takenNames(idx))
			aliasStyled = padRight(dimStyle.Render(aliasRaw), aliasW, len(aliasRaw))
		default:
			aliasRaw = "(auto)"
			aliasStyled = padRight(dimStyle.Render(aliasRaw), aliasW, len(aliasRaw))
		}

		nameRaw := it.name
		if it.kind == kindRoot {
			nameRaw = "(workspace root)"
		}
		var nameStyled string
		switch {
		case isCursor:
			nameStyled = selectedStyle.Render(nameRaw)
		case it.kind == kindGroup:
			nameStyled = groupNameStyle.Render(nameRaw)
		case it.kind == kindRoot:
			nameStyled = rootNameStyle.Render(nameRaw)
		default:
			nameStyled = nameRaw
		}

		branch := dimStyle.Render(row.prefix)

		fmt.Fprintf(&b, "%s%s  %s  %s%s\n",
			cursor, check, aliasStyled, branch, nameStyled)
	}

	if len(rows) > maxV {
		above := m.offset
		below := len(rows) - end
		if above > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more\n", above)))
		}
		if below > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more\n", below)))
		}
	}
	if m.editError != "" {
		b.WriteString("\n  " + errStyle.Render(m.editError))
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  ↑↓ navigate  space toggle  e edit alias  enter next  esc cancel"))
	return b.String()
}

func padRight(styled string, width, visibleLen int) string {
	if visibleLen >= width {
		return styled
	}
	return styled + spaces(width-visibleLen)
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = ' '
	}
	return string(buf)
}
