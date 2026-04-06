package setup

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	gh "github.com/kuchmenko/workspace/internal/github"
)

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
	orgFilter int // 0 = all, 1+ = specific org
	sortBy    sortMode
	cursor    int
	offset    int
	search    textinput.Model
	width     int
	height    int
	username  string
}

func newSelectModel(repos []gh.Repo, username string, w, h int) selectModel {
	items := make([]repoItem, len(repos))
	for i, r := range repos {
		items[i] = repoItem{repo: r}
	}

	ti := textinput.New()
	ti.Placeholder = "type to search..."
	ti.CharLimit = 60

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

	// Sort
	sort.SliceStable(indices, func(a, b int) bool {
		ra := m.all[indices[a]].repo
		rb := m.all[indices[b]].repo
		switch m.sortBy {
		case sortName:
			return ra.FullName < rb.FullName
		case sortPushed:
			return ra.PushedAt.After(rb.PushedAt)
		default: // sortActivity
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

func (m selectModel) update(msg tea.Msg) (selectModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
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
			// Toggle all visible
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

	// Pass to text input
	var cmd tea.Cmd
	prevVal := m.search.Value()
	m.search, cmd = m.search.Update(msg)
	if m.search.Value() != prevVal {
		m.cursor = 0
		m.offset = 0
	}
	return m, cmd
}

func (m selectModel) maxVisible() int {
	// header(3) + search(1) + orgs(1) + blank(1) + help(2) + status(1) = 9
	h := m.height - 9
	if h < 5 {
		h = 5
	}
	return h
}

func (m selectModel) view() string {
	var b strings.Builder

	// Title
	b.WriteString(titleStyle.Render(" ws setup "))
	b.WriteString("  Select repos\n\n")

	// Search
	b.WriteString("  " + m.search.View() + "    ")
	b.WriteString(dimStyle.Render("sort: ") + selectedStyle.Render(m.sortBy.String()))
	b.WriteString(dimStyle.Render(" (ctrl+s)"))
	b.WriteString("\n")

	// Org tabs
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

	// List
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

		pushed := humanizeTime(item.repo.PushedAt)
		activity := activityBar(item.repo.Activity)

		privLabel := ""
		if item.repo.Private {
			privLabel = dimStyle.Render(" ◆")
		}

		b.WriteString(fmt.Sprintf("%s %s %s%s  %s  %s\n",
			prefix, check, name, privLabel,
			dimStyle.Render(pushed), activity))
	}

	// Scrollbar hint
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

	// Footer
	b.WriteString("\n")
	selCount := m.selectedCount()
	b.WriteString(fmt.Sprintf("  Selected: %s / %d",
		selectedStyle.Render(fmt.Sprintf("%d", selCount)),
		len(m.all)))
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

func humanizeTime(t time.Time) string {
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
