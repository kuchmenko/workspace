package add

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kuchmenko/workspace/internal/branchprompt"
	"github.com/kuchmenko/workspace/internal/clone"
	"github.com/kuchmenko/workspace/internal/config"
)

// AddModel is the bubbletea model for the `ws add` interactive flow.
//
// Lifecycle (per Track B issue #20 Subsystem 5):
//
//   gathering → browse | browseEmpty
//   browse / browseEmpty → manual (i) | edit (⏎) | quit (esc)
//   manual → edit (valid URL) | browse (esc)
//   edit → confirm (⏎) | browse (esc)
//   confirm → cloning (y) | browse (esc)
//   cloning → branchPrompt (clone.ErrNeedsBootstrap) | done
//   branchPrompt → cloning
//   done → quit
//
// Embedding (Phase 5 ws agent): AddModel never calls tea.Quit. When it
// reaches done, it emits AddDoneMsg and waits for a key. Standalone
// callers (Phase 3 ws add) wrap AddModel in a thin shell that converts
// AddDoneMsg into tea.Quit; embedded callers (Phase 5 ws agent) keep
// running their own Update loop.
type AddModel struct {
	state addState
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

	// Async glue to the running tea.Program. Set via SetProgram
	// before tea.Run; used by the worker goroutines that need to
	// post async messages back into the loop (gather done, clone
	// done, branch needed).
	program *tea.Program

	// State for each step. Most fields belong to one state; see the
	// comment headers below for which.

	// gathering.
	spinner spinner.Model
	gathered *GatherResult

	// browse.
	cursor int        // index into filteredView()
	allSuggestions []Suggestion
	filterMode bool
	filterInput textinput.Model

	// manual.
	manualInput textinput.Model
	manualErr   string

	// edit (also reused by confirm).
	editFields  editFields
	editFocus   int // 0=Name 1=Category 2=Group
	editErr     string

	// cloning.
	queue       []editFields // resolved selections waiting to clone
	currentIdx  int          // index into queue
	currentName string       // for spinner header
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
	addStateCloning
	addStateBranchPrompt
	addStateDone
)

type editFields struct {
	Name      string
	URL       string
	Category  config.Category
	Group     string
	Path      string // computed from Category/Group/Name
	FromDisk  string // non-empty → migrate path, not clone
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
	filter.Placeholder = "filter..."
	filter.CharLimit = 60
	filter.Width = 30

	return AddModel{
		state:        addStateGathering,
		wsRoot:       opts.WsRoot,
		ws:           opts.Workspace,
		saveFn:       opts.Save,
		sources:      opts.Sources,
		gatherTO:     opts.GatherTimeout,
		standalone:   opts.Standalone,
		preURLs:      opts.PreURLs,
		spinner:      sp,
		manualInput:  manual,
		filterInput:  filter,
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

// SetProgram wires the running tea.Program into the model so worker
// goroutines (gather, clone) can call program.Send to post async msgs.
// Must be called once after tea.NewProgram and before tea.Run.
func (m *AddModel) SetProgram(p *tea.Program) { m.program = p }

// AddDoneMsg signals that the model has finished its work. Standalone
// callers consume this and quit; embedded callers consume it to
// transition back to their parent state.
type AddDoneMsg struct {
	Added   []config.Project
	Skipped []SkipReason
	Errors  []error
}

// gatherDoneMsg is posted by the gather goroutine to AddModel.Update.
type gatherDoneMsg struct {
	result *GatherResult
}

// cloneDoneMsg is posted after each Register call in the cloning queue.
type cloneDoneMsg struct {
	idx     int
	project config.Project
	skipped *SkipReason
	err     error
}

// allClonesDoneMsg signals the cloning loop reached the end of the queue.
type allClonesDoneMsg struct{}

// needsBranchMsg is the bridge from a clone goroutine that hit
// clone.ErrNeedsBootstrap. The TUI switches into branchPrompt state,
// the user picks, and the answer flows back via the channel.
type needsBranchMsg struct {
	project    string
	candidates []string
	answer     chan branchAnswer
}

// =============================================================================
// tea.Model interface
// =============================================================================

func (m AddModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.startGather())
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
	case addStateCloning:
		return m.viewCloning()
	case addStateBranchPrompt:
		return m.branchPrompt.View()
	case addStateDone:
		return m.viewDone()
	}
	return ""
}

