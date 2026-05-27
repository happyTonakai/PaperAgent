package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbletea/v2"

	"github.com/paperpaper/paperpaper/internal/api"
	exportPkg "github.com/paperpaper/paperpaper/internal/export"
	"github.com/paperpaper/paperpaper/internal/prompt"
	"github.com/paperpaper/paperpaper/internal/session"
	"github.com/paperpaper/paperpaper/internal/urlparse"

	"charm.land/bubbles/v2/textarea"
)

var (
	normalKeyI     = key.NewBinding(key.WithKeys("i"))
	normalKeyJ     = key.NewBinding(key.WithKeys("j", "down"))
	normalKeyK     = key.NewBinding(key.WithKeys("k", "up"))
	normalKeyJ10   = key.NewBinding(key.WithKeys("J", "shift+j"))
	normalKeyK10   = key.NewBinding(key.WithKeys("K", "shift+k"))
	normalKeyG     = key.NewBinding(key.WithKeys("g"))
	normalKeyGG    = key.NewBinding(key.WithKeys("G", "shift+g"))
	normalKeyCtrlD = key.NewBinding(key.WithKeys("ctrl+d"))
	normalKeyCtrlU = key.NewBinding(key.WithKeys("ctrl+u"))
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.resizeComponents()
		// Re-render viewport content with new width while respecting scroll position.
		m.refreshViewportContent(false)

	case tea.KeyPressMsg:
		m.clearSelection()
		return m.handleKeyMsg(msg)

	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)

	case tea.MouseMotionMsg:
		return m.handleMouseMotion(msg)

	case tea.MouseReleaseMsg:
		return m.handleMouseRelease(msg)

	case tea.MouseWheelMsg:
		m.clearSelection()

	case streamMsg:
		return m.handleStreamMsg(msg)

	case summarizeDoneMsg:
		m.manager.SetInitialSummary(msg.summary)
		m.phase = PhaseChat
		m.refreshViewportContent(false)
		m.manager.Save()
		go func() {
			title, _ := m.apiClient.ExtractTitle(m.cfg.API.LightModel, m.manager.Paper().Content)
			if title != "" {
				m.manager.SetTitle(title)
				m.manager.Save()
			}
		}()
		return m, nil

	case titleDoneMsg:
		if msg.title != "" {
			m.manager.SetTitle(msg.title)
			m.manager.Save()
		}
		return m, nil
	}

	// Update subcomponents
	var vpCmd, taCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	m.textarea, taCmd = m.textarea.Update(msg)
	cmds = append(cmds, vpCmd, taCmd)

	return m, tea.Batch(cmds...)
}

func (m *Model) handleKeyMsg(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Global keys
	if key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))) {
		if m.quitArmed {
			m.manager.Save()
			return m, tea.Quit
		}
		m.quitArmed = true
		m.statusNotice = "再按一次 Ctrl+C 退出"
		return m, nil
	}
	m.quitArmed = false

	// Handle confirm delete state (only for non-list modes)
	if m.confirmDelete && (m.mode != ModeList || m.listKind != ListKindResume) {
		switch msg.String() {
		case "y":
			if p := m.manager.Paper(); p != nil {
				session.DeletePaperByRef(p.Ref())
				m.manager.SetPaper(nil)
				m.updateTextareaPlaceholder()
				m.phase = PhaseInit
				m.streamContent = ""
				m.viewport.SetContent(bannerStyle.Render("论文已删除。\n\n请输入 arXiv 链接/ID 开始新的会话。"))
			}
			m.confirmDelete = false
			return m, nil
		case "n":
			m.confirmDelete = false
			m.viewport.SetContent(m.renderMessages())
			return m, nil
		}
		return m, nil
	}

	switch m.mode {
	case ModeNormal:
		return m.handleNormalKey(msg)
	case ModeInput:
		return m.handleInputKey(msg)
	case ModeList:
		return m.handleListKey(msg)
	}

	return m, nil
}

