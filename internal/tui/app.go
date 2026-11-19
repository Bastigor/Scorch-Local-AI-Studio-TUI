package tui

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"local-ai-studio-tui/internal/backend"
	"local-ai-studio-tui/internal/hardware"
	"local-ai-studio-tui/internal/imagegen"
	"local-ai-studio-tui/internal/llm"
	"local-ai-studio-tui/internal/models"
	"local-ai-studio-tui/internal/render"
	"local-ai-studio-tui/internal/store"
	"local-ai-studio-tui/internal/sysmon"
	"local-ai-studio-tui/internal/tui/theme"
)

type TabID int

const (
	TabChat TabID = iota
	TabImages
	TabModels
	TabMonitor
	TabLogs
)

type AppDeps struct {
	DataDir    string
	Config     store.Config
	Hardware   hardware.Info
	TermCaps   render.Capabilities
	Theme      theme.Theme
	BackendMgr *backend.Manager
	ConvStore  *store.ConversationStore
	ModelMgr   *models.Manager
	SysMon     *sysmon.Collector
}

type splashDoneMsg struct{}
type tickMonitorMsg time.Time
type renderThrottleMsg struct{}
type scorchAnimTickMsg struct{}

type wizardDoneMsg struct {
	err error
}

type chatStreamMsg struct {
	chunk llm.StreamChunk
	ch    <-chan llm.StreamChunk
}

type sdProgressMsg struct {
	pct float64
	ch  <-chan float64
}

type sdDoneMsg struct {
	imagePath string
	err       error
}

type downloadTickMsg struct {
	prog models.DownloadProgress
	ch   <-chan models.DownloadProgress
}

type AppModel struct {
	deps        AppDeps
	width       int
	height      int
	activeTab   TabID
	showSplash  bool
	showWizard  bool
	wizardBusy  bool
	wizardNote  string
	showHelp    bool
	confirmQuit bool

	statusMsg   string
	errMsg      string
	scorchState theme.ScorchState
	scorchFrame int

	// Chat state
	chatViewport  viewport.Model
	chatInput     textarea.Model
	chatSidebar   bool
	systemPrompt  textinput.Model
	tempInput     textinput.Model
	topPInput     textinput.Model
	maxTokInput   textinput.Model
	attachInput   textinput.Model
	attachingImg  bool
	attachedImg   string
	conversations []store.Conversation
	convIdx       int
	activeConv    store.Conversation
	renamingConv  bool
	renameInput   textinput.Model
	streaming     bool
	streamCancel  context.CancelFunc
	streamBuf     strings.Builder
	dirtyRender   bool

	// Images state
	imgInputs     []textinput.Model
	imgFocusIdx   int
	imgGenerating bool
	imgProgress   float64
	imgCancel     context.CancelFunc
	lastImagePath string
	lastImageView string
	gallery       []imagegen.GalleryEntry
	galleryIdx    int

	// Models state
	catalog        []models.CatalogItem
	installed      []models.InstalledModel
	catalogIdx     int
	installedIdx   int
	selectedLLM    string
	selectedSD     string
	importInput    textinput.Model
	importingMode  bool
	downloads      map[string]models.DownloadProgress
	downloadCancel map[string]context.CancelFunc

	// Monitor & Logs
	metrics     sysmon.Metrics
	logFilter   string
	logViewport viewport.Model
}

func NewAppModel(deps AppDeps) AppModel {
	ta := textarea.New()
	ta.Placeholder = "Digite sua mensagem... (Enter envia, Alt+Enter ou Ctrl+J quebra linha)"
	ta.Focus()
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	vp := viewport.New(76, 14)
	logVp := viewport.New(76, 16)

	sysIn := textinput.New()
	sysIn.SetValue(deps.Config.Chat.SystemPrompt)
	tempIn := textinput.New()
	tempIn.SetValue(fmt.Sprintf("%.2f", deps.Config.Chat.Temperature))
	topPIn := textinput.New()
	topPIn.SetValue(fmt.Sprintf("%.2f", deps.Config.Chat.TopP))
	maxTokIn := textinput.New()
	maxTokIn.SetValue(fmt.Sprintf("%d", deps.Config.Chat.MaxTokens))

	attachIn := textinput.New()
	attachIn.Placeholder = "Caminho da imagem para visão (requer mmproj)..."
	renameIn := textinput.New()
	renameIn.Placeholder = "Novo título da conversa..."

	imgFields := make([]textinput.Model, 9)
	defaults := []string{
		"a clean isometric studio workstation with warm lighting, high detail",
		"blurry, low quality, artifacts",
		"20",
		"7.0",
		"-1",
		"512",
		"512",
		"euler_a",
		"1",
	}
	for i := range imgFields {
		ti := textinput.New()
		ti.SetValue(defaults[i])
		imgFields[i] = ti
	}
	imgFields[0].Focus()

	impIn := textinput.New()
	impIn.Placeholder = "Cole o caminho local (.gguf ou .safetensors) e pressione Enter..."

	cat, _ := deps.ModelMgr.LoadOrCreateCatalog()
	inst, _ := deps.ModelMgr.ListInstalled()
	convs, _ := deps.ConvStore.List()
	gal, _ := imagegen.ListGallery(filepath.Join(deps.DataDir, "outputs"))

	var selLLM, selSD string
	for _, im := range inst {
		if im.Kind == models.KindLLM && selLLM == "" {
			selLLM = im.Path
		}
		if im.Kind == models.KindCheckpoint && selSD == "" {
			selSD = im.Path
		}
	}

	activeConv := store.Conversation{
		ID:    time.Now().Format("20060102-150405"),
		Title: "Sessão Inicial",
	}
	if len(convs) > 0 {
		activeConv = convs[0]
	}

	m := AppModel{
		deps:           deps,
		width:          deps.TermCaps.Cols,
		height:         deps.TermCaps.Rows,
		activeTab:      TabChat,
		showSplash:     true,
		showWizard:     !deps.BackendMgr.HasBinaries(),
		wizardNote:     fmt.Sprintf("Hardware detectado: %s (%s). Pressione Enter para baixar os binários ou Esc para pular.", deps.Hardware.GPUName, deps.Hardware.SelectedBackend),
		scorchState:    theme.ScorchIdle,
		chatViewport:   vp,
		chatInput:      ta,
		systemPrompt:   sysIn,
		tempInput:      tempIn,
		topPInput:      topPIn,
		maxTokInput:    maxTokIn,
		attachInput:    attachIn,
		renameInput:    renameIn,
		conversations:  convs,
		activeConv:     activeConv,
		imgInputs:      imgFields,
		gallery:        gal,
		catalog:        cat,
		installed:      inst,
		selectedLLM:    selLLM,
		selectedSD:     selSD,
		importInput:    impIn,
		downloads:      make(map[string]models.DownloadProgress),
		downloadCancel: make(map[string]context.CancelFunc),
		logFilter:      "all",
		logViewport:    logVp,
	}
	m.refreshChatViewport(true)
	return m
}

