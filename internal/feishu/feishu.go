package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/prompt"
	"github.com/happyTonakai/paperagent/internal/session"
	"github.com/happyTonakai/paperagent/internal/urlparse"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// chatSession tracks the active paper for each chat.
type chatSession struct {
	mu         sync.Mutex
	paperID    string // session_id of active paper
	chatID     string
	streaming  bool   // true if currently streaming summary
}

// Bot is the Feishu bot for PaperAgent.
type Bot struct {
	cfg        *config.Config
	apiClient  *api.Client
	client     *lark.Client
	replayClient *lark.Client
	replayMu     sync.Mutex
	wsClient   *larkws.Client
	cancel     context.CancelFunc
	sessions   map[string]*chatSession // chatID -> session
	sessionsMu sync.Mutex

	mu        sync.RWMutex
	connected bool
	lastError string
}

// feishuRequestFunc is the callback type used by withFreshTenantAccessTokenRetry.
type feishuRequestFunc func(client *lark.Client, options ...larkcore.RequestOptionFunc) error

// New creates a new Feishu bot. Always returns a non-nil Bot (even when disabled)
// so that hot-reload via the Web UI works. Call Start() to connect.
func New(cfg *config.Config) *Bot {
	return &Bot{
		cfg:      cfg,
		sessions: make(map[string]*chatSession),
	}
}

// Status returns the current connection status for observability.
func (b *Bot) Status() (enabled, connected bool, lastError string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cfg.Feishu.Enabled && b.cfg.Feishu.AppID != "" && b.cfg.Feishu.AppSecret != "",
		b.connected,
		b.lastError
}

// Start starts the Feishu bot WebSocket connection.
func (b *Bot) Start() error {
	if b.cfg.Feishu.AppID == "" || b.cfg.Feishu.AppSecret == "" {
		return fmt.Errorf("feishu: app_id or app_secret not configured")
	}

	b.client = lark.NewClient(b.cfg.Feishu.AppID, b.cfg.Feishu.AppSecret)
	if b.apiClient == nil {
		b.apiClient = api.NewClient(b.cfg)
	}

	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(b.onMessage).
		OnP2CardActionTrigger(b.onCardAction)

	b.wsClient = larkws.NewClient(
		b.cfg.Feishu.AppID,
		b.cfg.Feishu.AppSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)

	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

	log.Printf("[feishu] 🚀 connecting to Feishu WebSocket (app_id=%s)...", maskAppID(b.cfg.Feishu.AppID))

	go func() {
		err := b.wsClient.Start(ctx)
		b.mu.Lock()
		if err != nil {
			b.connected = false
			b.lastError = err.Error()
			log.Printf("[feishu] ❌ WebSocket disconnected: %v", err)
		} else {
			b.connected = false
			b.lastError = ""
			log.Printf("[feishu] WebSocket closed normally")
		}
		b.mu.Unlock()
	}()

	// Mark as connected after a short delay (WS start is async but usually connects fast)
	go func() {
		time.Sleep(3 * time.Second)
		b.mu.Lock()
		// Only mark connected if we're still running (cancel not called yet)
		if b.cancel != nil && ctx.Err() == nil {
			b.connected = true
			b.lastError = ""
			log.Printf("[feishu] ✅ WebSocket connected! Bot is ready to receive messages.")
		} else if b.lastError != "" {
			log.Printf("[feishu] ❌ connection failed: %s", b.lastError)
		}
		b.mu.Unlock()
	}()

	return nil
}

// Stop stops the Feishu bot.
func (b *Bot) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	b.connected = false
	b.lastError = ""
	log.Printf("[feishu] bot stopped")
}

// Reload stops the current connection, re-reads config, and restarts if enabled.
func (b *Bot) Reload() error {
	b.Stop()

	if !b.cfg.Feishu.Enabled {
		log.Printf("[feishu] disabled in config, not starting")
		return nil
	}
	if b.cfg.Feishu.AppID == "" || b.cfg.Feishu.AppSecret == "" {
		log.Printf("[feishu] app_id or app_secret empty, not starting")
		return nil
	}

	return b.Start()
}

// getSession retrieves or creates a chat session.
func (b *Bot) getSession(chatID string) *chatSession {
	b.sessionsMu.Lock()
	defer b.sessionsMu.Unlock()
	if s, ok := b.sessions[chatID]; ok {
		return s
	}
	s := &chatSession{chatID: chatID}
	b.sessions[chatID] = s
	return s
}