func (m *Model) handleNormalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, normalKeyI):
		m.mode = ModeInput
		m.textarea.Focus()
		return m, textarea.Blink
	case key.Matches(msg, normalKeyJ):
		m.viewport.ScrollDown(1)
	case key.Matches(msg, normalKeyK):
		m.viewport.ScrollUp(1)
	case key.Matches(msg, normalKeyJ10):
		m.viewport.ScrollDown(10)
	case key.Matches(msg, normalKeyK10):
		m.viewport.ScrollUp(10)
	case key.Matches(msg, normalKeyG):
		m.viewport.GotoTop()
	case key.Matches(msg, normalKeyGG):
		m.viewport.GotoBottom()
	case key.Matches(msg, normalKeyCtrlD):
		m.viewport.ScrollDown(m.viewport.Height() / 2)
	case key.Matches(msg, normalKeyCtrlU):
		m.viewport.ScrollUp(m.viewport.Height() / 2)
	}

	return m, nil
}

func (m *Model) handleInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Keystroke() {
	case "esc":
		m.mode = ModeNormal
		m.textarea.Blur()
		return m, nil

	case "enter":
		return m.handleSubmit()

	case "ctrl+d":
		return m.handleSubmit()

	case "shift+enter":
		// Let textarea handle as newline
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd

	case "tab":
		if m.completeCommand() {
			return m, nil
		}
	}

	// Update textarea
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m *Model) handleListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = ModeInput
		m.textarea.Focus()
		return m, textarea.Blink
	case "j", "down":
		if m.listCursor < m.currentListLen()-1 {
			m.listCursor++
		}
	case "k", "up":
		if m.listCursor > 0 {
			m.listCursor--
		}
	case "enter":
		switch m.listKind {
		case ListKindResume:
			if len(m.resumeItems) > 0 {
				return m.openPaper(m.resumeItems[m.listCursor].Ref())
			}
		case ListKindRounds:
			if len(m.roundItems) > 0 {
				return m.jumpToRound(m.roundItems[m.listCursor].Round)
			}
		}
	case "d":
		if m.listKind == ListKindResume && len(m.resumeItems) > 0 {
			m.confirmDelete = true
		}
	case "y":
		if m.confirmDelete && m.listKind == ListKindResume && len(m.resumeItems) > 0 {
			ref := m.resumeItems[m.listCursor].Ref()
			if err := session.DeletePaperByRef(ref); err != nil {
				_ = err
			}
			m.confirmDelete = false
			items, _ := session.ListPapers()
			m.resumeItems = items
			if m.listCursor >= len(m.resumeItems) && m.listCursor > 0 {
				m.listCursor--
			}
			if len(m.resumeItems) == 0 {
				m.mode = ModeInput
				m.textarea.Focus()
				return m, textarea.Blink
			}
		}
	case "n":
		m.confirmDelete = false
	}

	return m, nil
}

func (m *Model) currentListLen() int {
	switch m.listKind {
	case ListKindResume:
		return len(m.resumeItems)
	case ListKindRounds:
		return len(m.roundItems)
	default:
		return 0
	}
}

func (m *Model) handleSubmit() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.textarea.Value())
	if input == "" {
		return m, nil
	}

	m.textarea.Reset()

	// Handle commands
	if strings.HasPrefix(input, "/") {
		return m.handleCommand(input)
	}

	// Check if paper is loaded. A bare arXiv ID/link, URL, or file path should
	// load the paper first; otherwise keep supporting direct paste mode.
	if m.manager.Paper() == nil {
		if urlparse.IsArxivInput(input) || urlparse.IsURL(input) || urlparse.IsFilePath(input) {
			return m.loadFromInput(input)
		}

		m.loadPaperFromText(input)
		return m, m.startStream([]api.ChatMessage{
			{Role: "system", Content: prompt.GetHeavy()},
			{Role: "user", Content: input},
		})
	}

	// Normal question
	return m.askQuestion(input)
}

func (m *Model) loadPaperFromText(content string) {
	p := session.NewPaper(content, "")
	m.manager.SetPaper(p)
	m.updateTextareaPlaceholder()

	// Add initial user message
	displayContent := content
	if len(displayContent) > 200 {
		displayContent = displayContent[:200] + "..."
	}
	m.manager.AddMessage(session.Message{
		RoundNumber: 0,
		Role:        "user",
		Content:     displayContent,
		TokenCount:  session.EstimateTokens(content),
	})

	m.streaming = true
	m.streamContent = ""
	m.phase = PhaseInit

	m.refreshViewportContent(true)
}

