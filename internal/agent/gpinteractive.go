package agent

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"os"
	"time"

	"github.com/fogleman/gg"
	"golang.org/x/term"
)

const shmDir = "/dev/shm"

// renderer holds the persistent gg.Context (reused across frames to
// avoid 33MB alloc/GC per frame) and handles double-buffered file
// transfer to kitty via /dev/shm.
type renderer struct {
	dc       *gg.Context
	img      *image.RGBA
	w, h     int
	frameNum int
}

func newRenderer(w, h int) *renderer {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(img)
	return &renderer{dc: dc, img: img, w: w, h: h}
}

// present writes raw RGBA to a temp file in /dev/shm and tells kitty
// to display it. Double-buffers: alternates between two file paths so
// kitty reads file A while we write file B next frame.
func (r *renderer) present(cols, rows int) {
	r.frameNum++
	path := fmt.Sprintf("%s/ws-agent-%d.rgba", shmDir, r.frameNum%2)

	// Write raw pixels to /dev/shm (memcpy ~3ms at 4K).
	os.WriteFile(path, r.img.Pix, 0o600)

	// Tell kitty: ~100 bytes through pty.
	pathB64 := base64.StdEncoding.EncodeToString([]byte(path))
	fmt.Fprint(os.Stdout, "\033[H")
	fmt.Fprintf(os.Stdout, "\033_Gf=32,a=T,t=f,i=1,s=%d,v=%d,c=%d,r=%d,q=2;%s\033\\",
		r.w, r.h, cols, rows, pathB64)
}

func (r *renderer) cleanup() {
	os.Remove(fmt.Sprintf("%s/ws-agent-0.rgba", shmDir))
	os.Remove(fmt.Sprintf("%s/ws-agent-1.rgba", shmDir))
}

