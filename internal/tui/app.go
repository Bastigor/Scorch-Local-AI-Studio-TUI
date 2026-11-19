package tui

import (
	"context"
	"fmt"
	"os"
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
	BackendMgr *backend.Manager
	ModelMgr   *models.Manager
	ConvStore  *store.ConversationStore
	SysMon     *sysmon.Collector
}

type tickMsg time.Time
type streamChunkMsg llm.StreamChunk
type streamChHolder struct {
	ch     chan llm.StreamChunk
	cancel context.CancelFunc
}
type streamStartedMsg streamChHolder
type sdProgressMsg struct {
	pct  float64
	done bool
	path string
	err  error
}
type downloadProgressMsg models.DownloadProgress
type downloadStartedMsg struct {
	id     string
	ch     chan models.DownloadProgress
	cancel context.CancelFunc
}
type wizardDoneMsg struct {
	err error
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
	chatParams    bool
	paramFocusIdx int
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
	streamCh      chan llm.StreamChunk
	streamCancel  context.CancelFunc
	streamBuf     strings.Builder
	dirtyRender   bool

	// Images state
	imgInputs     []textinput.Model
	imgFocusIdx   int // 0..8 = inputs, 9 = botão Gerar
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
	focusInstalled bool
	selectedLLM    string
	selectedSD     string
	importInput    textinput.Model
	importingMode  bool
	downloads      map[string]models.DownloadProgress
	downloadChans  map[string]chan models.DownloadProgress
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

	vp := viewport.New(76, 12)
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
		fmt.Sprintf("%d", deps.Config.Image.Steps),
		fmt.Sprintf("%.1f", deps.Config.Image.CFGScale),
		"-1",
		fmt.Sprintf("%d", deps.Config.Image.Width),
		fmt.Sprintf("%d", deps.Config.Image.Height),
		deps.Config.Image.Sampler,
		fmt.Sprintf("%d", deps.Config.Image.BatchCount),
	}
	for i := range imgFields {
		ti := textinput.New()
		ti.SetValue(defaults[i])
		imgFields[i] = ti
	}
	imgFields[0].Focus()

	impIn := textinput.New()
	impIn.Placeholder = "Cole o caminho local (.gguf ou .safetensors) e pressione Enter..."

	var cat []models.CatalogItem
	var inst []models.InstalledModel
	var convs []store.Conversation
	var gal []imagegen.GalleryEntry

	if deps.ModelMgr != nil {
		cat, _ = deps.ModelMgr.LoadOrCreateCatalog()
		inst, _ = deps.ModelMgr.ListInstalled()
	}
	if deps.ConvStore != nil {
		convs, _ = deps.ConvStore.List()
	}
	if deps.DataDir != "" {
		gal, _ = imagegen.ListGallery(filepath.Join(deps.DataDir, "outputs"))
	}

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

	hasBins := deps.BackendMgr != nil && deps.BackendMgr.HasBinaries()
	cols := deps.TermCaps.Cols
	rows := deps.TermCaps.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	m := AppModel{
		deps:           deps,
		width:          cols,
		height:         rows,
		activeTab:      TabChat,
		showSplash:     true,
		showWizard:     !hasBins,
		wizardNote:     fmt.Sprintf("Hardware detectado: %s (%s). Pressione Enter para baixar os binários ou Esc para pular.", deps.Hardware.GPUName, deps.Hardware.SelectedBackend),
		scorchState:    theme.ScorchIdle,
		chatViewport:   vp,
		chatInput:      ta,
		chatSidebar:    true,
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
		downloadChans:  make(map[string]chan models.DownloadProgress),
		downloadCancel: make(map[string]context.CancelFunc),
		logFilter:      "all",
		logViewport:    logVp,
	}
	if deps.SysMon != nil {
		m.metrics = deps.SysMon.Sample()
	}
	m.resizeViewports()
	m.refreshChatViewport(true)
	return m
}