func (m *Model) askQuestion(question string) (tea.Model, tea.Cmd) {
	p := m.manager.Paper()
	round := m.manager.CurrentRound()
	if len(p.Messages) > 0 {
		round = p.Messages[len(p.Messages)-1].RoundNumber + 1
	}

	// Add user message
	m.manager.AddMessage(session.Message{
		RoundNumber: round,
		Role:        "user",
		Content:     question,
		TokenCount:  session.EstimateTokens(question),
	})

	m.streaming = true
	m.streamContent = ""

	// Build messages for CHAT phase
	recent := m.manager.GetRecentMessages(m.cfg.UI.MaxRecentRounds)
	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetLight()},
		{Role: "user", Content: fmt.Sprintf("以下是论文全文：\n\n%s", p.Content)},
	}
	for _, msg := range recent {
		messages = append(messages, api.ChatMessage{Role: msg.Role, Content: msg.Content})
	}

	m.refreshViewportContent(true)
	m.textarea.Reset()

	// Start streaming
	return m, m.startStream(messages)
}

func (m *Model) handleStreamMsg(msg streamMsg) (tea.Model, tea.Cmd) {
	if msg.chunk.Err != nil {
		m.err = msg.chunk.Err
		m.streaming = false
		if m.streamContent == "" {
			m.streamContent = fmt.Sprintf("[错误: %s]", msg.chunk.Err)
		} else {
			m.streamContent += "\n[生成中断]"
		}
		if m.phase == PhaseInit {
			m.manager.SetInitialSummary(m.streamContent)
			m.phase = PhaseChat
		}
		m.manager.AddMessage(session.Message{
			RoundNumber: m.manager.CurrentRound(),
			Role:        "assistant",
			Content:     m.streamContent,
			TokenCount:  session.EstimateTokens(m.streamContent),
		})
		m.manager.Save()
		m.refreshViewportContent(false)
		return m, nil
	}

	if msg.chunk.Done {
		m.streaming = false
		if m.phase == PhaseInit {
			m.manager.SetInitialSummary(m.streamContent)
			m.phase = PhaseChat
			m.manager.Save()
			// Extract title async
			go func() {
				title, _ := m.apiClient.ExtractTitle(m.cfg.API.LightModel, m.manager.Paper().Content)
				if title != "" {
					m.manager.SetTitle(title)
					m.manager.Save()
				}
			}()
		} else {
			m.manager.AddMessage(session.Message{
				RoundNumber: m.manager.CurrentRound(),
				Role:        "assistant",
				Content:     m.streamContent,
				TokenCount:  session.EstimateTokens(m.streamContent),
			})
			m.manager.Save()
			// Generate digest async
			go func() {
				p := m.manager.Paper()
				if p == nil || len(p.Messages) < 2 {
					return
				}
				userMsg := p.Messages[len(p.Messages)-2]
				digest, _ := m.apiClient.SummarizeQuestion(m.cfg.API.LightModel, userMsg.Content)
				if digest != "" {
					p.Messages[len(p.Messages)-2].Digest = digest
					m.manager.Save()
				}
			}()
		}
		m.refreshViewportContent(false)
		return m, nil
	}

	// Accumulate content
	m.streamContent += msg.chunk.Content
	m.streamBuf += msg.chunk.Content

	// Update viewport content periodically for smooth rendering
	if len(m.streamBuf) > 50 {
		m.streamBuf = ""
		m.refreshViewportContent(false)
	}

	// Continue streaming from existing channel
	return m, m.nextStreamCmd(m.streamChan)
}

