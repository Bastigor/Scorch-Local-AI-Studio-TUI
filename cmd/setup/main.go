// Scorch-Setup instala o "motor de IA" usado pelo Scorch Local AI Studio:
//   - llama-server (llama.cpp): executa os modelos de linguagem (.gguf);
//   - sd (stable-diffusion.cpp): gera imagens a partir de checkpoints.
//
// O aplicativo é apenas a interface; sem esses dois programas ele não consegue
// rodar modelos. Este executável baixa a versão adequada ao seu hardware para a
// pasta studio-data/bin, ao lado dele, sem precisar compilar nada.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"local-ai-studio-tui/internal/hardware"
	"local-ai-studio-tui/internal/models"
	"local-ai-studio-tui/internal/store"
)

func main() {
	var (
		dataDirFlag string
		backendFlag string
		force       bool
		noPause     bool
	)
	fs := flag.NewFlagSet("scorch-setup", flag.ContinueOnError)
	fs.StringVar(&dataDirFlag, "data-dir", "", "Pasta de dados (padrão: studio-data ao lado do executável)")
	fs.StringVar(&backendFlag, "backend", "auto", "Backend de aceleração: auto, cpu, vulkan ou cuda")
	fs.BoolVar(&force, "force", false, "Baixa novamente mesmo que os binários já existam")
	fs.BoolVar(&noPause, "no-pause", false, "Não espera Enter ao terminar")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	code := run(dataDirFlag, backendFlag, force)
	if !noPause {
		fmt.Print("\nPressione Enter para sair...")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	os.Exit(code)
}

func run(dataDirFlag, backendFlag string, force bool) int {
	fmt.Println("Scorch Local AI Studio — Setup")
	fmt.Println(strings.Repeat("=", 34))

	exePath, _ := os.Executable()
	dataDir := store.ResolveDataDir(dataDirFlag, os.Getenv("STUDIO_HOME"), exePath)
	if err := store.EnsureLayout(dataDir); err != nil {
		fmt.Printf("\nErro ao preparar a pasta de dados: %v\n", err)
		return 1
	}
	fmt.Printf("Pasta de dados: %s\n", dataDir)

	hw := hardware.Detect()
	fmt.Printf("CPU: %s\n", hw.CPUName)
	fmt.Printf("GPU: %s\n", hw.GPUName)

	backend := strings.ToLower(strings.TrimSpace(backendFlag))
	switch backend {
	case "", "auto":
		backend = string(hw.SelectedBackend)
		if backend == "" {
			backend = "cpu"
		}
	case "cpu", "vulkan", "cuda":
	default:
		fmt.Printf("\nBackend inválido: %q (use auto, cpu, vulkan ou cuda)\n", backendFlag)
		return 2
	}
	fmt.Printf("Backend escolhido: %s\n\n", backend)

	if force {
		if err := store.ResetRuntimeDirs(dataDir); err != nil {
			fmt.Printf("Erro ao limpar binários antigos: %v\n", err)
			return 1
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mgr := models.NewManager(dataDir)
	lastWasProgress := false
	err := mgr.InstallBackendBinaries(ctx, backend, func(msg string) {
		progress := strings.HasPrefix(msg, "Baixando")
		if progress {
			// Reescreve a mesma linha; espaços extras apagam texto anterior.
			fmt.Printf("\r%-100s", msg)
		} else {
			if lastWasProgress {
				fmt.Println()
			}
			fmt.Println(msg)
		}
		lastWasProgress = progress
	})
	if lastWasProgress {
		fmt.Println()
	}
	if err != nil {
		if ctx.Err() != nil {
			fmt.Println("\nInstalação cancelada.")
			return 130
		}
		fmt.Printf("\nFalha na instalação: %v\n", err)
		fmt.Println("Verifique sua conexão com a internet e execute o Setup novamente.")
		return 1
	}

	llama, sd := mgr.BackendBinaryStatus()
	fmt.Printf("\nllama-server (modelos de linguagem): %s\n", okText(llama))
	fmt.Printf("sd (geração de imagens):             %s\n", okText(sd))
	if !llama || !sd {
		return 1
	}

	fmt.Println("\nPronto! Para usar modelos, copie os arquivos para:")
	fmt.Printf("  %s  (modelos de linguagem .gguf)\n", filepath.Join(dataDir, "models", "llm"))
	fmt.Printf("  %s  (checkpoints de imagem)\n", filepath.Join(dataDir, "models", "checkpoints"))

	offerLaunch(exePath)
	return 0
}

func okText(ok bool) string {
	if ok {
		return "instalado"
	}
	return "NÃO instalado"
}

// offerLaunch oferece abrir o Scorch, se o executável estiver ao lado do Setup.
func offerLaunch(setupPath string) {
	name := "Scorch"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	app := filepath.Join(filepath.Dir(setupPath), name)
	if _, err := os.Stat(app); err != nil {
		return
	}
	fmt.Print("\nAbrir o Scorch agora? [S/n] ")
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "" && answer != "s" && answer != "sim" && answer != "y" {
		return
	}
	cmd := exec.Command(app)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Dir = filepath.Dir(app)
	_ = cmd.Run()
}
