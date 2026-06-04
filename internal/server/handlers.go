package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/config"
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
	ID                   string            `json:"id"`
	Title                string            `json:"title"`
	SourceURL            string            `json:"source_url"`
	ArxivID              string            `json:"arxiv_id,omitempty"`
	InitialSummary       string            `json:"initial_summary"`
	ModelUsed            string            `json:"model_used"`
	TotalTokens          int               `json:"total_tokens_used,omitempty"`
	TotalPromptTokens    int               `json:"total_prompt_tokens,omitempty"`
	TotalCompletionTokens int              `json:"total_completion_tokens,omitempty"`
	TotalCachedTokens    int               `json:"total_cached_tokens,omitempty"`
	Rating               int               `json:"rating"`
	CreatedAt            string            `json:"created_at"`
	UpdatedAt            string            `json:"updated_at"`
	Messages             []messageResponse `json:"messages"`
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

	content, sourceURL, arxivID, err := s.fetchPaperContent(req)
	if err != nil {
		log.Printf("[new-paper] fetch error: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	log.Printf("[new-paper] fetched %d chars, creating paper", len(content))

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

	paper := session.NewPaper(content, sourceURL, arxivID)
	paper.ModelUsed = s.cfg.API.DefaultModel

	// Try HTML title extraction for arXiv papers (instant, no LLM call)
	if arxivID != "" {
		if title, err := urlparse.FetchArxivTitle(arxivID); err == nil && title != "" {
			paper.SetTitle(title)
			log.Printf("[new-paper] title from HTML: %s", title)
		} else {
			log.Printf("[new-paper] HTML title extraction failed for %s: %v", arxivID, err)
		}
	}

	// Add initial user message FIRST so the paper has content before any save.
	paper.AddMessage(session.Message{
		RoundNumber: 0,
		Role:        "user",
		Content:     content,
		TokenCount:  session.EstimateTokens(content),
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

	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetHeavy()},
		{Role: "user", Content: content},
	}

	ch := s.api.ChatStream(s.cfg.API.DefaultModel, messages)
	var summaryBuilder strings.Builder
	var promptTokens, completionTokens, cachedTokens int

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

	// Auto-recover lost content from source URL if possible
	if paper.Content == "" && paper.SourceURL != "" {
		log.Printf("[chat] content empty for %s, re-fetching from %s", id, paper.SourceURL)
		sourceURL := paper.SourceURL
		unlock()

		content, _, _, err := s.fetchPaperContent(newPaperRequest{URL: sourceURL})
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

		for i, m := range paper.Messages {
			if m.RoundNumber == 0 && m.Role == "user" {
				paper.Messages[i].Content = content
				paper.Messages[i].TokenCount = session.EstimateTokens(content)
				break
			}
		}
		paper.Content = content
		paper.Save()
		log.Printf("[chat] recovered %d chars from source URL", len(content))
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

	log.Printf("[chat] question: %s", req.Question)

	round := paper.CurrentRound() + 1

	// Add user message
	userMsg := session.Message{
		RoundNumber: round,
		Role:        "user",
		Content:     req.Question,
		TokenCount:  session.EstimateTokens(req.Question),
		SkipContext: req.SkipContext,
	}
	paper.AddMessage(userMsg)

	// Build messages for CHAT phase
	recent := paper.RecentContextMessages(s.cfg.UI.MaxRecentRounds)
	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetLight()},
		{Role: "user", Content: fmt.Sprintf("以下是论文全文：\n\n%s", paper.Content)},
	}
	for _, msg := range recent {
		messages = append(messages, api.ChatMessage{Role: msg.Role, Content: msg.Content})
	}
	// Add current question
	messages = append(messages, api.ChatMessage{Role: "user", Content: req.Question})
	unlock() // Release lock before SSE stream

	// Stream answer via SSE
	sw, err := newSSEWriter(w)
	if err != nil {
		log.Printf("[chat] SSE not supported: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	ch := s.api.ChatStream(s.cfg.API.DefaultModel, messages)
	var answerBuilder strings.Builder
	var promptTokens, completionTokens, cachedTokens int

	for chunk := range ch {
		select {
		case <-r.Context().Done():
			log.Printf("[chat] client disconnected")
			return
		default:
		}

		if chunk.Err != nil {
			log.Printf("[chat] stream error: %v", chunk.Err)
			sw.WriteError(chunk.Err.Error())
			return
		}
		if chunk.Done {
			promptTokens = chunk.PromptTokens
			completionTokens = chunk.CompletionTokens
			cachedTokens = chunk.CachedTokens
			break
		}
		answerBuilder.WriteString(chunk.Content)
		if err := sw.WriteChunk(chunk.Content); err != nil {
			return
		}
	}

	answer := answerBuilder.String()
	log.Printf("[chat] answer complete: %d chars", len(answer))

	unlock = s.lockPaper(id)
	// Save assistant message
	assistantMsg := session.Message{
		RoundNumber:      round,
		Role:             "assistant",
		Content:          answer,
		TokenCount:       session.EstimateTokens(answer),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CachedTokens:     cachedTokens,
		SkipContext:      req.SkipContext,
	}
	paper.AddMessage(assistantMsg)
	paper.Save()
	unlock()

	sw.WriteDoneWithTokens(paper.Ref(), promptTokens, completionTokens, cachedTokens)
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

		content, _, _, err := s.fetchPaperContent(newPaperRequest{URL: sourceURL})
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

		// Update the round-0 user message with recovered content
		for i, m := range paper.Messages {
			if m.RoundNumber == 0 && m.Role == "user" {
				paper.Messages[i].Content = content
				paper.Messages[i].TokenCount = session.EstimateTokens(content)
				break
			}
		}
		paper.Content = content
		paper.Save()
		log.Printf("[retry-summary] recovered %d chars from source URL", len(content))
	}

	existingSummary := paper.InitialSummary
	unlock()

	// Build messages
	msgs := []api.ChatMessage{
		{Role: "system", Content: prompt.GetHeavy()},
		{Role: "user", Content: paper.Content},
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

	ch := s.api.ChatStream(s.cfg.API.DefaultModel, msgs)
	var newContent strings.Builder
	var promptTokens, completionTokens, cachedTokens int

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

	// Remove any existing round-0 assistant messages and add the updated one
	var filtered []session.Message
	for _, m := range paper.Messages {
		if !(m.RoundNumber == 0 && m.Role == "assistant") {
			filtered = append(filtered, m)
		}
	}
	paper.Messages = filtered
	paper.AddMessage(session.Message{
		RoundNumber:      0,
		Role:             "assistant",
		Content:          final,
		TokenCount:       session.EstimateTokens(final),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CachedTokens:     cachedTokens,
	})
	paper.Save()

	sw.WriteDoneWithTokens(paper.Ref(), promptTokens, completionTokens, cachedTokens)
}

// handleRetryChat regenerates the assistant answer for a specific round.
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

	// Remove existing assistant message for this round
	var filtered []session.Message
	for _, m := range paper.Messages {
		if !(m.RoundNumber == round && m.Role == "assistant") {
			filtered = append(filtered, m)
		}
	}
	paper.Messages = filtered

	// Build messages: paper + recent rounds up to (but not including) this round
	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetLight()},
		{Role: "user", Content: fmt.Sprintf("以下是论文全文：\n\n%s", paper.Content)},
	}
	// Include messages from rounds before this one (skip btw messages)
	for _, m := range paper.Messages {
		if m.SkipContext {
			continue
		}
		if m.RoundNumber < round || (m.RoundNumber == round && m.Role == "user") {
			messages = append(messages, api.ChatMessage{Role: m.Role, Content: m.Content})
		}
	}
	// Cap recent context
	if len(messages) > s.cfg.UI.MaxRecentRounds*2+2 {
		messages = append([]api.ChatMessage{messages[0], messages[1]}, messages[len(messages)-s.cfg.UI.MaxRecentRounds*2:]...)
	}
	unlock() // Release lock before SSE stream

	sw, err := newSSEWriter(w)
	if err != nil {
		log.Printf("[retry-chat] SSE not supported: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	ch := s.api.ChatStream(s.cfg.API.DefaultModel, messages)
	var answer strings.Builder
	var promptTokens, completionTokens, cachedTokens int

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

	result := answer.String()
	log.Printf("[retry-chat] answer complete: %d chars", len(result))

	unlock = s.lockPaper(id)
	defer unlock()

	paper, err = session.LoadPaperByRef(id)
	if err != nil {
		log.Printf("[retry-chat] reload failed: %v", err)
		return
	}

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
			"vault_path":    s.cfg.Obsidian.VaultPath,
			"export_folder": s.cfg.Obsidian.ExportFolder,
		},
		"ui": map[string]int{
			"max_recent_rounds": s.cfg.UI.MaxRecentRounds,
		},
		"feishu": map[string]interface{}{
			"enabled":    s.cfg.Feishu.Enabled,
			"app_id":     maskFeishu(s.cfg.Feishu.AppID),
			"app_secret": maskFeishu(s.cfg.Feishu.AppSecret),
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

	s.cfg.Lock()
	if v, ok := updates["api_key"].(string); ok && v != "" {
		if isEnvVarName(v) {
			s.cfg.API.APIKey = "${" + v + "}"
		} else {
			s.cfg.API.APIKey = v
		}
	}
	if v, ok := updates["base_url"].(string); ok && v != "" {
		s.cfg.API.BaseURL = v
	}
	if v, ok := updates["default_model"].(string); ok && v != "" {
		s.cfg.API.DefaultModel = v
	}
	if v, ok := updates["max_recent_rounds"].(float64); ok {
		s.cfg.UI.MaxRecentRounds = int(v)
	}
	if v, ok := updates["obsidian_vault_path"].(string); ok {
		s.cfg.Obsidian.VaultPath = v
	}
	if v, ok := updates["obsidian_export_folder"].(string); ok {
		s.cfg.Obsidian.ExportFolder = v
	}
	if v, ok := updates["feishu_enabled"].(bool); ok {
		s.cfg.Feishu.Enabled = v
	}
	if v, ok := updates["feishu_app_id"].(string); ok {
		s.cfg.Feishu.AppID = v
	}
	if v, ok := updates["feishu_app_secret"].(string); ok {
		if v != "" {
			s.cfg.Feishu.AppSecret = v
		}
	}
	s.cfg.Unlock()

	if err := s.cfg.Save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save config failed"})
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

	// Build context
	var context strings.Builder
	if paper.InitialSummary != "" {
		context.WriteString("## 初始总结\n\n")
		context.WriteString(paper.InitialSummary)
		context.WriteString("\n\n")
	}
	context.WriteString("## 对话历史\n\n")
	for _, msg := range paper.Messages {
		if msg.Role == "user" {
			context.WriteString(fmt.Sprintf("Q: %s\n", msg.Content))
		} else {
			context.WriteString(fmt.Sprintf("A: %s\n", msg.Content))
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

	ch := s.api.ChatStream(s.cfg.API.DefaultModel, messages)
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

	// Build context for summarize
	var context strings.Builder
	if paper.InitialSummary != "" {
		context.WriteString("## 初始总结\n\n")
		context.WriteString(paper.InitialSummary)
		context.WriteString("\n\n")
	}
	context.WriteString("## 对话历史\n\n")
	for _, msg := range paper.Messages {
		if msg.Role == "user" {
			context.WriteString(fmt.Sprintf("Q: %s\n", msg.Content))
		} else {
			context.WriteString(fmt.Sprintf("A: %s\n", msg.Content))
		}
	}

	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetSummarize()},
		{Role: "user", Content: context.String()},
	}

	result, _, _, _, _, err := s.api.Chat(s.cfg.API.DefaultModel, messages)
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

func (s *Server) fetchPaperContent(req newPaperRequest) (content string, sourceURL string, arxivID string, err error) {
	if req.URL != "" {
		sourceURL = req.URL
		if arxivURL, id, ok := urlparse.NormalizeArxivInput(req.URL); ok {
			sourceURL = arxivURL
			arxivID = id
			content, err = urlparse.FetchURL(arxivURL)
		} else {
			content, err = urlparse.FetchURL(req.URL)
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
		InitialSummary:        p.InitialSummary,
		ModelUsed:             p.ModelUsed,
		TotalTokens:           p.TotalTokens,
		TotalPromptTokens:     p.TotalPromptTokens,
		TotalCompletionTokens: p.TotalCompletionTokens,
		TotalCachedTokens:     p.TotalCachedTokens,
		Rating:                p.Rating,
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
