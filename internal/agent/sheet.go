package agent

import (
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/layout"
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
	rowWorktree
	rowProject
)

type sheetRow struct {
	kind     sheetRowKind
	label    string
	hint     string
	activity string
	wt       *Worktree
	proj     *Project
	indent   int
	section  string
}

type sheet struct {
	mode               sheetMode
	target             *Project
	group              string
	workspaceRoot      string
	groupPath          string
	rows               []sheetRow
	visible            []int
	cursor             int
	filter             tui.TextInput
	filterMode         bool
	searchWorktreePath string
	searchProjectID    string
	visual             bool
	visualAnchor       int
	parent             *sheet
	statusMsg          string
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
	if strings.HasPrefix(m.statusMsg, "inspect worktrees:") {
		s.statusMsg = m.statusMsg
		m.statusMsg = ""
	}
	return s
}

func newGroupSheet(m *Model, workspaceRoot, group string) *sheet {
	filter := tui.NewTextInput()
	filter.SetPrompt("")
	s := &sheet{
		mode:          sheetGroup,
		workspaceRoot: workspaceRoot,
		group:         group,
		groupPath:     groupRootPath(workspaceRoot, group),
		filter:        filter,
	}
	s.rebuild(m)
	return s
}

func groupRootPath(workspaceRoot, group string) string {
	path, err := layout.ProjectPath(workspaceRoot, group)
	if err != nil {
		return ""
	}
	return path
}

