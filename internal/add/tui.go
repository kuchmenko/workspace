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

// AddModel is the bubbletea model for the `ws add` interactive flow.
//
// Lifecycle:
//
//	gathering → browse | browseEmpty
//	browse / browseEmpty → manual (i) | edit (⏎) | quit (esc)
//	manual → edit (valid URL) | browse (esc)
//	edit → confirm (⏎) | browse (esc)
//	confirm → cloning (y) | browse (esc)
//	cloning → branchPrompt (clone.ErrNeedsBootstrap) | done
//	branchPrompt → cloning
//	done → quit
//
// Embedding: AddModel never calls tea.Quit. When it reaches done, it
// emits AddDoneMsg and waits for a key. Standalone callers (`ws add`)
// wrap AddModel and convert AddDoneMsg into tea.Quit; embedded
// callers (the future agent integration) keep running their own
// Update loop.
type AddModel struct {
	state          addState
	stateChangedAt time.Time

	// Inputs from the caller.
	wsRoot   string
	ws       *config.Workspace
	saveFn   func(*config.Workspace) error
	sources  []Source
	gatherTO time.Duration

	// Standalone flag: when true, AddModel calls tea.Quit on done.
	// When embedded inside ws agent, the parent owns the quit decision.
	standalone bool

	// Optional pre-supplied URLs from the CLI that bypass the gather +
	// browse phases. Headless callers don't construct AddModel at all,
	// but a TUI run with positional URLs (rare — this design treats
	// "URLs given" as a headless signal) could use this.
	preURLs []string

	// Window sizing.
	width, height int

	// State for each step. Most fields belong to one state; see the
	// comment headers below for which.

	// gathering.
	spinner spinner.Model

	// sourceOutcomes accumulates per-source results as each one
	// completes. Used by viewBrowse to render the "disk:N github:M"
	// chip line and by Update to decide when all sources are done.
	sourceOutcomes []SourceOutcome
	sourcesDone    int

	// browse.
	cursor         int // index into filteredView()
	allSuggestions []Suggestion
	filterMode     bool
	filterInput    textinput.Model

	// selectedURLs holds RemoteURLs of suggestions the user marked
	// for bulk add via space-toggle in browse. Stable across filter
	// changes — toggling a row off-screen still works as long as the
	// URL stays in allSuggestions.
	selectedURLs map[string]bool

	// manual.
	manualInput textinput.Model
	manualErr   string

	// edit (also reused by confirm).
	editFields editFields
	editFocus  int // 0=Name 1=Category 2=Group
	editErr    string

	// cloning.
	queue        []editFields      // resolved selections waiting to clone
	currentIdx   int               // index into queue
	branchAnswer chan branchAnswer // unblocks worker goroutines

	// branchPrompt.
	branchPrompt branchprompt.Model

	// done.
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
	Path     string // computed from Category/Group/Name
	FromDisk string // non-empty → migrate path, not clone
}

type branchAnswer struct {
	branch string
	err    error
}

// NewAddModel constructs an AddModel ready to be run via tea.NewProgram.
//
// The caller supplies the workspace, the save function, and the gather
// sources. NewAddModel does NOT call Gather itself — that happens in
// Init() so the bubbletea runtime can render the gathering view first.
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

// AddModelOptions is the constructor input. Carved out as a struct so
// the constructor signature doesn't grow with each new knob.
type AddModelOptions struct {
	WsRoot        string
	Workspace     *config.Workspace
	Save          func(*config.Workspace) error
	Sources       []Source
	GatherTimeout time.Duration

	// Standalone is true when AddModel runs as the root program (i.e.
	// `ws add` without an embedding parent). Done state then issues
	// tea.Quit. Embedded callers pass false; they handle AddDoneMsg
	// themselves to decide quit vs return-to-list.
	Standalone bool

	// PreURLs are URLs supplied by the caller — currently unused by the
	// TUI proper (CLI passes headless when URLs are given), kept as a
	// hook for callers that want to launch the TUI with a starter list.
	PreURLs []string
}

func (m AddModel) Init() tea.Cmd {
	// Streaming gather: each source runs as its own tea.Cmd so its
	// result lands on the bubbletea event loop the moment the source
	// returns. Disk + clipboard typically finish within a few hundred
	// ms; GitHub can take seconds on cold cache. The TUI transitions
	// from "gathering" to "browse" as soon as the FIRST source has
	// any data — repos from later sources fold in dynamically without
	// the user staring at a spinner.
	cmds := []tea.Cmd{m.spinner.Tick}
	for _, src := range m.sources {
		cmds = append(cmds, m.startSource(src))
	}
	return tea.Batch(cmds...)
}

// startSource produces a tea.Cmd that runs one source's
// FetchSuggestions in a goroutine and emits a sourceDoneMsg with the
// outcome. The per-source ctx deadline is applied here so a single
// slow provider never holds up the others — Gather's own timeout
// logic stays available but is unused by the streaming path.
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
		// Phantom-input debounce mirrors the bootstrap pattern.
		if !m.stateChangedAt.IsZero() && time.Since(m.stateChangedAt) < 100*time.Millisecond {
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			// Cancel everything. In standalone, quit; embedded
			// callers see an empty AddDoneMsg.
			done := m.toDone()
			if m.standalone {
				return done, tea.Sequence(emit(AddDoneMsg{}), tea.Quit)
			}
			return done, emit(AddDoneMsg{})
		}
	case sourceDoneMsg:
		// Source completion can land in any state — fold the new
		// results into allSuggestions even if the user has already
		// moved on to manual / edit / confirm. We just don't change
		// the visible state from those screens.
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
