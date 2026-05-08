package create

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kuchmenko/workspace/internal/add"
	"github.com/kuchmenko/workspace/internal/config"
)

// state tracks where the TUI is in its lifecycle. The transitions are
// linear modulo retry: loadingOwners → form ⇄ errored → creating →
// done. Esc from any state returns to errored=canceled or quits.
type state int

const (
	stateLoadingOwners state = iota
	stateForm
	stateCreating
	stateDone
	stateErrored
)

// focus identifies the active form field. Tab cycles forward,
// Shift-Tab backward. The integer order also drives keyboard handling
// inside Update — keep it stable.
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

// CreateModelOptions is the constructor input for tests + production.
// Tests inject GHRunner (fake) and Save (capture); production wiring
// passes a realGHRunner and config.Save closure.
type CreateModelOptions struct {
	WsRoot    string
	Workspace *config.Workspace
	Save      func(*config.Workspace) error
	GHRunner  ghRunner

	// Defaults sourced from cobra flags. Empty values are unbound
	// fields the user fills in via the form.
	Owner       string
	Name        string
	Visibility  Visibility
	Description string
	Category    config.Category
	Group       string
	ProjectName string
	URLFor      func(owner, name string) string
}

// CreateModel is the bubbletea Model for ws create. Single-screen
// form: top — title; middle — owner list + form fields; bottom —
// help/error/spinner. Update is a state-machine over `state`.
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

	// Outputs collected by Run after Program exits.
	result   *Result
	canceled bool
}

// NewCreateModel constructs the model with sane defaults wired from
// CreateModelOptions. Field defaults: visibility=private, category=
// personal, group=owner login (filled when owners load if Group is
// empty and category becomes work).
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
		focus:        focusName, // owner selection handled separately when list arrives
	}
	return m
}

// Init kicks off the async owner fetch + spinner tick.
func (m CreateModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.fetchOwnersCmd())
}

// fetchOwnersCmd queries gh for the current user + orgs in a goroutine.
// Returns ownersLoadedMsg on success, ownersErrMsg on failure.
func (m CreateModel) fetchOwnersCmd() tea.Cmd {
	runner := m.opts.GHRunner
	if runner == nil {
		runner = realGHRunner{}
	}
	return func() tea.Msg {
		owners, err := ListOwners(runner)
		if err != nil {
			return ownersErrMsg{err: err}
		}
		return ownersLoadedMsg{owners: owners}
	}
}

// createCmd kicks off the gh repo create + register + clone pipeline
// off the bubbletea event loop. Returns createDoneMsg on success,
// createErrMsg on any step's failure.
func (m CreateModel) createCmd() tea.Cmd {
	runner := m.opts.GHRunner
	if runner == nil {
		runner = realGHRunner{}
	}

	owner := m.currentOwner()
	name := strings.TrimSpace(m.nameInput.Value())
	desc := strings.TrimSpace(m.descInput.Value())
	visibility := m.visibilities[m.visIdx]
	category := m.categories[m.catIdx]
	group := strings.TrimSpace(m.groupInput.Value())

	wsRoot := m.opts.WsRoot
	ws := m.opts.Workspace
	saveFn := m.opts.Save
	if saveFn == nil {
		saveFn = func(w *config.Workspace) error { return config.Save(wsRoot, w) }
	}
	projectName := m.opts.ProjectName
	if projectName == "" {
		projectName = name
	}

	return func() tea.Msg {
		if _, err := CreateRepo(runner, CreateRepoOptions{
			Owner:       owner,
			Name:        name,
			Visibility:  visibility,
			Description: desc,
			AddReadme:   true,
		}); err != nil {
			return createErrMsg{err: fmt.Errorf("create repo: %w", err)}
		}

		urlFor := m.opts.URLFor
		if urlFor == nil {
			urlFor = SSHURLFromOwnerRepo
		}
		sshURL := urlFor(owner, name)
		regOpts := add.Options{
			Category:  category,
			Group:     group,
			Name:      projectName,
			WsRoot:    wsRoot,
			Workspace: ws,
			Save:      saveFn,
		}
		regRes, err := add.Register(regOpts, sshURL)
		if err != nil {
			return createErrMsg{
				err: fmt.Errorf("repo created on GitHub at %s but register failed: %w", sshURL, err),
			}
		}

		return createDoneMsg{
			result: &Result{
				Project: regRes.Project,
				Name:    regRes.Name,
				URL:     sshURL,
				Cloned:  regRes.Cloned,
			},
		}
	}
}

