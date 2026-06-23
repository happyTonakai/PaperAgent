package server

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/database"
	"github.com/happyTonakai/paperagent/internal/feishu"
	"github.com/happyTonakai/paperagent/internal/holiday"
	"github.com/happyTonakai/paperagent/internal/scheduler"
)

//go:embed frontend-dist
var frontendDist embed.FS

// holidayChecker is a package-level singleton shared by all Server instances.
// Initialized lazily on first call to getHolidayChecker() via sync.Once so
// concurrent callers (e.g. simultaneous scheduler + Web UI requests on first
// boot) cannot race to construct duplicate chains.
var (
	holidayChecker     *holiday.Checker
	holidayCheckerOnce sync.Once
)

func getHolidayChecker() *holiday.Checker {
	holidayCheckerOnce.Do(func() {
		// Provider chain is consulted in order: the first to respond
		// successfully wins. The order is intentional — Timor has the
		// richest response and is our primary source; OneAPI is an
		// independent mirror with a clean status-based schema; Bitefu is a
		// last-resort minimalist that just returns a single digit. If all
		// three fail, Checker falls back to the weekday rule
		// (Sat/Sun = holiday).
		chain := holiday.NewChain(
			holiday.NewTimorProvider(),
			holiday.NewOneAPIProvider(),
			holiday.NewBitefuProvider(),
		)
		holidayChecker = holiday.NewChecker(chain)
	})
	return holidayChecker
}

type Server struct {
	cfg             *config.Config
	api             *api.Client
	scoringAPI      *api.Client
	translationAPI  *api.Client
	mux             *http.ServeMux
	paperLocks      sync.Map
	logBuf          *logBuffer
	feishuBot       *feishu.Bot
	sched           *scheduler.Scheduler
}

// SetFeishuBot sets the feishu bot reference for hot-reload support.
// It also wires the bot's /push command to this server's RunPush, so the
// command drains the pending backlog regardless of the holiday rule.
func (s *Server) SetFeishuBot(b *feishu.Bot) {
	s.feishuBot = b
	if b != nil {
		b.SetForcePushFunc(func() (int, error) {
			return s.RunPush(true)
		})
	}
}

func New(cfg *config.Config) *Server {
	lb := newLogBuffer()
	initLogCapture(lb)
	s := &Server{
		cfg:            cfg,
		api:            api.NewClient(cfg),
		scoringAPI:     buildScoringClient(cfg),
		translationAPI: buildTranslationClient(cfg),
		mux:            http.NewServeMux(),
		logBuf:         lb,
	}
	s.registerRoutes()
	s.startScheduler()
	return s
}

