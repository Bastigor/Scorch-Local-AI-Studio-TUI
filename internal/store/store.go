package store

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"local-ai-studio-tui/internal/llm"
)

type Config struct {
	UI struct {
		Theme        string `toml:"theme"`
		ASCII        bool   `toml:"ascii"`
		ReduceMotion bool   `toml:"reduce_motion"`
		ImageMode    string `toml:"image_mode"`
		Mouse        bool   `toml:"mouse"`
	} `toml:"ui"`
	Chat struct {
		SystemPrompt string  `toml:"system_prompt"`
		Temperature  float64 `toml:"temperature"`
		TopP         float64 `toml:"top_p"`
		MaxTokens    int     `toml:"max_tokens"`
	} `toml:"chat"`
}

func DefaultConfig() Config {
	var c Config
	c.UI.Theme = "dark"
	c.UI.ImageMode = "auto"
	c.UI.Mouse = true
	c.Chat.SystemPrompt = "Você é um assistente de engenharia de software conciso e preciso."
	c.Chat.Temperature = 0.7
	c.Chat.TopP = 0.9
	c.Chat.MaxTokens = 2048
	return c
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
			return err
		}
	}
	return nil
}

func LoadOrCreateConfig(path string) (Config, error) {
	if raw, err := os.ReadFile(path); err == nil {
		cfg := DefaultConfig()
		if err := toml.Unmarshal(raw, &cfg); err != nil {
			return cfg, err
		}
		return cfg, nil
	}
	cfg := DefaultConfig()
	raw, err := toml.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	return cfg, os.WriteFile(path, raw, 0o644)
}

type Conversation struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	UpdatedAt time.Time     `json:"updated_at"`
	Messages  []llm.Message `json:"messages"`
}

type jsonlHeader struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
}

type jsonlMessageRow struct {
	Type    string      `json:"type"`
	Message llm.Message `json:"message"`
}

type ConversationStore struct {
	dir string
}

func NewConversationStore(dir string) *ConversationStore {
	_ = os.MkdirAll(dir, 0o755)
	return &ConversationStore{dir: dir}
}

func (s *ConversationStore) List() ([]Conversation, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []Conversation
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		c, err := s.loadFile(filepath.Join(s.dir, e.Name()))
		if err == nil {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *ConversationStore) loadFile(path string) (Conversation, error) {
	f, err := os.Open(path)
	if err != nil {
		return Conversation{}, err
	}
	defer f.Close()

	var c Conversation
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			continue
		}
		if probe.Type == "meta" {
			var h jsonlHeader
			if json.Unmarshal([]byte(line), &h) == nil {
				c.ID = h.ID
				c.Title = h.Title
				c.UpdatedAt = h.UpdatedAt
			}
		} else if probe.Type == "msg" {
			var row jsonlMessageRow
			if json.Unmarshal([]byte(line), &row) == nil {
				c.Messages = append(c.Messages, row.Message)
			}
		}
	}
	return c, sc.Err()
}

func (s *ConversationStore) Save(c Conversation) error {
	if c.ID == "" {
		c.ID = time.Now().Format("20060102-150405")
	}
	if strings.TrimSpace(c.Title) == "" {
		c.Title = "Nova Conversa"
	}
	c.UpdatedAt = time.Now()

	safeID := filepath.Base(c.ID)
	f, err := os.Create(filepath.Join(s.dir, safeID+".jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	if err := enc.Encode(jsonlHeader{
		Type:      "meta",
		ID:        c.ID,
		Title:     c.Title,
		UpdatedAt: c.UpdatedAt,
	}); err != nil {
		return err
	}
	for _, m := range c.Messages {
		if err := enc.Encode(jsonlMessageRow{
			Type:    "msg",
			Message: m,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationStore) Rename(id, newTitle string) error {
	safeID := filepath.Base(id)
	path := filepath.Join(s.dir, safeID+".jsonl")
	c, err := s.loadFile(path)
	if err != nil {
		return err
	}
	c.Title = strings.TrimSpace(newTitle)
	return s.Save(c)
}

func (s *ConversationStore) Delete(id string) error {
	safeID := filepath.Base(id)
	return os.Remove(filepath.Join(s.dir, safeID+".jsonl"))
}
