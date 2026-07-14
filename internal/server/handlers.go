package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/chat"
	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/database"
	"github.com/happyTonakai/paperagent/internal/export"
	"github.com/happyTonakai/paperagent/internal/prompt"
	"github.com/happyTonakai/paperagent/internal/session"
	"github.com/happyTonakai/paperagent/internal/urlparse"
)

// --- Request types ---

type newPaperRequest struct {
	URL     string `json:"url"`
	Content string `json:"content"`
}

type chatRequest struct {
	Question    string `json:"question"`
	SkipContext bool   `json:"skip_context"`
}

// --- Response types ---

type paperResponse struct {
	ID                    string            `json:"id"`
	Title                 string            `json:"title"`
	SourceURL             string            `json:"source_url"`
	ArxivID               string            `json:"arxiv_id,omitempty"`
	GitHubURL             string            `json:"github_url,omitempty"`
	InitialSummary        string            `json:"initial_summary"`
	ModelUsed             string            `json:"model_used"`
	TotalTokens           int               `json:"total_tokens_used,omitempty"`
	TotalPromptTokens     int               `json:"total_prompt_tokens,omitempty"`
	TotalCompletionTokens int               `json:"total_completion_tokens,omitempty"`
	TotalCachedTokens     int               `json:"total_cached_tokens,omitempty"`
	Rating                int               `json:"rating"`
	Pinned                bool              `json:"pinned"`
	CreatedAt             string            `json:"created_at"`
	UpdatedAt             string            `json:"updated_at"`
	Messages              []messageResponse `json:"messages"`
}

