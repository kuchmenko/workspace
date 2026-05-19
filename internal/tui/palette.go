package tui

import "github.com/charmbracelet/lipgloss"

type Palette struct {
	Title    Style
	Header   Style
	Footer   Style
	Selected Style
	Accent   Style
	Dim      Style
	Error    Style
	Check    Style
	Border   Style
	Group    Style
	Item     Style
}

var Amber = Palette{
	Title:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("215")),
	Header:   lipgloss.NewStyle().Foreground(lipgloss.Color("173")).Background(lipgloss.Color("235")),
	Footer:   lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Background(lipgloss.Color("235")),
	Selected: lipgloss.NewStyle().Foreground(lipgloss.Color("254")).Background(lipgloss.Color("236")).Bold(true),
	Accent:   lipgloss.NewStyle().Foreground(lipgloss.Color("215")).Bold(true),
	Dim:      lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
	Error:    lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),
	Check:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
	Border:   lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("173")).Padding(0, 1),
	Group:    lipgloss.NewStyle().Foreground(lipgloss.Color("182")).Bold(true),
	Item:     lipgloss.NewStyle().Foreground(lipgloss.Color("254")),
}

var Cyan = Palette{
	Title:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("6")).Padding(0, 1),
	Header:   lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true),
	Footer:   lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	Selected: lipgloss.NewStyle().Background(lipgloss.Color("237")),
	Accent:   lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true),
	Dim:      lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	Error:    lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),
	Check:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
	Border:   lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("6")).Padding(0, 1),
	Group:    lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true).Underline(true),
	Item:     lipgloss.NewStyle().Foreground(lipgloss.Color("15")),
}
