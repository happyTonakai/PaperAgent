package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/database"
	"github.com/happyTonakai/paperagent/internal/recommend"
	"github.com/happyTonakai/paperagent/internal/scheduler"
)

// resolveAPIKeyInput normalizes an API key value coming from the settings UI.
// A bare env-var name (e.g. OPENAI_API_KEY) is wrapped as ${NAME} so the
// downstream resolver can expand it via os.ExpandEnv.
func resolveAPIKeyInput(v string) string {
	if isEnvVarName(v) {
		return "${" + v + "}"
	}
	return v
}

// --- Scoring and translation always reuse the main API ---

func (s *Server) scoringClient() *api.Client {
	return s.api
}

func (s *Server) scoringModel() string {
	s.cfg.RLock()
	defer s.cfg.RUnlock()
	return s.cfg.API.DefaultModel
}

// translationClient returns the main API client when translation is enabled,
// or nil when the user has not opted in.
func (s *Server) translationClient() *api.Client {
	s.cfg.RLock()
	defer s.cfg.RUnlock()
	if s.cfg.Recommend.EnableTranslation {
		return s.api
	}
	return nil
}

// --- Config ---

func (s *Server) handleRecommendGetConfig(w http.ResponseWriter, r *http.Request) {
	s.cfg.RLock()
	defer s.cfg.RUnlock()

	resp := map[string]interface{}{
		"recommend": map[string]interface{}{
			"daily_papers":        s.cfg.Recommend.DailyPapers,
			"scoring_batch_size":  s.cfg.Recommend.ScoringBatchSize,
			"diversity_ratio":     s.cfg.Recommend.DiversityRatio,
			"scheduled_time":      s.cfg.Recommend.ScheduledTime,
			"push_to_feishu":      s.cfg.Recommend.PushToFeishu,
			"enable_translation":  s.cfg.Recommend.EnableTranslation,
		},
		"arxiv_categories": s.cfg.ArxivCategories,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRecommendUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	s.cfg.Lock()

	if rc, ok := updates["recommend"].(map[string]interface{}); ok {
		if v, ok := rc["daily_papers"].(float64); ok {
			s.cfg.Recommend.DailyPapers = int(v)
		}
		if v, ok := rc["scoring_batch_size"].(float64); ok {
			s.cfg.Recommend.ScoringBatchSize = int(v)
		}
		if v, ok := rc["diversity_ratio"].(float64); ok {
			s.cfg.Recommend.DiversityRatio = v
		}
		if v, ok := rc["scheduled_time"].(string); ok {
			s.cfg.Recommend.ScheduledTime = v
		}
		if v, ok := rc["push_to_feishu"].(bool); ok {
			s.cfg.Recommend.PushToFeishu = v
		}
		if v, ok := rc["enable_translation"].(bool); ok {
			s.cfg.Recommend.EnableTranslation = v
		}
	}

	if cats, ok := updates["arxiv_categories"].([]interface{}); ok {
		var strCats []string
		for _, c := range cats {
			if s, ok := c.(string); ok {
				strCats = append(strCats, s)
			}
		}
		s.cfg.ArxivCategories = strCats
	}

	s.cfg.Unlock()

	if err := s.cfg.Save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save config failed"})
		return
	}

	// Update scheduler config at runtime
	if s.sched != nil {
		s.cfg.RLock()
		s.sched.UpdateConfig(s.cfg.Recommend.ScheduledTime, s.cfg.Recommend.DailyPapers, s.cfg.Recommend.ScoringBatchSize, s.cfg.Recommend.DiversityRatio)
		s.cfg.RUnlock()
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// --- Preferences ---

func (s *Server) handleRecommendGetPreferences(w http.ResponseWriter, r *http.Request) {
	prefs, err := recommend.ReadPreferences()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": prefs})
}

func (s *Server) handleRecommendSavePreferences(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := recommend.WritePreferences(req.Content); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// --- RSS Fetch ---

func (s *Server) handleRecommendFetch(w http.ResponseWriter, r *http.Request) {
	s.cfg.RLock()
	categories := s.cfg.ArxivCategories
	s.cfg.RUnlock()

	if len(categories) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no arXiv categories configured"})
		return
	}

	articles, err := recommend.FetchArxivRSS(categories, 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	count := 0
	if len(articles) > 0 {
		count, err = database.SaveArticles(articles)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save articles: " + err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]int{"fetched": count})
}

// --- Generate Recommendations ---

func (s *Server) handleRecommendGenerate(w http.ResponseWriter, r *http.Request) {
	s.cfg.RLock()
	dailyPapers := s.cfg.Recommend.DailyPapers
	batchSize := s.cfg.Recommend.ScoringBatchSize
	diversityRatio := s.cfg.Recommend.DiversityRatio
	s.cfg.RUnlock()

	scoring := &recommend.ScoringClient{
		Client: s.scoringClient(),
		Model:  s.scoringModel(),
	}

	articles, err := recommend.GenerateDailyRecommendations(scoring, dailyPapers, batchSize, diversityRatio)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Trigger translation after manual generation. Internally no-ops when no
	// translation API is configured or articles already have translations.
	s.translateAndPersistArticles(articles)

	writeJSON(w, http.StatusOK, map[string]int{"recommended": len(articles)})
}

// --- Article List ---

func (s *Server) handleRecommendArticles(w http.ResponseWriter, r *http.Request) {
	statusStr := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	limit := 50
	offset := 0
	var status *int

	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 200 {
			limit = v
		}
	}
	if offsetStr != "" {
		if v, err := strconv.Atoi(offsetStr); err == nil && v >= 0 {
			offset = v
		}
	}
	if statusStr != "" {
		if v, err := strconv.Atoi(statusStr); err == nil {
			status = &v
		}
	}

	articles, err := database.GetArticles(status, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if articles == nil {
		articles = []database.Article{}
	}
	result := articlesToResponse(articles)
	writeJSON(w, http.StatusOK, result)
}

// --- Today's Recommendations ---

func (s *Server) handleRecommendToday(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	articles, err := database.GetArticlesByRecommendDate(date)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if articles == nil {
		articles = []database.Article{}
	}
	result := articlesToResponse(articles)
	writeJSON(w, http.StatusOK, result)
}

// --- Recommendation Dates ---

func (s *Server) handleRecommendDates(w http.ResponseWriter, r *http.Request) {
	dates, err := database.GetRecommendationDates()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if dates == nil {
		dates = []string{}
	}
	writeJSON(w, http.StatusOK, dates)
}

// --- Articles by Date ---

func (s *Server) handleRecommendArticlesByDate(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if date == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "date required"})
		return
	}
	articles, err := database.GetArticlesByRecommendDate(date)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if articles == nil {
		articles = []database.Article{}
	}
	result := articlesToResponse(articles)
	writeJSON(w, http.StatusOK, result)
}

// --- Article Status ---

func (s *Server) handleRecommendUpdateStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		Status int `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if err := database.UpdateArticleStatus(id, req.Status); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// --- Batch Article Status ---

// handleRecommendBatchUpdateStatus updates the status of many articles at once.
// Used by the "全部已读" / 飞书 "已阅" buttons.
// Body: {"ids": ["arxiv_id", ...], "status": 3}
// Caps at 500 ids per call to avoid unbounded IN clauses.
func (s *Server) handleRecommendBatchUpdateStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs    []string `json:"ids"`
		Status int      `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if len(req.IDs) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no-op"})
		return
	}
	if len(req.IDs) > 500 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many ids (max 500)"})
		return
	}
	if err := database.BatchUpdateArticleStatus(req.IDs, req.Status); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "count": strconv.Itoa(len(req.IDs))})
}