// onMessage handles incoming messages from Feishu.
func (b *Bot) onMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	msg := event.Event.Message
	if msg == nil || msg.MessageType == nil {
		return nil
	}

	msgType := *msg.MessageType
	if msgType != "text" {
		return nil
	}

	chatID := ""
	if msg.ChatId != nil {
		chatID = *msg.ChatId
	}
	messageID := ""
	if msg.MessageId != nil {
		messageID = *msg.MessageId
	}

	content := ""
	if msg.Content != nil {
		content = *msg.Content
	}

	var textBody struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &textBody); err != nil {
		return nil
	}

	text := strings.TrimSpace(textBody.Text)
	// Strip @mentions
	text = stripAtMentions(text)

	log.Printf("[feishu] received: chat=%s text=%s", chatID, text)

	// Route to command handler
	go b.handleCommand(chatID, messageID, text)

	return nil
}

// onCardAction handles interactive card button clicks.
func (b *Bot) onCardAction(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	if event.Event == nil || event.Event.Action == nil {
		return nil, nil
	}

	actionVal, _ := event.Event.Action.Value["action"].(string)
	paperID, _ := event.Event.Action.Value["paper_id"].(string)

	chatID := ""
	if event.Event.Context != nil {
		chatID = event.Event.Context.OpenChatID
	}

	log.Printf("[feishu] card action: %s paper=%s chat=%s", actionVal, paperID, chatID)

	switch {
	case strings.HasPrefix(actionVal, "open:"):
		// User clicked a paper in /list to open it
		paperID = strings.TrimPrefix(actionVal, "open:")
		return b.handleCardOpenPaper(paperID, chatID)
	case strings.HasPrefix(actionVal, "qa:"):
		// Resume Q&A for a paper that already has a summary
		paperID = strings.TrimPrefix(actionVal, "qa:")
		return b.handleCardResumeQA(paperID, chatID)
	}

	return nil, nil
}

// handleCommand routes slash commands.
func (b *Bot) handleCommand(chatID, messageID, text string) {
	s := b.getSession(chatID)

	switch {
	case strings.HasPrefix(text, "/new "):
		url := strings.TrimSpace(strings.TrimPrefix(text, "/new "))
		b.cmdNew(chatID, messageID, url)
	case text == "/new":
		b.sendText(chatID, "请提供论文链接，例如：\n`/new https://arxiv.org/abs/2106.09685`")
	case text == "/list":
		b.cmdList(chatID)
	case text == "/summary":
		b.cmdSummary(chatID)
	case strings.HasPrefix(text, "/fetch"):
		b.cmdFetch(chatID, text)
	case text == "/help":
		b.sendText(chatID, helpText)
	case strings.HasPrefix(text, "/btw "):
		btwQuestion := strings.TrimSpace(strings.TrimPrefix(text, "/btw "))
		if btwQuestion == "" {
			b.sendText(chatID, "请提供问题，例如：\n`/btw 什么是注意力机制？`")
			return
		}
		s.mu.Lock()
		paperID := s.paperID
		s.mu.Unlock()
		if paperID == "" {
			paperID = session.GetActivePaper()
			if paperID != "" {
				s.mu.Lock()
				s.paperID = paperID
				s.mu.Unlock()
			}
		}
		if paperID != "" {
			b.cmdChat(chatID, messageID, paperID, btwQuestion, true)
		} else {
			b.sendText(chatID, "请先使用 `/new <链接>` 创建一篇论文，然后再进行问答。")
		}
	case strings.HasPrefix(text, "/"):
		b.sendText(chatID, "未知命令。可用命令：\n• `/new <链接>` — 新建论文总结\n• `/list` — 查看文章列表\n• `/summary` — 查看当前论文总结\n• `/fetch [n]` — 拉取最近 n 轮问答\n• `/btw <问题>` — 提问但不记入上下文\n• `/help` — 查看帮助")
	default:
		// Treat as Q&A if there's an active paper
		s.mu.Lock()
		paperID := s.paperID
		streaming := s.streaming
		s.mu.Unlock()

		if streaming {
			b.sendText(chatID, "⏳ 正在生成总结中，请稍后再提问...")
			return
		}

		if paperID == "" {
			// Fall back to global active paper (survives restarts)
			paperID = session.GetActivePaper()
			if paperID != "" {
				// Restore per-chat session from global state
				s.mu.Lock()
				s.paperID = paperID
				s.mu.Unlock()
				log.Printf("[feishu] restored active paper %s for chat %s", paperID, chatID)
			}
		}

		if paperID != "" {
			b.cmdChat(chatID, messageID, paperID, text, false)
		} else {
			b.sendText(chatID, "请先使用 `/new <链接>` 创建一篇论文，然后再进行问答。\n\n使用 `/help` 查看所有命令。")
		}
	}
}

