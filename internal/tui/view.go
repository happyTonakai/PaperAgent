package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
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
		b.WriteString(m.viewport.View())
	}
	b.WriteString("\n")

	// Input area (hidden in list mode)
	if m.mode != ModeList {
		b.WriteString(m.renderInput())
		b.WriteString("\n")
	}

	// Status bar
	b.WriteString(m.renderStatusBar())

	return tea.View{
		Content:   b.String(),
		AltScreen: true,
		MouseMode: tea.MouseModeCellMotion,
	}
}

func (m *Model) renderHeader() string {
	title := "PaperPaper"
	if m.mode == ModeList {
		title = "会话列表"
	} else if p := m.manager.Paper(); p != nil && p.Title != "" {
		title = p.Title
	}

	model := m.cfg.API.DefaultModel
	phaseStr := "📄 Init"
	if m.phase == PhaseChat {
		phaseStr = "💬 Chat"
	}
	if m.mode == ModeList {
		phaseStr = "📋 List"
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
		modeHint = dimStyle.Render(" [NORMAL] i:编辑 j/k:滚动 q:退出")
	case ModeInput:
		modeHint = dimStyle.Render(" [INPUT] Enter:发送 Shift+Enter:换行 Esc:退出 /list:会话列表")
	}

	return lipgloss.NewStyle().
		BorderTop(true).
		BorderForeground(lipgloss.Color("240")).
		Render(input + modeHint)
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
	if len(m.listItems) == 0 {
		return bannerStyle.Render("没有历史论文。")
	}

	var b strings.Builder
	b.WriteString("\n")

	itemStyle := lipgloss.NewStyle().Padding(0, 2)
	selectedStyle := lipgloss.NewStyle().Padding(0, 2).Foreground(lipgloss.Color("39")).Bold(true)
	confirmStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)

	for i, item := range m.listItems {
		title := item.Title
		if title == "" {
			title = fmt.Sprintf("Paper #%d", item.ID)
		}

		line := fmt.Sprintf("  %d. %s", item.ID, title)

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
		b.WriteString(dimStyle.Render("  j/k:移动 Enter:打开 d:删除 Esc:返回"))
	}

	return b.String()
}

func (m *Model) renderMessages() string {
	p := m.manager.Paper()
	if p == nil {
		return bannerStyle.Render("欢迎使用 PaperPaper!\n\n请粘贴论文内容，然后按 Ctrl+D 或 Alt+Enter 提交。")
	}

	var b strings.Builder

	// Initial summary
	if p.InitialSummary != "" {
		rendered, err := glamour.Render(p.InitialSummary, "dark")
		if err == nil {
			b.WriteString(rendered)
		} else {
			b.WriteString(p.InitialSummary)
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
		rendered, err := glamour.Render(m.streamContent, "dark")
		if err == nil {
			b.WriteString(rendered)
		} else {
			b.WriteString(m.streamContent)
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
		if msg.Digest != "" {
			b.WriteString(msg.Digest)
		} else {
			content := msg.Content
			if len(content) > 100 {
				content = content[:100] + "..."
			}
			b.WriteString(content)
		}
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("   [Tokens: ~%d]", msg.TokenCount)))
	} else {
		header := aiStyle.Render("🤖 AI:")
		b.WriteString(header)
		b.WriteString(" ")
		rendered, err := glamour.Render(msg.Content, "dark")
		if err == nil {
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
		} else {
			content := msg.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			b.WriteString(content)
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
