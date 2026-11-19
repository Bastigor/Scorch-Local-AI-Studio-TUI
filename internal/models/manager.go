type githubReleaseResponse struct {
	Assets []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func resolveLatestGitHubAsset(repo string, keywords []string, fallbackURL string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fallbackURL
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "local-ai-studio-tui")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fallbackURL
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fallbackURL
	}

	var rel githubReleaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return fallbackURL
	}

	for _, asset := range rel.Assets {
		low := strings.ToLower(asset.Name)
		if !strings.HasSuffix(low, ".zip") {
			continue
		}
		matched := true
		for _, kw := range keywords {
			if !strings.Contains(low, strings.ToLower(kw)) {
				matched = false
				break
			}
		}
		if matched {
			return asset.BrowserDownloadURL
		}
	}
	return fallbackURL
}

func DefaultBackendReleases(backend string) []BinaryRelease {
	switch runtime.GOOS {
	case "windows":
		llamaKW := []string{"bin-win", "cpu", "x64"}
		sdKW := []string{"bin-win", "avx2", "x64"}
		llamaFallback := "https://github.com/ggml-org/llama.cpp/releases/download/b4831/llama-b4831-bin-win-avx2-x64.zip"
		sdFallback := "https://github.com/leejet/stable-diffusion.cpp/releases/download/master-312-90784e5/sd-master-90784e5-bin-win-avx2-x64.zip"

		if backend == "cuda" {
			llamaKW = []string{"bin-win", "cuda", "x64"}
			sdKW = []string{"bin-win", "cuda", "x64"}
			llamaFallback = "https://github.com/ggml-org/llama.cpp/releases/download/b4831/llama-b4831-bin-win-cuda-cu12.4-x64.zip"
			sdFallback = "https://github.com/leejet/stable-diffusion.cpp/releases/download/master-312-90784e5/sd-master-90784e5-bin-win-cuda12-x64.zip"
		} else if backend == "vulkan" {
			llamaKW = []string{"bin-win", "vulkan", "x64"}
			sdKW = []string{"bin-win", "vulkan", "x64"}
			llamaFallback = "https://github.com/ggml-org/llama.cpp/releases/download/b4831/llama-b4831-bin-win-vulkan-x64.zip"
			sdFallback = "https://github.com/leejet/stable-diffusion.cpp/releases/download/master-312-90784e5/sd-master-90784e5-bin-win-vulkan-x64.zip"
		}

		llamaURL := resolveLatestGitHubAsset("ggml-org/llama.cpp", llamaKW, "")
		if llamaURL == "" {
			llamaURL = resolveLatestGitHubAsset("ggml-org/llama.cpp", []string{"bin-win", "avx2", "x64"}, llamaFallback)
		}
		sdURL := resolveLatestGitHubAsset("leejet/stable-diffusion.cpp", sdKW, "")
		if sdURL == "" {
			sdURL = resolveLatestGitHubAsset("leejet/stable-diffusion.cpp", []string{"bin-win", "avx2", "x64"}, sdFallback)
		}

		return []BinaryRelease{
			{Name: "llama-server.exe", URL: llamaURL},
			{Name: "sd.exe", URL: sdURL},
		}
	case "darwin":
		return []BinaryRelease{
			{
				Name: "llama-server",
				URL:  resolveLatestGitHubAsset("ggml-org/llama.cpp", []string{"bin-macos", "arm64"}, "https://github.com/ggml-org/llama.cpp/releases/download/b4831/llama-b4831-bin-macos-arm64.zip"),
			},
			{
				Name: "sd",
				URL:  resolveLatestGitHubAsset("leejet/stable-diffusion.cpp", []string{"darwin", "arm64"}, "https://github.com/leejet/stable-diffusion.cpp/releases/download/master-312-90784e5/sd-master-90784e5-bin-Darwin-arm64.zip"),
			},
		}
	default:
		arch := "x64"
		if runtime.GOARCH == "arm64" {
			arch = "arm64"
		}
		llamaKW := []string{"bin-ubuntu", arch}
		if backend == "vulkan" && arch == "x64" {
			llamaKW = []string{"bin-ubuntu", "vulkan", "x64"}
		}
		return []BinaryRelease{
			{
				Name: "llama-server",
				URL:  resolveLatestGitHubAsset("ggml-org/llama.cpp", llamaKW, fmt.Sprintf("https://github.com/ggml-org/llama.cpp/releases/download/b4831/llama-b4831-bin-ubuntu-%s.zip", arch)),
			},
			{
				Name: "sd",
				URL:  resolveLatestGitHubAsset("leejet/stable-diffusion.cpp", []string{"linux", arch}, fmt.Sprintf("https://github.com/leejet/stable-diffusion.cpp/releases/download/master-312-90784e5/sd-master-90784e5-bin-Linux-Ubuntu-%s.zip", arch)),
			},
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
		_ = os.Remove(zipPath)
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

func (m *Manager) downloadFileRaw(ctx context.Context, rawURL, destPath, expectedSHA string, onTick func(downloaded, total int64, bps float64, eta int)) error {
	for attempt := 0; attempt < 2; attempt++ {
		var existing int64
		if st, err := os.Stat(destPath); err == nil {
			existing = st.Size()
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "local-ai-studio-tui")
		if existing > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existing))
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && existing > 0 {
			_ = resp.Body.Close()
			_ = os.Remove(destPath)
			continue
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			_ = resp.Body.Close()
			return fmt.Errorf("servidor retornou HTTP %d", resp.StatusCode)
		}

		flags := os.O_CREATE | os.O_WRONLY
		if resp.StatusCode == http.StatusPartialContent {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
			existing = 0
		}

		f, err := os.OpenFile(destPath, flags, 0o644)
		if err != nil {
			_ = resp.Body.Close()
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
				_ = resp.Body.Close()
				return ctx.Err()
			default:
			}
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, wErr := f.Write(buf[:n]); wErr != nil {
					_ = f.Close()
					_ = resp.Body.Close()
					return wErr
				}
				downloaded += int64(n)
				if onTick != nil && time.Since(lastEmit) >= 200*time.Millisecond {
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
					onTick(downloaded, total, bps, eta)
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = f.Close()
				_ = resp.Body.Close()
				return readErr
			}
		}
		_ = f.Close()
		_ = resp.Body.Close()

		if expectedSHA != "" {
			if err := VerifySHA256(destPath, expectedSHA); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("falha após tentativa de retomada")
}
