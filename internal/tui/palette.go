package tui

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
	Title:    NewStyle().Bold(true).Foreground("215"),
	Header:   NewStyle().Foreground("173").Background("235"),
	Footer:   NewStyle().Foreground("240").Background("235"),
	Selected: NewStyle().Foreground("254").Background("236").Bold(true),
	Accent:   NewStyle().Foreground("215").Bold(true),
	Dim:      NewStyle().Foreground("240"),
	Error:    NewStyle().Foreground("1").Bold(true),
	Check:    NewStyle().Foreground("2"),
	Border:   NewStyle().Border(RoundedBorder()).BorderForeground("173").Padding(0, 1),
	Group:    NewStyle().Foreground("182").Bold(true),
	Item:     NewStyle().Foreground("254"),
}

var Cyan = Palette{
	Title:    NewStyle().Bold(true).Foreground("15").Background("6").Padding(0, 1),
	Header:   NewStyle().Foreground("6").Bold(true),
	Footer:   NewStyle().Foreground("8"),
	Selected: NewStyle().Background("237"),
	Accent:   NewStyle().Foreground("6").Bold(true),
	Dim:      NewStyle().Foreground("8"),
	Error:    NewStyle().Foreground("1").Bold(true),
	Check:    NewStyle().Foreground("2"),
	Border:   NewStyle().Border(RoundedBorder()).BorderForeground("6").Padding(0, 1),
	Group:    NewStyle().Foreground("5").Bold(true).Underline(true),
	Item:     NewStyle().Foreground("15"),
}
