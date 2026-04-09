package agent

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"time"

	"github.com/fogleman/gg"
	"golang.org/x/term"
)

// RunGraphicsMode starts an interactive 60fps game-loop renderer using
// the Kitty graphics protocol. No bubbletea — raw terminal mode with a
// goroutine for key input and a ticker for frame pacing.
//
// scale controls render resolution relative to native terminal pixels:
//   - 1.0 = native (highest quality, ~50fps on 1080p)
//   - 0.5 = half res (good balance, ~100fps+ on 1080p)
//   - 0.25 = quarter res (fastest, still legible for navigation)
//
// Kitty upscales to fill the viewport regardless of render resolution.
// RunGraphicsMode starts an interactive 60fps renderer via Kitty graphics.
//
//   scale = render resolution relative to native pixels (quality)
//   zoom  = camera zoom level (how big nodes appear, how much you see)
//
// Both are configurable via CLI flags. Zoom is also adjustable at
// runtime with +/- keys.
func RunGraphicsMode(g *Graph, scale, zoom float64) error {
	// Terminal pixel dimensions.
	nativeW, nativeH := getTermPixelSize()
	if nativeW <= 0 || nativeH <= 0 {
		nativeW, nativeH = 1920, 1080
	}
	rw := int(float64(nativeW) * scale)
	rh := int(float64(nativeH) * scale)
	if rw < 320 {
		rw = 320
	}
	if rh < 240 {
		rh = 240
	}

	// Terminal cell dimensions for kitty placement.
	termCols, termRows := 200, 50
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		termCols, termRows = w, h
	}

	// Enter raw mode.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Alt screen, hide cursor, clear.
	fmt.Fprint(os.Stdout, "\033[?1049h\033[?25l\033[2J")
	defer fmt.Fprint(os.Stdout, "\033[?25h\033[?1049l")

	// ---- state ----
	history := []historyEntry{{nodeID: g.RootID, arrivalDir: DirNone}}
	camX := float64(g.Nodes[g.RootID].Pos.X)
	camY := float64(g.Nodes[g.RootID].Pos.Y)
	pulseFrame := 0
	mode := ModeNormal
	currentZoom := zoom

	currentID := func() string { return history[len(history)-1].nodeID }
	backDir := func() Direction {
		if len(history) < 2 {
			return DirNone
		}
		return history[len(history)-1].arrivalDir.Opposite()
	}
	prevID := func() string {
		if len(history) < 2 {
			return ""
		}
		return history[len(history)-2].nodeID
	}

	// ---- input goroutine ----
	keyCh := make(chan byte, 32)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			keyCh <- buf[0]
		}
	}()

	// ---- game loop ----
	targetFPS := 60
	ticker := time.NewTicker(time.Second / time.Duration(targetFPS))
	defer ticker.Stop()

	frameCount := 0
	fpsStart := time.Now()
	var measuredFPS int

	for range ticker.C {
		// --- process input ---
		drained := false
		for !drained {
			select {
			case key := <-keyCh:
				switch {
				case key == 'q' || key == 3: // q or ctrl-c
					return nil
				case key == 27: // esc
					if mode == ModeAction {
						mode = ModeNormal
					} else if len(history) > 1 {
						history = history[:len(history)-1]
					} else {
						return nil
					}
				case key == 'h' || key == 68: // h or left arrow (simplified)
					navigateGP(g, &history, &mode, DirWest)
				case key == 'j' || key == 66:
					navigateGP(g, &history, &mode, DirSouth)
				case key == 'k' || key == 65:
					navigateGP(g, &history, &mode, DirNorth)
				case key == 'l' || key == 67:
					navigateGP(g, &history, &mode, DirEast)
				case key == ' ':
					n := g.Nodes[currentID()]
					if n != nil && HasActions(n) {
						if mode == ModeAction {
							mode = ModeNormal
						} else {
							mode = ModeAction
						}
					}
				case key == '+' || key == '=':
					currentZoom *= 1.15
					if currentZoom > 4.0 {
						currentZoom = 4.0
					}
				case key == '-' || key == '_':
					currentZoom /= 1.15
					if currentZoom < 0.15 {
						currentZoom = 0.15
					}
				}
			default:
				drained = true
			}
		}

		// --- interpolate camera ---
		cur := g.Nodes[currentID()]
		if cur != nil {
			targetX := float64(cur.Pos.X)
			targetY := float64(cur.Pos.Y)
			camX += (targetX - camX) * 0.25
			camY += (targetY - camY) * 0.25
			// Snap if close enough.
			if abs64(targetX-camX) < 0.01 {
				camX = targetX
			}
			if abs64(targetY-camY) < 0.01 {
				camY = targetY
			}
		}

		// --- compute slots + highlights ---
		slots := ComputeSlots(g, currentID(), backDir(), prevID())
		hlMap := make(map[string]HighlightLevel)
		for id := range g.Nodes {
			hlMap[id] = HLBackground
		}
		for i := 0; i < len(history)-1; i++ {
			hlMap[history[i].nodeID] = HLAncestor
		}
		for _, d := range []Direction{DirNorth, DirEast, DirSouth, DirWest} {
			s := slots[d]
			switch s.Kind {
			case SlotChild:
				hlMap[s.NodeID] = HLNavTop
			case SlotBack:
				hlMap[s.NodeID] = HLNavBack
			case SlotMore:
				hlMap[s.NodeID] = HLNavMore
				for _, oid := range s.Children {
					if hlMap[oid] > HLOverflow {
						hlMap[oid] = HLOverflow
					}
				}
			}
		}
		hlMap[currentID()] = HLFocused

		// --- render frame ---
		frame := renderGPFrame(g, hlMap, camX, camY, rw, rh, pulseFrame, mode, currentID(), slots, currentZoom)
		writeKittyFrame(frame, rw, rh, termCols, termRows)

		// --- fps counter ---
		pulseFrame++
		frameCount++
		if elapsed := time.Since(fpsStart); elapsed >= time.Second {
			measuredFPS = frameCount
			frameCount = 0
			fpsStart = time.Now()
		}
		// Draw FPS as text overlay (next frame will include it).
		_ = measuredFPS // TODO: render FPS badge on canvas
	}
	return nil
}