// =============================================================================
// Gathering
// =============================================================================

func (m AddModel) startGather() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		// Apply the timeout outside Gather so we can short-circuit
		// the whole pipeline if all sources are slow. Gather's
		// per-source timeout still applies inside.
		if m.gatherTO > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, m.gatherTO+time.Second)
			defer cancel()
		}
		res, _ := Gather(ctx, m.sources, GatherOptions{SourceTimeout: m.gatherTO})
		return gatherDoneMsg{result: res}
	}
}

func (m AddModel) updateGathering(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case gatherDoneMsg:
		m.gathered = msg.result
		if msg.result != nil {
			m.allSuggestions = msg.result.Suggestions
		}
		if len(m.allSuggestions) == 0 {
			m.transitionTo(addStateBrowseEmpty)
		} else {
			m.transitionTo(addStateBrowse)
		}
		return m, nil
	}
	return m, nil
}

func (m AddModel) viewGathering() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Add project — gathering "))
	b.WriteString("\n\n")
	b.WriteString("  " + m.spinner.View() + " probing sources...\n\n")
	b.WriteString(addHelp.Render("[ctrl+c] cancel"))
	return b.String()
}

// =============================================================================
// Browse
// =============================================================================

func (m AddModel) updateBrowse(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if m.filterMode {
		switch key.String() {
		case "esc":
			m.filterMode = false
			m.filterInput.SetValue("")
			m.filterInput.Blur()
			return m, nil
		case "enter":
			m.filterMode = false
			m.filterInput.Blur()
			m.cursor = 0
			return m, nil
		}
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.cursor = 0
		return m, cmd
	}

	view := m.filteredView()

	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(view)-1 {
			m.cursor++
		}
	case "i":
		m.transitionTo(addStateManual)
		m.manualInput.SetValue("")
		m.manualErr = ""
		return m, m.manualInput.Focus()
	case "/":
		m.filterMode = true
		return m, m.filterInput.Focus()
	case "enter":
		if len(view) == 0 {
			return m, nil
		}
		s := view[m.cursor]
		m.editFields = m.editFromSuggestion(s)
		m.editFocus = 0
		m.editErr = ""
		m.transitionTo(addStateEdit)
		return m, nil
	case "esc":
		// Quit with whatever we have so far (zero in browse).
		done := m.toDone()
		if m.standalone {
			return done, tea.Sequence(emit(m.doneMsg()), tea.Quit)
		}
		return done, emit(m.doneMsg())
	}
	return m, nil
}

func (m AddModel) viewBrowse() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Add project "))
	b.WriteString("\n\n")

	view := m.filteredView()
	if len(view) == 0 {
		b.WriteString(addDim.Render("  No suggestions found.\n\n"))
		b.WriteString("  " + addHelp.Render("[i] enter URL manually   [esc] quit"))
		return b.String()
	}

	// Per-source diagnostics from the gather pass. Errors are shown
	// inline so the user can tell "github source unavailable" from
	// "github source returned zero results".
	if m.gathered != nil {
		var chips []string
		for _, o := range m.gathered.PerSource {
			color := "2"
			label := fmt.Sprintf("%s:%d", o.Name, o.Count)
			if o.Err != nil {
				color = "3"
				label = fmt.Sprintf("%s:err (%s)", o.Name, sourceErrHint(o.Err))
			}
			chips = append(chips, lipgloss.NewStyle().
				Foreground(lipgloss.Color(color)).Render(label))
		}
		b.WriteString("  ")
		b.WriteString(strings.Join(chips, "  "))
		b.WriteString("\n\n")
	}

	if m.filterInput.Value() != "" {
		fmt.Fprintf(&b, "  filter: %s\n\n", addAccent.Render(m.filterInput.Value()))
	}

	limit := 12
	start := 0
	if m.cursor >= limit {
		start = m.cursor - limit + 1
	}
	end := start + limit
	if end > len(view) {
		end = len(view)
	}
	for i := start; i < end; i++ {
		s := view[i]
		cursor := "  "
		if i == m.cursor {
			cursor = addCursor.Render("▸ ")
		}
		fmt.Fprintf(&b, "  %s%s  %s  %s\n",
			cursor,
			addPad(s.Name, 24),
			renderSourceChips(s.Sources),
			addDim.Render(shortURL(s)))
	}
	if len(view) > limit {
		fmt.Fprintf(&b, "\n  %s\n", addDim.Render(fmt.Sprintf("…and %d more", len(view)-end+(end-start)-limit)))
	}

	b.WriteString("\n")
	if m.filterMode {
		b.WriteString("  filter: " + m.filterInput.View() + "\n")
		b.WriteString("  " + addHelp.Render("[enter] commit   [esc] cancel"))
	} else {
		b.WriteString("  " + addHelp.Render("[↑↓] navigate  [⏎] select  [/] filter  [i] manual URL  [esc] quit"))
	}
	return b.String()
}

