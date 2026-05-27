package tui

import (
	"regexp"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/paperpaper/paperpaper/internal/api"
	"github.com/paperpaper/paperpaper/internal/config"
	"github.com/paperpaper/paperpaper/internal/prompt"
	"github.com/paperpaper/paperpaper/internal/session"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
)

type Mode int

const (
	ModeNormal Mode = iota
	ModeInput
	ModeList
)

type ListKind int

const (
	ListKindNone ListKind = iota
	ListKindResume
	ListKindRounds
)

type roundListItem struct {
	Round   int
	Display int
	Title   string
	Digest  string
}

type Phase int

const (
	PhaseInit Phase = iota
	PhaseChat
)

type streamMsg struct {
	chunk api.StreamChunk
}

type selectionPoint struct {
	x int
	y int
}

type viewportSelection struct {
	selecting bool
	active    bool
	start     selectionPoint
	end       selectionPoint
}

type summarizeDoneMsg struct {
	summary string
}

type titleDoneMsg struct {
	title string
}

type digestDoneMsg struct {
	digest  string
	roundID int
}

type Model struct {
	cfg       *config.Config
	apiClient *api.Client
	manager   *session.Manager

	viewport viewport.Model
	textarea textarea.Model

	mode   Mode
	phase  Phase
	ready  bool
	width  int
	height int

	streaming     bool
	streamContent string
	streamBuf     string
	streamChan    <-chan api.StreamChunk

	// List mode
	listKind    ListKind
	resumeItems []session.PaperSummary
	roundItems  []roundListItem
	listCursor  int

	// Delete confirmation
	confirmDelete bool

	selection    viewportSelection
	statusNotice string
	quitArmed    bool

	err error

	// Markdown renderer cache
	glamourRenderer *glamour.TermRenderer

	// Cache rendered markdown output to avoid re-rendering old messages
	// on every streaming update. key = content, cleared on resize.
	renderCache     map[string]string
	renderCacheWidth int
}

func NewModel(cfg *config.Config) *Model {
	vp := viewport.New()
	ta := textarea.New()
	ta.Placeholder = "输入 arXiv 链接/ID，或粘贴论文内容... (Enter 发送)"
	ta.Focus()
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter"))

	return &Model{
		cfg:         cfg,
		apiClient:   api.NewClient(cfg),
		manager:     session.NewManager(),
		viewport:    vp,
		textarea:    ta,
		mode:        ModeInput,
		phase:       PhaseInit,
		renderCache: make(map[string]string),
	}
}

func (m *Model) LoadPaper(p *session.Paper) {
	m.manager.SetPaper(p)
	m.updateTextareaPlaceholder()
	if p.InitialSummary != "" {
		m.phase = PhaseChat
		m.viewport.SetContent(m.renderMessages())
	} else {
		m.phase = PhaseInit
	}
}

func (m *Model) updateTextareaPlaceholder() {
	if m.manager.Paper() == nil {
		m.textarea.Placeholder = "输入 arXiv 链接/ID，或粘贴论文内容... (Enter 发送)"
		return
	}
	m.textarea.Placeholder = "输入问题... (Enter 发送, Shift+Enter 换行)"
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink}

	// If a paper was pre-loaded (e.g. from CLI arg) but has no summary yet, start streaming
	if p := m.manager.Paper(); p != nil && p.InitialSummary == "" && !m.streaming {
		m.streaming = true
		m.streamContent = ""
		cmds = append(cmds, m.startStream([]api.ChatMessage{
			{Role: "system", Content: prompt.GetHeavy()},
			{Role: "user", Content: p.Content},
		}))
	}

	return tea.Batch(cmds...)
}

func (m *Model) startStream(messages []api.ChatMessage) tea.Cmd {
	ch := m.apiClient.ChatStream(m.cfg.API.DefaultModel, messages)
	m.streamChan = ch
	return m.nextStreamCmd(ch)
}