// --- Article Comment ---

func (s *Server) handleRecommendUpdateComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if err := database.UpdateArticleComment(id, req.Comment); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// --- Community Votes ---

func (s *Server) handleRecommendFetchVotes(w http.ResponseWriter, r *http.Request) {
	// Fetch votes for articles that need updating
	ids, err := database.GetArticlesNeedingVotes(200)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, map[string]int{"updated": 0})
		return
	}

	votes := recommend.FetchVotesForArticles(ids)
	updated := 0
	for id, v := range votes {
		if err := database.UpdateArticleVotes(id, v.HFUpvotes, v.AXNetVotes); err != nil {
			log.Printf("[votes] update %s: %v", id, err)
		} else {
			updated++
		}
	}

	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

// --- Stats ---

// handleRecommendTrigger executes the full pipeline: fetch RSS → generate
// recommendations → translate → push to Feishu (if configured and enabled).
// This is the manual trigger endpoint.
func (s *Server) handleRecommendTrigger(w http.ResponseWriter, r *http.Request) {
	if s.sched == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scheduler not initialized (no categories configured)"})
		return
	}

	s.sched.ManualTrigger()

	writeJSON(w, http.StatusOK, map[string]string{"status": "triggered"})
}

// handleRecommendPushToFeishu drains the pending recommendation backlog and
// pushes it to the configured Feishu chat. Holiday-skip is bypassed (force
// push) so users can trigger this from the Web UI on demand. Reuses the
// shared RunPush path used by the scheduler and the Feishu /push command.
func (s *Server) handleRecommendPushToFeishu(w http.ResponseWriter, r *http.Request) {
	s.cfg.RLock()
	chatID := s.cfg.Feishu.DailyRecommendChatID
	pushEnabled := s.cfg.Recommend.PushToFeishu
	s.cfg.RUnlock()

	if chatID == "" {
		log.Printf("[recommend] push-to-feishu: daily_recommend_chat_id not configured")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "daily_recommend_chat_id not configured"})
		return
	}
	if !pushEnabled {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "push_to_feishu is disabled in config"})
		return
	}
	if s.feishuBot == nil {
		log.Printf("[recommend] push-to-feishu: feishu bot not running")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "feishu bot not running"})
		return
	}

	n, err := s.RunPush(true)
	if err != nil {
		// RunPush already logs the inner failure (push/feishu layer),
		// but the handler-level breadcrumb (which endpoint, which user
		// action) is what an operator scanning the Web UI log panel
		// needs to identify the request. Log it here so the failure
		// is grep-able from the buffer as well as visible in the 500 body.
		log.Printf("[recommend] push-to-feishu: failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no_articles", "message": "no pending recommendations"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "pushed", "count": strconv.Itoa(n)})
}

