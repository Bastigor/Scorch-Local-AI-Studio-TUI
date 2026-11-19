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
	if deps.SysMon != nil {
		m.metrics = deps.SysMon.Sample()
	}
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

	case downloadProgressMsg:
		m.downloads[msg.ID] = models.DownloadProgress(msg)
		if msg.Done {
			delete(m.downloadCancel, msg.ID)
			if msg.Err != nil {
				m.errMsg = msg.Err.Error()
			} else if m.deps.ModelMgr != nil {
				m.installed, _ = m.deps.ModelMgr.ListInstalled()
				m.statusMsg = "Download concluído: " + msg.Name
			}
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
	if m.activeTab == TabChat && !m.showSplash && !m.showWizard && !m.showHelp {
		m.chatInput, cmd = m.chatInput.Update(msg)
	}
	return m, cmd
}

func (m *AppModel) resizeViewports() {
	w := m.width - 6
	if w < 40 {
		w = 40
	}
	h := m.height - 12
	if h < 6 {
		h = 6
	}
	m.chatViewport.Width = w
	m.chatViewport.Height = h
	m.logViewport.Width = w
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
				m.wizardNote = "Baixando binários do backend..."
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

	if key == "ctrl+c" || (key == "q" && m.activeTab != TabChat) {
		m.confirmQuit = true
		return m, nil
	}

	switch key {
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

	switch key {
	case "ctrl+n":
		m.activeConv = store.Conversation{
			ID:    time.Now().Format("20060102-150405"),
			Title: "Nova Conversa",
		}
		m.refreshChatViewport(true)
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
			return streamChunkMsg{Err: fmt.Errorf("nenhum modelo LLM (.gguf) selecionado na aba Modelos"), Done: true}
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

func (m *AppModel) handleImagesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "tab":
		m.imgInputs[m.imgFocusIdx].Blur()
		m.imgFocusIdx = (m.imgFocusIdx + 1) % len(m.imgInputs)
		m.imgInputs[m.imgFocusIdx].Focus()
		return m, nil
	case "shift+tab":
		m.imgInputs[m.imgFocusIdx].Blur()
		m.imgFocusIdx = (m.imgFocusIdx - 1 + len(m.imgInputs)) % len(m.imgInputs)
		m.imgInputs[m.imgFocusIdx].Focus()
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
	case "enter":
		if m.imgGenerating {
			return m, nil
		}
		if m.selectedSD == "" {
			m.errMsg = "Selecione um checkpoint (.safetensors/.ckpt) na aba Modelos."
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

	var cmd tea.Cmd
	m.imgInputs[m.imgFocusIdx], cmd = m.imgInputs[m.imgFocusIdx].Update(msg)
	return m, cmd
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
					m.statusMsg = "Modelo importado: " + inst.Name
					m.installed, _ = m.deps.ModelMgr.ListInstalled()
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
	case "up", "k":
		if m.catalogIdx > 0 {
			m.catalogIdx--
		}
	case "down", "j":
		if m.catalogIdx < len(m.catalog)-1 {
			m.catalogIdx++
		}
	case "i":
		m.importingMode = true
		m.importInput.Focus()
	case "enter":
		if len(m.catalog) > 0 && m.catalogIdx < len(m.catalog) && m.deps.ModelMgr != nil {
			item := m.catalog[m.catalogIdx]
			if _, exists := m.downloadCancel[item.ID]; exists {
				return m, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			m.downloadCancel[item.ID] = cancel
			mgr := m.deps.ModelMgr
			return m, func() tea.Msg {
				err := mgr.DownloadWithResume(ctx, item, nil)
				return downloadProgressMsg{
					ID:   item.ID,
					Name: item.Name,
					Done: true,
					Err:  err,
				}
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
			fmt.Sprintf("Local AI Studio TUI\n\nBackend: %s | GPU: %s\n\nPressione Enter para continuar.",
				m.deps.Hardware.SelectedBackend, m.deps.Hardware.GPUName),
		)
	}

	if m.showWizard {
		return lipgloss.NewStyle().Padding(2, 4).Render(
			fmt.Sprintf("Configuração Inicial de Binários\n\n%s\n\n[Enter] Baixar binários   [Esc] Pular", m.wizardNote),
		)
	}

	if m.showHelp {
		return lipgloss.NewStyle().Padding(2, 4).Render(
			"Atalhos Principais\n\n1..5: Alternar abas (Chat, Imagens, Modelos, Monitor, Logs)\nCtrl+N: Nova conversa | Ctrl+R: Renomear | Ctrl+A: Anexar imagem | Ctrl+Y: Copiar\nCtrl+S: Parar chat | Ctrl+X: Cancelar imagem | Ctrl+C: Sair | ? / F1: Ajuda",
		)
	}

	if m.confirmQuit {
		return lipgloss.NewStyle().Padding(2, 4).Render(
			"Encerrar Local AI Studio e finalizar processos ativos? [y/N]",
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
		body = fmt.Sprintf("%s\n\n%s", m.chatViewport.View(), m.chatInput.View())
	case TabImages:
		body = fmt.Sprintf("Prompt: %s\nPassos: %s | CFG: %s | Seed: %s | Tamanho: %sx%s\nProgresso: %.0f%%\n\n%s",
			m.imgInputs[0].View(), m.imgInputs[2].View(), m.imgInputs[3].View(),
			m.imgInputs[4].View(), m.imgInputs[5].View(), m.imgInputs[6].View(),
			m.imgProgress*100, m.lastImageView)
	case TabModels:
		var lines []string
		lines = append(lines, "Catálogo disponível ([Enter] baixar, [i] importar arquivo local):")
		for idx, item := range m.catalog {
			prefix := "  "
			if idx == m.catalogIdx {
				prefix = "> "
			}
			lines = append(lines, fmt.Sprintf("%s%s (%s)", prefix, item.Name, item.SizeHuman))
		}
		lines = append(lines, fmt.Sprintf("\nInstalados: %d | LLM ativo: %s | SD ativo: %s",
			len(m.installed), filepath.Base(m.selectedLLM), filepath.Base(m.selectedSD)))
		if m.importingMode {
			lines = append(lines, "\nImportar: "+m.importInput.View())
		}
		body = strings.Join(lines, "\n")
	case TabMonitor:
		body = fmt.Sprintf("CPU: %.1f%% | RAM: %d / %d MB (%.1f%%)\nGPU (%s): %.1f%% | VRAM: %d / %d MB",
			m.metrics.CPUPercent, m.metrics.RAMUsedMB, m.metrics.RAMTotalMB, m.metrics.RAMPercent,
			m.metrics.GPUSource, m.metrics.GPUPercent, m.metrics.VRAMUsedMB, m.metrics.VRAMTotalMB)
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
