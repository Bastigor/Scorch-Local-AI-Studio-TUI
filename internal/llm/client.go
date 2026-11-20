package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	ImagePath string `json:"image_path,omitempty"`
}

type ChatParams struct {
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	MaxTokens   int     `json:"max_tokens"`
}

type StreamChunk struct {
	Delta string
	// Reasoning traz o "pensamento" de modelos de raciocínio (Qwen3, DeepSeek-R1...),
	// que o llama-server envia em reasoning_content, separado da resposta final.
	Reasoning string
	// Status é uma mensagem de andamento (ex.: "carregando modelo..."), sem texto da resposta.
	Status string
	Done   bool
	Err    error
}

func StreamChatCompletion(ctx context.Context, endpoint string, systemPrompt string, history []Message, params ChatParams, out chan<- StreamChunk) {
	defer close(out)

	apiMsgs := make([]map[string]any, 0, len(history)+1)
	if strings.TrimSpace(systemPrompt) != "" {
		apiMsgs = append(apiMsgs, map[string]any{
			"role":    "system",
			"content": systemPrompt,
		})
	}

	for _, m := range history {
		if m.ImagePath != "" {
			raw, err := os.ReadFile(m.ImagePath)
			if err == nil {
				b64 := base64.StdEncoding.EncodeToString(raw)
				apiMsgs = append(apiMsgs, map[string]any{
					"role": m.Role,
					"content": []map[string]any{
						{"type": "text", "text": m.Content},
						{"type": "image_url", "image_url": map[string]string{"url": "data:image/png;base64," + b64}},
					},
				})
				continue
			}
		}
		apiMsgs = append(apiMsgs, map[string]any{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	payload := map[string]any{
		"messages":    apiMsgs,
		"temperature": params.Temperature,
		"top_p":       params.TopP,
		"max_tokens":  params.MaxTokens,
		"stream":      true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		out <- StreamChunk{Err: err, Done: true}
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		out <- StreamChunk{Err: err, Done: true}
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			out <- StreamChunk{Done: true}
			return
		}
		out <- StreamChunk{Err: err, Done: true}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		out <- StreamChunk{Err: fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(errBody)), Done: true}
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			out <- StreamChunk{Done: true}
			return
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			out <- StreamChunk{Done: true}
			return
		}

		var parsed struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			continue
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			out <- StreamChunk{Err: fmt.Errorf("servidor: %s", parsed.Error.Message), Done: true}
			return
		}
		if len(parsed.Choices) == 0 {
			continue
		}
		d := parsed.Choices[0].Delta
		reasoning := d.ReasoningContent
		if reasoning == "" {
			reasoning = d.Reasoning
		}
		if d.Content != "" || reasoning != "" {
			out <- StreamChunk{Delta: d.Content, Reasoning: reasoning}
		}
	}

	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		out <- StreamChunk{Err: err, Done: true}
		return
	}
	out <- StreamChunk{Done: true}
}