func (m *Model) handleCommand(input string) (tea.Model, tea.Cmd) {
	parts := strings.SplitN(input, " ", 2)
	cmd := parts[0]

	switch cmd {
	case "/quit":
		m.manager.Save()
		return m, tea.Quit

	case "/new":
		m.manager.SetPaper(nil)
		m.updateTextareaPlaceholder()
		m.phase = PhaseInit
		m.streamContent = ""
		m.textarea.Reset()
		if len(parts) > 1 {
			return m.loadFromInput(strings.TrimSpace(parts[1]))
		}
		m.viewport.SetContent(bannerStyle.Render("欢迎使用 PaperPaper!\n\n请输入 arXiv 链接或 ID，然后按 Enter 开始抓取并总结。\n\n也可以粘贴论文全文，Shift+Enter 换行；或使用 /new <arxiv/url/path> 从 arXiv、URL 或文件加载。"))
		return m, nil

	case "/resume":
		items, err := session.ListPapers()
		if err != nil {
			m.err = err
			return m, nil
		}
		if len(items) == 0 {
			m.viewport.SetContent(bannerStyle.Render("没有历史论文。\n\n请输入 arXiv 链接/ID 开始新的会话。"))
			return m, nil
		}
		m.resumeItems = items
		m.roundItems = nil
		m.listKind = ListKindResume
		m.listCursor = 0
		m.confirmDelete = false
		m.mode = ModeList
		return m, nil

	case "/list":
		return m.showRoundList()

	case "/open":
		if len(parts) < 2 {
			m.viewport.SetContent(bannerStyle.Render("用法: /open <session-id>"))
			return m, nil
		}
		return m.openPaper(strings.TrimSpace(parts[1]))

	case "/delete":
		if m.manager.Paper() == nil {
			m.viewport.SetContent(bannerStyle.Render("没有加载的论文。"))
			return m, nil
		}
		m.confirmDelete = true
		m.viewport.SetContent(bannerStyle.Render("确认删除当前论文？\n\n按 y 确认，n 取消"))
		return m, nil

	case "/edit":
		return m.editLastQuestion()

	case "/del":
		if len(parts) < 2 {
			m.viewport.SetContent(bannerStyle.Render("用法: /del <round>"))
			return m, nil
		}
		var round int
		if _, err := fmt.Sscanf(parts[1], "%d", &round); err != nil {
			m.viewport.SetContent(bannerStyle.Render("无效的轮次: " + parts[1]))
			return m, nil
		}
		m.manager.DeleteRound(round)
		m.manager.Save()
		m.viewport.SetContent(m.renderMessages())
		return m, nil

	case "/summarize":
		return m.handleSummarize()

	case "/export":
		return m.handleExport()

	case "/model":
		if len(parts) > 1 {
			m.cfg.API.DefaultModel = strings.TrimSpace(parts[1])
			m.viewport.SetContent(bannerStyle.Render(fmt.Sprintf("模型已切换为: %s", m.cfg.API.DefaultModel)))
		} else {
			m.viewport.SetContent(bannerStyle.Render(fmt.Sprintf("当前模型: %s\n\n用法: /model <model-name>", m.cfg.API.DefaultModel)))
		}
		return m, nil

	case "/config":
		m.viewport.SetContent(bannerStyle.Render(fmt.Sprintf(
			"当前配置:\n\n  Base URL: %s\n  Model: %s\n  Light Model: %s\n  Max Rounds: %d\n  Obsidian Vault: %s",
			m.cfg.API.BaseURL,
			m.cfg.API.DefaultModel,
			m.cfg.API.LightModel,
			m.cfg.UI.MaxRecentRounds,
			m.cfg.Obsidian.VaultPath,
		)))
		return m, nil

	case "/help":
		m.viewport.SetContent(bannerStyle.Render(commandHelpText() +
			"\n快捷键:\n\n" +
			"  i     进入输入模式\n" +
			"  Esc   返回浏览模式\n" +
			"  j/k   上下滚动\n" +
			"  J/K   跳10行\n" +
			"  gg/G  跳到开头/结尾\n" +
			"  Ctrl+D/U 半页滚动\n" +
			"  双击 Ctrl+C 退出\n\n" +
			"输入模式:\n\n" +
			"  Enter       发送消息\n" +
			"  Shift+Enter 插入换行"))
		return m, nil
	}

	m.viewport.SetContent(bannerStyle.Render(fmt.Sprintf("未知命令: %s\n\n%s", cmd, commandHelpText())))
	return m, nil
}

func (m *Model) editLastQuestion() (tea.Model, tea.Cmd) {
	if m.manager.Paper() == nil || len(m.manager.Paper().Messages) == 0 {
		m.viewport.SetContent(bannerStyle.Render("没有可编辑的消息。"))
		return m, nil
	}

	msg := m.manager.GetLastUserMessage()
	if msg == nil {
		m.viewport.SetContent(bannerStyle.Render("没有可编辑的用户消息。"))
		return m, nil
	}

	// Delete the last round (user + assistant)
	m.manager.DeleteLastRound()

	// Fill textarea with the user's last question
	m.textarea.SetValue(msg.Content)
	m.mode = ModeInput
	m.textarea.Focus()
	m.viewport.SetContent(m.renderMessages())
	return m, textarea.Blink
}

