package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/paperpaper/paperpaper/internal/session"
)

func (m *Model) View() tea.View {
	if !m.ready {
		return tea.NewView("Initializing...")
	}

	var b strings.Builder

	// Header
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	// Content area
	switch m.mode {
	case ModeList:
		b.WriteString(m.renderList())
	default:
		b.WriteString(m.renderViewportWithScrollbar())
	}
	b.WriteString("\n")

	// Separator line
	if m.mode != ModeList {
		b.WriteString(m.renderSeparator())
		b.WriteString("\n")
		// Thinking bar animation
		if thinking := m.renderThinkingBar(); thinking != "" {
			b.WriteString(thinking)
			b.WriteString("\n")
		}
	}

	// Input area (hidden in list mode)
	if m.mode != ModeList {
		b.WriteString(m.renderInput())
		b.WriteString("\n")
	}

	// Status bar
	b.WriteString(m.renderStatusBar())

	return tea.View{
		Content:     b.String(),
		AltScreen:   true,
		MouseMode:   tea.MouseModeCellMotion,
		WindowTitle: m.renderWindowTitle(),
	}
}

func (m *Model) renderWindowTitle() string {
	title := "PaperPaper"
	if p := m.manager.Paper(); p != nil {
		paperTitle := strings.TrimSpace(p.Title)
		if paperTitle == "" && p.SourceURL != "" {
			paperTitle = p.SourceURL
		}
		if paperTitle != "" {
			title = "PaperPaper — " + paperTitle
		}
	}
	title = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(title)
	if len([]rune(title)) > 80 {
		r := []rune(title)
		title = string(r[:77]) + "..."
	}
	return title
}

func (m *Model) renderViewportWithScrollbar() string {
	view := m.viewport.View()
	lines := strings.Split(view, "\n")
	height := m.viewport.Height()
	width := m.viewport.Width()

	// viewport.View() should already render to height, but pad defensively so the
	// scrollbar always has a stable full-height track.
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	lines = m.applySelectionToViewportLines(lines)

	total := m.viewport.TotalLineCount()
	if total <= height || height <= 0 {
		for i := range lines {
			lines[i] = lipgloss.NewStyle().Width(width).Render(lines[i]) + " "
		}
		return strings.Join(lines, "\n")
	}

	thumbSize := height * height / total
	if thumbSize < 1 {
		thumbSize = 1
	}
	if thumbSize > height {
		thumbSize = height
	}

	maxTop := height - thumbSize
	maxOffset := total - height
	thumbTop := 0
	if maxOffset > 0 {
		thumbTop = m.viewport.YOffset() * maxTop / maxOffset
	}

	trackStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	thumbStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	for i := range lines {
		bar := trackStyle.Render("│")
		if i >= thumbTop && i < thumbTop+thumbSize {
			bar = thumbStyle.Render("█")
		}
		lines[i] = lipgloss.NewStyle().Width(width).Render(lines[i]) + bar
	}

	return strings.Join(lines, "\n")
}

func (m *Model) renderThinkingBar() string {
	if !m.streaming {
		return ""
	}

	spinners := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinner := spinners[m.thinkingTick%len(spinners)]

	label := "AI 正在总结..."
	if m.phase == PhaseChat {
		label = "AI 正在思考..."
	}

	barWidth := m.width - 4
	if barWidth < 10 {
		barWidth = 10
	}
	// Create a pulsing progress block that moves left-to-right
	cycle := barWidth + 6
	pos := m.thinkingTick % cycle
	// The "lit" segment is 4 chars wide
	start := pos - 4
	if start < 0 {
		start = 0
	}
	end := pos
	if end > barWidth {
		end = barWidth
	}
	// Guard: when pos > barWidth + 4, start can exceed end
	if start > end {
		start = end
	}

	var bar strings.Builder
	for i := 0; i < barWidth; i++ {
		if i >= start && i < end {
			bar.WriteString("█")
		} else {
			bar.WriteString("░")
		}
	}
	barStr := bar.String()

	text := fmt.Sprintf(" %s %s", spinner, label)

	thinkingStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("39"))

	barLit := lipgloss.NewStyle().
		Foreground(lipgloss.Color("39"))

	barDim := lipgloss.NewStyle().
		Foreground(lipgloss.Color("237"))

	return thinkingStyle.Render(text) + "\n" +
		barDim.Render(barStr[:start]) +
		barLit.Render(barStr[start:end]) +
		barDim.Render(barStr[end:])
}

func (m *Model) renderSeparator() string {
	line := strings.Repeat("─", m.width)
	return separatorStyle.Render(line)
}

