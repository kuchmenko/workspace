package add

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kuchmenko/workspace/internal/branchprompt"
	"github.com/kuchmenko/workspace/internal/config"
	"github.com/kuchmenko/workspace/internal/tui"
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

	spinner tui.Spinner

	sourceOutcomes []SourceOutcome
	sourcesDone    int

	cursor         int
	allSuggestions []Suggestion
	filterMode     bool
	filterInput    tui.TextInput

	selectedURLs map[string]bool

	manualInput tui.TextInput
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
	sp := tui.NewSpinner()
	sp.SetStyle(tui.DotSpinner)
	sp.SetTextStyle(tui.NewStyle().Foreground("6"))

	manual := tui.NewTextInput()
	manual.SetPlaceholder("git@github.com:owner/repo.git")
	manual.SetCharLimit(200)
	manual.SetWidth(60)

	filter := tui.NewTextInput()
	filter.SetPlaceholder("type to search name / url / description / org...")
	filter.SetCharLimit(60)
	filter.SetWidth(50)

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

func (m AddModel) Init() tui.Cmd {
	cmds := []tui.Cmd{m.spinner.Tick}
	for _, src := range m.sources {
		cmds = append(cmds, m.startSource(src))
	}
	return tui.Batch(cmds...)
}

func (m AddModel) startSource(src Source) tui.Cmd {
	timeout := m.gatherTO
	if timeout <= 0 {
		timeout = DefaultSourceTimeout
	}
	return func() tui.Msg {
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

func (m AddModel) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	switch msg := msg.(type) {
	case tui.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tui.KeyMsg:

		if !m.stateChangedAt.IsZero() && time.Since(m.stateChangedAt) < 100*time.Millisecond {
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			done := m.toDone()
			if m.standalone {
				return done, tui.Sequence(emit(AddDoneMsg{}), tui.Quit)
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

func (m AddModel) updateBrowse(msg tui.Msg) (tui.Model, tui.Cmd) {
	key, ok := msg.(tui.KeyMsg)
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
		var cmd tui.Cmd
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

		if len(m.selectedURLs) > 0 {
			m.transitionTo(addStateBulkConfirm)
			return m, nil
		}

		s := view[m.cursor]
		m.editFields = m.editFromSuggestion(s)
		m.editFocus = 0
		m.editErr = ""
		m.transitionTo(addStateEdit)
		return m, nil
	case " ":

		if len(view) == 0 {
			return m, nil
		}
		s := view[m.cursor]
		if s.RemoteURL == "" {
			return m, nil
		}
		if m.selectedURLs == nil {
			m.selectedURLs = make(map[string]bool)
		}
		if m.selectedURLs[s.RemoteURL] {
			delete(m.selectedURLs, s.RemoteURL)
		} else {
			m.selectedURLs[s.RemoteURL] = true
		}
		return m, nil
	case "a":

		if len(view) == 0 {
			return m, nil
		}
		if m.selectedURLs == nil {
			m.selectedURLs = make(map[string]bool)
		}
		allMarked := true
		for _, s := range view {
			if !m.selectedURLs[s.RemoteURL] {
				allMarked = false
				break
			}
		}
		if allMarked {
			for _, s := range view {
				delete(m.selectedURLs, s.RemoteURL)
			}
		} else {
			for _, s := range view {
				if s.RemoteURL != "" {
					m.selectedURLs[s.RemoteURL] = true
				}
			}
		}
		return m, nil
	case "esc":

		if len(m.selectedURLs) > 0 {
			m.selectedURLs = nil
			return m, nil
		}
		done := m.toDone()
		if m.standalone {
			return done, tui.Sequence(emit(m.doneMsg()), tui.Quit)
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

	if len(m.sources) > 0 {
		b.WriteString("  ")
		b.WriteString(renderSourceChipsLive(m.sourceOutcomes))
		if m.sourcesDone < len(m.sources) {
			fmt.Fprintf(&b, "  %s",
				addDim.Render(fmt.Sprintf("%s loading %d more...",
					m.spinner.View(), len(m.sources)-m.sourcesDone)))
		}
		b.WriteString("\n\n")
	}

	if m.filterInput.Value() != "" {
		fmt.Fprintf(&b, "  search: %s\n\n", addAccent.Render(m.filterInput.Value()))
	}

	rows := buildBrowseRows(view)
	cursorRow := -1
	itemSeen := 0
	for i, r := range rows {
		if r.kind == rowItem {
			if itemSeen == m.cursor {
				cursorRow = i
			}
			itemSeen++
		}
	}

	const visibleRows = 16
	start, end := windowAround(cursorRow, len(rows), visibleRows)
	for i := start; i < end; i++ {
		r := rows[i]
		switch r.kind {
		case rowGroup:
			fmt.Fprintf(&b, "  %s\n", r.text)
		case rowItem:
			s := r.suggestion
			selected := i == cursorRow
			marked := m.selectedURLs[s.RemoteURL]
			cursor := "    "
			if selected && marked {
				cursor = " " + addCursor.Render("▸") + addAccent.Render("●")
			} else if selected {
				cursor = "  " + addCursor.Render("▸ ")
			} else if marked {
				cursor = "  " + addAccent.Render("● ")
			}
			line := strings.TrimRight(renderItemLine(cursor, s), "\n")
			if selected {
				rs := addCursorRow
				if m.width > 0 {
					rs = rs.Width(m.width)
				}
				line = rs.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}
	if start > 0 || end < len(rows) {
		fmt.Fprintf(&b, "\n  %s\n",
			addDim.Render(fmt.Sprintf("(scrolled %d/%d items)", m.cursor+1, len(view))))
	}

	if cursorRow >= 0 && cursorRow < len(rows) && rows[cursorRow].kind == rowItem {
		b.WriteString("\n")
		b.WriteString(renderSelectionPreview(rows[cursorRow].suggestion))
	}

	b.WriteString("\n")
	if m.filterMode {
		b.WriteString("  search: " + m.filterInput.View() + "\n")
		b.WriteString("  " + addHelp.Render("[enter] commit   [esc] cancel"))
	} else if n := len(m.selectedURLs); n > 0 {
		fmt.Fprintf(&b, "  %s  %s\n",
			addAccent.Render(fmt.Sprintf("● %d marked", n)),
			addHelp.Render("[⏎] confirm bulk add  [space] toggle  [a] all  [esc] clear"))
		b.WriteString("  " + addHelp.Render("[↑↓] navigate  [/] search  [i] manual URL"))
	} else {
		b.WriteString("  " + addHelp.Render("[↑↓] navigate  [⏎] select  [space] mark  [a] all  [/] search  [i] manual URL  [esc] quit"))
	}
	return b.String()
}

func renderSelectionPreview(s *Suggestion) string {
	var b strings.Builder

	b.WriteString("  " + addPreviewName.Render(s.Name))
	if u := shortURL(*s); u != "" {
		b.WriteString("  " + addDim.Render(u))
	}
	b.WriteString("\n")

	desc := strings.TrimSpace(s.Description)
	if desc == "" {
		desc = "(no description)"
		b.WriteString("  " + addDim.Render(truncate(desc, 100)) + "\n")
	} else {
		desc = strings.ReplaceAll(desc, "\n", " ")
		b.WriteString("  " + truncate(desc, 100) + "\n")
	}

	var meta []string
	if !s.PushedAt.IsZero() && s.PushedAt.Year() > 1 {
		meta = append(meta, "pushed "+relativeTime(s.PushedAt))
	}
	if s.GhActivity > 0 {
		meta = append(meta, fmt.Sprintf("%d events", s.GhActivity))
	}
	if s.RegisteredPath != "" {
		meta = append(meta, "● already at "+s.RegisteredPath)
	} else if s.DiskPath != "" {
		meta = append(meta, "● local at "+s.DiskPath)
	}
	if len(meta) > 0 {
		b.WriteString("  " + addDim.Render(strings.Join(meta, " · ")) + "\n")
	}
	return b.String()
}

type browseRowKind int

const (
	rowGroup browseRowKind = iota
	rowItem
)

type browseRow struct {
	kind       browseRowKind
	text       string
	suggestion *Suggestion
}

func buildBrowseRows(view []Suggestion) []browseRow {
	if len(view) == 0 {
		return nil
	}

	groupCounts := map[string]int{}
	for i := range view {
		k, _, _ := groupKey(view[i])
		groupCounts[k]++
	}

	var rows []browseRow
	var lastKey string
	for i := range view {
		s := &view[i]
		key, label, _ := groupKey(*s)
		if key != lastKey {
			header := fmt.Sprintf("%s %s",
				addGroupHdr.Render(label),
				addDim.Render(fmt.Sprintf("(%d)", groupCounts[key])))
			rows = append(rows, browseRow{kind: rowGroup, text: header})
			lastKey = key
		}
		rows = append(rows, browseRow{kind: rowItem, suggestion: s})
	}
	return rows
}

func groupKey(s Suggestion) (key, label string, order int) {
	hasGh := hasSource(s.Sources, SourceGitHub)
	hasClip := hasSource(s.Sources, SourceClipboard)
	hasDisk := hasSource(s.Sources, SourceDisk)
	hasManual := hasSource(s.Sources, SourceManual)

	switch {
	case hasClip && !hasGh:
		return "_clip", "Clipboard", 0
	case hasManual && !hasGh:
		return "_manual", "Manual", 0
	case hasDisk && !hasGh:
		return "_disk", "Local (unregistered)", 1
	case hasGh && s.InferredGrp != "":
		return "gh:" + strings.ToLower(s.InferredGrp), s.InferredGrp, 2
	default:
		return "_other", "Other", 3
	}
}

func windowAround(cursor, total, size int) (start, end int) {
	if total <= size {
		return 0, total
	}
	if cursor < 0 {
		return 0, size
	}
	half := size / 2
	start = cursor - half
	if start < 0 {
		start = 0
	}
	end = start + size
	if end > total {
		end = total
		start = end - size
	}
	return start, end
}

func renderItemLine(cursor string, s *Suggestion) string {
	nameStyle := addItemName
	suffix := ""
	urlStyle := addDim

	switch {
	case s.RegisteredPath != "":

		nameStyle = addExists
		suffix = " " + addExistsTag.Render(
			fmt.Sprintf("● cloned at %s", s.RegisteredPath))
	case s.DiskPath != "":

		nameStyle = addExists
		suffix = " " + addExistsTag.Render(
			fmt.Sprintf("● local: %s", s.DiskPath))
	}

	url := shortURL(*s)
	return fmt.Sprintf("%s%s  %s  %s%s\n",
		cursor,
		nameStyle.Render(addPad(s.Name, 24)),
		renderSourceChips(s.Sources),
		urlStyle.Render(url),
		suffix)
}

func (m AddModel) filteredView() []Suggestion {
	q := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))
	if q == "" {
		return m.allSuggestions
	}
	var out []Suggestion
	for _, s := range m.allSuggestions {
		hay := strings.ToLower(s.Name + " " + s.RemoteURL + " " + s.InferredGrp + " " + s.Description)
		if strings.Contains(hay, q) {
			out = append(out, s)
		}
	}
	return out
}

func (m AddModel) editFromSuggestion(s Suggestion) editFields {
	cat := config.CategoryPersonal

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

func (m AddModel) updateEdit(msg tui.Msg) (tui.Model, tui.Cmd) {
	key, ok := msg.(tui.KeyMsg)
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

		if key.Type == tui.KeyRunes {
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

func (m AddModel) updateConfirm(msg tui.Msg) (tui.Model, tui.Cmd) {
	if key, ok := msg.(tui.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			m.queue = append(m.queue, m.editFields)
			m.currentIdx = 0
			m.transitionTo(addStateCloning)
			return m, tui.Batch(m.spinner.Tick, m.startCloneJob(0))
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

func (m AddModel) updateBulkConfirm(msg tui.Msg) (tui.Model, tui.Cmd) {
	key, ok := msg.(tui.KeyMsg)
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
		return m, tui.Batch(m.spinner.Tick, m.startCloneJob(0))
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

var (
	addTitle = tui.NewStyle().
			Bold(true).
			Foreground(tui.Color("15")).
			Background(tui.Color("6")).
			Padding(0, 1)

	addDim = tui.NewStyle().Foreground(tui.Color("8"))

	addHelp = tui.NewStyle().Foreground(tui.Color("8"))

	addCursor = tui.NewStyle().
			Foreground(tui.Color("6")).
			Bold(true)

	addAccent = tui.NewStyle().
			Foreground(tui.Color("6")).
			Bold(true)

	addErr = tui.NewStyle().
		Foreground(tui.Color("1")).
		Bold(true)

	addCheck = tui.NewStyle().Foreground(tui.Color("2"))

	addChip = tui.NewStyle().Foreground(tui.Color("4"))

	addGroupHdr = tui.NewStyle().
			Foreground(tui.Color("5")).
			Bold(true).
			Underline(true)

	addItemName = tui.NewStyle().Foreground(tui.Color("15"))

	addExists = tui.NewStyle().
			Foreground(tui.Color("3")).
			Bold(true)

	addExistsTag = tui.NewStyle().
			Foreground(tui.Color("3")).
			Italic(true)

	addPreviewName = tui.NewStyle().
			Foreground(tui.Color("14")).
			Bold(true)

	addCursorRow = tui.NewStyle().
			Background(tui.Color("237"))
)