// cmdNew creates a new paper from a URL and streams the summary.
func (b *Bot) cmdNew(chatID, messageID, url string) {
	s := b.getSession(chatID)

	s.mu.Lock()
	if s.streaming {
		s.mu.Unlock()
		b.sendText(chatID, "⏳ 正在处理上一篇文章，请稍候...")
		return
	}
	s.streaming = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.streaming = false
		s.mu.Unlock()
	}()

	// Fetch paper content
	content, sourceURL, err := b.fetchContent(url)
	if err != nil {
		log.Printf("[feishu] fetch error: %v", err)
		b.sendText(chatID, fmt.Sprintf("❌ 获取论文失败：%v", err))
		return
	}

	log.Printf("[feishu] fetched %d chars for %s", len(content), sourceURL)

	// Create paper
	paper := session.NewPaper(content, sourceURL)
	paper.ModelUsed = b.cfg.API.DefaultModel

	// Try HTML title extraction for arXiv
	if _, arxivID, ok := urlparse.NormalizeArxivInput(sourceURL); ok {
		if title, err := urlparse.FetchArxivTitle(arxivID); err == nil && title != "" {
			paper.SetTitle(title)
			log.Printf("[feishu] title from HTML: %s", title)
		}
	}

	if err := paper.Save(); err != nil {
		log.Printf("[feishu] save error: %v", err)
		b.sendText(chatID, "❌ 保存论文失败")
		return
	}

	// Set as active paper for this chat AND globally
	s.mu.Lock()
	s.paperID = paper.Ref()
	s.mu.Unlock()
	if err := session.SetActivePaper(paper.Ref()); err != nil {
		log.Printf("[feishu] set active paper error: %v", err)
	}

	log.Printf("[feishu] paper created: %s", paper.Ref())

	// Start streaming summary via interactive card
	b.streamSummary(chatID, paper)
}

// sendInteractiveCard sends an interactive card and returns the message ID (may be empty on failure).
func (b *Bot) sendInteractiveCard(chatID, cardJSON string) string {
	ctx := context.Background()
	var msgID string
	_ = b.doFeishuCall(ctx, "send card", func(client *lark.Client) error {
		resp, e := client.Im.Message.Create(ctx,
			larkim.NewCreateMessageReqBuilder().
				ReceiveIdType("chat_id").
				Body(larkim.NewCreateMessageReqBodyBuilder().
					ReceiveId(chatID).
					MsgType(larkim.MsgTypeInteractive).
					Content(cardJSON).
					Build()).
				Build())
		if e != nil {
			return fmt.Errorf("send card: %w", e)
		}
		if !resp.Success() {
			return fmt.Errorf("send card: code=%d msg=%s", resp.Code, resp.Msg)
		}
		if resp.Data != nil && resp.Data.MessageId != nil {
			msgID = *resp.Data.MessageId
		}
		return nil
	})
	return msgID
}

