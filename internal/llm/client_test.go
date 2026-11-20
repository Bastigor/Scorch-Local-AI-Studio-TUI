package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamChatCompletionSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Olá\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" mundo\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	ch := make(chan StreamChunk, 10)
	go StreamChatCompletion(
		context.Background(),
		srv.URL,
		"sistema",
		[]Message{{Role: "user", Content: "oi"}},
		ChatParams{Temperature: 0.7, TopP: 0.9, MaxTokens: 128},
		ch,
	)

	var sb strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		sb.WriteString(chunk.Delta)
	}
	if sb.String() != "Olá mundo" {
		t.Fatalf("got %q, want %q", sb.String(), "Olá mundo")
	}
}

func TestStreamChatCompletionReasoningAndServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"pensando\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"resposta\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"contexto excedido\"}}\n\n")
	}))
	defer srv.Close()

	ch := make(chan StreamChunk, 10)
	go StreamChatCompletion(context.Background(), srv.URL, "", []Message{{Role: "user", Content: "oi"}}, ChatParams{MaxTokens: 8}, ch)

	var reasoning, content strings.Builder
	var gotErr error
	for chunk := range ch {
		reasoning.WriteString(chunk.Reasoning)
		content.WriteString(chunk.Delta)
		if chunk.Err != nil {
			gotErr = chunk.Err
		}
	}
	if reasoning.String() != "pensando" || content.String() != "resposta" {
		t.Fatalf("reasoning=%q content=%q", reasoning.String(), content.String())
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "contexto excedido") {
		t.Fatalf("esperava erro do servidor, obteve %v", gotErr)
	}
}
