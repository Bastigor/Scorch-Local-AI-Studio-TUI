package backend

import "local-ai-studio-tui/internal/hardware"

type WorkspaceMode string

const (
	WorkspaceNone WorkspaceMode = "none"
)

type Manager struct {
	dataDir       string
	hw            hardware.Info
	activeMode    WorkspaceMode
	activeBackend hardware.BackendType
}

func NewManager(dataDir string, hw hardware.Info) *Manager {
	return &Manager{
		dataDir:       dataDir,
		hw:            hw,
		activeMode:    WorkspaceNone,
		activeBackend: hw.SelectedBackend,
	}
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

func (m *Manager) Shutdown() {}
