package add

import "github.com/charmbracelet/lipgloss"

var (
	addTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("6")).
			Padding(0, 1)

	addDim = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	addHelp = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	addCursor = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true)

	addAccent = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true)

	addErr = lipgloss.NewStyle().
		Foreground(lipgloss.Color("1")).
		Bold(true)

	addCheck = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))

	addChip = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))

	addGroupHdr = lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")).
			Bold(true).
			Underline(true)

	addItemName = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))

	addExists = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")).
			Bold(true)

	addExistsTag = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")).
			Italic(true)

	addPreviewName = lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true)

	addCursorRow = lipgloss.NewStyle().
			Background(lipgloss.Color("237"))
)
