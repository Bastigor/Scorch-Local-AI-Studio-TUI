package store

import (
	"path/filepath"
	"testing"

	"local-ai-studio-tui/internal/llm"
)

func TestConfigAndJSONLConversationPersistence(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureLayout(dir); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadOrCreateConfig(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Chat.MaxTokens <= 0 {
		t.Fatalf("expected positive default MaxTokens, got %d", cfg.Chat.MaxTokens)
	}

	cs := NewConversationStore(filepath.Join(dir, "conversations"))
	conv := Conversation{
		ID:    "conv-1",
		Title: "Teste Inicial",
		Messages: []llm.Message{
			{Role: "user", Content: "olá"},
			{Role: "assistant", Content: "oi"},
		},
	}
	if err := cs.Save(conv); err != nil {
		t.Fatal(err)
	}
	if err := cs.Rename("conv-1", "Teste Renomeado"); err != nil {
		t.Fatal(err)
	}

	list, err := cs.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "Teste Renomeado" || len(list[0].Messages) != 2 {
		t.Fatalf("unexpected conversation list: %+v", list)
	}

	if err := cs.Delete("conv-1"); err != nil {
		t.Fatal(err)
	}
	listAfter, _ := cs.List()
	if len(listAfter) != 0 {
		t.Fatalf("expected 0 conversations after delete, got %d", len(listAfter))
	}
}
