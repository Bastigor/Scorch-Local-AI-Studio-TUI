package tui

import (
	"context"
	"encoding/json"
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
type titleMsg struct {
	convID      string
	provisional string
	title       string
	err         error
}

type AppModel struct {
	deps        AppDeps
	width       int
	height      int
	activeTab   TabID
	showSplash  bool
	showHelp    bool
	confirmQuit bool

	statusMsg   string
	errMsg      string
	scorchState theme.ScorchState
	scorchFrame int

	// Chat state
	chatViewport  viewport.Model
	chatInput     textarea.Model
	chatFocused   bool
	chatSidebar   bool
	showShortcuts bool
	titleConvID   string
	titleDraft    string
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
	streamBuf     *strings.Builder
	reasonBuf     *strings.Builder
	chatStatus    string
	dirtyRender   bool
	md            *mdCache

	// Images state
	imgInputs     []textinput.Model
	imgFocusIdx   int
	imgEditing    bool
	imgGenerating bool
	imgProgress   float64
	imgCancel     context.CancelFunc
	lastImagePath string
	lastImageView string
	gallery       []imagegen.GalleryEntry
	galleryIdx    int

	// Models state (apenas listagem/ativação/importação: baixar modelos é manual)
	installed     []models.InstalledModel
	llmIdx        int
	sdIdx         int
	focusSD       bool
	selectedLLM   string
	selectedSD    string
	importInput   textinput.Model
	importingMode bool

	// Monitor & Logs
	metrics     sysmon.Metrics
	logFilter   string
	logViewport viewport.Model
}

func NewAppModel(deps AppDeps) AppModel {
	ta := textarea.New()
	ta.Placeholder = "Digite sua mensagem... (Enter envia, Esc alterna foco dos menus)"
	ta.Focus()
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.Prompt = ""

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
	// Os campos começam sem foco de edição: os números 1..5 trocam de aba
	// até o usuário pressionar Enter/i para editar um campo.

	impIn := textinput.New()
	impIn.Placeholder = "Cole o caminho local (.gguf ou .safetensors) e pressione Enter..."

	var inst []models.InstalledModel
	var convs []store.Conversation
	var gal []imagegen.GalleryEntry

	if deps.ModelMgr != nil {
		inst, _ = deps.ModelMgr.ListInstalled()
	}
	if deps.ConvStore != nil {
		convs, _ = deps.ConvStore.List()
	}
	if deps.DataDir != "" {
		gal, _ = imagegen.ListGallery(filepath.Join(deps.DataDir, "outputs"))
	}

	selLLM, selSD := loadSelection(deps.DataDir, inst)

	activeConv := store.Conversation{
		ID:    time.Now().Format("20060102-150405"),
		Title: "Sessão Inicial",
	}
	if len(convs) > 0 {
		activeConv = convs[0]
	}

	cols := deps.TermCaps.Cols
	rows := deps.TermCaps.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	m := AppModel{
		deps:          deps,
		width:         cols,
		height:        rows,
		activeTab:     TabChat,
		showSplash:    true,
		scorchState:   theme.ScorchIdle,
		chatViewport:  vp,
		chatInput:     ta,
		chatFocused:   true,
		chatSidebar:   true,
		systemPrompt:  sysIn,
		tempInput:     tempIn,
		topPInput:     topPIn,
		maxTokInput:   maxTokIn,
		attachInput:   attachIn,
		renameInput:   renameIn,
		conversations: convs,
		activeConv:    activeConv,
		imgInputs:     imgFields,
		gallery:       gal,
		installed:     inst,
		selectedLLM:   selLLM,
		selectedSD:    selSD,
		importInput:   impIn,
		streamBuf:     &strings.Builder{},
		reasonBuf:     &strings.Builder{},
		md:            &mdCache{},
		logFilter:     "all",
		logViewport:   logVp,
	}
	if deps.SysMon != nil {
		deps.SysMon.SetFallbackVRAMTotal(deps.Hardware.VRAMMB)
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
		if msg.Status != "" {
			m.chatStatus = msg.Status
			m.statusMsg = msg.Status
			m.refreshChatViewport(true)
			return m, waitNextStreamChunk(m.streamCh)
		}
		if msg.Err != nil {
			m.streaming = false
			m.scorchState = theme.ScorchError
			m.errMsg = msg.Err.Error()
			m.chatStatus = ""
			m.streamBuf.Reset()
			m.reasonBuf.Reset()
			m.refreshChatViewport(true)
			return m, nil
		}
		if msg.Reasoning != "" {
			m.reasonBuf.WriteString(msg.Reasoning)
			m.chatStatus = "O modelo está raciocinando..."
			m.refreshChatViewport(true)
		}
		if msg.Delta != "" {
			m.streamBuf.WriteString(msg.Delta)
			m.refreshChatViewport(true)
		}
		if msg.Done {
			m.streaming = false
			m.chatStatus = ""
			m.statusMsg = ""
			m.streamCancel = nil
			finalText := m.streamBuf.String()
			hadReasoning := m.reasonBuf.Len() > 0
			m.streamBuf.Reset()
			m.reasonBuf.Reset()
			if strings.TrimSpace(finalText) != "" {
				m.scorchState = theme.ScorchSuccess
				m.activeConv.Messages = append(m.activeConv.Messages, llm.Message{
					Role:    "assistant",
					Content: finalText,
				})
				if m.deps.ConvStore != nil {
					_ = m.deps.ConvStore.Save(m.activeConv)
					m.conversations, _ = m.deps.ConvStore.List()
				}
			} else {
				// Antes a resposta vazia era descartada em silêncio.
				m.scorchState = theme.ScorchError
				if hadReasoning {
					m.errMsg = "O modelo gastou todos os tokens raciocinando e não escreveu a resposta. Aumente Max Tokens (Ctrl+P) ou use um modelo sem modo de raciocínio."
				} else {
					m.errMsg = "O modelo não retornou texto. Veja a aba [5] Logs para detalhes do llama-server."
				}
			}
			m.refreshChatViewport(true)
			return m, m.titleCmd()
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

	case titleMsg:
		return m.applyGeneratedTitle(msg), nil

	case tea.KeyMsg:
		mm, cmd := m.handleKey(msg)
		if p, ok := mm.(*AppModel); ok {
			return *p, cmd
		}
		return mm, cmd
	}

	var cmd tea.Cmd
	if m.activeTab == TabChat && !m.showSplash && !m.showHelp && !m.chatParams && m.chatFocused {
		m.chatInput, cmd = m.chatInput.Update(msg)
	}
	return m, cmd
}

func (m *AppModel) resizeViewports() {
	d := m.chatLayout()
	m.chatViewport.Width = d.vpW
	m.chatViewport.Height = d.vpH
	m.chatInput.SetWidth(maxInt(10, d.mainW-4))

	bh := m.bodyHeight()
	m.logViewport.Width = maxInt(10, m.width-4)
	m.logViewport.Height = maxInt(3, bh-2-1)
}

func (m *AppModel) switchTab(tab TabID) {
	m.activeTab = tab
	// Sai do modo de edição dos formulários ao trocar de aba.
	m.stopImageEditing()
	if tab == TabModels && m.deps.ModelMgr != nil {
		// Reescaneia as pastas: o usuário pode ter copiado arquivos manualmente.
		m.installed, _ = m.deps.ModelMgr.ListInstalled()
		m.clampModelIdx()
	}
	if tab == TabMonitor && m.deps.SysMon != nil {
		m.metrics = m.deps.SysMon.Sample()
	} else if tab == TabLogs {
		m.refreshLogViewport()
	}
	m.resizeViewports()
}

func (m *AppModel) stopImageEditing() {
	m.imgEditing = false
	for i := range m.imgInputs {
		m.imgInputs[i].Blur()
	}
}

func (m *AppModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.showSplash {
		if key == "enter" || key == " " || key == "esc" {
			m.showSplash = false
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

	// F2 mostra/oculta a lista de atalhos do chat.
	if key == "f2" && m.activeTab == TabChat {
		m.showShortcuts = !m.showShortcuts
		m.resizeViewports()
		return m, nil
	}

	// Alt+1..5 sempre troca de aba, mesmo com um campo de texto em edição.
	switch key {
	case "alt+1":
		m.switchTab(TabChat)
		return m, nil
	case "alt+2":
		m.switchTab(TabImages)
		return m, nil
	case "alt+3":
		m.switchTab(TabModels)
		return m, nil
	case "alt+4":
		m.switchTab(TabMonitor)
		return m, nil
	case "alt+5":
		m.switchTab(TabLogs)
		return m, nil
	}

	inModalInput := m.activeTab == TabChat && (m.chatParams || m.renamingConv || m.attachingImg)
	chatInputEmpty := m.activeTab == TabChat && !inModalInput && strings.TrimSpace(m.chatInput.Value()) == ""

	// Nas abas de formulário, os números só são tratados como texto enquanto
	// um campo está realmente em edição (imgEditing / importingMode).
	canNavigateGlobal := !inModalInput && (m.activeTab != TabChat || !m.chatFocused || chatInputEmpty) &&
		!(m.activeTab == TabImages && m.imgEditing) &&
		!(m.activeTab == TabModels && m.importingMode)

	if canNavigateGlobal {
		switch key {
		case "q":
			m.confirmQuit = true
			return m, nil
		case "f1", "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "1":
			m.switchTab(TabChat)
			return m, nil
		case "2":
			m.switchTab(TabImages)
			return m, nil
		case "3":
			m.switchTab(TabModels)
			return m, nil
		case "4":
			m.switchTab(TabMonitor)
			return m, nil
		case "5":
			m.switchTab(TabLogs)
			return m, nil
		}
	} else if key == "f1" {
		m.showHelp = !m.showHelp
		return m, nil
	}

	switch m.activeTab {
	case TabChat:
		mm, cmd := m.handleChatKey(msg)
		m.resizeViewports() // painéis de parâmetros/renomear/anexar mudam a altura do chat
		return mm, cmd
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
			m.chatInput.Focus()
			m.chatFocused = true
			return m, nil
		case "esc":
			m.renamingConv = false
			m.chatInput.Focus()
			m.chatFocused = true
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
			m.chatInput.Focus()
			m.chatFocused = true
			return m, nil
		case "esc":
			m.attachingImg = false
			m.chatInput.Focus()
			m.chatFocused = true
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
			m.chatFocused = true
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
	case "esc":
		m.chatFocused = !m.chatFocused
		if m.chatFocused {
			m.chatInput.Focus()
			m.statusMsg = "Foco na caixa de mensagem."
		} else {
			m.chatInput.Blur()
			m.statusMsg = "Foco nos menus (use 1..5 para abas ou i/Enter para digitar)."
		}
		return m, nil
	case "i":
		if !m.chatFocused {
			m.chatFocused = true
			m.chatInput.Focus()
			return m, nil
		}
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
		m.chatFocused = false
		m.systemPrompt.Focus()
		return m, nil
	case "ctrl+r":
		m.renamingConv = true
		m.chatInput.Blur()
		m.chatFocused = false
		m.renameInput.SetValue(m.activeConv.Title)
		m.renameInput.Focus()
		return m, nil
	case "ctrl+a":
		m.attachingImg = true
		m.chatInput.Blur()
		m.chatFocused = false
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
			m.chatStatus = ""
			m.streamBuf.Reset()
			m.reasonBuf.Reset()
			m.scorchState = theme.ScorchIdle
			m.statusMsg = "Geração interrompida pelo usuário."
			m.refreshChatViewport(true)
		}
		return m, nil
	case "enter":
		if !m.chatFocused {
			m.chatFocused = true
			m.chatInput.Focus()
			return m, nil
		}
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
			// O próprio modelo substituirá este título provisório após a 1ª resposta.
			m.titleConvID = m.activeConv.ID
			m.titleDraft = title
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
		m.streaming = true
		m.streamBuf.Reset()
		m.reasonBuf.Reset()
		m.chatStatus = "Preparando o servidor do modelo..."
		m.statusMsg = m.chatStatus
		m.refreshChatViewport(true)

		return m, m.startChatStreamCmd()
	}

	if !m.chatFocused {
		return m, nil
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
		ch := make(chan llm.StreamChunk, 64)
		ctx, cancel := context.WithCancel(context.Background())

		fail := func(err error) {
			ch <- llm.StreamChunk{Err: err, Done: true}
			close(ch)
		}

		// O servidor sobe e a requisição acontece em segundo plano; a interface
		// recebe mensagens de status e nunca fica "muda" enquanto o modelo carrega.
		go func() {
			if backendMgr == nil {
				fail(fmt.Errorf("gerenciador de backend indisponível"))
				return
			}
			if modelPath == "" {
				fail(fmt.Errorf("nenhum modelo LLM (.gguf) selecionado na aba [3] Modelos"))
				return
			}
			endpoint, err := backendMgr.StartLlamaServer(ctx, modelPath, mmproj, func(s string) {
				select {
				case ch <- llm.StreamChunk{Status: s}:
				default:
				}
			})
			if err != nil {
				if ctx.Err() != nil {
					ch <- llm.StreamChunk{Done: true}
					close(ch)
					return
				}
				fail(err)
				return
			}
			llm.StreamChatCompletion(ctx, endpoint, sysPrompt, history, llm.ChatParams{
				Temperature: temp,
				TopP:        topP,
				MaxTokens:   maxTok,
			}, ch)
		}()

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

// imgFocusOrder é a ordem visual dos campos da aba de imagens (o índice 9 é o botão).
var imgFocusOrder = []int{0, 1, 2, 3, 4, 7, 5, 6, 8, 9}

func indexOfInt(list []int, v int) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return 0
}

func (m *AppModel) handleImagesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	order := imgFocusOrder
	pos := indexOfInt(order, m.imgFocusIdx)

	moveTo := func(newPos int) {
		if m.imgFocusIdx < len(m.imgInputs) {
			m.imgInputs[m.imgFocusIdx].Blur()
		}
		m.imgFocusIdx = order[(newPos+len(order))%len(order)]
		if m.imgEditing && m.imgFocusIdx < len(m.imgInputs) {
			m.imgInputs[m.imgFocusIdx].Focus()
		}
	}

	// Atalhos válidos nos dois modos.
	switch key {
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
		m.stopImageEditing()
		return m.triggerImageGeneration()
	}

	if m.imgEditing {
		switch key {
		case "esc", "enter":
			m.stopImageEditing()
			m.statusMsg = "Edição concluída. Use 1..5 para trocar de aba."
			return m, nil
		case "tab", "down":
			moveTo(pos + 1)
			return m, nil
		case "shift+tab", "up":
			moveTo(pos - 1)
			return m, nil
		}
		if m.imgFocusIdx < len(m.imgInputs) {
			var cmd tea.Cmd
			m.imgInputs[m.imgFocusIdx], cmd = m.imgInputs[m.imgFocusIdx].Update(msg)
			return m, cmd
		}
		return m, nil
	}

	// Modo de navegação: nenhum campo captura o teclado.
	switch key {
	case "tab", "down", "right", "j":
		moveTo(pos + 1)
	case "shift+tab", "up", "left", "k":
		moveTo(pos - 1)
	case "enter", "i", "e":
		if m.imgFocusIdx == len(m.imgInputs) {
			return m.triggerImageGeneration()
		}
		m.imgEditing = true
		m.imgInputs[m.imgFocusIdx].Focus()
		m.imgInputs[m.imgFocusIdx].CursorEnd()
		m.statusMsg = "Editando campo. Enter/Esc para terminar."
	case "g":
		return m.triggerImageGeneration()
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
					m.persistSelection()
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

	list := m.modelsOfKind(m.focusSD)
	idx := &m.llmIdx
	if m.focusSD {
		idx = &m.sdIdx
	}

	switch key {
	case "tab":
		m.focusSD = !m.focusSD
	case "up", "k":
		if *idx > 0 {
			*idx--
		}
	case "down", "j":
		if *idx < len(list)-1 {
			*idx++
		}
	case "i":
		m.importingMode = true
		m.importInput.Focus()
	case "r":
		if m.deps.ModelMgr != nil {
			m.installed, _ = m.deps.ModelMgr.ListInstalled()
			m.clampModelIdx()
			m.statusMsg = "Lista de modelos atualizada."
		}
	case "d", "delete":
		if len(list) > 0 && *idx < len(list) && m.deps.ModelMgr != nil {
			target := list[*idx]
			if freed, err := m.deps.ModelMgr.DeleteModel(target.Path); err == nil {
				m.statusMsg = fmt.Sprintf("Modelo removido (%d MB liberados).", freed/(1024*1024))
				m.installed, _ = m.deps.ModelMgr.ListInstalled()
				if m.selectedLLM == target.Path {
					m.selectedLLM = ""
				}
				if m.selectedSD == target.Path {
					m.selectedSD = ""
				}
				m.selectedLLM, m.selectedSD = fillSelection(m.selectedLLM, m.selectedSD, m.installed)
				m.persistSelection()
				m.clampModelIdx()
			} else {
				m.errMsg = err.Error()
			}
		}
	case "enter":
		if len(list) > 0 && *idx < len(list) {
			sel := list[*idx]
			if sel.Kind == models.KindLLM {
				m.selectedLLM = sel.Path
				m.statusMsg = "LLM ativo: " + sel.Name
			} else {
				m.selectedSD = sel.Path
				m.statusMsg = "Checkpoint de imagem ativo: " + sel.Name
			}
			m.errMsg = ""
			m.persistSelection()
		}
	}
	return m, nil
}

// modelsOfKind devolve os modelos instalados de um tipo (LLM ou checkpoint de imagem).
func (m AppModel) modelsOfKind(checkpoints bool) []models.InstalledModel {
	var out []models.InstalledModel
	for _, im := range m.installed {
		if (im.Kind == models.KindCheckpoint) == checkpoints {
			out = append(out, im)
		}
	}
	return out
}

func (m *AppModel) clampModelIdx() {
	if n := len(m.modelsOfKind(false)); m.llmIdx >= n {
		m.llmIdx = maxInt(0, n-1)
	}
	if n := len(m.modelsOfKind(true)); m.sdIdx >= n {
		m.sdIdx = maxInt(0, n-1)
	}
}

// titleCmd pede ao modelo um título para a conversa, apenas uma vez, após a
// primeira resposta. Roda em segundo plano e reaproveita o servidor já iniciado.
func (m *AppModel) titleCmd() tea.Cmd {
	if m.titleConvID == "" || m.titleConvID != m.activeConv.ID || m.deps.BackendMgr == nil || m.selectedLLM == "" {
		return nil
	}
	msgs := m.activeConv.Messages
	var userText, answer string
	for _, mm := range msgs {
		if mm.Role == "user" && userText == "" {
			userText = mm.Content
		}
		if mm.Role == "assistant" && answer == "" {
			answer = mm.Content
		}
	}
	if userText == "" || answer == "" {
		return nil
	}
	convID, draft, modelPath := m.titleConvID, m.titleDraft, m.selectedLLM
	m.titleConvID, m.titleDraft = "", ""
	bm := m.deps.BackendMgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		endpoint, err := bm.StartLlamaServer(ctx, modelPath, "", nil)
		if err != nil {
			return titleMsg{convID: convID, provisional: draft, err: err}
		}
		title, err := llm.GenerateTitle(ctx, endpoint, userText, answer)
		return titleMsg{convID: convID, provisional: draft, title: title, err: err}
	}
}

// applyGeneratedTitle grava o título gerado, sem sobrescrever um título que o
// usuário tenha renomeado manualmente enquanto o modelo trabalhava.
func (m AppModel) applyGeneratedTitle(msg titleMsg) AppModel {
	if msg.err != nil || msg.title == "" || m.deps.ConvStore == nil {
		return m
	}
	convs, _ := m.deps.ConvStore.List()
	for _, c := range convs {
		if c.ID == msg.convID {
			if c.Title != msg.provisional {
				return m
			}
			if err := m.deps.ConvStore.Rename(msg.convID, msg.title); err != nil {
				return m
			}
		}
	}
	if m.activeConv.ID == msg.convID && m.activeConv.Title == msg.provisional {
		m.activeConv.Title = msg.title
	}
	m.conversations, _ = m.deps.ConvStore.List()
	return m
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
	if m.streaming {
		switch {
		case m.streamBuf.Len() > 0:
			sb.WriteString(fmt.Sprintf("**Assistente:** %s▌\n", m.streamBuf.String()))
		case m.reasonBuf.Len() > 0:
			think := m.reasonBuf.String()
			if r := []rune(think); len(r) > 500 {
				think = "…" + string(r[len(r)-500:])
			}
			think = strings.ReplaceAll(strings.TrimSpace(think), "\n", "\n> ")
			sb.WriteString("**Assistente (raciocinando):**\n\n> " + think + "▌\n")
		default:
			status := m.chatStatus
			if status == "" {
				status = "Aguardando resposta..."
			}
			sb.WriteString("*" + status + "*\n")
		}
	}
	style := "dark"
	if strings.EqualFold(m.deps.Config.UI.Theme, "light") {
		style = "light"
	}
	rendered := sb.String()
	if m.md != nil {
		rendered = m.md.render(rendered, style, maxInt(20, m.chatViewport.Width-2))
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

// ---- seleção de modelos persistida entre sessões

type selectionState struct {
	LLM        string `json:"llm"`
	Checkpoint string `json:"checkpoint"`
}

func selectionPath(dataDir string) string {
	return filepath.Join(dataDir, "state.json")
}

// loadSelection restaura os modelos ativos da última sessão e, na falta deles,
// escolhe o primeiro modelo instalado de cada tipo.
func loadSelection(dataDir string, installed []models.InstalledModel) (llmPath, sdPath string) {
	var st selectionState
	if dataDir != "" {
		if raw, err := os.ReadFile(selectionPath(dataDir)); err == nil {
			_ = json.Unmarshal(raw, &st)
		}
	}
	has := func(path string, kind models.ModelKind) bool {
		for _, im := range installed {
			if im.Kind == kind && im.Path == path {
				return true
			}
		}
		return false
	}
	if has(st.LLM, models.KindLLM) {
		llmPath = st.LLM
	}
	if has(st.Checkpoint, models.KindCheckpoint) {
		sdPath = st.Checkpoint
	}
	for _, im := range installed {
		if im.Kind == models.KindLLM && llmPath == "" {
			llmPath = im.Path
		}
		if im.Kind == models.KindCheckpoint && sdPath == "" {
			sdPath = im.Path
		}
	}
	return llmPath, sdPath
}

func (m *AppModel) persistSelection() {
	if m.deps.DataDir == "" {
		return
	}
	raw, err := json.MarshalIndent(selectionState{LLM: m.selectedLLM, Checkpoint: m.selectedSD}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(selectionPath(m.deps.DataDir), raw, 0o644)
}

// mdCache guarda o renderizador Markdown para não recriá-lo a cada trecho recebido.
type mdCache struct {
	r     *glamour.TermRenderer
	width int
	style string
}

func (c *mdCache) render(text, style string, width int) string {
	if c.r == nil || c.width != width || c.style != style {
		// Estilo fixo: glamour.WithAutoStyle consulta o terminal (OSC 11) em plena
		// execução do Bubble Tea e pode travar/consumir teclas.
		r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(width))
		if err != nil {
			return text
		}
		c.r, c.width, c.style = r, width, style
	}
	out, err := c.r.Render(text)
	if err != nil {
		return text
	}
	return out
}

// findInstalled procura o item do catálogo entre os modelos já instalados (pelo nome do arquivo).
// fillSelection completa seleções vazias com o primeiro modelo instalado de cada tipo.
func fillSelection(llmPath, sdPath string, installed []models.InstalledModel) (string, string) {
	for _, im := range installed {
		if im.Kind == models.KindLLM && llmPath == "" {
			llmPath = im.Path
		}
		if im.Kind == models.KindCheckpoint && sdPath == "" {
			sdPath = im.Path
		}
	}
	return llmPath, sdPath
}