func (m *Model) nextStreamCmd(ch <-chan api.StreamChunk) tea.Cmd {
	return func() tea.Msg {
		chunk, ok := <-ch
		if !ok {
			return streamMsg{chunk: api.StreamChunk{Done: true}}
		}
		return streamMsg{chunk: chunk}
	}
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39"))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("141")).
			Bold(true)

	aiStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("117"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	bannerStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("39")).
			Padding(1, 3).
			MarginTop(1).
			MarginBottom(1)

	separatorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

// renderMarkdown renders markdown text with proper word wrap width
func (m *Model) renderMarkdown(text string) string {
	// Calculate target width: 2/3 of terminal width
	targetWidth := m.width * 2 / 3
	if targetWidth < 40 {
		targetWidth = 40
	}
	if targetWidth > 120 {
		targetWidth = 120
	}

	// Invalidate cache on resize
	if m.renderCacheWidth != targetWidth {
		m.renderCache = make(map[string]string)
		m.renderCacheWidth = targetWidth
	}

	// Return cached result if available (big win during streaming)
	if cached, ok := m.renderCache[text]; ok {
		return cached
	}

	// Create renderer once (use WordWrap=300 — wide enough to avoid
	// unnecessary wrapping but not so wide that glamour's padding becomes
	// expensive. We re-wrap at the target width below).
	if m.glamourRenderer == nil {
		style := styles.DarkStyleConfig
		style.H2.Prefix = ""
		style.H3.Prefix = ""
		style.H4.Prefix = ""
		style.H5.Prefix = ""
		style.H6.Prefix = ""
		// Use non-breaking space between bullet/number and text to prevent
		// lipgloss.Wrap from splitting them onto separate lines with CJK content.
		style.Item.BlockPrefix = "• "
		style.Enumeration.BlockPrefix = ") "

		renderer, err := glamour.NewTermRenderer(
			glamour.WithWordWrap(300),
			glamour.WithStyles(style),
		)
		if err != nil {
			return text
		}
		m.glamourRenderer = renderer
	}

	rendered, err := m.glamourRenderer.Render(preprocessMarkdown(text))
	if err != nil {
		return text
	}
	result := rebalanceWrap(rendered, targetWidth)
	m.renderCache[text] = result
	return result
}

var (
	blockDollarMathPattern  = regexp.MustCompile(`(?s)\$\$(.*?)\$\$`)
	blockBracketMathPattern = regexp.MustCompile(`(?s)\\\[(.*?)\\\]`)
	inlineParenMathPattern  = regexp.MustCompile(`\\\((.*?)\\\)`)
	inlineDollarMathPattern = regexp.MustCompile(`\$([^$\n]+?)\$`)
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// rebalanceWrap rewraps glamour's output to produce balanced line lengths for
// mixed CJK/English text. lipgloss.Wrap prefers breaking at spaces, which
// creates jagged edges when CJK text fills a line followed by a short English
// phrase. This wrapper breaks strictly at character boundaries, filling each
// line to the target width.
func rebalanceWrap(text string, targetWidth int) string {
	// glamour pads every line to its internal wrap width (10000). Measure
	// display width (ignoring ANSI codes) and trim padding.
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))

	for _, line := range lines {
		// Trim trailing padding spaces that glamour adds
		plain := ansiPattern.ReplaceAllString(line, "")
		plain = strings.TrimRight(plain, " ")

		w := ansi.StringWidth(plain)
		if w <= targetWidth {
			// Short line: keep ANSI styling, just trim trailing padding
			out = append(out, strings.TrimRight(line, " "))
			continue
		}

		// Extract leading whitespace (glamour's document margin)
		leading := ""
		for _, r := range plain {
			if r == ' ' || r == '\t' {
				leading += string(r)
			} else {
				break
			}
		}
		content := plain[len(leading):]
		contentWidth := targetWidth - ansi.StringWidth(leading)
		if contentWidth < 1 {
			contentWidth = 1
		}

		wrapped := wrapLineBalanced(content, contentWidth)
		for _, wl := range wrapped {
			out = append(out, leading+wl)
		}
	}
	return strings.Join(out, "\n")
}

// wrapLineBalanced wraps text at exact character boundaries, producing
// evenly-filled lines regardless of CJK/ASCII character mix.
func wrapLineBalanced(text string, width int) []string {
	if width < 1 {
		return []string{text}
	}
	var lines []string
	var cur strings.Builder
	curWidth := 0

	for _, r := range text {
		rw := ansi.StringWidth(string(r))
		if curWidth+rw > width && curWidth > 0 {
			lines = append(lines, cur.String())
			cur.Reset()
			curWidth = 0
		}
		cur.WriteRune(r)
		curWidth += rw
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}

func preprocessMarkdown(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = blockDollarMathPattern.ReplaceAllStringFunc(text, func(match string) string {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, "$$"), "$$"))
		if inner == "" {
			return match
		}
		return "\n\n```math\n" + inner + "\n```\n\n"
	})
	text = blockBracketMathPattern.ReplaceAllStringFunc(text, func(match string) string {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, `\[`), `\]`))
		if inner == "" {
			return match
		}
		return "\n\n```math\n" + inner + "\n```\n\n"
	})
	text = inlineParenMathPattern.ReplaceAllString(text, "`$1`")
	text = inlineDollarMathPattern.ReplaceAllString(text, "`$1`")
	return text
}

type commandInfo struct {
	Name        string
	Usage       string
	Description string
}

var commands = []commandInfo{
	{Name: "/new", Usage: "/new [arxiv/url/path]", Description: "新建会话，可从 arXiv、URL 或文件加载"},
	{Name: "/resume", Usage: "/resume", Description: "恢复历史论文会话"},
	{Name: "/list", Usage: "/list", Description: "列出当前论文的问答轮次并快速跳转"},
	{Name: "/open", Usage: "/open <session-id>", Description: "按 session ID 打开历史会话"},
	{Name: "/delete", Usage: "/delete", Description: "删除当前会话"},
	{Name: "/edit", Usage: "/edit", Description: "编辑最近一次问题"},
	{Name: "/del", Usage: "/del <round>", Description: "删除指定问答轮次"},
	{Name: "/summarize", Usage: "/summarize", Description: "对当前对话生成元总结"},
	{Name: "/export", Usage: "/export", Description: "导出到 Obsidian"},
	{Name: "/model", Usage: "/model [name]", Description: "查看或切换模型"},
	{Name: "/config", Usage: "/config", Description: "查看当前配置"},
	{Name: "/help", Usage: "/help", Description: "显示帮助"},
	{Name: "/quit", Usage: "/quit", Description: "保存并退出"},
}

func commandHelpText() string {
	var b strings.Builder
	b.WriteString("可用命令:\n\n")
	for _, c := range commands {
		b.WriteString("  ")
		b.WriteString(c.Usage)
		if len(c.Usage) < 18 {
			b.WriteString(strings.Repeat(" ", 18-len(c.Usage)))
		} else {
			b.WriteString("  ")
		}
		b.WriteString(c.Description)
		b.WriteString("\n")
	}
	return b.String()
}
