package render

import "testing"

func TestEvaluateCapabilities(t *testing.T) {
	cases := []struct {
		name      string
		in        TermEnvInput
		wantProto ImageProtocol
		wantColor string
	}{
		{
			name:      "Kitty graphics protocol",
			in:        TermEnvInput{Term: "xterm-kitty", KittyWindow: "1", ColorTerm: "truecolor"},
			wantProto: ProtoKitty,
			wantColor: "truecolor",
		},
		{
			name:      "iTerm2 inline protocol",
			in:        TermEnvInput{Term: "xterm-256color", TermProgram: "iTerm.app", ColorTerm: "truecolor"},
			wantProto: ProtoITerm2,
			wantColor: "truecolor",
		},
		{
			name:      "Windows Terminal Sixel",
			in:        TermEnvInput{Term: "xterm-256color", WTProfile: "sess-1", ColorTerm: "truecolor"},
			wantProto: ProtoSixel,
			wantColor: "truecolor",
		},
		{
			name:      "Generic SSH falls back to half-blocks",
			in:        TermEnvInput{Term: "xterm-256color", ColorTerm: "truecolor"},
			wantProto: ProtoBlocks,
			wantColor: "truecolor",
		},
		{
			name:      "NO_COLOR disables color and image preview",
			in:        TermEnvInput{Term: "xterm-256color", NoColor: "1"},
			wantProto: ProtoNone,
			wantColor: "none",
		},
		{
			name:      "Force flag overrides detection",
			in:        TermEnvInput{Term: "xterm-kitty", ForceMode: "blocks"},
			wantProto: ProtoBlocks,
			wantColor: "truecolor",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateCapabilities(tc.in)
			if got.EffectiveImageMode != tc.wantProto {
				t.Fatalf("got proto %s, want %s", got.EffectiveImageMode, tc.wantProto)
			}
			if got.ColorDepth != tc.wantColor {
				t.Fatalf("got color %s, want %s", got.ColorDepth, tc.wantColor)
			}
		})
	}
}
