package models

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAssetSkipsReleasesWithoutBinaries(t *testing.T) {
	releases := []map[string]any{
		// Release "latest" sem binários (caso real do llama.cpp): deve ser ignorado.
		{"tag_name": "v1.0.0", "assets": []map[string]string{
			{"name": "nightly-tag.txt", "browser_download_url": "http://x/nightly-tag.txt"},
		}},
		{"tag_name": "b100", "assets": []map[string]string{
			{"name": "cudart-llama-bin-win-cuda-12.4-x64.zip", "browser_download_url": "http://x/cudart.zip"},
			{"name": "llama-b100-bin-win-vulkan-arm64.zip", "browser_download_url": "http://x/arm.zip"},
			{"name": "llama-b100-bin-win-vulkan-x64.zip", "browser_download_url": "http://x/vulkan.zip"},
		}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(releases)
	}))
	defer srv.Close()

	old := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = old }()

	got := resolveAsset(context.Background(), assetSpec{
		Repo: "ggml-org/llama.cpp", Include: []string{"bin-win", "vulkan", "x64"},
		Exclude: []string{"cudart", "arm64"}, Fallback: "fallback",
	})
	if got != "http://x/vulkan.zip" {
		t.Fatalf("esperava o pacote vulkan x64, obteve %q", got)
	}
}

func TestResolveAssetFallbackOnAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	old := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = old }()

	got := resolveAsset(context.Background(), assetSpec{Repo: "a/b", Include: []string{"x"}, Fallback: "fallback-url"})
	if got != "fallback-url" {
		t.Fatalf("esperava fallback, obteve %q", got)
	}
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(content))
	}
	_ = zw.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExtractSDCliRenamedToSD(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "sd.zip")
	writeZip(t, zipPath, map[string]string{
		"sd-cli.exe":           "CLI",
		"sd-server.exe":        "SERVER",
		"stable-diffusion.dll": "DLL",
		"ggml.dll":             "GGML",
		"start-server.bat":     "bat",
	})
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0o755)

	if err := extractFromArchive(zipPath, bin, "sd.exe", []string{"sd-cli.exe", "sd.exe"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(bin, "sd.exe"))
	if err != nil || string(b) != "CLI" {
		t.Fatalf("sd.exe deveria ser o sd-cli.exe (err=%v, conteúdo=%q)", err, b)
	}
	for _, f := range []string{"stable-diffusion.dll", "ggml.dll"} {
		if _, err := os.Stat(filepath.Join(bin, f)); err != nil {
			t.Fatalf("biblioteca %s não extraída", f)
		}
	}
	if _, err := os.Stat(filepath.Join(bin, "sd-server.exe")); err == nil {
		t.Fatal("sd-server.exe não deveria ser extraído")
	}
}

func TestExtractMissingExecutableFails(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "x.zip")
	writeZip(t, zipPath, map[string]string{"readme.txt": "x"})
	if err := extractFromArchive(zipPath, dir, "llama-server.exe", []string{"llama-server.exe"}); err == nil {
		t.Fatal("esperava erro quando o executável não está no pacote")
	}
}

func TestExtractTarGz(t *testing.T) {
	dir := t.TempDir()
	tgz := filepath.Join(dir, "llama.tar.gz")

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, content string) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(content))
	}
	add("llama-b1/llama-server", "SRV")
	add("llama-b1/libggml.so", "LIB")
	add("llama-b1/LICENSE", "lic")
	_ = tw.Close()
	_ = gz.Close()
	_ = os.WriteFile(tgz, buf.Bytes(), 0o644)

	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0o755)
	if err := extractFromArchive(tgz, bin, "llama-server", []string{"llama-server"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(bin, "llama-server")); string(b) != "SRV" {
		t.Fatalf("llama-server não extraído corretamente: %q", b)
	}
	if _, err := os.Stat(filepath.Join(bin, "libggml.so")); err != nil {
		t.Fatal("libggml.so não extraída")
	}
	if _, err := os.Stat(filepath.Join(bin, "LICENSE")); err == nil {
		t.Fatal("LICENSE não deveria ser extraído")
	}
}

func TestDownloadWithResumeSkipsInstalledModel(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	item := CatalogItem{ID: "x", Name: "X", Filename: "x.gguf", Kind: KindLLM, URL: "http://127.0.0.1:1/never"}
	llmDir := filepath.Join(dir, "models", "llm")
	_ = os.MkdirAll(llmDir, 0o755)
	_ = os.WriteFile(filepath.Join(llmDir, "x.gguf"), []byte("modelo"), 0o644)

	if _, ok := m.IsInstalled(item); !ok {
		t.Fatal("IsInstalled deveria detectar o arquivo existente")
	}
	// A URL é inválida: se tentasse baixar, retornaria erro.
	if err := m.DownloadWithResume(context.Background(), item, nil); err != nil {
		t.Fatalf("não deveria baixar de novo: %v", err)
	}
}