// =============================================================================
// Messages
// =============================================================================

type ownersLoadedMsg struct{ owners []Owner }
type ownersErrMsg struct{ err error }
type createDoneMsg struct{ result *Result }
type createErrMsg struct{ err error }

// =============================================================================
// Update
// =============================================================================

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
		// Pre-select owner from flag if matched.
		if m.opts.Owner != "" {
			for i, o := range m.owners {
				if o.Login == m.opts.Owner {
					m.ownerCursor = i
					break
				}
			}
		}
		m.st = stateForm
		// Default focus on Name unless owner was provided via flag —
		// in which case the user is still likely to want to type a
		// name first.
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
			// Retry: if owners failed to load, try again; otherwise
			// drop back to form so the user can edit fields.
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
		// Any key dismisses the success screen.
		return m, tea.Quit

	case stateCreating:
		// Disallow input mid-creation; only Ctrl+C escapes.
		if msg.String() == "ctrl+c" {
			m.canceled = true
			return m, tea.Quit
		}
		return m, nil
	}

	// stateForm — main path.
	switch msg.String() {
	case "ctrl+c":
		m.canceled = true
		return m, tea.Quit
	case "esc":
		// Esc on Create button cancels; otherwise blurs current input.
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

	// Field-specific handling.
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

// refocus drives textinput Focus/Blur based on the current m.focus.
// Returns a tea.Cmd because Focus emits a blink cmd.
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

// validateForm runs client-side checks before launching the gh call.
// Cheap to fail here — the alternative is a 1-2s gh round-trip and a
// stderr-driven error.
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

// currentOwner returns the login of the highlighted owner. Empty
// string is valid only when called before owners load.
func (m CreateModel) currentOwner() string {
	if m.ownerCursor < 0 || m.ownerCursor >= len(m.owners) {
		return ""
	}
	return m.owners[m.ownerCursor].Login
}

// =============================================================================
// View
// =============================================================================

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
	// Window: keep the cursor visible. Show up to 6 rows.
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

// =============================================================================
// Styles
// =============================================================================

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

// =============================================================================
// Standalone runner
// =============================================================================

// runTUI launches the model as a tea.Program and returns the captured
// Result when the user confirms. Cancellation (Esc, Ctrl+C) returns
// (nil, ErrCancelled).
func runTUI(ctx context.Context, opts Options) (*Result, error) {
	model := NewCreateModel(CreateModelOptions{
		WsRoot:      opts.WsRoot,
		Workspace:   opts.Workspace,
		Save:        resolveSaveFn(opts),
		GHRunner:    opts.GHRunner,
		Owner:       opts.Owner,
		Name:        opts.Name,
		Visibility:  opts.Visibility,
		Description: opts.Description,
		Category:    opts.Category,
		Group:       opts.Group,
		ProjectName: opts.ProjectName,
		URLFor:      opts.URLFor,
	})

	prog := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithContext(ctx),
	)
	finalModel, err := prog.Run()
	if err != nil {
		return nil, fmt.Errorf("create TUI: %w", err)
	}
	final, ok := finalModel.(CreateModel)
	if !ok {
		return nil, fmt.Errorf("create TUI: unexpected final model type %T", finalModel)
	}
	if final.canceled {
		return nil, ErrCancelled
	}
	if final.err != nil {
		return nil, final.err
	}
	if final.result == nil {
		return nil, errors.New("create TUI exited with no result")
	}
	return final.result, nil
}

// ErrCancelled is returned by Run when the user dismisses the TUI
// without confirming. The cobra layer maps this to a soft exit (no
// error printed, exit 0) since cancellation is a user action, not a
// failure.
var ErrCancelled = errors.New("create canceled by user")
