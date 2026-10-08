// Package tui holds the shared lipgloss palette and reusable styles used by
// progress, status, and the equip wizard. Centralising the colours here means
// the whole tool has one place to retune visual identity.
package tui

import "github.com/charmbracelet/lipgloss"

// Colour palette — soft, terminal-safe ANSI 256 picks.
var (
	colorSuccess = lipgloss.Color("42")  // green
	colorInfo    = lipgloss.Color("39")  // cyan-blue
	colorWarn    = lipgloss.Color("214") // orange
	colorError   = lipgloss.Color("160") // red
	colorDim     = lipgloss.Color("245") // mid grey
	colorAccent  = lipgloss.Color("177") // magenta — accents only
)

// Inline level/status styles.
var (
	StyleSuccess = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	StyleInfo    = lipgloss.NewStyle().Foreground(colorInfo)
	StyleWarn    = lipgloss.NewStyle().Foreground(colorWarn).Bold(true)
	StyleError   = lipgloss.NewStyle().Foreground(colorError).Bold(true)
	StyleDim     = lipgloss.NewStyle().Foreground(colorDim)
	StyleAccent  = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
)

// Icons used by the progress logger to mark event severity. Glyphs picked to
// render under default macOS Terminal / iTerm2 fonts.
const (
	IconInfo    = "›"
	IconSuccess = "✓"
	IconWarn    = "!"
	IconError   = "✗"
)
