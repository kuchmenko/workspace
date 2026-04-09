package agent

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/color"
	"image/png"
	"math"
	"os"
	"syscall"
	"unsafe"

	"github.com/fogleman/gg"
)

// Kitty graphics protocol constants.
const (
	gpChunkSize = 4096 // base64 bytes per chunk
)

// GPRender renders the agent graph as a pixel image using the gg 2D
// library and outputs it to the terminal via the Kitty graphics protocol.
// This is a proof-of-concept: a single static frame, no interactivity.
//
// The image dimensions are derived from the terminal size (cols × rows ×
// approximate cell pixel size). On HiDPI screens, kitty auto-scales.
func GPRender(g *Graph, focusedID string, termW, termH int) error {
	// Get actual pixel dimensions from the terminal via ioctl.
	// Falls back to estimation if unavailable.
	pw, ph := getTermPixelSize()
	if pw <= 0 || ph <= 0 {
		pw = termW * 9
		ph = termH * 18
	}
	if pw < 800 {
		pw = 800
	}
	if ph < 600 {
		ph = 600
	}

	dc := gg.NewContext(pw, ph)

	// ---- palette (catppuccin mocha inspired) ----
	bgColor := hexColor("#1e1e2e")
	focusedFill := hexColor("#313244")
	focusedBorder := hexColor("#f5c2e7") // pink
	navFill := hexColor("#1e1e2e")
	navBorder := hexColor("#89dceb") // sky
	navBackBorder := hexColor("#cba6f7") // mauve
	navMoreBorder := hexColor("#f9e2af") // yellow
	dimFill := hexColor("#181825")
	dimBorder := hexColor("#45475a")
	edgeNav := hexColor("#89dceb")
	edgeDim := hexColor("#313244")
	textBright := hexColor("#cdd6f4")
	textDim := hexColor("#585b70")
	textFocused := hexColor("#f5c2e7")

	// ---- background ----
	dc.SetColor(bgColor)
	dc.Clear()

	// ---- layout constants ----
	const (
		nodeW       = 180.0
		nodeH       = 50.0
		nodeRadius  = 12.0
		gapX        = 60.0
		gapY        = 40.0
		borderWidth = 2.5
		fontSize    = 16.0
		edgeWidth   = 2.0
	)

	focused := g.Nodes[focusedID]
	if focused == nil {
		return fmt.Errorf("focused node %q not found", focusedID)
	}

	// Camera: focused at center.
	camX := float64(focused.Pos.X)
	camY := float64(focused.Pos.Y)
	centerX := float64(pw) / 2
	centerY := float64(ph) / 2
	stepX := nodeW + gapX
	stepY := nodeH + gapY

	project := func(p Grid) (float64, float64) {
		dx := float64(p.X) - camX
		dy := float64(p.Y) - camY
		return centerX + dx*stepX, centerY + dy*stepY
	}

	// ---- compute highlights ----
	slots := ComputeSlots(g, focusedID, DirNone, "")
	hlMap := make(map[string]HighlightLevel)
	for id := range g.Nodes {
		hlMap[id] = HLBackground
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
		}
	}
	hlMap[focusedID] = HLFocused

	// ---- draw edges ----
	for _, n := range g.Nodes {
		if !n.Placed || n.Parent == "" {
			continue
		}
		p, ok := g.Nodes[n.Parent]
		if !ok || !p.Placed {
			continue
		}
		px, py := project(p.Pos)
		cx, cy := project(n.Pos)

		edgeLvl := minLevel(hlMap[p.ID], hlMap[n.ID])
		if edgeLvl <= HLNavMore {
			dc.SetColor(edgeNav)
			dc.SetLineWidth(edgeWidth + 1)
		} else {
			dc.SetColor(edgeDim)
			dc.SetLineWidth(edgeWidth)
		}
		dc.DrawLine(px, py, cx, cy)
		dc.Stroke()
	}

	// ---- draw nodes ----
	// Sort: dim first, bright last (focused on top).
	type nodeRender struct {
		id string
		n  *Node
		hl HighlightLevel
	}
	var ordered []nodeRender
	for id, n := range g.Nodes {
		if !n.Placed {
			continue
		}
		ordered = append(ordered, nodeRender{id, n, hlMap[id]})
	}
	// Stable sort dim→bright.
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			if ordered[i].hl < ordered[j].hl {
				ordered[i], ordered[j] = ordered[j], ordered[i]
			}
		}
	}

	for _, nr := range ordered {
		nx, ny := project(nr.n.Pos)
		x := nx - nodeW/2
		y := ny - nodeH/2

		var fill, border, textC color.Color
		bw := borderWidth
		switch nr.hl {
		case HLFocused:
			fill = focusedFill
			border = focusedBorder
			textC = textFocused
			bw = borderWidth + 1.5
		case HLNavTop:
			fill = navFill
			border = navBorder
			textC = textBright
		case HLNavBack:
			fill = navFill
			border = navBackBorder
			textC = textBright
		case HLNavMore:
			fill = navFill
			border = navMoreBorder
			textC = textBright
		default:
			fill = dimFill
			border = dimBorder
			textC = textDim
		}

		// Filled rounded rect.
		dc.SetColor(fill)
		dc.DrawRoundedRectangle(x, y, nodeW, nodeH, nodeRadius)
		dc.Fill()

		// Border.
		dc.SetColor(border)
		dc.SetLineWidth(bw)
		dc.DrawRoundedRectangle(x, y, nodeW, nodeH, nodeRadius)
		dc.Stroke()

		// Glow effect on focused: outer soft ring.
		if nr.hl == HLFocused {
			fb := focusedBorder
			for i := 1; i <= 3; i++ {
				alpha := uint8(40 - i*10)
				dc.SetColor(color.RGBA{fb.R, fb.G, fb.B, alpha})
				dc.SetLineWidth(1)
				off := float64(i) * 3
				dc.DrawRoundedRectangle(x-off, y-off, nodeW+off*2, nodeH+off*2, nodeRadius+off)
				dc.Stroke()
			}
		}

		// Label with icon.
		icon := kindIcon(nr.n.Kind)
		label := nr.n.Label
		if label == "" {
			label = nr.n.ID
		}
		if icon != "" {
			label = icon + " " + label
		}

		// Truncate if too wide.
		dc.SetColor(textC)
		if err := dc.LoadFontFace("", fontSize); err != nil {
			// Fallback: gg's default.
			_ = err
		}
		maxW := nodeW - 20
		for {
			tw2, _ := dc.MeasureString(label)
			if tw2 <= maxW || len(label) <= 3 {
				break
			}
			label = label[:len(label)-2] + "…"
		}
		tw, _ := dc.MeasureString(label)
		dc.DrawString(label, nx-tw/2, ny+fontSize/3)
	}

	// ---- small dot decorations on edges (optional flair) ----
	for _, n := range g.Nodes {
		if !n.Placed || n.Parent == "" {
			continue
		}
		p, ok := g.Nodes[n.Parent]
		if !ok || !p.Placed {
			continue
		}
		px, py := project(p.Pos)
		cx, cy := project(n.Pos)
		edgeLvl := minLevel(hlMap[p.ID], hlMap[n.ID])
		if edgeLvl > HLNavMore {
			continue
		}
		// Small dot at midpoint.
		mx := (px + cx) / 2
		my := (py + cy) / 2
		dc.SetColor(edgeNav)
		dc.DrawCircle(mx, my, 3)
		dc.Fill()
	}

	// ---- encode PNG ----
	var buf bytes.Buffer
	if err := png.Encode(&buf, dc.Image()); err != nil {
		return fmt.Errorf("png encode: %w", err)
	}

	// ---- output via kitty graphics protocol ----
	return kittyWriteImage(buf.Bytes(), pw, ph, termW, termH)
}

