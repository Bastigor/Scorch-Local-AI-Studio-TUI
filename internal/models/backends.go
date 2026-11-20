package models

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// githubAPIBase pode ser sobrescrito nos testes.
var githubAPIBase = "https://api.github.com"

// Versões fixas usadas apenas quando a API do GitHub não responde
// (limite de requisições, sem rede, etc.). Os nomes foram conferidos nos releases.
const (
	pinnedLlamaTag = "b12550"
	pinnedSDTag    = "master-1106-532d2c1"
	pinnedSDShort  = "532d2c1"
)

type githubReleaseResponse struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// assetSpec descreve como escolher um arquivo dentro dos releases de um repositório.
type assetSpec struct {
	Repo     string
	Include  []string
	Exclude  []string
	Fallback string
}

func isArchiveName(low string) bool {
	return strings.HasSuffix(low, ".zip") || strings.HasSuffix(low, ".tar.gz") || strings.HasSuffix(low, ".tgz")
}

func matchAsset(name string, include, exclude []string) bool {
	low := strings.ToLower(name)
	if !isArchiveName(low) {
		return false
	}
	for _, kw := range include {
		if !strings.Contains(low, strings.ToLower(kw)) {
			return false
		}
	}
	for _, kw := range exclude {
		if strings.Contains(low, strings.ToLower(kw)) {
			return false
		}
	}
	return true
}

// resolveAsset percorre os releases mais recentes (e não só o "latest") até achar
// um que contenha o pacote desejado. O endpoint /releases/latest do llama.cpp
// pode apontar para um release sem binários, por isso ele não é usado.
func resolveAsset(ctx context.Context, spec assetSpec) string {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	apiURL := fmt.Sprintf("%s/repos/%s/releases?per_page=30", githubAPIBase, spec.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return spec.Fallback
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "local-ai-studio-tui")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return spec.Fallback
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return spec.Fallback
	}

	var releases []githubReleaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return spec.Fallback
	}
	for _, rel := range releases {
		if rel.Draft {
			continue
		}
		for _, asset := range rel.Assets {
			if matchAsset(asset.Name, spec.Include, spec.Exclude) {
				return asset.BrowserDownloadURL
			}
		}
	}
	return spec.Fallback
}

// BinaryRelease é um pacote a ser baixado e extraído em studio-data/bin.
type BinaryRelease struct {
	// Name é o nome final do executável em bin/ (ex.: "sd.exe"). Vazio quando
	// o pacote só fornece bibliotecas (ex.: cudart).
	Name string
	// Aliases são nomes aceitos para o executável dentro do pacote
	// (ex.: "sd-cli.exe" nas versões novas do stable-diffusion.cpp).
	Aliases []string
	URL     string
	SHA256  string
}

func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func ghDownload(repo, tag, file string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, file)
}

