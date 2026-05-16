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

	// Group header: bright magenta + bold so org names stand out
	// against the muted body. Underline gives a clear visual break
	// between groups in dense lists.
	addGroupHdr = lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")).
			Bold(true).
			Underline(true)

	// Default item-name color for fresh suggestions.
	addItemName = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))

	// "Already cloned" highlight for items that map to a registered
	// project or an unregistered local clone. Yellow so it screams
	// "look at me" without going full red, since picking the row is
	// still allowed (creates a copy after rename).
	addExists = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")).
			Bold(true)

	// Tag suffix that follows the item name, with a slightly dimmer
	// shade so it reads as metadata not part of the name.
	addExistsTag = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")).
			Italic(true)

	// Selection-preview header: bright cyan + bold, distinct from the
	// row's name color so the preview reads as separate panel.
	addPreviewName = lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true)

	// Cursor-row highlight: dark gray background, applied to the
	// entire selected row (padded to terminal width). Lipgloss
	// re-applies the bg around any inner ANSI sequences so chip
	// colors, dim URLs, and the cursor arrow keep their fg styling
	// while the bg stays continuous across the line.
	addCursorRow = lipgloss.NewStyle().
			Background(lipgloss.Color("237"))
)
