package agent

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// historyEntry tracks one step on the navigation stack. arrivalDir is the
// direction the user moved INTO this node — used to compute the back
// slot direction (= opposite of arrivalDir) at the next render.
type historyEntry struct {
	nodeID     string
	arrivalDir Direction
}

// Model is the bubbletea Model for the cross-renderer agent TUI.
type Model struct {
	graph      *Graph
	history    []historyEntry // top of stack = current focus
	mode       Mode
	width      int
	height     int
	status     string
	themeIndex int   // index into themes[]
	pulseFrame int   // animation frame counter for focused pulse
	animating  bool  // true while camera pan is in progress
	animFrom   Grid  // camera start pos for pan
	animTo     Grid  // camera target pos for pan
	animStep   int   // current step in pan animation
	animSteps  int   // total steps for pan animation
}

// NewModel constructs a Model rooted at the graph's root node. The
// initial history has just the root with no arrival direction.
func NewModel(g *Graph) *Model {
	m := &Model{
		graph: g,
		history: []historyEntry{
			{nodeID: g.RootID, arrivalDir: DirNone},
		},
		mode:   ModeNormal,
		status: "hjkl: move · space: actions · q: quit",
	}
	return m
}

// tickMsg drives animation frames (~30fps).
type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(33*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg{} })
}

func (m *Model) Init() tea.Cmd { return tickCmd() }

func (m *Model) theme() Theme { return themes[m.themeIndex%len(themes)] }

func (m *Model) currentID() string {
	if len(m.history) == 0 {
		return ""
	}
	return m.history[len(m.history)-1].nodeID
}

func (m *Model) currentNode() *Node {
	id := m.currentID()
	if id == "" {
		return nil
	}
	return m.graph.Nodes[id]
}

// backDirection returns the cardinal that should hold the "back" slot at
// the current focus, or DirNone if at root / no history.
func (m *Model) backDirection() Direction {
	if len(m.history) < 2 {
		return DirNone
	}
	top := m.history[len(m.history)-1]
	return top.arrivalDir.Opposite()
}

// previousID returns the node we'd go back to, or "" if we can't go back.
func (m *Model) previousID() string {
	if len(m.history) < 2 {
		return ""
	}
	return m.history[len(m.history)-2].nodeID
}

func (m *Model) computeSlots() SlotMap {
	return ComputeSlots(m.graph, m.currentID(), m.backDirection(), m.previousID())
}

// computeHighlight ranks every node in the graph by visual importance
// for the current focus. The 4-level hierarchy:
//
//   0 (HLFocused)   — current focus
//   1 (HLNav*)      — top-2 children, back, more (the cross nav targets)
//   2 (HLOverflow)  — children reachable via the more portal
//   2 (HLAncestor)  — nodes earlier in the navigation history
//   3 (HLBackground)— everything else
//
// The renderer uses these to dim the background while making the
// reachable region pop.
func (m *Model) computeHighlight(slots SlotMap) map[string]HighlightLevel {
	out := make(map[string]HighlightLevel, len(m.graph.Nodes))

	// Default everyone to background.
	for id := range m.graph.Nodes {
		out[id] = HLBackground
	}

	// Ancestors (history minus current).
	for i := 0; i < len(m.history)-1; i++ {
		out[m.history[i].nodeID] = HLAncestor
	}

	// Slot-based highlights override ancestor.
	for _, d := range []Direction{DirNorth, DirEast, DirSouth, DirWest} {
		s := slots[d]
		switch s.Kind {
		case SlotChild:
			out[s.NodeID] = HLNavTop
		case SlotBack:
			out[s.NodeID] = HLNavBack
		case SlotMore:
			out[s.NodeID] = HLNavMore
			// Highlight overflow children at level 2 so the user can
			// see what's "behind" the more portal even before entering.
			for _, oid := range s.Children {
				if cur, ok := out[oid]; ok && cur > HLOverflow {
					out[oid] = HLOverflow
				}
			}
		}
	}

	// Focused last so it always wins.
	out[m.currentID()] = HLFocused
	return out
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		m.pulseFrame++
		if m.animating {
			m.animStep++
			if m.animStep >= m.animSteps {
				m.animating = false
			}
		}
		return m, tickCmd()

	case tea.KeyMsg:
		key := msg.String()

		// Action-mode keymap.
		if m.mode == ModeAction {
			switch key {
			case "esc", " ", "space":
				m.mode = ModeNormal
				return m, nil
			case "h", "left":
				return m, m.fireAction(DirWest)
			case "j", "down":
				return m, m.fireAction(DirSouth)
			case "k", "up":
				return m, m.fireAction(DirNorth)
			case "l", "right":
				return m, m.fireAction(DirEast)
			case "q", "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}

		// Normal-mode keymap.
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			// Esc at root quits; otherwise it's a back step.
			if len(m.history) <= 1 {
				return m, tea.Quit
			}
			m.goBack()
		case "h", "left":
			m.moveDir(DirWest)
		case "j", "down":
			m.moveDir(DirSouth)
		case "k", "up":
			m.moveDir(DirNorth)
		case "l", "right":
			m.moveDir(DirEast)
		case " ", "space":
			if HasActions(m.currentNode()) {
				m.mode = ModeAction
			}
		case "t", "T":
			m.themeIndex = (m.themeIndex + 1) % len(themes)
			m.status = fmt.Sprintf("theme: %s", m.theme().Name)
			rebuildStyles(m.theme())
		}
	}
	return m, nil
}