type messageResponse struct {
	RoundNumber      int    `json:"round_number"`
	Role             string `json:"role"`
	Content          string `json:"content"`
	TokenCount       int    `json:"token_count"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	CachedTokens     int    `json:"cached_tokens,omitempty"`
	SkipContext      bool   `json:"skip_context,omitempty"`
}

type paperSummaryResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Rating    int    `json:"rating"`
	Pinned    bool   `json:"pinned"`
	UpdatedAt string `json:"updated_at"`
}

// --- Handlers ---

func (s *Server) handleNewPaper(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MB

	var req newPaperRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	log.Printf("[new-paper] fetching content for URL: %s", req.URL)

	content, sourceURL, arxivID, err := s.fetchPaperContent(r.Context(), req)
	if err != nil {
		log.Printf("[new-paper] fetch error: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	log.Printf("[new-paper] fetched %d chars, creating paper", len(content))

	// Extract references from the paper content before sending to LLM.
	body, references := session.ExtractReferences(content)
	log.Printf("[new-paper] extracted %d chars of references", len(references))

	// Check for existing paper with the same arXiv ID.
	if arxivID != "" {
		if existing, err := session.FindPaperByArxivID(arxivID); err == nil && existing != nil {
			log.Printf("[new-paper] paper with arxiv ID %s already exists: %s", arxivID, existing.Ref())
			// Set as active paper.
			if err := session.SetActivePaper(existing.Ref()); err != nil {
				log.Printf("[new-paper] set active paper error: %v", err)
			}
			title := existing.Title
			if title == "" {
				if existing.SessionID != "" && len(existing.SessionID) >= 8 {
					title = existing.SessionID[:8]
				} else {
					title = "Paper " + existing.Ref()
				}
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"existing": true,
				"id":       existing.Ref(),
				"title":    title,
			})
			return
		}
	}

	paper := session.NewPaper(body, sourceURL, arxivID)
	paper.ModelUsed = s.cfg.API.DefaultModel

	// Store references separately (not sent to LLM by default).
	paper.References = references
	log.Printf("[new-paper] storing body=%d chars, references=%d chars", len(body), len(references))

	// Try HTML title extraction for arXiv papers (instant, no LLM call)
	if arxivID != "" {
		if title, err := urlparse.FetchArxivTitleCtx(r.Context(), arxivID); err == nil && title != "" {
			paper.SetTitle(title)
			log.Printf("[new-paper] title from HTML: %s", title)
		} else {
			log.Printf("[new-paper] HTML title extraction failed for %s: %v", arxivID, err)
		}

		// Fetch the arXiv abstract page to extract a GitHub repo URL. Done
		// best-effort (same as FetchArxivTitle): failure does not block paper
		// creation. The abstract is also cached in chat_paper_abstracts so the
		// preference-update pipeline can read it (this fills a pre-existing
		// gap where the cache was never populated for Q&A papers).
		if abstract, err := urlparse.FetchArxivAbstractCtx(r.Context(), arxivID); err == nil && abstract != "" {
			if gh := urlparse.ExtractGitHubURL(abstract); gh != "" {
				paper.GitHubURL = gh
				log.Printf("[new-paper] github url from abstract: %s", gh)
			}
			if err := database.UpsertChatPaperAbstract(arxivID, abstract); err != nil {
				log.Printf("[new-paper] cache abstract: %v", err)
			}
		} else {
			log.Printf("[new-paper] abstract extraction failed for %s: %v", arxivID, err)
		}
	}

	// Add initial user message FIRST so the paper has content before any save.
	// Content is stored WITHOUT references; they are saved separately in paper.References.
	paper.AddMessage(session.Message{
		RoundNumber: 0,
		Role:        "user",
		Content:     body,
		TokenCount:  session.EstimateTokens(body),
	})

	if err := paper.Save(); err != nil {
		log.Printf("[new-paper] save error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
		return
	}

	// Auto-set as active paper
	if err := session.SetActivePaper(paper.Ref()); err != nil {
		log.Printf("[new-paper] set active paper error: %v", err)
	}

	log.Printf("[new-paper] paper created: %s", paper.Ref())

	// Start SSE stream
	sw, err := newSSEWriter(w)
	if err != nil {
		log.Printf("[new-paper] SSE not supported: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	if err := sw.WriteCreated(paper.Ref()); err != nil {
		log.Printf("[new-paper] failed to send created event: %v", err)
		return
	}

	// Send title immediately if HTML extraction succeeded
	if paper.Title != "" {
		sw.WriteTitle(paper.Title)
	}

	log.Printf("[new-paper] starting summary stream for %s", paper.Ref())

	// Use paper.Content (already stripped of references) for the LLM,
	// and add the get_references tool so LLM can request references on demand.
	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetSystem()},
		{Role: "user", Content: paper.Content},
		{Role: "user", Content: prompt.GetHeavy()},
	}
	// Tools must match chat.BuildChatTools(paper) byte-for-byte and in the same
	// order. Chat-completions serializes tools BEFORE messages into the cache
	// key, so any divergence here (extra / missing / reordered tool) at byte 0
	// invalidates the entire prefix lookup and the first Q&A round cannot
	// reuse the cached [system, paper.Content] prefix from this initial summary.
	tools, _ := chat.BuildChatTools(paper)

	ch := s.api.ChatStream(s.cfg.API.DefaultModel, messages, tools)
	var summaryBuilder strings.Builder
	var promptTokens, completionTokens, cachedTokens int
	var toolCalls []api.ToolCallCompleted

	// First pass: read chunks, detecting tool calls.
	for chunk := range ch {
		select {
		case <-r.Context().Done():
			log.Printf("[new-paper] client disconnected")
			return
		default:
		}

		if chunk.Err != nil {
			log.Printf("[new-paper] stream error: %v", chunk.Err)
			sw.WriteError(chunk.Err.Error())
			return
		}
		if chunk.ToolCalls != nil {
			toolCalls = chunk.ToolCalls
			break
		}
		if chunk.Done {
			promptTokens = chunk.PromptTokens
			completionTokens = chunk.CompletionTokens
			cachedTokens = chunk.CachedTokens
			break
		}
		summaryBuilder.WriteString(chunk.Content)
		if err := sw.WriteChunk(chunk.Content); err != nil {
			log.Printf("[new-paper] write chunk error: %v", err)
			return
		}
	}

	// If LLM called get_references, inject references and do a follow-up stream.
	// (Unlikely during summary generation, but handle it for completeness.)
	if len(toolCalls) > 0 && references != "" {
		log.Printf("[new-paper] tool call detected: %s, injecting references", toolCalls[0].Function.Name)
		followUpMessages := make([]api.ChatMessage, len(messages))
		copy(followUpMessages, messages)
		followUpMessages = append(followUpMessages,
			api.ChatMessage{
				Role:      "assistant",
				ToolCalls: toolCalls,
			},
			api.ChatMessage{
				Role:       "tool",
				ToolCallID: toolCalls[0].ID,
				Content:    references,
			},
		)

		ch2 := s.api.ChatStream(s.cfg.API.DefaultModel, followUpMessages, nil)
		for chunk := range ch2 {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			if chunk.Err != nil {
				sw.WriteError(chunk.Err.Error())
				return
			}
			if chunk.Done {
				promptTokens = chunk.PromptTokens
				completionTokens = chunk.CompletionTokens
				cachedTokens = chunk.CachedTokens
				break
			}
			summaryBuilder.WriteString(chunk.Content)
			if err := sw.WriteChunk(chunk.Content); err != nil {
				return
			}
		}
	}

	summary := summaryBuilder.String()
	log.Printf("[new-paper] summary complete for %s: %d chars", paper.Ref(), len(summary))

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

	sw.WriteDoneWithTokens(paper.Ref(), promptTokens, completionTokens, cachedTokens)
}

func (s *Server) handleGetPaper(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	paper, err := session.LoadPaperByRef(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}
	writeJSON(w, http.StatusOK, paperToResponse(paper))
}

func (s *Server) handleListPapers(w http.ResponseWriter, r *http.Request) {
	papers, err := session.ListPapers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}

	response := make([]paperSummaryResponse, 0, len(papers))
	for _, p := range papers {
		response = append(response, paperSummaryResponse{
			ID:        p.Ref(),
			Title:     p.Title,
			Rating:    p.Rating,
			Pinned:    p.Pinned,
			UpdatedAt: p.UpdatedAt.Format("2006-01-02 15:04"),
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleDeletePaper(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := session.DeletePaperByRef(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleUpdateTitle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	r.Body = http.MaxBytesReader(w, r.Body, 1<<12) // 4KB
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title required"})
		return
	}

	unlock := s.lockPaper(id)
	defer unlock()
	paper, err := session.LoadPaperByRef(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}
	paper.SetTitle(req.Title)
	paper.Save()
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "title": req.Title})
}

func (s *Server) handleUpdateRating(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	r.Body = http.MaxBytesReader(w, r.Body, 1<<12) // 4KB
	var req struct {
		Rating int `json:"rating"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Rating < 1 || req.Rating > 10 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rating must be 1-10"})
		return
	}

	unlock := s.lockPaper(id)
	defer unlock()
	paper, err := session.LoadPaperByRef(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}
	paper.Rating = req.Rating
	paper.Save()
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "rating": fmt.Sprintf("%d", req.Rating)})
}

