package recommend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/happyTonakai/paperagent/internal/database"
)

// TestFetchAndRecommendFullPipeline runs the complete recommendation pipeline
// using the local RSS test file and injected scores (no real API calls).
//
// This tests:
//   1. RSS feed parsing from local file
//   2. Saving articles to SQLite
//   3. Scoring with deterministic random scores
//   4. MarkDailyRecommendations with hybrid (score + random) strategy
//   5. Querying recommended articles
func TestFetchAndRecommendFullPipeline(t *testing.T) {
	// --- Setup: in-memory SQLite ---
	conn, cleanup, err := database.OpenTestDB()
	if err != nil {
		t.Fatalf("OpenTestDB: %v", err)
	}
	database.SetDB(conn)
	defer func() {
		cleanup()
		database.SetDB(nil)
	}()

	// --- Step 1: Parse local RSS XML ---
	rssPath := filepath.Join("..", "..", "testdata", "arxiv_cs.SD.rss.xml")
	data, err := os.ReadFile(rssPath)
	if err != nil {
		t.Skipf("testdata not available: %v", err)
	}

	articles, err := parseArxivRSS(data, "cs.SD")
	if err != nil {
		t.Fatalf("parseArxivRSS: %v", err)
	}
	if len(articles) < 5 {
		t.Fatalf("need at least 5 articles from RSS, got %d", len(articles))
	}
	t.Logf("parsed %d articles from RSS", len(articles))

	// --- Step 2: Save to DB ---
	inserted, err := database.SaveArticles(articles)
	if err != nil {
		t.Fatalf("SaveArticles: %v", err)
	}
	if inserted < 5 {
		t.Fatalf("inserted %d, need at least 5", inserted)
	}
	t.Logf("inserted %d articles", inserted)

	// --- Step 3: Inject synthetic scores (no real LLM call) ---
	// Score all articles with deterministic "random" values
	articleList, err := database.GetUnscoredArticles(200)
	if err != nil {
		t.Fatalf("GetUnscoredArticles: %v", err)
	}
	if len(articleList) == 0 {
		t.Fatal("no unscored articles")
	}

	scores := make(map[string]float64)
	for i, a := range articleList {
		// Deterministic "random" score based on index
		scores[a.ID] = float64(len(articleList)-i) / float64(len(articleList))
	}
	if err := database.UpdateArticleScores(scores); err != nil {
		t.Fatalf("UpdateArticleScores: %v", err)
	}
	t.Logf("scored %d articles", len(scores))

	// --- Step 4: Mark daily recommendations with hybrid strategy ---
	today := "2026-06-13"
	dailyPapers := 10
	diversityRatio := 0.3

	count, err := database.MarkDailyRecommendations(today, dailyPapers, diversityRatio)
	if err != nil {
		t.Fatalf("MarkDailyRecommendations: %v", err)
	}
	if count == 0 {
		t.Fatal("no recommendations generated")
	}
	t.Logf("generated %d recommendations", count)

	// --- Step 5: Verify results ---
	recs, err := database.GetArticlesByRecommendDate(today)
	if err != nil {
		t.Fatalf("GetArticlesByRecommendDate: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("no recommended articles found")
	}

	// Check types
	scoreCount := 0
	randomCount := 0
	for _, r := range recs {
		if r.RecommendationType == nil {
			t.Errorf("article[%s] has nil recommendation_type", r.ID)
			continue
		}
		switch *r.RecommendationType {
		case "score":
			scoreCount++
		case "random":
			randomCount++
		default:
			t.Errorf("unexpected recommendation_type: %q", *r.RecommendationType)
		}
	}
	t.Logf("recommendations: %d score-based, %d random", scoreCount, randomCount)

	if scoreCount == 0 {
		t.Error("expected at least 1 score-based recommendation")
	}
	if randomCount == 0 {
		t.Error("expected at least 1 random exploration recommendation")
	}

	// Batch orders should be 0..n-1
	for i, r := range recs {
		if r.BatchOrder == nil || *r.BatchOrder != i {
			t.Errorf("article[%d] batch_order = %v, want %d", i, r.BatchOrder, i)
		}
	}

	// All should have basic fields populated
	for i, r := range recs {
		if r.ID == "" {
			t.Errorf("rec[%d] has empty ID", i)
		}
		if r.Title == "" {
			t.Errorf("rec[%d] has empty Title", i)
		}
		if r.Link == "" {
			t.Errorf("rec[%d] has empty Link", i)
		}
	}

	// --- Step 6: Partial re-run (diversityRatio=0 → pure score) ---
	count2, err := database.MarkDailyRecommendations(today, 5, 0)
	if err != nil {
		t.Fatalf("MarkDailyRecommendations (pure): %v", err)
	}
	recs2, err := database.GetArticlesByRecommendDate(today)
	if err != nil {
		t.Fatalf("GetArticlesByRecommendDate after pure re-run: %v", err)
	}
	if len(recs2) == 0 {
		t.Fatal("pure score re-run produced no recommendations")
	}
	for _, r := range recs2 {
		if r.RecommendationType == nil || *r.RecommendationType != "score" {
			t.Errorf("pure mode had recommendation_type = %v", r.RecommendationType)
		}
	}
	t.Logf("pure score run: %d recommendations (all score type)", count2)
}
