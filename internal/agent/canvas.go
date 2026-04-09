package agent

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Cell sizes — how many terminal cells one logical grid step occupies.
// One grid cell holds one node box, so the cross-navigation neighbors
// (focused.Pos ± unit cardinal) appear right next to focused on screen.
const (
	cellW = 22
	cellH = 6
)

// Mode is the current TUI interaction mode.
type Mode int

const (
	ModeNormal Mode = iota
	ModeAction
)

// HighlightLevel ranks each node's visual weight on the canvas.
type HighlightLevel int

const (
	HLFocused   HighlightLevel = iota // 0 — current focus
	HLNavTop                          // 1a — top-2 children, used for nav
	HLNavBack                         // 1b — back / parent in history
	HLNavMore                         // 1c — more portal slot's representative
	HLOverflow                        // 2  — children reachable through more
	HLAncestor                        // 2  — ancestors in history
	HLBackground                      // 3  — everything else
)

// RenderState bundles everything Render needs.
type RenderState struct {
	Width, Height int
	FocusedID     string
	Slots         SlotMap
	Mode          Mode
	Actions       map[Direction]Action
	Highlight     map[string]HighlightLevel // node ID → level
	Camera        Grid                       // grid coord centered in viewport
	PulseFrame    int                        // animation frame for focused pulse
	Theme         Theme                      // active color theme
}

// Action is a quick-action descriptor in action mode.
type Action struct {
	Label string
	Exec  func() error
}

// Render produces the screen string. ONE pass: every node in the graph
// is projected from its grid Pos to a screen cell, then drawn at a
// brightness level determined by st.Highlight[id]. The "navigation
// cross" is not a separate overlay — it's literally the 5 nodes that
// happen to sit at the camera viewport center (focused + 4 cardinal
// neighbors), made visually prominent by the focused/nav-target styles.
//
// GlobalLayout already arranged the graph so every parent's top
// children land at +1 cardinal offsets, so when the camera centers on
// focused, those children appear in the cross positions naturally.
func Render(g *Graph, st RenderState) string {
	w, h := st.Width, st.Height
	if w < 30 {
		w = 30
	}
	if h < 12 {
		h = 12
	}
	grid := newGrid(w, h)

	cx := w / 2
	cy := h / 2

	project := func(p Grid) (int, int) {
		dx := p.X - st.Camera.X
		dy := p.Y - st.Camera.Y
		return cx + dx*cellW, cy + dy*cellH
	}

	// 1. Compute rects for every PLACED node. Overflow nodes that didn't
	//    get a cardinal cell remain Placed=false and are skipped — they
	//    are not visible on the canvas at all.
	rects := make(map[string]bgRect, len(g.Nodes))
	for id, n := range g.Nodes {
		if !n.Placed {
			continue
		}
		level := st.Highlight[id]
		bw, bh := nodeBoxSize(n, level)
		bcol, brow := project(n.Pos)
		bcol -= bw / 2
		brow -= bh / 2
		rects[id] = bgRect{col: bcol, row: brow, w: bw, h: bh, level: level}
	}

	// 2. Edges first (parent → child) so boxes overdraw their endpoints.
	//    Drawn in dim → bright order so brighter edges layer on top.
	for _, lvl := range []HighlightLevel{HLBackground, HLAncestor, HLOverflow, HLNavBack, HLNavMore, HLNavTop, HLFocused} {
		for id, n := range g.Nodes {
			if n.Parent == "" {
				continue
			}
			pr, ok := rects[n.Parent]
			if !ok {
				continue
			}
			cr, ok := rects[id]
			if !ok {
				continue
			}
			edgeLvl := minLevel(pr.level, cr.level)
			if edgeLvl != lvl {
				continue
			}
			drawBgEdge(grid, pr, cr, edgeLvl)
		}
	}

	// 3. Boxes — dim first so bright nodes overdraw cleanly.
	for _, lvl := range []HighlightLevel{HLBackground, HLAncestor, HLOverflow, HLNavBack, HLNavMore, HLNavTop, HLFocused} {
		for id, r := range rects {
			if r.level != lvl {
				continue
			}
			drawBox(grid, r.col, r.row, r.w, r.h, g.Nodes[id], r.level)
		}
	}

	// 4. Action overlay (replaces the 4 cardinal neighbor boxes).
	if st.Mode == ModeAction {
		focusedRect, ok := rects[st.FocusedID]
		if ok {
			drawActionOverlayAtFocused(grid, focusedRect, st.Actions)
		}
	}

	return serialize(grid)
}

