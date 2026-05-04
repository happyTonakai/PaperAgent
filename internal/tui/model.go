package tui

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/paperpaper/paperpaper/internal/api"
	"github.com/paperpaper/paperpaper/internal/config"
	"github.com/paperpaper/paperpaper/internal/prompt"
	"github.com/paperpaper/paperpaper/internal/session"
)

type Mode int

const (
	ModeNormal Mode = iota
	ModeInput
	ModeList
)

type Phase int

const (
	PhaseInit Phase = iota
	PhaseChat
)

type streamMsg struct {
	chunk api.StreamChunk
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

	mode    Mode
	phase   Phase
	ready   bool
	width   int
	height  int

	streaming     bool
	streamContent string
	streamBuf     string
	streamChan    <-chan api.StreamChunk

	// List mode
	listItems  []session.PaperSummary
	listCursor int

	// Delete confirmation
	confirmDelete bool

	err error
}

func NewModel(cfg *config.Config) *Model {
	vp := viewport.New()
	ta := textarea.New()
	ta.Placeholder = "输入问题... (Enter 发送, Shift+Enter 换行)"
	ta.Focus()
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter"))

	return &Model{
		cfg:       cfg,
		apiClient: api.NewClient(cfg),
		manager:   session.NewManager(),
		viewport:  vp,
		textarea:  ta,
		mode:      ModeInput,
		phase:     PhaseInit,
	}
}

func (m *Model) LoadPaper(p *session.Paper) {
	m.manager.SetPaper(p)
	if p.InitialSummary != "" {
		m.phase = PhaseChat
		m.viewport.SetContent(m.renderMessages())
	} else {
		m.phase = PhaseInit
	}
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
