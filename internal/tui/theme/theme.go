package theme

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type Palette struct {
	ActiveTab   lipgloss.Style
	InactiveTab lipgloss.Style
	Selected    lipgloss.Style
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
			Selected:    lipgloss.NewStyle().Bold(true),
			Error:       lipgloss.NewStyle().Bold(true),
			Muted:       lipgloss.NewStyle(),
			Accent:      lipgloss.NewStyle().Bold(true),
			Border:      border,
		}
	}

	accent := lipgloss.Color("#DC2626")
	inactiveFg := lipgloss.Color("#9CA3AF")
	errFg := lipgloss.Color("#F87171")
	mutedFg := lipgloss.Color("#6B7280")
	activeFg := lipgloss.Color("#FFFFFF")

	if isLight {
		accent = lipgloss.Color("#B91C1C")
		inactiveFg = lipgloss.Color("#4B5563")
		errFg = lipgloss.Color("#991B1B")
		mutedFg = lipgloss.Color("#6B7280")
		activeFg = lipgloss.Color("#FFFFFF")
	}

	if strings.EqualFold(colorDepth, "16") {
		accent = lipgloss.Color("1")
		inactiveFg = lipgloss.Color("8")
		errFg = lipgloss.Color("9")
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
		Selected: lipgloss.NewStyle().
			Bold(true).
			Foreground(accent),
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
