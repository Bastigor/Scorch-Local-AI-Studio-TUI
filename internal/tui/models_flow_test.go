package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"local-ai-studio-tui/internal/hardware"
	"local-ai-studio-tui/internal/models"
	"local-ai-studio-tui/internal/render"
	"local-ai-studio-tui/internal/store"
)

func TestModelsTabListsAndActivatesInstalledModels(t *testing.T) {
	dir := t.TempDir()
	if err := store.EnsureLayout(dir); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "models", "llm", "meu-modelo.gguf"), []byte("gguf"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "models", "checkpoints", "sd.safetensors"), []byte("sd"), 0o644)

	newModel := func() AppModel {
		m := NewAppModel(AppDeps{
			DataDir:  dir,
			Config:   store.DefaultConfig(),
			Hardware: hardware.Info{SelectedBackend: hardware.BackendCPU},
			TermCaps: render.Capabilities{Cols: 100, Rows: 30, ColorDepth: "truecolor"},
			ModelMgr: models.NewManager(dir),
		})
		m.showSplash = false
		return m
	}

	m := newModel()
	m.selectedLLM = ""
	m = sendKey(m, runes("3"))
	if m.activeTab != TabModels {
		t.Fatal("deveria abrir a aba de modelos")
	}
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if filepath.Base(m.selectedLLM) != "meu-modelo.gguf" {
		t.Fatalf("Enter deveria ativar o LLM listado, ativo=%q", m.selectedLLM)
	}
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyTab})
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if filepath.Base(m.selectedSD) != "sd.safetensors" {
		t.Fatalf("Tab+Enter deveria ativar o checkpoint, ativo=%q", m.selectedSD)
	}

	out := m.View()
	for _, banned := range []string{"Catálogo", "baixar", "Baixando"} {
		if strings.Contains(out, banned) {
			t.Fatalf("a interface não deve mais oferecer download (%q)", banned)
		}
	}

	// Arquivo copiado manualmente aparece ao reabrir a aba / atualizar.
	_ = os.WriteFile(filepath.Join(dir, "models", "llm", "novo.gguf"), []byte("gguf"), 0o644)
	m = sendKey(m, runes("r"))
	if len(m.modelsOfKind(false)) != 2 {
		t.Fatalf("esperava 2 LLMs após atualizar, obteve %d", len(m.modelsOfKind(false)))
	}

	// A seleção persiste para a próxima sessão.
	if m2 := newModel(); m2.selectedLLM != m.selectedLLM || m2.selectedSD != m.selectedSD {
		t.Fatalf("seleção não restaurada: %q/%q vs %q/%q", m2.selectedLLM, m2.selectedSD, m.selectedLLM, m.selectedSD)
	}
}
