package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"local-ai-studio-tui/internal/llm"
)

type ChatConfig struct {
	SystemPrompt string  `toml:"system_prompt"`
	Temperature  float64 `toml:"temperature"`
	TopP         float64 `toml:"top_p"`
	MaxTokens    int     `toml:"max_tokens"`
}

type ImageConfig struct {
	Steps      int     `toml:"steps"`
	CFGScale   float64 `toml:"cfg_scale"`
	Width      int     `toml:"width"`
	Height     int     `toml:"height"`
	Sampler    string  `toml:"sampler"`
	BatchCount int     `toml:"batch_count"`
}

type UIConfig struct {
	Theme        string `toml:"theme"`
	ASCII        bool   `toml:"ascii"`
	ReduceMotion bool   `toml:"reduce_motion"`
	ImageMode    string `toml:"image_mode"`
}

type Config struct {
	Chat  ChatConfig  `toml:"chat"`
	Image ImageConfig `toml:"image"`
	UI    UIConfig    `toml:"ui"`
}

func DefaultConfig() Config {
	return Config{
		Chat: ChatConfig{
			SystemPrompt: "Você é um assistente técnico útil, direto e preciso.",
			Temperature:  0.7,
			TopP:         0.9,
			MaxTokens:    1024,
		},
		Image: ImageConfig{
			Steps:      20,
			CFGScale:   7.0,
			Width:      512,
			Height:     512,
			Sampler:    "euler",
			BatchCount: 1,
		},
		UI: UIConfig{
			Theme:        "auto",
			ASCII:        false,
			ReduceMotion: false,
			ImageMode:    "auto",
		},
	}
}

func ResolveDataDir(flagDir, envDir, exePath string) string {
	if strings.TrimSpace(flagDir) != "" {
		return filepath.Clean(flagDir)
	}
	if strings.TrimSpace(envDir) != "" {
		return filepath.Clean(envDir)
	}
	if strings.TrimSpace(exePath) != "" {
		return filepath.Join(filepath.Dir(exePath), "studio-data")
	}
	return "studio-data"
}

func EnsureLayout(dataDir string) error {
	dirs := []string{
		dataDir,
		filepath.Join(dataDir, "bin"),
		filepath.Join(dataDir, "models", "llm"),
		filepath.Join(dataDir, "models", "checkpoints"),
		filepath.Join(dataDir, "conversations"),
		filepath.Join(dataDir, "outputs"),
		filepath.Join(dataDir, "cache"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("falha ao criar diretório %s: %w", d, err)
		}
	}
	return nil
}

func ResetRuntimeDirs(dataDir string) error {
	for _, sub := range []string{"bin", "cache"} {
		target := filepath.Join(dataDir, sub)
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("falha ao limpar %s: %w", sub, err)
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("falha ao recriar %s: %w", sub, err)
		}
	}
	return nil
}

func LoadOrCreateConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if saveErr := SaveConfig(path, cfg); saveErr != nil {
				return cfg, saveErr
			}
			return cfg, nil
		}
		return cfg, err
	}
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return DefaultConfig(), fmt.Errorf("config.toml inválido: %w", err)
	}
	if cfg.Chat.MaxTokens <= 0 {
		cfg.Chat.MaxTokens = DefaultConfig().Chat.MaxTokens
	}
	if cfg.Image.Steps <= 0 {
		cfg.Image.Steps = DefaultConfig().Image.Steps
	}
	if cfg.Image.Width <= 0 {
		cfg.Image.Width = DefaultConfig().Image.Width
	}
	if cfg.Image.Height <= 0 {
		cfg.Image.Height = DefaultConfig().Image.Height
	}
	if cfg.Image.BatchCount <= 0 {
		cfg.Image.BatchCount = DefaultConfig().Image.BatchCount
	}
	return cfg, nil
}

func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

type Conversation struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Messages  []llm.Message  `json:"messages"`
}

type conversationMetaLine struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type conversationMsgLine struct {
	Type    string       `json:"type"`
	Message llm.Message  `json:"message"`
}

type ConversationStore struct {
	dir string
}

func NewConversationStore(dir string) *ConversationStore {
	return &ConversationStore{dir: dir}
}

func (s *ConversationStore) filePath(id string) string {
	cleanID := filepath.Base(id)
	if !strings.HasSuffix(cleanID, ".jsonl") {
		cleanID += ".jsonl"
	}
	return filepath.Join(s.dir, cleanID)
}

func (s *ConversationStore) Save(conv Conversation) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	if strings.TrimSpace(conv.ID) == "" {
		conv.ID = fmt.Sprintf("conv-%d", time.Now().UnixNano())
	}
	if strings.TrimSpace(conv.Title) == "" {
		conv.Title = "Nova conversa"
	}
	now := time.Now().UTC()
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = now
	}
	conv.UpdatedAt = now

	meta := conversationMetaLine{
		Type:      "meta",
		ID:        conv.ID,
		Title:     conv.Title,
		CreatedAt: conv.CreatedAt,
		UpdatedAt: conv.UpdatedAt,
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	var sb strings.Builder
	sb.Write(metaBytes)
	sb.WriteByte('\n')

	for _, m := range conv.Messages {
		msgLine, err := json.Marshal(conversationMsgLine{
			Type:    "message",
			Message: m,
		})
		if err != nil {
			return err
		}
		sb.Write(msgLine)
		sb.WriteByte('\n')
	}

	return os.WriteFile(s.filePath(conv.ID), []byte(sb.String()), 0o644)
}

func (s *ConversationStore) Load(id string) (Conversation, error) {
	raw, err := os.ReadFile(s.filePath(id))
	if err != nil {
		return Conversation{}, err
	}
	var conv Conversation
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			continue
		}
		switch header.Type {
		case "meta":
			var m conversationMetaLine
			if err := json.Unmarshal([]byte(line), &m); err == nil {
				conv.ID = m.ID
				conv.Title = m.Title
				conv.CreatedAt = m.CreatedAt
				conv.UpdatedAt = m.UpdatedAt
			}
		case "message":
			var msgLine conversationMsgLine
			if err := json.Unmarshal([]byte(line), &msgLine); err == nil {
				conv.Messages = append(conv.Messages, msgLine.Message)
			}
		}
	}
	if conv.ID == "" {
		conv.ID = strings.TrimSuffix(filepath.Base(s.filePath(id)), ".jsonl")
	}
	if conv.Title == "" {
		conv.Title = conv.ID
	}
	return conv, sc.Err()
}

func (s *ConversationStore) List() ([]Conversation, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var list []Conversation
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		if conv, err := s.Load(id); err == nil {
			list = append(list, conv)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	return list, nil
}

func (s *ConversationStore) Rename(id, newTitle string) error {
	conv, err := s.Load(id)
	if err != nil {
		return err
	}
	conv.Title = strings.TrimSpace(newTitle)
	return s.Save(conv)
}

func (s *ConversationStore) Delete(id string) error {
	return os.Remove(s.filePath(id))
}
