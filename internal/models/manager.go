package models

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type BinaryRelease struct {
	Name   string
	URL    string
	SHA256 string
}

func DefaultBackendReleases(backend string) []BinaryRelease {
	switch runtime.GOOS {
	case "windows":
		llamaURL := "https://github.com/ggml-org/llama.cpp/releases/download/b4831/llama-b4831-bin-win-avx2-x64.zip"
		sdURL := "https://github.com/leejet/stable-diffusion.cpp/releases/download/master-312-90784e5/sd-master-90784e5-bin-win-avx2-x64.zip"
		if backend == "cuda" {
			llamaURL = "https://github.com/ggml-org/llama.cpp/releases/download/b4831/llama-b4831-bin-win-cuda-cu12.4-x64.zip"
			sdURL = "https://github.com/leejet/stable-diffusion.cpp/releases/download/master-312-90784e5/sd-master-90784e5-bin-win-cuda12-x64.zip"
		} else if backend == "vulkan" {
			llamaURL = "https://github.com/ggml-org/llama.cpp/releases/download/b4831/llama-b4831-bin-win-vulkan-x64.zip"
			sdURL = "https://github.com/leejet/stable-diffusion.cpp/releases/download/master-312-90784e5/sd-master-90784e5-bin-win-vulkan-x64.zip"
		}
		return []BinaryRelease{
			{Name: "llama-server.exe", URL: llamaURL},
			{Name: "sd.exe", URL: sdURL},
		}
	case "darwin":
		return []BinaryRelease{
			{Name: "llama-server", URL: "https://github.com/ggml-org/llama.cpp/releases/download/b4831/llama-b4831-bin-macos-arm64.zip"},
			{Name: "sd", URL: "https://github.com/leejet/stable-diffusion.cpp/releases/download/master-312-90784e5/sd-master-90784e5-bin-Darwin-arm64.zip"},
		}
	default:
		arch := "x64"
		if runtime.GOARCH == "arm64" {
			arch = "arm64"
		}
		llamaURL := fmt.Sprintf("https://github.com/ggml-org/llama.cpp/releases/download/b4831/llama-b4831-bin-ubuntu-%s.zip", arch)
		sdURL := fmt.Sprintf("https://github.com/leejet/stable-diffusion.cpp/releases/download/master-312-90784e5/sd-master-90784e5-bin-Linux-Ubuntu-%s.zip", arch)
		if backend == "vulkan" && arch == "x64" {
			llamaURL = "https://github.com/ggml-org/llama.cpp/releases/download/b4831/llama-b4831-bin-ubuntu-vulkan-x64.zip"
		}
		return []BinaryRelease{
			{Name: "llama-server", URL: llamaURL},
			{Name: "sd", URL: sdURL},
		}
	}
}

func (m *Manager) InstallBackendBinaries(ctx context.Context, backend string, onStatus func(string)) error {
	binDir := filepath.Join(m.dataDir, "bin")
	cacheDir := filepath.Join(m.dataDir, "cache")
	_ = os.MkdirAll(binDir, 0o755)
	_ = os.MkdirAll(cacheDir, 0o755)

	for _, rel := range DefaultBackendReleases(backend) {
		if onStatus != nil {
			onStatus(fmt.Sprintf("Baixando %s...", rel.Name))
		}
		zipPath := filepath.Join(cacheDir, rel.Name+".zip")
		if err := m.downloadFileRaw(ctx, rel.URL, zipPath, rel.SHA256, nil); err != nil {
			return fmt.Errorf("falha ao baixar %s: %w", rel.Name, err)
		}
		if onStatus != nil {
			onStatus(fmt.Sprintf("Extraindo %s...", rel.Name))
		}
		if err := extractBinaryFromZip(zipPath, binDir, rel.Name); err != nil {
			return fmt.Errorf("falha ao extrair %s: %w", rel.Name, err)
		}
	}
	return nil
}

func extractBinaryFromZip(zipPath, destDir, targetBin string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	found := false
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(f.Name)
		isTarget := strings.EqualFold(base, targetBin)
		isRuntimeLib := strings.HasSuffix(base, ".dll") || strings.Contains(base, ".so") ||
			strings.HasSuffix(base, ".dylib") || strings.HasSuffix(base, ".metal")
		if !isTarget && !isRuntimeLib {
			continue
		}
		destPath, err := SafeResolveUnder(destDir, base)
		if err != nil {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		_ = out.Close()
		_ = rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if isTarget {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("executável %s não encontrado dentro do pacote %s", targetBin, filepath.Base(zipPath))
	}
	return nil
}
