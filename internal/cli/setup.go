package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	gh "github.com/kuchmenko/workspace/internal/github"
	"github.com/kuchmenko/workspace/internal/tui"
)

type step int

const (
	stepLoading step = iota
	stepSelect
	stepGroup
	stepConfirm
)

type SetupResult struct {
	Confirmed bool
	Canceled  bool
	Err       error
	Groups    []GroupEntry
	Username  string
}

type GroupEntry struct {
	Name  string
	Repos []gh.Repo
}

type fetchDoneMsg struct {
	repos    []gh.Repo
	username string
	err      error
}

type SetupModel struct {
	step          step
	width         int
	height        int
	spinner       tui.Spinner
	err           error
	result        SetupResult
	username      string
	stepChangedAt time.Time

	selectModel  selectModel
	groupModel   groupModel
	confirmModel confirmModel
}

func NewSetupModel() SetupModel {
	s := tui.NewSpinner()
	s.SetStyle(tui.DotSpinner)
	s.SetTextStyle(tui.NewStyle().Foreground("6"))

	return SetupModel{
		step:    stepLoading,
		spinner: s,
	}
}

func (m SetupModel) Init() tui.Cmd {
	return tui.Batch(m.spinner.Tick, fetchRepos)
}

func fetchRepos() tui.Msg {
	repos, username, err := gh.FetchAll()
	return fetchDoneMsg{repos: repos, username: username, err: err}
}

func (m SetupModel) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.selectModel.width = msg.Width
		m.selectModel.height = msg.Height
		m.groupModel.width = msg.Width
		m.groupModel.height = msg.Height
		m.confirmModel.width = msg.Width
		m.confirmModel.height = msg.Height
		return m, nil

	case tui.KeyMsg:

		if !m.stepChangedAt.IsZero() && time.Since(m.stepChangedAt) < 100*time.Millisecond {
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c":
			m.result = SetupResult{Canceled: true}
			return m, tui.Quit
		}
	}

	switch m.step {
	case stepLoading:
		return m.updateLoading(msg)
	case stepSelect:
		return m.updateSelect(msg)
	case stepGroup:
		return m.updateGroup(msg)
	case stepConfirm:
		return m.updateConfirm(msg)
	}

	return m, nil
}

func (m SetupModel) updateLoading(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case fetchDoneMsg:
		if msg.err != nil {
			m.result = SetupResult{Err: msg.err}
			return m, tui.Quit
		}
		m.username = msg.username
		m.selectModel = newSelectModel(msg.repos, msg.username, m.width, m.height)
		m.step = stepSelect
		m.stepChangedAt = time.Now()
		return m, m.selectModel.search.Focus()
	case tui.SpinnerTickMsg:
		var cmd tui.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m SetupModel) updateSelect(msg tui.Msg) (tui.Model, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok && key.String() == "enter" {
		selected := m.selectModel.selected()
		if len(selected) == 0 {
			return m, nil
		}
		m.groupModel = newGroupModel(selected, m.username, m.width, m.height)
		m.step = stepGroup
		m.stepChangedAt = time.Now()
		return m, nil
	}
	if key, ok := msg.(tui.KeyMsg); ok && key.String() == "escape" {
		m.result = SetupResult{Canceled: true}
		return m, tui.Quit
	}

	var cmd tui.Cmd
	m.selectModel, cmd = m.selectModel.update(msg)
	return m, cmd
}

func (m SetupModel) updateGroup(msg tui.Msg) (tui.Model, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "enter":
			if !m.groupModel.editing {
				m.confirmModel = newConfirmModel(m.groupModel.groups, m.username, m.width, m.height)
				m.step = stepConfirm
				m.stepChangedAt = time.Now()
				return m, nil
			}
		case "escape":
			if m.groupModel.editing {
				m.groupModel.editing = false
				return m, nil
			}

			m.step = stepSelect
			m.stepChangedAt = time.Now()
			return m, m.selectModel.search.Focus()
		}
	}

	var cmd tui.Cmd
	m.groupModel, cmd = m.groupModel.update(msg)
	return m, cmd
}

func (m SetupModel) updateConfirm(msg tui.Msg) (tui.Model, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			m.result = SetupResult{
				Confirmed: true,
				Groups:    m.confirmModel.groups,
				Username:  m.username,
			}
			return m, tui.Quit
		case "n", "N":
			m.result = SetupResult{Canceled: true}
			return m, tui.Quit
		case "escape":
			m.step = stepGroup
			m.stepChangedAt = time.Now()
			return m, nil
		}
	}
	return m, nil
}

