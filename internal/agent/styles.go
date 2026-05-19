package agent

import "github.com/charmbracelet/lipgloss"

var (
	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("173")).
			Background(lipgloss.Color("235"))

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Background(lipgloss.Color("235"))

	accentBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("215"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("254")).
			Background(lipgloss.Color("236")).
			Bold(true)

	groupStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("182")).
			Bold(true)

	itemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("254"))

	wtStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("108"))

	sessionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("110"))

	badgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	wtStatusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("173"))

	statusMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("215")).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	favoriteStarStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("215"))

	activityAgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240"))

	chipNumberStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	chipNameStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("254")).
			Bold(true)

	flashSearchStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("215")).
				Background(lipgloss.Color("235"))

	flashLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("235")).
			Background(lipgloss.Color("215"))

	flashMatchStyle = lipgloss.NewStyle().
			Underline(true).
			Foreground(lipgloss.Color("215"))

	popupBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("173")).
				Padding(1, 1)

	popupTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("215"))

	popupSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("215")).
				Background(lipgloss.Color("237"))

	popupItemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("254"))

	popupDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	whichKeyBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("173")).
				Padding(0, 1)

	whichKeyTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("215")).
				Bold(true)

	whichKeyKeyStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("215")).
				Bold(true)

	whichKeyDescStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245"))
)