func (m *Model) renderNormalModeBar() string {
	bar := lipgloss.NewStyle().
		Background(lipgloss.Color("236")).
		Foreground(lipgloss.Color("250")).
		Padding(0, 1).
		Width(m.width).
		Render("NORMAL  i 编辑  │  j/k 滚动  │  J/K 跳10行  │  gg/G 首尾  │  Ctrl+D/U 半页  │  Ctrl+C 两次退出")
	return "\n" + bar
}

func (m *Model) renderHeader() string {
	title := "PaperPaper"
	if m.mode == ModeList {
		if m.listKind == ListKindRounds {
			title = "问答列表"
		} else {
			title = "恢复会话"
		}
	} else if p := m.manager.Paper(); p != nil && p.Title != "" {
		title = p.Title
	}

	model := m.cfg.API.DefaultModel
	phaseStr := "📄 Init"
	if m.phase == PhaseChat {
		phaseStr = "💬 Chat"
	}
	if m.mode == ModeList {
		if m.listKind == ListKindRounds {
			phaseStr = "🔎 Rounds"
		} else {
			phaseStr = "📋 Resume"
		}
	}

	left := titleStyle.Render(title)
	center := dimStyle.Render(model)
	right := phaseStr

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(center) - lipgloss.Width(right) - 4
	if gap < 0 {
		gap = 0
	}

	header := fmt.Sprintf("%s %s%s %s",
		left,
		center,
		strings.Repeat(" ", gap),
		right,
	)

	return lipgloss.NewStyle().
		Width(m.width).
		Background(lipgloss.Color("235")).
		Padding(0, 1).
		Render(header)
}

func (m *Model) renderInput() string {
	input := m.textarea.View()

	modeHint := ""
	switch m.mode {
	case ModeNormal:
		modeHint = m.renderNormalModeBar()
	case ModeInput:
		modeHint = dimStyle.Render(" [INPUT] Enter:发送 Shift+Enter:换行 Esc:浏览模式 /:命令提示")
	}

	content := input + modeHint
	if suggestions := m.renderCommandSuggestions(); suggestions != "" {
		content += "\n" + suggestions
	}

	return content
}