func (m SetupModel) View() string {
	switch m.step {
	case stepLoading:
		if m.err != nil {
			return errorStyle.Render("Error: " + m.err.Error())
		}
		return "\n  " + m.spinner.View() + " Fetching repos from GitHub...\n"
	case stepSelect:
		return m.selectModel.view()
	case stepGroup:
		return m.groupModel.view()
	case stepConfirm:
		return m.confirmModel.view()
	}
	return ""
}

func (m SetupModel) GetResult() SetupResult {
	return m.result
}

var (
	titleStyle = tui.NewStyle().
			Bold(true).
			Foreground(tui.Color("15")).
			Background(tui.Color("6")).
			Padding(0, 1)

	subtitleStyle = tui.NewStyle().
			Foreground(tui.Color("8"))

	selectedStyle = tui.NewStyle().
			Foreground(tui.Color("6"))

	dimStyle = tui.NewStyle().
			Foreground(tui.Color("8"))

	cursorStyle = tui.NewStyle().
			Foreground(tui.Color("6")).
			Bold(true)

	errorStyle = tui.NewStyle().
			Foreground(tui.Color("1")).
			Bold(true)

	activeTabStyle = tui.NewStyle().
			Foreground(tui.Color("15")).
			Background(tui.Color("6")).
			Padding(0, 1)

	inactiveTabStyle = tui.NewStyle().
				Foreground(tui.Color("7")).
				Padding(0, 1)

	helpStyle = tui.NewStyle().
			Foreground(tui.Color("8"))

	groupHeaderStyle = tui.NewStyle().
				Foreground(tui.Color("6")).
				Bold(true)

	checkStyle = tui.NewStyle().
			Foreground(tui.Color("2"))

	uncheckStyle = tui.NewStyle().
			Foreground(tui.Color("8"))
)

type confirmModel struct {
	groups   []GroupEntry
	username string
	width    int
	height   int
}

func newConfirmModel(groups []GroupEntry, username string, w, h int) confirmModel {
	return confirmModel{
		groups:   groups,
		username: username,
		width:    w,
		height:   h,
	}
}

func (m confirmModel) view() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" ws setup "))
	b.WriteString("  Confirm\n\n")

	totalRepos := 0
	for _, g := range m.groups {
		totalRepos += len(g.Repos)
	}

	fmt.Fprintf(&b, "  %s groups, %s projects\n\n",
		selectedStyle.Render(fmt.Sprintf("%d", len(m.groups))),
		selectedStyle.Render(fmt.Sprintf("%d", totalRepos)))

	for _, g := range m.groups {
		fmt.Fprintf(&b, "  %s\n", groupHeaderStyle.Render(g.Name))
		for _, r := range g.Repos {
			cat := "work"
			if r.Owner == m.username {
				cat = "personal"
			}
			path := g.Name + "/" + r.Name

			fmt.Fprintf(&b, "    %-30s %-10s %s\n",
				r.Name, dimStyle.Render(cat), dimStyle.Render(path))
		}
		b.WriteString("\n")
	}

	b.WriteString(helpStyle.Render("  Save workspace registry? "))
	b.WriteString(selectedStyle.Render("y"))
	b.WriteString(helpStyle.Render("/"))
	b.WriteString(dimStyle.Render("n"))
	b.WriteString(helpStyle.Render("  (esc go back)"))

	return b.String()
}

type groupModel struct {
	groups      []GroupEntry
	cursor      int
	repoCursor  int
	editing     bool
	editInput   tui.TextInput
	moving      bool
	moveFrom    int
	moveRepoIdx int
	width       int
	height      int
	username    string
}

func newGroupModel(repos []gh.Repo, username string, w, h int) groupModel {
	groupMap := make(map[string][]gh.Repo)
	var order []string

	for _, r := range repos {
		groupName := r.Owner
		if r.Owner == username {
			groupName = "personal"
		}
		if _, exists := groupMap[groupName]; !exists {
			order = append(order, groupName)
		}
		groupMap[groupName] = append(groupMap[groupName], r)
	}

	groups := make([]GroupEntry, 0, len(order))
	for _, name := range order {
		groups = append(groups, GroupEntry{
			Name:  name,
			Repos: groupMap[name],
		})
	}

	ti := tui.NewTextInput()
	ti.SetCharLimit(40)

	return groupModel{
		groups:     groups,
		repoCursor: -1,
		editInput:  ti,
		width:      w,
		height:     h,
		username:   username,
	}
}

