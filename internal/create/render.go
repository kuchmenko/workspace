package create

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m CreateModel) View() string {
	switch m.st {
	case stateLoadingOwners:
		return fmt.Sprintf("\n  %s %s loading GitHub owners…\n", m.spinner.View(), createTitle.Render(" ws create "))
	case stateErrored:
		return m.viewErrored()
	case stateDone:
		return m.viewDone()
	case stateCreating:
		return m.viewCreating()
	}
	return m.viewForm()
}

func (m CreateModel) viewForm() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(createTitle.Render(" ws create "))
	b.WriteString("  ")
	b.WriteString(createDim.Render("Bootstrap a new GitHub repo, register, and clone."))
	b.WriteString("\n\n")

	b.WriteString(m.renderOwnerList())
	b.WriteString("\n")
	b.WriteString(m.renderField("Name", m.nameInput.View(), focusName))
	b.WriteString("\n")
	b.WriteString(m.renderToggle("Visibility", []string{"private", "public"}, m.visIdx, focusVisibility))
	b.WriteString("\n")
	b.WriteString(m.renderField("Description", m.descInput.View(), focusDescription))
	b.WriteString("\n")
	b.WriteString(m.renderToggle("Category", []string{"personal", "work"}, m.catIdx, focusCategory))
	b.WriteString("\n")
	b.WriteString(m.renderField("Group", m.groupInput.View(), focusGroup))
	b.WriteString("\n")
	b.WriteString(m.renderCreateButton())
	b.WriteString("\n\n")
	b.WriteString(createDim.Render("tab/shift-tab move between fields • ←/→ toggles • esc cancels"))
	b.WriteString("\n")
	return b.String()
}

func (m CreateModel) renderOwnerList() string {
	var b strings.Builder
	header := "Owner"
	if m.focus == focusOwner {
		header = createCursor.Render("▸ ") + createAccent.Render(header)
	} else {
		header = "  " + createLabel.Render(header)
	}
	b.WriteString(header)
	b.WriteString("\n")
	if len(m.owners) == 0 {
		b.WriteString("    " + createDim.Render("(no owners loaded)"))
		return b.String()
	}

	const maxRows = 6
	start := m.ownerScroll
	if m.ownerCursor < start {
		start = m.ownerCursor
	}
	if m.ownerCursor >= start+maxRows {
		start = m.ownerCursor - maxRows + 1
	}
	if start < 0 {
		start = 0
	}
	end := start + maxRows
	if end > len(m.owners) {
		end = len(m.owners)
	}
	for i := start; i < end; i++ {
		o := m.owners[i]
		marker := "  "
		name := o.Login
		if i == m.ownerCursor {
			marker = createCursor.Render("● ")
			name = createAccent.Render(name)
		} else {
			name = createItemName.Render(name)
		}
		tag := ""
		if o.Kind == OwnerKindUser {
			tag = " " + createDim.Render("(you)")
		}
		b.WriteString("    " + marker + name + tag + "\n")
	}
	if end < len(m.owners) {
		b.WriteString("    " + createDim.Render(fmt.Sprintf("…%d more", len(m.owners)-end)) + "\n")
	}
	return b.String()
}

func (m CreateModel) renderField(label, view string, fieldFocus int) string {
	cursor := "  "
	lbl := createLabel.Render(label)
	if m.focus == fieldFocus {
		cursor = createCursor.Render("▸ ")
		lbl = createAccent.Render(label)
	}
	return fmt.Sprintf("%s%s\n    %s", cursor, lbl, view)
}

func (m CreateModel) renderToggle(label string, options []string, idx, fieldFocus int) string {
	cursor := "  "
	lbl := createLabel.Render(label)
	if m.focus == fieldFocus {
		cursor = createCursor.Render("▸ ")
		lbl = createAccent.Render(label)
	}
	parts := make([]string, len(options))
	for i, o := range options {
		if i == idx {
			parts[i] = createChip.Render("[" + o + "]")
		} else {
			parts[i] = createDim.Render(" " + o + " ")
		}
	}
	return fmt.Sprintf("%s%s\n    %s", cursor, lbl, strings.Join(parts, " "))
}

func (m CreateModel) renderCreateButton() string {
	cursor := "  "
	label := createBtn.Render(" Create ")
	if m.focus == focusCreate {
		cursor = createCursor.Render("▸ ")
		label = createBtnFocus.Render(" Create ")
	}
	return cursor + label + "  " + createDim.Render("(enter to confirm)")
}

func (m CreateModel) viewErrored() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(createTitle.Render(" ws create "))
	b.WriteString("\n\n  ")
	b.WriteString(createErr.Render("error: "))
	b.WriteString(m.err.Error())
	b.WriteString("\n\n  ")
	b.WriteString(createDim.Render("enter to retry • esc to cancel"))
	b.WriteString("\n")
	return b.String()
}

func (m CreateModel) viewCreating() string {
	owner := m.currentOwner()
	name := strings.TrimSpace(m.nameInput.Value())
	return fmt.Sprintf(
		"\n  %s %s creating %s/%s…\n",
		m.spinner.View(),
		createTitle.Render(" ws create "),
		createAccent.Render(owner),
		createAccent.Render(name),
	)
}

func (m CreateModel) viewDone() string {
	var b strings.Builder
	b.WriteString("\n  ")
	b.WriteString(createCheck.Render("✓ "))
	b.WriteString(createTitle.Render(" ws create "))
	b.WriteString("\n\n")
	if m.result != nil {
		fmt.Fprintf(&b, "    project:  %s\n", createAccent.Render(m.result.Name))
		fmt.Fprintf(&b, "    remote:   %s\n", createDim.Render(m.result.URL))
		fmt.Fprintf(&b, "    path:     %s\n", createDim.Render(m.result.Project.Path))
	}
	b.WriteString("\n  ")
	b.WriteString(createDim.Render("press any key to exit"))
	b.WriteString("\n")
	return b.String()
}

var (
	createTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("6")).
			Padding(0, 1)
	createDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	createLabel    = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Bold(true)
	createCursor   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	createAccent   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	createErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	createCheck    = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	createChip     = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	createItemName = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	createBtn      = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Background(lipgloss.Color("8"))
	createBtnFocus = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6")).Bold(true)
)
