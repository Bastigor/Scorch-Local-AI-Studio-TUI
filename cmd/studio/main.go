package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"local-ai-studio-tui/internal/backend"
	"local-ai-studio-tui/internal/hardware"
	"local-ai-studio-tui/internal/models"
	"local-ai-studio-tui/internal/render"
	"local-ai-studio-tui/internal/store"
	"local-ai-studio-tui/internal/sysmon"
	"local-ai-studio-tui/internal/tui"
)

func main() {
	var (
		dataDirFlag  string
		checkFlag    bool
		themeFlag    string
		asciiFlag    bool
		reduceMotion bool
		imageMode    string
	)

	fs := flag.NewFlagSet("studio", flag.ContinueOnError)
	fs.StringVar(&dataDirFlag, "data-dir", "", "Diretório de dados do estúdio")
	fs.BoolVar(&checkFlag, "check", false, "Executa diagnóstico de ambiente e sai")
	fs.StringVar(&themeFlag, "theme", "", "Tema visual (auto, light, dark)")
	fs.BoolVar(&asciiFlag, "ascii", false, "Usa bordas e símbolos ASCII")
	fs.BoolVar(&reduceMotion, "reduce-motion", false, "Reduz animações")
	fs.StringVar(&imageMode, "image-mode", "", "Modo de prévia de imagem (auto, kitty, iterm, sixel, blocks, none)")

	args := os.Args[1:]
	subcmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		subcmd = strings.ToLower(args[0])
		args = args[1:]
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	exePath, _ := os.Executable()
	dataDir := store.ResolveDataDir(dataDirFlag, os.Getenv("STUDIO_HOME"), exePath)

	if err := store.EnsureLayout(dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "Erro ao preparar diretório de dados: %v\n", err)
		os.Exit(1)
	}

	if subcmd == "reset" {
		if err := store.ResetRuntimeDirs(dataDir); err != nil {
			fmt.Fprintf(os.Stderr, "Erro ao resetar binários e cache: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Diretórios bin/ e cache/ resetados em %s\n", dataDir)
		return
	}

	cfgPath := filepath.Join(dataDir, "config.toml")
	cfg, err := store.LoadOrCreateConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Aviso de configuração: %v\n", err)
	}

	if themeFlag != "" {
		cfg.UI.Theme = themeFlag
	}
	if asciiFlag {
		cfg.UI.ASCII = true
	}
	if reduceMotion {
		cfg.UI.ReduceMotion = true
	}
	if imageMode != "" {
		cfg.UI.ImageMode = imageMode
	}

	hw := hardware.Detect()
	caps := render.DetectCapabilities(cfg.UI.ImageMode)

	if checkFlag || subcmd == "check" {
		printCheckReport(dataDir, hw, caps)
		return
	}

	backendMgr := backend.NewManager(dataDir, hw)
	defer backendMgr.Shutdown()

	modelMgr := models.NewManager(dataDir)
	convStore := store.NewConversationStore(filepath.Join(dataDir, "conversations"))
	collector := sysmon.NewCollector()

	appModel := tui.NewAppModel(tui.AppDeps{
		DataDir:    dataDir,
		Config:     cfg,
		Hardware:   hw,
		TermCaps:   caps,
		BackendMgr: backendMgr,
		ModelMgr:   modelMgr,
		ConvStore:  convStore,
		SysMon:     collector,
	})

	p := tea.NewProgram(appModel, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		backendMgr.Shutdown()
		fmt.Fprintf(os.Stderr, "Erro na execução da TUI: %v\n", err)
		os.Exit(1)
	}
}

func printCheckReport(dataDir string, hw hardware.Info, caps render.Capabilities) {
	fmt.Println("Local AI Studio — Diagnóstico de Ambiente")
	fmt.Printf("  SO / Arq:      %s/%s\n", hw.OS, hw.Arch)
	fmt.Printf("  Data Dir:      %s\n", dataDir)
	fmt.Printf("  CPU:           %s (%d núcleos)\n", hw.CPUName, hw.CPUCores)
	if hw.TotalRAMBytes > 0 {
		fmt.Printf("  RAM Total:     %.1f GB\n", float64(hw.TotalRAMBytes)/(1024*1024*1024))
	}
	fmt.Printf("  GPU:           %s (%s)\n", hw.GPUName, hw.GPUVendor)
	if hw.VRAMMB > 0 {
		fmt.Printf("  VRAM:          %d MB\n", hw.VRAMMB)
	}
	fmt.Printf("  Backend:       %s\n", hw.SelectedBackend)
	fmt.Printf("  Suportado:     %v\n", hw.Supported)
	if !hw.Supported && hw.UnsupportedReason != "" {
		fmt.Printf("  Motivo:        %s\n", hw.UnsupportedReason)
	}
	for _, p := range hw.MissingPrereqs {
		fmt.Printf("  Pré-requisito: %s -> %s\n", p.Name, p.InstallHint)
	}
	fmt.Printf("  Terminal:      %dx%d | Cor: %s | Imagem: %s (detectado: %s)\n",
		caps.Cols, caps.Rows, caps.ColorDepth, caps.EffectiveImageMode, caps.DetectedImageMode)

