package tui

import "charm.land/lipgloss/v2"

// Color palette — warm, modern feel.
var (
	ColorPrimary   = lipgloss.Color("#7C3AED") // violet
	ColorSecondary = lipgloss.Color("#06B6D4") // cyan
	ColorAccent    = lipgloss.Color("#F59E0B") // amber
	ColorSuccess   = lipgloss.Color("#10B981") // emerald
	ColorError     = lipgloss.Color("#EF4444") // red
	ColorMuted     = lipgloss.Color("#6B7280") // gray
	ColorBg        = lipgloss.Color("#1E1B2E") // dark violet bg
	ColorFg        = lipgloss.Color("#E2E8F0") // light slate
	ColorDimFg     = lipgloss.Color("#94A3B8") // dim slate
)

// Shared styles.
var (
	// Logo / header.
	LogoStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary).
			MarginBottom(1)

	// Title bar.
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorFg).
			Background(ColorPrimary).
			Padding(0, 2).
			MarginBottom(1)

	// Subtitle.
	SubtitleStyle = lipgloss.NewStyle().
			Foreground(ColorDimFg).
			Italic(true).
			MarginBottom(1)

	// Active menu item.
	ActiveItemStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true).
			PaddingLeft(2)

	// Normal menu item.
	NormalItemStyle = lipgloss.NewStyle().
			Foreground(ColorFg).
			PaddingLeft(4)

	// Description text under menu items.
	DescStyle = lipgloss.NewStyle().
			Foreground(ColorDimFg).
			PaddingLeft(4).
			Italic(true)

	// Active description.
	ActiveDescStyle = lipgloss.NewStyle().
			Foreground(ColorSecondary).
			PaddingLeft(2).
			Italic(true)

	// Input label.
	LabelStyle = lipgloss.NewStyle().
			Foreground(ColorAccent).
			Bold(true).
			MarginRight(1)

	// Input field (focused).
	FocusedInputStyle = lipgloss.NewStyle().
				Foreground(ColorPrimary).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary).
				Padding(0, 1).
				Width(50)

	// Input field (blurred).
	BlurredInputStyle = lipgloss.NewStyle().
				Foreground(ColorDimFg).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorMuted).
				Padding(0, 1).
				Width(50)

	// Success badge.
	SuccessBadge = lipgloss.NewStyle().
			Foreground(ColorSuccess).
			Bold(true)

	// Error badge.
	ErrorBadge = lipgloss.NewStyle().
			Foreground(ColorError).
			Bold(true)

	// Status bar at the bottom.
	StatusBarStyle = lipgloss.NewStyle().
			Foreground(ColorDimFg).
			MarginTop(1)

	// Chat bubble — user.
	UserBubbleStyle = lipgloss.NewStyle().
			Foreground(ColorFg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorPrimary).
			Padding(0, 1).
			MarginLeft(4)

	// Chat bubble — assistant.
	AssistantBubbleStyle = lipgloss.NewStyle().
				Foreground(ColorFg).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorSecondary).
				Padding(0, 1)

	// Spinner / loading.
	SpinnerStyle = lipgloss.NewStyle().
			Foreground(ColorAccent)

	// Help text.
	HelpStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			MarginTop(1)

	// Box container.
	BoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorMuted).
			Padding(1, 2)

	// Selected option indicator.
	SelectedDot = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			SetString("● ")

	// Unselected option indicator.
	UnselectedDot = lipgloss.NewStyle().
			Foreground(ColorMuted).
			SetString("○ ")
)

// Logo returns the gleann ASCII art logo.
func Logo() string {
	logo := `
   ▄▄▄  █     ▄▄▄  ▄▄▄  ▄   ▄ ▄   ▄
  █   █ █    █   █ █  █ █▀▄ █ █▀▄ █
  █   ▀ █    █▀▀▀  █▀▀█ █ ▀▄█ █ ▀▄█
  ▀▄▄▄▀ ▀▀▀▀ ▀▀▀▀  ▀  ▀ ▀   ▀ ▀   ▀`

	return LogoStyle.Render(logo)
}
