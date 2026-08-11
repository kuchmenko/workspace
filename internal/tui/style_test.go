package tui

import (
	"strings"
	"testing"
)

func TestOverlayPreservesExactCanvasDimensionsAcrossWideRunes(t *testing.T) {
	background := GradientCanvas(20, 6, strings.Repeat("界", 10)+"\n"+strings.Repeat("a", 20))
	foreground := NewStyle().Background("235").Width(9).Render(" modal ")
	view := Overlay(background, foreground, 20, 6)
	lines := strings.Split(view, "\n")
	if len(lines) != 6 {
		t.Fatalf("height = %d, want 6", len(lines))
	}
	for i, line := range lines {
		if got := Width(line); got != 20 {
			t.Fatalf("line %d width = %d, want 20", i, got)
		}
	}
}

func TestGradientResumesAfterNestedStyleBackgroundReset(t *testing.T) {
	nested := "\x1b[38;5;254m\x1b[48;5;232mproject\x1b[0m"
	line := GradientCanvas(20, 1, nested)
	gradient := "\x1b[48;2;43;37;33m"
	if !strings.Contains(line, "\x1b[49m"+gradient) && !strings.Contains(line, "\x1b[0m"+gradient) {
		t.Fatalf("gradient was not restored after nested style: %q", line)
	}
	if got := Width(line); got != 20 {
		t.Fatalf("width = %d, want 20", got)
	}
}

func TestGradientUsesContinuousPerRowColors(t *testing.T) {
	canvas := GradientCanvas(4, 12, "")
	lines := strings.Split(canvas, "\n")
	seen := map[string]bool{}
	for _, line := range lines {
		end := strings.Index(line, "m")
		if end < 0 {
			t.Fatalf("line has no background sequence: %q", line)
		}
		seen[line[:end+1]] = true
	}
	if len(seen) != len(lines) {
		t.Fatalf("gradient has %d colors across %d rows", len(seen), len(lines))
	}
}