// streamSummary streams the summary generation progress via interactive cards.
// When a card fills up, it is frozen and a new streaming continuation card is sent.
func (b *Bot) streamSummary(chatID string, paper *session.Paper) {
	// Send loading card
	cardMsgID := b.sendInteractiveCard(chatID, buildLoadingCard(paper.Ref(), paper.Title))
	if cardMsgID == "" {
		log.Printf("[feishu] failed to send loading card")
		b.sendText(chatID, "❌ 发送进度卡片失败")
		return
	}

	// Multi-card streaming state
	type cardSlot struct {
		id      string // Feishu message ID
		startAt int    // byte offset in totalContent where this card's content starts
	}
	slots := []cardSlot{{id: cardMsgID, startAt: 0}}

	// Build messages for summary
	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetHeavy()},
		{Role: "user", Content: paper.Content},
	}

	ch := b.apiClient.ChatStream(b.cfg.API.DefaultModel, messages)
	var totalContent strings.Builder
	var promptTokens, completionTokens, cachedTokens int
	lastPatch := 0

	for chunk := range ch {
		if chunk.Err != nil {
			log.Printf("[feishu] stream error: %v", chunk.Err)
			b.sendText(chatID, fmt.Sprintf("❌ 总结生成失败：%v", chunk.Err))
			return
		}
		if chunk.Done {
			promptTokens = chunk.PromptTokens
			completionTokens = chunk.CompletionTokens
			cachedTokens = chunk.CachedTokens
			break
		}
		totalContent.WriteString(chunk.Content)
		total := totalContent.String()

		// Patch active card every ~200 new chars
		if len(total)-lastPatch < 200 {
			continue
		}
		lastPatch = len(total)

		active := &slots[len(slots)-1]
		cardContent := total[active.startAt:]
		isFirst := len(slots) == 1

		fits, overflow := fitMarkdownContent(cardContent, func(c string) string {
			if isFirst {
				return buildStreamingCard(paper.Ref(), paper.Title, c)
			}
			return buildStreamingContinuationCard(c)
		})

		if overflow != "" {
			// Freeze current card
			b.patchCard(active.id, buildContinuationCard(fits))

			// Send new streaming continuation card
			overflowStart := active.startAt + len(fits)
			overflowContent := total[overflowStart:]
			newID := b.sendInteractiveCard(chatID, buildStreamingContinuationCard(overflowContent))
			if newID != "" {
				slots = append(slots, cardSlot{id: newID, startAt: overflowStart})
				log.Printf("[feishu] summary card full -> card #%d (total so far: %d chars)", len(slots), len(total))
			} else {
				log.Printf("[feishu] failed to send continuation card, overflow lost")
			}
		} else {
			// Normal streaming update
			if isFirst {
				b.patchCard(active.id, buildStreamingCard(paper.Ref(), paper.Title, fits))
			} else {
				b.patchCard(active.id, buildStreamingContinuationCard(fits))
			}
		}
	}

	summary := totalContent.String()
	log.Printf("[feishu] summary complete: %d chars across %d cards", len(summary), len(slots))

	// Save summary
	paper.SetInitialSummary(summary)
	paper.Save()

	// Finalize last card (may still overflow at the very end)
	last := &slots[len(slots)-1]
	lastContent := summary[last.startAt:]

	fits, overflow := fitMarkdownContent(lastContent, func(c string) string {
		return buildDoneCard(paper.Ref(), paper.Title, c, promptTokens, completionTokens, cachedTokens)
	})

	if overflow != "" {
		// Last card's content still doesn't fit — freeze as continuation, send one more done card
		b.patchCard(last.id, buildContinuationCard(fits))
		b.sendInteractiveCard(chatID, buildDoneCard(paper.Ref(), paper.Title, overflow, promptTokens, completionTokens, cachedTokens))
	} else if len(slots) == 1 {
		// Single card: patch from streaming to done in-place
		b.patchCard(last.id, buildDoneCard(paper.Ref(), paper.Title, fits, promptTokens, completionTokens, cachedTokens))
	} else {
		// Last of multiple cards: patch to done
		b.patchCard(last.id, buildDoneCard(paper.Ref(), paper.Title, fits, promptTokens, completionTokens, cachedTokens))
	}
}

// cmdList shows recent 10 papers as an interactive card.
func (b *Bot) cmdList(chatID string) {
	papers, err := session.ListPapers()
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("❌ 获取文章列表失败：%v", err))
		return
	}

	if len(papers) == 0 {
		b.sendText(chatID, "📭 还没有任何文章。\n使用 `/new <链接>` 创建第一篇吧！")
		return
	}

	// Take most recent 10
	if len(papers) > 10 {
		papers = papers[:10]
	}

	// Check if there's a currently selected paper to highlight
	s := b.getSession(chatID)
	s.mu.Lock()
	selectedID := s.paperID
	s.mu.Unlock()

	var cardJSON string
	if selectedID != "" {
		cardJSON = marshalCard(buildPaperListCardWithSelection(papers, selectedID))
	} else {
		cardJSON = buildPaperListCard(papers)
	}

	ctx := context.Background()
	err2 := b.doFeishuCall(ctx, "send list card", func(client *lark.Client) error {
		resp, e := client.Im.Message.Create(ctx,
			larkim.NewCreateMessageReqBuilder().
				ReceiveIdType("chat_id").
				Body(larkim.NewCreateMessageReqBodyBuilder().
					ReceiveId(chatID).
					MsgType(larkim.MsgTypeInteractive).
					Content(cardJSON).
					Build()).
				Build())
		if e != nil {
			return fmt.Errorf("create message: %w", e)
		}
		if !resp.Success() {
			return fmt.Errorf("create message: code=%d msg=%s", resp.Code, resp.Msg)
		}
		return nil
	})

	if err2 != nil {
		log.Printf("[feishu] failed to send list card: %v", err2)
		b.sendText(chatID, "❌ 发送列表卡片失败")
	}
}

// cmdSummary sends the initial summary of the active paper as text.
func (b *Bot) cmdSummary(chatID string) {
	s := b.getSession(chatID)
	s.mu.Lock()
	paperID := s.paperID
	s.mu.Unlock()

	if paperID == "" {
		paperID = session.GetActivePaper()
		if paperID != "" {
			s.mu.Lock()
			s.paperID = paperID
			s.mu.Unlock()
		}
	}

	if paperID == "" {
		b.sendText(chatID, "请先使用 `/new` 创建论文或用 `/list` 选择一篇。")
		return
	}
	paper, err := session.LoadPaperByRef(paperID)
	if err != nil || paper.InitialSummary == "" {
		b.sendText(chatID, "总结尚未生成或已丢失。")
		return
	}
	title := paper.Title
	if title == "" {
		title = paperRef(paper)
	}
	b.sendOverflowAsText(chatID, paper.InitialSummary, fmt.Sprintf("📄 %s", title))
}

