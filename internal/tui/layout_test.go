package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func sendKey(m AppModel, k tea.KeyMsg) AppModel {
	u, _ := m.Update(k)
	return u.(AppModel)
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestViewFitsTerminalOnEveryTab(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {100, 30}, {120, 40}, {160, 50}} {
		for tab := TabChat; tab <= TabLogs; tab++ {
			m := newTestAppModel(size[0], size[1])
			m.activeTab = tab
			m.resizeViewports()
			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) > size[1] {
				t.Errorf("%dx%d tab %d: %d linhas excedem a altura", size[0], size[1], tab, len(lines))
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w > size[0] {
					t.Errorf("%dx%d tab %d linha %d: largura %d excede %d", size[0], size[1], tab, i, w, size[0])
					break
				}
			}
		}
	}
}

func TestChatShortcutsWrapToWindowWidth(t *testing.T) {
	items := chatShortcutItems()
	for _, w := range []int{40, 76, 120, 300} {
		for _, l := range wrapItems(items, w, "  ") {
			if lipgloss.Width(l) > w && !strings.Contains(l, "  ") == false {
				t.Fatalf("linha com largura %d excede %d: %q", lipgloss.Width(l), w, l)
			}
		}
	}
	narrow := wrapItems(items, 40, "  ")
	wide := wrapItems(items, 300, "  ")
	if len(narrow) <= len(wide) || len(wide) != 1 {
		t.Fatalf("esperava quebra em janela estreita (%d linhas) e 1 linha em janela larga (%d)", len(narrow), len(wide))
	}
	joined := strings.Join(narrow, " ")
	for _, it := range items {
		if !strings.Contains(joined, it) {
			t.Fatalf("atalho cortado: %q", it)
		}
	}
}

func TestImagesTabNumbersWorkWithoutEditing(t *testing.T) {
	m := newTestAppModel(100, 30)
	m = sendKey(m, runes("2"))
	if m.activeTab != TabImages {
		t.Fatal("deveria ir para Imagens")
	}
	// Navega por vários campos: números continuam trocando de aba.
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyDown})
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyDown})
	m = sendKey(m, runes("3"))
	if m.activeTab != TabModels {
		t.Fatalf("número deveria trocar de aba fora da edição, aba=%v", m.activeTab)
	}
	// Durante a edição, números são texto e Alt+N ainda navega.
	m = sendKey(m, runes("2"))
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.imgEditing {
		t.Fatal("Enter deveria iniciar a edição")
	}
	before := m.imgInputs[m.imgFocusIdx].Value()
	m = sendKey(m, runes("7"))
	if m.activeTab != TabImages || m.imgInputs[m.imgFocusIdx].Value() != before+"7" {
		t.Fatal("durante a edição o número deve ser digitado no campo")
	}
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4"), Alt: true})
	if m.activeTab != TabMonitor || m.imgEditing {
		t.Fatal("Alt+4 deveria trocar de aba e encerrar a edição")
	}
}