func (m AppModel) Init() tea.Cmd {
	return tea.Batch(
		tea.Tick(1*time.Second, func(time.Time) tea.Msg { return splashDoneMsg{} }),
		tea.Tick(1500*time.Millisecond, func(t time.Time) tea.Msg { return tickMonitorMsg(t) }),
		tea.Tick(33*time.Millisecond, func(time.Time) tea.Msg { return renderThrottleMsg{} }),
		tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return scorchAnimTickMsg{} }),
	)
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.reflow()
		return m, nil

	case splashDoneMsg:
		m.showSplash = false
		return m, nil

	case scorchAnimTickMsg:
		if m.streaming || m.imgGenerating || m.wizardBusy || len(m.downloadCancel) > 0 {
			m.scorchState = theme.ScorchThinking
			m.scorchFrame = (m.scorchFrame + 1) % 2
		}
		return m, tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return scorchAnimTickMsg{} })

	case wizardDoneMsg:
		m.wizardBusy = false
		if msg.err != nil {
			m.scorchState = theme.ScorchError
			m.errMsg = fmt.Sprintf("Falha no setup inicial: %v", msg.err)
		} else {
			m.showWizard = false
			m.scorchState = theme.ScorchSuccess
			m.statusMsg = "Backends instalados com sucesso em studio-data/bin/."
		}
		return m, nil

	case tickMonitorMsg:
		if m.activeTab == TabMonitor {
			m.metrics = m.deps.SysMon.Sample()
		}
		if m.activeTab == TabLogs {
			lines := m.deps.BackendMgr.Logs().Filter(m.logFilter)
			var sb strings.Builder
			for _, l := range lines {
				fmt.Fprintf(&sb, "[%s] %s\n", l.Process, l.Line)
			}
			m.logViewport.SetContent(sb.String())
		}
		return m, tea.Tick(1500*time.Millisecond, func(t time.Time) tea.Msg { return tickMonitorMsg(t) })

	case renderThrottleMsg:
		if m.dirtyRender {
			m.dirtyRender = false
			m.refreshChatViewport(false)
		}
		return m, tea.Tick(33*time.Millisecond, func(time.Time) tea.Msg { return renderThrottleMsg{} })

	case chatStreamMsg:
		if msg.chunk.Err != nil {
			m.streaming = false
			m.scorchState = theme.ScorchError
			m.errMsg = fmt.Sprintf("Falha na inferência LLM: %v", msg.chunk.Err)
			return m, nil
		}
		if msg.chunk.Delta != "" {
			m.streamBuf.WriteString(msg.chunk.Delta)
			if len(m.activeConv.Messages) > 0 && m.activeConv.Messages[len(m.activeConv.Messages)-1].Role == "assistant" {
				m.activeConv.Messages[len(m.activeConv.Messages)-1].Content = m.streamBuf.String()
			}
			m.dirtyRender = true
		}
		if msg.chunk.Done {
			m.streaming = false
			m.scorchState = theme.ScorchSuccess
			m.refreshChatViewport(true)
			_ = m.deps.ConvStore.Save(m.activeConv)
			m.conversations, _ = m.deps.ConvStore.List()
			return m, nil
		}
		return m, waitNextStreamChunk(msg.ch)

	case sdProgressMsg:
		m.imgProgress = msg.pct
		if msg.ch != nil {
			return m, waitNextSDProgress(msg.ch)
		}
		return m, nil

	case sdDoneMsg:
		m.imgGenerating = false
		if msg.err != nil {
			m.scorchState = theme.ScorchError
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.scorchState = theme.ScorchSuccess
		m.lastImagePath = msg.imagePath
		m.statusMsg = fmt.Sprintf("Imagem salva: %s", msg.imagePath)
		view, err := render.RenderImageFile(msg.imagePath, m.deps.TermCaps.EffectiveImageMode, m.deps.TermCaps.InTmux, m.width/2, m.height-12)
		if err == nil {
			m.lastImageView = view
		}
		m.gallery, _ = imagegen.ListGallery(filepath.Join(m.deps.DataDir, "outputs"))
		return m, nil

	case downloadTickMsg:
		m.downloads[msg.prog.ID] = msg.prog
		if msg.prog.Done {
			delete(m.downloadCancel, msg.prog.ID)
			if msg.prog.Err != nil {
				m.scorchState = theme.ScorchError
				m.errMsg = fmt.Sprintf("Erro no download (%s): %v", msg.prog.Name, msg.prog.Err)
			} else {
				m.scorchState = theme.ScorchSuccess
				m.statusMsg = fmt.Sprintf("Download concluído: %s", msg.prog.Name)
				m.installed, _ = m.deps.ModelMgr.ListInstalled()
				m.ensureSelectedModels()
			}
			return m, nil
		}
		if msg.ch != nil {
			return m, waitNextDownloadTick(msg.ch)
		}
		return m, nil

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Y <= 2 {
			idx := msg.X / 14
			if idx >= 0 && idx <= int(TabLogs) {
				m.activeTab = TabID(idx)
			}
		}
		return m, nil

	case tea.KeyMsg:
		if m.showSplash {
			m.showSplash = false
			return m, nil
		}

		if m.showWizard {
			if msg.String() == "esc" {
				m.showWizard = false
				return m, nil
			}
			if msg.String() == "enter" && !m.wizardBusy {
				m.wizardBusy = true
				m.wizardNote = "Baixando e extraindo binários nativos... Aguarde."
				return m, func() tea.Msg {
					err := m.deps.ModelMgr.InstallBackendBinaries(context.Background(), string(m.deps.Hardware.SelectedBackend), nil)
					return wizardDoneMsg{err: err}
				}
			}
			return m, nil
		}

		if msg.Type == tea.KeyCtrlC {
			if m.streaming && m.streamCancel != nil {
				m.streamCancel()
				m.streaming = false
				m.statusMsg = "Geração de texto cancelada."
				return m, nil
			}
			if m.imgGenerating && m.imgCancel != nil {
				m.imgCancel()
				m.imgGenerating = false
				m.statusMsg = "Geração de imagem cancelada."
				return m, nil
			}
			if len(m.downloadCancel) > 0 {
				for id, cancel := range m.downloadCancel {
					cancel()
					delete(m.downloadCancel, id)
				}
				m.statusMsg = "Downloads ativos cancelados."
				return m, nil
			}
			if m.confirmQuit {
				m.deps.BackendMgr.Shutdown()
				return m, tea.Quit
			}
			m.confirmQuit = true
			m.statusMsg = "Pressione Ctrl+C novamente para encerrar o Local AI Studio."
			return m, nil
		}
		m.confirmQuit = false

		switch msg.String() {
		case "?":
			if m.activeTab != TabChat || !m.chatInput.Focused() || m.chatInput.Value() == "" {
				m.showHelp = !m.showHelp
				return m, nil
			}
		case "esc":
			m.showHelp = false
			m.importingMode = false
			m.attachingImg = false
			m.renamingConv = false
			return m, nil
		case "alt+1":
			m.activeTab = TabChat
			return m, nil
		case "alt+2":
			m.activeTab = TabImages
			return m, nil
		case "alt+3":
			m.activeTab = TabModels
			return m, nil
		case "alt+4":
			m.activeTab = TabMonitor
			m.metrics = m.deps.SysMon.Sample()
			return m, nil
		case "alt+5":
			m.activeTab = TabLogs
			return m, nil
		case "tab":
			if m.activeTab == TabImages {
				m.imgInputs[m.imgFocusIdx].Blur()
				m.imgFocusIdx = (m.imgFocusIdx + 1) % len(m.imgInputs)
				m.imgInputs[m.imgFocusIdx].Focus()
				return m, nil
			}
			m.activeTab = (m.activeTab + 1) % 5
			return m, nil
		case "shift+tab":
			if m.activeTab == TabImages {
				m.imgInputs[m.imgFocusIdx].Blur()
				m.imgFocusIdx = (m.imgFocusIdx - 1 + len(m.imgInputs)) % len(m.imgInputs)
				m.imgInputs[m.imgFocusIdx].Focus()
				return m, nil
			}
			m.activeTab = (m.activeTab + 4) % 5
			return m, nil
		}

		return m.updateActiveTab(msg)
	}

	return m, nil
}