// lockPaper acquires per-paper write lock for load→modify→save sequences.
// Returns unlock func. Safe for concurrent access across goroutines.
func (s *Server) lockPaper(id string) func() {
	v, _ := s.paperLocks.LoadOrStore(id, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Server) registerRoutes() {
	mux := s.mux

	mux.HandleFunc("POST /api/papers", s.handleNewPaper)
	mux.HandleFunc("GET /api/papers", s.handleListPapers)
	mux.HandleFunc("GET /api/papers/{id}", s.handleGetPaper)
	mux.HandleFunc("DELETE /api/papers/{id}", s.handleDeletePaper)
	mux.HandleFunc("PATCH /api/papers/{id}/title", s.handleUpdateTitle)
	mux.HandleFunc("PATCH /api/papers/{id}/rating", s.handleUpdateRating)
	mux.HandleFunc("PATCH /api/papers/{id}/pin", s.handleTogglePin)
	mux.HandleFunc("POST /api/papers/{id}/chat", s.handleChat)
	mux.HandleFunc("DELETE /api/papers/{id}/rounds/{n}", s.handleDeleteRound)
	mux.HandleFunc("POST /api/papers/{id}/export", s.handleExport)
	mux.HandleFunc("POST /api/papers/{id}/summarize", s.handleSummarize)
	mux.HandleFunc("POST /api/papers/{id}/summarize-export", s.handleSummarizeExport)
	mux.HandleFunc("POST /api/papers/{id}/retry-summary", s.handleRetrySummary)
	mux.HandleFunc("POST /api/papers/{id}/chat/{round}/retry", s.handleRetryChat)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/config", s.handleUpdateConfig)
	mux.HandleFunc("GET /api/config/status", s.handleConfigStatus)
	mux.HandleFunc("GET /api/prompts", s.handleGetPrompts)
	mux.HandleFunc("POST /api/prompts", s.handleSavePrompts)
	mux.HandleFunc("GET /api/logs", s.handleGetLogs)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/feishu/status", s.handleFeishuStatus)
	mux.HandleFunc("GET /api/active-paper", s.handleGetActivePaper)
	mux.HandleFunc("PUT /api/active-paper", s.handleSetActivePaper)

	// Recommend system routes
	mux.HandleFunc("GET /api/recommend/config", s.handleRecommendGetConfig)
	mux.HandleFunc("PUT /api/recommend/config", s.handleRecommendUpdateConfig)
	mux.HandleFunc("POST /api/recommend/trigger", s.handleRecommendTrigger)
	mux.HandleFunc("POST /api/recommend/push-to-feishu", s.handleRecommendPushToFeishu)
	mux.HandleFunc("GET /api/recommend/scheduler-status", s.handleRecommendSchedulerStatus)
	mux.HandleFunc("GET /api/recommend/preferences", s.handleRecommendGetPreferences)
	mux.HandleFunc("PUT /api/recommend/preferences", s.handleRecommendSavePreferences)
	mux.HandleFunc("POST /api/recommend/fetch", s.handleRecommendFetch)
	mux.HandleFunc("POST /api/recommend/generate", s.handleRecommendGenerate)
	mux.HandleFunc("GET /api/recommend/articles", s.handleRecommendArticles)
	mux.HandleFunc("GET /api/recommend/today", s.handleRecommendToday)
	mux.HandleFunc("GET /api/recommend/dates", s.handleRecommendDates)
	mux.HandleFunc("GET /api/recommend/dates/{date}", s.handleRecommendArticlesByDate)
	mux.HandleFunc("PUT /api/recommend/articles/{id}/status", s.handleRecommendUpdateStatus)
	mux.HandleFunc("PUT /api/recommend/articles/status", s.handleRecommendBatchUpdateStatus)
	mux.HandleFunc("PUT /api/recommend/articles/{id}/comment", s.handleRecommendUpdateComment)
	mux.HandleFunc("POST /api/recommend/votes", s.handleRecommendFetchVotes)
	mux.HandleFunc("GET /api/recommend/stats", s.handleRecommendStats)

	s.registerStatic()
}

func (s *Server) startScheduler() {
	s.cfg.RLock()
	categories := s.cfg.ArxivCategories
	dailyPapers := s.cfg.Recommend.DailyPapers
	batchSize := s.cfg.Recommend.ScoringBatchSize
	scheduledTime := s.cfg.Recommend.ScheduledTime
	s.cfg.RUnlock()

	if len(categories) == 0 {
		log.Println("[server] no arXiv categories, scheduler not started")
		return
	}

	if scheduledTime == "" {
		scheduledTime = "08:00"
	}

	// One-time migration: import existing JSON papers to chat_papers
	if err := s.migrateChatPapers(); err != nil {
		log.Printf("[server] chat_papers migration: %v", err)
	}

	s.sched = scheduler.New(categories, s.scoringClient(), s.scoringModel(), dailyPapers, batchSize, s.cfg.Recommend.DiversityRatio, scheduledTime)

	// Connect scheduler completion to Feishu daily recommendation push.
	// The push logic itself lives in RunPush so the Feishu bot's /push
	// command can share the same code path.
	s.sched.SetOnComplete(func(articles []database.Article, force bool) {
		log.Printf("[scheduler] onComplete: %d articles, feishuBot=%v, force=%v", len(articles), s.feishuBot != nil, force)

		// 1. Translate and persist to DB (if translation API configured)
		s.translateAndPersistArticles(articles)

		// 2. Push (handles holiday-skip, pending backlog, etc.)
		if _, err := s.RunPush(force); err != nil {
			log.Printf("[scheduler] onComplete: RunPush error: %v", err)
		}
	})

	s.sched.Start()
}

// RunPush is the single entry point for pushing recommended articles to
// the configured Feishu chat. It is used by both the scheduler's onComplete
// and the Feishu bot's /push command.
//
// Flow:
//  1. If `force` is false, check the holiday calendar. On a holiday, return
//     early WITHOUT touching the database, so the pending backlog stays
//     intact for the next workday.
//  2. Query articles in batches (500 at a time) with pushed_at IS NULL AND
//     recommend_date IS NOT NULL, oldest first. The loop continues until
//     a batch comes back short, which handles extreme backlogs (>500
//     accumulated articles) by pushing in multiple waves.
//  3. If the chat is not configured or the bot is not running, log and skip.
//  4. Send the recommendation card and mark every pushed article with the
//     current timestamp. If the send fails, return the error WITHOUT
//     marking — the next push will retry the same batch.
//
// Returns the total number of articles that were pushed (0 if the day was
// a holiday, the backlog was empty, or push was skipped for any other reason).
func (s *Server) RunPush(force bool) (int, error) {
	today := time.Now().Format("2006-01-02")

	// Step 1: holiday fast-path (skip DB query entirely on a holiday).
	if !force {
		res := getHolidayChecker().IsHoliday(time.Now())
		if res.IsHoliday {
			log.Printf("[push] %s is holiday (source=%s), skipping push", today, res.Source)
			return 0, nil
		}
	}

	// Step 2: gate on chat configuration and bot availability. Cheap to check
	// up front so we don't even touch the DB when push is unconfigured.
	s.cfg.RLock()
	chatID := s.cfg.Feishu.DailyRecommendChatID
	pushEnabled := s.cfg.Recommend.PushToFeishu
	s.cfg.RUnlock()

	if chatID == "" || !pushEnabled || s.feishuBot == nil {
		log.Printf("[push] %s: skipping (chatID=%q, pushEnabled=%v, feishuBot=%v)",
			today, chatID, pushEnabled, s.feishuBot != nil)
		return 0, nil
	}

	// Step 3: drain the pending backlog in batches. 500 is the batch size,
	// matching the SQLite IN-clause limit in MarkArticlesPushed and the
	// soft ceiling for one Feishu card set.
	const batchSize = 500
	pushed := 0
	for {
		pending, err := database.GetUnpushedArticles(batchSize)
		if err != nil {
			return pushed, fmt.Errorf("get unpushed: %w", err)
		}
		if len(pending) == 0 {
			break
		}

		n, err := s.pushAndMark(chatID, pending, today, force)
		pushed += n
		if err != nil {
			// On error, do NOT mark; the same articles stay pending for retry.
			return pushed, err
		}
		if len(pending) < batchSize {
			break
		}
	}
	if pushed == 0 {
		log.Printf("[push] %s: no pending articles, nothing to push", today)
	}
	return pushed, nil
}

// pushAndMark sends a single batch and marks the articles as pushed on success.
// On send failure, returns the error WITHOUT marking so the next run retries.
// `force` is recorded in the log so operators can trace force-pushes
// (manual trigger / Feishu /push) vs scheduled pushes from the server logs.
func (s *Server) pushAndMark(chatID string, pending []database.Article, today string, force bool) (int, error) {
	log.Printf("[push] %s: pushing %d articles to chat %s (force=%v)", today, len(pending), chatID, force)
	if err := s.feishuBot.PushDailyRecommend(chatID, pending); err != nil {
		return 0, fmt.Errorf("feishu push: %w", err)
	}
	ids := make([]string, len(pending))
	for i, a := range pending {
		ids[i] = a.ID
	}
	ts := time.Now().Format("2006-01-02 15:04:05")
	if err := database.MarkArticlesPushed(ids, ts); err != nil {
		return len(pending), fmt.Errorf("mark pushed: %w", err)
	}
	return len(pending), nil
}

func (s *Server) migrateChatPapers() error {
	count, err := database.ChatPaperCount()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil // already migrated
	}

	imported, err := database.MigrateChatPapersFromJSON(config.PapersDir())
	if err != nil {
		return fmt.Errorf("migrate json to chat_papers: %w", err)
	}
	if imported > 0 {
		log.Printf("[server] imported %d existing Q&A papers to chat_papers", imported)
	}
	return nil
}