func (m AppModel) Init() tea.Cmd {
	return tickCmd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeViewports()
		m.dirtyRender = true
		return m, nil

	case tickMsg:
		m.scorchFrame++
		if m.deps.SysMon != nil && m.activeTab == TabMonitor {
			m.metrics = m.deps.SysMon.Sample()
		}
		if m.dirtyRender && !m.streaming {
			m.refreshChatViewport(false)
			m.dirtyRender = false
		}
		return m, tickCmd()

	case streamStartedMsg:
		m.streamCh = msg.ch
		m.streamCancel = msg.cancel
		return m, waitNextStreamChunk(msg.ch)

	case streamChunkMsg:
		if msg.Err != nil {
			m.streaming = false
			m.scorchState = theme.ScorchError
			m.errMsg = msg.Err.Error()
			m.refreshChatViewport(true)
			return m, nil
		}
		if msg.Delta != "" {
			m.streamBuf.WriteString(msg.Delta)
			m.dirtyRender = true
			m.refreshChatViewport(true)
		}
		if msg.Done {
			m.streaming = false
			m.scorchState = theme.ScorchSuccess
			finalText := m.streamBuf.String()
			m.streamBuf.Reset()
			if strings.TrimSpace(finalText) != "" {
				m.activeConv.Messages = append(m.activeConv.Messages, llm.Message{
					Role:    "assistant",
					Content: finalText,
				})
				if m.deps.ConvStore != nil {
					_ = m.deps.ConvStore.Save(m.activeConv)
					m.conversations, _ = m.deps.ConvStore.List()
				}
			}
			m.refreshChatViewport(true)
			return m, nil
		}
		return m, waitNextStreamChunk(m.streamCh)

	case sdProgressMsg:
		m.imgProgress = msg.pct
		if msg.err != nil {
			m.imgGenerating = false
			m.scorchState = theme.ScorchError
			m.errMsg = msg.err.Error()
			return m, nil
		}
		if msg.done {
			m.imgGenerating = false
			m.scorchState = theme.ScorchSuccess
			m.lastImagePath = msg.path
			m.statusMsg = "Imagem gerada: " + filepath.Base(msg.path)
			if m.deps.DataDir != "" {
				m.gallery, _ = imagegen.ListGallery(filepath.Join(m.deps.DataDir, "outputs"))
			}
			view, err := render.RenderImageFile(msg.path, m.deps.TermCaps.EffectiveImageMode, m.deps.TermCaps.InTmux, 48, 16)
			if err == nil {
				m.lastImageView = view
			}
		}
		return m, nil

	case downloadStartedMsg:
		m.downloadChans[msg.id] = msg.ch
		m.downloadCancel[msg.id] = msg.cancel
		return m, waitNextDownloadProgress(msg.id, msg.ch)

	case downloadProgressMsg:
		m.downloads[msg.ID] = models.DownloadProgress(msg)
		if msg.Done {
			delete(m.downloadCancel, msg.ID)
			delete(m.downloadChans, msg.ID)
			if msg.Err != nil {
				m.errMsg = "Erro no download: " + msg.Err.Error()
				m.statusMsg = ""
			} else if m.deps.ModelMgr != nil {
				m.installed, _ = m.deps.ModelMgr.ListInstalled()
				for _, im := range m.installed {
					if im.Kind == models.KindLLM && m.selectedLLM == "" {
						m.selectedLLM = im.Path
					}
					if im.Kind == models.KindCheckpoint && m.selectedSD == "" {
						m.selectedSD = im.Path
					}
				}
				m.statusMsg = "Download concluído: " + msg.Name
			}
			return m, nil
		}
		if ch, ok := m.downloadChans[msg.ID]; ok {
			return m, waitNextDownloadProgress(msg.ID, ch)
		}
		return m, nil

	case wizardDoneMsg:
		m.wizardBusy = false
		if msg.err != nil {
			m.wizardNote = "Falha ao baixar binários: " + msg.err.Error()
		} else {
			m.showWizard = false
			m.statusMsg = "Binários instalados com sucesso."
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	if m.activeTab == TabChat && !m.showSplash && !m.showWizard && !m.showHelp && !m.chatParams {
		m.chatInput, cmd = m.chatInput.Update(msg)
	}
	return m, cmd
}

func (m *AppModel) resizeViewports() {
	sidebarW := 0
	if m.chatSidebar && m.width >= 100 {
		sidebarW = 28
	}
	w := m.width - sidebarW - 6
	if w < 40 {
		w = 40
	}
	h := m.height - 14
	if h < 6 {
		h = 6
	}
	m.chatViewport.Width = w
	m.chatViewport.Height = h
	m.logViewport.Width = m.width - 6
	m.logViewport.Height = h
}

func (m *AppModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.showSplash {
		if key == "enter" || key == " " || key == "esc" {
			m.showSplash = false
		}
		return m, nil
	}

	if m.showWizard {
		switch key {
		case "esc":
			m.showWizard = false
		case "enter":
			if !m.wizardBusy && m.deps.ModelMgr != nil {
				m.wizardBusy = true
				m.wizardNote = "Baixando binários do backend (llama-server e sd)..."
				backendName := string(m.deps.Hardware.SelectedBackend)
				mgr := m.deps.ModelMgr
				return m, func() tea.Msg {
					err := mgr.InstallBackendBinaries(context.Background(), backendName, nil)
					return wizardDoneMsg{err: err}
				}
			}
		}
		return m, nil
	}

	if m.showHelp {
		if key == "?" || key == "esc" || key == "enter" {
			m.showHelp = false
		}
		return m, nil
	}

	if m.confirmQuit {
		switch strings.ToLower(key) {
		case "y", "s", "enter":
			if m.deps.BackendMgr != nil {
				m.deps.BackendMgr.Shutdown()
			}
			return m, tea.Quit
		case "n", "esc":
			m.confirmQuit = false
		}
		return m, nil
	}

	if key == "ctrl+c" {
		m.confirmQuit = true
		return m, nil
	}

	isTypingText := (m.activeTab == TabChat && !m.chatParams) ||
		(m.activeTab == TabImages && m.imgFocusIdx < len(m.imgInputs)) ||
		(m.activeTab == TabModels && m.importingMode)

	if !isTypingText {
		switch key {
		case "q":
			m.confirmQuit = true
			return m, nil
		case "f1", "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "1":
			m.activeTab = TabChat
			return m, nil
		case "2":
			m.activeTab = TabImages
			return m, nil
		case "3":
			m.activeTab = TabModels
			return m, nil
		case "4":
			m.activeTab = TabMonitor
			if m.deps.SysMon != nil {
				m.metrics = m.deps.SysMon.Sample()
			}
			return m, nil
		case "5":
			m.activeTab = TabLogs
			m.refreshLogViewport()
			return m, nil
		}
	} else if key == "f1" {
		m.showHelp = !m.showHelp
		return m, nil
	}

	switch m.activeTab {
	case TabChat:
		return m.handleChatKey(msg)
	case TabImages:
		return m.handleImagesKey(msg)
	case TabModels:
		return m.handleModelsKey(msg)
	case TabLogs:
		switch key {
		case "f":
			switch m.logFilter {
			case "all":
				m.logFilter = "llama-server"
			case "llama-server":
				m.logFilter = "sd"
			default:
				m.logFilter = "all"
			}
			m.refreshLogViewport()
		case "up", "k":
			m.logViewport.LineUp(1)
		case "down", "j":
			m.logViewport.LineDown(1)
		}
	}
	return m, nil
}

func (m *AppModel) handleChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.renamingConv {
		switch key {
		case "enter":
			title := strings.TrimSpace(m.renameInput.Value())
			if title != "" && m.deps.ConvStore != nil {
				_ = m.deps.ConvStore.Rename(m.activeConv.ID, title)
				m.activeConv.Title = title
				m.conversations, _ = m.deps.ConvStore.List()
			}
			m.renamingConv = false
			return m, nil
		case "esc":
			m.renamingConv = false
			return m, nil
		}
		var cmd tea.Cmd
		m.renameInput, cmd = m.renameInput.Update(msg)
		return m, cmd
	}

	if m.attachingImg {
		switch key {
		case "enter":
			p := strings.TrimSpace(strings.Trim(m.attachInput.Value(), `"'`))
			if p != "" {
				m.attachedImg = p
				m.statusMsg = "Imagem anexada: " + filepath.Base(p)
			}
			m.attachingImg = false
			return m, nil
		case "esc":
			m.attachingImg = false
			return m, nil
		}
		var cmd tea.Cmd
		m.attachInput, cmd = m.attachInput.Update(msg)
		return m, cmd
	}

	if m.chatParams {
		inputs := []*textinput.Model{&m.systemPrompt, &m.tempInput, &m.topPInput, &m.maxTokInput}
		switch key {
		case "esc", "ctrl+p":
			m.chatParams = false
			m.chatInput.Focus()
			return m, nil
		case "tab", "down":
			inputs[m.paramFocusIdx].Blur()
			m.paramFocusIdx = (m.paramFocusIdx + 1) % len(inputs)
			inputs[m.paramFocusIdx].Focus()
			return m, nil
		case "shift+tab", "up":
			inputs[m.paramFocusIdx].Blur()
			m.paramFocusIdx = (m.paramFocusIdx - 1 + len(inputs)) % len(inputs)
			inputs[m.paramFocusIdx].Focus()
			return m, nil
		}
		var cmd tea.Cmd
		*inputs[m.paramFocusIdx], cmd = inputs[m.paramFocusIdx].Update(msg)
		return m, cmd
	}

	switch key {
	case "alt+1", "ctrl+1":
		m.activeTab = TabChat
		return m, nil
	case "alt+2", "ctrl+2":
		m.activeTab = TabImages
		return m, nil
	case "alt+3", "ctrl+3":
		m.activeTab = TabModels
		return m, nil
	case "alt+4", "ctrl+4":
		m.activeTab = TabMonitor
		if m.deps.SysMon != nil {
			m.metrics = m.deps.SysMon.Sample()
		}
		return m, nil
	case "alt+5", "ctrl+5":
		m.activeTab = TabLogs
		m.refreshLogViewport()
		return m, nil
	case "ctrl+n":
		m.activeConv = store.Conversation{
			ID:    time.Now().Format("20060102-150405"),
			Title: "Nova Conversa",
		}
		m.statusMsg = "Novo chat iniciado."
		m.refreshChatViewport(true)
		return m, nil
	case "ctrl+h":
		m.chatSidebar = !m.chatSidebar
		m.resizeViewports()
		return m, nil
	case "ctrl+up":
		if len(m.conversations) > 0 && m.convIdx > 0 {
			m.convIdx--
			m.activeConv = m.conversations[m.convIdx]
			m.refreshChatViewport(true)
		}
		return m, nil
	case "ctrl+down":
		if len(m.conversations) > 0 && m.convIdx < len(m.conversations)-1 {
			m.convIdx++
			m.activeConv = m.conversations[m.convIdx]
			m.refreshChatViewport(true)
		}
		return m, nil
	case "ctrl+p":
		m.chatParams = true
		m.paramFocusIdx = 0
		m.chatInput.Blur()
		m.systemPrompt.Focus()
		return m, nil
	case "ctrl+r":
		m.renamingConv = true
		m.renameInput.SetValue(m.activeConv.Title)
		m.renameInput.Focus()
		return m, nil
	case "ctrl+a":
		m.attachingImg = true
		m.attachInput.SetValue("")
		m.attachInput.Focus()
		return m, nil
	case "ctrl+y":
		if len(m.activeConv.Messages) > 0 {
			last := m.activeConv.Messages[len(m.activeConv.Messages)-1]
			fallback := CopyToClipboardOSC52(m.deps.DataDir, last.Content)
			m.statusMsg = "Copiado (OSC 52 + " + filepath.Base(fallback) + ")"
		}
		return m, nil
	case "ctrl+s":
		if m.streaming && m.streamCancel != nil {
			m.streamCancel()
			m.streaming = false
			m.scorchState = theme.ScorchIdle
			m.statusMsg = "Geração interrompida pelo usuário."
		}
		return m, nil
	case "enter":
		if m.streaming {
			return m, nil
		}
		prompt := strings.TrimSpace(m.chatInput.Value())
		if prompt == "" {
			return m, nil
		}
		m.chatInput.Reset()
		m.errMsg = ""
		m.scorchState = theme.ScorchThinking

		if len(m.activeConv.Messages) == 0 && (m.activeConv.Title == "Sessão Inicial" || m.activeConv.Title == "Nova Conversa") {
			title := prompt
			if len(title) > 28 {
				title = title[:28] + "..."
			}
			m.activeConv.Title = title
		}

		userMsg := llm.Message{
			Role:      "user",
			Content:   prompt,
			ImagePath: m.attachedImg,
		}
		m.attachedImg = ""
		m.activeConv.Messages = append(m.activeConv.Messages, userMsg)
		if m.deps.ConvStore != nil {
			_ = m.deps.ConvStore.Save(m.activeConv)
			m.conversations, _ = m.deps.ConvStore.List()
		}
		m.refreshChatViewport(true)
		m.streaming = true
		m.streamBuf.Reset()

		return m, m.startChatStreamCmd()
	}

	var cmd tea.Cmd
	m.chatInput, cmd = m.chatInput.Update(msg)
	return m, cmd
}