func (m AppModel) updateActiveTab(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.activeTab {
	case TabChat:
		if m.attachingImg {
			if msg.String() == "enter" {
				m.attachedImg = strings.TrimSpace(m.attachInput.Value())
				m.attachingImg = false
				m.attachInput.Reset()
				m.statusMsg = fmt.Sprintf("Imagem anexada: %s", m.attachedImg)
				return m, nil
			}
			var cmd tea.Cmd
			m.attachInput, cmd = m.attachInput.Update(msg)
			return m, cmd
		}
		if m.renamingConv {
			if msg.String() == "enter" {
				newTitle := m.renameInput.Value()
				m.renamingConv = false
				m.renameInput.Reset()
				if strings.TrimSpace(newTitle) != "" {
					_ = m.deps.ConvStore.Rename(m.activeConv.ID, newTitle)
					m.activeConv.Title = strings.TrimSpace(newTitle)
					m.conversations, _ = m.deps.ConvStore.List()
				}
				return m, nil
			}
			var cmd tea.Cmd
			m.renameInput, cmd = m.renameInput.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "ctrl+s":
			m.chatSidebar = !m.chatSidebar
			m.reflow()
			return m, nil
		case "ctrl+a":
			m.attachingImg = true
			m.attachInput.Focus()
			return m, nil
		case "ctrl+r":
			m.renamingConv = true
			m.renameInput.SetValue(m.activeConv.Title)
			m.renameInput.Focus()
			return m, nil
		case "ctrl+p":
			// Alterna entre conversas salvas no histórico JSONL
			if len(m.conversations) > 0 {
				m.convIdx = (m.convIdx + 1) % len(m.conversations)
				m.activeConv = m.conversations[m.convIdx]
				m.refreshChatViewport(true)
				m.statusMsg = fmt.Sprintf("Conversa carregada: %s", m.activeConv.Title)
			}
			return m, nil
		case "ctrl+d":
			if m.activeConv.ID != "" {
				_ = m.deps.ConvStore.Delete(m.activeConv.ID)
				m.conversations, _ = m.deps.ConvStore.List()
				m.activeConv = store.Conversation{
					ID:    time.Now().Format("20060102-150405"),
					Title: "Nova Conversa",
				}
				m.refreshChatViewport(true)
				m.statusMsg = "Conversa apagada."
			}
			return m, nil
		case "ctrl+y":
			if len(m.activeConv.Messages) > 0 {
				last := m.activeConv.Messages[len(m.activeConv.Messages)-1].Content
				p := CopyToClipboardOSC52(m.deps.DataDir, last)
				m.statusMsg = fmt.Sprintf("Última resposta copiada via OSC 52 (fallback: %s)", p)
			}
			return m, nil
		case "ctrl+n":
			m.activeConv = store.Conversation{
				ID:    time.Now().Format("20060102-150405"),
				Title: fmt.Sprintf("Conversa %d", len(m.conversations)+1),
			}
			m.refreshChatViewport(true)
			return m, nil
		case "alt+enter", "ctrl+j":
			m.chatInput.InsertString("\n")
			return m, nil
		case "enter":
			prompt := strings.TrimSpace(m.chatInput.Value())
			if prompt == "" || m.streaming {
				return m, nil
			}
			m.chatInput.Reset()
			m.errMsg = ""
			m.activeConv.Messages = append(m.activeConv.Messages,
				llm.Message{Role: "user", Content: prompt, ImagePath: m.attachedImg},
				llm.Message{Role: "assistant", Content: ""},
			)
			m.attachedImg = ""
			m.streamBuf.Reset()
			m.streaming = true
			m.refreshChatViewport(true)
			return m, m.startChatStreamCmd()
		}
		var cmd tea.Cmd
		m.chatInput, cmd = m.chatInput.Update(msg)
		return m, cmd

	case TabImages:
		switch msg.String() {
		case "ctrl+g", "enter":
			if m.imgGenerating {
				return m, nil
			}
			return m, m.startImageGenCmd()
		case "ctrl+o":
			if m.lastImagePath != "" {
				_ = render.OpenExternally(m.lastImagePath)
				m.statusMsg = "Abrindo imagem no visualizador externo..."
			}
			return m, nil
		case "pgdown":
			if len(m.gallery) > 0 {
				m.galleryIdx = (m.galleryIdx + 1) % len(m.gallery)
				m.loadGalleryEntryToForm(m.gallery[m.galleryIdx])
			}
			return m, nil
		case "pgup":
			if len(m.gallery) > 0 {
				m.galleryIdx = (m.galleryIdx - 1 + len(m.gallery)) % len(m.gallery)
				m.loadGalleryEntryToForm(m.gallery[m.galleryIdx])
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.imgInputs[m.imgFocusIdx], cmd = m.imgInputs[m.imgFocusIdx].Update(msg)
		return m, cmd

	case TabModels:
		if m.importingMode {
			if msg.String() == "enter" {
				path := m.importInput.Value()
				m.importingMode = false
				m.importInput.Reset()
				mod, err := m.deps.ModelMgr.ImportLocalModel(path)
				if err != nil {
					m.scorchState = theme.ScorchError
					m.errMsg = err.Error()
				} else {
					m.scorchState = theme.ScorchSuccess
					m.statusMsg = fmt.Sprintf("Modelo importado: %s (%s)", mod.Name, mod.Kind)
					m.installed, _ = m.deps.ModelMgr.ListInstalled()
					m.ensureSelectedModels()
				}
				return m, nil
			}
			var cmd tea.Cmd
			m.importInput, cmd = m.importInput.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "up", "k":
			if m.catalogIdx > 0 {
				m.catalogIdx--
			}
		case "down", "j":
			if m.catalogIdx < len(m.catalog)-1 {
				m.catalogIdx++
			}
		case "n":
			if len(m.installed) > 0 {
				m.installedIdx = (m.installedIdx + 1) % len(m.installed)
			}
		case "enter":
			// Define modelo instalado selecionado como ativo para Chat ou Imagens
			if len(m.installed) > 0 && m.installedIdx < len(m.installed) {
				sel := m.installed[m.installedIdx]
				if sel.Kind == models.KindLLM {
					m.selectedLLM = sel.Path
					m.statusMsg = fmt.Sprintf("LLM ativo selecionado: %s", sel.Name)
				} else {
					m.selectedSD = sel.Path
					m.statusMsg = fmt.Sprintf("Checkpoint SD ativo selecionado: %s", sel.Name)
				}
			}
		case "i":
			m.importingMode = true
			m.importInput.Focus()
		case "d":
			if len(m.catalog) > 0 {
				item := m.catalog[m.catalogIdx]
				return m, m.startDownloadCmd(item)
			}
		case "q":
			for _, item := range m.catalog {
				if item.Starter {
					return m, m.startDownloadCmd(item)
				}
			}
		case "x":
			if len(m.installed) > 0 && m.installedIdx < len(m.installed) {
				target := m.installed[m.installedIdx]
				freed, err := m.deps.ModelMgr.DeleteModel(target.Path)
				if err != nil {
					m.errMsg = err.Error()
				} else {
					m.statusMsg = fmt.Sprintf("Removido %s (%.1f MB liberados)", target.Name, float64(freed)/(1024*1024))
					m.installed, _ = m.deps.ModelMgr.ListInstalled()
					if m.installedIdx >= len(m.installed) && m.installedIdx > 0 {
						m.installedIdx--
					}
					m.ensureSelectedModels()
				}
			}
		}
		return m, nil

	case TabLogs:
		switch msg.String() {
		case "1":
			m.logFilter = "all"
		case "2":
			m.logFilter = "llama-server"
		case "3":
			m.logFilter = "sd"
		}
		var cmd tea.Cmd
		m.logViewport, cmd = m.logViewport.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *AppModel) ensureSelectedModels() {
	var foundLLM, foundSD bool
	for _, im := range m.installed {
		if im.Path == m.selectedLLM {
			foundLLM = true
		}
		if im.Path == m.selectedSD {
			foundSD = true
		}
	}
	if !foundLLM {
		m.selectedLLM = ""
		for _, im := range m.installed {
			if im.Kind == models.KindLLM {
				m.selectedLLM = im.Path
				break
			}
		}
	}
	if !foundSD {
		m.selectedSD = ""
		for _, im := range m.installed {
			if im.Kind == models.KindCheckpoint {
				m.selectedSD = im.Path
				break
			}
		}
	}
}

func (m *AppModel) loadGalleryEntryToForm(entry imagegen.GalleryEntry) {
	m.imgInputs[0].SetValue(entry.Params.Prompt)
	m.imgInputs[1].SetValue(entry.Params.NegativePrompt)
	m.imgInputs[2].SetValue(fmt.Sprintf("%d", entry.Params.Steps))
	m.imgInputs[3].SetValue(fmt.Sprintf("%.2f", entry.Params.CFGScale))
	m.imgInputs[4].SetValue(fmt.Sprintf("%d", entry.Params.Seed))
	m.imgInputs[5].SetValue(fmt.Sprintf("%d", entry.Params.Width))
	m.imgInputs[6].SetValue(fmt.Sprintf("%d", entry.Params.Height))
	if entry.Params.Sampler != "" {
		m.imgInputs[7].SetValue(entry.Params.Sampler)
	}
	m.lastImagePath = entry.ImagePath
	if view, err := render.RenderImageFile(entry.ImagePath, m.deps.TermCaps.EffectiveImageMode, m.deps.TermCaps.InTmux, m.width/2, m.height-12); err == nil {
		m.lastImageView = view
	}
	m.statusMsg = fmt.Sprintf("Parâmetros restaurados de %s", filepath.Base(entry.ImagePath))
}

func waitNextStreamChunk(ch <-chan llm.StreamChunk) tea.Cmd {
	return func() tea.Msg {
		chunk, ok := <-ch
		if !ok {
			return chatStreamMsg{chunk: llm.StreamChunk{Done: true}}
		}
		return chatStreamMsg{chunk: chunk, ch: ch}
	}
}

func waitNextSDProgress(ch <-chan float64) tea.Cmd {
	return func() tea.Msg {
		pct, ok := <-ch
		if !ok {
			return sdProgressMsg{pct: 1.0}
		}
		return sdProgressMsg{pct: pct, ch: ch}
	}
}

func waitNextDownloadTick(ch <-chan models.DownloadProgress) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return downloadTickMsg{prog: p, ch: ch}
	}
}

func (m *AppModel) startChatStreamCmd() tea.Cmd {
	m.ensureSelectedModels()
	if m.selectedLLM == "" {
		m.streaming = false
		m.scorchState = theme.ScorchError
		m.errMsg = "Nenhum modelo GGUF instalado. Vá para a aba Modelos (Alt+3) e pressione 'q' para baixar o Qwen2.5 Coder inicial ou 'i' para importar."
		return nil
	}

	var mmproj string
	for _, im := range m.installed {
		if im.Path == m.selectedLLM {
			mmproj = im.MmprojPath
			break
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.streamCancel = cancel
	history := append([]llm.Message(nil), m.activeConv.Messages[:len(m.activeConv.Messages)-1]...)
	sysPrompt := m.systemPrompt.Value()

	temp, err := strconv.ParseFloat(m.tempInput.Value(), 64)
	if err != nil {
		temp = m.deps.Config.Chat.Temperature
	}
	topP, err := strconv.ParseFloat(m.topPInput.Value(), 64)
	if err != nil {
		topP = m.deps.Config.Chat.TopP
	}
	maxTok, err := strconv.Atoi(m.maxTokInput.Value())
	if err != nil {
		maxTok = m.deps.Config.Chat.MaxTokens
	}

	params := llm.ChatParams{
		Temperature: temp,
		TopP:        topP,
		MaxTokens:   maxTok,
	}
	llmPath := m.selectedLLM

	return func() tea.Msg {
		ep, err := m.deps.BackendMgr.StartLlamaServer(ctx, llmPath, mmproj)
		if err != nil {
			return chatStreamMsg{chunk: llm.StreamChunk{Err: err, Done: true}}
		}
		ch := make(chan llm.StreamChunk, 64)
		go llm.StreamChatCompletion(ctx, ep, sysPrompt, history, params, ch)
		first, ok := <-ch
		if !ok {
			return chatStreamMsg{chunk: llm.StreamChunk{Done: true}}
		}
		return chatStreamMsg{chunk: first, ch: ch}
	}
}

func (m *AppModel) startImageGenCmd() tea.Cmd {
	m.ensureSelectedModels()
	if m.selectedSD == "" {
		m.scorchState = theme.ScorchError
		m.errMsg = "Nenhum checkpoint Stable Diffusion (.safetensors/.ckpt) instalado em studio-data/models/checkpoints/."
		return nil
	}

	m.imgGenerating = true
	m.imgProgress = 0
	m.errMsg = ""
	ctx, cancel := context.WithCancel(context.Background())
	m.imgCancel = cancel

	steps, _ := strconv.Atoi(m.imgInputs[2].Value())
	if steps <= 0 {
		steps = 20
	}
	cfgScale, _ := strconv.ParseFloat(m.imgInputs[3].Value(), 64)
	if cfgScale <= 0 {
		cfgScale = 7.0
	}
	seed, err := strconv.ParseInt(m.imgInputs[4].Value(), 10, 64)
	if err != nil || seed < 0 {
		seed = rand.Int63n(999999999)
	}
	width, _ := strconv.Atoi(m.imgInputs[5].Value())
	if width <= 0 {
		width = 512
	}
	height, _ := strconv.Atoi(m.imgInputs[6].Value())
	if height <= 0 {
		height = 512
	}
	batch, _ := strconv.Atoi(m.imgInputs[8].Value())
	if batch <= 0 {
		batch = 1
	}

	params := imagegen.GenParams{
		ModelPath:      m.selectedSD,
		Prompt:         m.imgInputs[0].Value(),
		NegativePrompt: m.imgInputs[1].Value(),
		Steps:          steps,
		CFGScale:       cfgScale,
		Seed:           seed,
		Width:          width,
		Height:         height,
		Sampler:        m.imgInputs[7].Value(),
		BatchCount:     batch,
	}

	outFile := filepath.Join(m.deps.DataDir, "outputs", fmt.Sprintf("img_%s.png", time.Now().Format("20060102_150405")))
	args := imagegen.BuildSDArgs(params, outFile)

	progCh := make(chan float64, 16)
	doneCmd := func() tea.Msg {
		defer close(progCh)
		err := m.deps.BackendMgr.RunStableDiffusion(ctx, args, func(line string) {
			if pct, ok := imagegen.ParseProgress(line); ok {
				select {
				case progCh <- pct:
				default:
				}
			}
		})
		if err != nil {
			return sdDoneMsg{err: err}
		}
		_, _ = imagegen.SaveMetadata(outFile, params)
		return sdDoneMsg{imagePath: outFile}
	}

	return tea.Batch(doneCmd, waitNextSDProgress(progCh))
}

func (m *AppModel) startDownloadCmd(item models.CatalogItem) tea.Cmd {
	if _, exists := m.downloadCancel[item.ID]; exists {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.downloadCancel[item.ID] = cancel
	ch := make(chan models.DownloadProgress, 32)

	go func() {
		defer close(ch)
		err := m.deps.ModelMgr.DownloadWithResume(ctx, item, func(p models.DownloadProgress) {
			select {
			case ch <- p:
			default:
			}
		})
		ch <- models.DownloadProgress{
			ID:   item.ID,
			Name: item.Name,
			Done: true,
			Err:  err,
		}
	}()

	return waitNextDownloadTick(ch)
}

func (m *AppModel) reflow() {
	vpW := m.width - 4
	if m.chatSidebar && m.width >= 100 {
		vpW = m.width - 36
	}
	if vpW < 20 {
		vpW = 20
	}
	vpH := m.height - 11
	if vpH < 5 {
		vpH = 5
	}
	m.chatViewport.Width = vpW
	m.chatViewport.Height = vpH
	m.chatInput.SetWidth(vpW)
	m.logViewport.Width = m.width - 4
	m.logViewport.Height = m.height - 9
}

func (m *AppModel) refreshChatViewport(forceBottom bool) {
	atBottom := m.chatViewport.AtBottom()
	if len(m.activeConv.Messages) == 0 {
		empty := lipgloss.JoinVertical(lipgloss.Center,
			theme.RenderScorch(m.scorchState, m.scorchFrame, m.deps.Config.UI.ReduceMotion),
			"",
			m.deps.Theme.MutedText.Render("Local AI Studio TUI pronto. Envie uma mensagem abaixo ou pressione ? para ajuda."),
		)
		m.chatViewport.SetContent(empty)
		return
	}

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(m.chatViewport.Width-2),
	)

	var sb strings.Builder
	for _, msg := range m.activeConv.Messages {
		roleTag := "[VOCÊ]"
		if msg.Role == "assistant" {
			roleTag = "[ASSISTENTE]"
		}
		sb.WriteString(roleTag + "\n")
		rendered := msg.Content
		if renderer != nil && msg.Role == "assistant" && msg.Content != "" {
			if out, err := renderer.Render(msg.Content); err == nil {
				rendered = strings.TrimSpace(out)
			}
		}
		sb.WriteString(rendered + "\n\n")
	}
	m.chatViewport.SetContent(sb.String())
	if forceBottom || atBottom {
		m.chatViewport.GotoBottom()
	}
}

func (m AppModel) View() string {
	if m.width < 80 || m.height < 24 {
		return m.deps.Theme.ErrorBlock.Render(
			fmt.Sprintf("%s Terminal muito pequeno (%dx%d). Redimensione para pelo menos 80x24.",
				m.deps.Theme.ErrorMark, m.width, m.height),
		)
	}

	if m.showSplash {
		splash := lipgloss.JoinVertical(lipgloss.Center,
			theme.RenderScorch(theme.ScorchIdle, 0, true),
			"",
			lipgloss.NewStyle().Bold(true).Render("LOCAL AI STUDIO TUI"),
			m.deps.Theme.MutedText.Render(fmt.Sprintf("Backend: %s | Imagem: %s | Pressione qualquer tecla...",
				m.deps.Hardware.SelectedBackend, m.deps.TermCaps.EffectiveImageMode)),
		)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, splash)
	}

	if m.showWizard {
		wiz := lipgloss.JoinVertical(lipgloss.Center,
			theme.RenderScorch(m.scorchState, m.scorchFrame, m.deps.Config.UI.ReduceMotion),
			"",
			lipgloss.NewStyle().Bold(true).Render("SETUP INICIAL DE BACKENDS"),
			m.wizardNote,
		)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.deps.Theme.AccentBorder.Render(wiz))
	}

	if m.showHelp {
		helpText := strings.Join([]string{
			"ATALHOS DE TECLADO - LOCAL AI STUDIO TUI",
			"",
			"Alt+1..5 / Tab   : Alternar entre abas (Chat, Imagens, Modelos, Monitor, Logs)",
			"?                : Abrir/fechar esta ajuda",
			"Ctrl+C           : Cancelar operação/downloads em andamento (2x para sair)",
			"Ctrl+S (Chat)    : Recolher/expandir painel lateral de parâmetros",
			"Ctrl+N / Ctrl+P  : Nova conversa / Alternar conversa salva (JSONL)",
			"Ctrl+R / Ctrl+D  : Renomear / Apagar conversa ativa",
			"Ctrl+A / Ctrl+Y  : Anexar imagem (visão) / Copiar última resposta via OSC 52",
			"PgUp/PgDn (Img)  : Navegar na galeria e restaurar parâmetros no formulário",
			"Ctrl+O (Imagens) : Abrir última imagem gerada no visualizador externo do SO",
			"q / d / i (Mod.) : Baixar Qwen2.5 Coder / Baixar selecionado / Importar local",
			"n / Enter (Mod.) : Selecionar modelo instalado como ativo",
			"",
			"Nota sobre mouse: a seleção nativa do terminal pode exigir Shift+Arrastar.",
			"Pressione Esc ou ? para voltar.",
		}, "\n")
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.deps.Theme.AccentBorder.Render(helpText))
	}

	tabNames := []string{"1:Chat", "2:Imagens", "3:Modelos", "4:Monitor", "5:Logs"}
	var renderedTabs []string
	for i, name := range tabNames {
		if TabID(i) == m.activeTab {
			renderedTabs = append(renderedTabs, m.deps.Theme.ActiveTab.Render(name))
		} else {
			renderedTabs = append(renderedTabs, m.deps.Theme.HeaderTab.Render(name))
		}
	}
	header := lipgloss.JoinHorizontal(lipgloss.Top, renderedTabs...)

	var errBanner string
	if m.errMsg != "" {
		errBanner = m.deps.Theme.ErrorBlock.Width(m.width - 2).Render(
			fmt.Sprintf("%s ERRO: %s", m.deps.Theme.ErrorMark, m.errMsg),
		)
	}

	var body string
	switch m.activeTab {
	case TabChat:
		body = m.viewChat()
	case TabImages:
		body = m.viewImages()
	case TabModels:
		body = m.viewModels()
	case TabMonitor:
		body = m.viewMonitor()
	case TabLogs:
		body = m.viewLogs()
	}

	activeModelName := filepath.Base(m.selectedLLM)
	if m.activeTab == TabImages && m.selectedSD != "" {
		activeModelName = filepath.Base(m.selectedSD)
	}
	status := fmt.Sprintf("Backend: %s | Modelo: %s | %s",
		m.deps.BackendMgr.ActiveBackend(),
		activeModelName,
		m.statusMsg,
	)
	footer := m.deps.Theme.StatusBar.Width(m.width).Render(
		status + "  |  Alt+1..5: Abas  ?: Ajuda  Ctrl+C: Cancelar/Sair",
	)

	parts := []string{header}
	if errBanner != "" {
		parts = append(parts, errBanner)
	}
	parts = append(parts, body, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m AppModel) viewChat() string {
	convHeader := fmt.Sprintf("Conversa: %s (%d salvas | Ctrl+P: alternar, Ctrl+R: renomear, Ctrl+D: apagar)",
		m.activeConv.Title, len(m.conversations))
	if m.attachedImg != "" {
		convHeader += fmt.Sprintf(" | Anexo: %s", filepath.Base(m.attachedImg))
	}

	var extraInput string
	if m.attachingImg {
		extraInput = "\nAnexar imagem: " + m.attachInput.View()
	} else if m.renamingConv {
		extraInput = "\nRenomear conversa: " + m.renameInput.View()
	}

	mainPane := m.deps.Theme.Panel.Width(m.chatViewport.Width + 2).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			m.deps.Theme.MutedText.Render(convHeader),
			m.chatViewport.View(),
			m.chatInput.View()+extraInput,
		),
	)
	if !m.chatSidebar || m.width < 100 {
		return mainPane
	}
	side := m.deps.Theme.Panel.Width(32).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			"PARÂMETROS LLM",
			"",
			"System Prompt:",
			m.systemPrompt.View(),
			"Temperature:",
			m.tempInput.View(),
			"Top-P:",
			m.topPInput.View(),
			"Max Tokens:",
			m.maxTokInput.View(),
		),
	)
	return lipgloss.JoinHorizontal(lipgloss.Top, mainPane, side)
}