// cmdFetch fetches recent n rounds of Q&A (default 2) as text.
func (b *Bot) cmdFetch(chatID, text string) {
	s := b.getSession(chatID)
	s.mu.Lock()
	paperID := s.paperID
	s.mu.Unlock()

	if paperID == "" {
		paperID = session.GetActivePaper()
		if paperID != "" {
			s.mu.Lock()
			s.paperID = paperID
			s.mu.Unlock()
		}
	}

	if paperID == "" {
		b.sendText(chatID, "请先使用 `/new` 创建论文或用 `/list` 选择一篇。")
		return
	}
	paper, err := session.LoadPaperByRef(paperID)
	if err != nil {
		b.sendText(chatID, "找不到该文章。")
		return
	}
	n := 2
	parts := strings.Fields(text)
	if len(parts) >= 2 {
		if parsed, err := strconv.Atoi(parts[1]); err == nil && parsed > 0 && parsed <= 20 {
			n = parsed
		}
	}
	msgs := paper.Messages
	if len(msgs) == 0 {
		b.sendText(chatID, "还没有任何问答记录。")
		return
	}
	// Collect last n Q&A pairs
	var rounds []fetchRound
	seen := map[int]bool{}
	for i := len(msgs) - 1; i >= 0 && len(rounds) < n; i-- {
		m := msgs[i]
		if m.Role != "assistant" || seen[m.RoundNumber] {
			continue
		}
		var q string
		for j := i - 1; j >= 0; j-- {
			if msgs[j].RoundNumber == m.RoundNumber && msgs[j].Role == "user" {
				q = msgs[j].Content
				break
			}
		}
		rounds = append(rounds, fetchRound{m.RoundNumber, q, m.Content})
		seen[m.RoundNumber] = true
	}
	for i, j := 0, len(rounds)-1; i < j; i, j = i+1, j-1 {
		rounds[i], rounds[j] = rounds[j], rounds[i]
	}
	title := paper.Title
	if title == "" {
		title = paperRef(paper)
	}
	b.sendOverflowAsText(chatID, formatRounds(rounds), fmt.Sprintf("💬 %s · 最近 %d 轮", title, n))
}

