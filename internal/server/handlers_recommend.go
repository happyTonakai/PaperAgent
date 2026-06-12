package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/database"
	"github.com/happyTonakai/paperagent/internal/recommend"
)

// --- Scoring client helper ---

func (s *Server) scoringClient() *api.Client {
	s.cfg.RLock()
	defer s.cfg.RUnlock()

	ep := s.cfg.API.Scoring
	if ep != nil && ep.BaseURL != "" && ep.APIKey != "" {
		return api.NewClientFromEndpoint(ep.BaseURL, ep.APIKey, ep.Model)
	}
	// Fallback to main API config
	return api.NewClientFromEndpoint(s.cfg.API.BaseURL, s.cfg.API.APIKey, s.cfg.API.DefaultModel)
}

func (s *Server) scoringModel() string {
	s.cfg.RLock()
	defer s.cfg.RUnlock()

	if s.cfg.API.Scoring != nil && s.cfg.API.Scoring.Model != "" {
		return s.cfg.API.Scoring.Model
	}
	return s.cfg.API.DefaultModel
}

// --- Config ---

func (s *Server) handleRecommendGetConfig(w http.ResponseWriter, r *http.Request) {
	s.cfg.RLock()
	defer s.cfg.RUnlock()
	resp := map[string]interface{}{
		"recommend": map[string]interface{}{
			"daily_papers":        s.cfg.Recommend.DailyPapers,
			"scoring_batch_size":  s.cfg.Recommend.ScoringBatchSize,
			"auto_refresh":        s.cfg.Recommend.AutoRefresh,
			"diversity_ratio":     s.cfg.Recommend.DiversityRatio,
		},
		"arxiv_categories": s.cfg.ArxivCategories,
		"api": map[string]interface{}{
			"scoring": apiEndpointToMap(s.cfg.API.Scoring),
		},
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
		if v, ok := rc["auto_refresh"].(bool); ok {
			s.cfg.Recommend.AutoRefresh = v
		}
		if v, ok := rc["diversity_ratio"].(float64); ok {
			s.cfg.Recommend.DiversityRatio = v
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

	if apiCfg, ok := updates["api"].(map[string]interface{}); ok {
		if sc, ok := apiCfg["scoring"].(map[string]interface{}); ok {
			if s.cfg.API.Scoring == nil {
				s.cfg.API.Scoring = &config.APIEndpoint{}
			}
			if v, ok := sc["base_url"].(string); ok && v != "" {
				s.cfg.API.Scoring.BaseURL = v
			}
			if v, ok := sc["api_key"].(string); ok && v != "" && !strings.HasPrefix(v, "•") {
				s.cfg.API.Scoring.APIKey = v
			}
			if v, ok := sc["model"].(string); ok && v != "" {
				s.cfg.API.Scoring.Model = v
			}
		}
	}

	s.cfg.Unlock()

	if err := s.cfg.Save(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save config failed"})
		return
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

// translationClient returns an API client for the translation endpoint,
// or nil if translation is not configured.
func (s *Server) translationClient() *api.Client {
	s.cfg.RLock()
	defer s.cfg.RUnlock()

	ep := s.cfg.API.Translation
	if ep != nil && ep.BaseURL != "" && ep.APIKey != "" {
		return api.NewClientFromEndpoint(ep.BaseURL, ep.APIKey, ep.Model)
	}
	return nil
}

// translateAndPersistArticles translates articles' titles and abstracts (if translation
// API is configured) and persists the translations to the SQLite database.
// Called once after each daily recommendation run.
func (s *Server) translateAndPersistArticles(articles []database.Article) {
	transClient := s.translationClient()
	if transClient == nil {
		return // no translation API configured
	}

	s.cfg.RLock()
	model := ""
	if s.cfg.API.Translation != nil {
		model = s.cfg.API.Translation.Model
	}
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

	// Collect texts
	var texts []string
	type mapping struct {
		idx     int
		isTitle bool
	}
	var mappings []mapping

	for i, a := range toTranslate {
		if a.Title != "" {
			texts = append(texts, a.Title)
			mappings = append(mappings, mapping{idx: i, isTitle: true})
		}
		if a.Abstract != nil && *a.Abstract != "" {
			texts = append(texts, *a.Abstract)
			mappings = append(mappings, mapping{idx: i, isTitle: false})
		}
	}

	if len(texts) == 0 {
		return
	}

	results, err := transClient.TranslateTexts(model, texts)
	if err != nil {
		log.Printf("[server] translate articles for DB: %v", err)
		return
	}

	// Group results by article
	type artTrans struct {
		translatedTitle    string
		translatedAbstract string
	}
	transMap := make(map[int]*artTrans, len(toTranslate))
	for i, m := range mappings {
		if i >= len(results) || results[i] == "" {
			continue
		}
		if transMap[m.idx] == nil {
			transMap[m.idx] = &artTrans{}
		}
		if m.isTitle {
			transMap[m.idx].translatedTitle = results[i]
		} else {
			transMap[m.idx].translatedAbstract = results[i]
		}
	}

	// Persist to DB
	for idx, tr := range transMap {
		if err := database.UpdateArticleTranslations(toTranslate[idx].ID, tr.translatedTitle, tr.translatedAbstract); err != nil {
			log.Printf("[server] persist translation for %s: %v", toTranslate[idx].ID, err)
		}
	}

	log.Printf("[server] translated and persisted %d articles", len(transMap))
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

func apiEndpointToMap(ep *config.APIEndpoint) map[string]interface{} {
	if ep == nil {
		return nil
	}
	maskedKey := "••••••••"
	if len(ep.APIKey) > 8 {
		maskedKey = ep.APIKey[:4] + "••••" + ep.APIKey[len(ep.APIKey)-4:]
	}
	return map[string]interface{}{
		"base_url": ep.BaseURL,
		"api_key":  maskedKey,
		"model":    ep.Model,
	}
}