func (m AppModel) viewImages() string {
	labels := []string{"Prompt", "Negative", "Steps", "CFG", "Seed (-1=rand)", "Largura", "Altura", "Sampler", "Batch"}
	var formLines []string
	formLines = append(formLines, "GERAÇÃO STABLE DIFFUSION (Tab navega, Enter gera, PgUp/PgDn galeria, Ctrl+O externo)", "")
	for i, lbl := range labels {
		prefix := "  "
		if i == m.imgFocusIdx {
			prefix = "> "
		}
		formLines = append(formLines, fmt.Sprintf("%s%-14s: %s", prefix, lbl, m.imgInputs[i].View()))
	}
	if m.imgGenerating {
		formLines = append(formLines, "", fmt.Sprintf("Progresso: %.0f%%", m.imgProgress*100))
	}
	left := m.deps.Theme.Panel.Width(m.width/2 - 2).Render(strings.Join(formLines, "\n"))

	rightContent := theme.RenderScorch(m.scorchState, m.scorchFrame, m.deps.Config.UI.ReduceMotion) + "\n\nNenhuma imagem gerada ainda."
	if m.lastImageView != "" {
		rightContent = m.lastImageView + "\n" + m.lastImagePath
	} else if len(m.gallery) > 0 {
		var gLines []string
		gLines = append(gLines, fmt.Sprintf("GALERIA (%d imagens — PgUp/PgDn para reabrir)", len(m.gallery)), "")
		for idx, g := range m.gallery {
			mark := "  "
			if idx == m.galleryIdx {
				mark = "> "
			}
			gLines = append(gLines, fmt.Sprintf("%s%s (Seed: %d)", mark, filepath.Base(g.ImagePath), g.Params.Seed))
		}
		rightContent = strings.Join(gLines, "\n")
	}
	right := m.deps.Theme.Panel.Width(m.width/2 - 2).Render(rightContent)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m AppModel) viewModels() string {
	var lines []string
	lines = append(lines, "CATÁLOGO DE MODELOS (j/k: navegar, d: baixar, q: baixar Qwen2.5 Coder inicial, i: importar local)", "")
	for i, item := range m.catalog {
		row := fmt.Sprintf("  [%s] %s (%s)", item.Kind, item.Name, item.SizeHuman)
		if i == m.catalogIdx {
			row = m.deps.Theme.SelectedItem.Render(fmt.Sprintf("> [%s] %s (%s)", item.Kind, item.Name, item.SizeHuman))
		}
		lines = append(lines, row)
	}

	if len(m.downloads) > 0 {
		lines = append(lines, "", "DOWNLOADS:")
		for _, dl := range m.downloads {
			if dl.Done {
				continue
			}
			pct := 0.0
			if dl.Total > 0 {
				pct = float64(dl.Downloaded) / float64(dl.Total) * 100
			}
			lines = append(lines, fmt.Sprintf("  - %s: %.1f%% (%.2f MB/s, ETA %ds)",
				dl.Name, pct, dl.SpeedBPS/(1024*1024), dl.ETASeconds))
		}
	}

	if m.importingMode {
		lines = append(lines, "", "Importar arquivo local:", m.importInput.View())
	}

	lines = append(lines, "", "MODELOS INSTALADOS EM studio-data/models/ (n: alternar seleção, Enter: ativar, x: remover):", "")
	if len(m.installed) == 0 {
		lines = append(lines, m.deps.Theme.MutedText.Render("  Nenhum modelo instalado ainda."))
	} else {
		for idx, im := range m.installed {
			activeTag := ""
			if im.Path == m.selectedLLM || im.Path == m.selectedSD {
				activeTag = " [ATIVO]"
			}
			prefix := "  "
			if idx == m.installedIdx {
				prefix = "> "
			}
			lines = append(lines, fmt.Sprintf("%s%s [%s] %s%s (%.1f MB)",
				prefix, m.deps.Theme.SuccessMark, im.Kind, im.Name, activeTag, float64(im.SizeBytes)/(1024*1024)))
		}
	}

	return m.deps.Theme.Panel.Width(m.width - 2).Render(strings.Join(lines, "\n"))
}