// nodeBoxSize is the box size for any node, accounting for icon prefix.
func nodeBoxSize(n *Node, level HighlightLevel) (int, int) {
	label := n.Label
	if label == "" {
		label = n.ID
	}
	icon := kindIcon(n.Kind)
	if icon != "" {
		label = icon + " " + label
	}
	w := runewidth.StringWidth(label) + 4 // border + 1 pad each side
	if w < 11 {
		w = 11
	}
	if w > cellW-2 {
		w = cellW - 2
	}
	if level == HLFocused && w < 16 {
		w = 16
	}
	return w, 3
}

// drawActionOverlayAtFocused replaces the 4 cardinal neighbors (which
// sit at +1 cell from focused on the global grid) with action boxes in
// fixed per-direction colors.
func drawActionOverlayAtFocused(grid *cellGrid, focused bgRect, actions map[Direction]Action) {
	const aw = 18
	const ah = 3

	cardCol := focused.col + focused.w/2
	cardRow := focused.row + focused.h/2

	positions := map[Direction]struct{ col, row int }{
		DirNorth: {cardCol - aw/2, cardRow - cellH - ah/2},
		DirSouth: {cardCol - aw/2, cardRow + cellH - ah/2},
		DirEast:  {cardCol + cellW - aw/2, cardRow - ah/2},
		DirWest:  {cardCol - cellW - aw/2, cardRow - ah/2},
	}

	for d, pos := range positions {
		act, ok := actions[d]
		if !ok || act.Label == "" {
			continue
		}
		styleID := actionStyleFor(d)
		// Wipe interior so dim bg doesn't show through.
		for y := 0; y < ah; y++ {
			for x := 0; x < aw; x++ {
				grid.put(pos.col+x, pos.row+y, ' ', styleDefault)
			}
		}
		// Heavy border.
		for x := 0; x < aw; x++ {
			ch := '━'
			if x == 0 {
				ch = '┏'
			} else if x == aw-1 {
				ch = '┓'
			}
			grid.put(pos.col+x, pos.row, ch, styleID)
		}
		for x := 0; x < aw; x++ {
			ch := '━'
			if x == 0 {
				ch = '┗'
			} else if x == aw-1 {
				ch = '┛'
			}
			grid.put(pos.col+x, pos.row+ah-1, ch, styleID)
		}
		grid.put(pos.col, pos.row+1, '┃', styleID)
		grid.put(pos.col+aw-1, pos.row+1, '┃', styleID)
		// Centered label.
		label := act.Label
		if runewidth.StringWidth(label) > aw-2 {
			label = runewidth.Truncate(label, aw-3, "…")
		}
		startX := pos.col + (aw-runewidth.StringWidth(label))/2
		x := startX
		for _, r := range label {
			grid.put(x, pos.row+1, r, styleID)
			x += runewidth.RuneWidth(r)
		}
	}
}

// bgRect is the screen-space rectangle of a background node, plus its
// highlight level for style selection.
type bgRect struct {
	col, row, w, h int
	level          HighlightLevel
}

// bgBoxSize is the compact size used for non-cross bg nodes.
func bgBoxSize(n *Node) (int, int) {
	label := n.Label
	if label == "" {
		label = n.ID
	}
	w := runewidth.StringWidth(label) + 4
	if w < 7 {
		w = 7
	}
	if w > cellW-2 {
		w = cellW - 2
	}
	return w, 3
}

// drawBox draws a single box with style based on its highlight level.
func drawBox(grid *cellGrid, col, row, w, h int, n *Node, level HighlightLevel) {
	style := boxStyleFor(level)
	border := style.border

	// Top border
	for x := 0; x < w; x++ {
		var ch rune
		switch x {
		case 0:
			ch = border.tl
		case w - 1:
			ch = border.tr
		default:
			ch = border.h
		}
		grid.put(col+x, row, ch, style.id)
	}
	// Bottom border
	for x := 0; x < w; x++ {
		var ch rune
		switch x {
		case 0:
			ch = border.bl
		case w - 1:
			ch = border.br
		default:
			ch = border.h
		}
		grid.put(col+x, row+h-1, ch, style.id)
	}
	// Side borders
	for y := 1; y < h-1; y++ {
		grid.put(col, row+y, border.v, style.id)
		grid.put(col+w-1, row+y, border.v, style.id)
	}

	// Label with icon prefix, centered.
	label := n.Label
	if label == "" {
		label = n.ID
	}
	icon := kindIcon(n.Kind)
	if icon != "" {
		label = icon + " " + label
	}
	maxLabel := w - 2
	if runewidth.StringWidth(label) > maxLabel {
		label = runewidth.Truncate(label, maxLabel-1, "…")
	}
	labelW := runewidth.StringWidth(label)
	startX := col + (w-labelW)/2
	x := startX
	for _, r := range label {
		grid.put(x, row+1, r, style.id)
		x += runewidth.RuneWidth(r)
	}
}