func (m *Model) handleSummarize() (tea.Model, tea.Cmd) {
	p := m.manager.Paper()
	if p == nil {
		m.viewport.SetContent(bannerStyle.Render("没有加载的论文。"))
		return m, nil
	}

	// Build context for summarize
	var context strings.Builder
	if p.InitialSummary != "" {
		context.WriteString("## 初始总结\n\n")
		context.WriteString(p.InitialSummary)
		context.WriteString("\n\n")
	}
	context.WriteString("## 对话历史\n\n")
	for _, msg := range p.Messages {
		if msg.Role == "user" {
			context.WriteString(fmt.Sprintf("Q: %s\n", msg.Content))
		} else {
			context.WriteString(fmt.Sprintf("A: %s\n", msg.Content))
		}
	}

	m.streaming = true
	m.streamContent = ""

	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetDigest()},
		{Role: "user", Content: context.String()},
	}

	m.refreshViewportContent(true)
	return m, m.startStream(messages)
}

func (m *Model) handleExport() (tea.Model, tea.Cmd) {
	p := m.manager.Paper()
	if p == nil {
		m.statusNotice = "没有加载的论文，无法导出"
		return m, nil
	}

	path, err := exportPkg.ExportToObsidian(m.cfg, p)
	if err != nil {
		m.statusNotice = fmt.Sprintf("导出失败: %v", err)
		return m, nil
	}

	m.statusNotice = fmt.Sprintf("导出成功: %s", path)
	m.mode = ModeInput
	m.textarea.Focus()
	return m, textarea.Blink
}

func (m *Model) openPaper(ref string) (tea.Model, tea.Cmd) {
	p, err := session.LoadPaperByRef(ref)
	if err != nil {
		m.viewport.SetContent(bannerStyle.Render(fmt.Sprintf("无法加载论文 %s: %v", ref, err)))
		return m, nil
	}

	m.LoadPaper(p)
	m.viewport.GotoBottom()
	m.mode = ModeInput
	m.textarea.Focus()
	m.textarea.Reset()
	return m, textarea.Blink
}

func (m *Model) loadFromInput(input string) (tea.Model, tea.Cmd) {
	var content string
	var sourceURL string
	var err error

	if arxivURL, _, ok := urlparse.NormalizeArxivInput(input); ok {
		sourceURL = arxivURL
		m.viewport.SetContent(bannerStyle.Render("正在抓取 arXiv 论文全文..."))
		content, err = urlparse.FetchURL(arxivURL)
	} else if urlparse.IsURL(input) {
		sourceURL = input
		m.viewport.SetContent(bannerStyle.Render("正在从 URL 加载..."))
		content, err = urlparse.FetchURL(input)
	} else if urlparse.IsFilePath(input) {
		m.viewport.SetContent(bannerStyle.Render("正在从文件加载..."))
		content, err = urlparse.LoadFile(input)
	} else {
		m.viewport.SetContent(bannerStyle.Render("无效的输入，请提供 arXiv 链接/ID、URL 或文件路径。"))
		return m, nil
	}

	if err != nil {
		m.viewport.SetContent(bannerStyle.Render(fmt.Sprintf("加载失败: %v", err)))
		return m, nil
	}

	p := session.NewPaper(content, sourceURL)
	m.manager.SetPaper(p)
	m.updateTextareaPlaceholder()

	displayContent := content
	if len(displayContent) > 200 {
		displayContent = displayContent[:200] + "..."
	}
	m.manager.AddMessage(session.Message{
		RoundNumber: 0,
		Role:        "user",
		Content:     displayContent,
		TokenCount:  session.EstimateTokens(content),
	})

	m.streaming = true
	m.streamContent = ""
	m.phase = PhaseInit
	m.mode = ModeInput

	m.refreshViewportContent(true)

	return m, m.startStream([]api.ChatMessage{
		{Role: "system", Content: prompt.GetHeavy()},
		{Role: "user", Content: content},
	})
}

func (m *Model) refreshViewportContent(forceBottom bool) {
	wasAtBottom := m.viewport.AtBottom()
	yOffset := m.viewport.YOffset()
	m.viewport.SetContent(m.renderMessages())
	if forceBottom || wasAtBottom {
		m.viewport.GotoBottom()
		return
	}
	m.viewport.SetYOffset(yOffset)
}