func (m groupModel) totalItems() int {
	n := 0
	for _, g := range m.groups {
		n += 1 + len(g.Repos)
	}
	return n
}

func (m groupModel) flatToGroupRepo(flat int) (int, int) {
	pos := 0
	for gi, g := range m.groups {
		if pos == flat {
			return gi, -1
		}
		pos++
		for ri := range g.Repos {
			if pos == flat {
				return gi, ri
			}
			pos++
		}
	}
	return 0, -1
}

func (m groupModel) groupRepoToFlat(gi, ri int) int {
	pos := 0
	for i, g := range m.groups {
		if i == gi && ri == -1 {
			return pos
		}
		pos++
		for j := range g.Repos {
			if i == gi && j == ri {
				return pos
			}
			pos++
		}
	}
	return 0
}

func (m groupModel) flatCursor() int {
	m.clampCursor()
	return m.groupRepoToFlat(m.cursor, m.repoCursor)
}

func (m *groupModel) clampCursor() {
	if len(m.groups) == 0 {
		m.cursor = 0
		m.repoCursor = -1
		return
	}
	if m.cursor >= len(m.groups) {
		m.cursor = len(m.groups) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.repoCursor >= len(m.groups[m.cursor].Repos) {
		m.repoCursor = len(m.groups[m.cursor].Repos) - 1
	}
}

func (m groupModel) update(msg tui.Msg) (groupModel, tui.Cmd) {
	if m.editing {
		return m.updateEditing(msg)
	}
	if m.moving {
		return m.updateMoving(msg)
	}

	key, ok := msg.(tui.KeyMsg)
	if !ok {
		return m, nil
	}

	total := m.totalItems()
	flat := m.flatCursor()

	switch key.String() {
	case "up", "k":
		if flat > 0 {
			m.cursor, m.repoCursor = m.flatToGroupRepo(flat - 1)
		}

	case "down", "j":
		if flat < total-1 {
			m.cursor, m.repoCursor = m.flatToGroupRepo(flat + 1)
		}

	case "r":

		if m.repoCursor == -1 && m.cursor < len(m.groups) {
			m.editing = true
			m.editInput.SetValue(m.groups[m.cursor].Name)
			m.editInput.Focus()
			return m, nil
		}

	case "m":

		if m.repoCursor >= 0 {
			m.moving = true
			m.moveFrom = m.cursor
			m.moveRepoIdx = m.repoCursor
		}

	case "n":

		m.groups = append(m.groups, GroupEntry{Name: "new-group"})
		m.cursor = len(m.groups) - 1
		m.repoCursor = -1
		m.editing = true
		m.editInput.SetValue("new-group")
		m.editInput.Focus()
		return m, nil
	}

	return m, nil
}

func (m groupModel) updateEditing(msg tui.Msg) (groupModel, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "enter":
			newName := strings.TrimSpace(m.editInput.Value())
			if newName != "" {
				m.groups[m.cursor].Name = newName
			}
			m.editing = false
			return m, nil
		case "escape":
			m.editing = false
			return m, nil
		}
	}

	var cmd tui.Cmd
	m.editInput, cmd = m.editInput.Update(msg)
	return m, cmd
}

func (m groupModel) updateMoving(msg tui.Msg) (groupModel, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "escape":
			m.moving = false
			return m, nil
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.groups)-1 {
				m.cursor++
			}
		case "enter":
			if m.cursor != m.moveFrom {
				repo := m.groups[m.moveFrom].Repos[m.moveRepoIdx]
				m.groups[m.moveFrom].Repos = append(
					m.groups[m.moveFrom].Repos[:m.moveRepoIdx],
					m.groups[m.moveFrom].Repos[m.moveRepoIdx+1:]...,
				)
				m.groups[m.cursor].Repos = append(m.groups[m.cursor].Repos, repo)

				if len(m.groups[m.moveFrom].Repos) == 0 {
					m.groups = append(m.groups[:m.moveFrom], m.groups[m.moveFrom+1:]...)
					if m.cursor > m.moveFrom {
						m.cursor--
					}
				}
			}
			m.moving = false
			m.repoCursor = -1
			m.clampCursor()
		}
	}
	return m, nil
}