func (m *Model) renderStatusBar() string {
	p := m.manager.Paper()
	rounds := 0
	tokens := 0
	if p != nil {
		rounds = len(p.Messages) / 2
		tokens = p.TotalTokens
	}

	left := fmt.Sprintf("Tokens: %s", formatNumber(tokens))
	right := fmt.Sprintf("Rounds: %d", rounds)
	if m.rawMode {
		right = rawLabelStyle.Render("RAW") + " " + right
	}
	if m.statusNotice != "" {
		left = m.statusNotice
	}

	if m.err != nil {
		left = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(fmt.Sprintf("Error: %s", m.err))
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if gap < 0 {
		gap = 0
	}

	bar := fmt.Sprintf(" %s%s%s ", left, strings.Repeat(" ", gap), right)

	return lipgloss.NewStyle().
		Width(m.width).
		Background(lipgloss.Color("235")).
		Render(bar)
}

func (m *Model) renderList() string {
	if m.listKind == ListKindRounds {
		return m.renderRoundList()
	}
	return m.renderResumeList()
}

func (m *Model) renderResumeList() string {
	if len(m.resumeItems) == 0 {
		return bannerStyle.Render("没有历史论文。")
	}

	var b strings.Builder
	b.WriteString("\n")

	itemStyle := lipgloss.NewStyle().Padding(0, 2)
	selectedStyle := lipgloss.NewStyle().Padding(0, 2).Foreground(lipgloss.Color("39")).Bold(true)
	confirmStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)

	for i, item := range m.resumeItems {
		title := item.Title
		if title == "" {
			title = "Paper " + item.ShortRef()
		}

		line := fmt.Sprintf("  %s  %s", item.ShortRef(), title)

		if i == m.listCursor {
			b.WriteString(selectedStyle.Render("▸ " + strings.TrimPrefix(line, "  ")))
		} else {
			b.WriteString(itemStyle.Render(line))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if m.confirmDelete {
		b.WriteString(confirmStyle.Render("  确认删除？ y:确认 n:取消"))
	} else {
		b.WriteString(dimStyle.Render("  j/k:移动 Enter:恢复 d:删除 Esc:返回"))
	}

	return b.String()
}

func (m *Model) renderRoundList() string {
	if len(m.roundItems) == 0 {
		return bannerStyle.Render("当前论文还没有可跳转的问答轮次。")
	}

	var b strings.Builder
	b.WriteString("\n")
	itemStyle := lipgloss.NewStyle().Padding(0, 2)
	selectedStyle := lipgloss.NewStyle().Padding(0, 2).Foreground(lipgloss.Color("39")).Bold(true)

	for i, item := range m.roundItems {
		line := fmt.Sprintf("  #%d  %s", item.Display, item.Title)
		if i == m.listCursor {
			b.WriteString(selectedStyle.Render("▸ " + strings.TrimPrefix(line, "  ")))
		} else {
			b.WriteString(itemStyle.Render(line))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  j/k:移动 Enter:跳转到该轮 Esc:返回"))
	return b.String()
}

func (m *Model) renderCommandSuggestions() string {
	value := strings.TrimSpace(m.textarea.Value())
	if m.mode != ModeInput || !strings.HasPrefix(value, "/") {
		return ""
	}
	var lines []string
	for _, c := range commands {
		if strings.HasPrefix(c.Name, value) || value == "/" {
			lines = append(lines, fmt.Sprintf("  %-20s %s", c.Usage, c.Description))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "  未知命令，输入 /help 查看全部命令")
	}
	if len(lines) > 8 {
		lines = lines[:8]
		lines = append(lines, "  ...")
	}
	return dimStyle.Render(strings.Join(lines, "\n"))
}

func (m *Model) renderMessages() string {
	p := m.manager.Paper()
	if p == nil {
		return bannerStyle.Render("欢迎使用 PaperPaper!\n\n请输入 arXiv 链接或 ID，然后按 Enter 开始抓取并总结。\n\n也可以粘贴论文全文，或使用 /new <arxiv/url/path> 从 arXiv、URL 或文件加载。")
	}

	var b strings.Builder

	// Initial summary
	if p.InitialSummary != "" {
		if m.rawMode {
			b.WriteString(aiStyle.Render("📋 初始总结"))
			b.WriteString(" ")
			b.WriteString(rawLabelStyle.Render("[RAW]"))
			b.WriteString("\n")
			b.WriteString(rawBorderStyle.Render(rawTextStyle.Render(p.InitialSummary)))
		} else {
			rendered := m.renderMarkdown(p.InitialSummary)
			b.WriteString(rendered)
		}
		b.WriteString("\n")
		sepWidth := m.width - 4
		if sepWidth < 1 {
			sepWidth = 1
		}
		b.WriteString(separatorStyle.Render(strings.Repeat("─", sepWidth)))
		b.WriteString("\n")
	}

	// Messages
	for _, msg := range p.Messages {
		b.WriteString(m.renderMessage(msg))
		b.WriteString("\n")
	}

	// Streaming content
	if m.streaming && m.streamContent != "" {
		b.WriteString(aiStyle.Render("🤖 AI: "))
		if m.rawMode {
			b.WriteString(rawLabelStyle.Render("[RAW]"))
			b.WriteString(" ")
			b.WriteString(rawTextStyle.Render(m.streamContent))
		} else {
			rendered := m.renderMarkdown(m.streamContent)
			b.WriteString(rendered)
		}
		b.WriteString(dimStyle.Render(" ▍"))
		b.WriteString("\n")
	}

	return b.String()
}
func (m *Model) renderMessage(msg session.Message) string {
	var b strings.Builder

	if msg.Role == "user" {
		header := userStyle.Render("📝 You:")
		b.WriteString(header)
		b.WriteString(" ")
		b.WriteString(msg.Content)
		b.WriteString("\n")
		if msg.Digest != "" {
			b.WriteString(dimStyle.Render("   摘要: " + msg.Digest))
			b.WriteString("\n")
		}
		b.WriteString(dimStyle.Render(fmt.Sprintf("   [Tokens: ~%d]", msg.TokenCount)))
	} else {
		header := aiStyle.Render("🤖 AI:")
		b.WriteString(header)
		if m.rawMode {
			b.WriteString(" ")
			b.WriteString(rawLabelStyle.Render("[RAW]"))
		}
		b.WriteString(" ")
		if m.rawMode {
			// Raw mode: show content as-is, no markdown rendering
			rawContent := rawBorderStyle.Render(rawTextStyle.Render(msg.Content))
			b.WriteString(rawContent)
		} else {
			rendered := m.renderMarkdown(msg.Content)
			// Add indentation for AI messages
			lines := strings.Split(rendered, "\n")
			for i, line := range lines {
				if i > 0 {
					b.WriteString("   ")
				}
				b.WriteString(line)
				if i < len(lines)-1 {
					b.WriteString("\n")
				}
			}
		}
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("   [Tokens: %d]", msg.TokenCount)))
	}

	return b.String()
}

func formatNumber(n int) string {
	str := fmt.Sprintf("%d", n)
	if len(str) <= 3 {
		return str
	}

	var result []byte
	for i, c := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
