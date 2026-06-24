package recommend

import (
	"fmt"
	"log"
	"time"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/database"
)

// ScoringClient wraps an API client with its configured model.
type ScoringClient struct {
	Client *api.Client
	Model  string
}

// GenerateDailyRecommendations runs the full daily recommendation pipeline:
//  1. Read preferences
//  2. Fetch and score unscored articles via LLM
//  3. Mark top N as daily recommendations
//  4. Fetch community votes for recommended articles (display only)
//
// Returns the recommended articles.
func GenerateDailyRecommendations(scoring *ScoringClient, dailyPapers, batchSize int, diversityRatio float64) ([]database.Article, error) {
	prefs, err := ReadPreferences()
	if err != nil {
		return nil, fmt.Errorf("read preferences: %w", err)
	}

	if prefs == "" {
		// No preferences → cannot run LLM scoring. Still continue to
		// MarkDailyRecommendations; it will pick from all unread articles
		// (now that the SQL filter is score >= 0) ordered by created_at DESC.
		log.Println("[recommend] preferences empty, skipping LLM scoring")
	} else {
		// Get unscored articles
		unscored, err := database.GetUnscoredArticles(500)
		if err != nil {
			return nil, fmt.Errorf("get unscored: %w", err)
		}

		if len(unscored) > 0 {
			log.Printf("[recommend] scoring %d unscored articles...", len(unscored))
			articles := make([]ArticleInfo, len(unscored))
			for i, a := range unscored {
				abstract := ""
				if a.Abstract != nil {
					abstract = *a.Abstract
				}
				articles[i] = ArticleInfo{
					ID:       a.ID,
					Title:    a.Title,
					Abstract: abstract,
				}
			}

			onProgress := func(completed, total int) {
				log.Printf("[recommend] scoring batch %d/%d", completed, total)
			}

			scores, err := ScoreArticlesBatch(scoring.Client, scoring.Model, prefs, articles, batchSize, onProgress)
			if err != nil {
				return nil, fmt.Errorf("score articles: %w", err)
			}

			if err := database.UpdateArticleScores(scores); err != nil {
				return nil, fmt.Errorf("update scores: %w", err)
			}
			log.Printf("[recommend] scored %d articles", len(scores))
		} else {
			log.Println("[recommend] no unscored articles to score")
		}
	}

	// Mark daily recommendations
	today := time.Now().Format("2006-01-02")
	count, err := database.MarkDailyRecommendations(today, dailyPapers, diversityRatio)
	if err != nil {
		return nil, fmt.Errorf("mark daily recommendations: %w", err)
	}
	log.Printf("[recommend] marked %d daily recommendations for %s", count, today)

	// Fetch community votes for recommended articles (display only, not used in ranking)
	var recs []database.Article
	if count > 0 {
		recs, err = database.GetArticlesByRecommendDate(today)
		if err != nil {
			log.Printf("[recommend] get recommended articles for votes: %v", err)
		} else {
			var ids []string
			for _, r := range recs {
				ids = append(ids, r.ID)
			}
			if len(ids) > 0 {
				votes := FetchVotesForArticles(ids)
				for id, v := range votes {
					if err := database.UpdateArticleVotes(id, v.HFUpvotes, v.AXNetVotes); err != nil {
						log.Printf("[recommend] update votes for %s: %v", id, err)
					}
				}
				log.Printf("[recommend] fetched votes for %d articles", len(votes))
			}
		}
	}

	return recs, nil
}

// FetchAndStoreRSS fetches articles from arXiv RSS, filters by excluded
// keywords, and saves to the database. Returns the number of newly inserted
// articles. This is a standalone entry point for the scheduler's periodic RSS
// fetch jobs (separate from the daily recommendation pipeline).
func FetchAndStoreRSS(categories []string, excludedKeywords []string) (int, error) {
	if len(categories) == 0 {
		return 0, nil
	}

	articles, err := FetchArxivRSS(categories, 100)
	if err != nil {
		return 0, fmt.Errorf("fetch RSS: %w", err)
	}
	if len(articles) > 0 && len(excludedKeywords) > 0 {
		before := len(articles)
		articles = FilterArticlesByKeywords(articles, excludedKeywords)
		if dropped := before - len(articles); dropped > 0 {
			log.Printf("[recommend] filtered out %d RSS articles by excluded keywords (%d kept)", dropped, len(articles))
		}
	}
	if len(articles) > 0 {
		inserted, err := database.SaveArticles(articles)
		if err != nil {
			return 0, fmt.Errorf("save articles: %w", err)
		}
		log.Printf("[recommend] fetched %d new articles from RSS", inserted)
		return inserted, nil
	}
	log.Println("[recommend] no new articles from RSS")
	return 0, nil
}

// FetchAndRecommend runs preference update → RSS fetch → generate daily recommendations.
// This is the main entry point called by the scheduler or manual trigger.
func FetchAndRecommend(categories []string, scoring *ScoringClient, dailyPapers, batchSize int, diversityRatio float64, excludedKeywords []string) ([]database.Article, error) {
	// Step 0: Update preferences based on yesterday's feedback
	prefs, err := ReadPreferences()
	if err != nil {
		log.Printf("[recommend] read preferences error: %v", err)
	}

	feedbacks, err := CollectYesterdayFeedback()
	if err != nil {
		log.Printf("[recommend] collect feedback error: %v", err)
	} else if len(feedbacks) > 0 {
		log.Printf("[recommend] collecting %d feedback signals for preference update", len(feedbacks))
		newPrefs, err := UpdatePreferences(scoring.Client, scoring.Model, prefs, feedbacks)
		if err != nil {
			log.Printf("[recommend] preference update failed: %v", err)
		} else if newPrefs != prefs {
			if err := WritePreferences(newPrefs); err != nil {
				log.Printf("[recommend] write preferences failed: %v", err)
			} else {
				log.Printf("[recommend] preferences updated")
			}
		}
	} else {
		log.Println("[recommend] no new feedback, skipping preference update")
	}

	// Step 1: Fetch RSS
	if _, err := FetchAndStoreRSS(categories, excludedKeywords); err != nil {
		return nil, err
	}

	// Step 2: Generate daily recommendations
	return GenerateDailyRecommendations(scoring, dailyPapers, batchSize, diversityRatio)
}
