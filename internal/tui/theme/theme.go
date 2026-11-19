package theme

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

var (
	ColorAccent = lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{TrueColor: "#9E1B1E", ANSI256: "124", ANSI: "1"},
		Dark:  lipgloss.CompleteColor{TrueColor: "#E5383B", ANSI256: "196", ANSI: "9"},
	}
	ColorAccentText = lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{TrueColor: "#FFFFFF", ANSI256: "231", ANSI: "15"},
		Dark:  lipgloss.CompleteColor{TrueColor: "#FFFFFF", ANSI256: "231", ANSI: "15"},
	}
	ColorPanelBg = lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{TrueColor: "#F4F1EA", ANSI256: "255", ANSI: "7"},
		Dark:  lipgloss.CompleteColor{TrueColor: "#16181D", ANSI256: "234", ANSI: "0"},
	}
	ColorBorder = lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{TrueColor: "#B8B2A6", ANSI256: "249", ANSI: "8"},
		Dark:  lipgloss.CompleteColor{TrueColor: "#3A3F4B", ANSI256: "238", ANSI: "8"},
	}
	ColorMuted = lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{TrueColor: "#5C5852", ANSI256: "241", ANSI: "8"},
		Dark:  lipgloss.CompleteColor{TrueColor: "#9BA1AE", ANSI256: "246", ANSI: "7"},
	}
	ColorSuccess = lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{TrueColor: "#1E7E34", ANSI256: "28", ANSI: "2"},
		Dark:  lipgloss.CompleteColor{TrueColor: "#4ADE80", ANSI256: "83", ANSI: "10"},
	}
)

type Theme struct {
	Light        bool
	ASCII        bool
	NoColor      bool
	SuccessMark  string
	ErrorMark    string
	HeaderTab    lipgloss.Style
	ActiveTab    lipgloss.Style
	Panel        lipgloss.Style
	AccentBorder lipgloss.Style
	ErrorBlock   lipgloss.Style
	SuccessText  lipgloss.Style
	MutedText    lipgloss.Style
	SelectedItem lipgloss.Style
	StatusBar    lipgloss.Style
}

func New(light, ascii bool) Theme {
	noColor := os.Getenv("NO_COLOR") != ""
	lipgloss.SetHasDarkBackground(!light)

	border := lipgloss.RoundedBorder()
	if ascii {
		border = lipgloss.ASCIIBorder()
	}

	successMark := "✓"
	errorMark := "✗"
	if ascii {
		successMark = "OK"
		errorMark = "!!"
	}

	t := Theme{
		Light:       light,
		ASCII:       ascii,
		NoColor:     noColor,
		SuccessMark: successMark,
		ErrorMark:   errorMark,
	}

	if noColor {
		t.HeaderTab = lipgloss.NewStyle().Padding(0, 1)
		t.ActiveTab = lipgloss.NewStyle().Padding(0, 1).Bold(true).Reverse(true)
		t.Panel = lipgloss.NewStyle().Border(border).Padding(0, 1)
		t.AccentBorder = lipgloss.NewStyle().Border(border).Bold(true).Padding(0, 1)
		t.ErrorBlock = lipgloss.NewStyle().Border(border).Bold(true).Padding(0, 1)
		t.SuccessText = lipgloss.NewStyle().Bold(true)
		t.MutedText = lipgloss.NewStyle()
		t.SelectedItem = lipgloss.NewStyle().Bold(true).Reverse(true)
		t.StatusBar = lipgloss.NewStyle().Reverse(true).Padding(0, 1)
		return t
	}

	t.HeaderTab = lipgloss.NewStyle().Foreground(ColorMuted).Padding(0, 1)
	t.ActiveTab = lipgloss.NewStyle().Background(ColorAccent).Foreground(ColorAccentText).Bold(true).Padding(0, 1)
	t.Panel = lipgloss.NewStyle().Background(ColorPanelBg).Border(border).BorderForeground(ColorBorder).Padding(0, 1)
	t.AccentBorder = lipgloss.NewStyle().Background(ColorPanelBg).Border(border).BorderForeground(ColorAccent).Padding(0, 1)
	t.ErrorBlock = lipgloss.NewStyle().Background(ColorPanelBg).Border(border).BorderForeground(ColorAccent).Foreground(ColorAccent).Bold(true).Padding(0, 1)
	t.SuccessText = lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true)
	t.MutedText = lipgloss.NewStyle().Foreground(ColorMuted)
	t.SelectedItem = lipgloss.NewStyle().Background(ColorAccent).Foreground(ColorAccentText).Bold(true)
	t.StatusBar = lipgloss.NewStyle().Background(ColorPanelBg).Border(border, true, false, false, false).BorderForeground(ColorBorder).Padding(0, 1)
	return t
}
