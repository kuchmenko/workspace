package agent

import "github.com/charmbracelet/lipgloss"

// Theme defines the visual palette for the agent canvas. Multiple themes
// are registered and cycled at runtime via the T key.
type Theme struct {
	Name string

	// Highlight-level colors.
	Focused  lipgloss.Color
	NavTop   lipgloss.Color
	NavBack  lipgloss.Color
	NavMore  lipgloss.Color
	Overflow lipgloss.Color
	Ancestor lipgloss.Color
	Bg       lipgloss.Color

	// Edge colors.
	EdgeFocused lipgloss.Color
	EdgeNav     lipgloss.Color
	EdgeBg      lipgloss.Color

	// Action quadrant colors (fixed across themes for muscle memory).
	ActionN lipgloss.Color
	ActionE lipgloss.Color
	ActionS lipgloss.Color
	ActionW lipgloss.Color

	// Header / status chrome.
	HeaderFg lipgloss.Color
	HeaderBg lipgloss.Color
	StatusFg lipgloss.Color
}

// Icon prefixes per NodeKind. Kept in theme.go because they're visual
// presentation, not semantic.
func kindIcon(k NodeKind) string {
	switch k {
	case KindRoot:
		return "⚡"
	case KindWorkspace:
		return "🏠"
	case KindGroup:
		return "📂"
	case KindProject:
		return "📦"
	case KindWorktree:
		return "🌿"
	case KindPortal:
		return "📋"
	case KindOrphan:
		return "👻"
	}
	return ""
}

// ---- built-in themes ----

var themes = []Theme{
	themeCatppuccin,
	themeTokyoNight,
	themeGruvbox,
	themeMonochrome,
}

var themeCatppuccin = Theme{
	Name:        "catppuccin",
	Focused:     lipgloss.Color("219"), // pink
	NavTop:      lipgloss.Color("87"),  // sky
	NavBack:     lipgloss.Color("141"), // lavender
	NavMore:     lipgloss.Color("220"), // yellow
	Overflow:    lipgloss.Color("251"), // overlay
	Ancestor:    lipgloss.Color("147"), // mauve
	Bg:          lipgloss.Color("238"), // surface
	EdgeFocused: lipgloss.Color("219"),
	EdgeNav:     lipgloss.Color("87"),
	EdgeBg:      lipgloss.Color("237"),
	ActionN:     lipgloss.Color("220"),
	ActionE:     lipgloss.Color("76"),
	ActionS:     lipgloss.Color("51"),
	ActionW:     lipgloss.Color("207"),
	HeaderFg:    lipgloss.Color("219"),
	HeaderBg:    lipgloss.Color("235"),
	StatusFg:    lipgloss.Color("244"),
}

var themeTokyoNight = Theme{
	Name:        "tokyo-night",
	Focused:     lipgloss.Color("176"), // magenta
	NavTop:      lipgloss.Color("110"), // blue
	NavBack:     lipgloss.Color("146"), // muted lavender
	NavMore:     lipgloss.Color("186"), // warm yellow
	Overflow:    lipgloss.Color("249"),
	Ancestor:    lipgloss.Color("103"),
	Bg:          lipgloss.Color("236"),
	EdgeFocused: lipgloss.Color("176"),
	EdgeNav:     lipgloss.Color("110"),
	EdgeBg:      lipgloss.Color("235"),
	ActionN:     lipgloss.Color("186"),
	ActionE:     lipgloss.Color("114"),
	ActionS:     lipgloss.Color("110"),
	ActionW:     lipgloss.Color("176"),
	HeaderFg:    lipgloss.Color("176"),
	HeaderBg:    lipgloss.Color("234"),
	StatusFg:    lipgloss.Color("243"),
}

var themeGruvbox = Theme{
	Name:        "gruvbox",
	Focused:     lipgloss.Color("208"), // orange
	NavTop:      lipgloss.Color("142"), // green
	NavBack:     lipgloss.Color("109"), // blue
	NavMore:     lipgloss.Color("214"), // yellow
	Overflow:    lipgloss.Color("246"),
	Ancestor:    lipgloss.Color("132"),
	Bg:          lipgloss.Color("237"),
	EdgeFocused: lipgloss.Color("208"),
	EdgeNav:     lipgloss.Color("142"),
	EdgeBg:      lipgloss.Color("235"),
	ActionN:     lipgloss.Color("214"),
	ActionE:     lipgloss.Color("142"),
	ActionS:     lipgloss.Color("109"),
	ActionW:     lipgloss.Color("175"),
	HeaderFg:    lipgloss.Color("208"),
	HeaderBg:    lipgloss.Color("234"),
	StatusFg:    lipgloss.Color("243"),
}

var themeMonochrome = Theme{
	Name:        "mono",
	Focused:     lipgloss.Color("255"),
	NavTop:      lipgloss.Color("252"),
	NavBack:     lipgloss.Color("248"),
	NavMore:     lipgloss.Color("250"),
	Overflow:    lipgloss.Color("244"),
	Ancestor:    lipgloss.Color("242"),
	Bg:          lipgloss.Color("238"),
	EdgeFocused: lipgloss.Color("255"),
	EdgeNav:     lipgloss.Color("250"),
	EdgeBg:      lipgloss.Color("236"),
	ActionN:     lipgloss.Color("255"),
	ActionE:     lipgloss.Color("253"),
	ActionS:     lipgloss.Color("251"),
	ActionW:     lipgloss.Color("249"),
	HeaderFg:    lipgloss.Color("255"),
	HeaderBg:    lipgloss.Color("234"),
	StatusFg:    lipgloss.Color("242"),
}