func (s *Server) handleTogglePin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	unlock := s.lockPaper(id)
	defer unlock()
	paper, err := session.LoadPaperByRef(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}

	paper.Pinned = !paper.Pinned
	paper.Save()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "updated",
		"pinned": paper.Pinned,
	})
}

// handleChat handles a chat request for a paper.
//
// The shared Q&A pipeline lives in internal/chat.Engine; this handler is
// responsible only for:
//   - acquiring the per-paper lock and loading the paper (with auto-recovery
//     from source URL if the cached body is missing);
//   - opening the SSE stream (headers must be flushed before the lock is
//     released);
//   - translating engine events into SSE events via sseSink.
//
// Lock semantics: the per-paper lock is held for the entire SSE stream.
// The previous implementation released it during streaming to allow
// concurrent rating/pin/title updates on the same paper; that was
// possible because the old code re-loaded the paper from disk after
// streaming. The new engine works off an in-memory paper pointer, so
// releasing and re-acquiring the lock would require a reload inside the
// engine. Holding the lock is simpler and avoids races; the trade-off is
// that rating/pin/title updates on the same paper block for the duration
// of a chat (typically a few seconds).
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	log.Printf("[chat] loading paper %s", id)

	unlock := s.lockPaper(id)
	paper, err := session.LoadPaperByRef(id)
	if err != nil {
		unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}

	// Auto-recover lost content from source URL if possible.
	if paper.Content == "" && paper.SourceURL != "" {
		log.Printf("[chat] content empty for %s, re-fetching from %s", id, paper.SourceURL)
		sourceURL := paper.SourceURL
		unlock()

		content, _, _, err := s.fetchPaperContent(r.Context(), newPaperRequest{URL: sourceURL})
		if err != nil {
			log.Printf("[chat] re-fetch failed: %v", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("paper content lost and re-fetch from source URL failed: %v", err)})
			return
		}

		unlock = s.lockPaper(id)
		paper, err = session.LoadPaperByRef(id)
		if err != nil {
			unlock()
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
			return
		}

		// Store recovered content without references.
		bodyContent, refs := session.ExtractReferences(content)
		for i, m := range paper.Messages {
			if m.RoundNumber == 0 && m.Role == "user" {
				paper.Messages[i].Content = bodyContent
				paper.Messages[i].TokenCount = session.EstimateTokens(bodyContent)
				break
			}
		}
		paper.Content = bodyContent
		paper.References = refs
		paper.Save()
		log.Printf("[chat] recovered %d chars from source URL, extracted %d ref chars", len(bodyContent), len(refs))
	} else if paper.Content == "" {
		unlock()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "paper content is empty and no source URL to recover from"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB

	body, err := io.ReadAll(r.Body)
	if err != nil {
		unlock()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot read request body"})
		return
	}

	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Question == "" {
		unlock()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question required"})
		return
	}

	q := req.Question
	if utf8.RuneCountInString(q) > 20 {
		q = strings.ToValidUTF8(string([]rune(q)[:20]), "") + "…"
	}
	log.Printf("[chat] question: %s", q)

	// Open the SSE stream. Headers must be flushed before any long work,
	// so we do this under the lock — the lock is held for the whole stream
	// (see fn-level comment).
	sw, err := newSSEWriter(w)
	if err != nil {
		unlock()
		log.Printf("[chat] SSE not supported: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	tools, handlers := chat.BuildChatTools(paper)
	if err := s.chatEngine.Answer(r.Context(), paper, req.Question, req.SkipContext, tools, handlers, &sseSink{sw: sw}); err != nil {
		log.Printf("[chat] engine error: %v", err)
		sw.WriteError(err.Error())
	}
	unlock()
}

func (s *Server) handleDeleteRound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	nStr := r.PathValue("n")

	unlock := s.lockPaper(id)
	paper, err := session.LoadPaperByRef(id)
	if err != nil {
		unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}

	n, err := strconv.Atoi(nStr)
	if err != nil {
		unlock()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid round number"})
		return
	}

	paper.DeleteRound(n)
	paper.Save()
	unlock()

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	paper, err := session.LoadPaperByRef(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}

	exportPath, err := export.ExportToObsidian(s.cfg, paper)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "export failed: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "exported",
		"path":   exportPath,
	})
}