func navigateGP(g *Graph, history *[]historyEntry, mode *Mode, dir Direction) {
	cur := (*history)[len(*history)-1]
	backDir := DirNone
	if len(*history) >= 2 {
		backDir = cur.arrivalDir.Opposite()
	}
	prevID := ""
	if len(*history) >= 2 {
		prevID = (*history)[len(*history)-2].nodeID
	}

	if *mode == ModeAction {
		*mode = ModeNormal
		// Action fire would go here (layer 7)
		return
	}

	slots := ComputeSlots(g, cur.nodeID, backDir, prevID)
	s := slots[dir]
	switch s.Kind {
	case SlotChild, SlotMore:
		if _, ok := g.Nodes[s.NodeID]; ok {
			*history = append(*history, historyEntry{nodeID: s.NodeID, arrivalDir: dir})
		}
	case SlotBack:
		if len(*history) > 1 {
			*history = (*history)[:len(*history)-1]
		}
	}
}

func renderGPFrame(g *Graph, hlMap map[string]HighlightLevel, camX, camY float64, w, h, pulse int, mode Mode, focusedID string, slots SlotMap, zoom float64) []byte {
	dc := gg.NewContext(w, h)

	// Palette.
	bgColor := hexColor("#1e1e2e")
	focusedFill := hexColor("#313244")
	focusedBorder := hexColor("#f5c2e7")
	navFill := hexColor("#1e1e2e")
	navBorder := hexColor("#89dceb")
	navBackBorder := hexColor("#cba6f7")
	navMoreBorder := hexColor("#f9e2af")
	dimFill := hexColor("#181825")
	dimBorder := hexColor("#45475a")
	overflowBorder := hexColor("#6c7086")
	ancestorBorder := hexColor("#585b70")
	edgeNav := hexColor("#89dceb")
	edgeDim := hexColor("#313244")
	textBright := hexColor("#cdd6f4")
	textDim := hexColor("#585b70")
	textFocused := hexColor("#f5c2e7")

	// Scale factors: sf = resolution quality, zoom = camera zoom.
	sf := float64(w) / 1920.0
	z := sf * zoom
	nodeW := 200.0 * z
	nodeH := 56.0 * z
	nodeR := 14.0 * z
	stepX := (nodeW + 60.0*z)
	stepY := (nodeH + 40.0*z)
	fontSize := 24.0 * z
	borderW := 2.5 * z
	edgeW := 2.0 * z
	if fontSize < 8 {
		fontSize = 8
	}

	// Load a nice font if available. Try several common paths.
	fontLoaded := false
	fontPaths := []string{
		"/usr/share/fonts/TTF/0xProtoNerdFont-Regular.ttf",
		"/usr/share/fonts/TTF/0xProtoNerdFontMono-Regular.ttf",
		"/usr/share/fonts/liberation/LiberationMono-Regular.ttf",
		"/usr/share/fonts/Adwaita/AdwaitaMono-Regular.ttf",
		"/usr/share/fonts/Adwaita/AdwaitaSans-Regular.ttf",
	}
	for _, fp := range fontPaths {
		if err := dc.LoadFontFace(fp, fontSize); err == nil {
			fontLoaded = true
			break
		}
	}
	_ = fontLoaded

	centerX := float64(w) / 2
	centerY := float64(h) / 2

	project := func(gx, gy int) (float64, float64) {
		dx := float64(gx) - camX
		dy := float64(gy) - camY
		return centerX + dx*stepX, centerY + dy*stepY
	}

	// Background.
	dc.SetColor(bgColor)
	dc.Clear()

	// Subtle grid dots.
	dc.SetColor(hexColor("#313244"))
	for gx := -10; gx <= 10; gx++ {
		for gy := -10; gy <= 10; gy++ {
			px, py := project(gx, gy)
			if px > -50 && px < float64(w)+50 && py > -50 && py < float64(h)+50 {
				dc.DrawCircle(px, py, 1.5*sf)
				dc.Fill()
			}
		}
	}

	// Edges.
	for _, n := range g.Nodes {
		if !n.Placed || n.Parent == "" {
			continue
		}
		p, ok := g.Nodes[n.Parent]
		if !ok || !p.Placed {
			continue
		}
		px, py := project(p.Pos.X, p.Pos.Y)
		cx, cy := project(n.Pos.X, n.Pos.Y)

		lvl := minLevel(hlMap[p.ID], hlMap[n.ID])
		if lvl <= HLNavMore {
			dc.SetColor(withAlpha(edgeNav, 180))
			dc.SetLineWidth(edgeW + 1*sf)
		} else {
			dc.SetColor(withAlpha(edgeDim, 100))
			dc.SetLineWidth(edgeW)
		}
		dc.DrawLine(px, py, cx, cy)
		dc.Stroke()
	}

	// Nodes (dim first, bright last).
	type nr struct {
		n  *Node
		hl HighlightLevel
	}
	var ordered []nr
	for _, n := range g.Nodes {
		if !n.Placed {
			continue
		}
		ordered = append(ordered, nr{n, hlMap[n.ID]})
	}
	// Sort: high HL (dim) first, low HL (bright) last.
	for i := range ordered {
		for j := i + 1; j < len(ordered); j++ {
			if ordered[i].hl < ordered[j].hl {
				ordered[i], ordered[j] = ordered[j], ordered[i]
			}
		}
	}

	for _, item := range ordered {
		nx, ny := project(item.n.Pos.X, item.n.Pos.Y)
		x := nx - nodeW/2
		y := ny - nodeH/2

		var fill, brd color.RGBA
		var textC color.RGBA
		bw := borderW
		switch item.hl {
		case HLFocused:
			fill = focusedFill
			// Pulse: border color oscillates.
			t := float64(pulse%60) / 60.0
			mix := 0.7 + 0.3*sinSmooth(t)
			brd = lerpColor(hexColor("#b4befe"), focusedBorder, mix)
			textC = textFocused
			bw = borderW + 1.5*sf
		case HLNavTop:
			fill = navFill
			brd = navBorder
			textC = textBright
		case HLNavBack:
			fill = navFill
			brd = navBackBorder
			textC = textBright
		case HLNavMore:
			fill = navFill
			brd = navMoreBorder
			textC = textBright
		case HLOverflow:
			fill = dimFill
			brd = overflowBorder
			textC = textDim
		case HLAncestor:
			fill = dimFill
			brd = ancestorBorder
			textC = textDim
		default:
			fill = dimFill
			brd = dimBorder
			textC = textDim
		}

		// Shadow.
		if item.hl <= HLNavMore {
			dc.SetColor(color.RGBA{0, 0, 0, 40})
			dc.DrawRoundedRectangle(x+3*sf, y+3*sf, nodeW, nodeH, nodeR)
			dc.Fill()
		}

		// Fill.
		dc.SetColor(fill)
		dc.DrawRoundedRectangle(x, y, nodeW, nodeH, nodeR)
		dc.Fill()

		// Border.
		dc.SetColor(brd)
		dc.SetLineWidth(bw)
		dc.DrawRoundedRectangle(x, y, nodeW, nodeH, nodeR)
		dc.Stroke()

		// Glow on focused.
		if item.hl == HLFocused {
			for i := 1; i <= 3; i++ {
				alpha := uint8(30 - i*8)
				dc.SetColor(color.RGBA{brd.R, brd.G, brd.B, alpha})
				dc.SetLineWidth(sf)
				off := float64(i) * 3 * sf
				dc.DrawRoundedRectangle(x-off, y-off, nodeW+off*2, nodeH+off*2, nodeR+off)
				dc.Stroke()
			}
		}

		// Label.
		label := item.n.Label
		if label == "" {
			label = item.n.ID
		}
		dc.SetColor(textC)
		for {
			tw, _ := dc.MeasureString(label)
			if tw <= nodeW-16*sf || len(label) <= 3 {
				break
			}
			label = label[:len(label)-2] + "…"
		}
		tw, _ := dc.MeasureString(label)
		dc.DrawString(label, nx-tw/2, ny+fontSize/3)
	}

	// Action mode overlay.
	if mode == ModeAction {
		// Dim overlay.
		dc.SetColor(color.RGBA{0, 0, 0, 120})
		dc.DrawRectangle(0, 0, float64(w), float64(h))
		dc.Fill()

		// Redraw focused on top.
		fn := g.Nodes[focusedID]
		if fn != nil {
			nx, ny := project(fn.Pos.X, fn.Pos.Y)
			dc.SetColor(focusedFill)
			dc.DrawRoundedRectangle(nx-nodeW/2, ny-nodeH/2, nodeW, nodeH, nodeR)
			dc.Fill()
			dc.SetColor(focusedBorder)
			dc.SetLineWidth(borderW + 1.5*sf)
			dc.DrawRoundedRectangle(nx-nodeW/2, ny-nodeH/2, nodeW, nodeH, nodeR)
			dc.Stroke()
		}

		// Action buttons at cardinal positions.
		actions := ActionsFor(g.Nodes[focusedID])
		fx, fy := project(g.Nodes[focusedID].Pos.X, g.Nodes[focusedID].Pos.Y)
		actionColors := map[Direction]color.RGBA{
			DirNorth: hexColor("#f9e2af"),
			DirEast:  hexColor("#a6e3a1"),
			DirSouth: hexColor("#89dceb"),
			DirWest:  hexColor("#f38ba8"),
		}
		for d, act := range actions {
			ox, oy := 0.0, 0.0
			switch d {
			case DirNorth:
				oy = -stepY
			case DirSouth:
				oy = stepY
			case DirEast:
				ox = stepX
			case DirWest:
				ox = -stepX
			}
			ax := fx + ox - nodeW/2
			ay := fy + oy - nodeH/2
			ac := actionColors[d]
			dc.SetColor(color.RGBA{ac.R, ac.G, ac.B, 40})
			dc.DrawRoundedRectangle(ax, ay, nodeW, nodeH, nodeR)
			dc.Fill()
			dc.SetColor(ac)
			dc.SetLineWidth(borderW)
			dc.DrawRoundedRectangle(ax, ay, nodeW, nodeH, nodeR)
			dc.Stroke()
			dc.SetColor(ac)
			tw, _ := dc.MeasureString(act.Label)
			dc.DrawString(act.Label, fx+ox-tw/2, fy+oy+fontSize/3)
		}
	}

	// Encode PNG.
	var buf bytes.Buffer
	png.Encode(&buf, dc.Image())
	return buf.Bytes()
}

