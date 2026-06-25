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
	"github.com/happyTonakai/paperagent/internal/chat"
	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/database"
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
	mu        sync.Mutex
	chatID    string
	streaming bool // true if currently streaming summary
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

	// forcePush is an externally injected function that drains the pending
	// recommendation backlog and pushes it to the daily-recommend chat.
	// Set by the server via SetForcePushFunc. Returns the number of articles
	// pushed and any error. The Feishu /push command calls this regardless
	// of the holiday-skip rule, so the user can always force a push from
	// chat when they want to.
	forcePush func() (int, error)
}

// SetForcePushFunc injects the function invoked by the /push slash command.
// Typically wired up by the server so the bot and the scheduler share one
// push code path.
func (b *Bot) SetForcePushFunc(fn func() (int, error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.forcePush = fn
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
		// User clicked a paper in list/search card to open it
		paperID = strings.TrimPrefix(actionVal, "open:")
		pageStr, _ := event.Event.Action.Value["page"].(string)
		currentPage, _ := strconv.Atoi(pageStr)
		searchKeyword, _ := event.Event.Action.Value["search"].(string)
		return b.handleCardOpenPaper(paperID, chatID, currentPage, searchKeyword)
	case actionVal == "page_nav":
		// User clicked pagination button in list/search card
		pageStr, _ := event.Event.Action.Value["page"].(string)
		targetPage, _ := strconv.Atoi(pageStr)
		searchKeyword, _ := event.Event.Action.Value["search"].(string)
		return b.handlePageNav(targetPage, chatID, searchKeyword)
	case strings.HasPrefix(actionVal, "qa:"):
		// Resume Q&A for a paper that already has a summary
		paperID = strings.TrimPrefix(actionVal, "qa:")
		return b.handleCardResumeQA(paperID, chatID)
	case strings.HasPrefix(actionVal, "recommend:like:"):
		articleID := strings.TrimPrefix(actionVal, "recommend:like:")
		return b.handleRecommendLike(articleID, chatID)
	case strings.HasPrefix(actionVal, "recommend:dislike:"):
		articleID := strings.TrimPrefix(actionVal, "recommend:dislike:")
		return b.handleRecommendDislike(articleID, chatID)
	case strings.HasPrefix(actionVal, "recommend:activate:"):
		articleID := strings.TrimPrefix(actionVal, "recommend:activate:")
		return b.handleRecommendActivate(articleID, chatID)
	case actionVal == "recommend:mark-read-page":
		// Bulk mark all articles in the current page as read.
		rawIDs, _ := event.Event.Action.Value["paper_ids"].([]any)
		ids := make([]string, 0, len(rawIDs))
		for _, v := range rawIDs {
			if s, ok := v.(string); ok {
				ids = append(ids, s)
			}
		}
		return b.handleRecommendMarkReadPage(ids, chatID)
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
	case strings.HasPrefix(text, "/search "):
		keywords := strings.TrimSpace(strings.TrimPrefix(text, "/search "))
		b.cmdSearch(chatID, keywords)
	case text == "/search":
		b.sendText(chatID, "请提供搜索关键词，例如：\n`/search transformer`")
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
		paperID := session.GetActivePaper()
		if paperID != "" {
			b.cmdChat(chatID, messageID, paperID, btwQuestion, true)
		} else {
			b.sendText(chatID, "请先使用 `/new <链接>` 创建一篇论文，然后再进行问答。")
		}
	case strings.HasPrefix(text, "/rate "):
		ratingStr := strings.TrimSpace(strings.TrimPrefix(text, "/rate "))
		b.cmdRate(chatID, ratingStr)
	case text == "/pin":
		b.cmdPinToggle(chatID)
	case text == "/push":
		b.cmdPush(chatID)
	case strings.HasPrefix(text, "/"):
		b.sendText(chatID, "未知命令。可用命令：\n• `/new <链接>` — 新建论文总结\n• `/list` — 查看文章列表\n• `/summary` — 查看当前论文总结\n• `/fetch [n]` — 拉取最近 n 轮问答\n• `/btw <问题>` — 提问但不记入上下文\n• `/rate <1-10>` — 给当前论文打分\n• `/pin` — 置顶/取消置顶当前论文\n• `/push` — 立即推送积压推荐\n• `/help` — 查看帮助")
	default:
		// Check if the input is just an arXiv URL/ID — auto-create new paper.
		if arxivURL, _, ok := urlparse.NormalizeArxivInput(text); ok {
			b.cmdNew(chatID, messageID, arxivURL)
			return
		}

		// Treat as Q&A if there's an active paper
		paperID := session.GetActivePaper()
		s.mu.Lock()
		streaming := s.streaming
		s.mu.Unlock()

		if streaming {
			b.sendText(chatID, "⏳ 正在生成总结中，请稍后再提问...")
			return
		}

		if paperID != "" {
			b.cmdChat(chatID, messageID, paperID, text, false)
		} else {
			b.sendText(chatID, "请先使用 `/new <链接>` 创建一篇论文，然后再进行问答。\n\n使用 `/help` 查看所有命令。")
		}
	}
}