// cmdChat handles a Q&A round for an active paper.
func (b *Bot) cmdChat(chatID, messageID, paperID, question string, skipContext bool) {
	paper, err := session.LoadPaperByRef(paperID)
	if err != nil {
		b.sendText(chatID, "❌ 找不到该文章，请重新 `/new` 创建。")
		return
	}

	// If summary is still empty, tell user to wait
	if paper.InitialSummary == "" {
		b.sendText(chatID, "⏳ 总结还在生成中，请稍候...")
		return
	}

	round := paper.CurrentRound() + 1

	// Build messages (exclude btw rounds from context)
	recent := paper.RecentContextMessages(b.cfg.UI.MaxRecentRounds)
	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetLight()},
		{Role: "user", Content: fmt.Sprintf("以下是论文全文：\n\n%s", paper.Content)},
	}
	for _, msg := range recent {
		messages = append(messages, api.ChatMessage{Role: msg.Role, Content: msg.Content})
	}
	// Add current question
	messages = append(messages, api.ChatMessage{Role: "user", Content: question})

	// Send initial "thinking" card
	cardMsgID := b.sendInteractiveCard(chatID, buildThinkingCard(paperID, paper.Title))
	if cardMsgID == "" {
		log.Printf("[feishu] failed to send thinking card")
		// Fall back to text
		result, _, _, _, _, chatErr := b.apiClient.Chat(b.cfg.API.DefaultModel, messages)
		if chatErr != nil {
			b.sendText(chatID, fmt.Sprintf("❌ 回答失败：%v", chatErr))
			return
		}
		b.sendText(chatID, result)
		return
	}

	// Multi-card streaming state
	type chatCardSlot struct {
		id      string
		startAt int // byte offset in totalContent
	}
	slots := []chatCardSlot{{id: cardMsgID, startAt: 0}}

	// Stream the answer
	ch := b.apiClient.ChatStream(b.cfg.API.DefaultModel, messages)
	var totalContent strings.Builder
	var promptTokens, completionTokens, cachedTokens int
	lastPatch := 0

	for chunk := range ch {
		if chunk.Err != nil {
			log.Printf("[feishu] chat stream error: %v", chunk.Err)
			b.sendText(chatID, fmt.Sprintf("❌ 回答失败：%v", chunk.Err))
			return
		}
		if chunk.Done {
			promptTokens = chunk.PromptTokens
			completionTokens = chunk.CompletionTokens
			cachedTokens = chunk.CachedTokens
			break
		}
		totalContent.WriteString(chunk.Content)
		total := totalContent.String()

		if len(total)-lastPatch < 200 {
			continue
		}
		lastPatch = len(total)

		active := &slots[len(slots)-1]
		cardContent := total[active.startAt:]
		isFirst := len(slots) == 1

		fits, overflow := fitMarkdownContent(cardContent, func(c string) string {
			if isFirst {
				return buildChatStreamingCard(paperID, paper.Title, c)
			}
			return buildChatStreamingContinuationCard(c)
		})

		if overflow != "" {
			// Freeze current card as a continuation card
			b.patchCard(active.id, buildContinuationCard(fits))

			// Send new streaming continuation card
			overflowStart := active.startAt + len(fits)
			overflowContent := total[overflowStart:]
			newID := b.sendInteractiveCard(chatID, buildChatStreamingContinuationCard(overflowContent))
			if newID != "" {
				slots = append(slots, chatCardSlot{id: newID, startAt: overflowStart})
				log.Printf("[feishu] chat card full -> card #%d (total so far: %d chars)", len(slots), len(total))
			}
		} else {
			if isFirst {
				b.patchCard(active.id, buildChatStreamingCard(paperID, paper.Title, fits))
			} else {
				b.patchCard(active.id, buildChatStreamingContinuationCard(fits))
			}
		}
	}

	answer := totalContent.String()

	// Save messages
	paper.AddMessage(session.Message{
		RoundNumber: round,
		Role:        "user",
		Content:     question,
		TokenCount:  session.EstimateTokens(question),
		SkipContext: skipContext,
	})
	paper.AddMessage(session.Message{
		RoundNumber:      round,
		Role:             "assistant",
		Content:          answer,
		TokenCount:       session.EstimateTokens(answer),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CachedTokens:     cachedTokens,
		SkipContext:      skipContext,
	})
	paper.Save()

	// Finalize last card
	last := &slots[len(slots)-1]
	lastContent := answer[last.startAt:]

	fits, overflow := fitMarkdownContent(lastContent, func(c string) string {
		return buildChatDoneCard(paperID, paper.Title, c, round, promptTokens, completionTokens, cachedTokens)
	})

	if overflow != "" {
		// Last card still doesn't fit — freeze, send one more done card
		b.patchCard(last.id, buildContinuationCard(fits))
		b.sendInteractiveCard(chatID, buildChatDoneCard(paperID, paper.Title, overflow, round, promptTokens, completionTokens, cachedTokens))
	} else {
		b.patchCard(last.id, buildChatDoneCard(paperID, paper.Title, fits, round, promptTokens, completionTokens, cachedTokens))
	}
}

func (b *Bot) handleCardOpenPaper(paperID, chatID string) (*callback.CardActionTriggerResponse, error) {
	paper, err := session.LoadPaperByRef(paperID)
	if err != nil {
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: "error", Content: "文章不存在"},
		}, nil
	}

	s := b.getSession(chatID)
	s.mu.Lock()
	s.paperID = paperID
	s.mu.Unlock()

	// Set as global active paper
	if err := session.SetActivePaper(paperID); err != nil {
		log.Printf("[feishu] set active paper error: %v", err)
	}

	// Rebuild the list card with the selected paper highlighted
	papers, _ := session.ListPapers()
	if len(papers) > 10 {
		papers = papers[:10]
	}

	title := paper.Title
	if title == "" {
		title = "Paper " + paperID[:8]
	}

	return &callback.CardActionTriggerResponse{
		Toast: &callback.Toast{
			Type:    "success",
			Content: fmt.Sprintf("已选中：%s", title),
		},
		Card: &callback.Card{
			Type: "raw",
			Data: buildPaperListCardWithSelection(papers, paperID),
		},
	}, nil
}

// handleCardResumeQA resumes Q&A for a paper that already has a summary.
func (b *Bot) handleCardResumeQA(paperID, chatID string) (*callback.CardActionTriggerResponse, error) {
	paper, err := session.LoadPaperByRef(paperID)
	if err != nil {
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: "error", Content: "文章不存在"},
		}, nil
	}

	s := b.getSession(chatID)
	s.mu.Lock()
	s.paperID = paperID
	s.mu.Unlock()

	// Set as global active paper
	if err := session.SetActivePaper(paperID); err != nil {
		log.Printf("[feishu] set active paper error: %v", err)
	}

	title := paper.Title
	if title == "" {
		title = "Paper " + paperID[:8]
	}

	return &callback.CardActionTriggerResponse{
		Toast: &callback.Toast{
			Type:    "success",
			Content: fmt.Sprintf("已切换到：%s\n可以直接提问啦 ✨", title),
		},
	}, nil
}

