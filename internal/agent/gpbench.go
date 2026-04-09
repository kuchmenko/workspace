package agent

import (
	"fmt"
	"image"
	"os"
	"sort"
	"time"

	"github.com/fogleman/gg"
)

// RunBench renders N frames headless (no kitty, no terminal) and prints
// per-stage timing breakdown. Use to identify rendering bottlenecks
// without needing a TTY.
//
// Usage: ws agent --template realistic --bench 100 --scale 1.0 --zoom 2
func RunBench(g *Graph, scale, zoom float64, frames int) error {
	nativeW, nativeH := getTermPixelSize()
	if nativeW <= 0 || nativeH <= 0 {
		nativeW, nativeH = 3840, 2160 // assume 4K for bench
	}
	rw := int(float64(nativeW) * scale)
	rh := int(float64(nativeH) * scale)

	fmt.Fprintf(os.Stderr, "bench: %d frames @ %dx%d (scale=%.2f zoom=%.1f)\n", frames, rw, rh, scale, zoom)

	img := image.NewRGBA(image.Rect(0, 0, rw, rh))
	dc := gg.NewContextForRGBA(img)

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
	tc := newTextBitmapCache(activeFontPath)

	// Compute highlights once (focused = root, no navigation).
	focusedID := g.RootID
	slots := ComputeSlots(g, focusedID, DirNone, "")
	hlMap := make(map[string]HighlightLevel, len(g.Nodes))
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

	camX := float64(g.Nodes[focusedID].Pos.X)
	camY := float64(g.Nodes[focusedID].Pos.Y)

	// Timing accumulators.
	drawTimes := make([]time.Duration, 0, frames)
	writeTimes := make([]time.Duration, 0, frames)
	totalTimes := make([]time.Duration, 0, frames)

	shmPath := fmt.Sprintf("%s/ws-agent-bench.rgba", "/dev/shm")
	defer os.Remove(shmPath)

	for i := 0; i < frames; i++ {
		frameStart := time.Now()

		// Stage 1: draw.
		t0 := time.Now()
		renderGPFrameInto(dc, img, g, hlMap, camX, camY, rw, rh, i, ModeNormal, focusedID, slots, zoom, 0, activeFontPath, 0, 0, tc)
		drawDur := time.Since(t0)

		// Stage 2: write to /dev/shm (simulates present without kitty).
		t1 := time.Now()
		os.WriteFile(shmPath, img.Pix, 0o600)
		writeDur := time.Since(t1)

		totalDur := time.Since(frameStart)

		drawTimes = append(drawTimes, drawDur)
		writeTimes = append(writeTimes, writeDur)
		totalTimes = append(totalTimes, totalDur)
	}

	// Stats.
	printStats := func(name string, times []time.Duration) {
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		var sum time.Duration
		for _, t := range times {
			sum += t
		}
		avg := sum / time.Duration(len(times))
		p50 := times[len(times)/2]
		p95 := times[int(float64(len(times))*0.95)]
		p99 := times[int(float64(len(times))*0.99)]
		min := times[0]
		max := times[len(times)-1]

		fmt.Fprintf(os.Stderr, "  %-12s avg=%-8s p50=%-8s p95=%-8s p99=%-8s min=%-8s max=%s\n",
			name, avg.Round(time.Microsecond), p50.Round(time.Microsecond),
			p95.Round(time.Microsecond), p99.Round(time.Microsecond),
			min.Round(time.Microsecond), max.Round(time.Microsecond))
	}

	fmt.Fprintln(os.Stderr, "\n--- benchmark results ---")
	fmt.Fprintf(os.Stderr, "  resolution:  %dx%d (%d bytes/frame = %.1f MB)\n", rw, rh, rw*rh*4, float64(rw*rh*4)/1024/1024)
	fmt.Fprintf(os.Stderr, "  nodes:       %d placed\n", countPlaced(g))
	fmt.Fprintf(os.Stderr, "  frames:      %d\n\n", frames)
	printStats("draw", drawTimes)
	printStats("write_shm", writeTimes)
	printStats("total", totalTimes)

	avgTotal := totalTimes[len(totalTimes)/2]
	fmt.Fprintf(os.Stderr, "\n  theoretical FPS: %.0f\n", float64(time.Second)/float64(avgTotal))
	fmt.Fprintf(os.Stderr, "  draw %%:          %.0f%%\n", float64(drawTimes[len(drawTimes)/2])/float64(avgTotal)*100)
	fmt.Fprintf(os.Stderr, "  write_shm %%:     %.0f%%\n\n", float64(writeTimes[len(writeTimes)/2])/float64(avgTotal)*100)

	return nil
}

func countPlaced(g *Graph) int {
	n := 0
	for _, node := range g.Nodes {
		if node.Placed {
			n++
		}
	}
	return n
}