// cmdPush drains the pending recommendation backlog and pushes it to the
// configured daily-recommend chat. Used when the user wants to override the
// holiday-skip rule (e.g. on a long weekend they want the papers anyway).
// Target chat is the daily_recommend_chat_id, not the chat where /push was
// sent from — keeping a single merge point matches the scheduler's behavior.
func (b *Bot) cmdPush(chatID string) {
	b.mu.RLock()
	fn := b.forcePush
	b.mu.RUnlock()

	if fn == nil {
		b.sendText(chatID, "❌ 推送服务未初始化。请检查服务端是否正常运行。")
		return
	}

	b.sendText(chatID, "⏳ 正在推送待阅推荐……")

	n, err := fn()
	if err != nil {
		log.Printf("[feishu] cmdPush: force push error: %v", err)
		b.sendText(chatID, fmt.Sprintf("❌ 推送失败：%v", err))
		return
	}
	if n == 0 {
		b.sendText(chatID, "✅ 推送完成：暂无积压推荐。")
		return
	}
	b.sendText(chatID, fmt.Sprintf("✅ 已推送 %d 篇推荐到每日推荐群。", n))
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
	content, sourceURL, arxivID, err := b.fetchContent(context.Background(), url)
	if err != nil {
		log.Printf("[feishu] fetch error: %v", err)
		b.sendText(chatID, fmt.Sprintf("❌ 获取论文失败：%v", err))
		return
	}

	log.Printf("[feishu] fetched %d chars for %s", len(content), sourceURL)

	// Check for existing paper with the same arXiv ID.
	if arxivID != "" {
		if existing, err := session.FindPaperByArxivID(arxivID); err == nil && existing != nil {
			log.Printf("[feishu] paper with arxiv ID %s already exists: %s", arxivID, existing.Ref())
			// Set as active paper
			if err := session.SetActivePaper(existing.Ref()); err != nil {
				log.Printf("[feishu] set active paper error: %v", err)
			}
			title := existing.Title
			if title == "" {
				title = paperRef(existing)
			}
			b.sendText(chatID, fmt.Sprintf(
				"📋 **%s**\n\n该论文已存在，已激活。\n如果需要继续提问直接问就行。\n如果需要原始总结请输入 `/summary`\n如果需要最近几轮的QA请输入 `/fetch n`",
				title,
			))
			return
		}
	}

	// Create paper
	paper := session.NewPaper(content, sourceURL, arxivID)
	paper.ModelUsed = b.cfg.API.DefaultModel

	// Try HTML title extraction for arXiv
	if arxivID != "" {
		if title, err := urlparse.FetchArxivTitleCtx(context.Background(), arxivID); err == nil && title != "" {
			paper.SetTitle(title)
			log.Printf("[feishu] title from HTML: %s", title)
		}
		// Cache the abstract for preference updates. We deliberately do NOT
		// write into the `articles` table: that table is the RSS-sourced
		// recommendation pool and any entry with status=0/score=0 would be
		// picked up by MarkDailyRecommendations and pushed to the user.
		// chat_paper_abstracts is the dedicated cache for Q&A abstracts.
		if abstract, err := urlparse.FetchArxivAbstractCtx(context.Background(), arxivID); err == nil {
			if err := database.UpsertChatPaperAbstract(arxivID, abstract); err != nil {
				log.Printf("[feishu] cache abstract for %s: %v", arxivID, err)
			}
		} else {
			log.Printf("[feishu] abstract extraction for %s: %v", arxivID, err)
		}
	}

	// Add round 0 user message FIRST so the paper has content before any save.
	paper.AddMessage(session.Message{
		RoundNumber: 0,
		Role:        "user",
		Content:     content,
		TokenCount:  session.EstimateTokens(content),
	})

	if err := paper.Save(); err != nil {
		log.Printf("[feishu] save error: %v", err)
		b.sendText(chatID, "❌ 保存论文失败")
		return
	}

	// Set as global active paper
	if err := session.SetActivePaper(paper.Ref()); err != nil {
		log.Printf("[feishu] set active paper error: %v", err)
	}

	log.Printf("[feishu] paper created: %s", paper.Ref())

	// Start streaming summary via interactive card
	b.streamSummary(chatID, paper)
}