func (s *sheet) rebuild(m *Model) {
	if s.mode == sheetProject {
		s.rows = buildProjectSheetRows(m, s.target)
	} else {
		s.rows = buildGroupSheetRows(m, s.workspaceRoot, s.group, s.groupPath)
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

func (s *sheet) focusWorktreePath(path string) bool {
	for i, rowIndex := range s.visible {
		row := s.rows[rowIndex]
		if row.wt != nil && row.wt.Path == path {
			s.cursor = i
			return true
		}
	}
	return false
}

// ---------- update ----------

func (s *sheet) update(m *Model, msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	if s.filterMode {
		return s.updateFilterMode(m, msg)
	}

	key := msg.String()
	if !s.filterMode && s.filter.Value() != "" && (key == "q" || key == "esc") {
		s.clearSearch()
		return m, nil
	}

	if s.visual {
		switch key {
		case "v", "esc":
			s.clearVisual()
			return m, nil
		case "a", "d":
			if len(s.visualWorktrees()) == 0 {
				s.clearVisual()
				s.statusMsg = "no worktrees selected"
				return m, nil
			}
		case "j", "down", "k", "up", "g", "home", "G", "end", "ctrl+d", "ctrl+u", "ctrl+f", "pgdn", "ctrl+b", "pgup", "ctrl+c", "ctrl+q":
		default:
			return m, nil
		}
	}
	if handled, model, cmd := s.updateLifecycleKey(m, key); handled {
		return model, cmd
	}
	if handled, model, cmd := s.updateContextKey(m, key); handled {
		return model, cmd
	}
	if key == "v" && s.mode == sheetProject {
		if row := s.focused(); row != nil && row.kind == rowWorktree && row.wt != nil && !row.wt.IsMain {
			s.visual = true
			s.visualAnchor = s.cursor
		}
		return m, nil
	}

	switch key {
	case "q", "esc":
		if s.visual {
			s.clearVisual()
			return m, nil
		}
		return s.close(m)
	case "h", "left":
		return s.close(m)
	case "ctrl+c", "ctrl+q":
		if m.jobsRunning() {
			m.statusMsg = "actions are still queued or running · A Open Activity"
			return m, nil
		}
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
	case "ctrl+d":
		s.moveCursor(max(1, s.pageRows(m)/2))
		return m, nil
	case "ctrl+u":
		s.moveCursor(-max(1, s.pageRows(m)/2))
		return m, nil
	case "ctrl+f", "pgdn":
		s.moveCursor(s.pageRows(m))
		return m, nil
	case "ctrl+b", "pgup":
		s.moveCursor(-s.pageRows(m))
		return m, nil
	case "/":
		s.captureSearchOrigin()
		s.filterMode = true
		return m, s.filter.Focus()
	}

	if key == "l" || key == "right" {
		key = "enter"
	}
	row := s.focused()
	if row == nil {
		return m, nil
	}

	switch row.kind {
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
	case "esc", "ctrl+c":
		s.clearSearch()
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

func (s *sheet) captureSearchOrigin() {
	s.searchWorktreePath = ""
	s.searchProjectID = ""
	if row := s.focused(); row != nil {
		if row.wt != nil {
			s.searchWorktreePath = row.wt.Path
		} else if row.proj != nil {
			s.searchProjectID = row.proj.ID
		}
	}
}

func (s *sheet) clearSearch() {
	s.filterMode = false
	s.filter.SetValue("")
	s.filter.Blur()
	s.applyFilter()
	if s.searchWorktreePath != "" {
		s.focusWorktreePath(s.searchWorktreePath)
	} else if s.searchProjectID != "" {
		for i, rowIndex := range s.visible {
			if project := s.rows[rowIndex].proj; project != nil && project.ID == s.searchProjectID {
				s.cursor = i
				break
			}
		}
	}
	s.searchWorktreePath = ""
	s.searchProjectID = ""
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

func (s *sheet) clearVisual() {
	s.visual = false
	s.visualAnchor = 0
}

func (s *sheet) visualWorktrees() []*Worktree {
	if !s.visual || s.mode != sheetProject {
		return nil
	}
	start, end := s.visualAnchor, s.cursor
	if start > end {
		start, end = end, start
	}
	worktrees := make([]*Worktree, 0, end-start+1)
	for i := start; i <= end; i++ {
		row := s.rowAt(i)
		if row != nil && row.kind == rowWorktree && row.wt != nil && !row.wt.IsMain {
			worktrees = append(worktrees, row.wt)
		}
	}
	return worktrees
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

func (s *sheet) updateContextKey(m *Model, key string) (bool, tui.Model, tui.Cmd) {
	switch key {
	case "s":
		model, cmd := m.launch(s.workspaceRootForTarget(), s.primaryPath())
		return true, model, cmd
	case "S":
		model, cmd := s.openGlobalSearch(m)
		return true, model, cmd
	case "w":
		if s.target != nil {
			m.formReturnSheet = s
			m.formReturnFlash = nil
			m.popupProj = s.target
			m.wtBranch.SetValue("")
			m.wtBranch.Focus()
			m.wtField = 0
			m.sheet = nil
			m.mode = viewNewWorktree
			return true, m, nil
		}
	case "e":
		if s.target != nil {
			m.formReturnSheet = s
			m.formReturnFlash = nil
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
			return true, m, nil
		}
	case "f":
		if s.target != nil {
			return true, m, m.toggleFavoriteFor(s.target)
		} else if s.group != "" {
			return true, m, m.toggleFavoriteGroup(s.workspaceRoot, s.group)
		}
		s.rebuild(m)
		return true, m, nil
	}
	return false, m, nil
}

func (s *sheet) dispatchWorktree(m *Model, wt *Worktree, key string) (tui.Model, tui.Cmd) {
	if wt == nil {
		return m, nil
	}
	switch key {
	case "enter", "c", "s", "l":
		return m.launch(m.workspaceRootFor(s.target), wt.Path)
	}
	return m, nil
}

func (s *sheet) openGlobalSearch(m *Model) (tui.Model, tui.Cmd) {
	m.flashReturnSheet = s
	m.sheet = nil
	m.openGlobalSearch()
	return m, nil
}

func (s *sheet) workspaceRootForTarget() string {
	if s.mode == sheetGroup {
		return s.workspaceRoot
	}
	if s.target != nil {
		return s.target.WorkspaceRoot
	}
	return ""
}
