package add

import (
	"github.com/kuchmenko/workspace/internal/tui"
)

var (
	addTitle = tui.NewStyle().
			Bold(true).
			Foreground(tui.Color("15")).
			Background(tui.Color("6")).
			Padding(0, 1)

	addDim = tui.NewStyle().Foreground(tui.Color("8"))

	addHelp = tui.NewStyle().Foreground(tui.Color("8"))

	addCursor = tui.NewStyle().
			Foreground(tui.Color("6")).
			Bold(true)

	addAccent = tui.NewStyle().
			Foreground(tui.Color("6")).
			Bold(true)

	addErr = tui.NewStyle().
		Foreground(tui.Color("1")).
		Bold(true)

	addCheck = tui.NewStyle().Foreground(tui.Color("2"))

	addChip = tui.NewStyle().Foreground(tui.Color("4"))

	addGroupHdr = tui.NewStyle().
			Foreground(tui.Color("5")).
			Bold(true).
			Underline(true)

	addItemName = tui.NewStyle().Foreground(tui.Color("15"))

	addExists = tui.NewStyle().
			Foreground(tui.Color("3")).
			Bold(true)

	addExistsTag = tui.NewStyle().
			Foreground(tui.Color("3")).
			Italic(true)

	addPreviewName = tui.NewStyle().
			Foreground(tui.Color("14")).
			Bold(true)

	addCursorRow = tui.NewStyle().
			Background(tui.Color("237"))
)