// sendInteractiveCard sends an interactive card and returns (message ID, error).
// The message ID may be empty even on success if the API response doesn't
// include one. Callers that don't care about the error (e.g. fire-and-forget
// streaming updates) should use `_ = b.sendInteractiveCard(...)`.
func (b *Bot) sendInteractiveCard(chatID, cardJSON string) (string, error) {
	ctx := context.Background()
	var msgID string
	err := b.doFeishuCall(ctx, "send card", func(client *lark.Client) error {
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
			// Log the full Feishu error here too, not just return it:
			// doFeishuCall only logs on retry/recovery, not on the final
			// failure, so without this the cause of a push failure would
			// only surface in the HTTP 500 body the caller sees — invisible
			// to the in-memory log buffer that the Web UI's log panel
			// reads. resp.Msg often embeds the ext=ErrCode/ErrMsg detail
			// (e.g. "Failed to create card content, ext=ErrCode: 11310;
			// ErrMsg: element exceeds the limit") so logging it is
			// enough to root-cause the failure.
			log.Printf("[feishu] send card: FAIL code=%d msg=%s", resp.Code, resp.Msg)
			return fmt.Errorf("send card: code=%d msg=%s", resp.Code, resp.Msg)
		}
		if resp.Data != nil && resp.Data.MessageId != nil {
			msgID = *resp.Data.MessageId
		}
		return nil
	})
	return msgID, err
}

// streamSummary streams the summary generation progress via interactive cards.
// When a card fills up, it is frozen and a new streaming continuation card is sent.
func (b *Bot) streamSummary(chatID string, paper *session.Paper) {
	// Send loading card
	cardMsgID, _ := b.sendInteractiveCard(chatID, buildLoadingCard(paper.Ref(), paper.Title))
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
		{Role: "system", Content: prompt.GetSystem()},
		{Role: "user", Content: paper.Content},
		{Role: "user", Content: prompt.GetHeavy()},
	}

	ch := b.apiClient.ChatStream(b.cfg.API.DefaultModel, messages, nil)
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
			converted := latexToUnicode(c)
			if isFirst {
				return buildStreamingCard(paper.Ref(), paper.Title, converted)
			}
			return buildStreamingContinuationCard(converted)
		})

		if overflow != "" {
			// Freeze current card (converted)
			b.patchCard(active.id, buildContinuationCard(latexToUnicode(fits)))

			// Send new streaming continuation card (converted)
			overflowStart := active.startAt + len(fits)
			overflowContent := total[overflowStart:]
			newID, _ := b.sendInteractiveCard(chatID, buildStreamingContinuationCard(latexToUnicode(overflowContent)))
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
	paper.AddMessage(session.Message{
		RoundNumber:      0,
		Role:             "assistant",
		Content:          summary,
		TokenCount:       session.EstimateTokens(summary),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CachedTokens:     cachedTokens,
		SkipContext:      true,
	})
	paper.Save()

	// Finalize last card (may still overflow at the very end)
	last := &slots[len(slots)-1]
	lastContent := summary[last.startAt:]

	fits, overflow := fitMarkdownContent(lastContent, func(c string) string {
		return buildDoneCard(paper.Ref(), paper.Title, latexToUnicode(c), promptTokens, completionTokens, cachedTokens)
	})

	if overflow != "" {
		// Last card's content still doesn't fit — freeze as continuation, send one more done card
		b.patchCard(last.id, buildContinuationCard(latexToUnicode(fits)))
		_, _ = b.sendInteractiveCard(chatID, buildDoneCard(paper.Ref(), paper.Title, latexToUnicode(overflow), promptTokens, completionTokens, cachedTokens))
	} else if len(slots) == 1 {
		// Single card: patch from streaming to done in-place
		b.patchCard(last.id, buildDoneCard(paper.Ref(), paper.Title, latexToUnicode(fits), promptTokens, completionTokens, cachedTokens))
	} else {
		// Last of multiple cards: patch to done
		b.patchCard(last.id, buildDoneCard(paper.Ref(), paper.Title, latexToUnicode(fits), promptTokens, completionTokens, cachedTokens))
	}
}

// pageSize is the number of papers per page in list/search cards.
const pageSize = 8

// cmdList shows papers as a paginated interactive card.
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

	totalCount := len(papers)
	page := 0
	end := pageSize
	if end > totalCount {
		end = totalCount
	}
	pagePapers := papers[:end]

	// Determine selected paper — use global active paper
	selectedID := session.GetActivePaper()
	log.Printf("[feishu] cmdList: selectedID=%q pagePapers=%d total=%d", selectedID, len(pagePapers), totalCount)

	cardJSON := marshalCard(buildPaperListCardPaginated(pagePapers, totalCount, page, pageSize, selectedID, "", ""))

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