func writeKittyFrame(pngData []byte, w, h, cols, rows int) {
	b64 := base64.StdEncoding.EncodeToString(pngData)
	// Move cursor to origin, use image ID 1 so kitty replaces previous.
	fmt.Fprint(os.Stdout, "\033[H")
	for i := 0; i < len(b64); i += gpChunkSize {
		end := i + gpChunkSize
		if end > len(b64) {
			end = len(b64)
		}
		chunk := b64[i:end]
		more := 1
		if end >= len(b64) {
			more = 0
		}
		if i == 0 {
			// i=1: fixed image ID for replacement. q=2: suppress kitty response.
			fmt.Fprintf(os.Stdout, "\033_Gf=100,a=T,t=d,i=1,s=%d,v=%d,c=%d,r=%d,q=2,m=%d;%s\033\\",
				w, h, cols, rows, more, chunk)
		} else {
			fmt.Fprintf(os.Stdout, "\033_Gm=%d;%s\033\\", more, chunk)
		}
	}
}

func sinSmooth(t float64) float64 {
	return (1 + sin(t*2*3.14159)) / 2
}

func sin(x float64) float64 {
	// Approximation good enough for smooth animation.
	x = x - float64(int(x/(2*3.14159)))*2*3.14159
	if x < 0 {
		x += 2 * 3.14159
	}
	if x > 3.14159 {
		return -sin(x - 3.14159)
	}
	// Bhaskara approximation.
	return 16 * x * (3.14159 - x) / (5*3.14159*3.14159 - 4*x*(3.14159-x))
}

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func withAlpha(c color.RGBA, a uint8) color.RGBA {
	return color.RGBA{c.R, c.G, c.B, a}
}

func lerpColor(a, b color.RGBA, t float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: 255,
	}
}