func (m AddModel) filteredView() []Suggestion {
	q := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))
	if q == "" {
		return m.allSuggestions
	}
	var out []Suggestion
	for _, s := range m.allSuggestions {
		hay := strings.ToLower(s.Name + " " + s.RemoteURL + " " + s.InferredGrp)
		if strings.Contains(hay, q) {
			out = append(out, s)
		}
	}
	return out
}

func (m AddModel) editFromSuggestion(s Suggestion) editFields {
	cat := config.CategoryPersonal
	// Crude heuristic: if the inferred group looks like it could be a
	// work org (anything other than the user's GitHub login or
	// "personal"), default to Work. The user can flip on the edit
	// screen. Phase 4 plans a richer signal.
	grp := s.InferredGrp
	if grp != "" && grp != "personal" {
		cat = config.CategoryWork
	}
	return editFields{
		Name:     s.Name,
		URL:      s.RemoteURL,
		Category: cat,
		Group:    grp,
		Path:     buildPath(grp, cat, s.Name),
		FromDisk: s.DiskPath,
	}
}

// =============================================================================
// Manual URL input
// =============================================================================

func (m AddModel) updateManual(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			val := strings.TrimSpace(m.manualInput.Value())
			if val == "" {
				m.manualErr = "URL is required"
				return m, nil
			}
			// Build editFields from the bare URL.
			name := parseRepoNameFromURL(val)
			m.editFields = editFields{
				Name:     name,
				URL:      val,
				Category: config.CategoryPersonal,
				Group:    "",
				Path:     buildPath("", config.CategoryPersonal, name),
			}
			m.editFocus = 0
			m.editErr = ""
			m.transitionTo(addStateEdit)
			return m, nil
		case "esc":
			m.transitionTo(addStateBrowse)
			m.manualInput.Blur()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.manualInput, cmd = m.manualInput.Update(msg)
	return m, cmd
}

func (m AddModel) viewManual() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Manual URL "))
	b.WriteString("\n\n")
	b.WriteString("  " + m.manualInput.View() + "\n")
	if m.manualErr != "" {
		b.WriteString("\n  " + addErr.Render(m.manualErr) + "\n")
	}
	b.WriteString("\n  " + addHelp.Render("[⏎] continue   [esc] back"))
	return b.String()
}

// =============================================================================
// Edit
// =============================================================================