// cmdSearch searches papers by title keyword and shows a paginated card.
func (b *Bot) cmdSearch(chatID, keyword string) {
	if keyword == "" {
		b.sendText(chatID, "请提供搜索关键词，例如：\n`/search transformer`")
		return
	}

	allPapers, err := session.ListPapers()
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("❌ 搜索失败：%v", err))
		return
	}

	keywordLower := strings.ToLower(keyword)
	var matched []session.PaperSummary
	for _, p := range allPapers {
		if strings.Contains(strings.ToLower(p.Title), keywordLower) {
			matched = append(matched, p)
		}
	}

	if len(matched) == 0 {
		b.sendText(chatID, fmt.Sprintf("🔍 没有找到标题包含「%s」的文章", keyword))
		return
	}

	totalCount := len(matched)
	page := 0
	end := pageSize
	if end > totalCount {
		end = totalCount
	}
	pagePapers := matched[:end]

	// Check if there's a currently selected paper to highlight
	selectedID := session.GetActivePaper()

	cardJSON := marshalCard(buildPaperListCardPaginated(pagePapers, totalCount, page, pageSize, selectedID,
		fmt.Sprintf("🔍 搜索结果：%s", keyword), keyword))

	ctx := context.Background()
	err2 := b.doFeishuCall(ctx, "send search card", func(client *lark.Client) error {
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
		log.Printf("[feishu] failed to send search card: %v", err2)
		b.sendText(chatID, "❌ 发送搜索结果卡片失败")
	}
}

// cmdRate rates the active paper with a score (1-10).
func (b *Bot) cmdRate(chatID, ratingStr string) {
	paperID := session.GetActivePaper()
	if paperID == "" {
		b.sendText(chatID, "请先使用 `/new` 创建论文或用 `/list` 选择一篇。")
		return
	}
	rating, err := strconv.Atoi(ratingStr)
	if err != nil || rating < 1 || rating > 10 {
		b.sendText(chatID, "评分必须在 1 到 10 之间，例如：`/rate 8`")
		return
	}
	paper, err := session.LoadPaperByRef(paperID)
	if err != nil {
		b.sendText(chatID, "找不到该文章。")
		return
	}
	paper.Rating = rating
	if err := paper.Save(); err != nil {
		b.sendText(chatID, "❌ 保存评分失败")
		return
	}
	title := paper.Title
	if title == "" {
		title = paperRef(paper)
	}
	b.sendText(chatID, fmt.Sprintf("⭐ **%s** 评分已设为 **%d/10**", title, rating))
}

// cmdPinToggle toggles the pinned status of the active paper.
func (b *Bot) cmdPinToggle(chatID string) {
	paperID := session.GetActivePaper()
	if paperID == "" {
		b.sendText(chatID, "请先使用 `/new` 创建论文或用 `/list` 选择一篇。")
		return
	}
	paper, err := session.LoadPaperByRef(paperID)
	if err != nil {
		b.sendText(chatID, "找不到该文章。")
		return
	}
	paper.Pinned = !paper.Pinned
	if err := paper.Save(); err != nil {
		b.sendText(chatID, "❌ 保存置顶状态失败")
		return
	}
	title := paper.Title
	if title == "" {
		title = paperRef(paper)
	}
	if paper.Pinned {
		b.sendText(chatID, fmt.Sprintf("📌 **%s** 已置顶", title))
	} else {
		b.sendText(chatID, fmt.Sprintf("📌 **%s** 已取消置顶", title))
	}
}

