package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadResumeAndSHA256(t *testing.T) {
	payload := []byte("0123456789ABCDEF-payload-de-teste-gguf")
	sum := sha256.Sum256(payload)
	expectedHex := hex.EncodeToString(sum[:])

	var sawRange bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		if strings.HasPrefix(rng, "bytes=") {
			sawRange = true
			startStr := strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-")
			start, _ := strconv.Atoi(startStr)
			if start < 0 || start >= len(payload) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)-start))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[start:])
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	mgr := NewManager(dataDir)
	item := CatalogItem{
		ID:       "test-llm",
		Name:     "Test LLM",
		Filename: "test.gguf",
		Kind:     KindLLM,
		URL:      srv.URL,
		SHA256:   expectedHex,
	}

	// Simula download parcial pré-existente de 10 bytes
	partPath := filepath.Join(dataDir, "models", "llm", "test.gguf.part")
	if err := os.MkdirAll(filepath.Dir(partPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partPath, payload[:10], 0o644); err != nil {
		t.Fatal(err)
	}

	if err := mgr.DownloadWithResume(context.Background(), item, nil); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if !sawRange {
		t.Fatal("expected HTTP Range header on resumed download")
	}

	finalPath := filepath.Join(dataDir, "models", "llm", "test.gguf")
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q, want %q", string(got), string(payload))
	}

	// Testa falha de checksum SHA-256
	badItem := item
	badItem.Filename = "bad.gguf"
	badItem.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := mgr.DownloadWithResume(context.Background(), badItem, nil); err == nil {
		t.Fatal("expected SHA-256 mismatch error")
	}
}
