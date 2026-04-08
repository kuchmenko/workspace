package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kuchmenko/workspace/internal/pulse"
	"github.com/spf13/cobra"
)

// newPulseCmd wires `ws pulse` into the CLI tree. The command launches
// a bubbletea TUI with three tabs: Pulse, PRs, Inbox.
//
// `ws pulse --snapshot` skips the TUI entirely and dumps the current
// pulse data as JSON to stdout. Use this when the TUI shows nothing
// and you need to see what GitHub actually returned.
func newPulseCmd() *cobra.Command {
	var (
		period   string
		snapshot bool
		showAll  bool
	)
	cmd := &cobra.Command{
		Use:   "pulse",
		Short: "Cross-machine activity dashboard from GitHub events",
		Long: `Show what you've been pushing across all your projects, with
machine attribution via the wt/<machine>/<topic> branch convention and
the project.autopush.owned registry. Data comes from the GitHub Events
API — only pushed work is counted.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := parsePeriodFlag(period)
			if err != nil {
				return err
			}
			if snapshot {
				return runPulseSnapshot(p, !showAll)
			}
			model := newPulseModel(p)
			prog := tea.NewProgram(model, tea.WithAltScreen())
			if _, err := prog.Run(); err != nil {
				return fmt.Errorf("pulse TUI crashed: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&period, "period", "p", "7d", "initial period: 1d|7d|30d")
	cmd.Flags().BoolVar(&snapshot, "snapshot", false, "dump current pulse + PRs + inbox as JSON to stdout (no TUI)")
	cmd.Flags().BoolVar(&showAll, "all", false, "with --snapshot, also include PRs from repos not in workspace.toml")
	return cmd
}

// runPulseSnapshot runs all three collectors once and prints a JSON
// blob with everything fetched. Useful for debugging "pulse shows
// nothing": you can see raw event counts, the project index match,
// and the exact data the TUI is rendering from.
func runPulseSnapshot(period pulse.Period, limitPRs bool) error {
	if ws == nil {
		return fmt.Errorf("workspace not loaded")
	}
	out := struct {
		Workspace struct {
			Root         string `json:"root"`
			ProjectCount int    `json:"project_count"`
		} `json:"workspace"`
		Period      string             `json:"period"`
		Pulse       *pulse.Snapshot    `json:"pulse,omitempty"`
		PulseError  string             `json:"pulse_error,omitempty"`
		PRsMine     *pulse.PRSnapshot  `json:"prs_mine,omitempty"`
		PRsMineErr  string             `json:"prs_mine_error,omitempty"`
		Inbox       pulse.InboxSnapshot `json:"inbox"`
	}{}
	out.Workspace.Root = wsRoot
	out.Workspace.ProjectCount = len(ws.Projects)
	out.Period = period.Name

	snap, err := pulse.Collect(ws, period)
	if err != nil {
		out.PulseError = err.Error()
	} else {
		out.Pulse = &snap
	}
	prs, err := pulse.FetchPRs(ws, pulse.PRScopeMine, limitPRs)
	if err != nil {
		out.PRsMineErr = err.Error()
	} else {
		out.PRsMine = &prs
	}
	out.Inbox = pulse.CollectInbox(ws, wsRoot)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func parsePeriodFlag(s string) (pulse.Period, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1d", "24h", "day":
		return pulse.Period1d, nil
	case "7d", "week":
		return pulse.Period7d, nil
	case "30d", "month":
		return pulse.Period30d, nil
	}
	return pulse.Period{}, fmt.Errorf("unknown period %q (use 1d|7d|30d)", s)
}

// =============================================================================
// Model
// =============================================================================

type pulseTab int

const (
	tabPulse pulseTab = iota
	tabPRs
	tabInbox
	tabCount
)

func (t pulseTab) String() string {
	switch t {
	case tabPulse:
		return "Pulse"
	case tabPRs:
		return "PRs"
	case tabInbox:
		return "Inbox"
	}
	return "?"
}

type pulseView int

const (
	viewList pulseView = iota
	viewDrill
)

// prOverride is an optimistic state delta the TUI applies to a PR row
// while an action is in flight. Once the next snapshot lands the
// override is dropped and the row reflects whatever the API actually
// did. Pointer fields make "no override" distinguishable from "set
// to false".
type prOverride struct {
	draft *bool
	state *string
}

func prKey(pr pulse.PR) string {
	return fmt.Sprintf("%s#%d", pr.Repo, pr.Number)
}

type pulseModel struct {
	tab    pulseTab
	view   pulseView
	period pulse.Period

	loading bool
	spin    spinner.Model
	err     error
	snap    pulse.Snapshot

	cursor int // selected project index in viewList (Pulse tab)

	// PRs tab state.
	prScope    pulse.PRScope
	prSnapMine pulse.PRSnapshot
	prSnapRev  pulse.PRSnapshot
	prLoading  bool
	prErr      error
	prCursor   int    // flat index across visible PR rows
	prToast    string // last action result, cleared on next key

	// In-flight action tracking. Key = "repo#number". Set when an
	// action fires for a PR; cleared in bulk when the next PR
	// snapshot lands. The TUI renders ⟳ next to in-flight rows so
	// the user has visual feedback that an action is mid-air.
	// prOptimistic carries optimistic state overrides (draft / state)
	// applied to the local copy of the PR until the refresh
	// reconciles with reality.
	prInFlight   map[string]bool
	prOptimistic map[string]prOverride

	// Inbox tab state.
	inbox        pulse.InboxSnapshot
	inboxLoading bool
	inboxCursor  int

	// Bar chart rise animation. Goes from 0.0 to 1.0 over a few
	// frames after a fresh snapshot lands. The renderer multiplies
	// the bar heights by this value so bars "grow" into place.
	chartAnim float64

	// PR scope filter: when true, only show PRs in repos registered
	// in workspace.toml. Toggle with [a] to surface everything you
	// have access to (helps debug "I expected my PR to be here").
	prLimitToWorkspace bool

	width, height int
}

func newPulseModel(p pulse.Period) pulseModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return pulseModel{
		tab:                tabPulse,
		view:               viewList,
		period:             p,
		loading:            true,
		spin:               sp,
		prLimitToWorkspace: true,
		prInFlight:         map[string]bool{},
		prOptimistic:       map[string]prOverride{},
	}
}

// =============================================================================
// Messages
// =============================================================================

type snapshotMsg struct{ snap pulse.Snapshot }
type errMsg struct{ err error }
type refreshTickMsg time.Time
type prSnapshotMsg struct {
	scope pulse.PRScope
	snap  pulse.PRSnapshot
}
type prErrMsg struct{ err error }
type prActionMsg struct {
	ok  string
	err error
}
type inboxMsg struct{ snap pulse.InboxSnapshot }
type chartAnimTickMsg struct{}

func fetchSnapshot(period pulse.Period) tea.Cmd {
	return func() tea.Msg {
		if ws == nil {
			return errMsg{err: fmt.Errorf("workspace not loaded")}
		}
		snap, err := pulse.Collect(ws, period)
		if err != nil {
			return errMsg{err: err}
		}
		return snapshotMsg{snap: snap}
	}
}

func fetchPRs(scope pulse.PRScope, limitToWorkspace bool) tea.Cmd {
	return func() tea.Msg {
		if ws == nil {
			return prErrMsg{err: fmt.Errorf("workspace not loaded")}
		}
		snap, err := pulse.FetchPRs(ws, scope, limitToWorkspace)
		if err != nil {
			return prErrMsg{err: err}
		}
		return prSnapshotMsg{scope: scope, snap: snap}
	}
}

func fetchInbox() tea.Cmd {
	return func() tea.Msg {
		if ws == nil {
			return inboxMsg{}
		}
		return inboxMsg{snap: pulse.CollectInbox(ws, wsRoot)}
	}
}

// runPRAction wraps a synchronous PR mutation into a tea.Cmd. The
// success message is shown in the toast slot; on success the model
// also re-fetches the current scope so the row reflects the new state.
func runPRAction(label string, fn func() error, scope pulse.PRScope, limitToWorkspace bool) tea.Cmd {
	return tea.Sequence(
		func() tea.Msg {
			if err := fn(); err != nil {
				return prActionMsg{err: err}
			}
			return prActionMsg{ok: label}
		},
		fetchPRs(scope, limitToWorkspace),
	)
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return refreshTickMsg(t) })
}

// chartAnimTick drives the rise-up animation. ~16ms per frame ≈ 60fps;
// the renderer adds 1/animFrames to chartAnim each tick. Animation
// stops once chartAnim reaches 1.0.
const chartAnimFrames = 8

func chartAnimTick() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg { return chartAnimTickMsg{} })
}

// =============================================================================
// Update
// =============================================================================

func (m pulseModel) Init() tea.Cmd {
	return tea.Batch(
		m.spin.Tick,
		fetchSnapshot(m.period),
		fetchPRs(pulse.PRScopeMine, m.prLimitToWorkspace),
		fetchInbox(),
		tickEvery(10*time.Second),
	)
}

func (m pulseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case snapshotMsg:
		m.loading = false
		m.err = nil
		m.snap = msg.snap
		if m.cursor >= len(m.snap.Projects) {
			m.cursor = 0
		}
		// Kick the bar chart rise animation. Restart from 0 each
		// fresh snapshot so users get a visible "the data updated"
		// signal in addition to the new numbers.
		m.chartAnim = 0
		return m, chartAnimTick()

	case chartAnimTickMsg:
		m.chartAnim += 1.0 / float64(chartAnimFrames)
		if m.chartAnim >= 1.0 {
			m.chartAnim = 1.0
			return m, nil
		}
		return m, chartAnimTick()

	case errMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case prSnapshotMsg:
		m.prLoading = false
		m.prErr = nil
		if msg.scope == pulse.PRScopeMine {
			m.prSnapMine = msg.snap
		} else {
			m.prSnapRev = msg.snap
		}
		// Reconcile: anything that was in flight has now been
		// resolved one way or another by the refresh. Drop the
		// optimistic overrides too — the snapshot is the source of
		// truth from here on.
		m.prInFlight = map[string]bool{}
		m.prOptimistic = map[string]prOverride{}
		if m.prCursor >= m.visiblePRCount() {
			m.prCursor = 0
		}
		return m, nil

	case prErrMsg:
		m.prLoading = false
		m.prErr = msg.err
		return m, nil

	case prActionMsg:
		if msg.err != nil {
			m.prToast = "✗ " + msg.err.Error()
			// On failure, drop optimistic overrides immediately so
			// the row reverts to its real state. The refresh that
			// runs after the action will reconfirm. Inflight stays
			// true until the snapshot lands so the spinner is
			// visible during the refresh round-trip.
			m.prOptimistic = map[string]prOverride{}
		} else {
			m.prToast = "✓ " + msg.ok
		}
		return m, nil

	case inboxMsg:
		m.inboxLoading = false
		m.inbox = msg.snap
		if m.inboxCursor >= len(m.inbox.Entries) {
			m.inboxCursor = 0
		}
		return m, nil

	case refreshTickMsg:
		// Auto-refresh every 10s. We only re-fetch the tab the user
		// is currently looking at — keeps GitHub rate spend predictable
		// and avoids stacking concurrent fetches if the API is slow.
		var cmds []tea.Cmd
		if m.tab == tabPulse && !m.loading {
			m.loading = true
			cmds = append(cmds, fetchSnapshot(m.period), m.spin.Tick)
		}
		if m.tab == tabPRs && !m.prLoading {
			m.prLoading = true
			cmds = append(cmds, fetchPRs(m.prScope, m.prLimitToWorkspace), m.spin.Tick)
		}
		if m.tab == tabInbox && !m.inboxLoading {
			m.inboxLoading = true
			cmds = append(cmds, fetchInbox(), m.spin.Tick)
		}
		cmds = append(cmds, tickEvery(10*time.Second))
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m pulseModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Any key clears a stale toast.
	m.prToast = ""

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "tab":
		m.tab = (m.tab + 1) % tabCount
		return m, nil
	case "shift+tab":
		m.tab = (m.tab + tabCount - 1) % tabCount
		return m, nil

	case "r":
		// Refresh whichever tab the user is currently on.
		switch m.tab {
		case tabPulse:
			return m, m.refreshNow()
		case tabPRs:
			return m, m.refreshPRsNow()
		case tabInbox:
			return m, m.refreshInboxNow()
		}
	}

	switch m.tab {
	case tabPulse:
		return m.handlePulseKey(msg)
	case tabPRs:
		return m.handlePRsKey(msg)
	case tabInbox:
		return m.handleInboxKey(msg)
	}
	return m, nil
}

func (m pulseModel) handlePulseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "1":
		m.period = pulse.Period1d
		return m, m.refreshNow()
	case "2":
		m.period = pulse.Period7d
		return m, m.refreshNow()
	case "3":
		m.period = pulse.Period30d
		return m, m.refreshNow()

	case "up", "k":
		if m.view == viewList && m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down", "j":
		if m.view == viewList && m.cursor < len(m.snap.Projects)-1 {
			m.cursor++
		}
		return m, nil

	case "enter":
		if m.view == viewList && len(m.snap.Projects) > 0 {
			m.view = viewDrill
		}
		return m, nil
	case "esc":
		if m.view == viewDrill {
			m.view = viewList
		}
		return m, nil
	}
	return m, nil
}

func (m pulseModel) handlePRsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "1":
		m.prScope = pulse.PRScopeMine
		m.prCursor = 0
		return m, m.refreshPRsNow()
	case "2":
		m.prScope = pulse.PRScopeReviewing
		m.prCursor = 0
		return m, m.refreshPRsNow()
	case "a":
		// Toggle workspace.toml filter. Useful when you expected a PR
		// to be visible but it isn't — flipping this surfaces every
		// PR you have access to so you can confirm whether the PR is
		// actually missing from your workspace registry vs missing
		// entirely.
		m.prLimitToWorkspace = !m.prLimitToWorkspace
		m.prCursor = 0
		return m, m.refreshPRsNow()

	case "up", "k":
		if m.prCursor > 0 {
			m.prCursor--
		}
		return m, nil
	case "down", "j":
		if m.prCursor < m.visiblePRCount()-1 {
			m.prCursor++
		}
		return m, nil

	case "o", "enter":
		pr, ok := m.currentPR()
		if !ok {
			return m, nil
		}
		return m, runPRAction("opened in browser",
			func() error { return pulse.OpenInBrowser(pr.URL) }, m.prScope, m.prLimitToWorkspace)

	case "d":
		pr, ok := m.currentPR()
		if !ok {
			return m, nil
		}
		newDraft := !pr.Draft
		label := "marked ready for review"
		if newDraft {
			label = "marked as draft"
		}
		m.markInFlight(pr, prOverride{draft: &newDraft})
		return m, runPRAction(label,
			func() error { return pulse.SetDraft(pr, newDraft) }, m.prScope, m.prLimitToWorkspace)

	case "x":
		pr, ok := m.currentPR()
		if !ok {
			return m, nil
		}
		closed := "closed"
		m.markInFlight(pr, prOverride{state: &closed})
		return m, runPRAction(fmt.Sprintf("closed #%d", pr.Number),
			func() error { return pulse.ClosePR(pr) }, m.prScope, m.prLimitToWorkspace)

	case "u":
		pr, ok := m.currentPR()
		if !ok {
			return m, nil
		}
		open := "open"
		m.markInFlight(pr, prOverride{state: &open})
		return m, runPRAction(fmt.Sprintf("reopened #%d", pr.Number),
			func() error { return pulse.ReopenPR(pr) }, m.prScope, m.prLimitToWorkspace)
	}
	return m, nil
}

// visiblePRCount returns the number of PR rows currently in the
// active scope's snapshot, summed across all groups.
func (m pulseModel) visiblePRCount() int {
	snap := m.activePRSnapshot()
	n := 0
	for _, g := range snap.Groups {
		n += len(g.PRs)
	}
	return n
}

// currentPR returns the PR under the cursor in the active scope.
func (m pulseModel) currentPR() (pulse.PR, bool) {
	snap := m.activePRSnapshot()
	i := 0
	for _, g := range snap.Groups {
		for _, pr := range g.PRs {
			if i == m.prCursor {
				return pr, true
			}
			i++
		}
	}
	return pulse.PR{}, false
}

func (m pulseModel) activePRSnapshot() pulse.PRSnapshot {
	if m.prScope == pulse.PRScopeMine {
		return m.prSnapMine
	}
	return m.prSnapRev
}

// markInFlight registers a PR as having an action mid-flight and
// records the optimistic delta to apply to the row until the next
// snapshot reconciles. Mutates the model in place; the caller still
// returns the runPRAction tea.Cmd to actually do the work.
func (m *pulseModel) markInFlight(pr pulse.PR, ov prOverride) {
	if m.prInFlight == nil {
		m.prInFlight = map[string]bool{}
	}
	if m.prOptimistic == nil {
		m.prOptimistic = map[string]prOverride{}
	}
	k := prKey(pr)
	m.prInFlight[k] = true
	m.prOptimistic[k] = ov
}

// applyOverride returns a copy of pr with any optimistic override
// applied. Used by the renderer so the row reflects the user's
// intent immediately, before the API confirms.
func (m pulseModel) applyOverride(pr pulse.PR) pulse.PR {
	ov, ok := m.prOptimistic[prKey(pr)]
	if !ok {
		return pr
	}
	if ov.draft != nil {
		pr.Draft = *ov.draft
	}
	if ov.state != nil {
		pr.State = *ov.state
	}
	return pr
}

func (m *pulseModel) refreshPRsNow() tea.Cmd {
	if m.prLoading {
		return nil
	}
	m.prLoading = true
	return tea.Batch(fetchPRs(m.prScope, m.prLimitToWorkspace), m.spin.Tick)
}

func (m *pulseModel) refreshInboxNow() tea.Cmd {
	if m.inboxLoading {
		return nil
	}
	m.inboxLoading = true
	return tea.Batch(fetchInbox(), m.spin.Tick)
}

func (m pulseModel) handleInboxKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.inboxCursor > 0 {
			m.inboxCursor--
		}
	case "down", "j":
		if m.inboxCursor < len(m.inbox.Entries)-1 {
			m.inboxCursor++
		}
	}
	return m, nil
}

func (m *pulseModel) refreshNow() tea.Cmd {
	if m.loading {
		return nil
	}
	m.loading = true
	return tea.Batch(fetchSnapshot(m.period), m.spin.Tick)
}

// =============================================================================
// View
// =============================================================================

var (
	styleHeader      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	styleTabActive   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Background(lipgloss.Color("236")).Padding(0, 2)
	styleTabInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Padding(0, 2)
	stylePeriodOn    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	stylePeriodOff   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleSelected    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("219"))
	styleMuted       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleErr         = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styleSpark       = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
)

func (m pulseModel) View() string {
	var b strings.Builder

	// Header: tabs + period selector
	b.WriteString(m.renderHeader())
	b.WriteString("\n\n")

	// Body
	// Pulse-tab error / loading guards apply only on the Pulse tab.
	// PRs tab has its own loading/error state, handled in renderPRsTab.
	if m.tab == tabPulse {
		if m.err != nil {
			b.WriteString(styleErr.Render("error: " + m.err.Error()))
			b.WriteString("\n\n")
			b.WriteString(styleMuted.Render("press [r] to retry, [q] to quit"))
			return b.String()
		}
		if m.loading && m.snap.TotalCommits == 0 {
			b.WriteString(m.spin.View())
			b.WriteString(" loading from GitHub…")
			return b.String()
		}
	}

	switch m.tab {
	case tabPulse:
		if m.view == viewList {
			b.WriteString(m.renderPulseList())
		} else {
			b.WriteString(m.renderPulseDrill())
		}
	case tabPRs:
		b.WriteString(m.renderPRsTab())
	case tabInbox:
		b.WriteString(m.renderInboxTab())
	}

	b.WriteString("\n\n")
	b.WriteString(m.renderFooter())
	return b.String()
}

func (m pulseModel) renderHeader() string {
	tabs := []string{}
	for i, name := range []string{"Pulse", "PRs", "Inbox"} {
		if pulseTab(i) == m.tab {
			tabs = append(tabs, styleTabActive.Render(name))
		} else {
			tabs = append(tabs, styleTabInactive.Render(name))
		}
	}
	tabRow := strings.Join(tabs, " ")

	periods := []string{}
	for _, p := range []pulse.Period{pulse.Period1d, pulse.Period7d, pulse.Period30d} {
		label := "[" + p.Name + "]"
		if p.Name == m.period.Name {
			periods = append(periods, stylePeriodOn.Render(label))
		} else {
			periods = append(periods, stylePeriodOff.Render(label))
		}
	}
	periodRow := strings.Join(periods, " ")

	title := styleHeader.Render("ws pulse")
	return fmt.Sprintf("%s    %s    %s", title, tabRow, periodRow)
}

func (m pulseModel) renderPulseList() string {
	var b strings.Builder
	snap := m.snap

	if snap.TotalCommits == 0 {
		fmt.Fprintf(&b, "  no commits in the last %s\n\n", m.period.Name)
		fmt.Fprintf(&b, "  diagnostics:\n")
		fmt.Fprintf(&b, "    raw events fetched : %d\n", snap.RawEventCount)
		fmt.Fprintf(&b, "    push events        : %d\n", snap.PushEventCount)
		fmt.Fprintf(&b, "    workspace projects : %d\n", len(ws.Projects))
		if snap.RawEventCount == 0 {
			b.WriteString("\n  ")
			b.WriteString(styleMuted.Render("→ GitHub returned 0 events. Check `ws auth login` scopes (need read:user, repo)."))
		} else if snap.PushEventCount == 0 {
			b.WriteString("\n  ")
			b.WriteString(styleMuted.Render("→ Events exist but none are pushes in this period. Try [3] for 30d."))
		}
		return b.String()
	}

	prs := 0 // placeholder until Phase 4
	fmt.Fprintf(&b, "  %d commits  ·  %d merged PRs  ·  %d projects\n\n",
		snap.TotalCommits, prs, len(snap.Projects))

	if len(snap.ByMachine) > 0 {
		b.WriteString("  By machine:\n")
		max := 1
		for _, mc := range snap.ByMachine {
			if mc.Count > max {
				max = mc.Count
			}
		}
		for _, mc := range snap.ByMachine {
			bar := makeBar(mc.Count, max, 18)
			pct := 0
			if snap.TotalCommits > 0 {
				pct = mc.Count * 100 / snap.TotalCommits
			}
			fmt.Fprintf(&b, "    %-8s %s %3d  (%2d%%)\n", mc.Machine, bar, mc.Count, pct)
		}
		b.WriteString("\n")
	}

	if len(snap.ByDay) > 0 {
		// Period-aware bar widths so the chart fits typical terminals:
		//   1d  → 24 hourly bars, 2 cols each → ~72 chars wide
		//   7d  → 7 daily bars, 4 cols each   → ~32 chars wide
		//   30d → 30 daily bars, 1 col each   → ~60 chars wide
		colWidth := 4
		switch m.period.Name {
		case "1d":
			colWidth = 2
		case "30d":
			colWidth = 1
		}
		labels := bucketLabels(m.period, snap.GeneratedAt)
		chart := barChart(snap.ByDay, labels, 6, colWidth, m.chartAnim)
		fmt.Fprintf(&b, "  %s — %s\n\n",
			styleHeader.Render("pushes per "+bucketLabel(m.period)),
			styleMuted.Render(fmt.Sprintf("last %s", m.period.Name)))
		// Indent each chart line by two spaces.
		for _, line := range strings.Split(chart, "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "  %-22s %-8s %-22s %s\n",
		"PROJECT", "COMMITS", "MACHINES", "LAST")
	for i, ps := range snap.Projects {
		marker := "  "
		row := fmt.Sprintf("%-22s %-8d %-22s %s",
			truncate(ps.Project, 22),
			ps.Commits,
			machinesSummary(ps.Machines),
			humanAgo(snap.GeneratedAt, ps.LastPush),
		)
		if i == m.cursor {
			marker = styleSelected.Render("▶ ")
			row = styleSelected.Render(row)
		}
		fmt.Fprintf(&b, "%s%s\n", marker, row)
	}

	return b.String()
}

func (m pulseModel) renderPulseDrill() string {
	if m.cursor >= len(m.snap.Projects) {
		return styleMuted.Render("(no project selected)")
	}
	ps := m.snap.Projects[m.cursor]
	var b strings.Builder

	// Header.
	fmt.Fprintf(&b, "  %s   %s\n",
		styleHeader.Render(ps.Project),
		styleMuted.Render(ps.Repo))
	fmt.Fprintf(&b, "  %d pushes   last: %s   period: %s\n\n",
		ps.Commits, humanAgo(m.snap.GeneratedAt, ps.LastPush), m.period.Name)

	// Bar chart, same renderer as the main view but a bit shorter
	// because the drill page also has to fit branches + recent.
	if len(ps.Spark) > 0 {
		colWidth := 4
		switch m.period.Name {
		case "1d":
			colWidth = 2
		case "30d":
			colWidth = 1
		}
		labels := bucketLabels(m.period, m.snap.GeneratedAt)
		chart := barChart(ps.Spark, labels, 5, colWidth, m.chartAnim)
		for _, line := range strings.Split(chart, "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
		b.WriteString("\n")
	}

	// Machines breakdown for this project.
	if len(ps.Machines) > 0 {
		b.WriteString("  ")
		b.WriteString(styleHeader.Render("by machine"))
		b.WriteString("\n")
		max := 1
		for _, mc := range ps.Machines {
			if mc.Count > max {
				max = mc.Count
			}
		}
		for _, mc := range ps.Machines {
			fmt.Fprintf(&b, "    %-10s %s  %d\n", mc.Machine, makeBar(mc.Count, max, 22), mc.Count)
		}
		b.WriteString("\n")
	}

	// Top branches.
	if len(ps.Branches) > 0 {
		b.WriteString("  ")
		b.WriteString(styleHeader.Render("top branches"))
		b.WriteString("\n")
		topN := 6
		if len(ps.Branches) < topN {
			topN = len(ps.Branches)
		}
		for _, bs := range ps.Branches[:topN] {
			fmt.Fprintf(&b, "    %-40s %-9s ↑%-3d %s\n",
				truncate(bs.Branch, 40),
				styleMuted.Render("["+bs.Machine+"]"),
				bs.Pushes,
				styleMuted.Render(humanAgo(m.snap.GeneratedAt, bs.LastPush)))
		}
		if len(ps.Branches) > topN {
			fmt.Fprintf(&b, "    %s\n", styleMuted.Render(fmt.Sprintf("… %d more", len(ps.Branches)-topN)))
		}
		b.WriteString("\n")
	}

	// Recent pushes.
	if len(ps.Recent) > 0 {
		b.WriteString("  ")
		b.WriteString(styleHeader.Render("recent pushes"))
		b.WriteString("\n")
		for _, c := range ps.Recent {
			fmt.Fprintf(&b, "    %s  %-30s %-9s %s\n",
				styleMuted.Render(c.SHA),
				truncate(c.Branch, 30),
				styleMuted.Render("["+c.Machine+"]"),
				styleMuted.Render(humanAgo(m.snap.GeneratedAt, c.Timestamp)))
		}
		b.WriteString("\n")
	}

	// Cross-link: open PRs in this project pulled from prSnapMine.
	matchingPRs := m.prsForProject(ps)
	if len(matchingPRs) > 0 {
		b.WriteString("  ")
		b.WriteString(styleHeader.Render("open PRs in this project"))
		b.WriteString("\n")
		for _, pr := range matchingPRs {
			state := "[open]"
			if pr.Draft {
				state = "[draft]"
			}
			fmt.Fprintf(&b, "    #%-5d %-50s %-9s %s\n",
				pr.Number,
				truncate(pr.Title, 50),
				state,
				styleMuted.Render("["+string(pr.Source)+"]"))
		}
	}

	return b.String()
}

// prsForProject filters the cached Mine PR snapshot down to PRs
// targeting the given ProjectStat. Match is by repo full_name when
// available; falls back to project name lookup.
func (m pulseModel) prsForProject(ps pulse.ProjectStat) []pulse.PR {
	var out []pulse.PR
	for _, g := range m.prSnapMine.Groups {
		if g.Repo != ps.Repo && g.Project != ps.Project {
			continue
		}
		out = append(out, g.PRs...)
	}
	return out
}

func (m pulseModel) renderPRsTab() string {
	var b strings.Builder

	// Sub-tabs: Mine | Reviewing
	mine := "Mine"
	reviewing := "Reviewing"
	if m.prScope == pulse.PRScopeMine {
		mine = styleTabActive.Render("[1] Mine")
		reviewing = styleTabInactive.Render("[2] Reviewing")
	} else {
		mine = styleTabInactive.Render("[1] Mine")
		reviewing = styleTabActive.Render("[2] Reviewing")
	}
	fmt.Fprintf(&b, "  %s  %s\n\n", mine, reviewing)

	if m.prErr != nil {
		fmt.Fprintf(&b, "  %s\n", styleErr.Render("error: "+m.prErr.Error()))
		return b.String()
	}
	if m.prLoading && m.visiblePRCount() == 0 {
		fmt.Fprintf(&b, "  %s loading PRs from GitHub…\n", m.spin.View())
		return b.String()
	}

	snap := m.activePRSnapshot()
	if len(snap.Groups) == 0 {
		b.WriteString(styleMuted.Render("  no PRs in this scope"))
		if snap.LimitedToWorkspace {
			b.WriteString(styleMuted.Render(" (filtered to workspace.toml repos)"))
		}
		b.WriteString("\n")
		return b.String()
	}

	fmt.Fprintf(&b, "  %d PRs across %d repos\n\n", snap.Total, len(snap.Groups))

	// Tree render. Cursor is a flat index across all PR rows.
	idx := 0
	prevOrg := ""
	for _, g := range snap.Groups {
		if g.Org != prevOrg {
			fmt.Fprintf(&b, "  %s\n", styleHeader.Render("▼ "+g.Org))
			prevOrg = g.Org
		}
		repoLabel := g.Repo
		if g.Project != "" {
			repoLabel = g.Repo + styleMuted.Render("  ("+g.Project+")")
		}
		fmt.Fprintf(&b, "    %s\n", repoLabel)
		for _, pr := range g.PRs {
			selected := idx == m.prCursor
			displayed := m.applyOverride(pr)
			inFlight := m.prInFlight[prKey(pr)]
			row := formatPRRow(displayed)
			marker := "      "
			if selected {
				marker = styleSelected.Render("    ▶ ")
				row = styleSelected.Render(row)
			}
			if inFlight {
				row = styleSpark.Render("⟳ ") + row
			} else {
				row = "  " + row
			}
			fmt.Fprintf(&b, "%s%s\n", marker, row)
			idx++
		}
		b.WriteString("\n")
	}

	if m.prToast != "" {
		b.WriteString("\n  ")
		if strings.HasPrefix(m.prToast, "✗") {
			b.WriteString(styleErr.Render(m.prToast))
		} else {
			b.WriteString(styleSelected.Render(m.prToast))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func (m pulseModel) renderInboxTab() string {
	var b strings.Builder
	if m.inboxLoading && len(m.inbox.Entries) == 0 {
		fmt.Fprintf(&b, "  %s scanning local worktrees…\n", m.spin.View())
		return b.String()
	}
	if len(m.inbox.Entries) == 0 {
		b.WriteString(styleMuted.Render("  inbox is clean — every local commit is pushed."))
		b.WriteString("\n")
		return b.String()
	}

	fmt.Fprintf(&b, "  %d unpushed commits across %d worktrees\n\n", m.inbox.Total, len(m.inbox.Entries))
	for i, e := range m.inbox.Entries {
		marker := "  "
		header := fmt.Sprintf("%-22s  %-30s  ", truncate(e.Project, 22), truncate(e.Branch, 30))
		if e.NoUpstream {
			header += styleMuted.Render("(no upstream)")
		} else {
			header += fmt.Sprintf("↑%d", e.Ahead)
		}
		if i == m.inboxCursor {
			marker = styleSelected.Render("▶ ")
			header = styleSelected.Render(header)
		}
		fmt.Fprintf(&b, "%s%s\n", marker, header)
		// Show commits only for the selected entry to keep the view dense.
		if i == m.inboxCursor && len(e.Commits) > 0 {
			for _, c := range e.Commits {
				fmt.Fprintf(&b, "      %s  %s  %s\n",
					styleMuted.Render(c.SHA),
					truncate(c.Subject, 60),
					styleMuted.Render(humanAgo(m.inbox.GeneratedAt, c.Time)))
			}
		}
	}
	return b.String()
}

func formatPRRow(pr pulse.PR) string {
	state := "[open]"
	if pr.State == "closed" {
		state = "[closed]"
	}
	if pr.Draft {
		state = "[draft]"
	}
	return fmt.Sprintf("#%-5d  %-50s  %-10s  [%s]",
		pr.Number, truncate(pr.Title, 50), state, pr.Machine)
}

func (m pulseModel) renderFooter() string {
	var help []string
	if m.tab == tabPulse {
		help = append(help, "[1/2/3] period", "[tab] tab", "[r] refresh")
		if m.view == viewList {
			help = append(help, "[↑↓] nav", "[enter] drill")
		} else {
			help = append(help, "[esc] back")
		}
	} else if m.tab == tabPRs {
		filterLabel := "[a] show all"
		if !m.prLimitToWorkspace {
			filterLabel = "[a] limit to ws"
		}
		help = append(help,
			"[1/2] scope", filterLabel, "[tab] tab", "[r] refresh", "[↑↓] nav",
			"[o] open", "[d] draft", "[x] close", "[u] reopen")
	} else {
		help = append(help, "[tab] tab", "[r] refresh", "[↑↓] nav")
	}
	help = append(help, "[q] quit")
	left := styleMuted.Render(strings.Join(help, "  "))

	right := ""
	if m.snap.CollectedIn > 0 {
		right = styleMuted.Render(fmt.Sprintf("collected in %dms", m.snap.CollectedIn.Milliseconds()))
	}
	if m.loading {
		right = m.spin.View() + " refreshing"
	}
	return left + "    " + right
}

// =============================================================================
// Helpers
// =============================================================================

// sparkline keeps the old single-line bar string for the per-project
// drill-down where space is tight. The main pulse view uses the
// bigger barChart below.
func sparkline(buckets []int) string {
	bars := []rune("▁▂▃▄▅▆▇█")
	max := 0
	for _, v := range buckets {
		if v > max {
			max = v
		}
	}
	if max == 0 {
		return strings.Repeat("▁", len(buckets))
	}
	var b strings.Builder
	for _, v := range buckets {
		idx := v * (len(bars) - 1) / max
		b.WriteRune(bars[idx])
	}
	return b.String()
}

// barChart renders a multi-row vertical bar chart with a value row
// above each bar and a label row below. height is the number of
// chart rows; colWidth is the column count per bar; anim is 0..1
// scaling factor for the rise animation.
//
// Each bar is drawn with █ (full block) for completely filled cells
// and a fractional unicode block character (▁..▇) for the partial
// top cell so growth feels smooth at low resolutions.
func barChart(buckets []int, labels []string, height, colWidth int, anim float64) string {
	if anim <= 0 {
		anim = 0.001
	}
	if anim > 1 {
		anim = 1
	}
	max := 0
	for _, v := range buckets {
		if v > max {
			max = v
		}
	}
	if max == 0 {
		max = 1
	}

	gap := " "
	bars := []rune("▁▂▃▄▅▆▇█")

	var lines []string

	// Top row: max value annotation aligned to the leftmost bar.
	var top strings.Builder
	top.WriteString(styleMuted.Render(fmt.Sprintf("max %d", max)))
	lines = append(lines, top.String())

	// Chart rows, top to bottom.
	for row := height; row >= 1; row-- {
		var line strings.Builder
		for _, v := range buckets {
			scaled := float64(v) * anim
			// Each row represents (height units of value) / max.
			// Compute fractional fill at this row in eighths so we
			// can pick a partial block character for the top row of
			// each bar.
			cellTop := float64(row) * float64(max) / float64(height)
			cellBottom := float64(row-1) * float64(max) / float64(height)
			switch {
			case scaled >= cellTop:
				line.WriteString(strings.Repeat("█", colWidth))
			case scaled > cellBottom:
				frac := (scaled - cellBottom) / (cellTop - cellBottom)
				idx := int(frac*float64(len(bars))) - 1
				if idx < 0 {
					idx = 0
				}
				if idx >= len(bars) {
					idx = len(bars) - 1
				}
				line.WriteString(strings.Repeat(string(bars[idx]), colWidth))
			default:
				line.WriteString(strings.Repeat(" ", colWidth))
			}
			line.WriteString(gap)
		}
		lines = append(lines, styleSpark.Render(line.String()))
	}

	// Per-bar value row, only when animation is at full bloom (so
	// the numbers don't flicker mid-animation).
	if anim >= 1 {
		var values strings.Builder
		for _, v := range buckets {
			values.WriteString(centerStr(formatBarValue(v, colWidth), colWidth))
			values.WriteString(gap)
		}
		lines = append(lines, styleMuted.Render(values.String()))
	} else {
		lines = append(lines, "")
	}

	// X-axis label row.
	var labs strings.Builder
	for i := range buckets {
		l := ""
		if i < len(labels) {
			l = labels[i]
		}
		labs.WriteString(centerStr(l, colWidth))
		labs.WriteString(gap)
	}
	lines = append(lines, styleMuted.Render(labs.String()))

	return strings.Join(lines, "\n")
}

func formatBarValue(v, w int) string {
	if v == 0 {
		return "·"
	}
	s := fmt.Sprintf("%d", v)
	if len(s) > w {
		s = s[:w]
	}
	return s
}

func centerStr(s string, width int) string {
	if len(s) >= width {
		return s
	}
	pad := width - len(s)
	left := pad / 2
	right := pad - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

// bucketLabels generates the x-axis labels for the bar chart based
// on the period. Labels mark only "interesting" positions (every Nth)
// to avoid visual noise; the others are left blank.
func bucketLabels(period pulse.Period, now time.Time) []string {
	labels := make([]string, period.Buckets)
	bucketSize := period.Window / time.Duration(period.Buckets)
	start := now.Add(-period.Window)
	for i := 0; i < period.Buckets; i++ {
		bucketTime := start.Add(time.Duration(i) * bucketSize)
		switch period.Name {
		case "1d":
			// 24 hourly buckets — label every 4 hours.
			if bucketTime.Hour()%4 == 0 {
				labels[i] = fmt.Sprintf("%02d", bucketTime.Hour())
			}
		case "7d":
			// 7 daily buckets — short weekday name everywhere.
			labels[i] = bucketTime.Weekday().String()[:3]
		case "30d":
			// 30 daily buckets — label every 5 days plus the last.
			if i%5 == 0 || i == period.Buckets-1 {
				labels[i] = fmt.Sprintf("%d", bucketTime.Day())
			}
		}
	}
	return labels
}

func makeBar(v, max, width int) string {
	if max <= 0 {
		return strings.Repeat("░", width)
	}
	filled := v * width / max
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func machinesSummary(ms []pulse.MachineCount) string {
	if len(ms) == 0 {
		return styleMuted.Render("—")
	}
	out := make([]string, 0, len(ms))
	// Sort by count desc was already done by aggregate; defensive copy.
	cp := append([]pulse.MachineCount(nil), ms...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Count > cp[j].Count })
	for _, m := range cp {
		out = append(out, fmt.Sprintf("%s:%d", m.Machine, m.Count))
	}
	s := strings.Join(out, " ")
	if len(s) > 22 {
		s = s[:21] + "…"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}

func humanAgo(now, t time.Time) string {
	if t.IsZero() {
		return styleMuted.Render("—")
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func bucketLabel(p pulse.Period) string {
	switch p.Name {
	case "1d":
		return "hour"
	default:
		return "day"
	}
}