func (m *AppModel) startChatStreamCmd() tea.Cmd {
	backendMgr := m.deps.BackendMgr
	modelPath := m.selectedLLM
	sysPrompt := m.systemPrompt.Value()
	history := append([]llm.Message(nil), m.activeConv.Messages...)
	temp, _ := strconv.ParseFloat(m.tempInput.Value(), 64)
	if temp <= 0 {
		temp = 0.7
	}
	topP, _ := strconv.ParseFloat(m.topPInput.Value(), 64)
	if topP <= 0 {
		topP = 0.9
	}
	maxTok, _ := strconv.Atoi(m.maxTokInput.Value())
	if maxTok <= 0 {
		maxTok = 1024
	}

	var mmproj string
	for _, im := range m.installed {
		if im.Path == modelPath {
			mmproj = im.MmprojPath
			break
		}
	}

	return func() tea.Msg {
		if backendMgr == nil {
			return streamChunkMsg{Err: fmt.Errorf("gerenciador de backend indisponível"), Done: true}
		}
		if modelPath == "" {
			return streamChunkMsg{Err: fmt.Errorf("nenhum modelo LLM (.gguf) selecionado na aba [3] Modelos"), Done: true}
		}
		ctx, cancel := context.WithCancel(context.Background())
		endpoint, err := backendMgr.StartLlamaServer(ctx, modelPath, mmproj)
		if err != nil {
			cancel()
			return streamChunkMsg{Err: err, Done: true}
		}
		ch := make(chan llm.StreamChunk, 32)
		go llm.StreamChatCompletion(ctx, endpoint, sysPrompt, history, llm.ChatParams{
			Temperature: temp,
			TopP:        topP,
			MaxTokens:   maxTok,
		}, ch)
		return streamStartedMsg{ch: ch, cancel: cancel}
	}
}