// handleRetrySummary regenerates or continues the initial summary via SSE.
// If initial_summary is empty, starts fresh.
// If initial_summary has content, sends "continue from here" prompt.
// References are stripped from the paper content sent to the LLM; available via get_references tool.
func (s *Server) handleRetrySummary(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	unlock := s.lockPaper(id)
	paper, err := session.LoadPaperByRef(id)
	if err != nil {
		unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}

	// If content was lost (e.g. old-format paper corrupted by SavePaper),
	// try to recover from source URL.
	if paper.Content == "" {
		if paper.SourceURL == "" {
			unlock()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "paper content is empty and no source URL to recover from"})
			return
		}

		log.Printf("[retry-summary] content empty for %s, re-fetching from %s", id, paper.SourceURL)
		sourceURL := paper.SourceURL
		unlock()

		content, _, _, err := s.fetchPaperContent(r.Context(), newPaperRequest{URL: sourceURL})
		if err != nil {
			log.Printf("[retry-summary] re-fetch failed: %v", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("re-fetch from source URL failed: %v", err)})
			return
		}

		// Lock again and update paper with recovered content
		unlock = s.lockPaper(id)
		paper, err = session.LoadPaperByRef(id)
		if err != nil {
			unlock()
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
			return
		}

		// Re-extract references from recovered content.
		bodyContent, references := session.ExtractReferences(content)

		// Update the round-0 user message with recovered content (stripped of refs).
		for i, m := range paper.Messages {
			if m.RoundNumber == 0 && m.Role == "user" {
				paper.Messages[i].Content = bodyContent
				paper.Messages[i].TokenCount = session.EstimateTokens(bodyContent)
				break
			}
		}
		paper.Content = bodyContent
		paper.References = references
		paper.Save()
		log.Printf("[retry-summary] recovered %d chars from source URL, refs %d chars", len(bodyContent), len(references))
	}

	existingSummary := paper.InitialSummary

	// Use paper.Content directly.
	// New papers have references stripped at creation; old papers (References为空)
	// have full content with references intact.
	body := paper.Content

	// Use the same tool set as the live chat path so a summary retry can
	// still invoke fetch_arxiv / get_references. Captured under the lock so
	// BuildChatTools' read of paper.References is race-free (matches
	// handleRetryChat). The handlers close over a string copy for refs and
	// a stateless factory for fetch_arxiv, so they stay safe after unlock.
	tools, handlers := chat.BuildChatTools(paper)

	unlock()

	// Build messages
	msgs := []api.ChatMessage{
		{Role: "system", Content: prompt.GetSystem()},
		{Role: "user", Content: body},
		{Role: "user", Content: prompt.GetHeavy()},
	}

	if existingSummary != "" {
		msgs = append(msgs,
			api.ChatMessage{Role: "assistant", Content: existingSummary},
			api.ChatMessage{Role: "user", Content: "Continue writing the summary from where you left off. Do not repeat what has already been written. Simply continue."},
		)
		log.Printf("[retry-summary] continuing from existing %d chars", len(existingSummary))
	} else {
		log.Printf("[retry-summary] fresh summary generation")
	}

	sw, err := newSSEWriter(w)
	if err != nil {
		log.Printf("[retry-summary] SSE not supported: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	ch := s.api.ChatStream(s.cfg.API.DefaultModel, msgs, tools)
	var newContent strings.Builder
	var promptTokens, completionTokens, cachedTokens int
	var toolCalls []api.ToolCallCompleted

	for chunk := range ch {
		select {
		case <-r.Context().Done():
			log.Printf("[retry-summary] client disconnected")
			return
		default:
		}

		if chunk.Err != nil {
			log.Printf("[retry-summary] stream error: %v", chunk.Err)
			sw.WriteError(chunk.Err.Error())
			return
		}
		if chunk.ToolCalls != nil {
			toolCalls = chunk.ToolCalls
			break
		}
		if chunk.Done {
			promptTokens = chunk.PromptTokens
			completionTokens = chunk.CompletionTokens
			cachedTokens = chunk.CachedTokens
			break
		}
		newContent.WriteString(chunk.Content)
		if err := sw.WriteChunk(chunk.Content); err != nil {
			return
		}
	}

	// Handle tool call (fetch_arxiv, get_references, ...). Mirrors the
	// dispatch in chat.Engine.stream via chat.ResolveToolCall, so the retry
	// path resolves tools identically to the live-chat path.
	if len(toolCalls) > 0 {
		toolName := toolCalls[0].Function.Name
		log.Printf("[retry-summary] tool call detected: %s", toolName)
		if err := sw.WriteToolCall(toolName); err != nil {
			log.Printf("[retry-summary] write tool_call event: %v", err)
		}
		toolResult := chat.ResolveToolCall(r.Context(), handlers, toolCalls)

		followUpMsgs := make([]api.ChatMessage, len(msgs))
		copy(followUpMsgs, msgs)
		followUpMsgs = append(followUpMsgs,
			api.ChatMessage{Role: "assistant", ToolCalls: toolCalls},
			api.ChatMessage{Role: "tool", ToolCallID: toolCalls[0].ID, Content: toolResult},
		)

		ch2 := s.api.ChatStream(s.cfg.API.DefaultModel, followUpMsgs, nil)
		for chunk := range ch2 {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			if chunk.Err != nil {
				sw.WriteError(chunk.Err.Error())
				return
			}
			if chunk.Done {
				promptTokens = chunk.PromptTokens
				completionTokens = chunk.CompletionTokens
				cachedTokens = chunk.CachedTokens
				break
			}
			newContent.WriteString(chunk.Content)
			if err := sw.WriteChunk(chunk.Content); err != nil {
				return
			}
		}
	}

	unlock = s.lockPaper(id)
	defer unlock()

	// Re-load to get latest state
	paper, err = session.LoadPaperByRef(id)
	if err != nil {
		log.Printf("[retry-summary] reload failed after stream: %v", err)
		return
	}

	final := existingSummary + newContent.String()
	paper.SetInitialSummary(final)

	// Remove any existing round-0 assistant messages
	var filtered []session.Message
	for _, m := range paper.Messages {
		if m.RoundNumber != 0 || m.Role != "assistant" {
			filtered = append(filtered, m)
		}
	}

	// Insert the new round-0 assistant at position 1 (after round-0 user, before Q&A rounds)
	newMsg := session.Message{
		RoundNumber:      0,
		Role:             "assistant",
		Content:          final,
		TokenCount:       session.EstimateTokens(final),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CachedTokens:     cachedTokens,
		SkipContext:      true,
		CreatedAt:        time.Now(),
	}

	// Find index of first non-round-0 message to insert after round-0 user
	insertAt := len(filtered) // default: append
	for i, m := range filtered {
		if m.RoundNumber > 0 || (m.RoundNumber == 0 && m.Role != "user") {
			insertAt = i
			break
		}
	}
	paper.Messages = append(filtered[:insertAt], append([]session.Message{newMsg}, filtered[insertAt:]...)...)
	paper.UpdatedAt = time.Now()
	paper.Save()

	sw.WriteDoneWithTokens(paper.Ref(), promptTokens, completionTokens, cachedTokens)
}

// handleRetryChat regenerates the assistant answer for a specific round.
// References are stripped from paper content; available via get_references tool.
func (s *Server) handleRetryChat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	nStr := r.PathValue("round")

	unlock := s.lockPaper(id)
	paper, err := session.LoadPaperByRef(id)
	if err != nil {
		unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}

	round, err := strconv.Atoi(nStr)
	if err != nil {
		unlock()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid round number"})
		return
	}

	// Find user message for this round to get the question
	var question string
	for _, m := range paper.Messages {
		if m.RoundNumber == round && m.Role == "user" {
			question = m.Content
			break
		}
	}
	if question == "" {
		unlock()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "round not found"})
		return
	}

	// Use paper.Content directly.
	// New papers have references stripped at creation; old papers (References为空)
	// have full content with references intact.
	bodyForLLM := paper.Content

	// Collect context messages from rounds BEFORE target round (skip btw messages).
	// Use TruncationAnchor set by the original chat to filter context.
	var prevMsgs []session.Message
	for _, m := range paper.Messages {
		if m.SkipContext {
			continue
		}
		if m.RoundNumber < round {
			if paper.TruncationAnchor > 0 && m.RoundNumber < paper.TruncationAnchor {
				continue
			}
			prevMsgs = append(prevMsgs, m)
		}
	}
	if minR, maxR := session.ContextRoundRange(prevMsgs); maxR > 0 {
		log.Printf("[retry-chat] context includes rounds %d-%d (total %d msgs, anchor=%d, target_round=%d)", minR, maxR, len(prevMsgs), paper.TruncationAnchor, round)
	} else {
		log.Printf("[retry-chat] context is empty (anchor=%d, target_round=%d)", paper.TruncationAnchor, round)
	}

	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetSystem()},
		{Role: "user", Content: bodyForLLM},
		{Role: "user", Content: prompt.GetLight()},
	}
	for _, m := range prevMsgs {
		messages = append(messages, chat.ToAPIMessage(m))
	}
	// Add the current question at the end (not part of truncated context)
	messages = append(messages, api.ChatMessage{Role: "user", Content: question})

	// Use the same tool set as the live chat path (handleChat) so a retry
	// can still invoke fetch_arxiv / get_references. The handlers close
	// over paper state captured at this point (a string copy for refs, a
	// stateless factory for fetch_arxiv), so they stay safe to call after
	// the lock is released below.
	tools, handlers := chat.BuildChatTools(paper)

	unlock() // Release lock before SSE stream

	sw, err := newSSEWriter(w)
	if err != nil {
		log.Printf("[retry-chat] SSE not supported: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	ch := s.api.ChatStream(s.cfg.API.DefaultModel, messages, tools)
	var answer strings.Builder
	var promptTokens, completionTokens, cachedTokens int
	var toolCalls []api.ToolCallCompleted

	for chunk := range ch {
		select {
		case <-r.Context().Done():
			log.Printf("[retry-chat] client disconnected")
			return
		default:
		}

		if chunk.Err != nil {
			log.Printf("[retry-chat] stream error: %v", chunk.Err)
			sw.WriteError(chunk.Err.Error())
			return
		}
		if chunk.ToolCalls != nil {
			toolCalls = chunk.ToolCalls
			break
		}
		if chunk.Done {
			promptTokens = chunk.PromptTokens
			completionTokens = chunk.CompletionTokens
			cachedTokens = chunk.CachedTokens
			break
		}
		answer.WriteString(chunk.Content)
		if err := sw.WriteChunk(chunk.Content); err != nil {
			return
		}
	}

	// Handle tool call (fetch_arxiv, get_references, ...). Mirrors the
	// dispatch in chat.Engine.stream via chat.ResolveToolCall, so the retry
	// path resolves tools identically to the live-chat path.
	if len(toolCalls) > 0 {
		toolName := toolCalls[0].Function.Name
		log.Printf("[retry-chat] tool call detected: %s", toolName)
		if err := sw.WriteToolCall(toolName); err != nil {
			log.Printf("[retry-chat] write tool_call event: %v", err)
		}
		toolResult := chat.ResolveToolCall(r.Context(), handlers, toolCalls)

		followUpMsgs := make([]api.ChatMessage, len(messages))
		copy(followUpMsgs, messages)
		followUpMsgs = append(followUpMsgs,
			api.ChatMessage{Role: "assistant", ToolCalls: toolCalls},
			api.ChatMessage{Role: "tool", ToolCallID: toolCalls[0].ID, Content: toolResult},
		)

		ch2 := s.api.ChatStream(s.cfg.API.DefaultModel, followUpMsgs, nil)
		for chunk := range ch2 {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			if chunk.Err != nil {
				sw.WriteError(chunk.Err.Error())
				return
			}
			if chunk.Done {
				promptTokens = chunk.PromptTokens
				completionTokens = chunk.CompletionTokens
				cachedTokens = chunk.CachedTokens
				break
			}
			answer.WriteString(chunk.Content)
			if err := sw.WriteChunk(chunk.Content); err != nil {
				return
			}
		}
	}

	result := answer.String()
	log.Printf("[retry-chat] answer complete: %d chars", len(result))

	unlock = s.lockPaper(id)
	defer unlock()

	paper, err = session.LoadPaperByRef(id)
	if err != nil {
		log.Printf("[retry-chat] reload failed: %v", err)
		return
	}

	// Filter out old assistant message for this round (reloaded from disk)
	var filtered []session.Message
	for _, m := range paper.Messages {
		if m.RoundNumber != round || m.Role != "assistant" {
			filtered = append(filtered, m)
		}
	}
	paper.Messages = filtered

	paper.AddMessage(session.Message{
		RoundNumber:      round,
		Role:             "assistant",
		Content:          result,
		TokenCount:       session.EstimateTokens(result),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CachedTokens:     cachedTokens,
	})
	paper.Save()

	sw.WriteDoneWithTokens(paper.Ref(), promptTokens, completionTokens, cachedTokens)
}

// handleConfigStatus returns a lightweight status payload used by the Web UI
// to detect a first-run state (config.yaml missing on disk) and auto-open
// the settings dialog instead of showing a blank UI.
func (s *Server) handleConfigStatus(w http.ResponseWriter, r *http.Request) {
	s.cfg.RLock()
	apiKey := s.cfg.API.APIKey
	apiKeyConfigured := apiKey != "" && !strings.HasPrefix(apiKey, "${")
	s.cfg.RUnlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"config_exists":      config.ConfigExists(),
		"api_key_configured": apiKeyConfigured,
	})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.cfg.RLock()
	apiKey := s.cfg.API.APIKey
	maskedKey := "••••••••"
	hasCustomKey := false
	if apiKey != "" && !strings.HasPrefix(apiKey, "${") {
		head, tail := 4, len(apiKey)-4
		if len(apiKey) < 8 {
			head, tail = 2, 2
		}
		if tail < 0 {
			tail = len(apiKey)
		}
		maskedKey = apiKey[:head] + "••••" + apiKey[tail:]
		hasCustomKey = true
	}
	cfg := map[string]interface{}{
		"api": map[string]interface{}{
			"base_url":       s.cfg.API.BaseURL,
			"api_key":        maskedKey,
			"api_key_source": "env",
			"default_model":  s.cfg.API.DefaultModel,
		},
		"obsidian": map[string]string{
			"export_path": s.cfg.Obsidian.ExportPath,
		},
		"ui": map[string]int{
			"min_recent_rounds": s.cfg.UI.MinRecentRounds,
			"max_input_tokens":  s.cfg.UI.MaxInputTokens,
		},
		"feishu": map[string]interface{}{
			"enabled":                 s.cfg.Feishu.Enabled,
			"app_id":                  maskFeishu(s.cfg.Feishu.AppID),
			"app_secret":              maskFeishu(s.cfg.Feishu.AppSecret),
			"daily_recommend_chat_id": s.cfg.Feishu.DailyRecommendChatID,
		},
	}
	if hasCustomKey {
		cfg["api"].(map[string]interface{})["api_key_source"] = "config"
	}
	s.cfg.RUnlock()
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB

	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	// Snapshot the live cfg under RLock, apply the requested updates to
	// the snapshot, validate it, and only commit if validation passes.
	// This guarantees we never leave the in-memory cfg in an invalid
	// state when the request is rejected — the next Load() restores the
	// last good on-disk form regardless.
	s.cfg.RLock()
	pending := s.cfg.Snapshot()
	s.cfg.RUnlock()

	if v, ok := updates["api_key"].(string); ok && v != "" && !strings.Contains(v, "••••") {
		pending.API.APIKey = resolveAPIKeyInput(v)
	}
	if v, ok := updates["base_url"].(string); ok && v != "" {
		pending.API.BaseURL = v
	}
	if v, ok := updates["default_model"].(string); ok && v != "" {
		pending.API.DefaultModel = v
	}
	if v, ok := updates["min_recent_rounds"].(float64); ok {
		pending.UI.MinRecentRounds = int(v)
	}
	if v, ok := updates["max_input_tokens"].(float64); ok {
		pending.UI.MaxInputTokens = int(v)
	}
	if v, ok := updates["obsidian_export_path"].(string); ok {
		pending.Obsidian.ExportPath = v
	}
	if v, ok := updates["feishu_enabled"].(bool); ok {
		pending.Feishu.Enabled = v
	}
	if v, ok := updates["feishu_app_id"].(string); ok {
		// Same guard as api_key: ignore empty + masked values so a
		// stray POST can't clear the stored app_id.
		if v != "" && !strings.Contains(v, "••••") {
			pending.Feishu.AppID = v
		}
	}
	if v, ok := updates["feishu_app_secret"].(string); ok {
		if v != "" && !strings.Contains(v, "••••") {
			pending.Feishu.AppSecret = v
		}
	}
	if v, ok := updates["feishu_daily_recommend_chat_id"].(string); ok {
		pending.Feishu.DailyRecommendChatID = v
	}

	if err := pending.Validate(); err != nil {
		log.Printf("[config] REJECTED POST /api/config from %s: %v (body keys: %v)", r.RemoteAddr, err, mapKeys(updates))
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Cross-field check: catches e.g. POST /api/config turning feishu
	// off while recommend.push_to_feishu is still true. Without this,
	// the user would silently end up with a push_to_feishu=true that
	// does nothing at runtime, and the next save from the recommend
	// tab would mysteriously fail.
	if err := pending.HandleCrossFieldChecks(); err != nil {
		log.Printf("[config] REJECTED POST /api/config from %s: %v (body keys: %v)", r.RemoteAddr, err, mapKeys(updates))
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	s.cfg.Lock()
	s.cfg.CommitFrom(pending)
	s.cfg.Unlock()

	if err := s.cfg.Save(); err != nil {
		// Save's own Validate would have rejected illegal configs above;
		// reaching here means a filesystem failure. Roll back the
		// in-memory state so the next request doesn't see partially-
		// applied changes.
		log.Printf("[config] save failed, reloading from disk: %v", err)
		if fresh, lerr := config.Load(); lerr == nil {
			s.cfg.Lock()
			s.cfg.CommitFrom(fresh)
			s.cfg.Unlock()
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save config failed: " + err.Error()})
		return
	}

	// Reload feishu bot if feishu settings changed
	if s.feishuBot != nil {
		_, feishuChanged := updates["feishu_enabled"]
		_, appIDChanged := updates["feishu_app_id"]
		_, appSecretChanged := updates["feishu_app_secret"]
		if feishuChanged || appIDChanged || appSecretChanged {
			log.Printf("[server] feishu config changed, reloading bot...")
			if err := s.feishuBot.Reload(); err != nil {
				log.Printf("[server] feishu reload failed: %v", err)
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleGetPrompts(w http.ResponseWriter, r *http.Request) {
	type promptInfo struct {
		Name    string `json:"name"`
		Content string `json:"content"`
		Source  string `json:"source"` // "builtin" or "custom"
	}
	var result []promptInfo
	for _, name := range prompt.BuiltinNames() {
		effective := prompt.GetContent(name)
		source := "custom"
		// Check if user has a custom override
		userPath := config.ConfigDir() + "/prompts/" + name + ".txt"
		if _, err := os.Stat(userPath); os.IsNotExist(err) {
			source = "builtin"
		}
		result = append(result, promptInfo{
			Name:    name,
			Content: effective,
			Source:  source,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleSavePrompts(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	type promptSave struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	var updates []promptSave
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	for _, u := range updates {
		if err := prompt.Save(u.Name, u.Content); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("save %s failed: %v", u.Name, err)})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// handleSummarize generates a meta-summary of the entire conversation via SSE.
func (s *Server) handleSummarize(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	paper, err := session.LoadPaperByRef(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}

	// Build context.
	// Skip round 0: Q is the full paper text and A is the initial summary,
	// the latter is already provided via 初始总结.
	var context strings.Builder
	if paper.InitialSummary != "" {
		context.WriteString("## 初始总结\n\n")
		context.WriteString(paper.InitialSummary)
		context.WriteString("\n\n")
	}
	context.WriteString("## 对话历史\n\n")
	for _, msg := range paper.Messages {
		if msg.RoundNumber <= 0 {
			continue
		}
		if msg.Role == "user" {
			fmt.Fprintf(&context, "Q: %s\n", msg.Content)
		} else {
			fmt.Fprintf(&context, "A: %s\n", msg.Content)
		}
	}

	sw, err := newSSEWriter(w)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetSummarize()},
		{Role: "user", Content: context.String()},
	}

	ch := s.api.ChatStream(s.cfg.API.DefaultModel, messages, nil)
	var promptTokens, completionTokens, cachedTokens int
	for chunk := range ch {
		select {
		case <-r.Context().Done():
			return
		default:
		}

		if chunk.Err != nil {
			sw.WriteError(chunk.Err.Error())
			return
		}
		if chunk.Done {
			promptTokens = chunk.PromptTokens
			completionTokens = chunk.CompletionTokens
			cachedTokens = chunk.CachedTokens
			break
		}
		if err := sw.WriteChunk(chunk.Content); err != nil {
			return
		}
	}

	sw.WriteDoneWithTokens(paper.Ref(), promptTokens, completionTokens, cachedTokens)
}

// handleSummarizeExport summarizes the conversation and exports the result to Obsidian.
func (s *Server) handleSummarizeExport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	paper, err := session.LoadPaperByRef(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}

	// Build context for summarize.
	// Skip round 0: Q is the full paper text and A is the initial summary,
	// the latter is already provided via 初始总结.
	var context strings.Builder
	if paper.InitialSummary != "" {
		context.WriteString("## 初始总结\n\n")
		context.WriteString(paper.InitialSummary)
		context.WriteString("\n\n")
	}
	context.WriteString("## 对话历史\n\n")
	for _, msg := range paper.Messages {
		if msg.RoundNumber <= 0 {
			continue
		}
		if msg.Role == "user" {
			fmt.Fprintf(&context, "Q: %s\n", msg.Content)
		} else {
			fmt.Fprintf(&context, "A: %s\n", msg.Content)
		}
	}

	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetSummarize()},
		{Role: "user", Content: context.String()},
	}

	result, _, _, _, _, _, err := s.api.Chat(s.cfg.API.DefaultModel, messages, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "summarize failed: " + err.Error()})
		return
	}

	// Create a paper-like object with the summary for export
	exportPaper := &session.Paper{
		SessionID:      paper.SessionID,
		Title:          paper.Title,
		SourceURL:      paper.SourceURL,
		InitialSummary: result,
		ModelUsed:      paper.ModelUsed,
	}

	exportPath, err := export.ExportToObsidian(s.cfg, exportPaper)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "export failed: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "exported",
		"path":   exportPath,
	})
}

// --- Helpers ---

// mapKeys returns a sorted slice of a request body's top-level keys,
// used in reject-log messages so operators can see which fields a
// caller tried to set without dumping the full body (which may
// contain secrets).
func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 500 {
		limit = l
	}
	entries := s.logBuf.Recent(limit)
	if entries == nil {
		entries = []LogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"logs": entries})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleGetActivePaper returns the persisted active paper ID.
