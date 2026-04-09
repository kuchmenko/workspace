package agent

import (
	"image"
	"image/color"
	"testing"

	"github.com/fogleman/gg"
)

// Micro-benchmarks for individual draw stages at 4K to find the real
// bottleneck inside renderGPFrameInto.

func BenchmarkClear4K(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 3840, 2160))
	dc := gg.NewContextForRGBA(img)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dc.SetColor(color.RGBA{30, 30, 46, 255})
		dc.Clear()
	}
}

func BenchmarkClear1080(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	dc := gg.NewContextForRGBA(img)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dc.SetColor(color.RGBA{30, 30, 46, 255})
		dc.Clear()
	}
}

func BenchmarkRoundedRect4K(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 3840, 2160))
	dc := gg.NewContextForRGBA(img)
	dc.SetColor(color.RGBA{49, 50, 68, 255})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dc.DrawRoundedRectangle(100, 100, 400, 112, 28)
		dc.Fill()
	}
}

func BenchmarkGridDots4K(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 3840, 2160))
	dc := gg.NewContextForRGBA(img)
	dc.SetColor(color.RGBA{49, 50, 68, 255})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for x := 0; x < 30; x++ {
			for y := 0; y < 30; y++ {
				dc.DrawCircle(float64(x*128), float64(y*72), 3)
				dc.Fill()
			}
		}
	}
}

func BenchmarkLoadFont(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	dc := gg.NewContextForRGBA(img)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dc.LoadFontFace("/usr/share/fonts/TTF/0xProtoNerdFont-Regular.ttf", 48)
	}
}

func BenchmarkDrawString4K(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 3840, 2160))
	dc := gg.NewContextForRGBA(img)
	dc.LoadFontFace("/usr/share/fonts/TTF/0xProtoNerdFont-Regular.ttf", 48)
	dc.SetColor(color.RGBA{205, 214, 244, 255})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dc.DrawString("limitless-exchange-api", 500, 500)
	}
}

func BenchmarkMeasureString(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	dc := gg.NewContextForRGBA(img)
	dc.LoadFontFace("/usr/share/fonts/TTF/0xProtoNerdFont-Regular.ttf", 48)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dc.MeasureString("limitless-exchange-api")
	}
}