// DefaultBackendReleases resolve os pacotes de llama.cpp e stable-diffusion.cpp
// adequados ao sistema e ao backend (cpu, vulkan, cuda...).
func DefaultBackendReleases(ctx context.Context, backend string) []BinaryRelease {
	const llamaRepo = "ggml-org/llama.cpp"
	const sdRepo = "leejet/stable-diffusion.cpp"

	llamaExe := exeName("llama-server")
	sdExe := exeName("sd")
	sdAliases := []string{exeName("sd-cli"), exeName("sd")}

	arch := "x64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}

	switch runtime.GOOS {
	case "windows":
		var llama, sd assetSpec
		var extras []BinaryRelease
		switch backend {
		case "cuda":
			llama = assetSpec{llamaRepo, []string{"bin-win", "cuda-12", "x64"}, []string{"cudart", "arm64"},
				ghDownload(llamaRepo, pinnedLlamaTag, "llama-"+pinnedLlamaTag+"-bin-win-cuda-12.4-x64.zip")}
			sd = assetSpec{sdRepo, []string{"bin-win", "cuda12", "x64"}, []string{"cudart"},
				ghDownload(sdRepo, pinnedSDTag, "sd-master-"+pinnedSDShort+"-bin-win-cuda12-x64.zip")}
			extras = append(extras,
				BinaryRelease{URL: resolveAsset(ctx, assetSpec{llamaRepo, []string{"cudart", "win", "cuda-12", "x64"}, nil,
					ghDownload(llamaRepo, pinnedLlamaTag, "cudart-llama-bin-win-cuda-12.4-x64.zip")})},
				BinaryRelease{URL: resolveAsset(ctx, assetSpec{sdRepo, []string{"cudart", "win", "cu12", "x64"}, nil,
					ghDownload(sdRepo, pinnedSDTag, "cudart-sd-bin-win-cu12-x64.zip")})},
			)
		case "vulkan", "rocm":
			llama = assetSpec{llamaRepo, []string{"bin-win", "vulkan", "x64"}, []string{"cudart", "arm64"},
				ghDownload(llamaRepo, pinnedLlamaTag, "llama-"+pinnedLlamaTag+"-bin-win-vulkan-x64.zip")}
			sd = assetSpec{sdRepo, []string{"bin-win", "vulkan", "x64"}, nil,
				ghDownload(sdRepo, pinnedSDTag, "sd-master-"+pinnedSDShort+"-bin-win-vulkan-x64.zip")}
		default:
			llama = assetSpec{llamaRepo, []string{"bin-win", "cpu", "x64"}, []string{"arm64"},
				ghDownload(llamaRepo, pinnedLlamaTag, "llama-"+pinnedLlamaTag+"-bin-win-cpu-x64.zip")}
			sd = assetSpec{sdRepo, []string{"bin-win", "cpu", "x64"}, nil,
				ghDownload(sdRepo, pinnedSDTag, "sd-master-"+pinnedSDShort+"-bin-win-cpu-x64.zip")}
		}
		out := []BinaryRelease{
			{Name: llamaExe, URL: resolveAsset(ctx, llama)},
			{Name: sdExe, Aliases: sdAliases, URL: resolveAsset(ctx, sd)},
		}
		return append(out, extras...)

	case "darwin":
		return []BinaryRelease{
			{Name: llamaExe, URL: resolveAsset(ctx, assetSpec{llamaRepo, []string{"bin-macos", arch}, nil,
				ghDownload(llamaRepo, pinnedLlamaTag, "llama-"+pinnedLlamaTag+"-bin-macos-"+arch+".tar.gz")})},
			{Name: sdExe, Aliases: sdAliases, URL: resolveAsset(ctx, assetSpec{sdRepo, []string{"darwin", arch}, nil, ""})},
		}

	default:
		sdArch := "x86_64"
		if arch == "arm64" {
			sdArch = "aarch64"
		}
		llama := assetSpec{llamaRepo, []string{"bin-ubuntu", arch}, []string{"vulkan", "cuda", "rocm", "sycl", "openvino", "s390x", "zdnn", "snapdragon", "cudart"},
			ghDownload(llamaRepo, pinnedLlamaTag, "llama-"+pinnedLlamaTag+"-bin-ubuntu-"+arch+".tar.gz")}
		sd := assetSpec{sdRepo, []string{"linux", sdArch}, []string{"vulkan", "rocm", "cuda"},
			ghDownload(sdRepo, pinnedSDTag, "sd-master-"+pinnedSDShort+"-bin-Linux-Ubuntu-26.04-"+sdArch+".zip")}
		if backend == "vulkan" {
			llama.Include = []string{"bin-ubuntu", "vulkan", arch}
			llama.Exclude = []string{"cudart"}
			llama.Fallback = ghDownload(llamaRepo, pinnedLlamaTag, "llama-"+pinnedLlamaTag+"-bin-ubuntu-vulkan-"+arch+".tar.gz")
			sd.Include = []string{"linux", sdArch, "vulkan"}
			sd.Exclude = []string{"rocm", "cuda"}
			sd.Fallback = ghDownload(sdRepo, pinnedSDTag, "sd-master-"+pinnedSDShort+"-bin-Linux-Ubuntu-26.04-"+sdArch+"-vulkan.zip")
		}
		return []BinaryRelease{
			{Name: llamaExe, URL: resolveAsset(ctx, llama)},
			{Name: sdExe, Aliases: sdAliases, URL: resolveAsset(ctx, sd)},
		}
	}
}

// BackendBinaryStatus informa o que já existe em bin/.
func (m *Manager) BackendBinaryStatus() (llama bool, sd bool) {
	binDir := filepath.Join(m.dataDir, "bin")
	_, e1 := os.Stat(filepath.Join(binDir, exeName("llama-server")))
	_, e2 := os.Stat(filepath.Join(binDir, exeName("sd")))
	return e1 == nil, e2 == nil
}

// InstallBackendBinaries baixa e extrai llama-server e sd. Pacotes cujo executável
// já está em bin/ não são baixados de novo. onStatus recebe mensagens de progresso.
func (m *Manager) InstallBackendBinaries(ctx context.Context, backend string, onStatus func(string)) error {
	say := func(format string, args ...any) {
		if onStatus != nil {
			onStatus(fmt.Sprintf(format, args...))
		}
	}

	binDir := filepath.Join(m.dataDir, "bin")
	cacheDir := filepath.Join(m.dataDir, "cache")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}

	say("Consultando versões disponíveis no GitHub...")
	releases := DefaultBackendReleases(ctx, backend)

	for _, rel := range releases {
		if rel.URL == "" {
			label := rel.Name
			if label == "" {
				label = "bibliotecas"
			}
			return fmt.Errorf("nenhum pacote compatível encontrado para %s (%s/%s)", label, runtime.GOOS, runtime.GOARCH)
		}
		if rel.Name != "" {
			if _, err := os.Stat(filepath.Join(binDir, rel.Name)); err == nil {
				say("%s já instalado, pulando.", rel.Name)
				continue
			}
		}

		label := rel.Name
		if label == "" {
			label = filepath.Base(rel.URL)
		}
		archivePath := filepath.Join(cacheDir, filepath.Base(rel.URL))
		_ = os.Remove(archivePath)

		err := m.downloadFileRaw(ctx, rel.URL, archivePath, rel.SHA256, func(done, total int64, bps float64, _ int) {
			if total > 0 {
				say("Baixando %s: %.1f%% (%.1f / %.1f MB, %.1f MB/s)", label,
					float64(done)*100/float64(total), float64(done)/1048576, float64(total)/1048576, bps/1048576)
			} else {
				say("Baixando %s: %.1f MB", label, float64(done)/1048576)
			}
		})
		if err != nil {
			return fmt.Errorf("falha ao baixar %s (%s): %w", label, rel.URL, err)
		}

		say("Extraindo %s...", label)
		targets := rel.Aliases
		if len(targets) == 0 && rel.Name != "" {
			targets = []string{rel.Name}
		}
		if err := extractFromArchive(archivePath, binDir, rel.Name, targets); err != nil {
			return fmt.Errorf("falha ao extrair %s: %w", label, err)
		}
		_ = os.Remove(archivePath)
	}
	say("Binários instalados.")
	return nil
}