func (m AddModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "tab", "down":
		m.editFocus = (m.editFocus + 1) % 4 // 0=Name 1=URL 2=Category 3=Group
	case "shift+tab", "up":
		m.editFocus = (m.editFocus + 3) % 4
	case "enter":
		// Validate & advance to confirm.
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
		// Plain typing edits the focused field.
		s := key.String()
		// Filter to printable rune-ish keys.
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
		// Category: cycle on space, otherwise ignore alphabetic input
		// — only personal|work allowed.
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

// =============================================================================
// Confirm
// =============================================================================

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

// =============================================================================
// Cloning
// =============================================================================

func (m AddModel) startCloneJob(idx int) tea.Cmd {
	if idx >= len(m.queue) {
		return func() tea.Msg { return allClonesDoneMsg{} }
	}
	job := m.queue[idx]
	prog := m.program
	return func() tea.Msg {
		// Build a per-iteration Options for Register. The TUI
		// suppresses Register's --no-clone semantics — we always clone
		// here. Disk-found suggestions could trigger migrate instead;
		// Phase 1-C declared the per-source semantic and Phase 4 will
		// fully implement the migrate path. For Phase 3 we skip the
		// disk-already-cloned case via the FromDisk early-return below.
		opts := Options{
			URLs:      []string{job.URL},
			Name:      job.Name,
			Category:  job.Category,
			Group:     job.Group,
			WsRoot:    m.wsRoot,
			Workspace: m.ws,
			Save:      m.saveFn,
			Mode:      ModeHeadless,
			NoClone:   job.FromDisk != "", // disk-found → register only
		}

		ch := make(chan branchAnswer, 1)
		_ = ch // currently unused; reserved for Phase 4 wiring through
		// Register handles the workspace.toml mutation atomically.
		// branchprompt is reserved for future TUI wiring; Phase 3
		// keeps the Register call non-interactive — if the clone
		// returns ErrNeedsBootstrap, we surface it as a per-job
		// error rather than block on a sub-step. That keeps the
		// cloning loop deterministic; Phase 4 can layer the prompt
		// in via the same channel pattern bootstrap uses.
		_ = prog
		regOpts := opts
		regRes, err := Register(regOpts, job.URL)
		out := cloneDoneMsg{idx: idx}
		if err != nil {
			if errors.Is(err, ErrAlreadyRegistered) {
				out.skipped = &SkipReason{URL: job.URL, Reason: err.Error()}
			} else if errors.Is(err, clone.ErrNeedsBootstrap) {
				out.err = fmt.Errorf("%s: default branch ambiguous (run `ws bootstrap %s` after add)", job.Name, job.Name)
			} else {
				out.err = err
			}
		} else if regRes != nil {
			out.project = regRes.Project
		}
		return out
	}
}

func (m AddModel) updateCloning(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case cloneDoneMsg:
		switch {
		case msg.err != nil:
			m.errors = append(m.errors, msg.err)
		case msg.skipped != nil:
			m.skipped = append(m.skipped, *msg.skipped)
		default:
			m.added = append(m.added, msg.project)
		}
		m.currentIdx = msg.idx + 1
		if m.currentIdx >= len(m.queue) {
			m.transitionTo(addStateDone)
			if m.standalone {
				return m, tea.Sequence(emit(m.doneMsg()), tea.Quit)
			}
			return m, emit(m.doneMsg())
		}
		return m, m.startCloneJob(m.currentIdx)
	case needsBranchMsg:
		// Reserved for Phase 4; in Phase 3 we never produce this.
		m.branchPrompt = branchprompt.NewModel(msg.project, msg.candidates)
		m.branchAnswer = msg.answer
		m.transitionTo(addStateBranchPrompt)
		return m, nil
	case allClonesDoneMsg:
		m.transitionTo(addStateDone)
		if m.standalone {
			return m, tea.Sequence(emit(m.doneMsg()), tea.Quit)
		}
		return m, emit(m.doneMsg())
	}
	return m, nil
}

func (m AddModel) viewCloning() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Cloning "))
	b.WriteString("\n\n")
	total := len(m.queue)
	done := m.currentIdx
	fmt.Fprintf(&b, "  %d / %d\n\n", done, total)
	if m.currentIdx < total {
		j := m.queue[m.currentIdx]
		fmt.Fprintf(&b, "  %s %s\n", m.spinner.View(), j.Name)
		fmt.Fprintf(&b, "    %s\n", addDim.Render(j.Path))
	}
	if len(m.errors) > 0 {
		fmt.Fprintf(&b, "\n  %s %d failed\n", addErr.Render("✗"), len(m.errors))
	}
	b.WriteString("\n  " + addHelp.Render("[ctrl+c] abort"))
	return b.String()
}

// =============================================================================
// Branch prompt (reserved — wiring complete for Phase 4)
// =============================================================================

