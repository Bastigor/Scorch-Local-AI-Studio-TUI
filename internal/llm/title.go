package llm

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const titleSystemPrompt = "Você cria títulos curtos para conversas. Responda somente com o título (no máximo 5 palavras, no mesmo idioma da conversa), sem aspas, sem pontuação final e sem explicações."

func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "..."
}

// CleanTitle normaliza a saída do modelo: primeira linha, sem aspas, sem prefixo
// "Título:" e com tamanho limitado. Retorna "" se não sobrar nada aproveitável.
func CleanTitle(raw string) string {
	raw = strings.TrimSpace(raw)
	// Remove blocos de raciocínio que alguns modelos embutem no texto.
	if i := strings.Index(raw, "</think>"); i >= 0 {
		raw = raw[i+len("</think>"):]
	}
	raw = strings.TrimSpace(raw)
	if i := strings.IndexAny(raw, "\r\n"); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.TrimSpace(raw)
	for _, prefix := range []string{"Título:", "Titulo:", "Title:", "TÍTULO:"} {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, prefix))
	}
	raw = strings.Trim(raw, "\"'“”‘’`*#_ ")
	raw = strings.TrimRight(raw, ".!?:;,")
	raw = strings.TrimSpace(raw)
	if utf8.RuneCountInString(raw) > 40 {
		raw = strings.TrimSpace(string([]rune(raw)[:40]))
	}
	return raw
}

// GenerateTitle pede ao próprio modelo um título curto para a conversa.
func GenerateTitle(ctx context.Context, endpoint, userText, assistantText string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	// "/no_think" desliga o modo de raciocínio dos modelos Qwen3; os demais ignoram.
	prompt := fmt.Sprintf("Pergunta do usuário:\n%s\n\nResposta do assistente:\n%s\n\nEscreva o título da conversa. /no_think",
		clip(userText, 400), clip(assistantText, 600))

	ch := make(chan StreamChunk, 64)
	go StreamChatCompletion(ctx, endpoint, titleSystemPrompt,
		[]Message{{Role: "user", Content: prompt}},
		ChatParams{Temperature: 0.3, TopP: 0.9, MaxTokens: 256}, ch)

	var sb strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			return "", chunk.Err
		}
		sb.WriteString(chunk.Delta)
	}
	title := CleanTitle(sb.String())
	if title == "" {
		return "", fmt.Errorf("o modelo não retornou um título")
	}
	return title, nil
}