func (m AppModel) viewMonitor() string {
	lines := []string{
		"MONITOR DE HARDWARE EM TEMPO REAL",
		"",
		fmt.Sprintf("CPU (%s) : %.1f%%", m.deps.Hardware.CPUName, m.metrics.CPUPercent),
		fmt.Sprintf("RAM              : %d / %d MB (%.1f%%)", m.metrics.RAMUsedMB, m.metrics.RAMTotalMB, m.metrics.RAMPercent),
	}
	if m.metrics.GPUAvailable {
		lines = append(lines,
			fmt.Sprintf("GPU (%s) [%s]: %.1f%%", m.deps.Hardware.GPUName, m.metrics.GPUSource, m.metrics.GPUPercent),
			fmt.Sprintf("VRAM             : %d / %d MB", m.metrics.VRAMUsedMB, m.metrics.VRAMTotalMB),
		)
	} else {
		lines = append(lines, m.deps.Theme.MutedText.Render("GPU/VRAM         : Telemetria via nvidia-smi/rocm-smi indisponível nesta máquina."))
	}
	return m.deps.Theme.Panel.Width(m.width - 2).Render(strings.Join(lines, "\n"))
}

func (m AppModel) viewLogs() string {
	header := fmt.Sprintf("LOGS DOS BACKENDS (Filtro ativo: %s | 1: Todos, 2: llama-server, 3: sd)", m.logFilter)
	return m.deps.Theme.Panel.Width(m.width - 2).Render(header + "\n\n" + m.logViewport.View())
}
