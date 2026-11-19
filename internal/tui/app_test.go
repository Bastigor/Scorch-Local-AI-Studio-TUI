package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"local-ai-studio-tui/internal/backend"
	"local-ai-studio-tui/internal/hardware"
	"local-ai-studio-tui/internal/models"
	"local-ai-studio-tui/internal/render"
	"local-ai-studio-tui/internal/store"
	"local-ai-studio-tui/internal/sysmon"
	"local-ai-studio-tui/internal/tui/theme"
)

func newTestApp(t *testing.T, ascii, light bool) AppModel {
	dir := t.TempDir()
	_ = store.EnsureLayout(dir)
	cfg := store.DefaultConfig()
	cfg.UI.ASCII = ascii
	if light {
		cfg.UI.Theme = "light"
	}
	hw := hardware.Info{SelectedBackend: hardware.BackendCPU, CPUName: "Test CPU", Supported: true}
	caps := render.TermEnvInput{Cols: 100, Rows: 30}
	th := theme.New(light, ascii)

	m := NewAppModel(AppDeps{
		DataDir:    dir,
		Config:     cfg,
		Hardware:   hw,
		TermCaps:   render.EvaluateCapabilities(caps),
		Theme:      th,
		BackendMgr: backend.NewManager(dir, hw),
		ConvStore:  store.NewConversationStore(dir + "/conversations"),
		ModelMgr:   models.NewManager(dir),
		SysMon:     sysmon.NewCollector(),
	})
	m.showWizard = false
	return m
}

func TestAppNavigationAndModes(t *testing.T) {
	modes := []struct {
		name  string
		ascii bool
		light bool
	}{
		{"dark_unicode", false, false},
		{"light_unicode", false, true},
		{"dark_ascii", true, false},
	}

	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestApp(t, tc.ascii, tc.light)

			// Dispensa splash
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = updated.(AppModel)

			// Percorre todas as 5 abas verificando renderização não vazia
			for tab := 1; tab <= 5; tab++ {
				updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune('0' + tab)}, Alt: true})
				m = updated.(AppModel)
				out := m.View()
				if strings.TrimSpace(out) == "" {
					t.Fatalf("empty view on tab %d", tab)
				}
			}
		})
	}
}

func TestSmallTerminalWarning(t *testing.T) {
	m := newTestApp(t, true, false)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 18})
	m = updated.(AppModel)
	if !strings.Contains(m.View(), "80x24") {
		t.Fatalf("expected 80x24 warning, got: %s", m.View())
	}
}