// drawBgEdge draws a straight orthogonal edge between parent and child.
// With cross-aware layout every parent→child edge is exactly 1 grid step
// in a cardinal direction, so the edge is always a single horizontal or
// vertical segment from the facing border of parent to the facing border
// of child. No L-shapes needed.
func drawBgEdge(grid *cellGrid, parent, child bgRect, level HighlightLevel) {
	style := edgeStyleFor(level)

	// Determine direction from parent grid pos to child grid pos.
	pgx := parent.col + parent.w/2
	pgy := parent.row + parent.h/2
	cgx := child.col + child.w/2
	cgy := child.row + child.h/2

	dx := cgx - pgx
	dy := cgy - pgy

	if abs(dx) > abs(dy) {
		// Horizontal edge.
		y := parent.row + parent.h/2
		var x0, x1 int
		if dx > 0 {
			x0 = parent.col + parent.w // right border of parent
			x1 = child.col - 1         // left border of child
		} else {
			x0 = child.col + child.w    // right border of child
			x1 = parent.col - 1         // left border of parent
		}
		drawHLine(grid, y, x0, x1, style)
	} else if dy != 0 {
		// Vertical edge.
		x := parent.col + parent.w/2
		var y0, y1 int
		if dy > 0 {
			y0 = parent.row + parent.h // bottom border of parent
			y1 = child.row - 1         // top border of child
		} else {
			y0 = child.row + child.h    // bottom border of child
			y1 = parent.row - 1         // top border of parent
		}
		drawVLine(grid, x, y0, y1, style)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// drawActionOverlay overwrites the 4 cross slot positions with action
// buttons in fixed per-direction colors. slotPos is the same map the
// cross overlay uses, so action boxes land exactly on top of the slots.
func drawActionOverlay(grid *cellGrid, slotPos map[Direction]struct{ col, row int }, actions map[Direction]Action) {
	const aw = 18
	const ah = 3

	for d, pos := range slotPos {
		act, ok := actions[d]
		if !ok || act.Label == "" {
			continue
		}
		styleID := actionStyleFor(d)
		// Heavy border
		for x := 0; x < aw; x++ {
			ch := '━'
			if x == 0 {
				ch = '┏'
			} else if x == aw-1 {
				ch = '┓'
			}
			grid.put(pos.col+x, pos.row, ch, styleID)
		}
		for x := 0; x < aw; x++ {
			ch := '━'
			if x == 0 {
				ch = '┗'
			} else if x == aw-1 {
				ch = '┛'
			}
			grid.put(pos.col+x, pos.row+2, ch, styleID)
		}
		grid.put(pos.col, pos.row+1, '┃', styleID)
		grid.put(pos.col+aw-1, pos.row+1, '┃', styleID)
		// Wipe interior so dim bg doesn't show through
		for x := 1; x < aw-1; x++ {
			grid.put(pos.col+x, pos.row+1, ' ', styleID)
		}
		// Centered label
		label := act.Label
		if runewidth.StringWidth(label) > aw-2 {
			label = runewidth.Truncate(label, aw-3, "…")
		}
		startX := pos.col + (aw-runewidth.StringWidth(label))/2
		x := startX
		for _, r := range label {
			grid.put(x, pos.row+1, r, styleID)
			x += runewidth.RuneWidth(r)
		}
	}
}

func minLevel(a, b HighlightLevel) HighlightLevel {
	if a < b {
		return a
	}
	return b
}

// ---------------- low-level draw helpers ----------------

func drawHLine(g *cellGrid, y, x0, x1 int, style int) {
	if y < 0 || y >= g.h {
		return
	}
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	for x := x0; x <= x1; x++ {
		if x < 0 || x >= g.w {
			continue
		}
		if g.runes[y][x] == ' ' {
			g.put(x, y, '─', style)
		}
	}
}

func drawVLine(g *cellGrid, x, y0, y1 int, style int) {
	if x < 0 || x >= g.w {
		return
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	for y := y0; y <= y1; y++ {
		if y < 0 || y >= g.h {
			continue
		}
		if g.runes[y][x] == ' ' {
			g.put(x, y, '│', style)
		}
	}
}

// ---------------- cell grid + serialization ----------------

type cellGrid struct {
	w, h   int
	runes  [][]rune
	styles [][]int
}

func newGrid(w, h int) *cellGrid {
	g := &cellGrid{w: w, h: h}
	g.runes = make([][]rune, h)
	g.styles = make([][]int, h)
	for i := 0; i < h; i++ {
		row := make([]rune, w)
		for j := range row {
			row[j] = ' '
		}
		g.runes[i] = row
		g.styles[i] = make([]int, w)
	}
	return g
}

func (g *cellGrid) put(col, row int, r rune, style int) {
	if col < 0 || col >= g.w || row < 0 || row >= g.h {
		return
	}
	g.runes[row][col] = r
	g.styles[row][col] = style
}

func serialize(g *cellGrid) string {
	var sb strings.Builder
	for r := 0; r < g.h; r++ {
		runStart := 0
		runStyle := g.styles[r][0]
		for c := 1; c <= g.w; c++ {
			var s int
			if c < g.w {
				s = g.styles[r][c]
			}
			if c == g.w || s != runStyle {
				segment := string(g.runes[r][runStart:c])
				sb.WriteString(styleByID(runStyle).Render(segment))
				runStart = c
				if c < g.w {
					runStyle = s
				}
			}
		}
		if r < g.h-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// Theme colors are now in theme.go. The style registry is rebuilt
// whenever the theme changes (T key).

const (
	styleDefault = iota
	// Box styles per highlight level
	styleBoxFocused
	styleBoxNavTop
	styleBoxNavBack
	styleBoxNavMore
	styleBoxOverflow
	styleBoxAncestor
	styleBoxBg
	// Edge styles
	styleEdgeFocused
	styleEdgeNav
	styleEdgeOverflow
	styleEdgeBg
	// Action overlay
	styleActionN
	styleActionE
	styleActionS
	styleActionW
)

type boxBorder struct {
	tl, tr, bl, br, h, v rune
}

var (
	borderLight   = boxBorder{'╭', '╮', '╰', '╯', '─', '│'}
	borderHeavy   = boxBorder{'┏', '┓', '┗', '┛', '━', '┃'}
	borderDouble  = boxBorder{'╔', '╗', '╚', '╝', '═', '║'}
)

type boxStyleSpec struct {
	id     int
	border boxBorder
}

func boxStyleFor(level HighlightLevel) boxStyleSpec {
	switch level {
	case HLFocused:
		return boxStyleSpec{styleBoxFocused, borderDouble}
	case HLNavTop:
		return boxStyleSpec{styleBoxNavTop, borderHeavy}
	case HLNavBack:
		return boxStyleSpec{styleBoxNavBack, borderHeavy}
	case HLNavMore:
		return boxStyleSpec{styleBoxNavMore, borderHeavy}
	case HLOverflow:
		return boxStyleSpec{styleBoxOverflow, borderLight}
	case HLAncestor:
		return boxStyleSpec{styleBoxAncestor, borderLight}
	}
	return boxStyleSpec{styleBoxBg, borderLight}
}

func edgeStyleFor(level HighlightLevel) int {
	switch level {
	case HLFocused:
		return styleEdgeFocused
	case HLNavTop, HLNavBack, HLNavMore:
		return styleEdgeNav
	case HLOverflow, HLAncestor:
		return styleEdgeOverflow
	}
	return styleEdgeBg
}

func actionStyleFor(d Direction) int {
	switch d {
	case DirNorth:
		return styleActionN
	case DirEast:
		return styleActionE
	case DirSouth:
		return styleActionS
	case DirWest:
		return styleActionW
	}
	return styleDefault
}

var styleRegistry map[int]lipgloss.Style

func init() { rebuildStyles(themeCatppuccin) }

// rebuildStyles reconstructs the style registry from a Theme. Called on
// startup and every time the user presses T to cycle themes.
func rebuildStyles(t Theme) {
	styleRegistry = map[int]lipgloss.Style{
		styleDefault: lipgloss.NewStyle(),

		styleBoxFocused:  lipgloss.NewStyle().Bold(true).Foreground(t.Focused),
		styleBoxNavTop:   lipgloss.NewStyle().Bold(true).Foreground(t.NavTop),
		styleBoxNavBack:  lipgloss.NewStyle().Foreground(t.NavBack),
		styleBoxNavMore:  lipgloss.NewStyle().Bold(true).Foreground(t.NavMore),
		styleBoxOverflow: lipgloss.NewStyle().Foreground(t.Overflow),
		styleBoxAncestor: lipgloss.NewStyle().Foreground(t.Ancestor),
		styleBoxBg:       lipgloss.NewStyle().Foreground(t.Bg),

		styleEdgeFocused:  lipgloss.NewStyle().Foreground(t.EdgeFocused),
		styleEdgeNav:      lipgloss.NewStyle().Foreground(t.EdgeNav),
		styleEdgeOverflow: lipgloss.NewStyle().Foreground(t.Overflow),
		styleEdgeBg:       lipgloss.NewStyle().Foreground(t.EdgeBg),

		styleActionN: lipgloss.NewStyle().Bold(true).Foreground(t.ActionN),
		styleActionE: lipgloss.NewStyle().Bold(true).Foreground(t.ActionE),
		styleActionS: lipgloss.NewStyle().Bold(true).Foreground(t.ActionS),
		styleActionW: lipgloss.NewStyle().Bold(true).Foreground(t.ActionW),
	}
}

func styleByID(id int) lipgloss.Style {
	if s, ok := styleRegistry[id]; ok {
		return s
	}
	return styleRegistry[styleDefault]
}