// cmdSummary sends the initial summary of the active paper as text.
func (b *Bot) cmdSummary(chatID string) {
	paperID := session.GetActivePaper()

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
	paperID := session.GetActivePaper()

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
	// Collect last n Q&A pairs (skip RoundNumber 0 = initial summary, use /summary instead)
	var rounds []fetchRound
	seen := map[int]bool{}
	for i := len(msgs) - 1; i >= 0 && len(rounds) < n; i-- {
		m := msgs[i]
		if m.Role != "assistant" || seen[m.RoundNumber] || m.RoundNumber == 0 {
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

	// Send initial "thinking" card. If the card send fails, fall back to a
	// synchronous Chat() call and send the answer as plain text — this
	// preserves the prior behavior where the user always gets an answer.
	cardMsgID, _ := b.sendInteractiveCard(chatID, buildThinkingCard(paperID, paper.Title))
	if cardMsgID == "" {
		log.Printf("[feishu] failed to send thinking card, falling back to text")
		// Build messages inline for the fallback path. This duplicates the
		// message construction the engine would do, but only on this rare
		// fallback branch; the streaming path below uses the shared engine.
		messages := chat.BuildMessages(paper, question, prompt.GetLight())
		result, _, _, _, _, _, chatErr := b.apiClient.Chat(b.cfg.API.DefaultModel, messages, nil)
		if chatErr != nil {
			b.sendText(chatID, fmt.Sprintf("❌ 回答失败：%v", chatErr))
			return
		}
		// Persist user + assistant messages on the fallback path so the
		// answer is not lost from the paper history. We pass zero token
		// counts because the synchronous Chat() call doesn't expose them;
		// SetAnchorFromTokens still anchors the round correctly (the
		// algorithm rounds up to min_recent_rounds when counts are zero).
		paper.AddMessage(session.Message{RoundNumber: round, Role: "user", Content: question, TokenCount: session.EstimateTokens(question), SkipContext: skipContext})
		paper.AddMessage(session.Message{RoundNumber: round, Role: "assistant", Content: result, TokenCount: session.EstimateTokens(result), SkipContext: skipContext})
		paper.SetAnchorFromTokens(round, 0, 0, b.cfg.UI.MaxInputTokens, b.cfg.UI.MinRecentRounds)
		paper.Save()
		b.sendText(chatID, result)
		return
	}

	// Hand the rest off to the shared chat engine. The engine handles
	// user-message persistence, LLM streaming (including tool-call
	// follow-up), and assistant-message persistence + anchor update.
	sink := newCardSink(b, chatID, paperID, paper.Title, cardMsgID, round)
	engine := chat.NewEngine(b.apiClient, b.cfg)
	tools, handlers := chat.BuildChatTools(paper)
	if err := engine.Answer(context.Background(), paper, question, skipContext, tools, handlers, sink); err != nil {
		log.Printf("[feishu] chat engine error: %v", err)
	}
}

func (b *Bot) handleCardOpenPaper(paperID, chatID string, currentPage int, searchKeyword string) (*callback.CardActionTriggerResponse, error) {
	paper, err := session.LoadPaperByRef(paperID)
	if err != nil {
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: "error", Content: "文章不存在"},
		}, nil
	}

	// Set as global active paper
	if err := session.SetActivePaper(paperID); err != nil {
		log.Printf("[feishu] set active paper error: %v", err)
	}

	allPapers, _ := session.ListPapers()

	// Filter by search keyword if in search mode
	var pagePapers []session.PaperSummary
	totalCount := 0
	if searchKeyword != "" {
		kw := strings.ToLower(searchKeyword)
		for _, p := range allPapers {
			if strings.Contains(strings.ToLower(p.Title), kw) {
				pagePapers = append(pagePapers, p)
			}
		}
		totalCount = len(pagePapers)
	} else {
		pagePapers = allPapers
		totalCount = len(allPapers)
	}

	page := currentPage
	if page*pageSize >= totalCount && totalCount > 0 {
		page = 0
	}
	start := page * pageSize
	end := start + pageSize
	if end > totalCount {
		end = totalCount
	}
	pagePapers = pagePapers[start:end]

	// Determine header title
	headerTitle := ""
	if searchKeyword != "" {
		headerTitle = fmt.Sprintf("🔍 搜索结果：%s", searchKeyword)
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
			Data: buildPaperListCardPaginated(pagePapers, totalCount, page, pageSize, paperID, headerTitle, searchKeyword),
		},
	}, nil
}

