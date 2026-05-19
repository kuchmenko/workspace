package create

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kuchmenko/workspace/internal/config"
)

type state int

const (
	stateLoadingOwners state = iota
	stateForm
	stateCreating
	stateDone
	stateErrored
)

const (
	focusOwner = iota
	focusName
	focusVisibility
	focusDescription
	focusCategory
	focusGroup
	focusCreate
	focusCount
)

type CreateModelOptions struct {
	WsRoot    string
	Workspace *config.Workspace
	Save      func(*config.Workspace) error
	GHRunner  ghRunner

	Owner       string
	Name        string
	Visibility  Visibility
	Description string
	Category    config.Category
	Group       string
	ProjectName string
	URLFor      func(owner, name string) string
}

type CreateModel struct {
	opts CreateModelOptions

	st  state
	err error

	owners       []Owner
	ownerCursor  int
	ownerScroll  int
	visibilities []Visibility
	visIdx       int
	categories   []config.Category
	catIdx       int

	focus      int
	nameInput  textinput.Model
	descInput  textinput.Model
	groupInput textinput.Model

	spinner spinner.Model

	width  int
	height int

	result   *Result
	canceled bool
}

func NewCreateModel(opts CreateModelOptions) CreateModel {
	cat := opts.Category
	if cat == "" {
		cat = config.CategoryPersonal
	}
	vis := opts.Visibility
	if vis == "" {
		vis = VisibilityPrivate
	}

	name := textinput.New()
	name.Placeholder = "my-new-repo"
	name.CharLimit = 100
	name.Width = 40
	name.SetValue(opts.Name)

	desc := textinput.New()
	desc.Placeholder = "(optional) one-line description"
	desc.CharLimit = 200
	desc.Width = 60
	desc.SetValue(opts.Description)

	group := textinput.New()
	group.Placeholder = "(optional) project group/dir"
	group.CharLimit = 80
	group.Width = 40
	group.SetValue(opts.Group)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = createAccent

	visibilities := []Visibility{VisibilityPrivate, VisibilityPublic}
	visIdx := 0
	for i, v := range visibilities {
		if v == vis {
			visIdx = i
			break
		}
	}

	categories := []config.Category{config.CategoryPersonal, config.CategoryWork}
	catIdx := 0
	for i, c := range categories {
		if c == cat {
			catIdx = i
			break
		}
	}

	m := CreateModel{
		opts:         opts,
		st:           stateLoadingOwners,
		nameInput:    name,
		descInput:    desc,
		groupInput:   group,
		spinner:      sp,
		visibilities: visibilities,
		visIdx:       visIdx,
		categories:   categories,
		catIdx:       catIdx,
		focus:        focusName,
	}
	return m
}

func (m CreateModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.fetchOwnersCmd())
}

func (m CreateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case ownersLoadedMsg:
		m.owners = msg.owners

		if m.opts.Owner != "" {
			for i, o := range m.owners {
				if o.Login == m.opts.Owner {
					m.ownerCursor = i
					break
				}
			}
		}
		m.st = stateForm

		m.focus = focusName
		m.nameInput.Focus()
		return m, nil

	case ownersErrMsg:
		m.err = msg.err
		m.st = stateErrored
		return m, nil

	case createDoneMsg:
		m.result = msg.result
		m.st = stateDone
		return m, nil

	case createErrMsg:
		m.err = msg.err
		m.st = stateErrored
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m CreateModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.st {
	case stateLoadingOwners:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tea.Quit
		}
		return m, nil

	case stateErrored:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.canceled = true
			return m, tea.Quit
		case "enter":

			if len(m.owners) == 0 {
				m.err = nil
				m.st = stateLoadingOwners
				return m, m.fetchOwnersCmd()
			}
			m.err = nil
			m.st = stateForm
			return m, m.refocus()
		}
		return m, nil

	case stateDone:

		return m, tea.Quit

	case stateCreating:

		if msg.String() == "ctrl+c" {
			m.canceled = true
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		m.canceled = true
		return m, tea.Quit
	case "esc":

		if m.focus == focusCreate {
			m.canceled = true
			return m, tea.Quit
		}
		m.canceled = true
		return m, tea.Quit
	case "tab":
		m.focus = (m.focus + 1) % focusCount
		return m, m.refocus()
	case "shift+tab":
		m.focus = (m.focus - 1 + focusCount) % focusCount
		return m, m.refocus()
	}

	switch m.focus {
	case focusOwner:
		return m.handleOwnerKey(msg)
	case focusVisibility:
		return m.handleToggleKey(msg, &m.visIdx, len(m.visibilities))
	case focusCategory:
		return m.handleToggleKey(msg, &m.catIdx, len(m.categories))
	case focusName:
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	case focusDescription:
		var cmd tea.Cmd
		m.descInput, cmd = m.descInput.Update(msg)
		return m, cmd
	case focusGroup:
		var cmd tea.Cmd
		m.groupInput, cmd = m.groupInput.Update(msg)
		return m, cmd
	case focusCreate:
		if msg.String() == "enter" {
			if err := m.validateForm(); err != nil {
				m.err = err
				m.st = stateErrored
				return m, nil
			}
			m.st = stateCreating
			return m, tea.Batch(m.spinner.Tick, m.createCmd())
		}
	}
	return m, nil
}

func (m CreateModel) handleOwnerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.owners) == 0 {
		return m, nil
	}
	switch msg.String() {
	case "up", "k":
		if m.ownerCursor > 0 {
			m.ownerCursor--
		}
	case "down", "j":
		if m.ownerCursor < len(m.owners)-1 {
			m.ownerCursor++
		}
	case "home", "g":
		m.ownerCursor = 0
	case "end", "G":
		m.ownerCursor = len(m.owners) - 1
	}
	return m, nil
}

func (m CreateModel) handleToggleKey(msg tea.KeyMsg, idx *int, max int) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "left", "h":
		if *idx > 0 {
			*idx--
		}
	case "right", "l", " ":
		if *idx < max-1 {
			*idx++
		}
	case "1":
		*idx = 0
	case "2":
		if max >= 2 {
			*idx = 1
		}
	}
	return m, nil
}

func (m *CreateModel) refocus() tea.Cmd {
	m.nameInput.Blur()
	m.descInput.Blur()
	m.groupInput.Blur()
	switch m.focus {
	case focusName:
		return m.nameInput.Focus()
	case focusDescription:
		return m.descInput.Focus()
	case focusGroup:
		return m.groupInput.Focus()
	}
	return nil
}

func (m CreateModel) validateForm() error {
	if len(m.owners) == 0 {
		return errors.New("no owners available; check `gh auth status`")
	}
	if m.ownerCursor < 0 || m.ownerCursor >= len(m.owners) {
		return errors.New("invalid owner selection")
	}
	name := strings.TrimSpace(m.nameInput.Value())
	if err := validateName(name); err != nil {
		return err
	}
	return nil
}

func (m CreateModel) currentOwner() string {
	if m.ownerCursor < 0 || m.ownerCursor >= len(m.owners) {
		return ""
	}
	return m.owners[m.ownerCursor].Login
}
