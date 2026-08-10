package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type Color string

type Border struct{ inner lipgloss.Border }

func RoundedBorder() Border { return Border{inner: lipgloss.RoundedBorder()} }

type Style struct{ s lipgloss.Style }

func NewStyle() Style { return Style{s: lipgloss.NewStyle()} }

func (s Style) Render(text string) string { return s.s.Render(text) }
func (s Style) Bold(b bool) Style         { return Style{s: s.s.Bold(b)} }
func (s Style) Faint(b bool) Style        { return Style{s: s.s.Faint(b)} }
func (s Style) Italic(b bool) Style       { return Style{s: s.s.Italic(b)} }
func (s Style) Underline(b bool) Style    { return Style{s: s.s.Underline(b)} }
func (s Style) Foreground(c Color) Style  { return Style{s: s.s.Foreground(lipgloss.Color(string(c)))} }
func (s Style) Background(c Color) Style  { return Style{s: s.s.Background(lipgloss.Color(string(c)))} }
func (s Style) Padding(p ...int) Style    { return Style{s: s.s.Padding(p...)} }
func (s Style) Width(w int) Style         { return Style{s: s.s.Width(w)} }
func (s Style) Height(h int) Style        { return Style{s: s.s.Height(h)} }
func (s Style) Align(p Position) Style    { return Style{s: s.s.Align(lipgloss.Position(p))} }
func (s Style) Border(b Border) Style     { return Style{s: s.s.Border(b.inner)} }
func (s Style) BorderForeground(c Color) Style {
	return Style{s: s.s.BorderForeground(lipgloss.Color(string(c)))}
}

type Position float64

const (
	Top    Position = Position(lipgloss.Top)
	Left   Position = Position(lipgloss.Left)
	Center Position = Position(lipgloss.Center)
	Right  Position = Position(lipgloss.Right)
	Bottom Position = Position(lipgloss.Bottom)
)

type PlaceOption func(*placeConfig)

type placeConfig struct {
	whitespaceBg *Color
}

func WithWhitespaceBackground(c Color) PlaceOption {
	return func(p *placeConfig) { p.whitespaceBg = &c }
}

func Place(width, height int, hPos, vPos Position, content string, opts ...PlaceOption) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = Truncate(lines[i], width)
	}
	content = strings.Join(lines, "\n")

	cfg := placeConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	teaOpts := []lipgloss.WhitespaceOption{}
	if cfg.whitespaceBg != nil {
		teaOpts = append(teaOpts, lipgloss.WithWhitespaceBackground(lipgloss.Color(string(*cfg.whitespaceBg))))
	}
	return lipgloss.Place(width, height, lipgloss.Position(hPos), lipgloss.Position(vPos), content, teaOpts...)
}

func Width(s string) int  { return lipgloss.Width(s) }
func Height(s string) int { return lipgloss.Height(s) }

func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

func JoinHorizontal(pos Position, strs ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Position(pos), strs...)
}

func JoinVertical(pos Position, strs ...string) string {
	return lipgloss.JoinVertical(lipgloss.Position(pos), strs...)
}

func GradientCanvas(width, height int, content string) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = gradientLine(lines[i], width, gradientBackground(i, height))
	}
	return strings.Join(lines, "\n")
}

func gradientBackground(row, height int) string {
	start := [3]int{43, 37, 33}
	end := [3]int{14, 14, 16}
	denominator := max(1, height-1)
	red := start[0] + (end[0]-start[0])*row/denominator
	green := start[1] + (end[1]-start[1])*row/denominator
	blue := start[2] + (end[2]-start[2])*row/denominator
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", red, green, blue)
}

func gradientLine(line string, width int, background string) string {
	line = Truncate(line, width)
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+background)
	line = strings.ReplaceAll(line, "\x1b[49m", "\x1b[49m"+background)
	line += strings.Repeat(" ", max(0, width-Width(line)))
	return background + line + "\x1b[0m"
}

func DimCanvas(width, height int, content string) string {
	return GradientCanvas(width, height, NewStyle().Foreground("240").Render(ansi.Strip(content)))
}

func Overlay(background, foreground string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	backgroundLines := strings.Split(Place(width, height, Left, Top, background), "\n")
	foregroundLines := strings.Split(foreground, "\n")
	foregroundWidth := min(width, Width(foreground))
	foregroundHeight := min(height, Height(foreground))
	x := max(0, (width-foregroundWidth)/2)
	y := max(0, (height-foregroundHeight)/2)
	for i := 0; i < foregroundHeight; i++ {
		row := y + i
		line := backgroundLines[row]
		left := exactWidth(ansi.Cut(line, 0, x), x)
		center := exactWidth(ansi.Cut(foregroundLines[i], 0, foregroundWidth), foregroundWidth)
		right := exactWidth(ansi.Cut(line, x+foregroundWidth, width), width-x-foregroundWidth)
		backgroundLines[row] = left + center + right
	}
	return strings.Join(backgroundLines, "\n")
}

func exactWidth(line string, width int) string {
	line = ansi.Truncate(line, width, "")
	return line + strings.Repeat(" ", max(0, width-Width(line)))
}