func waitNextStreamChunk(ch <-chan llm.StreamChunk) tea.Cmd {
	return func() tea.Msg {
		chunk, ok := <-ch
		if !ok {
			return streamChunkMsg{Done: true}
		}
		return streamChunkMsg(chunk)
	}
}

func waitNextDownloadProgress(id string, ch <-chan models.DownloadProgress) tea.Cmd {
	return func() tea.Msg {
		prog, ok := <-ch
		if !ok {
			return downloadProgressMsg{ID: id, Done: true}
		}
		return downloadProgressMsg(prog)
	}
}

func (m *AppModel) handleImagesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	totalFocus := len(m.imgInputs) + 1 // 0..8 = campos, 9 = botão Gerar

	switch key {
	case "tab", "down":
		if m.imgFocusIdx < len(m.imgInputs) {
			m.imgInputs[m.imgFocusIdx].Blur()
		}
		m.imgFocusIdx = (m.imgFocusIdx + 1) % totalFocus
		if m.imgFocusIdx < len(m.imgInputs) {
			m.imgInputs[m.imgFocusIdx].Focus()
		}
		return m, nil
	case "shift+tab", "up":
		if m.imgFocusIdx < len(m.imgInputs) {
			m.imgInputs[m.imgFocusIdx].Blur()
		}
		m.imgFocusIdx = (m.imgFocusIdx - 1 + totalFocus) % totalFocus
		if m.imgFocusIdx < len(m.imgInputs) {
			m.imgInputs[m.imgFocusIdx].Focus()
		}
		return m, nil
	case "ctrl+x":
		if m.imgGenerating && m.imgCancel != nil {
			m.imgCancel()
			m.imgGenerating = false
			m.statusMsg = "Geração de imagem cancelada."
		}
		return m, nil
	case "ctrl+o":
		if m.lastImagePath != "" {
			_ = render.OpenExternally(m.lastImagePath)
		}
		return m, nil
	case "ctrl+g":
		return m.triggerImageGeneration()
	case "enter":
		if m.imgFocusIdx == len(m.imgInputs) {
			return m.triggerImageGeneration()
		}
		if m.imgFocusIdx < len(m.imgInputs) {
			m.imgInputs[m.imgFocusIdx].Blur()
		}
		m.imgFocusIdx = (m.imgFocusIdx + 1) % totalFocus
		if m.imgFocusIdx < len(m.imgInputs) {
			m.imgInputs[m.imgFocusIdx].Focus()
		}
		return m, nil
	}

	if m.imgFocusIdx < len(m.imgInputs) {
		var cmd tea.Cmd
		m.imgInputs[m.imgFocusIdx], cmd = m.imgInputs[m.imgFocusIdx].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *AppModel) triggerImageGeneration() (tea.Model, tea.Cmd) {
	if m.imgGenerating {
		return m, nil
	}
	if m.selectedSD == "" {
		m.errMsg = "Selecione um checkpoint (.safetensors/.ckpt) na aba [3] Modelos."
		return m, nil
	}
	steps, _ := strconv.Atoi(m.imgInputs[2].Value())
	if steps <= 0 {
		steps = 20
	}
	cfgScale, _ := strconv.ParseFloat(m.imgInputs[3].Value(), 64)
	if cfgScale <= 0 {
		cfgScale = 7.0
	}
	seed, _ := strconv.ParseInt(m.imgInputs[4].Value(), 10, 64)
	if seed < 0 {
		seed = time.Now().UnixNano() % 1_000_000_000
	}
	w, _ := strconv.Atoi(m.imgInputs[5].Value())
	if w <= 0 {
		w = 512
	}
	h, _ := strconv.Atoi(m.imgInputs[6].Value())
	if h <= 0 {
		h = 512
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
		Width:          w,
		Height:         h,
		Sampler:        m.imgInputs[7].Value(),
		BatchCount:     batch,
	}

	outDir := filepath.Join(m.deps.DataDir, "outputs")
	_ = os.MkdirAll(outDir, 0o755)
	outPath := filepath.Join(outDir, fmt.Sprintf("img_%s.png", time.Now().Format("20060102_150405")))
	args := imagegen.BuildSDArgs(params, outPath)

	ctx, cancel := context.WithCancel(context.Background())
	m.imgCancel = cancel
	m.imgGenerating = true
	m.imgProgress = 0
	m.scorchState = theme.ScorchThinking
	m.errMsg = ""
	m.statusMsg = "Gerando imagem..."

	mgr := m.deps.BackendMgr
	return m, func() tea.Msg {
		if mgr == nil {
			return sdProgressMsg{done: true, err: fmt.Errorf("backend indisponível")}
		}
		err := mgr.RunStableDiffusion(ctx, args, nil)
		if err != nil {
			return sdProgressMsg{done: true, err: err}
		}
		_, _ = imagegen.SaveMetadata(outPath, params)
		return sdProgressMsg{pct: 1.0, done: true, path: outPath}
	}
}

