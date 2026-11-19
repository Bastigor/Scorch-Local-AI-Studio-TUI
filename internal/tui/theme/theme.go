package theme

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type Palette struct {
	ActiveTab   lipgloss.Style
	InactiveTab lipgloss.Style
	Error       lipgloss.Style
	Muted       lipgloss.Style
	Accent      lipgloss.Style
	Border      lipgloss.Border
}

func PaletteFor(themeName string, colorDepth string, ascii bool) Palette {
	noColor := strings.EqualFold(colorDepth, "none") || strings.EqualFold(themeName, "none")
	isLight := strings.EqualFold(themeName, "light")

	border := lipgloss.RoundedBorder()
	if ascii {
		border = lipgloss.Border{
			Top:          "-",
			Bottom:       "-",
			Left:         "|",
			Right:        "|",
			TopLeft:      "+",
			TopRight:     "+",
			BottomLeft:   "+",
			BottomRight:  "+",
			MiddleLeft:   "+",
			MiddleRight:  "+",
			MiddleTop:    "+",
			MiddleBottom: "+",
			Middle:       "+",
		}
	}

	if noColor {
		base := lipgloss.NewStyle().Padding(0, 1)
		return Palette{
			ActiveTab:   base.Copy().Bold(true),
			InactiveTab: base.Copy(),
			Error:       lipgloss.NewStyle().Bold(true),
			Muted:       lipgloss.NewStyle(),
			Accent:      lipgloss.NewStyle().Bold(true),
			Border:      border,
		}
	}

	accent := lipgloss.Color("#7C3AED")
	inactiveFg := lipgloss.Color("#9CA3AF")
	errFg := lipgloss.Color("#EF4444")
	mutedFg := lipgloss.Color("#6B7280")
	activeFg := lipgloss.Color("#FFFFFF")

	if isLight {
		accent = lipgloss.Color("#5B21B6")
		inactiveFg = lipgloss.Color("#4B5563")
		errFg = lipgloss.Color("#B91C1C")
		mutedFg = lipgloss.Color("#6B7280")
		activeFg = lipgloss.Color("#FFFFFF")
	}

	if strings.EqualFold(colorDepth, "16") {
		accent = lipgloss.Color("5")
		inactiveFg = lipgloss.Color("8")
		errFg = lipgloss.Color("1")
		mutedFg = lipgloss.Color("7")
		activeFg = lipgloss.Color("15")
	}

	return Palette{
		ActiveTab: lipgloss.NewStyle().
			Bold(true).
			Foreground(activeFg).
			Background(accent).
			Padding(0, 2),
		InactiveTab: lipgloss.NewStyle().
			Foreground(inactiveFg).
			Padding(0, 2),
		Error: lipgloss.NewStyle().
			Bold(true).
			Foreground(errFg),
		Muted: lipgloss.NewStyle().
			Foreground(mutedFg),
		Accent: lipgloss.NewStyle().
			Bold(true).
			Foreground(accent),
		Border: border,
	}
}
