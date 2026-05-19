package setup

import (
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

type Result struct {
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

type Model struct {
	step          step
	width         int
	height        int
	spinner       tui.Spinner
	err           error
	result        Result
	username      string
	stepChangedAt time.Time

	selectModel  selectModel
	groupModel   groupModel
	confirmModel confirmModel
}

func NewModel() Model {
	s := tui.NewSpinner()
	s.SetStyle(tui.DotSpinner)
	s.SetTextStyle(tui.NewStyle().Foreground("6"))

	return Model{
		step:    stepLoading,
		spinner: s,
	}
}

func (m Model) Init() tui.Cmd {
	return tui.Batch(m.spinner.Tick, fetchRepos)
}

func fetchRepos() tui.Msg {
	repos, username, err := gh.FetchAll()
	return fetchDoneMsg{repos: repos, username: username, err: err}
}

func (m Model) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
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
			m.result = Result{Canceled: true}
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

func (m Model) updateLoading(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case fetchDoneMsg:
		if msg.err != nil {
			m.result = Result{Err: msg.err}
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

func (m Model) updateSelect(msg tui.Msg) (tui.Model, tui.Cmd) {
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
		m.result = Result{Canceled: true}
		return m, tui.Quit
	}

	var cmd tui.Cmd
	m.selectModel, cmd = m.selectModel.update(msg)
	return m, cmd
}

func (m Model) updateGroup(msg tui.Msg) (tui.Model, tui.Cmd) {
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

func (m Model) updateConfirm(msg tui.Msg) (tui.Model, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			m.result = Result{
				Confirmed: true,
				Groups:    m.confirmModel.groups,
				Username:  m.username,
			}
			return m, tui.Quit
		case "n", "N":
			m.result = Result{Canceled: true}
			return m, tui.Quit
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

func (m Model) GetResult() Result {
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
