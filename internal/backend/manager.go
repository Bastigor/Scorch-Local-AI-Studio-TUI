package backend

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"local-ai-studio-tui/internal/hardware"
)

type WorkspaceMode string

const (
	WorkspaceNone  WorkspaceMode = "none"
	WorkspaceChat  WorkspaceMode = "chat"
	WorkspaceImage WorkspaceMode = "image"
)

type LogEntry struct {
	Process string
	Line    string
}

type LogBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
	max     int
}

func NewLogBuffer(max int) *LogBuffer {
	if max <= 0 {
		max = 2000
	}
	return &LogBuffer{
		entries: make([]LogEntry, 0, max),
		max:     max,
	}
}

func (b *LogBuffer) Append(proc, line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.entries) >= b.max {
		copy(b.entries, b.entries[1:])
		b.entries[len(b.entries)-1] = LogEntry{Process: proc, Line: line}
		return
	}
	b.entries = append(b.entries, LogEntry{Process: proc, Line: line})
}

func (b *LogBuffer) Filter(proc string) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]LogEntry, 0, len(b.entries))
	for _, e := range b.entries {
		if proc == "" || proc == "all" || strings.EqualFold(e.Process, proc) {
			out = append(out, e)
		}
	}
	return out
}

func (b *LogBuffer) Tail(proc string, n int) []string {
	filtered := b.Filter(proc)
	if len(filtered) > n {
		filtered = filtered[len(filtered)-n:]
	}
	lines := make([]string, len(filtered))
	for i, e := range filtered {
		lines[i] = e.Line
	}
	return lines
}

type Manager struct {
	mu            sync.Mutex
	dataDir       string
	hw            hardware.Info
	activeMode    WorkspaceMode
	activeModel   string
	activePort    int
	activeCmd     *exec.Cmd
	activeDone    chan struct{}
	activeBackend hardware.BackendType
	logs          *LogBuffer
}

func NewManager(dataDir string, hw hardware.Info) *Manager {
	return &Manager{
		dataDir:       dataDir,
		hw:            hw,
		activeMode:    WorkspaceNone,
		activeBackend: hw.SelectedBackend,
		logs:          NewLogBuffer(2000),
	}
}

func (m *Manager) Logs() *LogBuffer {
	return m.logs
}

func (m *Manager) ActiveWorkspace() WorkspaceMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeMode
}

func (m *Manager) ActiveBackend() hardware.BackendType {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeBackend
}

func (m *Manager) BinaryPath(name string) string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return filepath.Join(m.dataDir, "bin", name+ext)
}

func (m *Manager) HasBinaries() bool {
	_, err1 := os.Stat(m.BinaryPath("llama-server"))
	_, err2 := os.Stat(m.BinaryPath("sd"))
	return err1 == nil && err2 == nil
}

func AllocatePort(preferred int) (int, error) {
	if preferred > 0 {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferred))
		if err == nil {
			_ = ln.Close()
			return preferred, nil
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.Port, nil
}

// healthTimeout é o tempo máximo para o llama-server carregar o modelo.
// Modelos grandes em disco lento podem levar bem mais que alguns segundos.
var healthTimeout = 240 * time.Second