func (s *Server) handleGetActivePaper(w http.ResponseWriter, r *http.Request) {
	id := session.GetActivePaper()
	if id == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"id": nil})
		return
	}
	// Verify the paper still exists
	paper, err := session.LoadPaperByRef(id)
	if err != nil {
		// Paper was deleted, clear stale active paper
		session.ClearActivePaper()
		writeJSON(w, http.StatusOK, map[string]interface{}{"id": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": paper.Ref()})
}

// handleSetActivePaper persists the active paper ID.
func (s *Server) handleSetActivePaper(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<12) // 4KB
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.ID == "" {
		session.ClearActivePaper()
		writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
		return
	}
	// Verify the paper exists
	if _, err := session.LoadPaperByRef(req.ID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "paper not found"})
		return
	}
	if err := session.SetActivePaper(req.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleFeishuStatus(w http.ResponseWriter, r *http.Request) {
	if s.feishuBot == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"available": false,
		})
		return
	}
	enabled, connected, lastError := s.feishuBot.Status()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"available":  true,
		"enabled":    enabled,
		"connected":  connected,
		"last_error": lastError,
	})
}

func (s *Server) fetchPaperContent(ctx context.Context, req newPaperRequest) (content string, sourceURL string, arxivID string, err error) {
	if req.URL != "" {
		sourceURL = req.URL
		if arxivURL, id, ok := urlparse.NormalizeArxivInput(req.URL); ok {
			sourceURL = arxivURL
			arxivID = id
			content, err = urlparse.FetchURL(ctx, arxivURL)
		} else {
			content, err = urlparse.FetchURL(ctx, req.URL)
		}
		if err != nil {
			return "", "", "", fmt.Errorf("fetch URL failed: %w", err)
		}
		if content == "" {
			return "", "", "", fmt.Errorf("empty content from URL")
		}
	} else if req.Content != "" {
		content = req.Content
	} else {
		return "", "", "", fmt.Errorf("url or content required")
	}
	return content, sourceURL, arxivID, nil
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("json encode error: %v", err)
	}
}