// fetchContent fetches paper content from a URL.
func (b *Bot) fetchContent(url string) (content, sourceURL string, err error) {
	if arxivURL, _, ok := urlparse.NormalizeArxivInput(url); ok {
		sourceURL = arxivURL
		content, err = urlparse.FetchURL(arxivURL)
	} else {
		sourceURL = url
		content, err = urlparse.FetchURL(url)
	}
	if err != nil {
		return "", "", fmt.Errorf("获取论文失败: %w", err)
	}
	if content == "" {
		return "", "", fmt.Errorf("论文内容为空")
	}
	return content, sourceURL, nil
}

// sendOverflowAsText sends overflow content as one or more text messages,
// splitting at natural boundaries to stay under Feishu's text message limit.
func (b *Bot) sendOverflowAsText(chatID, text, prefix string) {
	// Feishu text messages have a ~32KB payload limit.
	// We split conservatively at 20KB (about 6000 CJK chars).
	chunks := splitTextByBytes(text, 20000)
	for i, chunk := range chunks {
		msg := chunk
		if i == 0 && prefix != "" {
			msg = prefix + "\n\n" + chunk
		}
		b.sendText(chatID, msg)
	}
}

func (b *Bot) sendText(chatID, text string) {
	b.sendMessage(chatID, text)
}

// patchCard updates an existing interactive card via Patch API.
func (b *Bot) patchCard(messageID, cardJSON string) {
	if b.client == nil {
		return
	}
	ctx := context.Background()
	resp, err := b.client.Im.Message.Patch(ctx,
		larkim.NewPatchMessageReqBuilder().
			MessageId(messageID).
			Body(larkim.NewPatchMessageReqBodyBuilder().
				Content(cardJSON).
				Build()).
			Build())

	if err != nil || !resp.Success() {
		log.Printf("[feishu] patch card failed: err=%v %s", err, apiErrDetails(resp))
	}
}

func apiErrDetails(resp interface{}) string {
	if resp == nil {
		return "<nil response>"
	}
	type baseResp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return fmt.Sprintf("<marshal error: %v>", err)
	}
	var br baseResp
	if err := json.Unmarshal(b, &br); err != nil {
		return fmt.Sprintf("<unmarshal error: %v>", err)
	}
	return fmt.Sprintf("code=%d msg=%s", br.Code, br.Msg)
}

// stripAtMentions removes @bot mentions from text.
func stripAtMentions(text string) string {
	// Remove @_user_1 style mentions
	for {
		idx := strings.Index(text, "@_user_")
		if idx < 0 {
			break
		}
		end := idx + len("@_user_")
		for end < len(text) && text[end] >= '0' && text[end] <= '9' {
			end++
		}
		text = text[:idx] + text[end:]
	}
	// Remove @_all
	text = strings.ReplaceAll(text, "@_all", "")
	return strings.TrimSpace(text)
}

const helpText = "📚 **PaperAgent 飞书助手**\n\n" +
	"可用命令：\n" +
	"• **/new <链接>** — 创建新的论文总结\n" +
	"• **/list** — 查看最近 10 篇文章\n" +
	"• **/summary** — 查看当前论文的初始总结\n" +
	"• **/fetch [n]** — 拉取最近 n 轮问答（默认 2）\n" +
	"• **/btw <问题>** — 提问但不记入上下文\n" +
	"• **/help** — 显示本帮助\n" +
	"\n" +
	"直接发送文字即可对当前论文进行多轮 Q&A。"

func maskAppID(appID string) string {
	if len(appID) <= 8 {
		return "***"
	}
	return appID[:4] + "***" + appID[len(appID)-4:]
}

// ─── Message content auto-detection (markdown → card/post/text) ───

var mdIndicators = []string{"```", "**", "~~", "`", "\n- ", "\n* ", "\n1. ", "\n# ", "---"}

func hasMarkdown(s string) bool {
	for _, ind := range mdIndicators {
		if strings.Contains(s, ind) {
			return true
		}
	}
	return false
}

func countMdTables(s string) int {
	count := 0
	inTable := false
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		isTable := len(trimmed) > 1 && trimmed[0] == '|' && trimmed[len(trimmed)-1] == '|'
		if isTable && !inTable {
			count++
			inTable = true
		} else if !isTable {
			inTable = false
		}
	}
	return count
}

func buildPostMdJSON(content string) string {
	post := map[string]any{
		"zh_cn": map[string]any{
			"content": [][]map[string]any{
				{{"tag": "md", "text": content}},
			},
		},
	}
	b, _ := json.Marshal(post)
	return string(b)
}