func processAlive(done chan struct{}) bool {
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// StartLlamaServer inicia (ou reaproveita) o llama-server para o modelo informado.
// onStatus, se não nulo, recebe mensagens de andamento.
func (m *Manager) StartLlamaServer(ctx context.Context, modelPath, mmprojPath string, onStatus func(string)) (string, error) {
	say := func(format string, args ...any) {
		if onStatus != nil {
			onStatus(fmt.Sprintf(format, args...))
		}
	}

	m.mu.Lock()
	if m.activeMode == WorkspaceChat && m.activeModel == modelPath && m.activeCmd != nil && processAlive(m.activeDone) {
		ep := fmt.Sprintf("http://127.0.0.1:%d", m.activePort)
		m.mu.Unlock()
		return ep, nil
	}
	m.stopLocked()

	bin := m.BinaryPath("llama-server")
	if _, err := os.Stat(bin); err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("binário llama-server não encontrado em %s. Reabra o aplicativo para baixar os binários ou copie o executável para essa pasta", bin)
	}
	if st, err := os.Stat(modelPath); err != nil || st.IsDir() {
		m.mu.Unlock()
		return "", fmt.Errorf("arquivo do modelo não encontrado: %s", modelPath)
	}

	port, err := AllocatePort(8080)
	if err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("falha ao alocar porta local: %w", err)
	}

	backendsToTry := []hardware.BackendType{m.hw.SelectedBackend}
	for _, b := range m.hw.FallbackBackends {
		if b != m.hw.SelectedBackend {
			backendsToTry = append(backendsToTry, b)
		}
	}
	// Sempre termina tentando CPU puro.
	hasCPU := false
	for _, b := range backendsToTry {
		if b == hardware.BackendCPU {
			hasCPU = true
		}
	}
	if !hasCPU {
		backendsToTry = append(backendsToTry, hardware.BackendCPU)
	}

	var lastErr error
	for _, b := range backendsToTry {
		args := []string{
			"-m", modelPath,
			"--host", "127.0.0.1",
			"--port", fmt.Sprintf("%d", port),
			"-c", "4096",
		}
		if mmprojPath != "" {
			args = append(args, "--mmproj", mmprojPath)
		}
		if b == hardware.BackendCPU {
			args = append(args, "-ngl", "0")
		} else {
			args = append(args, "-ngl", "99")
		}

		say("Iniciando llama-server (%s) e carregando o modelo...", b)
		m.logs.Append("llama-server", fmt.Sprintf("[studio] %s %s", filepath.Base(bin), strings.Join(args, " ")))

		cmd := exec.Command(bin, args...)
		cmd.Dir = filepath.Dir(bin)
		w := &lineWriter{proc: "llama-server", logs: m.logs}
		cmd.Stdout = w
		cmd.Stderr = w

		if err := cmd.Start(); err != nil {
			lastErr = err
			m.logs.Append("llama-server", fmt.Sprintf("[erro] falha ao iniciar com backend %s: %v", b, err))
			continue
		}

		done := make(chan struct{})
		var waitErr error
		go func() {
			waitErr = cmd.Wait()
			w.Flush()
			close(done)
		}()

		m.activeMode = WorkspaceChat
		m.activeModel = modelPath
		m.activePort = port
		m.activeCmd = cmd
		m.activeDone = done
		m.activeBackend = b
		m.mu.Unlock()

		endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
		err := waitHealthy(ctx, endpoint+"/health", healthTimeout, done)

		if err == nil {
			say("Modelo carregado (%s). Gerando resposta...", b)
			return endpoint, nil
		}

		m.mu.Lock()
		tail := strings.Join(m.logs.Tail("llama-server", 8), " | ")
		exitInfo := ""
		if !processAlive(done) {
			exitInfo = fmt.Sprintf(" (processo encerrou: %v)", waitErr)
		}
		m.stopLocked()
		if ctx.Err() != nil {
			m.mu.Unlock()
			return "", ctx.Err()
		}
		lastErr = fmt.Errorf("llama-server falhou com backend %s%s: %v — últimos logs: %s", b, exitInfo, err, tail)
	}

	m.mu.Unlock()
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("nenhum backend disponível conseguiu iniciar o llama-server")
}

func (m *Manager) RunStableDiffusion(ctx context.Context, args []string, onLine func(string)) error {
	m.mu.Lock()
	m.stopLocked()

	bin := m.BinaryPath("sd")
	if _, err := os.Stat(bin); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("binário stable-diffusion (sd) não encontrado em %s", bin)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	if dev := os.Getenv("SD_VULKAN_DEVICE"); dev != "" {
		cmd.Env = append(os.Environ(), "GGML_VK_VISIBLE_DEVICES="+dev)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.mu.Unlock()
		return err
	}

	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("falha ao iniciar stable-diffusion.cpp: %w", err)
	}

	m.activeMode = WorkspaceImage
	m.activeCmd = cmd
	m.mu.Unlock()

	var wg sync.WaitGroup
	scan := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := sc.Text()
			m.logs.Append("sd", line)
			if onLine != nil {
				onLine(line)
			}
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)

	waitErr := cmd.Wait()
	wg.Wait()

	m.mu.Lock()
	m.activeCmd = nil
	m.activeMode = WorkspaceNone
	m.mu.Unlock()

	if waitErr != nil && ctx.Err() == nil {
		tail := strings.Join(m.logs.Tail("sd", 5), " | ")
		return fmt.Errorf("erro na geração de imagem: %v (%s)", waitErr, tail)
	}
	return ctx.Err()
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

func (m *Manager) stopLocked() {
	if m.activeCmd != nil && m.activeCmd.Process != nil {
		_ = m.activeCmd.Process.Kill()
		if m.activeDone != nil {
			select {
			case <-m.activeDone:
			case <-time.After(3 * time.Second):
			}
		}
	}
	m.activeCmd = nil
	m.activeDone = nil
	m.activeMode = WorkspaceNone
	m.activeModel = ""
	m.activePort = 0
}

func waitHealthy(ctx context.Context, url string, timeout time.Duration, exited <-chan struct{}) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			return fmt.Errorf("o processo terminou antes de ficar pronto")
		default:
		}
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			// 200 = pronto; 503 = ainda carregando o modelo.
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("timeout de %s aguardando %s", timeout, url)
}

// lineWriter converte a saída do processo em linhas de log.
type lineWriter struct {
	mu   sync.Mutex
	proc string
	logs *LogBuffer
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := -1
		for j, c := range w.buf {
			if c == '\n' || c == '\r' {
				i = j
				break
			}
		}
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if line != "" {
			w.logs.Append(w.proc, line)
		}
	}
	return len(p), nil
}

func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if line := strings.TrimSpace(string(w.buf)); line != "" {
		w.logs.Append(w.proc, line)
	}
	w.buf = nil
}
