package setup

import (
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	gh "github.com/kuchmenko/workspace/internal/github"
)

type step int

const (
	stepLoading step = iota
	stepSelect
	stepGroup
	stepConfirm
)

// Result holds the final output of the setup wizard.
type Result struct {
	Confirmed bool
	Cancelled bool
	Err       error
	Groups    []GroupEntry
	Username  string
}

type GroupEntry struct {
	Name  string
	Repos []gh.Repo
}

// fetchDoneMsg is sent when GitHub data is fetched.
type fetchDoneMsg struct {
	repos    []gh.Repo
	username string
	err      error
}

type Model struct {
	step          step
	width         int
	height        int
	spinner       spinner.Model
	err           error
	result        Result
	username      string
	stepChangedAt time.Time // debounce key events on step transitions

	selectModel  selectModel
	groupModel   groupModel
	confirmModel confirmModel
}

func NewModel() Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

	return Model{
		step:    stepLoading,
		spinner: s,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, fetchRepos)
}

func fetchRepos() tea.Msg {
	repos, username, err := gh.FetchAll()
	return fetchDoneMsg{repos: repos, username: username, err: err}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.selectModel.width = msg.Width
		m.selectModel.height = msg.Height
		m.groupModel.width = msg.Width
		m.groupModel.height = msg.Height
		m.confirmModel.width = msg.Width
		m.confirmModel.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Ignore key events within 100ms of a step transition to prevent phantom inputs
		if !m.stepChangedAt.IsZero() && time.Since(m.stepChangedAt) < 100*time.Millisecond {
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c":
			m.result = Result{Cancelled: true}
			return m, tea.Quit
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

func (m Model) updateLoading(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case fetchDoneMsg:
		if msg.err != nil {
			m.result = Result{Err: msg.err}
			return m, tea.Quit
		}
		m.username = msg.username
		m.selectModel = newSelectModel(msg.repos, msg.username, m.width, m.height)
		m.step = stepSelect
		m.stepChangedAt = time.Now()
		return m, m.selectModel.search.Focus()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateSelect(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" {
		selected := m.selectModel.selected()
		if len(selected) == 0 {
			return m, nil
		}
		m.groupModel = newGroupModel(selected, m.username, m.width, m.height)
		m.step = stepGroup
		m.stepChangedAt = time.Now()
		return m, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "escape" {
		m.result = Result{Cancelled: true}
		return m, tea.Quit
	}

	var cmd tea.Cmd
	m.selectModel, cmd = m.selectModel.update(msg)
	return m, cmd
}

func (m Model) updateGroup(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
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
			// Go back to select
			m.step = stepSelect
			m.stepChangedAt = time.Now()
			return m, m.selectModel.search.Focus()
		}
	}

	var cmd tea.Cmd
	m.groupModel, cmd = m.groupModel.update(msg)
	return m, cmd
}

func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			m.result = Result{
				Confirmed: true,
				Groups:    m.confirmModel.groups,
				Username:  m.username,
			}
			return m, tea.Quit
		case "n", "N":
			m.result = Result{Cancelled: true}
			return m, tea.Quit
		case "escape":
			m.step = stepGroup
			m.stepChangedAt = time.Now()
			return m, nil
		}
	}
	return m, nil
}

func (m Model) View() string {
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

// GetResult returns the final result after the program exits.
func (m Model) GetResult() Result {
	return m.result
}

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("6")).
			Padding(0, 1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	cursorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).
			Bold(true)

	activeTabStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("6")).
			Padding(0, 1)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("7")).
				Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	groupHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("6")).
				Bold(true)

	checkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))

	uncheckStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))
)
