package render

import (
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

type ImageProtocol string

const (
	ProtoKitty  ImageProtocol = "kitty"
	ProtoITerm2 ImageProtocol = "iterm"
	ProtoSixel  ImageProtocol = "sixel"
	ProtoBlocks ImageProtocol = "blocks"
	ProtoNone   ImageProtocol = "none"
)

type Capabilities struct {
	TermEnv            string
	ColorDepth         string
	NoColor            bool
	Cols               int
	Rows               int
	InTmux             bool
	InScreen           bool
	DetectedImageMode  ImageProtocol
	EffectiveImageMode ImageProtocol
}

type TermEnvInput struct {
	Term        string
	TermProgram string
	ColorTerm   string
	NoColor     string
	Tmux        string
	Sty         string
	KittyWindow string
	WTProfile   string
	SixelDA1    bool
	ForceMode   string
	Cols        int
	Rows        int
}

func DetectCapabilities(forceMode string) Capabilities {
	cols, rows := 80, 24
	fd := int(os.Stdout.Fd())
	if w, h, err := term.GetSize(fd); err == nil && w > 0 && h > 0 {
		cols, rows = w, h
	}
	sixelDA1 := probeSixelDeviceAttributes(fd)
	return EvaluateCapabilities(TermEnvInput{
		Term:        os.Getenv("TERM"),
		TermProgram: os.Getenv("TERM_PROGRAM"),
		ColorTerm:   os.Getenv("COLORTERM"),
		NoColor:     os.Getenv("NO_COLOR"),
		Tmux:        os.Getenv("TMUX"),
		Sty:         os.Getenv("STY"),
		KittyWindow: os.Getenv("KITTY_WINDOW_ID"),
		WTProfile:   os.Getenv("WT_SESSION"),
		SixelDA1:    sixelDA1,
		ForceMode:   forceMode,
		Cols:        cols,
		Rows:        rows,
	})
}

// probeSixelDeviceAttributes consulta Primary Device Attributes (\x1b[c) em modo raw
// procurando pelo parâmetro ";4;" (indicação padrão DEC de suporte a Sixel).
func probeSixelDeviceAttributes(fd int) bool {
	if !term.IsTerminal(fd) {
		return false
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return false
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	_, _ = os.Stdout.Write([]byte("\x1b[c"))
	_ = os.Stdin.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	defer func() { _ = os.Stdin.SetReadDeadline(time.Time{}) }()

	buf := make([]byte, 64)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	resp := string(buf[:n])
	return strings.Contains(resp, ";4;") || strings.Contains(resp, ";4c")
}

func EvaluateCapabilities(in TermEnvInput) Capabilities {
	caps := Capabilities{
		TermEnv:  in.Term,
		NoColor:  in.NoColor != "",
		Cols:     in.Cols,
		Rows:     in.Rows,
		InTmux:   in.Tmux != "",
		InScreen: in.Sty != "",
	}

	ct := strings.ToLower(in.ColorTerm)
	tm := strings.ToLower(in.Term)
	switch {
	case caps.NoColor:
		caps.ColorDepth = "none"
	case ct == "truecolor" || ct == "24bit" || strings.Contains(tm, "kitty") || strings.Contains(tm, "wezterm"):
		caps.ColorDepth = "truecolor"
	case strings.Contains(tm, "256color"):
		caps.ColorDepth = "256"
	default:
		caps.ColorDepth = "16"
	}

	tp := strings.ToLower(in.TermProgram)
	detected := ProtoBlocks
	switch {
	case in.KittyWindow != "" || strings.Contains(tm, "kitty") || tp == "ghostty" || tp == "wezterm":
		detected = ProtoKitty
	case tp == "iterm.app":
		detected = ProtoITerm2
	case in.SixelDA1 || in.WTProfile != "" || strings.Contains(tm, "foot") || strings.Contains(tm, "mlterm") || strings.Contains(tm, "sixel"):
		detected = ProtoSixel
	case caps.NoColor:
		detected = ProtoNone
	default:
		detected = ProtoBlocks
	}

	if caps.InScreen && detected != ProtoNone {
		detected = ProtoBlocks
	}

	caps.DetectedImageMode = detected
	caps.EffectiveImageMode = detected

	switch strings.ToLower(in.ForceMode) {
	case "kitty":
		caps.EffectiveImageMode = ProtoKitty
	case "iterm":
		caps.EffectiveImageMode = ProtoITerm2
	case "sixel":
		caps.EffectiveImageMode = ProtoSixel
	case "blocks":
		caps.EffectiveImageMode = ProtoBlocks
	case "none":
		caps.EffectiveImageMode = ProtoNone
	}

	return caps
}
