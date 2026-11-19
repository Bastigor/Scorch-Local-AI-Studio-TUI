package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"local-ai-studio-tui/internal/backend"
	"local-ai-studio-tui/internal/hardware"
	"local-ai-studio-tui/internal/models"
	"local-ai-studio-tui/internal/render"
	"local-ai-studio-tui/internal/store"
	"local-ai-studio-tui/internal/sysmon"
	"local-ai-studio-tui/internal/tui"
	"local-ai-studio-tui/internal/tui/theme"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "check":
			runCheck(os.Args[2:])
			return
		case "reset":
			runReset(os.Args[2:])
			return
		case "serve":
			runServe(os.Args[2:])
			return
		}
	}

	fs := flag.NewFlagSet("studio", flag.ExitOnError)
	dataDirFlag := fs.String("data-dir", "", "Diretório de dados portátil (padrão: ./studio-data)")
	checkFlag := fs.Bool("check", false, "Executa diagnóstico do ambiente e sai")
	asciiFlag := fs.Bool("ascii", false, "Usa apenas caracteres ASCII sem box-drawing Unicode")
	reduceMotionFlag := fs.Bool("reduce-motion", false, "Desabilita animações da interface")
	imageModeFlag := fs.String("image-mode", "auto", "Modo de renderização de imagem: auto|kitty|sixel|iterm|blocks|none")
	lightFlag := fs.Bool("light", false, "Força tema claro")
	_ = fs.Parse(os.Args[1:])

	dataDir := resolveDataDir(*dataDirFlag)

	if *checkFlag {
		printDiagnostics(dataDir, *imageModeFlag)
		return
	}

	if err := store.EnsureLayout(dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "Erro ao inicializar diretório de dados (%s): %v\n", dataDir, err)
		os.Exit(1)
	}

	cfg, err := store.LoadOrCreateConfig(filepath.Join(dataDir, "config.toml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erro ao carregar config.toml: %v\n", err)
		os.Exit(1)
	}

	if *asciiFlag {
		cfg.UI.ASCII = true
	}
	if *reduceMotionFlag || os.Getenv("STUDIO_REDUCE_MOTION") == "1" {
		cfg.UI.ReduceMotion = true
	}
	if *lightFlag {
		cfg.UI.Theme = "light"
	}
	if *imageModeFlag != "auto" {
		cfg.UI.ImageMode = *imageModeFlag
	}

	hw := hardware.Detect()
	termCaps := render.DetectCapabilities(cfg.UI.ImageMode)
	th := theme.New(cfg.UI.Theme == "light", cfg.UI.ASCII)

	mgr := backend.NewManager(dataDir, hw)
	defer mgr.Shutdown()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sigCh
		mgr.Shutdown()
		os.Exit(0)
	}()

	convStore := store.NewConversationStore(filepath.Join(dataDir, "conversations"))
	modelMgr := models.NewManager(dataDir)
	mon := sysmon.NewCollector()

	appModel := tui.NewAppModel(tui.AppDeps{
		DataDir:    dataDir,
		Config:     cfg,
		Hardware:   hw,
		TermCaps:   termCaps,
		Theme:      th,
		BackendMgr: mgr,
		ConvStore:  convStore,
		ModelMgr:   modelMgr,
		SysMon:     mon,
	})

	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if cfg.UI.Mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}

	p := tea.NewProgram(appModel, opts...)
	if _, err := p.Run(); err != nil {
		mgr.Shutdown()
		fmt.Fprintf(os.Stderr, "Erro fatal na TUI: %v\n", err)
		os.Exit(1)
	}
}

func resolveDataDir(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if env := os.Getenv("STUDIO_HOME"); env != "" {
		return env
	}
	exe, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exe), "studio-data")
	}
	return "studio-data"
}

func runCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	dataDirFlag := fs.String("data-dir", "", "Diretório de dados")
	imageModeFlag := fs.String("image-mode", "auto", "Modo de imagem")
	_ = fs.Parse(args)
	printDiagnostics(resolveDataDir(*dataDirFlag), *imageModeFlag)
}

func printDiagnostics(dataDir, imageMode string) {
	hw := hardware.Detect()
	caps := render.DetectCapabilities(imageMode)

	fmt.Println("=== Local AI Studio TUI - Diagnóstico de Ambiente ===")
	fmt.Printf("SO / Arquitetura : %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("Diretório Dados  : %s\n", dataDir)
	fmt.Printf("CPU              : %s (%d cores)\n", hw.CPUName, hw.CPUCores)
	fmt.Printf("RAM Total        : %.2f GB\n", float64(hw.TotalRAMBytes)/(1024*1024*1024))
	fmt.Printf("GPU Detectada    : %s (Vendor: %s, VRAM: %d MB)\n", hw.GPUName, hw.GPUVendor, hw.VRAMMB)
	fmt.Printf("Backend Selecionado: %s\n", hw.SelectedBackend)
	if !hw.Supported {
		fmt.Printf("AVISO            : %s\n", hw.UnsupportedReason)
	}
	if len(hw.MissingPrereqs) > 0 {
		fmt.Println("Pré-requisitos faltantes:")
		for _, p := range hw.MissingPrereqs {
			fmt.Printf("  - %s -> Instale com: %s\n", p.Name, p.InstallHint)
		}
	} else {
		fmt.Println("Pré-requisitos   : OK")
	}
	fmt.Printf("Terminal         : TERM=%s | Color=%s | Size=%dx%d\n", caps.TermEnv, caps.ColorDepth, caps.Cols, caps.Rows)
	fmt.Printf("Multiplexador    : tmux=%v | screen=%v\n", caps.InTmux, caps.InScreen)
	fmt.Printf("Modo de Imagem   : %s (detectado: %s)\n", caps.EffectiveImageMode, caps.DetectedImageMode)
}

func runReset(args []string) {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	dataDirFlag := fs.String("data-dir", "", "Diretório de dados")
	_ = fs.Parse(args)
	dataDir := resolveDataDir(*dataDirFlag)

	targets := []string{
		filepath.Join(dataDir, "bin"),
		filepath.Join(dataDir, "cache"),
	}
	for _, t := range targets {
		if err := os.RemoveAll(t); err != nil {
			fmt.Fprintf(os.Stderr, "Falha ao remover %s: %v\n", t, err)
			os.Exit(1)
		}
		_ = os.MkdirAll(t, 0o755)
	}
	fmt.Printf("Reset concluído em %s (binários e caches limpos; modelos, conversas e outputs preservados).\n", dataDir)
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dataDirFlag := fs.String("data-dir", "", "Diretório de dados")
	portFlag := fs.Int("port", 14200, "Porta local (127.0.0.1)")
	_ = fs.Parse(args)
	dataDir := resolveDataDir(*dataDirFlag)

	hw := hardware.Detect()
	mgr := backend.NewManager(dataDir, hw)
	defer mgr.Shutdown()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"backend": hw.SelectedBackend,
			"active":  mgr.ActiveWorkspace(),
		})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", *portFlag)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	fmt.Printf("Local AI Studio API headless ouvindo exclusivamente em http://%s\n", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "Erro no servidor local: %v\n", err)
		os.Exit(1)
	}
}