func (m groupModel) view() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" ws setup "))
	b.WriteString("  Assign groups\n")
	b.WriteString(subtitleStyle.Render("  Auto-grouped by org. Rename, move, or create new groups."))
	b.WriteString("\n\n")

	if m.moving {
		b.WriteString(selectedStyle.Render(fmt.Sprintf(
			"  Moving: %s → select target group\n\n",
			m.groups[m.moveFrom].Repos[m.moveRepoIdx].Name)))
	}

	flat := 0
	currentFlat := m.flatCursor()

	for gi, g := range m.groups {
		isCurrent := flat == currentFlat
		prefix := "  "
		if isCurrent {
			prefix = cursorStyle.Render("> ")
		}

		header := g.Name
		if m.editing && gi == m.cursor && m.repoCursor == -1 {
			header = m.editInput.View()
		} else if isCurrent {
			header = selectedStyle.Render(header)
		} else {
			header = groupHeaderStyle.Render(header)
		}

		if m.moving && gi == m.cursor {
			header += selectedStyle.Render(" ← move here")
		}

		fmt.Fprintf(&b, "%s┌ %s (%d repos)\n", prefix, header, len(g.Repos))
		flat++

		for _, r := range g.Repos {
			isCurrent = flat == currentFlat
			rPrefix := "  │  "
			if isCurrent {
				rPrefix = cursorStyle.Render("> ") + "│  "
			}

			name := r.Name
			if isCurrent {
				name = selectedStyle.Render(name)
			}

			fmt.Fprintf(&b, "%s%s\n", rPrefix, name)
			flat++
		}

		b.WriteString("  └\n")
	}

	b.WriteString("\n")
	if m.moving {
		b.WriteString(helpStyle.Render("  ↑↓ select group  enter confirm  esc cancel"))
	} else {
		b.WriteString(helpStyle.Render("  ↑↓ navigate  r rename  m move  n new group  enter finish  esc back"))
	}

	return b.String()
}

type sortMode int

const (
	sortActivity sortMode = iota
	sortName
	sortPushed
)

func (s sortMode) String() string {
	switch s {
	case sortActivity:
		return "activity"
	case sortName:
		return "name"
	case sortPushed:
		return "pushed"
	}
	return ""
}

type repoItem struct {
	repo    gh.Repo
	checked bool
}

type selectModel struct {
	all       []repoItem
	orgs      []string
	orgFilter int
	sortBy    sortMode
	cursor    int
	offset    int
	search    tui.TextInput
	width     int
	height    int
	username  string
}

func newSelectModel(repos []gh.Repo, username string, w, h int) selectModel {
	items := make([]repoItem, len(repos))
	for i, r := range repos {
		items[i] = repoItem{repo: r}
	}

	ti := tui.NewTextInput()
	ti.SetPlaceholder("type to search...")
	ti.SetCharLimit(60)

	orgs := gh.Orgs(repos)

	return selectModel{
		all:      items,
		orgs:     orgs,
		search:   ti,
		width:    w,
		height:   h,
		username: username,
	}
}

func (m selectModel) filtered() []int {
	query := strings.ToLower(m.search.Value())
	var org string
	if m.orgFilter > 0 && m.orgFilter <= len(m.orgs) {
		org = m.orgs[m.orgFilter-1]
	}

	var indices []int
	for i, item := range m.all {
		if org != "" && item.repo.Owner != org {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.repo.FullName), query) {
			continue
		}
		indices = append(indices, i)
	}

	sort.SliceStable(indices, func(a, b int) bool {
		ra := m.all[indices[a]].repo
		rb := m.all[indices[b]].repo
		switch m.sortBy {
		case sortName:
			return ra.FullName < rb.FullName
		case sortPushed:
			return ra.PushedAt.After(rb.PushedAt)
		default:
			if ra.Activity != rb.Activity {
				return ra.Activity > rb.Activity
			}
			return ra.PushedAt.After(rb.PushedAt)
		}
	})

	return indices
}

func (m selectModel) selected() []gh.Repo {
	var sel []gh.Repo
	for _, item := range m.all {
		if item.checked {
			sel = append(sel, item.repo)
		}
	}
	return sel
}

func (m selectModel) selectedCount() int {
	n := 0
	for _, item := range m.all {
		if item.checked {
			n++
		}
	}
	return n
}

