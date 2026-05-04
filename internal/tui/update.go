package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/paperpaper/paperpaper/internal/api"
	exportPkg "github.com/paperpaper/paperpaper/internal/export"
	"github.com/paperpaper/paperpaper/internal/prompt"
	"github.com/paperpaper/paperpaper/internal/session"
	"github.com/paperpaper/paperpaper/internal/urlparse"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.resizeComponents()

	case tea.KeyPressMsg:
		return m.handleKeyMsg(msg)

	case streamMsg:
		return m.handleStreamMsg(msg)

	case summarizeDoneMsg:
		m.manager.SetInitialSummary(msg.summary)
		m.phase = PhaseChat
		m.viewport.SetContent(m.renderMessages())
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
		m.manager.Save()
		return m, tea.Quit
	}

	// Handle confirm delete state (only for non-list modes)
	if m.confirmDelete && m.mode != ModeList {
		switch msg.String() {
		case "y":
			if p := m.manager.Paper(); p != nil {
				session.DeletePaper(p.ID)
				m.manager.SetPaper(nil)
				m.phase = PhaseInit
				m.streamContent = ""
				m.viewport.SetContent(bannerStyle.Render("论文已删除。\n\n请粘贴论文内容开始新的会话。"))
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
	switch msg.String() {
	case "i":
		m.mode = ModeInput
		m.textarea.Focus()
		return m, textarea.Blink
	case "q", "esc":
		m.manager.Save()
		return m, tea.Quit
	case "j", "down":
		m.viewport.ScrollDown(1)
	case "k", "up":
		m.viewport.ScrollUp(1)
	case "g":
		m.viewport.GotoTop()
	case "G":
		m.viewport.GotoBottom()
	case "ctrl+d":
		m.viewport.ScrollDown(m.viewport.Height() / 2)
	case "ctrl+u":
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
		if m.listCursor < len(m.listItems)-1 {
			m.listCursor++
		}
	case "k", "up":
		if m.listCursor > 0 {
			m.listCursor--
		}
	case "enter":
		if len(m.listItems) > 0 {
			return m.openPaper(m.listItems[m.listCursor].ID)
		}
	case "d":
		if len(m.listItems) > 0 {
			m.confirmDelete = true
		}
	case "y":
		if m.confirmDelete && len(m.listItems) > 0 {
			id := m.listItems[m.listCursor].ID
			if err := session.DeletePaper(id); err != nil {
				// silently fail
				_ = err
			}
			m.confirmDelete = false
			// Refresh list
			items, _ := session.ListPapers()
			m.listItems = items
			if m.listCursor >= len(m.listItems) && m.listCursor > 0 {
				m.listCursor--
			}
			if len(m.listItems) == 0 {
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

	// Check if paper is loaded
	if m.manager.Paper() == nil {
		// Treat input as paper content
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

	m.viewport.SetContent(m.renderMessages())
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

	m.viewport.SetContent(m.renderMessages())
	m.textarea.Reset()
	m.viewport.GotoBottom()

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
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
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
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil
	}

	// Accumulate content
	m.streamContent += msg.chunk.Content
	m.streamBuf += msg.chunk.Content

	// Update viewport content periodically for smooth rendering
	if len(m.streamBuf) > 50 {
		m.streamBuf = ""
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
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
		m.phase = PhaseInit
		m.streamContent = ""
		m.textarea.Reset()
		if len(parts) > 1 {
			return m.loadFromInput(strings.TrimSpace(parts[1]))
		}
		m.viewport.SetContent(bannerStyle.Render("欢迎使用 PaperPaper!\n\n请粘贴论文内容，然后按 Enter 提交。\n\nShift+Enter 换行，或使用 /new <url/path> 从文件或 URL 加载。"))
		return m, nil

	case "/list":
		items, err := session.ListPapers()
		if err != nil {
			m.err = err
			return m, nil
		}
		if len(items) == 0 {
			m.viewport.SetContent(bannerStyle.Render("没有历史论文。\n\n请粘贴论文内容开始新的会话。"))
			return m, nil
		}
		m.listItems = items
		m.listCursor = 0
		m.confirmDelete = false
		m.mode = ModeList
		return m, nil

	case "/open":
		if len(parts) < 2 {
			m.viewport.SetContent(bannerStyle.Render("用法: /open <id>"))
			return m, nil
		}
		var id int
		if _, err := fmt.Sscanf(parts[1], "%d", &id); err != nil {
			m.viewport.SetContent(bannerStyle.Render("无效的 ID: " + parts[1]))
			return m, nil
		}
		return m.openPaper(id)

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
		m.viewport.SetContent(bannerStyle.Render(
			"可用命令:\n\n" +
				"  /new [url/path]  新建会话（可从 URL 或文件加载）\n" +
				"  /list            会话列表\n" +
				"  /open <id>       加载历史会话\n" +
				"  /delete          删除当前会话\n" +
				"  /edit            编辑最近问题\n" +
				"  /del <round>     删除指定轮次\n" +
				"  /summarize       对话元总结\n" +
				"  /export          导出到 Obsidian\n" +
				"  /model [name]    模型切换\n" +
				"  /config          配置管理\n" +
				"  /help            显示帮助\n" +
				"  /quit            退出\n\n" +
				"快捷键:\n\n" +
				"  i     进入输入模式\n" +
				"  Esc   返回浏览模式\n" +
				"  j/k   上下滚动\n" +
				"  q     退出\n\n" +
				"输入模式:\n\n" +
				"  Enter       发送消息\n" +
				"  Shift+Enter 插入换行"))
		return m, nil
	}

	m.textarea.Reset()
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

	m.viewport.SetContent(m.renderMessages())
	return m, m.startStream(messages)
}

func (m *Model) handleExport() (tea.Model, tea.Cmd) {
	p := m.manager.Paper()
	if p == nil {
		m.viewport.SetContent(bannerStyle.Render("没有加载的论文。"))
		return m, nil
	}

	path, err := exportPkg.ExportToObsidian(m.cfg, p)
	if err != nil {
		m.viewport.SetContent(bannerStyle.Render(fmt.Sprintf("导出失败: %v", err)))
		return m, nil
	}

	m.viewport.SetContent(bannerStyle.Render(fmt.Sprintf("导出成功!\n\n文件: %s", path)))
	return m, nil
}

func (m *Model) openPaper(id int) (tea.Model, tea.Cmd) {
	p, err := session.LoadPaper(id)
	if err != nil {
		m.viewport.SetContent(bannerStyle.Render(fmt.Sprintf("无法加载论文 #%d: %v", id, err)))
		return m, nil
	}

	m.LoadPaper(p)
	m.mode = ModeInput
	m.textarea.Focus()
	m.textarea.Reset()
	return m, textarea.Blink
}

func (m *Model) loadFromInput(input string) (tea.Model, tea.Cmd) {
	var content string
	var sourceURL string
	var err error

	if urlparse.IsURL(input) {
		sourceURL = input
		m.viewport.SetContent(bannerStyle.Render("正在从 URL 加载..."))
		content, err = urlparse.FetchURL(input)
	} else if urlparse.IsFilePath(input) {
		m.viewport.SetContent(bannerStyle.Render("正在从文件加载..."))
		content, err = urlparse.LoadFile(input)
	} else {
		m.viewport.SetContent(bannerStyle.Render("无效的输入，请提供 URL 或文件路径。"))
		return m, nil
	}

	if err != nil {
		m.viewport.SetContent(bannerStyle.Render(fmt.Sprintf("加载失败: %v", err)))
		return m, nil
	}

	p := session.NewPaper(content, sourceURL)
	m.manager.SetPaper(p)

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

	m.viewport.SetContent(m.renderMessages())

	return m, m.startStream([]api.ChatMessage{
		{Role: "system", Content: prompt.GetHeavy()},
		{Role: "user", Content: content},
	})
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