// handleRecommendSchedulerStatus returns the current scheduler state.
func (s *Server) handleRecommendSchedulerStatus(w http.ResponseWriter, r *http.Request) {
	if s.sched == nil {
		writeJSON(w, http.StatusOK, scheduler.SchedulerStatus{
			Scheduled: "",
		})
		return
	}

	status := s.sched.Status()
	s.cfg.RLock()
	status.PushToFeishu = s.cfg.Recommend.PushToFeishu
	s.cfg.RUnlock()

	// Augment with push-backlog stats. These are derived from the DB, not
	// the scheduler itself, but they belong in the same response so the
	// UI can show "积压 N 篇" / "上次推送 X" alongside scheduler state.
	// A DB error here sets a sentinel value so the UI can distinguish
	// "no backlog" (0) from "unknown" (sentinel) — silently treating DB
	// errors as "0" would mislead users about whether the pipeline is
	// healthy.
	if pending, err := database.CountUnpushedArticles(); err == nil {
		status.PendingPushCount = pending
	} else {
		log.Printf("[recommend] scheduler-status: count unpushed: %v", err)
		status.PendingPushCount = -1
	}
	if last, err := database.LastPushAt(); err == nil {
		status.LastPushAt = last
	} else {
		log.Printf("[recommend] scheduler-status: last push at: %v", err)
		status.LastPushAt = "error"
	}

	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleRecommendStats(w http.ResponseWriter, r *http.Request) {
	stats, err := database.GetStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Get total count
	total := 0
	for _, c := range stats {
		total += c
	}

	unread := stats[0]
	clicked := stats[1]
	liked := stats[2]
	disliked := 0
	if v, ok := stats[-1]; ok {
		disliked = v
	}
	read := stats[3]

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"unread":  unread,
		"clicked": clicked,
		"liked":   liked,
		"disliked": disliked,
		"read":    read,
		"total":   total,
	})
}

// ─── Translation helpers ───

// translateAndPersistArticles translates articles' titles and abstracts (if translation
// API is configured) and persists the translations to the SQLite database.
// Called once after each daily recommendation run.
func (s *Server) translateAndPersistArticles(articles []database.Article) {
	transClient := s.translationClient()
	if transClient == nil {
		return // translation not enabled
	}

	s.cfg.RLock()
	model := s.cfg.API.DefaultModel
	s.cfg.RUnlock()

	// Skip articles that already have translations
	var toTranslate []database.Article
	for _, a := range articles {
		if a.TranslatedTitle == nil || *a.TranslatedTitle == "" {
			toTranslate = append(toTranslate, a)
		}
	}
	if len(toTranslate) == 0 {
		return
	}

	// One API call per article: each call is small enough to fit under the
	// transport's ResponseHeaderTimeout, and a single failure won't block
	// the rest of the batch.
	translated := 0
	for _, a := range toTranslate {
		abstract := ""
		if a.Abstract != nil {
			abstract = *a.Abstract
		}
		tTitle, tAbstract, err := transClient.TranslateArticle(model, a.Title, abstract)
		if err != nil {
			log.Printf("[server] translate article %s: %v", a.ID, err)
			continue
		}
		if err := database.UpdateArticleTranslations(a.ID, tTitle, tAbstract); err != nil {
			log.Printf("[server] persist translation for %s: %v", a.ID, err)
			continue
		}
		translated++
	}

	log.Printf("[server] translated and persisted %d articles", translated)
}

// articlesToResponse converts database.Article slice to response maps,
// including translated_title/translated_abstract from the DB fields.
func articlesToResponse(articles []database.Article) []map[string]interface{} {
	result := make([]map[string]interface{}, len(articles))
	for i, a := range articles {
		item := map[string]interface{}{
			"id":             a.ID,
			"title":          a.Title,
			"link":           a.Link,
			"abstract":       a.Abstract,
			"status":         a.Status,
			"score":          a.Score,
			"author":         a.Author,
			"category":       a.Category,
			"hf_upvotes":     a.HFUpvotes,
			"ax_net_votes":   a.AXNetVotes,
			"comment":        a.Comment,
			"recommend_date": a.RecommendDate,
			"batch_order":    a.BatchOrder,
			"created_at":     a.CreatedAt,
		}
		if a.TranslatedTitle != nil && *a.TranslatedTitle != "" {
			item["translated_title"] = *a.TranslatedTitle
		}
		if a.TranslatedAbstract != nil && *a.TranslatedAbstract != "" {
			item["translated_abstract"] = *a.TranslatedAbstract
		}
		result[i] = item
	}
	return result
}

// --- Helpers ---