func (m AddModel) updateBranchPrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case branchprompt.PickedMsg:
		m.resolveBranch(msg.Branch, nil)
		m.transitionTo(addStateCloning)
		return m, nil
	case branchprompt.CancelledMsg:
		m.resolveBranch("", errors.New("user cancelled branch selection"))
		m.transitionTo(addStateCloning)
		return m, nil
	}
	var cmd tea.Cmd
	m.branchPrompt, cmd = m.branchPrompt.Update(msg)
	return m, cmd
}

func (m *AddModel) resolveBranch(branch string, err error) {
	if m.branchAnswer != nil {
		m.branchAnswer <- branchAnswer{branch: branch, err: err}
		m.branchAnswer = nil
	}
}

// =============================================================================
// Done
// =============================================================================

func (m AddModel) updateDone(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		if m.standalone {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m AddModel) viewDone() string {
	var b strings.Builder
	b.WriteString(addTitle.Render(" Done "))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  %s %d added\n", addCheck.Render("✓"), len(m.added))
	if len(m.skipped) > 0 {
		fmt.Fprintf(&b, "  %s %d skipped\n", addDim.Render("⊘"), len(m.skipped))
	}
	if len(m.errors) > 0 {
		fmt.Fprintf(&b, "  %s %d errored\n", addErr.Render("✗"), len(m.errors))
		b.WriteString("\n")
		for _, e := range m.errors {
			fmt.Fprintf(&b, "    %s\n", addDim.Render(e.Error()))
		}
	}
	b.WriteString("\n  " + addHelp.Render("[any key] exit"))
	return b.String()
}

// =============================================================================
// Helpers
// =============================================================================

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

func emit(msg tea.Msg) tea.Cmd {
	return func() tea.Msg { return msg }
}

func parseRepoNameFromURL(url string) string {
	// Lightweight wrapper around git.ParseRepoName to avoid a dep
	// loop into internal/git for code that doesn't otherwise need it.
	url = strings.TrimSpace(url)
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimSuffix(url, "/")
	if i := strings.LastIndexAny(url, "/:"); i >= 0 {
		return url[i+1:]
	}
	return url
}

func addPad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func renderSourceChips(srcs []SourceKind) string {
	if len(srcs) == 0 {
		return ""
	}
	var parts []string
	for _, k := range srcs {
		parts = append(parts, addChip.Render("["+k.String()+"]"))
	}
	return strings.Join(parts, " ")
}

func shortURL(s Suggestion) string {
	if s.RemoteURL != "" {
		return s.RemoteURL
	}
	if s.DiskPath != "" {
		return s.DiskPath
	}
	return ""
}

// sourceErrHint summarizes a per-source error into a one-or-two-word
// chip suffix. Keeps the gather chips readable on narrow terminals
// without burying the user in stack-trace prose.
//
// Errors in the source pipeline are wrapped as `<source>: <inner>` or
// even `<source>: <middle>: <inner>` (clipboard wraps the binary path,
// github wraps "github source", etc). The fallback strips those
// prefixes and shows the deepest cause — that's the actionable bit
// the user wants to read.
func sourceErrHint(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case strings.Contains(msg, "ErrNotAuthed"), strings.Contains(msg, "not authed"):
		return "no auth"
	case strings.Contains(strings.ToLower(msg), "rate limit"),
		strings.Contains(msg, "API rate limit"):
		return "rate-limited"
	case strings.Contains(strings.ToLower(msg), "401"),
		strings.Contains(strings.ToLower(msg), "unauthorized"):
		return "401 expired?"
	case strings.Contains(msg, "Nothing is copied"),
		strings.Contains(msg, "No selection"):
		return "empty"
	}
	// Fallback: drop everything up to and including the LAST `: ` so
	// "/sbin/wl-paste: failed to bind" → "failed to bind". Cap at 24
	// chars, single line.
	tail := msg
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		tail = strings.TrimSpace(msg[i+2:])
	}
	tail = strings.ReplaceAll(tail, "\n", " ")
	if len(tail) > 24 {
		tail = tail[:24]
	}
	return tail
}

// =============================================================================
// Styles
// =============================================================================

var (
	addTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("6")).
			Padding(0, 1)

	addDim = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	addHelp = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	addCursor = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true)

	addAccent = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true)

	addErr = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).
			Bold(true)

	addCheck = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))

	addChip = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
)
