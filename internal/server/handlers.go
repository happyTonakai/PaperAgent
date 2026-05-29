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

	"github.com/paperpaper/paperpaper/internal/api"
	"github.com/paperpaper/paperpaper/internal/config"
	"github.com/paperpaper/paperpaper/internal/export"
	"github.com/paperpaper/paperpaper/internal/prompt"
	"github.com/paperpaper/paperpaper/internal/session"
	"github.com/paperpaper/paperpaper/internal/urlparse"
)

// --- Request types ---

type newPaperRequest struct {
	URL     string `json:"url"`
	Content string `json:"content"`
}

type chatRequest struct {
	Question string `json:"question"`
}

// --- Response types ---

type paperResponse struct {
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	SourceURL      string            `json:"source_url"`
	InitialSummary string            `json:"initial_summary"`
	ModelUsed      string            `json:"model_used"`
	Rating         int               `json:"rating"`
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`
	Messages       []messageResponse `json:"messages"`
}

type messageResponse struct {
	RoundNumber int    `json:"round_number"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	Digest      string `json:"digest,omitempty"`
	TokenCount  int    `json:"token_count"`
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

	content, sourceURL, err := s.fetchPaperContent(req)
	if err != nil {
		log.Printf("[new-paper] fetch error: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	log.Printf("[new-paper] fetched %d chars, creating paper", len(content))

	paper := session.NewPaper(content, sourceURL)
	paper.ModelUsed = s.cfg.API.DefaultModel

	// Try HTML title extraction for arXiv papers (instant, no LLM call)
	if _, arxivID, ok := urlparse.NormalizeArxivInput(sourceURL); ok {
		if title, err := urlparse.FetchArxivTitle(arxivID); err == nil && title != "" {
			paper.SetTitle(title)
			log.Printf("[new-paper] title from HTML: %s", title)
		} else {
			log.Printf("[new-paper] HTML title extraction failed for %s: %v", arxivID, err)
		}
	}

	if err := paper.Save(); err != nil {
		log.Printf("[new-paper] save error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
		return
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

	// Add initial user message
	paper.AddMessage(session.Message{
		RoundNumber: 0,
		Role:        "user",
		Content:     content,
		TokenCount:  session.EstimateTokens(content),
	})

	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetHeavy()},
		{Role: "user", Content: content},
	}

	ch := s.api.ChatStream(s.cfg.API.DefaultModel, messages)
	var summaryBuilder strings.Builder

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
	paper.Save()

	sw.WriteDone(paper.Ref())
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
	}
	paper.AddMessage(userMsg)

	// Build messages for CHAT phase
	recent := paper.RecentMessages(s.cfg.UI.MaxRecentRounds)
	messages := []api.ChatMessage{
		{Role: "system", Content: prompt.GetLight()},
		{Role: "user", Content: fmt.Sprintf("以下是论文全文：\n\n%s", paper.Content)},
	}
	for _, msg := range recent {
		messages = append(messages, api.ChatMessage{Role: msg.Role, Content: msg.Content})
	}
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
		RoundNumber: round,
		Role:        "assistant",
		Content:     answer,
		TokenCount:  session.EstimateTokens(answer),
	}
	paper.AddMessage(assistantMsg)
	paper.Save()
	unlock()

	// Generate digest async — reload paper inside goroutine by ref
	go func(paperRef string, question string, round int) {
		digest, err := s.api.SummarizeQuestion(s.cfg.API.LightModel, question)
		if err == nil && digest != "" {
			p, loadErr := session.LoadPaperByRef(paperRef)
			if loadErr != nil {
				log.Printf("[chat] reload paper for digest: %v", loadErr)
				return
			}
			for i := range p.Messages {
				if p.Messages[i].Role == "user" && p.Messages[i].RoundNumber == round {
					p.Messages[i].Digest = digest
					break
				}
			}
			p.Save()
		}
	}(paper.Ref(), req.Question, round)

	sw.WriteDone(paper.Ref())
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

	if paper.Content == "" {
		unlock()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "paper content is empty"})
		return
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
	paper.Save()

	sw.WriteDone(paper.Ref())
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
	// Include messages from rounds before this one
	for _, m := range paper.Messages {
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
		RoundNumber: round,
		Role:        "assistant",
		Content:     result,
		TokenCount:  session.EstimateTokens(result),
	})
	paper.Save()

	sw.WriteDone(paper.Ref())
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
			"light_model":    s.cfg.API.LightModel,
		},
		"obsidian": map[string]string{
			"vault_path":    s.cfg.Obsidian.VaultPath,
			"export_folder": s.cfg.Obsidian.ExportFolder,
		},
		"ui": map[string]int{
			"max_recent_rounds": s.cfg.UI.MaxRecentRounds,
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
	if v, ok := updates["light_model"].(string); ok && v != "" {
		s.cfg.API.LightModel = v
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
	s.cfg.Unlock()

	if err := s.cfg.Save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save config failed"})
		return
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

// --- Helpers ---

func (s *Server) fetchPaperContent(req newPaperRequest) (content string, sourceURL string, err error) {
	if req.URL != "" {
		sourceURL = req.URL
		if arxivURL, _, ok := urlparse.NormalizeArxivInput(req.URL); ok {
			sourceURL = arxivURL
			content, err = urlparse.FetchURL(arxivURL)
		} else {
			content, err = urlparse.FetchURL(req.URL)
		}
		if err != nil {
			return "", "", fmt.Errorf("fetch URL failed: %w", err)
		}
		if content == "" {
			return "", "", fmt.Errorf("empty content from URL")
		}
	} else if req.Content != "" {
		content = req.Content
	} else {
		return "", "", fmt.Errorf("url or content required")
	}
	return content, sourceURL, nil
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
			RoundNumber: m.RoundNumber,
			Role:        m.Role,
			Content:     m.Content,
			Digest:      m.Digest,
			TokenCount:  m.TokenCount,
		})
	}

	return paperResponse{
		ID:             p.Ref(),
		Title:          p.Title,
		SourceURL:      p.SourceURL,
		InitialSummary: p.InitialSummary,
		ModelUsed:      p.ModelUsed,
		Rating:         p.Rating,
		CreatedAt:      p.CreatedAt.Format("2006-01-02 15:04"),
		UpdatedAt:      p.UpdatedAt.Format("2006-01-02 15:04"),
		Messages:       msgs,
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
