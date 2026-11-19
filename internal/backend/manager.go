package backend

import (
	"context"
	"strings"
	"sync"

	"local-ai-studio-tui/internal/hardware"
)

type WorkspaceMode string

const (
	WorkspaceNone WorkspaceMode = "none"
)

type LogEntry struct {
	Process string
	Line    string
}

type LogBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
}

func (b *LogBuffer) Append(proc, line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, LogEntry{Process: proc, Line: line})
}

func (b *LogBuffer) Filter(proc string) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if proc == "" || proc == "all" {
		return append([]LogEntry(nil), b.entries...)
	}
	var out []LogEntry
	for _, e := range b.entries {
		if strings.EqualFold(e.Process, proc) {
			out = append(out, e)
		}
	}
	return out
}

type Manager struct {
	dataDir       string
	hw            hardware.Info
	activeMode    WorkspaceMode
	activeBackend hardware.BackendType
	logs          *LogBuffer
}

func NewManager(dataDir string, hw hardware.Info) *Manager {
	return &Manager{
		dataDir:       dataDir,
		hw:            hw,
		activeMode:    WorkspaceNone,
		activeBackend: hw.SelectedBackend,
		logs:          &LogBuffer{},
	}
}

func (m *Manager) Logs() *LogBuffer {
	return m.logs
}

func (m *Manager) ActiveWorkspace() WorkspaceMode {
	return m.activeMode
}

func (m *Manager) ActiveBackend() hardware.BackendType {
	return m.activeBackend
}

func (m *Manager) HasBinaries() bool {
	return false
}

func (m *Manager) StartLlamaServer(ctx context.Context, modelPath, mmprojPath string) (string, error) {
	return "http://127.0.0.1:8080", nil
}

func (m *Manager) RunStableDiffusion(ctx context.Context, args []string, onLine func(string)) error {
	return nil
}

func (m *Manager) Shutdown() {}
