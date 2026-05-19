package add

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/config"
)

func (m AddModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "tab", "down":
		m.editFocus = (m.editFocus + 1) % 4
	case "shift+tab", "up":
		m.editFocus = (m.editFocus + 3) % 4
	case "enter":

		if err := m.validateEdit(); err != nil {
			m.editErr = err.Error()
			return m, nil
		}
		m.editFields.Path = buildPath(m.editFields.Group, m.editFields.Category, m.editFields.Name)
		m.transitionTo(addStateConfirm)
		return m, nil
	case "esc":
		m.transitionTo(addStateBrowse)
		return m, nil
	default:

		s := key.String()

		if key.Type == tea.KeyRunes {
			runes := key.Runes
			m.applyEditRunes(runes)
			return m, nil
		}
		if s == "backspace" {
			m.applyEditBackspace()
			return m, nil
		}
	}
	return m, nil
}

func (m *AddModel) applyEditRunes(runes []rune) {
	r := string(runes)
	switch m.editFocus {
	case 0:
		m.editFields.Name += r
	case 1:
		m.editFields.URL += r
	case 2:

		if r == " " {
			if m.editFields.Category == config.CategoryPersonal {
				m.editFields.Category = config.CategoryWork
			} else {
				m.editFields.Category = config.CategoryPersonal
			}
		}
	case 3:
		m.editFields.Group += r
	}
	m.editFields.Path = buildPath(m.editFields.Group, m.editFields.Category, m.editFields.Name)
}

func (m *AddModel) applyEditBackspace() {
	switch m.editFocus {
	case 0:
		if len(m.editFields.Name) > 0 {
			m.editFields.Name = m.editFields.Name[:len(m.editFields.Name)-1]
		}
	case 1:
		if len(m.editFields.URL) > 0 {
			m.editFields.URL = m.editFields.URL[:len(m.editFields.URL)-1]
		}
	case 3:
		if len(m.editFields.Group) > 0 {
			m.editFields.Group = m.editFields.Group[:len(m.editFields.Group)-1]
		}
	}
	m.editFields.Path = buildPath(m.editFields.Group, m.editFields.Category, m.editFields.Name)
}

func (m AddModel) validateEdit() error {
	if strings.TrimSpace(m.editFields.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(m.editFields.URL) == "" {
		return errors.New("URL is required")
	}
	if m.editFields.Category != config.CategoryPersonal && m.editFields.Category != config.CategoryWork {
		return errors.New("category must be personal or work")
	}
	if _, exists := m.ws.Projects[m.editFields.Name]; exists {
		return fmt.Errorf("name %q is already registered", m.editFields.Name)
	}
	return nil
}

func (m AddModel) viewEdit() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Edit project "))
	b.WriteString("\n\n")

	rows := []struct{ label, value string }{
		{"Name", m.editFields.Name},
		{"URL", m.editFields.URL},
		{"Category", string(m.editFields.Category) + addDim.Render("   (space to toggle: personal | work)")},
		{"Group", m.editFields.Group + addDim.Render("   (auto-inferred; empty → category)")},
	}
	for i, r := range rows {
		marker := "  "
		label := r.label
		if i == m.editFocus {
			marker = addCursor.Render("▸ ")
			label = addAccent.Render(r.label)
		}
		fmt.Fprintf(&b, "  %s%s: %s\n", marker, addPad(label, 12), r.value)
	}
	fmt.Fprintf(&b, "\n  %s: %s\n", addPad("Path", 12), addDim.Render(m.editFields.Path))

	if m.editErr != "" {
		b.WriteString("\n  " + addErr.Render(m.editErr) + "\n")
	}
	b.WriteString("\n  " + addHelp.Render("[tab/↑↓] field  [⏎] confirm  [esc] back"))
	return b.String()
}

func (m AddModel) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			m.queue = append(m.queue, m.editFields)
			m.currentIdx = 0
			m.transitionTo(addStateCloning)
			return m, tea.Batch(m.spinner.Tick, m.startCloneJob(0))
		case "n", "N", "esc":
			m.transitionTo(addStateBrowse)
			return m, nil
		}
	}
	return m, nil
}

func (m AddModel) viewConfirm() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Confirm "))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  Add  %s\n", addAccent.Render(m.editFields.Name))
	fmt.Fprintf(&b, "       %s\n", addDim.Render(m.editFields.URL))
	fmt.Fprintf(&b, "       %s → %s\n\n",
		string(m.editFields.Category),
		addDim.Render(m.editFields.Path))
	if m.editFields.FromDisk != "" {
		b.WriteString("  " + addDim.Render("(disk) repo already at "+m.editFields.FromDisk+
			" — register only, no clone\n"))
		b.WriteString("\n")
	}
	b.WriteString("  " + addHelp.Render("[y/⏎] add   [n/esc] back"))
	return b.String()
}

func (m AddModel) updateBulkConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "y", "Y", "enter":
		queue := m.buildBulkQueue()
		if len(queue) == 0 {
			m.transitionTo(addStateBrowse)
			return m, nil
		}
		m.queue = queue
		m.currentIdx = 0
		m.selectedURLs = nil
		m.transitionTo(addStateCloning)
		return m, tea.Batch(m.spinner.Tick, m.startCloneJob(0))
	case "n", "N", "esc":
		m.transitionTo(addStateBrowse)
		return m, nil
	}
	return m, nil
}

func (m AddModel) buildBulkQueue() []editFields {
	if len(m.selectedURLs) == 0 {
		return nil
	}
	var out []editFields
	for i := range m.allSuggestions {
		s := m.allSuggestions[i]
		if !m.selectedURLs[s.RemoteURL] {
			continue
		}
		if s.RegisteredPath != "" {
			continue
		}
		out = append(out, m.editFromSuggestion(s))
	}
	return out
}

func (m AddModel) viewBulkConfirm() string {
	queue := m.buildBulkQueue()
	var b strings.Builder
	b.WriteString(addTitle.Render(" Bulk add "))
	b.WriteString("\n\n")
	if len(queue) == 0 {
		b.WriteString("  " + addDim.Render("(no eligible URLs — every selection is already registered)\n"))
		b.WriteString("\n  " + addHelp.Render("[esc] back"))
		return b.String()
	}
	fmt.Fprintf(&b, "  Will add %s repos:\n\n", addAccent.Render(fmt.Sprintf("%d", len(queue))))
	const max = 10
	shown := queue
	if len(shown) > max {
		shown = shown[:max]
	}
	for _, ef := range shown {
		fmt.Fprintf(&b, "  • %s  %s  %s\n",
			addItemName.Render(addPad(ef.Name, 24)),
			addDim.Render(fmt.Sprintf("[%s]", ef.Category)),
			addDim.Render(ef.URL))
	}
	if len(queue) > max {
		fmt.Fprintf(&b, "  %s\n", addDim.Render(fmt.Sprintf("…and %d more", len(queue)-max)))
	}
	b.WriteString("\n  " + addHelp.Render("[y/⏎] confirm   [n/esc] back"))
	return b.String()
}