// buildMessageContent picks the right msg_type based on content:
// - plain text → text
// - markdown with ≤5 tables → interactive card (best rendering)
// - markdown with >5 tables → post with md tag
func (b *Bot) buildMessageContent(text string) (msgType, body string) {
	if !hasMarkdown(text) {
		b, _ := json.Marshal(map[string]string{"text": text})
		return larkim.MsgTypeText, string(b)
	}
	if countMdTables(text) > 5 {
		return larkim.MsgTypePost, buildPostMdJSON(text)
	}
	return larkim.MsgTypeInteractive, buildCardMarkdown(text)
}

// sendMessage sends text with auto-detected message type.
func (b *Bot) sendMessage(chatID, text string) {
	if b.client == nil {
		log.Printf("[feishu] client not initialized")
		return
	}
	msgType, body := b.buildMessageContent(text)
	ctx := context.Background()
	err := b.doFeishuCall(ctx, "send message", func(client *lark.Client) error {
		resp, e := client.Im.Message.Create(ctx,
			larkim.NewCreateMessageReqBuilder().
				ReceiveIdType("chat_id").
				Body(larkim.NewCreateMessageReqBodyBuilder().
					ReceiveId(chatID).
					MsgType(msgType).
					Content(body).
					Build()).
				Build())
		if e != nil {
			return fmt.Errorf("send message: %w", e)
		}
		if !resp.Success() {
			return fmt.Errorf("send message: code=%d msg=%s", resp.Code, resp.Msg)
		}
		return nil
	})
	if err != nil {
		log.Printf("[feishu] send message failed: %v", err)
	}
}

// paperRef returns a short reference for a paper.
func paperRef(p *session.Paper) string {
	if p.SessionID != "" && len(p.SessionID) >= 8 {
		return p.SessionID[:8]
	}
	return fmt.Sprintf("%d", p.ID)
}

// fetchRound holds one Q&A pair for /fetch output.
type fetchRound struct {
	R int
	Q string
	A string
}

// formatRounds formats Q&A rounds as markdown.
func formatRounds(rounds []fetchRound) string {
	var sb strings.Builder
	for i, item := range rounds {
		if i > 0 {
			sb.WriteString("\n---\n")
		}
		sb.WriteString(fmt.Sprintf("**Q%d:** %s\n\n**A%d:** %s", item.R, item.Q, item.R, item.A))
	}
	return sb.String()
}

// ─── Retry infrastructure (modeled on cc-connect) ───

func (b *Bot) replayCli() *lark.Client {
	b.replayMu.Lock()
	defer b.replayMu.Unlock()
	if b.replayClient == nil {
		b.replayClient = lark.NewClient(
			b.cfg.Feishu.AppID, b.cfg.Feishu.AppSecret,
			lark.WithEnableTokenCache(false),
		)
	}
	return b.replayClient
}

func (b *Bot) fetchFreshToken(ctx context.Context) (string, error) {
	resp, err := b.replayCli().GetTenantAccessTokenBySelfBuiltApp(ctx, &larkcore.SelfBuiltTenantAccessTokenReq{
		AppID: b.cfg.Feishu.AppID, AppSecret: b.cfg.Feishu.AppSecret,
	})
	if err != nil {
		return "", fmt.Errorf("feishu: token fetch: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("feishu: token fetch code=%d msg=%s", resp.Code, resp.Msg)
	}
	return resp.TenantAccessToken, nil
}

func isTokenInvalid(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "99991663") ||
		strings.Contains(strings.ToLower(err.Error()), "invalid access token")
}

func isNetError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "connection reset") || strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "i/o timeout") || strings.Contains(s, "connection refused")
}

const maxRetries = 3

// doFeishuCall executes a Feishu API call with retry logic:
// - Transient net errors → exponential backoff retry
// - Invalid token error → refresh token, retry once
func (b *Bot) doFeishuCall(ctx context.Context, op string, call func(client *lark.Client) error) error {
	err := call(b.client)
	if err == nil {
		return nil
	}

	// Token invalid: refresh and retry once
	if isTokenInvalid(err) {
		if _, tokErr := b.fetchFreshToken(ctx); tokErr == nil {
			log.Printf("[feishu] %s: retrying with fresh token", op)
			return call(b.replayCli())
		}
		return err
	}

	// Network error: retry with backoff
	if isNetError(err) {
		delay := 500 * time.Millisecond
		for i := 0; i < maxRetries; i++ {
			jitter := time.Duration(rand.Int64N(int64(delay / 4)))
			time.Sleep(delay + jitter)
			if retryErr := call(b.client); retryErr == nil {
				log.Printf("[feishu] %s recovered after retry %d", op, i+1)
				return nil
			}
			delay *= 2
		}
	}

	return err
}