// handlePageNav navigates to a specific page in the list/search card.
func (b *Bot) handlePageNav(targetPage int, chatID string, searchKeyword string) (*callback.CardActionTriggerResponse, error) {
	allPapers, err := session.ListPapers()
	if err != nil {
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: "error", Content: "获取文章列表失败"},
		}, nil
	}

	// Filter by search keyword if in search mode
	var pagePapers []session.PaperSummary
	totalCount := 0
	if searchKeyword != "" {
		kw := strings.ToLower(searchKeyword)
		for _, p := range allPapers {
			if strings.Contains(strings.ToLower(p.Title), kw) {
				pagePapers = append(pagePapers, p)
			}
		}
		totalCount = len(pagePapers)
	} else {
		pagePapers = allPapers
		totalCount = len(allPapers)
	}

	if totalCount == 0 {
		return nil, nil
	}

	// Clamp to valid range
	page := targetPage
	totalPages := (totalCount + pageSize - 1) / pageSize
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	start := page * pageSize
	end := start + pageSize
	if end > totalCount {
		end = totalCount
	}
	pagePapers = pagePapers[start:end]

	// Determine header title
	headerTitle := ""
	if searchKeyword != "" {
		headerTitle = fmt.Sprintf("🔍 搜索结果：%s", searchKeyword)
	}

	// Get currently selected paper — use global active paper
	selectedID := session.GetActivePaper()
	log.Printf("[feishu] handlePageNav: page=%d selectedID=%q pagePapers=%d total=%d search=%q", page, selectedID, len(pagePapers), totalCount, searchKeyword)

	// Log each paper's ref for debugging
	for _, p := range pagePapers {
		if p.Ref() == selectedID {
			log.Printf("[feishu] handlePageNav: selected paper FOUND on this page: %s", p.Title)
		}
	}

	return &callback.CardActionTriggerResponse{
		Card: &callback.Card{
			Type: "raw",
			Data: buildPaperListCardPaginated(pagePapers, totalCount, page, pageSize, selectedID, headerTitle, searchKeyword),
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

// fetchContent fetches paper content from a URL. ctx is propagated to
// urlparse so the caller's cancellation (e.g., bot shutdown) terminates
// the HTTP request or arxiv2text subprocess.
func (b *Bot) fetchContent(ctx context.Context, url string) (content, sourceURL, arxivID string, err error) {
	if arxivURL, id, ok := urlparse.NormalizeArxivInput(url); ok {
		sourceURL = arxivURL
		arxivID = id
		content, err = urlparse.FetchURL(ctx, arxivURL)
	} else {
		sourceURL = url
		content, err = urlparse.FetchURL(ctx, url)
	}
	if err != nil {
		return "", "", "", fmt.Errorf("获取论文失败: %w", err)
	}
	if content == "" {
		return "", "", "", fmt.Errorf("论文内容为空")
	}
	return content, sourceURL, arxivID, nil
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
func (b *Bot) patchCard(messageID, cardJSON string) error {
	if b.client == nil {
		return fmt.Errorf("feishu: client not initialized")
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
		return fmt.Errorf("patch card %s: err=%v", messageID, err)
	}
	return nil
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
	"• **/list** — 查看文章列表（支持翻页）\n" +
		"• **/search <关键词>** — 搜索论文标题\n" +
	"• **/summary** — 查看当前论文的初始总结\n" +
	"• **/fetch [n]** — 拉取最近 n 轮问答（默认 2）\n" +
	"• **/btw <问题>** — 提问但不记入上下文\n" +
	"• **/rate <1-10>** — 给当前论文打分\n" +
	"• **/pin** — 置顶/取消置顶当前论文\n" +
	"• **/push** — 立即推送积压的每日推荐（绕过节假日跳过）\n" +
	"• **/help** — 显示本帮助\n" +
	"\n" +
	"直接发送文字即可对当前论文进行多轮 Q&A。"

func maskAppID(appID string) string {
	if len(appID) <= 8 {
		return "***"
	}
	return appID[:4] + "***" + appID[len(appID)-4:]
}

// ─── Daily Recommendation Push ───

// PushDailyRecommend sends the given recommended articles as one or more
// interactive cards to the specified Feishu chat. Called by the scheduler
// after the daily pipeline completes and by the Feishu /push command.
// Reads translations and statuses from SQLite (populated by
// translateAndPersistArticles in server.go and by the card-action handlers).
//
// Returns an error if any card send fails. Callers should NOT mark the
// articles as pushed_at on error, so a later push retries the entire batch.
func (b *Bot) PushDailyRecommend(chatID string, articles []database.Article) error {
	if b.client == nil {
		log.Printf("[feishu] PushDailyRecommend: client is nil, skipping")
		return fmt.Errorf("feishu client is nil")
	}
	if chatID == "" {
		log.Printf("[feishu] PushDailyRecommend: chatID is empty, skipping")
		return fmt.Errorf("chatID is empty")
	}
	if len(articles) == 0 {
		log.Printf("[feishu] PushDailyRecommend: no articles, skipping")
		return nil
	}
	log.Printf("[feishu] PushDailyRecommend: sending %d articles to chat %s", len(articles), chatID)

	// Prefer the freshest data from the DB (translations + statuses). Fall
	// back to the input articles if the DB query fails.
	ids := make([]string, len(articles))
	for i, a := range articles {
		ids[i] = a.ID
	}
	dbArticles, err := database.GetArticlesByIDs(ids)
	if err != nil || len(dbArticles) == 0 {
		dbArticles = articles
	}
	items := make([]RecommendCardItem, 0, len(dbArticles))
	for _, a := range dbArticles {
		title := a.Title
		abstract := ""
		if a.Abstract != nil {
			abstract = *a.Abstract
		}
		if a.TranslatedTitle != nil && *a.TranslatedTitle != "" {
			title = *a.TranslatedTitle
		}
		if a.TranslatedAbstract != nil && *a.TranslatedAbstract != "" {
			abstract = *a.TranslatedAbstract
		}
		items = append(items, RecommendCardItem{
			ID:         a.ID,
			Title:      title,
			Abstract:   abstract,
			PDFURL:     arxivAbsToPDF(a.Link),
			Score:      a.Score,
			Status:     a.Status,
			AXNetVotes: a.AXNetVotes,
		})
	}

	// Split into pages so each card stays under Feishu's JSON/element limits
	// while showing full abstracts. recommendPageSize articles per card.
	totalPages := (len(items) + recommendPageSize - 1) / recommendPageSize
	for page := 1; page <= totalPages; page++ {
		start := (page - 1) * recommendPageSize
		end := start + recommendPageSize
		if end > len(items) {
			end = len(items)
		}
		cardJSON := buildDailyRecommendCard(items[start:end], page, totalPages)
		if _, err := b.sendInteractiveCard(chatID, cardJSON); err != nil {
			// Log with page context so the in-memory log buffer (the
			// Web UI's log panel) shows WHICH page of how many failed
			// and the size it was. sendInteractiveCard already logs the
			// Feishu code/msg; this adds the page-level breadcrumb.
			log.Printf("[feishu] PushDailyRecommend: page %d/%d send failed (card=%dB, articles=%d): %v",
				page, totalPages, len(cardJSON), end-start, err)
			return fmt.Errorf("send daily recommend card %d/%d: %w", page, totalPages, err)
		}
		log.Printf("[feishu] sent daily recommend card %d/%d (%d articles) to chat %s",
			page, totalPages, end-start, chatID)
	}
	return nil
}

// loadRecommendItemsForToday returns today's recommended articles as
// RecommendCardItems, ordered by batch_order. Translations and statuses
// are read straight from the DB so the card reflects the latest state
// after a like / dislike / activate / mark-read-page click. Used by both
// the daily push and the card-action re-render path.
func (b *Bot) loadRecommendItemsForToday() ([]RecommendCardItem, error) {
	today := time.Now().Format("2006-01-02")
	dbArticles, err := database.GetArticlesByRecommendDate(today)
	if err != nil {
		return nil, err
	}
	items := make([]RecommendCardItem, 0, len(dbArticles))
	for _, a := range dbArticles {
		title := a.Title
		abstract := ""
		if a.Abstract != nil {
			abstract = *a.Abstract
		}
		if a.TranslatedTitle != nil && *a.TranslatedTitle != "" {
			title = *a.TranslatedTitle
		}
		if a.TranslatedAbstract != nil && *a.TranslatedAbstract != "" {
			abstract = *a.TranslatedAbstract
		}
		items = append(items, RecommendCardItem{
			ID:         a.ID,
			Title:      title,
			Abstract:   abstract,
			PDFURL:     arxivAbsToPDF(a.Link),
			Score:      a.Score,
			Status:     a.Status,
			AXNetVotes: a.AXNetVotes,
		})
	}
	return items, nil
}

// findRecommendPage returns the 1-indexed page that contains the article
// with the given ID, using recommendPageSize as the page size. Returns 1
// if the article is not in the list (caller falls back to rendering the
// first page).
func findRecommendPage(items []RecommendCardItem, articleID string) int {
	for i, item := range items {
		if item.ID == articleID {
			return i/recommendPageSize + 1
		}
	}
	return 1
}

// buildDailyRecommendCardForArticle rebuilds the page of the daily
// recommendation card that contains the given article, reflecting the
// latest statuses from the DB. Returns the card as a map so it can be
// embedded in callback.Card{Data: ...} for the card-action response. The
// returned card includes the per-article buttons in their post-action
// state (highlighted + disabled) when the user has already interacted.
func (b *Bot) buildDailyRecommendCardForArticle(articleID string) (map[string]any, error) {
	items, err := b.loadRecommendItemsForToday()
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no recommended articles for today")
	}
	page := findRecommendPage(items, articleID)
	totalPages := (len(items) + recommendPageSize - 1) / recommendPageSize
	start := (page - 1) * recommendPageSize
	end := start + recommendPageSize
	if end > len(items) {
		end = len(items)
	}
	cardJSON := buildDailyRecommendCard(items[start:end], page, totalPages)
	var cardMap map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &cardMap); err != nil {
		return nil, fmt.Errorf("unmarshal card: %w", err)
	}
	return cardMap, nil
}

// recommendResponseWithCard returns a CardActionTriggerResponse with both
// a toast and a refreshed card. On error (e.g. article not in today's
// list), returns a toast-only response so the user still sees feedback
// for their click.
func (b *Bot) recommendResponseWithCard(articleID, toastType, content string) *callback.CardActionTriggerResponse {
	card, err := b.buildDailyRecommendCardForArticle(articleID)
	if err != nil {
		log.Printf("[feishu] rebuild card for %s: %v", articleID, err)
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: toastType, Content: content},
		}
	}
	return &callback.CardActionTriggerResponse{
		Toast: &callback.Toast{Type: toastType, Content: content},
		Card: &callback.Card{
			Type: "raw",
			Data: card,
		},
	}
}