// moveDir resolves the slot in direction d and acts on it. With the
// cross-aware layout, more portals are real graph nodes — entering one
// is identical to entering any other child.
func (m *Model) moveDir(d Direction) {
	slots := m.computeSlots()
	slot, ok := slots[d]
	if !ok || slot.Kind == SlotEmpty {
		return
	}
	switch slot.Kind {
	case SlotChild, SlotMore:
		m.enterChild(slot.NodeID, d)
	case SlotBack:
		m.goBack()
	}
}

// enterChild pushes a new history entry for the given child node.
// arrivalDir is the direction we moved to get there.
func (m *Model) enterChild(id string, arrivalDir Direction) {
	n, ok := m.graph.Nodes[id]
	if !ok {
		return
	}
	// Start camera pan animation from current focused pos to new.
	cur := m.currentNode()
	if cur != nil {
		m.animFrom = cur.Pos
		m.animTo = n.Pos
		m.animStep = 0
		m.animSteps = 5 // ~165ms at 30fps
		m.animating = true
	}
	m.history = append(m.history, historyEntry{nodeID: id, arrivalDir: arrivalDir})
}

// goBack pops the history stack one step.
func (m *Model) goBack() {
	if len(m.history) <= 1 {
		return
	}
	m.history = m.history[:len(m.history)-1]
}

// (enterMore removed: more portals are now baked into the global layout
// as real KindPortal nodes; entering one is just enterChild.)

func (m *Model) fireAction(d Direction) tea.Cmd {
	cur := m.currentNode()
	if cur == nil {
		m.mode = ModeNormal
		return nil
	}
	actions := ActionsFor(cur)
	a, ok := actions[d]
	if !ok || a.Exec == nil {
		m.mode = ModeNormal
		m.status = fmt.Sprintf("no action bound to %s yet", d)
		return nil
	}
	m.mode = ModeNormal
	if err := a.Exec(); err != nil {
		m.status = fmt.Sprintf("action error: %v", err)
	}
	return nil
}

func (m *Model) View() string {
	if m.width == 0 {
		return "initializing…"
	}
	cur := m.currentNode()
	label := "(none)"
	if cur != nil {
		label = cur.Label
	}
	modeTag := "normal"
	if m.mode == ModeAction {
		modeTag = "action"
	}
	header := m.headerStyle().Render(fmt.Sprintf(" ws agent · %s · %s ", label, modeTag))

	slots := m.computeSlots()
	highlight := m.computeHighlight(slots)
	camera := Grid{}
	if cur != nil {
		camera = cur.Pos
	}
	// Smooth camera pan: interpolate between animFrom and animTo.
	if m.animating && m.animSteps > 0 {
		t := float64(m.animStep) / float64(m.animSteps)
		if t > 1 {
			t = 1
		}
		camera = Grid{
			X: m.animFrom.X + int(float64(m.animTo.X-m.animFrom.X)*t),
			Y: m.animFrom.Y + int(float64(m.animTo.Y-m.animFrom.Y)*t),
		}
	}
	st := RenderState{
		Width:      m.width,
		Height:     m.height - 2,
		FocusedID:  m.currentID(),
		Slots:      slots,
		Mode:       m.mode,
		Highlight:  highlight,
		Camera:     camera,
		PulseFrame: m.pulseFrame,
		Theme:      m.theme(),
	}
	if m.mode == ModeAction && cur != nil {
		st.Actions = ActionsFor(cur)
	}
	canvas := Render(m.graph, st)

	footerText := m.status
	if m.mode == ModeAction {
		footerText = "ACTION · h/j/k/l fire · esc/space exit"
	}
	footer := m.statusStyle().Render(" " + footerText + " ")
	return header + "\n" + canvas + "\n" + footer
}

func (m *Model) headerStyle() lipgloss.Style {
	t := m.theme()
	return lipgloss.NewStyle().Bold(true).Foreground(t.HeaderFg).Background(t.HeaderBg)
}

func (m *Model) statusStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(m.theme().StatusFg)
}
