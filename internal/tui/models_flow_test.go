package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"local-ai-studio-tui/internal/hardware"
	"local-ai-studio-tui/internal/models"
	"local-ai-studio-tui/internal/render"
	"local-ai-studio-tui/internal/store"
)

func TestModelsTabDoesNotRedownloadInstalledModel(t *testing.T) {
	dir := t.TempDir()
	if err := store.EnsureLayout(dir); err != nil {
		t.Fatal(err)
	}
	item := models.DefaultCatalog()[0]
	path := filepath.Join(dir, "models", "llm", item.Filename)
	_ = os.WriteFile(path, []byte("gguf"), 0o644)

	m := NewAppModel(AppDeps{
		DataDir:  dir,
		Config:   store.DefaultConfig(),
		Hardware: hardware.Info{SelectedBackend: hardware.BackendCPU},
		TermCaps: render.Capabilities{Cols: 100, Rows: 30, ColorDepth: "truecolor"},
		ModelMgr: models.NewManager(dir),
	})
	m.showSplash, m.showWizard = false, false
	m.selectedLLM = ""
	m.activeTab = TabModels

	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	am := u.(AppModel)
	if cmd != nil {
		t.Fatal("não deveria iniciar download de modelo já instalado")
	}
	if len(am.downloads) != 0 || am.selectedLLM == "" {
		t.Fatalf("esperava ativar o modelo existente (downloads=%d, ativo=%q)", len(am.downloads), am.selectedLLM)
	}

	// A seleção persiste para a próxima sessão.
	m2 := NewAppModel(AppDeps{
		DataDir:  dir,
		Config:   store.DefaultConfig(),
		Hardware: hardware.Info{SelectedBackend: hardware.BackendCPU},
		TermCaps: render.Capabilities{Cols: 100, Rows: 30, ColorDepth: "truecolor"},
		ModelMgr: models.NewManager(dir),
	})
	if m2.selectedLLM != am.selectedLLM {
		t.Fatalf("seleção não restaurada: %q vs %q", m2.selectedLLM, am.selectedLLM)
	}
}
