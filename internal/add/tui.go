package add

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kuchmenko/workspace/internal/branchprompt"
	"github.com/kuchmenko/workspace/internal/config"
)

type AddModel struct {
	state          addState
	stateChangedAt time.Time

	wsRoot   string
	ws       *config.Workspace
	saveFn   func(*config.Workspace) error
	sources  []Source
	gatherTO time.Duration

	standalone bool

	preURLs []string

	width, height int

	spinner spinner.Model

	sourceOutcomes []SourceOutcome
	sourcesDone    int

	cursor         int
	allSuggestions []Suggestion
	filterMode     bool
	filterInput    textinput.Model

	selectedURLs map[string]bool

	manualInput textinput.Model
	manualErr   string

	editFields editFields
	editFocus  int
	editErr    string

	queue        []editFields
	currentIdx   int
	branchAnswer chan branchAnswer

	branchPrompt branchprompt.Model

	added   []config.Project
	skipped []SkipReason
	errors  []error
}

type addState int

const (
	addStateGathering addState = iota
	addStateBrowse
	addStateBrowseEmpty
	addStateManual
	addStateEdit
	addStateConfirm
	addStateBulkConfirm
	addStateCloning
	addStateBranchPrompt
	addStateDone
)

type editFields struct {
	Name     string
	URL      string
	Category config.Category
	Group    string
	Path     string
	FromDisk string
}

type branchAnswer struct {
	branch string
	err    error
}

func NewAddModel(opts AddModelOptions) AddModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

	manual := textinput.New()
	manual.Placeholder = "git@github.com:owner/repo.git"
	manual.CharLimit = 200
	manual.Width = 60

	filter := textinput.New()
	filter.Placeholder = "type to search name / url / description / org..."
	filter.CharLimit = 60
	filter.Width = 50

	return AddModel{
		state:       addStateGathering,
		wsRoot:      opts.WsRoot,
		ws:          opts.Workspace,
		saveFn:      opts.Save,
		sources:     opts.Sources,
		gatherTO:    opts.GatherTimeout,
		standalone:  opts.Standalone,
		preURLs:     opts.PreURLs,
		spinner:     sp,
		manualInput: manual,
		filterInput: filter,
	}
}

type AddModelOptions struct {
	WsRoot        string
	Workspace     *config.Workspace
	Save          func(*config.Workspace) error
	Sources       []Source
	GatherTimeout time.Duration

	Standalone bool

	PreURLs []string
}

func (m AddModel) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	for _, src := range m.sources {
		cmds = append(cmds, m.startSource(src))
	}
	return tea.Batch(cmds...)
}

func (m AddModel) startSource(src Source) tea.Cmd {
	timeout := m.gatherTO
	if timeout <= 0 {
		timeout = DefaultSourceTimeout
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		start := time.Now()
		got, err := src.FetchSuggestions(ctx)
		return sourceDoneMsg{
			name:  src.Name(),
			items: got,
			err:   err,
			took:  time.Since(start),
		}
	}
}

func (m AddModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:

		if !m.stateChangedAt.IsZero() && time.Since(m.stateChangedAt) < 100*time.Millisecond {
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			done := m.toDone()
			if m.standalone {
				return done, tea.Sequence(emit(AddDoneMsg{}), tea.Quit)
			}
			return done, emit(AddDoneMsg{})
		}
	case sourceDoneMsg:

		return m.handleSourceDone(msg)
	}

	switch m.state {
	case addStateGathering:
		return m.updateGathering(msg)
	case addStateBrowse, addStateBrowseEmpty:
		return m.updateBrowse(msg)
	case addStateManual:
		return m.updateManual(msg)
	case addStateEdit:
		return m.updateEdit(msg)
	case addStateConfirm:
		return m.updateConfirm(msg)
	case addStateBulkConfirm:
		return m.updateBulkConfirm(msg)
	case addStateCloning:
		return m.updateCloning(msg)
	case addStateBranchPrompt:
		return m.updateBranchPrompt(msg)
	case addStateDone:
		return m.updateDone(msg)
	}
	return m, nil
}

func (m AddModel) View() string {
	switch m.state {
	case addStateGathering:
		return m.viewGathering()
	case addStateBrowse, addStateBrowseEmpty:
		return m.viewBrowse()
	case addStateManual:
		return m.viewManual()
	case addStateEdit:
		return m.viewEdit()
	case addStateConfirm:
		return m.viewConfirm()
	case addStateBulkConfirm:
		return m.viewBulkConfirm()
	case addStateCloning:
		return m.viewCloning()
	case addStateBranchPrompt:
		return m.branchPrompt.View()
	case addStateDone:
		return m.viewDone()
	}
	return ""
}

func (m *AddModel) transitionTo(s addState) {
	m.state = s
	m.stateChangedAt = time.Now()
}

func (m AddModel) toDone() AddModel {
	m.state = addStateDone
	m.stateChangedAt = time.Now()
	return m
}

func (m AddModel) doneMsg() AddDoneMsg {
	return AddDoneMsg{Added: m.added, Skipped: m.skipped, Errors: m.errors}
}
