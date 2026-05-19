package create

import (
	"errors"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/tui"
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
	nameInput  tui.TextInput
	descInput  tui.TextInput
	groupInput tui.TextInput

	spinner tui.Spinner

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

	name := tui.NewTextInput()
	name.SetPlaceholder("my-new-repo")
	name.SetCharLimit(100)
	name.SetWidth(40)
	name.SetValue(opts.Name)

	desc := tui.NewTextInput()
	desc.SetPlaceholder("(optional) one-line description")
	desc.SetCharLimit(200)
	desc.SetWidth(60)
	desc.SetValue(opts.Description)

	group := tui.NewTextInput()
	group.SetPlaceholder("(optional) project group/dir")
	group.SetCharLimit(80)
	group.SetWidth(40)
	group.SetValue(opts.Group)

	sp := tui.NewSpinner()
	sp.SetStyle(tui.DotSpinner)
	sp.SetTextStyle(createAccent)

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

func (m CreateModel) Init() tui.Cmd {
	return tui.Batch(m.spinner.Tick, m.fetchOwnersCmd())
}

func (m CreateModel) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tui.SpinnerTickMsg:
		var cmd tui.Cmd
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

	case tui.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m CreateModel) handleKey(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
	switch m.st {
	case stateLoadingOwners:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tui.Quit
		}
		return m, nil

	case stateErrored:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.canceled = true
			return m, tui.Quit
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

		return m, tui.Quit

	case stateCreating:

		if msg.String() == "ctrl+c" {
			m.canceled = true
			return m, tui.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		m.canceled = true
		return m, tui.Quit
	case "esc":

		if m.focus == focusCreate {
			m.canceled = true
			return m, tui.Quit
		}
		m.canceled = true
		return m, tui.Quit
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
		var cmd tui.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	case focusDescription:
		var cmd tui.Cmd
		m.descInput, cmd = m.descInput.Update(msg)
		return m, cmd
	case focusGroup:
		var cmd tui.Cmd
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
			return m, tui.Batch(m.spinner.Tick, m.createCmd())
		}
	}
	return m, nil
}

func (m CreateModel) handleOwnerKey(msg tui.KeyMsg) (tui.Model, tui.Cmd) {
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

func (m CreateModel) handleToggleKey(msg tui.KeyMsg, idx *int, max int) (tui.Model, tui.Cmd) {
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

func (m *CreateModel) refocus() tui.Cmd {
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