func (m *AppModel) handleModelsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.importingMode {
		switch key {
		case "enter":
			raw := m.importInput.Value()
			m.importingMode = false
			m.importInput.Reset()
			if m.deps.ModelMgr != nil {
				inst, err := m.deps.ModelMgr.ImportLocalModel(raw)
				if err != nil {
					m.errMsg = err.Error()
				} else {
					m.errMsg = ""
					m.statusMsg = "Modelo importado: " + inst.Name
					m.installed, _ = m.deps.ModelMgr.ListInstalled()
					if inst.Kind == models.KindLLM {
						m.selectedLLM = inst.Path
					} else if inst.Kind == models.KindCheckpoint {
						m.selectedSD = inst.Path
					}
				}
			}
			return m, nil
		case "esc":
			m.importingMode = false
			return m, nil
		}
		var cmd tea.Cmd
		m.importInput, cmd = m.importInput.Update(msg)
		return m, cmd
	}

	switch key {
	case "tab":
		m.focusInstalled = !m.focusInstalled
	case "up", "k":
		if !m.focusInstalled && m.catalogIdx > 0 {
			m.catalogIdx--
		} else if m.focusInstalled && m.installedIdx > 0 {
			m.installedIdx--
		}
	case "down", "j":
		if !m.focusInstalled && m.catalogIdx < len(m.catalog)-1 {
			m.catalogIdx++
		} else if m.focusInstalled && m.installedIdx < len(m.installed)-1 {
			m.installedIdx++
		}
	case "i":
		m.importingMode = true
		m.importInput.Focus()
	case "d", "delete":
		if m.focusInstalled && len(m.installed) > 0 && m.installedIdx < len(m.installed) && m.deps.ModelMgr != nil {
			target := m.installed[m.installedIdx]
			if freed, err := m.deps.ModelMgr.DeleteModel(target.Path); err == nil {
				m.statusMsg = fmt.Sprintf("Modelo removido (%d MB liberados).", freed/(1024*1024))
				m.installed, _ = m.deps.ModelMgr.ListInstalled()
				if m.installedIdx >= len(m.installed) && m.installedIdx > 0 {
					m.installedIdx--
				}
			} else {
				m.errMsg = err.Error()
			}
		}
	case "enter":
		if m.focusInstalled {
			if len(m.installed) > 0 && m.installedIdx < len(m.installed) {
				sel := m.installed[m.installedIdx]
				if sel.Kind == models.KindLLM {
					m.selectedLLM = sel.Path
					m.statusMsg = "LLM ativo: " + sel.Name
				} else {
					m.selectedSD = sel.Path
					m.statusMsg = "Checkpoint SD ativo: " + sel.Name
				}
			}
			return m, nil
		}
		if len(m.catalog) > 0 && m.catalogIdx < len(m.catalog) && m.deps.ModelMgr != nil {
			item := m.catalog[m.catalogIdx]
			if _, exists := m.downloadCancel[item.ID]; exists {
				m.statusMsg = "Download já em andamento: " + item.Name
				return m, nil
			}
			m.errMsg = ""
			m.statusMsg = "Iniciando download: " + item.Name
			m.downloads[item.ID] = models.DownloadProgress{
				ID:   item.ID,
				Name: item.Name,
			}
			ctx, cancel := context.WithCancel(context.Background())
			ch := make(chan models.DownloadProgress, 16)
			mgr := m.deps.ModelMgr
			go func() {
				err := mgr.DownloadWithResume(ctx, item, func(p models.DownloadProgress) {
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
				close(ch)
			}()
			return m, func() tea.Msg {
				return downloadStartedMsg{id: item.ID, ch: ch, cancel: cancel}
			}
		}
	}
	return m, nil
}

func (m *AppModel) refreshChatViewport(scrollBottom bool) {
	var sb strings.Builder
	for _, msg := range m.activeConv.Messages {
		roleLabel := "Você"
		if msg.Role == "assistant" {
			roleLabel = "Assistente"
		}
		sb.WriteString(fmt.Sprintf("**%s:** %s\n\n", roleLabel, msg.Content))
	}
	if m.streaming && m.streamBuf.Len() > 0 {
		sb.WriteString(fmt.Sprintf("**Assistente:** %s▌\n", m.streamBuf.String()))
	}
	rendered := sb.String()
	if r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(maxInt(40, m.chatViewport.Width-4))); err == nil {
		if out, err := r.Render(rendered); err == nil {
			rendered = out
		}
	}
	m.chatViewport.SetContent(rendered)
	if scrollBottom {
		m.chatViewport.GotoBottom()
	}
}