// handleRecommendLike marks an article as liked (status=2) and refreshes
// the card so the 👍 button is highlighted + disabled.
func (b *Bot) handleRecommendLike(articleID, chatID string) (*callback.CardActionTriggerResponse, error) {
	if err := database.UpdateArticleStatus(articleID, 2); err != nil {
		log.Printf("[feishu] recommend like error: %v", err)
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: "error", Content: "操作失败"},
		}, nil
	}
	return b.recommendResponseWithCard(articleID, "success", "已点赞 👍"), nil
}

// handleRecommendDislike marks an article as disliked (status=-1) and
// refreshes the card so the 👎 button is highlighted + disabled.
func (b *Bot) handleRecommendDislike(articleID, chatID string) (*callback.CardActionTriggerResponse, error) {
	if err := database.UpdateArticleStatus(articleID, -1); err != nil {
		log.Printf("[feishu] recommend dislike error: %v", err)
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: "error", Content: "操作失败"},
		}, nil
	}
	return b.recommendResponseWithCard(articleID, "success", "已点踩 👎"), nil
}

// handleRecommendMarkReadPage bulk-marks all articles in the current Feishu
// recommendation card page as read. Mirrors the WebUI's hover-to-read
// affordance and the "全部已读" button. Refreshes the card so the bulk
// button shows ✓ + disabled and the per-article buttons reflect the new
// state.
func (b *Bot) handleRecommendMarkReadPage(articleIDs []string, chatID string) (*callback.CardActionTriggerResponse, error) {
	if len(articleIDs) == 0 {
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: "warning", Content: "本页无文章"},
		}, nil
	}
	if err := database.BatchUpdateArticleStatus(articleIDs, 3); err != nil {
		log.Printf("[feishu] recommend mark-read-page error: %v", err)
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: "error", Content: "操作失败"},
		}, nil
	}
	log.Printf("[feishu] mark-read-page: marked %d articles as read (chat=%s)", len(articleIDs), chatID)
	// Re-render via the first ID (the page's items are batch_order-ordered,
	// so the first ID identifies the page unambiguously).
	firstID := articleIDs[0]
	if resp := b.recommendResponseWithCard(firstID, "success", fmt.Sprintf("已标记本页 %d 篇为已读", len(articleIDs))); resp != nil {
		return resp, nil
	}
	return &callback.CardActionTriggerResponse{
		Toast: &callback.Toast{Type: "success", Content: fmt.Sprintf("已标记本页 %d 篇为已读", len(articleIDs))},
	}, nil
}

