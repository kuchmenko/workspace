package agent

import "github.com/charmbracelet/lipgloss"

// Warm amber "command post" palette.
var (
	// Header / footer bars.
	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("173")). // amber dim — breadcrumb
			Background(lipgloss.Color("235"))

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Background(lipgloss.Color("235"))

	// Selection: amber accent bar.
	accentBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("215")) // warm amber ▌

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("254")). // bright text
			Background(lipgloss.Color("236")). // subtle dark bg
			Bold(true)

	// Type colors.
	groupStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("182")). // soft mauve
			Bold(true)

	itemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("254")) // white — primary items

	wtStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("108")) // muted sage — git/branch

	sessionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("110")) // cool steel — history

	badgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")) // subtle

	wtStatusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("173")) // warm amber dim — dirty/ahead indicators

	statusMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("215")). // amber
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	// favoriteStarStyle paints the leading `*` indicator placed
	// before favorited projects in the header section.
	favoriteStarStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("215")) // amber, slightly brighter than section

	// activityAgeStyle is the right-aligned " 2m linux" column on
	// header-section rows.
	activityAgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240"))

	// chipNumberStyle paints the leading "1." part of a header chip.
	// Dimmer than the project name so the eye reads the name first;
	// the digit is still picked up at a glance for hotkey use.
	chipNumberStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	// chipNameStyle paints the project name inside a header chip.
	chipNameStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("254")).
			Bold(true)

	// Flash search.
	flashSearchStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("215")). // amber
				Background(lipgloss.Color("235"))

	flashLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("235")). // dark on amber
			Background(lipgloss.Color("215"))

	flashMatchStyle = lipgloss.NewStyle().
			Underline(true).
			Foreground(lipgloss.Color("215")) // amber underlined match

	// Popup forms.
	popupBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("173")).
				Padding(1, 1)

	popupTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("215")) // amber

	popupSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("215")).
				Background(lipgloss.Color("237"))

	popupItemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("254"))

	popupDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	// Which-key panel.
	whichKeyBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("173")).
				Padding(0, 1)

	whichKeyTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("215")).
				Bold(true)

	whichKeyKeyStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("215")). // amber key
				Bold(true)

	whichKeyDescStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245")) // secondary text
)
