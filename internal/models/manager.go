package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ModelKind string

const (
	KindLLM        ModelKind = "llm"
	KindCheckpoint ModelKind = "checkpoint"
)

type CatalogItem struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Filename   string    `json:"filename"`
	Kind       ModelKind `json:"kind"`
	SizeHuman  string    `json:"size_human"`
	URL        string    `json:"url"`
	SHA256     string    `json:"sha256,omitempty"`
	MmprojFile string    `json:"mmproj_file,omitempty"`
	MmprojURL  string    `json:"mmproj_url,omitempty"`
	Starter    bool      `json:"starter,omitempty"`
}

type InstalledModel struct {
	Name       string
	Path       string
	Kind       ModelKind
	SizeBytes  int64
	MmprojPath string
}

type DownloadProgress struct {
	ID         string
	Name       string
	Downloaded int64
	Total      int64
	SpeedBPS   float64
	ETASeconds int
	Done       bool
	Err        error
}

type Manager struct {
	dataDir string
}

func NewManager(dataDir string) *Manager {
	return &Manager{dataDir: dataDir}
}

func DefaultCatalog() []CatalogItem {
	return []CatalogItem{
		{
			ID:        "qwen2.5-coder-1.5b",
			Name:      "Qwen2.5 Coder 1.5B Instruct (Q4_K_M)",
			Filename:  "Qwen2.5-Coder-1.5B-Instruct-Q4_K_M.gguf",
			Kind:      KindLLM,
			SizeHuman: "1.12 GB",
			URL:       "https://huggingface.co/Qwen/Qwen2.5-Coder-1.5B-Instruct-GGUF/resolve/main/qwen2.5-coder-1.5b-instruct-q4_k_m.gguf",
			Starter:   true,
		},
		{
			ID:        "qwen2.5-coder-7b",
			Name:      "Qwen2.5 Coder 7B Instruct (Q4_K_M)",
			Filename:  "Qwen2.5-Coder-7B-Instruct-Q4_K_M.gguf",
			Kind:      KindLLM,
			SizeHuman: "4.68 GB",
			URL:       "https://huggingface.co/Qwen/Qwen2.5-Coder-7B-Instruct-GGUF/resolve/main/qwen2.5-coder-7b-instruct-q4_k_m.gguf",
		},
		{
			ID:        "sd-turbo-fp16",
			Name:      "SD Turbo 1.0 (FP16 Checkpoint)",
			Filename:  "sd_turbo.safetensors",
			Kind:      KindCheckpoint,
			SizeHuman: "3.46 GB",
			URL:       "https://huggingface.co/stabilityai/sd-turbo/resolve/main/sd_turbo.safetensors",
		},
	}
}

func (m *Manager) LoadOrCreateCatalog() ([]CatalogItem, error) {
	catPath := filepath.Join(m.dataDir, "catalog.json")
	if raw, err := os.ReadFile(catPath); err == nil {
		var items []CatalogItem
		if err := json.Unmarshal(raw, &items); err == nil && len(items) > 0 {
			return items, nil
		}
	}
	items := DefaultCatalog()
	raw, _ := json.MarshalIndent(items, "", "  ")
	_ = os.WriteFile(catPath, raw, 0o644)
	return items, nil
}

func ValidateModelFile(path string) (ModelKind, error) {
	base := strings.ToLower(filepath.Base(path))
	ext := strings.ToLower(filepath.Ext(base))

	rejectPatterns := []struct {
		token  string
		reason string
	}{
		{"lora", "arquivo identificado como LoRA (adaptador leve), não um checkpoint completo"},
		{"vae", "arquivo identificado como VAE isolado; selecione um checkpoint completo de difusão"},
		{"controlnet", "arquivo identificado como ControlNet; não pode ser executado como modelo principal"},
		{"control_", "arquivo identificado como ControlNet; não pode ser executado como modelo principal"},
		{"clip", "arquivo identificado como Text Encoder/CLIP isolado"},
		{"t5xxl", "arquivo identificado como Text Encoder T5 isolado"},
		{"mmproj", "arquivo identificado como projetor multimodal (mmproj)"},
	}

	for _, rp := range rejectPatterns {
		if strings.Contains(base, rp.token) {
			return "", fmt.Errorf("modelo rejeitado: %s", rp.reason)
		}
	}

	switch ext {
	case ".gguf":
		return KindLLM, nil
	case ".safetensors", ".ckpt":
		return KindCheckpoint, nil
	default:
		return "", fmt.Errorf("extensão não suportada (%s): utilize .gguf para LLMs ou .safetensors/.ckpt para Stable Diffusion", ext)
	}
}

func SafeResolveUnder(baseDir, userRelPath string) (string, error) {
	if strings.ContainsRune(userRelPath, 0) {
		return "", fmt.Errorf("caminho inválido")
	}
	cleanBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	if realBase, err := filepath.EvalSymlinks(cleanBase); err == nil {
		cleanBase = realBase
	}

	target := filepath.Join(cleanBase, filepath.Clean("/"+userRelPath))
	if realTarget, err := filepath.EvalSymlinks(filepath.Dir(target)); err == nil {
		target = filepath.Join(realTarget, filepath.Base(target))
	}

	rel, err := filepath.Rel(cleanBase, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("acesso negado: tentativa de path traversal detectada (%s)", userRelPath)
	}
	return target, nil
}

