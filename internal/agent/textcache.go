package agent

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"github.com/fogleman/gg"
)

// textBitmapCache pre-renders text labels into small RGBA bitmaps keyed
// by (text, fontSize, color). On each frame, the cached bitmap is blit'd
// via draw.Draw instead of calling dc.DrawString (which does full
// freetype rasterization at ~1ms per call).
//
// Cache entries are created lazily and persist for the session lifetime.
// At typical usage (~50 unique labels × ~3 sizes) the cache uses ~2MB.
type textBitmapCache struct {
	fontPath string
	entries  map[textCacheKey]*textCacheEntry
}

type textCacheKey struct {
	text     string
	fontSize int // quantized to int to avoid float key issues
	r, g, b  uint8
}

type textCacheEntry struct {
	img  *image.RGBA
	w, h int
}

func newTextBitmapCache(fontPath string) *textBitmapCache {
	return &textBitmapCache{
		fontPath: fontPath,
		entries:  make(map[textCacheKey]*textCacheEntry),
	}
}

// drawCached renders text at (cx, cy) centered horizontally on the
// target context. Uses cached bitmap if available, otherwise rasterizes
// once and caches.
func (tc *textBitmapCache) drawCached(dst *image.RGBA, text string, cx, cy, fontSize float64, c color.RGBA) {
	if text == "" || tc.fontPath == "" {
		return
	}

	key := textCacheKey{
		text:     text,
		fontSize: int(math.Round(fontSize)),
		r:        c.R, g: c.G, b: c.B,
	}

	entry, ok := tc.entries[key]
	if !ok {
		entry = tc.rasterize(text, fontSize, c)
		tc.entries[key] = entry
	}

	// Blit centered at (cx, cy).
	dx := int(cx) - entry.w/2
	dy := int(cy) - entry.h/2
	sr := image.Rect(0, 0, entry.w, entry.h)
	dp := image.Pt(dx, dy)
	draw.Draw(dst, sr.Add(dp), entry.img, image.Point{}, draw.Over)
}

// rasterize renders text into a tight-fitting RGBA image.
func (tc *textBitmapCache) rasterize(text string, fontSize float64, c color.RGBA) *textCacheEntry {
	// Use a temp gg context to measure and draw.
	tmpDC := gg.NewContext(1, 1)
	if err := tmpDC.LoadFontFace(tc.fontPath, fontSize); err != nil {
		return &textCacheEntry{img: image.NewRGBA(image.Rect(0, 0, 1, 1)), w: 1, h: 1}
	}
	tw, th := tmpDC.MeasureString(text)
	w := int(math.Ceil(tw)) + 4
	h := int(math.Ceil(th+fontSize)) + 4

	dc := gg.NewContext(w, h)
	if err := dc.LoadFontFace(tc.fontPath, fontSize); err != nil {
		return &textCacheEntry{img: image.NewRGBA(image.Rect(0, 0, 1, 1)), w: 1, h: 1}
	}
	dc.SetColor(c)
	dc.DrawString(text, 2, fontSize+1)

	rgba, ok := dc.Image().(*image.RGBA)
	if !ok {
		return &textCacheEntry{img: image.NewRGBA(image.Rect(0, 0, 1, 1)), w: 1, h: 1}
	}
	return &textCacheEntry{img: rgba, w: w, h: h}
}