func (m *Model) resizeComponents() {
	if !m.ready {
		return
	}
	headerHeight := 1
	footerHeight := 2
	inputHeight := 4

	viewportHeight := m.height - headerHeight - footerHeight - inputHeight
	if viewportHeight < 5 {
		viewportHeight = 5
	}

	m.viewport.SetWidth(m.width - 2)
	m.viewport.SetHeight(viewportHeight)
	m.textarea.SetWidth(m.width - 2)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m *Model) showRoundList() (tea.Model, tea.Cmd) {
	items := m.buildRoundItems()
	if len(items) == 0 {
		m.viewport.SetContent(bannerStyle.Render("当前论文还没有可跳转的问答轮次。\n\n提问后会在这里显示每轮问题摘要。"))
		return m, nil
	}
	m.roundItems = items
	m.resumeItems = nil
	m.listKind = ListKindRounds
	m.listCursor = 0
	m.confirmDelete = false
	m.mode = ModeList
	return m, nil
}

func (m *Model) buildRoundItems() []roundListItem {
	p := m.manager.Paper()
	if p == nil {
		return nil
	}

	assistantRounds := map[int]bool{}
	for _, msg := range p.Messages {
		if msg.Role == "assistant" {
			assistantRounds[msg.RoundNumber] = true
		}
	}

	seen := map[int]bool{}
	var items []roundListItem
	for _, msg := range p.Messages {
		if msg.Role != "user" || seen[msg.RoundNumber] {
			continue
		}
		// Initial paper-load pseudo-message has no paired assistant message. Do not
		// show it as a Q&A round, but do include real first questions at round 0.
		if !assistantRounds[msg.RoundNumber] {
			continue
		}
		seen[msg.RoundNumber] = true
		title := msg.Digest
		if title == "" {
			title = firstLine(msg.Content)
			if len([]rune(title)) > 80 {
				r := []rune(title)
				title = string(r[:80]) + "..."
			}
		}
		items = append(items, roundListItem{Round: msg.RoundNumber, Display: len(items) + 1, Title: title, Digest: msg.Digest})
	}
	return items
}

func (m *Model) completeCommand() bool {
	value := strings.TrimSpace(m.textarea.Value())
	if !strings.HasPrefix(value, "/") || strings.Contains(value, " ") {
		return false
	}
	var matches []commandInfo
	for _, c := range commands {
		if strings.HasPrefix(c.Name, value) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		return false
	}
	if len(matches) == 1 {
		m.textarea.SetValue(matches[0].Name + " ")
		return true
	}
	prefix := commonCommandPrefix(matches)
	if prefix != value {
		m.textarea.SetValue(prefix)
	}
	return true
}

func commonCommandPrefix(matches []commandInfo) string {
	if len(matches) == 0 {
		return ""
	}
	prefix := matches[0].Name
	for _, c := range matches[1:] {
		for !strings.HasPrefix(c.Name, prefix) && prefix != "" {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}

func (m *Model) jumpToRound(round int) (tea.Model, tea.Cmd) {
	m.mode = ModeInput
	m.textarea.Focus()
	m.refreshViewportContent(false)
	m.viewport.SetYOffset(m.yOffsetForRound(round))
	return m, textarea.Blink
}

func (m *Model) yOffsetForRound(round int) int {
	content := m.renderMessagesBeforeRound(round)
	if content == "" {
		return 0
	}
	lines := strings.Count(content, "\n")
	if lines > 0 {
		lines--
	}
	if lines < 0 {
		lines = 0
	}
	return lines
}

func (m *Model) renderMessagesBeforeRound(round int) string {
	p := m.manager.Paper()
	if p == nil {
		return ""
	}
	var b strings.Builder
	if p.InitialSummary != "" {
		b.WriteString(m.renderMarkdown(p.InitialSummary))
		b.WriteString("\n")
		sepWidth := m.width - 4
		if sepWidth < 1 {
			sepWidth = 1
		}
		b.WriteString(separatorStyle.Render(strings.Repeat("─", sepWidth)))
		b.WriteString("\n")
	}
	for _, msg := range p.Messages {
		if msg.RoundNumber >= round {
			break
		}
		b.WriteString(m.renderMessage(msg))
		b.WriteString("\n")
	}
	return b.String()
}