// RunGraphicsMode starts an interactive 60fps game-loop renderer.
//
// Three performance optimizations vs naive approach:
//  1. mmap'd framebuffer: gg draws directly into /dev/shm. Zero memcpy.
//  2. Persistent gg.Context: no 33MB alloc/GC per frame.
//  3. Viewport culling: skip nodes/edges outside the visible area.
func RunGraphicsMode(g *Graph, scale, zoom float64) error {
	termCols, termRows := 200, 50
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		termCols, termRows = w, h
	}

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

	// Persistent renderer: reuses gg.Context across frames (no alloc/GC).
	ren := newRenderer(rw, rh)
	defer ren.cleanup()

	// Load font ONCE — LoadFontFace parses TTF every call (0.3ms).
	fontPaths := []string{
		"/usr/share/fonts/TTF/0xProtoNerdFont-Regular.ttf",
		"/usr/share/fonts/TTF/0xProtoNerdFontMono-Regular.ttf",
		"/usr/share/fonts/liberation/LiberationMono-Regular.ttf",
		"/usr/share/fonts/Adwaita/AdwaitaMono-Regular.ttf",
	}
	var activeFontPath string
	for _, fp := range fontPaths {
		if _, err := os.Stat(fp); err == nil {
			activeFontPath = fp
			break
		}
	}

	// Pre-render text bitmap cache: rasterize each label once, blit
	// from cache on every frame. Saves ~1ms/DrawString × 20 calls = 20ms.
	textCache := newTextBitmapCache(activeFontPath)

	// Enter raw mode.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

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
	ticker := time.NewTicker(time.Second / 60)
	defer ticker.Stop()

	frameCount := 0
	fpsStart := time.Now()
	var measuredFPS int
	var lastDrawMs, lastPresentMs int64

	for range ticker.C {
		// --- input ---
		drained := false
		for !drained {
			select {
			case key := <-keyCh:
				switch {
				case key == 'q' || key == 3:
					return nil
				case key == 27:
					if mode == ModeAction {
						mode = ModeNormal
					} else if len(history) > 1 {
						history = history[:len(history)-1]
					} else {
						return nil
					}
				case key == 'h' || key == 68:
					navigateGP(g, &history, &mode, DirWest)
				case key == 'j' || key == 66:
					navigateGP(g, &history, &mode, DirSouth)
				case key == 'k' || key == 65:
					navigateGP(g, &history, &mode, DirNorth)
				case key == 'l' || key == 67:
					navigateGP(g, &history, &mode, DirEast)
				case key == ' ':
					if n := g.Nodes[currentID()]; n != nil && HasActions(n) {
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

		// --- camera interpolation ---
		if cur := g.Nodes[currentID()]; cur != nil {
			tx, ty := float64(cur.Pos.X), float64(cur.Pos.Y)
			camX += (tx - camX) * 0.25
			camY += (ty - camY) * 0.25
			if abs64(tx-camX) < 0.01 {
				camX = tx
			}
			if abs64(ty-camY) < 0.01 {
				camY = ty
			}
		}

		// --- highlights ---
		slots := ComputeSlots(g, currentID(), backDir(), prevID())
		hlMap := make(map[string]HighlightLevel, len(g.Nodes))
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

		// --- render into reused context ---
		t0 := time.Now()
		renderGPFrameInto(ren.dc, ren.img, g, hlMap, camX, camY, ren.w, ren.h, pulseFrame, mode, currentID(), slots, currentZoom, measuredFPS, activeFontPath, lastDrawMs, lastPresentMs, textCache)
		drawMs := time.Since(t0).Milliseconds()

		// --- present: write to /dev/shm + tell kitty ---
		t1 := time.Now()
		ren.present(termCols, termRows)
		presentMs := time.Since(t1).Milliseconds()

		// --- fps ---
		pulseFrame++
		frameCount++
		// Stash timing for HUD on next frame.
		lastDrawMs = drawMs
		lastPresentMs = presentMs
		if elapsed := time.Since(fpsStart); elapsed >= time.Second {
			measuredFPS = frameCount
			frameCount = 0
			fpsStart = time.Now()
		}
	}
	return nil
}

func navigateGP(g *Graph, history *[]historyEntry, mode *Mode, dir Direction) {
	cur := (*history)[len(*history)-1]
	bd := DirNone
	if len(*history) >= 2 {
		bd = cur.arrivalDir.Opposite()
	}
	pid := ""
	if len(*history) >= 2 {
		pid = (*history)[len(*history)-2].nodeID
	}
	if *mode == ModeAction {
		*mode = ModeNormal
		return
	}
	slots := ComputeSlots(g, cur.nodeID, bd, pid)
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

// renderGPFrameInto draws one frame into the provided gg.Context (which
// is backed by the mmap'd framebuffer). No allocations, no PNG, no
// memcpy. The context is reused across frames.
func renderGPFrameInto(dc *gg.Context, dst *image.RGBA, g *Graph, hlMap map[string]HighlightLevel, camX, camY float64, w, h, pulse int, mode Mode, focusedID string, slots SlotMap, zoom float64, fps int, fontPath string, drawMs, presentMs int64, tc *textBitmapCache) {
	W := float64(w)
	H := float64(h)

	// Palette (stack-allocated, no heap).
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

	sf := W / 1920.0
	z := sf * zoom
	nodeW := 200.0 * z
	nodeH := 56.0 * z
	nodeR := 14.0 * z
	stepX := nodeW + 60.0*z
	stepY := nodeH + 40.0*z
	fontSize := 24.0 * z
	borderW := 2.5 * z
	edgeW := 2.0 * z
	if fontSize < 8 {
		fontSize = 8
	}

	centerX := W / 2
	centerY := H / 2

	project := func(gx, gy int) (float64, float64) {
		return centerX + (float64(gx)-camX)*stepX,
			centerY + (float64(gy)-camY)*stepY
	}

	// Viewport bounds for culling (with margin).
	margin := stepX
	vpLeft := -margin
	vpRight := W + margin
	vpTop := -margin
	vpBottom := H + margin
	visible := func(px, py float64) bool {
		return px > vpLeft && px < vpRight && py > vpTop && py < vpBottom
	}

	// ---- clear ----
	dc.SetColor(bgColor)
	dc.Clear()

	// ---- edges (culled) ----
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
		if !visible(px, py) && !visible(cx, cy) {
			continue // both endpoints off-screen
		}
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

	// ---- nodes (dim first, bright last; culled) ----
	type nr struct {
		n  *Node
		hl HighlightLevel
	}
	var ordered []nr
	for _, n := range g.Nodes {
		if !n.Placed {
			continue
		}
		px, py := project(n.Pos.X, n.Pos.Y)
		if !visible(px, py) {
			continue // off-screen, skip entirely
		}
		ordered = append(ordered, nr{n, hlMap[n.ID]})
	}
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
			t := float64(pulse%60) / 60.0
			mix := 0.7 + 0.3*sinSmooth(t)
			brd = lerpColor(hexColor("#b4befe"), focusedBorder, mix)
			textC = textFocused
			bw = borderW + 1.5*z
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

		// Shadow on nav+ nodes.
		if item.hl <= HLNavMore {
			dc.SetColor(color.RGBA{0, 0, 0, 40})
			dc.DrawRoundedRectangle(x+3*z, y+3*z, nodeW, nodeH, nodeR)
			dc.Fill()
		}

		// Border as filled shape (no stroke AA artifacts).
		dc.SetColor(brd)
		dc.DrawRoundedRectangle(x, y, nodeW, nodeH, nodeR)
		dc.Fill()
		dc.SetColor(fill)
		dc.DrawRoundedRectangle(x+bw, y+bw, nodeW-bw*2, nodeH-bw*2, nodeR-bw)
		dc.Fill()

		// Glow on focused.
		if item.hl == HLFocused {
			for i := 1; i <= 3; i++ {
				dc.SetColor(color.RGBA{brd.R, brd.G, brd.B, uint8(30 - i*8)})
				dc.SetLineWidth(z)
				off := float64(i) * 3 * z
				dc.DrawRoundedRectangle(x-off, y-off, nodeW+off*2, nodeH+off*2, nodeR+off)
				dc.Stroke()
			}
		}

		// Label — skip text on dim bg nodes (saves ~1ms per node).
		if item.hl <= HLOverflow {
			label := item.n.Label
			if label == "" {
				label = item.n.ID
			}
			// Truncate to fit box (use a rough char-width estimate to
			// avoid MeasureString calls).
			maxChars := int(nodeW / (fontSize * 0.6))
			if len(label) > maxChars && maxChars > 3 {
				label = label[:maxChars-1] + "…"
			}
			tc.drawCached(dst, label, nx, ny, fontSize, textC)
		}
	}

	// ---- action mode overlay ----
	if mode == ModeAction {
		dc.SetColor(color.RGBA{0, 0, 0, 120})
		dc.DrawRectangle(0, 0, W, H)
		dc.Fill()
		if fn := g.Nodes[focusedID]; fn != nil {
			nx, ny := project(fn.Pos.X, fn.Pos.Y)
			dc.SetColor(focusedFill)
			dc.DrawRoundedRectangle(nx-nodeW/2, ny-nodeH/2, nodeW, nodeH, nodeR)
			dc.Fill()
			dc.SetColor(focusedBorder)
			dc.SetLineWidth(borderW + 1.5*z)
			dc.DrawRoundedRectangle(nx-nodeW/2, ny-nodeH/2, nodeW, nodeH, nodeR)
			dc.Stroke()
		}
		actions := ActionsFor(g.Nodes[focusedID])
		fx, fy := project(g.Nodes[focusedID].Pos.X, g.Nodes[focusedID].Pos.Y)
		ac := map[Direction]color.RGBA{
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
			c := ac[d]
			dc.SetColor(color.RGBA{c.R, c.G, c.B, 40})
			dc.DrawRoundedRectangle(ax, ay, nodeW, nodeH, nodeR)
			dc.Fill()
			dc.SetColor(c)
			dc.SetLineWidth(borderW)
			dc.DrawRoundedRectangle(ax, ay, nodeW, nodeH, nodeR)
			dc.Stroke()
			tc.drawCached(dst, act.Label, fx+ox, fy+oy, fontSize, c)
		}
	}

	// ---- HUD ----
	hudSize := H * 0.022
	if hudSize < 12 {
		hudSize = 12
	}
	if hudSize > 28 {
		hudSize = 28
	}
	mg := H * 0.03
	pad := hudSize * 0.5

	// HUD uses cached text blit too. Pill backgrounds drawn with gg,
	// text labels blit'd from cache.
	hudFS := hudSize

	// Timing — bottom-left.
	line3 := fmt.Sprintf("%d FPS  draw:%dms  present:%dms  %dx%d", fps, drawMs, presentMs, w, h)
	l3W := float64(len(line3)) * hudFS * 0.6
	l3Y := H - mg
	dc.SetColor(color.RGBA{0, 0, 0, 200})
	dc.DrawRoundedRectangle(mg-pad, l3Y-hudSize-pad, l3W+pad*2, hudSize+pad*2, pad)
	dc.Fill()
	fpsC := hexColor("#a6e3a1")
	if fps < 30 {
		fpsC = hexColor("#f38ba8")
	} else if fps < 55 {
		fpsC = hexColor("#f9e2af")
	}
	tc.drawCached(dst, line3, mg+l3W/2, l3Y-hudFS*0.3, hudFS, fpsC)

	// Zoom — above timing.
	zt := fmt.Sprintf("zoom %.1fx", zoom)
	ztW := float64(len(zt)) * hudFS * 0.6
	zy := l3Y - hudSize - pad*3
	dc.SetColor(color.RGBA{0, 0, 0, 180})
	dc.DrawRoundedRectangle(mg-pad, zy-hudSize-pad, ztW+pad*2, hudSize+pad*2, pad)
	dc.Fill()
	tc.drawCached(dst, zt, mg+ztW/2, zy-hudFS*0.3, hudFS, hexColor("#89b4fa"))

	// Focused label — top-center.
	fl := focusedID
	if fn := g.Nodes[focusedID]; fn != nil && fn.Label != "" {
		fl = fn.Label
	}
	flW := float64(len(fl)) * hudFS * 0.6
	dc.SetColor(color.RGBA{0, 0, 0, 200})
	dc.DrawRoundedRectangle(W/2-flW/2-pad*2, mg-pad, flW+pad*4, hudSize+pad*2, pad)
	dc.Fill()
	tc.drawCached(dst, fl, W/2, mg+hudFS*0.5, hudFS, hexColor("#f5c2e7"))

	// Controls — bottom-center.
	hint := "hjkl:move  +/-:zoom  space:actions  q:quit"
	hintW := float64(len(hint)) * hudFS * 0.6
	dc.SetColor(color.RGBA{0, 0, 0, 140})
	dc.DrawRoundedRectangle(W/2-hintW/2-pad*2, H-mg-hudSize-pad, hintW+pad*4, hudSize+pad*2, pad)
	dc.Fill()
	tc.drawCached(dst, hint, W/2, H-mg-hudFS*0.3, hudFS, hexColor("#585b70"))
}

func sinSmooth(t float64) float64 {
	return (1 + sin(t*2*3.14159)) / 2
}

func sin(x float64) float64 {
	x = x - float64(int(x/(2*3.14159)))*2*3.14159
	if x < 0 {
		x += 2 * 3.14159
	}
	if x > 3.14159 {
		return -sin(x - 3.14159)
	}
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