func isRuntimeLib(base string) bool {
	low := strings.ToLower(base)
	return strings.HasSuffix(low, ".dll") || strings.Contains(low, ".so") ||
		strings.HasSuffix(low, ".dylib") || strings.HasSuffix(low, ".metal")
}

// extractFromArchive extrai o executável (renomeando para destName quando
// necessário) e todas as bibliotecas de runtime para destDir.
// Se destName estiver vazio, extrai apenas as bibliotecas.
func extractFromArchive(archivePath, destDir, destName string, targets []string) error {
	var entries []archiveEntry
	var closeFn func()
	var err error

	low := strings.ToLower(archivePath)
	if strings.HasSuffix(low, ".zip") {
		entries, closeFn, err = openZipEntries(archivePath)
	} else {
		entries, closeFn, err = openTarGzEntries(archivePath)
	}
	if err != nil {
		return err
	}
	defer closeFn()

	// Escolhe o executável respeitando a ordem de preferência dos aliases.
	chosen := ""
	for _, want := range targets {
		for _, e := range entries {
			if strings.EqualFold(filepath.Base(e.name), want) {
				chosen = e.name
				break
			}
		}
		if chosen != "" {
			break
		}
	}
	if destName != "" && chosen == "" {
		return fmt.Errorf("executável %s não encontrado dentro do pacote %s", strings.Join(targets, " ou "), filepath.Base(archivePath))
	}

	for _, e := range entries {
		base := filepath.Base(e.name)
		isExe := chosen != "" && e.name == chosen
		if !isExe && !isRuntimeLib(base) {
			continue
		}
		outName := base
		if isExe {
			outName = destName
		}
		dest, err := SafeResolveUnder(destDir, outName)
		if err != nil {
			continue
		}
		if err := e.write(dest); err != nil {
			return err
		}
	}
	return nil
}

type archiveEntry struct {
	name  string
	write func(dest string) error
}

func writeStream(dest string, r io.Reader) error {
	_ = os.Remove(dest) // evita falha se o arquivo antigo estiver em uso/somente leitura
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, r)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func openZipEntries(path string) ([]archiveEntry, func(), error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, func() {}, err
	}
	var out []archiveEntry
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		f := f
		out = append(out, archiveEntry{name: f.Name, write: func(dest string) error {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			return writeStream(dest, rc)
		}})
	}
	return out, func() { _ = r.Close() }, nil
}

// openTarGzEntries lê o .tar.gz uma vez e guarda o conteúdo dos arquivos úteis
// em arquivos temporários, já que tar não permite acesso aleatório.
func openTarGzEntries(path string) ([]archiveEntry, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, func() {}, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, func() {}, err
	}
	defer gz.Close()

	tmpDir, err := os.MkdirTemp(filepath.Dir(path), "extract-")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	var out []archiveEntry
	type pendingLink struct{ name, target string }
	var links []pendingLink
	tr := tar.NewReader(gz)
	i := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			links = append(links, pendingLink{name: hdr.Name, target: filepath.Base(hdr.Linkname)})
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		tmp := filepath.Join(tmpDir, fmt.Sprintf("%d.bin", i))
		i++
		if err := writeStream(tmp, tr); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		tmpCopy := tmp
		out = append(out, archiveEntry{name: hdr.Name, write: func(dest string) error {
			in, err := os.Open(tmpCopy)
			if err != nil {
				return err
			}
			defer in.Close()
			return writeStream(dest, in)
		}})
		_ = base
	}
	// Links simbólicos (ex.: libllama-common.so.1 -> libllama-common.so.0.0.1) viram
	// cópias do arquivo de destino, já que o destino final pode não suportar links.
	for _, l := range links {
		for _, e := range out {
			if filepath.Base(e.name) == l.target {
				out = append(out, archiveEntry{name: l.name, write: e.write})
				break
			}
		}
	}
	return out, cleanup, nil
}
