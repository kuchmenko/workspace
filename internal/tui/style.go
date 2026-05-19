package tui

import "github.com/charmbracelet/lipgloss"

type Color string

type Border struct{ inner lipgloss.Border }

func RoundedBorder() Border { return Border{inner: lipgloss.RoundedBorder()} }
func NormalBorder() Border  { return Border{inner: lipgloss.NormalBorder()} }
func ThickBorder() Border   { return Border{inner: lipgloss.ThickBorder()} }

type Style struct{ s lipgloss.Style }

func NewStyle() Style { return Style{s: lipgloss.NewStyle()} }

func (s Style) Render(text string) string { return s.s.Render(text) }
func (s Style) Bold(b bool) Style         { return Style{s: s.s.Bold(b)} }
func (s Style) Italic(b bool) Style       { return Style{s: s.s.Italic(b)} }
func (s Style) Underline(b bool) Style    { return Style{s: s.s.Underline(b)} }
func (s Style) Foreground(c Color) Style  { return Style{s: s.s.Foreground(lipgloss.Color(string(c)))} }
func (s Style) Background(c Color) Style  { return Style{s: s.s.Background(lipgloss.Color(string(c)))} }
func (s Style) Padding(p ...int) Style    { return Style{s: s.s.Padding(p...)} }
func (s Style) Margin(m ...int) Style     { return Style{s: s.s.Margin(m...)} }
func (s Style) Width(w int) Style         { return Style{s: s.s.Width(w)} }
func (s Style) Height(h int) Style        { return Style{s: s.s.Height(h)} }
func (s Style) Align(p Position) Style    { return Style{s: s.s.Align(lipgloss.Position(p))} }
func (s Style) Border(b Border) Style     { return Style{s: s.s.Border(b.inner)} }
func (s Style) BorderForeground(c Color) Style {
	return Style{s: s.s.BorderForeground(lipgloss.Color(string(c)))}
}

type Position float64

const (
	Left   Position = Position(lipgloss.Left)
	Center Position = Position(lipgloss.Center)
	Right  Position = Position(lipgloss.Right)
)

func Width(s string) int  { return lipgloss.Width(s) }
func Height(s string) int { return lipgloss.Height(s) }

func JoinHorizontal(pos Position, strs ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Position(pos), strs...)
}

func JoinVertical(pos Position, strs ...string) string {
	return lipgloss.JoinVertical(lipgloss.Position(pos), strs...)
}
