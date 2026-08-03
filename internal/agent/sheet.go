package agent

import (
	"fmt"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/repo"
	"github.com/kuchmenko/workspace/internal/tui"
)

type sheetMode int

const (
	sheetProject sheetMode = iota
	sheetGroup
)

type sheetRowKind int

const (
	rowHeader sheetRowKind = iota
	rowAction
	rowWorktree
	rowProject
)

type sheetAction int

const (
	actNone sheetAction = iota
	actShellMain
	actNewWorktree
	actSearch
	actEdit
	actFavorite
)

type sheetRow struct {
	kind    sheetRowKind
	label   string
	hint    string
	keyHint string
	action  sheetAction
	wt      *Worktree
	proj    *Project
	indent  int
	section string
}

type sheet struct {
	mode       sheetMode
	target     *Project
	group      string
	groupPath  string
	rows       []sheetRow
	visible    []int
	cursor     int
	filter     tui.TextInput
	filterMode bool
	parent     *sheet
	pendingDel *Worktree
	statusMsg  string
}

func newProjectSheet(m *Model, p *Project, parent *sheet) *sheet {
	filter := tui.NewTextInput()
	filter.SetPrompt("")
	s := &sheet{
		mode:   sheetProject,
		target: p,
		parent: parent,
		filter: filter,
	}
	s.rebuild(m)
	return s
}

func newGroupSheet(m *Model, group string) *sheet {
	filter := tui.NewTextInput()
	filter.SetPrompt("")
	s := &sheet{
		mode:      sheetGroup,
		group:     group,
		groupPath: groupRootPath(m, group),
		filter:    filter,
	}
	s.rebuild(m)
	return s
}

func groupRootPath(m *Model, group string) string {
	root := m.workspaceRootForGroup(group)
	if root == "" {
		return ""
	}
	return GroupPath(root, group)
}

func (s *sheet) rebuild(m *Model) {
	if s.mode == sheetProject {
		s.rows = buildProjectSheetRows(m, s.target)
	} else {
		s.rows = buildGroupSheetRows(m, s.group, s.groupPath)
	}
	s.applyFilter()
}

func (s *sheet) primaryPath() string {
	if s.mode == sheetGroup {
		return s.groupPath
	}
	if s.target != nil {
		return s.target.Path
	}
	return ""
}

func (s *sheet) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	s.visible = s.visible[:0]

	if q == "" {
		for i := range s.rows {
			s.visible = append(s.visible, i)
		}
		s.clampCursor()
		return
	}

	// First pass: keep non-header rows that match.
	keep := make([]bool, len(s.rows))
	for i, r := range s.rows {
		if r.kind == rowHeader {
			continue
		}
		if strings.Contains(strings.ToLower(r.label), q) {
			keep[i] = true
		}
	}

	// Keep section headers that have at least one visible row in their section.
	currentHeader := -1
	sectionHasMatch := false
	for i, r := range s.rows {
		if r.kind == rowHeader {
			if currentHeader >= 0 && sectionHasMatch {
				keep[currentHeader] = true
			}
			currentHeader = i
			sectionHasMatch = false
			continue
		}
		if keep[i] {
			sectionHasMatch = true
		}
	}
	if currentHeader >= 0 && sectionHasMatch {
		keep[currentHeader] = true
	}

	for i, k := range keep {
		if k {
			s.visible = append(s.visible, i)
		}
	}
	s.clampCursor()
}

func (s *sheet) clampCursor() {
	if len(s.visible) == 0 {
		s.cursor = 0
		return
	}
	if s.cursor >= len(s.visible) {
		s.cursor = len(s.visible) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	// Cursor must never land on a header row.
	if r := s.rowAt(s.cursor); r != nil && r.kind == rowHeader {
		// Try forward then backward.
		for i := s.cursor + 1; i < len(s.visible); i++ {
			if s.rows[s.visible[i]].kind != rowHeader {
				s.cursor = i
				return
			}
		}
		for i := s.cursor - 1; i >= 0; i-- {
			if s.rows[s.visible[i]].kind != rowHeader {
				s.cursor = i
				return
			}
		}
	}
}

func (s *sheet) rowAt(visIdx int) *sheetRow {
	if visIdx < 0 || visIdx >= len(s.visible) {
		return nil
	}
	return &s.rows[s.visible[visIdx]]
}

func (s *sheet) focused() *sheetRow { return s.rowAt(s.cursor) }

// ---------- update ----------

func (s *sheet) update(m *Model, msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	if s.filterMode {
		return s.updateFilterMode(m, msg)
	}

	key := msg.String()

	// Pending wt delete confirmation lives inside the sheet.
	if s.pendingDel != nil {
		s.statusMsg = ""
		wt := s.pendingDel
		s.pendingDel = nil
		if key == "y" {
			return s.dispatchWtDelete(m, wt)
		}
		return m, nil
	}

	switch key {
	case "esc":
		return s.close(m)
	case "ctrl+c", "ctrl+q":
		return m, tui.Quit
	case "j", "down":
		s.moveCursor(+1)
		return m, nil
	case "k", "up":
		s.moveCursor(-1)
		return m, nil
	case "g", "home":
		s.cursor = 0
		s.clampCursor()
		return m, nil
	case "G", "end":
		s.cursor = len(s.visible) - 1
		s.clampCursor()
		return m, nil
	case "/":
		s.filterMode = true
		return m, s.filter.Focus()
	}

	row := s.focused()
	if row == nil {
		return m, nil
	}

	switch row.kind {
	case rowAction:
		return s.dispatchAction(m, row.action, key)
	case rowWorktree:
		return s.dispatchWorktree(m, row.wt, key)
	case rowProject:
		if key == "enter" {
			m.sheet = newProjectSheet(m, row.proj, s)
			return m, nil
		}
	}
	return m, nil
}

func (s *sheet) updateFilterMode(m *Model, msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		s.filterMode = false
		s.filter.SetValue("")
		s.filter.Blur()
		s.applyFilter()
	case "enter":
		s.filterMode = false
		s.filter.Blur()
	default:
		var cmd tui.Cmd
		s.filter, cmd = s.filter.Update(msg)
		s.applyFilter()
		return m, cmd
	}
	return m, nil
}