func (m *Manager) ImportLocalModel(rawInputPath string) (InstalledModel, error) {
	cleaned := strings.TrimSpace(strings.Trim(rawInputPath, `"'`))
	if cleaned == "" {
		return InstalledModel{}, fmt.Errorf("caminho vazio")
	}
	absSrc, err := filepath.Abs(cleaned)
	if err != nil {
		return InstalledModel{}, err
	}
	realSrc, err := filepath.EvalSymlinks(absSrc)
	if err != nil {
		return InstalledModel{}, fmt.Errorf("arquivo não encontrado: %w", err)
	}
	info, err := os.Stat(realSrc)
	if err != nil || info.IsDir() {
		return InstalledModel{}, fmt.Errorf("caminho inválido ou é um diretório")
	}

	kind, err := ValidateModelFile(realSrc)
	if err != nil {
		return InstalledModel{}, err
	}

	subDir := "llm"
	if kind == KindCheckpoint {
		subDir = "checkpoints"
	}
	destDir := filepath.Join(m.dataDir, "models", subDir)
	_ = os.MkdirAll(destDir, 0o755)

	destPath, err := SafeResolveUnder(destDir, filepath.Base(realSrc))
	if err != nil {
		return InstalledModel{}, err
	}

	if err := os.Symlink(realSrc, destPath); err != nil {
		if copyErr := copyFile(realSrc, destPath); copyErr != nil {
			return InstalledModel{}, fmt.Errorf("falha ao importar modelo: %w", copyErr)
		}
	}

	return InstalledModel{
		Name:      filepath.Base(destPath),
		Path:      destPath,
		Kind:      kind,
		SizeBytes: info.Size(),
	}, nil
}

func (m *Manager) ListInstalled() ([]InstalledModel, error) {
	var out []InstalledModel
	for _, spec := range []struct {
		dir  string
		kind ModelKind
	}{
		{filepath.Join(m.dataDir, "models", "llm"), KindLLM},
		{filepath.Join(m.dataDir, "models", "checkpoints"), KindCheckpoint},
	} {
		entries, err := os.ReadDir(spec.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			full := filepath.Join(spec.dir, e.Name())
			if _, err := ValidateModelFile(full); err != nil {
				continue
			}
			info, _ := e.Info()
			var sz int64
			if info != nil {
				sz = info.Size()
			}
			var mmproj string
			if spec.kind == KindLLM {
				for _, cand := range entries {
					if strings.Contains(strings.ToLower(cand.Name()), "mmproj") && strings.HasSuffix(strings.ToLower(cand.Name()), ".gguf") {
						mmproj = filepath.Join(spec.dir, cand.Name())
						break
					}
				}
			}
			out = append(out, InstalledModel{
				Name:       e.Name(),
				Path:       full,
				Kind:       spec.kind,
				SizeBytes:  sz,
				MmprojPath: mmproj,
			})
		}
	}
	return out, nil
}

func (m *Manager) RenameModel(oldPath, newName string) error {
	dir := filepath.Dir(oldPath)
	dest, err := SafeResolveUnder(dir, newName)
	if err != nil {
		return err
	}
	if _, err := ValidateModelFile(dest); err != nil {
		return err
	}
	return os.Rename(oldPath, dest)
}

func (m *Manager) DeleteModel(path string) (int64, error) {
	modelsRoot := filepath.Join(m.dataDir, "models")
	rel, err := filepath.Rel(modelsRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return 0, fmt.Errorf("caminho fora da pasta de modelos")
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	freed := info.Size()
	return freed, os.Remove(path)
}

func (m *Manager) DownloadWithResume(ctx context.Context, item CatalogItem, onProgress func(DownloadProgress)) error {
	subDir := "llm"
	if item.Kind == KindCheckpoint {
		subDir = "checkpoints"
	}
	destDir := filepath.Join(m.dataDir, "models", subDir)
	_ = os.MkdirAll(destDir, 0o755)

	finalPath, err := SafeResolveUnder(destDir, item.Filename)
	if err != nil {
		return err
	}
	partPath := finalPath + ".part"

	var existing int64
	if st, err := os.Stat(partPath); err == nil {
		existing = st.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return err
	}
	if existing > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existing))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("servidor retornou HTTP %d", resp.StatusCode)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if resp.StatusCode == http.StatusPartialContent {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
		existing = 0
	}

	f, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		return err
	}

	total := existing + resp.ContentLength
	downloaded := existing
	start := time.Now()
	lastEmit := time.Now()
	buf := make([]byte, 64*1024)

	for {
		select {
		case <-ctx.Done():
			_ = f.Close()
			return ctx.Err()
		default:
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				_ = f.Close()
				return wErr
			}
			downloaded += int64(n)
			if onProgress != nil && time.Since(lastEmit) >= 200*time.Millisecond {
				lastEmit = time.Now()
				elapsed := time.Since(start).Seconds()
				var bps float64
				var eta int
				if elapsed > 0 {
					bps = float64(downloaded-existing) / elapsed
					if bps > 0 && total > downloaded {
						eta = int(float64(total-downloaded) / bps)
					}
				}
				onProgress(DownloadProgress{
					ID:         item.ID,
					Name:       item.Name,
					Downloaded: downloaded,
					Total:      total,
					SpeedBPS:   bps,
					ETASeconds: eta,
				})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = f.Close()
			return readErr
		}
	}
	_ = f.Close()

	if item.SHA256 != "" {
		if err := VerifySHA256(partPath, item.SHA256); err != nil {
			return err
		}
	}

	return os.Rename(partPath, finalPath)
}

func VerifySHA256(path, expectedHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(expectedHex)) {
		return fmt.Errorf("checksum SHA-256 inválido: esperado %s, obtido %s", expectedHex, got)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