func (s *Server) registerStatic() {
	fSys, err := fs.Sub(frontendDist, "frontend-dist")
	if err != nil {
		log.Printf("Warning: no embedded frontend dist found, will serve 404 for static files")
		return
	}
	fileServer := http.FileServer(http.FS(fSys))
	s.mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Clean(r.URL.Path)
		if path == "/" || !strings.Contains(path, ".") {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	}))
}

func (s *Server) Handler() http.Handler {
	return withCORS(withLogging(s.mux))
}

func (s *Server) Start(addr string) error {
	log.Printf("PaperAgent server starting on http://%s\n", addr)
	return http.ListenAndServe(addr, s.Handler())
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip noisy endpoints: health/logs/scheduler-status polling and Vite-bundled static assets.
		path := r.URL.Path
		if path == "/api/health" || path == "/api/logs" || path == "/api/recommend/scheduler-status" ||
			path == "/favicon.ico" || strings.HasPrefix(path, "/assets/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(lw, r)
		duration := time.Since(start)
		log.Printf("[%s] %s %s -> %d (%s)", r.Method, r.URL.Path, r.RemoteAddr, lw.statusCode, duration.Round(time.Millisecond))
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lw *loggingResponseWriter) WriteHeader(code int) {
	lw.statusCode = code
	lw.ResponseWriter.WriteHeader(code)
}

func (lw *loggingResponseWriter) Write(b []byte) (int, error) {
	return lw.ResponseWriter.Write(b)
}

func (lw *loggingResponseWriter) Flush() {
	if f, ok := lw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && corsAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func corsAllowedOrigin(origin string) bool {
	return strings.Contains(origin, "localhost") || strings.Contains(origin, "127.0.0.1")
}