// handleRecommendActivate activates a recommended paper in the Q&A system.
// It fetches the paper content from the article's URL, creates a session.Paper,
// and starts streaming the summary asynchronously. The card is refreshed so
// the 🤖 button is highlighted + disabled.
//
// Idempotency: if the article is already activated (status==1) the async
// cmdNew call is skipped. cmdNew's own dedup via FindPaperByArxivID would
// catch duplicates too, but it would still spam the chat with "正在处理"
// or "已激活" text messages from the dedup path. Skipping here keeps the
// chat clean and gives a clear "已激活过" toast on repeat clicks.
func (b *Bot) handleRecommendActivate(articleID, chatID string) (*callback.CardActionTriggerResponse, error) {
	article, err := database.GetArticleByID(articleID)
	if err != nil {
		log.Printf("[feishu] get article %s: %v", articleID, err)
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: "error", Content: "获取文章失败"},
		}, nil
	}
	if article == nil || article.Link == "" {
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: "error", Content: "文章不存在或无链接"},
		}, nil
	}

	alreadyActivated := article.Status == 1
	if !alreadyActivated {
		// Mark article as clicked (status=1). Failure is non-fatal: the
		// activate button still triggers cmdNew.
		_ = database.UpdateArticleStatus(articleID, 1)
	}

	toastContent := "正在获取论文，请稍候..."
	if alreadyActivated {
		toastContent = "已激活过，可直接提问"
	}
	resp := b.recommendResponseWithCard(articleID, "success", toastContent)

	if !alreadyActivated {
		// Launch async paper fetch + summary generation (reuses cmdNew logic).
		// cmdNew dedupes via FindPaperByArxivID, so a duplicate paper is
		// impossible even if the click races with another path.
		go b.cmdNew(chatID, "", article.Link)
	}

	return resp, nil
}

// ─── Message content auto-detection (markdown → card/post/text) ───

var mdIndicators = []string{"```", "**", "~~", "`", "\n- ", "\n* ", "\n1. ", "\n# ", "---", "> "}

func hasMarkdown(s string) bool {
	for _, ind := range mdIndicators {
		if strings.Contains(s, ind) {
			return true
		}
	}
	return false
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
	text = latexToUnicode(text)
	if !hasMarkdown(text) {
		b, _ := json.Marshal(map[string]string{"text": text})
		return larkim.MsgTypeText, string(b)
	}
	if countMdTables(text) > maxCardMdTables {
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