func paperToResponse(p *session.Paper) paperResponse {
	msgs := make([]messageResponse, 0, len(p.Messages))
	for _, m := range p.Messages {
		// Skip tool-call assistant messages (empty content with tool calls)
		// and tool result messages — these are internal to the LLM context
		// and should not be rendered in the UI (e.g. a fetch_arxiv result
		// dumps the full fetched paper text, which the LLM needs but the
		// user should not see, just like the Q0 paper content is hidden).
		if m.Role == "tool" || (m.Role == "assistant" && m.Content == "" && len(m.ToolCalls) > 0) {
			continue
		}
		msgs = append(msgs, messageResponse{
			RoundNumber:      m.RoundNumber,
			Role:             m.Role,
			Content:          m.Content,
			TokenCount:       m.TokenCount,
			PromptTokens:     m.PromptTokens,
			CompletionTokens: m.CompletionTokens,
			CachedTokens:     m.CachedTokens,
			SkipContext:      m.SkipContext,
		})
	}

	return paperResponse{
		ID:                    p.Ref(),
		Title:                 p.Title,
		SourceURL:             p.SourceURL,
		ArxivID:               p.ArxivID,
		GitHubURL:             p.GitHubURL,
		InitialSummary:        p.InitialSummary,
		ModelUsed:             p.ModelUsed,
		TotalTokens:           p.TotalTokens,
		TotalPromptTokens:     p.TotalPromptTokens,
		TotalCompletionTokens: p.TotalCompletionTokens,
		TotalCachedTokens:     p.TotalCachedTokens,
		Rating:                p.Rating,
		Pinned:                p.Pinned,
		CreatedAt:             p.CreatedAt.Format("2006-01-02 15:04"),
		UpdatedAt:             p.UpdatedAt.Format("2006-01-02 15:04"),
		Messages:              msgs,
	}
}

// isEnvVarName reports whether s looks like an environment variable name
// (all uppercase letters and underscores, at least 2 chars).
func isEnvVarName(s string) bool {
	if len(s) < 2 {
		return false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && r != '_' {
			return false
		}
	}
	return true
}

// maskFeishu masks a feishu credential for safe display.
func maskFeishu(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}