// kittyWriteImage sends a PNG image to the terminal using the kitty
// graphics protocol. The image is displayed at full terminal size using
// the c= (columns) and r= (rows) placement parameters.
func kittyWriteImage(pngData []byte, w, h, termCols, termRows int) error {
	b64 := base64.StdEncoding.EncodeToString(pngData)

	// Enter alt screen, hide cursor, clear, move to top-left.
	fmt.Fprint(os.Stdout, "\033[?1049h\033[?25l\033[2J\033[H")

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
			// First chunk: format=PNG, action=transmit+display, source=direct,
			// s/v=pixel size, c/r=terminal cell span (fills viewport).
			fmt.Fprintf(os.Stdout, "\033_Gf=100,a=T,t=d,s=%d,v=%d,c=%d,r=%d,m=%d;%s\033\\",
				w, h, termCols, termRows, more, chunk)
		} else {
			fmt.Fprintf(os.Stdout, "\033_Gm=%d;%s\033\\", more, chunk)
		}
	}

	// Wait for keypress then restore terminal.
	fmt.Fprint(os.Stderr, "\n  press any key to exit...")
	buf := make([]byte, 1)
	os.Stdin.Read(buf)
	fmt.Fprint(os.Stdout, "\033[?25h\033[?1049l")
	return nil
}

// getTermPixelSize queries the terminal's pixel dimensions via TIOCGWINSZ.
func getTermPixelSize() (int, int) {
	type winsize struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}
	var ws winsize
	fd := os.Stdout.Fd()
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0, 0
	}
	return int(ws.Xpixel), int(ws.Ypixel)
}

func hexColor(hex string) color.RGBA {
	var r, g, b uint8
	if len(hex) == 7 && hex[0] == '#' {
		fmt.Sscanf(hex[1:], "%02x%02x%02x", &r, &g, &b)
	}
	return color.RGBA{r, g, b, 255}
}

// Ensure math import is used (for potential future use).
var _ = math.Pi
