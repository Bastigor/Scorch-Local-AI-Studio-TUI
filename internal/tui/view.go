package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"local-ai-studio-tui/internal/models"
	"local-ai-studio-tui/internal/tui/theme"
)

const (
	headerH = 3
	statusH = 4
	sideW   = 28
)

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// bodyHeight é a altura disponível para o conteúdo da aba (entre cabeçalho e barra de status).
func (m AppModel) bodyHeight() int {
	return maxInt(8, m.height-headerH-statusH)
}

// ---------- caixas com borda ----------

// renderBox desenha um painel com borda e título embutido na borda superior.
// w e h são as dimensões totais (borda incluída); h <= 0 deixa a altura livre.
func renderBox(pal theme.Palette, title, content string, w, h int, focused bool) string {
	if w < 6 {
		w = 6
	}
	bs := pal.Muted
	ts := pal.Selected
	if focused {
		bs = pal.Accent
		ts = pal.Accent
	}
	b := pal.Border
	inner := w - 2

	t := ""
	if title != "" {
		t = " " + title + " "
	}
	if lipgloss.Width(t) > inner-2 {
		t = lipgloss.NewStyle().MaxWidth(maxInt(1, inner-2)).Render(t)
	}
	fill := inner - 1 - lipgloss.Width(t)
	if fill < 0 {
		fill = 0
	}
	top := bs.Render(b.TopLeft+b.Top) + ts.Render(t) + bs.Render(strings.Repeat(b.Top, fill)+b.TopRight)

	if h > 0 {
		// Quebra as linhas longas antes de cortar, para a altura final ser exata.
		content = lipgloss.NewStyle().Width(maxInt(1, inner-2)).Render(content)
		lines := strings.Split(content, "\n")
		if len(lines) > h-2 {
			lines = lines[:maxInt(0, h-2)]
		}
		content = strings.Join(lines, "\n")
	}

	st := lipgloss.NewStyle().
		Border(b, false, true, true, true).
		BorderForeground(bs.GetForeground()).
		Padding(0, 1).
		Width(inner).
		MaxWidth(w)
	if h > 0 {
		st = st.Height(maxInt(0, h-2))
	}
	return top + "\n" + st.Render(content)
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

func inputView(ti textinput.Model, w int) string {
	ti.Prompt = ""
	ti.Width = maxInt(3, w)
	return ti.View()
}

// wrapItems agrupa os itens em linhas de no máximo width colunas.
func wrapItems(items []string, width int, sep string) []string {
	var lines []string
	cur := ""
	for _, it := range items {
		if cur == "" {
			cur = it
			continue
		}
		if lipgloss.Width(cur)+lipgloss.Width(sep)+lipgloss.Width(it) <= width {
			cur += sep + it
		} else {
			lines = append(lines, cur)
			cur = it
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func windowLines(lines []string, sel, h int) []string {
	if h <= 0 || len(lines) <= h {
		return lines
	}
	start := sel - h/2
	if start < 0 {
		start = 0
	}
	if start+h > len(lines) {
		start = len(lines) - h
	}
	return lines[start : start+h]
}

func gauge(pct float64, w int, ascii bool) string {
	if w < 4 {
		w = 4
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct/100*float64(w) + 0.5)
	full, empty := "█", "░"
	if ascii {
		full, empty = "#", "-"
	}
	return strings.Repeat(full, filled) + strings.Repeat(empty, w-filled)
}

func sparkline(vals []float64, w int, ascii bool) string {
	levels := []rune("▁▂▃▄▅▆▇█")
	if ascii {
		levels = []rune(".:-=+*#%")
	}
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	var sb strings.Builder
	for _, v := range vals {
		if v < 0 {
			v = 0
		}
		if v > 100 {
			v = 100
		}
		idx := int(v / 100 * float64(len(levels)-1))
		sb.WriteRune(levels[idx])
	}
	return sb.String()
}

// ---------- layout do chat ----------

func chatShortcutItems() []string {
	return []string{
		"[Esc] Foco Menus/Prompt",
		"[Ctrl+N] Novo Chat",
		"[Ctrl+H] Histórico",
		"[Ctrl+↑/↓] Alternar",
		"[Ctrl+P] Parâmetros",
		"[Ctrl+R] Renomear",
		"[Ctrl+A] Anexar",
		"[Ctrl+Y] Copiar",
		"[Ctrl+S] Parar",
		"[F2] Ocultar atalhos",
	}
}

type chatDims struct {
	sidebarW int
	mainW    int
	lines    []string
	toolbarH int
	inputH   int
	extraH   int
	vpBoxH   int
	vpW      int
	vpH      int
}

// chatLayout calcula tamanhos a partir da largura/altura atuais; a barra de
// atalhos quebra em quantas linhas forem necessárias para caber na janela.
func (m AppModel) chatLayout() chatDims {
	var d chatDims
	w := m.width
	if w <= 0 {
		w = 80
	}
	if m.chatSidebar && w >= 100 {
		d.sidebarW = sideW
	}
	d.mainW = w - d.sidebarW
	if m.showShortcuts {
		d.lines = wrapItems(chatShortcutItems(), maxInt(10, w-4), "  ")
		d.toolbarH = len(d.lines) + 2
	}
	d.inputH = 5
	if m.chatParams {
		d.extraH += 6
	}
	if m.renamingConv {
		d.extraH += 3
	}
	if m.attachingImg {
		d.extraH += 3
	}
	d.vpBoxH = maxInt(4, m.bodyHeight()-d.toolbarH-d.inputH-d.extraH)
	d.vpW = maxInt(10, d.mainW-4)
	d.vpH = d.vpBoxH - 2
	return d
}

// ---------- View ----------

func (m AppModel) statusText(pal theme.Palette) string {
	text := m.statusMsg
	if m.errMsg != "" {
		return pal.Error.Render("Erro: " + m.errMsg)
	}
	if text == "" {
		text = "Pronto."
	}
	return pal.Muted.Render(text)
}

func (m AppModel) View() string {
	if m.width < 80 || m.height < 24 {
		return fmt.Sprintf("Terminal muito pequeno (%dx%d). Redimensione para pelo menos 80x24.", m.width, m.height)
	}

	pal := theme.PaletteFor(m.deps.Config.UI.Theme, m.deps.TermCaps.ColorDepth, m.deps.Config.UI.ASCII)

	center := func(title, content string, w int, focused bool) string {
		w = minInt(w, m.width-2)
		box := renderBox(pal, title, content, w, 0, focused)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}

	if m.showSplash {
		notice := ""
		if m.deps.BackendMgr != nil && !m.deps.BackendMgr.HasBinaries() {
			notice = "\n\n" + pal.Error.Render("Motor de IA não encontrado. Execute o Scorch-Setup antes de usar.")
		}
		return center("", pal.Accent.Render("Scorch Local AI Studio")+"\n\nAperte Enter para continuar"+notice, 60, true)
	}

	if m.showHelp {
		help := "1..5: Alternar abas (Chat, Imagens, Modelos, Monitor, Logs). Alt+1..5 funciona mesmo durante a digitação.\n\n" +
			"Chat: F2 Mostra/oculta atalhos | Esc Alterna foco prompt/menus | Ctrl+N Novo chat | Ctrl+H Histórico | Ctrl+↑/↓ Trocar chat | Ctrl+P Parâmetros | Ctrl+R Renomear | Ctrl+A Anexar | Ctrl+Y Copiar | Ctrl+S Parar\n\n" +
			"Imagens: ↑/↓/Tab Navegar | Enter ou i Editar campo | Enter/Esc Terminar edição | Ctrl+G Gerar | Ctrl+X Cancelar | Ctrl+O Abrir\n\n" +
			"Modelos: ↑/↓ Navegar | Enter Ativar | Tab Alternar LLM/Imagem | i Importar arquivo | r Atualizar lista | d Excluir\n\n" +
			"Geral: ? ou F1 Ajuda | Ctrl+C Sair"
		return center("Atalhos Principais", help, 100, true)
	}

	if m.confirmQuit {
		return center("Sair", pal.Selected.Render("Encerrar Local AI Studio e finalizar processos ativos? [y/N]"), 70, true)
	}

	// ----- cabeçalho -----
	tabs := []string{"[1] Chat", "[2] Imagens", "[3] Modelos", "[4] Monitor", "[5] Logs"}
	var renderedTabs []string
	for i, t := range tabs {
		if TabID(i) == m.activeTab {
			renderedTabs = append(renderedTabs, pal.ActiveTab.Render(t))
		} else {
			renderedTabs = append(renderedTabs, pal.InactiveTab.Render(t))
		}
	}
	header := renderBox(pal, "Scorch Local AI Studio", lipgloss.JoinHorizontal(lipgloss.Left, renderedTabs...), m.width, headerH, false)

	// ----- corpo -----
	var body string
	switch m.activeTab {
	case TabChat:
		body = m.viewChat(pal)
	case TabImages:
		body = m.viewImages(pal)
	case TabModels:
		body = m.viewModels(pal)
	case TabMonitor:
		body = m.viewMonitor(pal)
	case TabLogs:
		body = m.viewLogs(pal)
	}

	// ----- barra de status (2 linhas de texto) -----
	statusInner := m.width - 4
	statusBody := lipgloss.NewStyle().Width(statusInner).MaxWidth(statusInner).Render(m.statusText(pal))
	hintText := "[?] Ajuda  [Alt+1..5] Abas  [Ctrl+C] Sair"
	if m.activeTab == TabChat {
		hintText = "[F2] Atalhos do chat  " + hintText
	}
	hint := pal.Muted.Render(hintText)
	statusLines := strings.Split(statusBody, "\n")
	if len(statusLines) > 1 {
		statusLines = statusLines[:1]
		statusLines[0] = truncate(strings.Split(statusBody, "\n")[0], statusInner)
	}
	status := renderBox(pal, "Status", strings.Join(statusLines, "\n")+"\n"+truncate(hint, statusInner), m.width, statusH, false)

	return lipgloss.JoinVertical(lipgloss.Left, header, body, status)
}

// ---------- Chat ----------

func (m AppModel) viewChat(pal theme.Palette) string {
	d := m.chatLayout()
	bh := m.bodyHeight()

	toolbarText := make([]string, len(d.lines))
	for i, l := range d.lines {
		toolbarText[i] = pal.Muted.Render(l)
	}
	toolbar := ""
	if d.toolbarH > 0 {
		toolbar = renderBox(pal, "Atalhos do chat (F2 oculta)", strings.Join(toolbarText, "\n"), m.width, d.toolbarH, false)
	}

	var parts []string

	if m.chatParams {
		pLabels := []string{"System Prompt", "Temperature", "Top-P", "Max Tokens"}
		pInputs := []textinput.Model{m.systemPrompt, m.tempInput, m.topPInput, m.maxTokInput}
		var pLines []string
		for i, lbl := range pLabels {
			prefix := "  "
			if i == m.paramFocusIdx {
				prefix = pal.Selected.Render("> ")
			}
			pLines = append(pLines, fmt.Sprintf("%s%s: %s", prefix, lbl, inputView(pInputs[i], d.mainW-4-len(lbl)-6)))
		}
		parts = append(parts, renderBox(pal, "Parâmetros do Chat (↑/↓/Tab navegar, Esc fechar)", strings.Join(pLines, "\n"), d.mainW, 6, true))
	}
	if m.renamingConv {
		parts = append(parts, renderBox(pal, "Renomear conversa (Enter confirma, Esc cancela)", inputView(m.renameInput, d.mainW-6), d.mainW, 3, true))
	}
	if m.attachingImg {
		parts = append(parts, renderBox(pal, "Anexar imagem (Enter confirma, Esc cancela)", inputView(m.attachInput, d.mainW-6), d.mainW, 3, true))
	}

	modelName := "sem modelo selecionado"
	if m.selectedLLM != "" {
		modelName = filepath.Base(m.selectedLLM)
	}
	vp := m.chatViewport
	vp.Width = d.vpW
	vp.Height = d.vpH
	convTitle := "Conversa — " + modelName
	parts = append(parts, renderBox(pal, convTitle, vp.View(), d.mainW, d.vpBoxH, !m.chatFocused && !m.chatParams))

	inputTitle := "Mensagem"
	if m.attachedImg != "" {
		inputTitle += " (imagem: " + filepath.Base(m.attachedImg) + ")"
	}
	if m.streaming {
		inputTitle += " — gerando... (Ctrl+S para parar)"
	}
	in := m.chatInput
	in.SetWidth(maxInt(10, d.mainW-4))
	parts = append(parts, renderBox(pal, inputTitle, in.View(), d.mainW, d.inputH, m.chatFocused))

	mainCol := lipgloss.JoinVertical(lipgloss.Left, parts...)

	row := mainCol
	if d.sidebarW > 0 {
		var sideLines []string
		sideLines = append(sideLines, pal.Selected.Render("+ Novo Chat [Ctrl+N]"))
		if len(m.conversations) == 0 {
			sideLines = append(sideLines, pal.Muted.Render("(nenhuma salva)"))
		}
		maxItems := maxInt(1, bh-d.toolbarH-2-2)
		for i, c := range m.conversations {
			if i >= maxItems {
				break
			}
			title := truncate(c.Title, d.sidebarW-6)
			if c.ID == m.activeConv.ID {
				sideLines = append(sideLines, pal.Selected.Render("> "+title))
			} else {
				sideLines = append(sideLines, "  "+title)
			}
		}
		sidebar := renderBox(pal, "Conversas", strings.Join(sideLines, "\n"), d.sidebarW, bh-d.toolbarH, false)
		row = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, mainCol)
	}
	if toolbar == "" {
		return row
	}
	return lipgloss.JoinVertical(lipgloss.Left, toolbar, row)
}

// ---------- Imagens ----------

func (m AppModel) viewImages(pal theme.Palette) string {
	bh := m.bodyHeight()
	leftW := m.width
	previewW := 0
	if m.width >= 110 && m.lastImageView != "" {
		previewW = 52
		leftW = m.width - previewW
	}

	field := func(idx int, title string, w int) string {
		focused := m.imgFocusIdx == idx
		t := title
		if focused && m.imgEditing {
			t += " (editando)"
		}
		return renderBox(pal, t, inputView(m.imgInputs[idx], w-6), w, 3, focused)
	}

	prompt := field(0, "Prompt", leftW)
	neg := field(1, "Negative Prompt", leftW)

	colW := leftW / 4
	lastW := leftW - colW*3
	width := func(i int) int {
		if i == 3 {
			return lastW
		}
		return colW
	}
	row1 := lipgloss.JoinHorizontal(lipgloss.Top,
		field(2, "Steps", width(0)),
		field(3, "CFG Scale", width(1)),
		field(4, "Seed (-1 aleatório)", width(2)),
		field(7, "Sampler", width(3)),
	)

	btnLabel := "[ Gerar ]"
	if m.imgGenerating {
		btnLabel = fmt.Sprintf("Gerando %.0f%%", m.imgProgress*100)
	}
	btnFocused := m.imgFocusIdx == len(m.imgInputs)
	btnText := pal.Selected.Render(btnLabel)
	if btnFocused {
		btnText = pal.ActiveTab.Render(btnLabel)
	}
	row2 := lipgloss.JoinHorizontal(lipgloss.Top,
		field(5, "Largura", width(0)),
		field(6, "Altura", width(1)),
		field(8, "Batch", width(2)),
		renderBox(pal, "Ação", btnText, width(3), 3, btnFocused),
	)

	used := 3 + 3 + 6
	infoH := maxInt(3, bh-used)
	var info []string
	info = append(info, pal.Muted.Render("↑/↓ navegar • Enter editar • Ctrl+G gerar • Ctrl+X cancelar • Ctrl+O abrir"))
	info = append(info, fmt.Sprintf("Checkpoint: %s", baseOrNone(m.selectedSD)))
	info = append(info, fmt.Sprintf("Progresso: %.0f%%  %s", m.imgProgress*100, gauge(m.imgProgress*100, 20, m.deps.Config.UI.ASCII)))
	if m.lastImagePath != "" {
		info = append(info, "Última imagem: "+filepath.Base(m.lastImagePath))
	}
	infoBox := renderBox(pal, "Geração", strings.Join(info, "\n"), leftW, infoH, false)

	left := lipgloss.JoinVertical(lipgloss.Left, prompt, neg, row1, row2, infoBox)
	if previewW > 0 {
		preview := renderBox(pal, "Prévia", m.lastImageView, previewW, bh, false)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, preview)
	}
	return left
}

func baseOrNone(p string) string {
	if p == "" {
		return "(nenhum — selecione na aba [3] Modelos)"
	}
	return filepath.Base(p)
}

// ---------- Modelos ----------

func (m AppModel) viewModels(pal theme.Palette) string {
	bh := m.bodyHeight()
	infoH := 6
	importH := 0
	if m.importingMode {
		importH = 3
	}
	avail := bh - infoH - importH
	llmH := avail / 2
	sdH := avail - llmH

	list := func(items []models.InstalledModel, sel int, focused bool, h int, empty string) string {
		var lines []string
		if len(items) == 0 {
			lines = append(lines, pal.Muted.Render("  "+empty))
		}
		for i, im := range items {
			line := im.Name
			if im.Path == m.selectedLLM || im.Path == m.selectedSD {
				line += "  [ATIVO]"
			}
			if focused && i == sel {
				lines = append(lines, pal.Selected.Render("> "+line))
			} else {
				lines = append(lines, "  "+line)
			}
		}
		return strings.Join(windowLines(lines, sel, h-2), "\n")
	}

	llms := m.modelsOfKind(false)
	sds := m.modelsOfKind(true)
	editing := m.importingMode

	llmBox := renderBox(pal, fmt.Sprintf("Modelos de Linguagem (%d) — [Enter] ativar  [d] remover", len(llms)),
		list(llms, m.llmIdx, !m.focusSD, llmH, "Nenhum modelo .gguf encontrado."), m.width, llmH, !m.focusSD && !editing)
	sdBox := renderBox(pal, fmt.Sprintf("Checkpoints de Imagem (%d) — [Enter] ativar  [d] remover", len(sds)),
		list(sds, m.sdIdx, m.focusSD, sdH, "Nenhum checkpoint .safetensors/.ckpt encontrado."), m.width, sdH, m.focusSD && !editing)

	dir := "studio-data"
	if m.deps.DataDir != "" {
		dir = m.deps.DataDir
	}
	info := pal.Muted.Render("Adicione modelos copiando os arquivos para:") + "\n" +
		filepath.Join(dir, "models", "llm") + "  (LLM .gguf)\n" +
		filepath.Join(dir, "models", "checkpoints") + "  (imagem)\n" +
		pal.Muted.Render("ou use [i] para importar um arquivo • [Tab] alterna a lista • [r] atualiza")
	infoBox := renderBox(pal, "Como adicionar modelos", info, m.width, infoH, false)

	parts := []string{llmBox, sdBox, infoBox}
	if m.importingMode {
		parts = append(parts, renderBox(pal, "Importar (.gguf / .safetensors) — Enter confirma, Esc cancela", inputView(m.importInput, m.width-6), m.width, importH, true))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// ---------- Monitor ----------

func (m AppModel) viewMonitor(pal theme.Palette) string {
	bh := m.bodyHeight()
	ascii := m.deps.Config.UI.ASCII
	leftW := m.width / 2
	rightW := m.width - leftW
	topH := bh / 2
	botH := bh - topH

	panel := func(title string, w, h int, pct float64, text string, hist []float64) string {
		gw := maxInt(8, w-4-8)
		lines := []string{
			text,
			fmt.Sprintf("%s %5.1f%%", pal.Accent.Render(gauge(pct, gw, ascii)), pct),
			pal.Muted.Render(sparkline(hist, maxInt(8, w-4), ascii)),
		}
		return renderBox(pal, title, strings.Join(lines, "\n"), w, h, false)
	}

	k := m.metrics
	cpu := panel("CPU", leftW, topH, k.CPUPercent,
		truncate(m.deps.Hardware.CPUName, leftW-4), k.CPUHistory)
	ram := panel("RAM", rightW, topH, k.RAMPercent,
		fmt.Sprintf("%d / %d MB em uso", k.RAMUsedMB, k.RAMTotalMB), k.RAMHistory)

	gpuName := m.deps.Hardware.GPUName
	gpuText := fmt.Sprintf("%s | Backend: %s", gpuName, m.deps.Hardware.SelectedBackend)
	if k.GPUSource != "" {
		gpuText += " | " + k.GPUSource
	} else if !k.GPUAvailable {
		gpuText += " | lendo contadores..."
	}
	gpu := panel("GPU", leftW, botH, k.GPUPercent, truncate(gpuText, leftW-4), k.GPUHistory)

	var vramText string
	switch {
	case k.VRAMTotalMB > 0:
		vramText = fmt.Sprintf("%d MB em uso de %d MB", k.VRAMUsedMB, k.VRAMTotalMB)
	case k.GPUAvailable:
		vramText = fmt.Sprintf("%d MB em uso", k.VRAMUsedMB)
	default:
		vramText = "Aguardando leitura da VRAM..."
	}
	vram := panel("VRAM em uso", rightW, botH, k.VRAMPercent, vramText, k.VRAMHistory)

	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, cpu, ram),
		lipgloss.JoinHorizontal(lipgloss.Top, gpu, vram),
	)
}

// ---------- Logs ----------

func (m AppModel) viewLogs(pal theme.Palette) string {
	bh := m.bodyHeight()
	vp := m.logViewport
	vp.Width = maxInt(10, m.width-4)
	vp.Height = maxInt(3, bh-2)
	title := fmt.Sprintf("Logs — filtro: %s ([f] alterna, ↑/↓ rola)", m.logFilter)
	return renderBox(pal, title, vp.View(), m.width, bh, true)
}