func (m *AppModel) refreshLogViewport() {
	if m.deps.BackendMgr == nil {
		m.logViewport.SetContent("Nenhum log disponível.")
		return
	}
	entries := m.deps.BackendMgr.Logs().Filter(m.logFilter)
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", e.Process, e.Line))
	}
	m.logViewport.SetContent(sb.String())
	m.logViewport.GotoBottom()
}

func (m AppModel) View() string {
	if m.width < 80 || m.height < 24 {
		return fmt.Sprintf("Terminal muito pequeno (%dx%d). Redimensione para pelo menos 80x24.", m.width, m.height)
	}

	pal := theme.PaletteFor(m.deps.Config.UI.Theme, m.deps.TermCaps.ColorDepth, m.deps.Config.UI.ASCII)

	if m.showSplash {
		return lipgloss.NewStyle().Padding(2, 4).Render(
			fmt.Sprintf("%s\n\nBackend: %s | GPU: %s\n\nPressione Enter para continuar.",
				pal.Accent.Render("Local AI Studio TUI"), m.deps.Hardware.SelectedBackend, m.deps.Hardware.GPUName),
		)
	}

	if m.showWizard {
		return lipgloss.NewStyle().Padding(2, 4).Render(
			fmt.Sprintf("%s\n\n%s\n\n%s   [Esc] Pular",
				pal.Accent.Render("Configuração Inicial de Binários"),
				m.wizardNote,
				pal.Selected.Render("[Enter] Baixar binários")),
		)
	}

	if m.showHelp {
		return lipgloss.NewStyle().Padding(2, 4).Render(
			pal.Accent.Render("Atalhos Principais") + "\n\n" +
				"1..5 (ou Alt+1..5): Alternar abas (Chat, Imagens, Modelos, Monitor, Logs)\n" +
				"Chat: Ctrl+N Novo chat | Ctrl+H Histórico | Ctrl+↑/↓ Trocar chat | Ctrl+P Parâmetros | Ctrl+R Renomear | Ctrl+A Anexar | Ctrl+Y Copiar | Ctrl+S Parar\n" +
				"Imagens: ↑/↓/Tab Navegar campos | Ctrl+G Gerar imagem | Ctrl+X Cancelar | Ctrl+O Abrir\n" +
				"Modelos: ↑/↓ Navegar | Enter Baixar/Selecionar | Tab Alternar lista | i Importar local | d Excluir\n" +
				"Geral: ? ou F1 Ajuda | Ctrl+C Sair",
		)
	}

	if m.confirmQuit {
		return lipgloss.NewStyle().Padding(2, 4).Render(
			pal.Selected.Render("Encerrar Local AI Studio e finalizar processos ativos? [y/N]"),
		)
	}

	tabs := []string{"[1] Chat", "[2] Imagens", "[3] Modelos", "[4] Monitor", "[5] Logs"}
	var renderedTabs []string
	for i, t := range tabs {
		if TabID(i) == m.activeTab {
			renderedTabs = append(renderedTabs, pal.ActiveTab.Render(t))
		} else {
			renderedTabs = append(renderedTabs, pal.InactiveTab.Render(t))
		}
	}
	header := lipgloss.JoinHorizontal(lipgloss.Left, renderedTabs...)

	var body string
	switch m.activeTab {
	case TabChat:
		toolbar := pal.Muted.Render("[Ctrl+N] Novo Chat  [Ctrl+H] Histórico  [Ctrl+↑/↓] Alternar  [Ctrl+P] Parâmetros  [Ctrl+R] Renomear  [Ctrl+A] Anexar  [Ctrl+Y] Copiar")
		mainChat := fmt.Sprintf("%s\n\n%s", m.chatViewport.View(), m.chatInput.View())
		if m.renamingConv {
			mainChat = "Renomear conversa: " + m.renameInput.View() + "\n\n" + mainChat
		}
		if m.attachingImg {
			mainChat = "Anexar imagem: " + m.attachInput.View() + "\n\n" + mainChat
		}
		if m.chatParams {
			pLabels := []string{"System Prompt", "Temperature", "Top-P", "Max Tokens"}
			pInputs := []textinput.Model{m.systemPrompt, m.tempInput, m.topPInput, m.maxTokInput}
			var pLines []string
			pLines = append(pLines, pal.Accent.Render("Parâmetros do Chat (↑/↓/Tab navegar, Esc fechar):"))
			for i, lbl := range pLabels {
				prefix := "  "
				if i == m.paramFocusIdx {
					prefix = pal.Selected.Render("> ")
				}
				pLines = append(pLines, fmt.Sprintf("%s%s: %s", prefix, lbl, pInputs[i].View()))
			}
			mainChat = strings.Join(pLines, "\n") + "\n\n" + mainChat
		}

		if m.chatSidebar && m.width >= 100 {
			var sideLines []string
			sideLines = append(sideLines, pal.Selected.Render("+ Novo Chat [Ctrl+N]"))
			sideLines = append(sideLines, pal.Muted.Render("Conversas Recentes:"))
			if len(m.conversations) == 0 {
				sideLines = append(sideLines, pal.Muted.Render("  (nenhuma salva)"))
			}
			for i, c := range m.conversations {
				if i >= 12 {
					break
				}
				title := c.Title
				if len(title) > 20 {
					title = title[:20] + "…"
				}
				if c.ID == m.activeConv.ID {
					sideLines = append(sideLines, pal.Selected.Render("> "+title))
				} else {
					sideLines = append(sideLines, "  "+title)
				}
			}
			sidebarBox := lipgloss.NewStyle().
				Width(26).
				Border(pal.Border).
				Padding(0, 1).
				Render(strings.Join(sideLines, "\n"))
			body = lipgloss.JoinVertical(lipgloss.Left, toolbar, lipgloss.JoinHorizontal(lipgloss.Top, sidebarBox, "  ", mainChat))
		} else {
			body = lipgloss.JoinVertical(lipgloss.Left, toolbar, mainChat)
		}

	case TabImages:
		fieldLabels := []string{
			"Prompt",
			"Negative Prompt",
			"Passos (Steps)",
			"CFG Scale",
			"Seed (-1 aleatório)",
			"Largura (Width)",
			"Altura (Height)",
			"Sampler",
			"Batch Count",
		}
		var formLines []string
		formLines = append(formLines, pal.Muted.Render("Navegue com ↑/↓ ou Tab • Gere com [Ctrl+G] ou Enter no botão abaixo:"))
		for i, lbl := range fieldLabels {
			prefix := "  "
			labelStyled := lbl
			if m.imgFocusIdx == i {
				prefix = pal.Selected.Render("> ")
				labelStyled = pal.Selected.Render(lbl)
			}
			formLines = append(formLines, fmt.Sprintf("%s%s: %s", prefix, labelStyled, m.imgInputs[i].View()))
		}
		genBtn := "[ Gerar Imagem ]"
		if m.imgFocusIdx == len(m.imgInputs) {
			genBtn = pal.ActiveTab.Render("[ Gerar Imagem ]")
		} else {
			genBtn = pal.Selected.Render("[ Gerar Imagem ]")
		}
		formLines = append(formLines, "", "  "+genBtn+fmt.Sprintf("   Progresso: %.0f%%", m.imgProgress*100))
		if m.lastImageView != "" {
			formLines = append(formLines, "", m.lastImageView)
		}
		body = strings.Join(formLines, "\n")

	case TabModels:
		var lines []string
		lines = append(lines, "Catálogo disponível ([Enter] baixar, [i] importar local, [Tab] alternar lista):")
		for idx, item := range m.catalog {
			isSel := !m.focusInstalled && idx == m.catalogIdx
			line := fmt.Sprintf("%s (%s)", item.Name, item.SizeHuman)
			if prog, ok := m.downloads[item.ID]; ok && !prog.Done {
				pct := 0.0
				if prog.Total > 0 {
					pct = float64(prog.Downloaded) * 100 / float64(prog.Total)
				}
				line += fmt.Sprintf("  [Baixando: %.1f%% | %.1f MB/s]", pct, prog.SpeedBPS/(1024*1024))
			}
			if isSel {
				lines = append(lines, pal.Selected.Render("> "+line))
			} else {
				lines = append(lines, "  "+line)
			}
		}

		lines = append(lines, "", fmt.Sprintf("Modelos Instalados (%d) — [Enter] ativar, [d] remover:", len(m.installed)))
		if len(m.installed) == 0 {
			lines = append(lines, pal.Muted.Render("  Nenhum modelo instalado ainda."))
		}
		for idx, im := range m.installed {
			isSel := m.focusInstalled && idx == m.installedIdx
			activeTag := ""
			if im.Path == m.selectedLLM || im.Path == m.selectedSD {
				activeTag = " [ATIVO]"
			}
			line := fmt.Sprintf("%s [%s]%s", im.Name, im.Kind, activeTag)
			if isSel {
				lines = append(lines, pal.Selected.Render("> "+line))
			} else {
				lines = append(lines, "  "+line)
			}
		}

		if m.importingMode {
			lines = append(lines, "\nImportar: "+m.importInput.View())
		}
		body = strings.Join(lines, "\n")

	case TabMonitor:
		gpuLabel := m.metrics.GPUSource
		if gpuLabel == "" {
			gpuLabel = m.deps.Hardware.GPUName
		}
		body = fmt.Sprintf("CPU (%s): %.1f%%\nRAM: %d / %d MB (%.1f%%)\nGPU (%s | Backend: %s): %.1f%% | VRAM: %d / %d MB",
			m.deps.Hardware.CPUName, m.metrics.CPUPercent,
			m.metrics.RAMUsedMB, m.metrics.RAMTotalMB, m.metrics.RAMPercent,
			gpuLabel, m.deps.Hardware.SelectedBackend,
			m.metrics.GPUPercent, m.metrics.VRAMUsedMB, m.metrics.VRAMTotalMB)

	case TabLogs:
		body = fmt.Sprintf("Filtro ([f]): %s\n\n%s", m.logFilter, m.logViewport.View())
	}

	statusLine := m.statusMsg
	if m.errMsg != "" {
		statusLine = pal.Error.Render("Erro: " + m.errMsg)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		body,
		"",
		pal.Muted.Render(statusLine+"  |  [?] Ajuda  [Ctrl+C] Sair"),
	)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