func (m selectModel) update(msg tui.Msg) (selectModel, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		filtered := m.filtered()
		maxVisible := m.maxVisible()

		switch key.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				if m.cursor < m.offset {
					m.offset = m.cursor
				}
			}
			return m, nil

		case "down", "j":
			if m.cursor < len(filtered)-1 {
				m.cursor++
				if m.cursor >= m.offset+maxVisible {
					m.offset = m.cursor - maxVisible + 1
				}
			}
			return m, nil

		case " ":
			if len(filtered) > 0 && m.cursor < len(filtered) {
				idx := filtered[m.cursor]
				m.all[idx].checked = !m.all[idx].checked
			}
			return m, nil

		case "tab":
			m.orgFilter = (m.orgFilter + 1) % (len(m.orgs) + 1)
			m.cursor = 0
			m.offset = 0
			return m, nil

		case "ctrl+a":

			allChecked := true
			for _, idx := range filtered {
				if !m.all[idx].checked {
					allChecked = false
					break
				}
			}
			for _, idx := range filtered {
				m.all[idx].checked = !allChecked
			}
			return m, nil

		case "ctrl+s":
			m.sortBy = (m.sortBy + 1) % 3
			m.cursor = 0
			m.offset = 0
			return m, nil
		}
	}

	var cmd tui.Cmd
	prevVal := m.search.Value()
	m.search, cmd = m.search.Update(msg)
	if m.search.Value() != prevVal {
		m.cursor = 0
		m.offset = 0
	}
	return m, cmd
}

func (m selectModel) maxVisible() int {
	h := m.height - 9
	if h < 5 {
		h = 5
	}
	return h
}

func (m selectModel) view() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" ws setup "))
	b.WriteString("  Select repos\n\n")

	b.WriteString("  " + m.search.View() + "    ")
	b.WriteString(dimStyle.Render("sort: ") + selectedStyle.Render(m.sortBy.String()))
	b.WriteString(dimStyle.Render(" (ctrl+s)"))
	b.WriteString("\n")

	b.WriteString("  ")
	if m.orgFilter == 0 {
		b.WriteString(activeTabStyle.Render("all"))
	} else {
		b.WriteString(inactiveTabStyle.Render("all"))
	}
	for i, org := range m.orgs {
		b.WriteString(" ")
		if m.orgFilter == i+1 {
			b.WriteString(activeTabStyle.Render(org))
		} else {
			b.WriteString(inactiveTabStyle.Render(org))
		}
	}
	b.WriteString(dimStyle.Render("  (tab)"))
	b.WriteString("\n\n")

	filtered := m.filtered()
	maxVisible := m.maxVisible()

	if len(filtered) == 0 {
		b.WriteString("  " + dimStyle.Render("no repos match") + "\n")
	}

	end := m.offset + maxVisible
	if end > len(filtered) {
		end = len(filtered)
	}

	for vi := m.offset; vi < end; vi++ {
		idx := filtered[vi]
		item := m.all[idx]
		isCursor := vi == m.cursor

		prefix := "  "
		if isCursor {
			prefix = cursorStyle.Render("> ")
		}

		check := uncheckStyle.Render("○")
		if item.checked {
			check = checkStyle.Render("●")
		}

		name := item.repo.FullName
		if isCursor {
			name = selectedStyle.Render(name)
		}

		pushed := humanizeTimeShort(item.repo.PushedAt)
		activity := activityBar(item.repo.Activity)

		privLabel := ""
		if item.repo.Private {
			privLabel = dimStyle.Render(" ◆")
		}

		fmt.Fprintf(&b, "%s %s %s%s  %s  %s\n",
			prefix, check, name, privLabel,
			dimStyle.Render(pushed), activity)
	}

	if len(filtered) > maxVisible {
		above := m.offset
		below := len(filtered) - end
		if above > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more\n", above)))
		}
		if below > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more\n", below)))
		}
	}

	b.WriteString("\n")
	selCount := m.selectedCount()
	fmt.Fprintf(&b, "  Selected: %s / %d",
		selectedStyle.Render(fmt.Sprintf("%d", selCount)),
		len(m.all))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  ↑↓ navigate  space select  ctrl+a toggle all  enter next  esc quit"))

	return b.String()
}

func activityBar(count int) string {
	if count == 0 {
		return dimStyle.Render("○○○○○")
	}
	filled := count / 5
	if filled > 5 {
		filled = 5
	}
	if filled == 0 && count > 0 {
		filled = 1
	}
	bar := strings.Repeat("●", filled) + strings.Repeat("○", 5-filled)
	return selectedStyle.Render(bar)
}

func humanizeTimeShort(t time.Time) string {
	if t.IsZero() {
		return "     -"
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%3dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%3dh ago", int(d.Hours()))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%3dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01")
	}
}
