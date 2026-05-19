package agent

import "github.com/kuchmenko/workspace/internal/tui"

var (
	headerStyle    = tui.Amber.Header
	footerStyle    = tui.Amber.Footer
	accentBarStyle = tui.NewStyle().Foreground("215")
	selectedStyle  = tui.Amber.Selected
	groupStyle     = tui.Amber.Group
	itemStyle      = tui.Amber.Item
	dimStyle       = tui.Amber.Dim

	wtStyle       = tui.NewStyle().Foreground("108")
	sessionStyle  = tui.NewStyle().Foreground("110")
	badgeStyle    = tui.NewStyle().Foreground("240")
	wtStatusStyle = tui.NewStyle().Foreground("173")

	statusMsgStyle    = tui.NewStyle().Foreground("215").Bold(true)
	favoriteStarStyle = tui.NewStyle().Foreground("215")
	activityAgeStyle  = tui.NewStyle().Foreground("240")

	chipNumberStyle = tui.NewStyle().Foreground("245")
	chipNameStyle   = tui.NewStyle().Foreground("254").Bold(true)

	flashSearchStyle = tui.NewStyle().Bold(true).Foreground("215").Background("235")
	flashLabelStyle  = tui.NewStyle().Bold(true).Foreground("235").Background("215")
	flashMatchStyle  = tui.NewStyle().Underline(true).Foreground("215")

	popupBorderStyle   = tui.NewStyle().Border(tui.RoundedBorder()).BorderForeground("173").Padding(1, 1)
	popupTitleStyle    = tui.NewStyle().Bold(true).Foreground("215")
	popupSelectedStyle = tui.NewStyle().Bold(true).Foreground("215").Background("237")
	popupItemStyle     = tui.NewStyle().Foreground("254")
	popupDimStyle      = tui.NewStyle().Foreground("240")

	whichKeyBorderStyle = tui.NewStyle().Border(tui.RoundedBorder()).BorderForeground("173").Padding(0, 1)
	whichKeyTitleStyle  = tui.NewStyle().Foreground("215").Bold(true)
	whichKeyKeyStyle    = tui.NewStyle().Foreground("215").Bold(true)
	whichKeyDescStyle   = tui.NewStyle().Foreground("245")
)