func (s *sheet) moveCursor(delta int) {
	if len(s.visible) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for i := 0; i < abs(delta); i++ {
		next := s.cursor + step
		// Skip headers.
		for next >= 0 && next < len(s.visible) && s.rows[s.visible[next]].kind == rowHeader {
			next += step
		}
		if next < 0 || next >= len(s.visible) {
			return
		}
		s.cursor = next
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (s *sheet) close(m *Model) (tui.Model, tui.Cmd) {
	if s.parent != nil {
		m.sheet = s.parent
		return m, nil
	}
	m.sheet = nil
	return m, nil
}

// ---------- dispatch ----------

func (s *sheet) dispatchAction(m *Model, act sheetAction, key string) (tui.Model, tui.Cmd) {
	path := s.primaryPath()
	switch act {
	case actShellMain:
		if key == "enter" || key == "s" || key == "l" {
			m.Launch = &LaunchRequest{Cwd: path}
			return m, tui.Quit
		}
	case actNewWorktree:
		if (key == "enter" || key == "w") && s.target != nil {
			m.popupProj = s.target
			m.wtBranch.SetValue("")
			m.wtBranch.Focus()
			m.wtField = 0
			m.sheet = nil
			m.mode = viewNewWorktree
			return m, nil
		}
	case actSearch:
		if key == "enter" || key == "/" {
			return s.openGlobalSearch(m)
		}
	case actEdit:
		if (key == "enter" || key == "e") && s.target != nil {
			p := s.target
			m.popupProj = p
			m.editGroup.SetValue(p.Group)
			m.editGroup.Focus()
			m.editCategory = config.Category(p.Category)
			if m.editCategory == "" {
				m.editCategory = config.CategoryPersonal
			}
			m.editField = 0
			m.editErr = ""
			m.sheet = nil
			m.mode = viewEditProject
			return m, nil
		}
	case actFavorite:
		if key == "enter" || key == "f" {
			if s.target != nil {
				m.toggleFavoriteFor(s.target)
			} else if s.group != "" {
				m.toggleFavoriteGroup(s.group)
			}
			s.rebuild(m)
			return m, nil
		}
	}
	return m, nil
}

func (s *sheet) dispatchWorktree(m *Model, wt *Worktree, key string) (tui.Model, tui.Cmd) {
	if wt == nil {
		return m, nil
	}
	switch key {
	case "enter", "c", "s", "l":
		m.Launch = &LaunchRequest{Cwd: wt.Path}
		return m, tui.Quit
	case "d":
		if wt.IsMain {
			return m, nil
		}
		s.pendingDel = wt
		s.statusMsg = fmt.Sprintf("delete %s? y to confirm", worktreeDisplayName(*wt))
		return m, nil
	}
	return m, nil
}

func (s *sheet) openGlobalSearch(m *Model) (tui.Model, tui.Cmd) {
	m.sheet = nil
	m.mode = viewFlash
	m.flashGlobal = true
	m.flashQuery.SetValue("")
	m.flashQuery.Focus()
	m.savedExpanded = make(map[string]bool)
	for k, v := range m.expanded {
		m.savedExpanded[k] = v
	}
	for _, ws := range m.workspaces {
		for _, g := range ws.Groups {
			m.expanded[g] = true
		}
	}
	m.rebuildItems()
	m.recomputeFlash()
	return m, nil
}

func (s *sheet) dispatchWtDelete(m *Model, wt *Worktree) (tui.Model, tui.Cmd) {
	p := s.target
	projID := ""
	if p != nil {
		projID = p.ID
	}
	wsRoot := m.workspaceRootFor(p)
	machine, err := explorerMachineName()
	if err == nil {
		err = repo.RemoveWorktree(repo.WorktreeRemoveOptions{WorkspaceRoot: wsRoot, Project: projID, Branch: wt.Branch, Machine: machine})
	}
	if err != nil {
		s.statusMsg = err.Error()
		return m, nil
	}
	m.wtCache.Invalidate(p.Path)
	s.rebuild(m)
	s.statusMsg = "worktree deleted"
	return m, nil
}

// ---------- view ----------

func (s *sheet) view(width, height int) string {
	popupW := 60
	if width < 66 {
		popupW = width - 6
	}
	if popupW < 30 {
		popupW = 30
	}
	innerW := popupW - 6

	var lines []string
	lines = append(lines, popupTitleStyle.Width(innerW).Render(s.title()))
	if sub := s.subtitle(); sub != "" {
		lines = append(lines, popupDimStyle.Width(innerW).Render(sub))
	}

	// Filter prompt sits below the title.
	if s.filterMode || s.filter.Value() != "" {
		prompt := "/" + s.filter.Value()
		if s.filterMode {
			prompt = "/" + s.filter.View()
		}
		lines = append(lines, popupSelectedStyle.Width(innerW).Render(" "+prompt))
	}
	lines = append(lines, "")

	if len(s.visible) == 0 {
		empty := "(no matches)"
		if s.filter.Value() == "" {
			empty = "(empty)"
		}
		lines = append(lines, popupDimStyle.Width(innerW).Render(empty))
	} else {
		// Window the visible rows around the cursor so the popup stays bounded.
		const maxRows = 18
		start, end := windowAround(s.cursor, len(s.visible), maxRows)
		for i := start; i < end; i++ {
			lines = append(lines, s.renderRow(i, innerW))
		}
		if start > 0 {
			lines = append(lines, popupDimStyle.Width(innerW).Render(fmt.Sprintf("  …%d above", start)))
		}
		if end < len(s.visible) {
			lines = append(lines, popupDimStyle.Width(innerW).Render(fmt.Sprintf("  …%d below", len(s.visible)-end)))
		}
	}

	if s.statusMsg != "" {
		lines = append(lines, "")
		lines = append(lines, statusMsgStyle.Width(innerW).Render(" "+s.statusMsg))
	}

	lines = append(lines, "")
	lines = append(lines, popupDimStyle.Width(innerW).Render(s.footerHint()))

	content := strings.Join(lines, "\n")
	popup := popupBorderStyle.Render(content)
	return tui.Place(width, height, tui.Center, tui.Center, popup,
		tui.WithWhitespaceBackground(tui.Color("234")))
}

func windowAround(cursor, total, max int) (int, int) {
	if total <= max {
		return 0, total
	}
	half := max / 2
	start := cursor - half
	if start < 0 {
		start = 0
	}
	end := start + max
	if end > total {
		end = total
		start = end - max
	}
	return start, end
}

func (s *sheet) renderRow(visIdx, innerW int) string {
	r := &s.rows[s.visible[visIdx]]
	selected := visIdx == s.cursor

	if r.kind == rowHeader {
		text := fmt.Sprintf("── %s ", r.label)
		if len(text) < innerW {
			text += strings.Repeat("─", innerW-len(text))
		}
		return popupDimStyle.Width(innerW).Render(text)
	}

	label := r.label
	hint := r.hint
	key := r.keyHint

	left := "  " + label
	if r.indent > 0 {
		left = strings.Repeat("    ", r.indent) + label
	}

	right := ""
	if hint != "" {
		right += hint
	}
	if key != "" {
		if right != "" {
			right += "  "
		}
		right += key
	}

	leftW := tui.Width(left)
	rightW := tui.Width(right)
	gap := innerW - leftW - rightW
	if gap < 1 {
		gap = 1
	}
	line := left + strings.Repeat(" ", gap) + right

	if selected {
		return popupSelectedStyle.Width(innerW).Render(line)
	}
	return popupItemStyle.Width(innerW).Render(line)
}

func (s *sheet) title() string {
	if s.mode == sheetGroup {
		return fmt.Sprintf("@%s", s.group)
	}
	if s.target != nil {
		return s.target.Name
	}
	return "launch"
}

func (s *sheet) subtitle() string {
	if s.mode == sheetGroup {
		if s.groupPath != "" {
			return s.groupPath
		}
		return "group"
	}
	if s.target == nil {
		return ""
	}
	cat := s.target.Category
	if cat == "" {
		cat = "personal"
	}
	return fmt.Sprintf("%s · %s", cat, s.target.Path)
}

func (s *sheet) footerHint() string {
	if s.filterMode {
		return "  type to filter · enter:apply · esc:clear"
	}
	if s.mode == sheetGroup {
		return "  ⏎:open  /:filter  esc:back"
	}
	r := s.focused()
	if r == nil {
		return "  /:filter  esc:back"
	}
	switch r.kind {
	case rowWorktree:
		if r.wt != nil && !r.wt.IsMain {
			return "  ⏎:shell  d:delete"
		}
		return "  ⏎:shell"
	case rowAction:
		return "  ⏎:run  /:filter  esc:back"
	}
	return "  /:filter  esc:back"
}
