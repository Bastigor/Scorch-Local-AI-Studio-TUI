package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"local-ai-studio-tui/internal/hardware"
	"local-ai-studio-tui/internal/render"
	"local-ai-studio-tui/internal/store"
	"local-ai-studio-tui/internal/tui/theme"
)

func newTestAppModel(cols, rows int) AppModel {
	cfg := store.DefaultConfig()
	caps := render.Capabilities{
		Cols:               cols,
		Rows:               rows,
		ColorDepth:         "truecolor",
		DetectedImageMode:  render.ProtoBlocks,
		EffectiveImageMode: render.ProtoBlocks,
	}
	hw := hardware.Info{
		OS:              "windows",
		Arch:            "amd64",
		GPUName:         "Test GPU",
		GPUVendor:       "nvidia",
		SelectedBackend: hardware.BackendCUDA,
		Supported:       true,
	}
	m := NewAppModel(AppDeps{
		DataDir:  ".",
		Config:   cfg,
		Hardware: hw,
		TermCaps: caps,
	})
	m.showSplash = false
	return m
}

func TestAppModelMinimumTerminalSizeGuard(t *testing.T) {
	m := newTestAppModel(60, 20)
	out := m.View()
	if !strings.Contains(out, "80x24") {
		t.Fatalf("expected minimum size warning containing 80x24, got: %q", out)
	}
}

func TestAppModelTabNavigation(t *testing.T) {
	m := newTestAppModel(100, 30)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	am := updated.(AppModel)
	if am.activeTab != TabImages {
		t.Fatalf("expected TabImages, got %v", am.activeTab)
	}

	updated, _ = am.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	am = updated.(AppModel)
	if am.activeTab != TabModels {
		t.Fatalf("expected TabModels, got %v", am.activeTab)
	}

	updated, _ = am.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	am = updated.(AppModel)
	if am.activeTab != TabMonitor {
		t.Fatalf("expected TabMonitor, got %v", am.activeTab)
	}

	updated, _ = am.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	am = updated.(AppModel)
	if am.activeTab != TabLogs {
		t.Fatalf("expected TabLogs, got %v", am.activeTab)
	}
}

func TestThemePaletteVariants(t *testing.T) {
	pDark := theme.PaletteFor("dark", "truecolor", false)
	pLight := theme.PaletteFor("light", "truecolor", false)
	pNoColor := theme.PaletteFor("dark", "none", true)

	if pDark.ActiveTab.Render("Chat") == "" {
		t.Fatal("expected non-empty rendered tab in dark palette")
	}
	if pLight.Error.Render("Erro") == "" {
		t.Fatal("expected non-empty rendered error in light palette")
	}
	if pNoColor.Muted.Render("Status") != "Status" {
		t.Fatalf("expected plain text in no-color palette, got %q", pNoColor.Muted.Render("Status"))
	}
}
