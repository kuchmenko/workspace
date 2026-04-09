package agent

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/color"
	"image/png"
	"testing"

	"github.com/fogleman/gg"
)

// BenchmarkGPRenderPipeline measures each stage of the graphics protocol
// rendering pipeline to identify the bottleneck.
//
// Run: go test -bench=BenchmarkGP -benchmem ./internal/agent/

func BenchmarkGPDraw(b *testing.B) {
	// Stage 1: gg drawing (CPU-bound 2D rendering)
	for i := 0; i < b.N; i++ {
		dc := gg.NewContext(1920, 1080)
		dc.SetColor(color.RGBA{30, 30, 46, 255})
		dc.Clear()
		// Simulate 20 nodes
		for j := 0; j < 20; j++ {
			x := float64(100 + j*90)
			y := float64(100 + (j%5)*120)
			dc.SetColor(color.RGBA{49, 50, 68, 255})
			dc.DrawRoundedRectangle(x, y, 180, 50, 12)
			dc.Fill()
			dc.SetColor(color.RGBA{245, 194, 231, 255})
			dc.SetLineWidth(2.5)
			dc.DrawRoundedRectangle(x, y, 180, 50, 12)
			dc.Stroke()
			dc.SetColor(color.RGBA{205, 214, 244, 255})
			dc.DrawString(fmt.Sprintf("node-%d", j), x+20, y+30)
		}
		// 10 edges
		for j := 0; j < 10; j++ {
			dc.SetColor(color.RGBA{137, 220, 235, 128})
			dc.SetLineWidth(2)
			dc.DrawLine(float64(190+j*90), float64(125+(j%5)*120), float64(280+j*90), float64(125+((j+1)%5)*120))
			dc.Stroke()
		}
	}
}

func BenchmarkGPEncodePNG(b *testing.B) {
	// Stage 2: PNG compression (typically the bottleneck)
	dc := gg.NewContext(1920, 1080)
	dc.SetColor(color.RGBA{30, 30, 46, 255})
	dc.Clear()
	for j := 0; j < 20; j++ {
		dc.DrawRoundedRectangle(float64(100+j*90), float64(100+(j%5)*120), 180, 50, 12)
		dc.Fill()
	}
	img := dc.Image()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		png.Encode(&buf, img)
	}
}

func BenchmarkGPEncodeBase64(b *testing.B) {
	// Stage 3: base64 encoding
	data := make([]byte, 500_000) // typical PNG size
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		base64.StdEncoding.EncodeToString(data)
	}
}

func BenchmarkGPDrawHalf(b *testing.B) {
	// Half resolution: 960×540 — should be ~4x faster
	for i := 0; i < b.N; i++ {
		dc := gg.NewContext(960, 540)
		dc.SetColor(color.RGBA{30, 30, 46, 255})
		dc.Clear()
		for j := 0; j < 20; j++ {
			x := float64(50 + j*45)
			y := float64(50 + (j%5)*60)
			dc.DrawRoundedRectangle(x, y, 90, 25, 6)
			dc.Fill()
			dc.DrawRoundedRectangle(x, y, 90, 25, 6)
			dc.Stroke()
		}
	}
}

func BenchmarkGPEncodePNGHalf(b *testing.B) {
	dc := gg.NewContext(960, 540)
	dc.SetColor(color.RGBA{30, 30, 46, 255})
	dc.Clear()
	for j := 0; j < 20; j++ {
		dc.DrawRoundedRectangle(float64(50+j*45), float64(50+(j%5)*60), 90, 25, 6)
		dc.Fill()
	}
	img := dc.Image()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		png.Encode(&buf, img)
	}
}

// BenchmarkGPFullPipeline measures the complete render + encode cycle.
func BenchmarkGPFullPipeline1080(b *testing.B) {
	for i := 0; i < b.N; i++ {
		dc := gg.NewContext(1920, 1080)
		dc.SetColor(color.RGBA{30, 30, 46, 255})
		dc.Clear()
		for j := 0; j < 20; j++ {
			dc.DrawRoundedRectangle(float64(100+j*90), float64(100+(j%5)*120), 180, 50, 12)
			dc.Fill()
			dc.DrawRoundedRectangle(float64(100+j*90), float64(100+(j%5)*120), 180, 50, 12)
			dc.Stroke()
			dc.DrawString(fmt.Sprintf("n%d", j), float64(120+j*90), float64(130+(j%5)*120))
		}
		var buf bytes.Buffer
		png.Encode(&buf, dc.Image())
		base64.StdEncoding.EncodeToString(buf.Bytes())
	}
}

func BenchmarkGPFullPipeline540(b *testing.B) {
	for i := 0; i < b.N; i++ {
		dc := gg.NewContext(960, 540)
		dc.SetColor(color.RGBA{30, 30, 46, 255})
		dc.Clear()
		for j := 0; j < 20; j++ {
			dc.DrawRoundedRectangle(float64(50+j*45), float64(50+(j%5)*60), 90, 25, 6)
			dc.Fill()
			dc.DrawRoundedRectangle(float64(50+j*45), float64(50+(j%5)*60), 90, 25, 6)
			dc.Stroke()
			dc.DrawString(fmt.Sprintf("n%d", j), float64(60+j*45), float64(65+(j%5)*60))
		}
		var buf bytes.Buffer
		png.Encode(&buf, dc.Image())
		base64.StdEncoding.EncodeToString(buf.Bytes())
	}
}
