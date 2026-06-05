package server

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/feishu"
)

//go:embed frontend-dist
var frontendDist embed.FS

type Server struct {
	cfg        *config.Config
	api        *api.Client
	mux        *http.ServeMux
	paperLocks sync.Map
	logBuf     *logBuffer
	feishuBot  *feishu.Bot
}

// SetFeishuBot sets the feishu bot reference for hot-reload support.
func (s *Server) SetFeishuBot(b *feishu.Bot) {
	s.feishuBot = b
}

func New(cfg *config.Config) *Server {
	lb := newLogBuffer()
	initLogCapture(lb)
	s := &Server{
		cfg:    cfg,
		api:    api.NewClient(cfg),
		mux:    http.NewServeMux(),
		logBuf: lb,
	}
	s.registerRoutes()
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
	mux.HandleFunc("GET /api/prompts", s.handleGetPrompts)
	mux.HandleFunc("POST /api/prompts", s.handleSavePrompts)
	mux.HandleFunc("GET /api/logs", s.handleGetLogs)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/feishu/status", s.handleFeishuStatus)
	mux.HandleFunc("GET /api/active-paper", s.handleGetActivePaper)
	mux.HandleFunc("PUT /api/active-paper", s.handleSetActivePaper)

	s.registerStatic()
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
		// Skip noisy polling endpoints
		if r.URL.Path == "/api/health" || r.URL.Path == "/api/logs" {
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
