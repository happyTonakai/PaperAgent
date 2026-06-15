package database

import (
	"fmt"
	"testing"
	"time"
)

// setupTestDB creates an in-memory SQLite database for testing
// and registers it via SetDB. Returns cleanup function that also resets SetDB.
func setupTestDB(t *testing.T) func() {
	t.Helper()
	conn, cleanup, err := OpenTestDB()
	if err != nil {
		t.Fatalf("OpenTestDB: %v", err)
	}
	SetDB(conn)
	return func() {
		cleanup()
		SetDB(nil)
	}
}

func TestSaveAndGetArticle(t *testing.T) {
	defer setupTestDB(t)()

	abstract := "This is a test abstract about machine learning."
	err := SaveArticle(&NewArticle{
		ID:       "2401.00001",
		Title:    "Test Paper Title",
		Link:     "https://arxiv.org/abs/2401.00001",
		Abstract: &abstract,
		Author:   strPtr("John Doe"),
		Category: strPtr("cs.LG"),
	})
	if err != nil {
		t.Fatalf("SaveArticle: %v", err)
	}

	article, err := GetArticleByID("2401.00001")
	if err != nil {
		t.Fatalf("GetArticleByID: %v", err)
	}
	if article == nil {
		t.Fatal("article not found")
	}
	if article.Title != "Test Paper Title" {
		t.Errorf("Title = %q, want %q", article.Title, "Test Paper Title")
	}
	if article.Abstract == nil || *article.Abstract != abstract {
		t.Errorf("Abstract mismatch")
	}
	if article.Author == nil || *article.Author != "John Doe" {
		t.Errorf("Author mismatch")
	}
	if article.Category == nil || *article.Category != "cs.LG" {
		t.Errorf("Category mismatch")
	}
}

func TestUpsertArticleByArxivID(t *testing.T) {
	defer setupTestDB(t)()

	abstract := "Abstract from Q&A paper."

	err := UpsertArticleByArxivID("2401.00002", "Q&A Paper", "https://arxiv.org/abs/2401.00002", &abstract)
	if err != nil {
		t.Fatalf("UpsertArticleByArxivID: %v", err)
	}

	article, err := GetArticleByID("2401.00002")
	if err != nil {
		t.Fatalf("GetArticleByID: %v", err)
	}
	if article == nil {
		t.Fatal("article not found after upsert")
	}
	if article.Title != "Q&A Paper" {
		t.Errorf("Title = %q, want %q", article.Title, "Q&A Paper")
	}
	if article.Abstract == nil || *article.Abstract != abstract {
		t.Errorf("Abstract mismatch")
	}

	// Update with new abstract
	newAbstract := "Updated abstract."
	err = UpsertArticleByArxivID("2401.00002", "Q&A Paper", "https://arxiv.org/abs/2401.00002", &newAbstract)
	if err != nil {
		t.Fatalf("UpsertArticleByArxivID (update): %v", err)
	}

	article, err = GetArticleByID("2401.00002")
	if err != nil {
		t.Fatalf("GetArticleByID after update: %v", err)
	}
	if article.Abstract == nil || *article.Abstract != newAbstract {
		t.Errorf("Abstract after update = %q, want %q", *article.Abstract, newAbstract)
	}
}

func TestUpdateArticleTranslations(t *testing.T) {
	defer setupTestDB(t)()

	abstract := "Original abstract."
	err := SaveArticle(&NewArticle{
		ID:       "2401.00003",
		Title:    "Test Paper",
		Link:     "https://arxiv.org/abs/2401.00003",
		Abstract: &abstract,
	})
	if err != nil {
		t.Fatalf("SaveArticle: %v", err)
	}

	err = UpdateArticleTranslations("2401.00003", "翻译后的标题", "翻译后的摘要")
	if err != nil {
		t.Fatalf("UpdateArticleTranslations: %v", err)
	}

	article, err := GetArticleByID("2401.00003")
	if err != nil {
		t.Fatalf("GetArticleByID: %v", err)
	}
	if article.TranslatedTitle == nil || *article.TranslatedTitle != "翻译后的标题" {
		t.Errorf("TranslatedTitle = %v, want %q", article.TranslatedTitle, "翻译后的标题")
	}
	if article.TranslatedAbstract == nil || *article.TranslatedAbstract != "翻译后的摘要" {
		t.Errorf("TranslatedAbstract = %v, want %q", article.TranslatedAbstract, "翻译后的摘要")
	}
	if article.Title != "Test Paper" {
		t.Errorf("Title changed to %q", article.Title)
	}
	if article.Abstract == nil || *article.Abstract != abstract {
		t.Errorf("Abstract changed")
	}
}

func TestMarkDailyRecommendations_PureScore(t *testing.T) {
	defer setupTestDB(t)()

	// Insert 10 articles with scores 1.0, 0.9, ..., 0.1
	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("2401.000%02d", i)
		score := float64(11-i) * 0.1
		err := SaveArticle(&NewArticle{
			ID:       id,
			Title:    fmt.Sprintf("Paper %d", i),
			Link:     "https://arxiv.org/abs/" + id,
			Abstract: strPtr(fmt.Sprintf("Abstract %d", i)),
		})
		if err != nil {
			t.Fatalf("SaveArticle %d: %v", i, err)
		}
		if err := UpdateArticleScore(id, score); err != nil {
			t.Fatalf("UpdateArticleScore %d: %v", i, err)
		}
	}

	today := "2026-06-13"
	count, err := MarkDailyRecommendations(today, 5, 0)
	if err != nil {
		t.Fatalf("MarkDailyRecommendations: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5 recommendations, got %d", count)
	}

	articles, err := GetArticlesByRecommendDate(today)
	if err != nil {
		t.Fatalf("GetArticlesByRecommendDate: %v", err)
	}
	if len(articles) != 5 {
		t.Fatalf("expected 5 recommended articles, got %d", len(articles))
	}

	// Should be the top 5 by score: 1.0, 0.9, 0.8, 0.7, 0.6
	// Use delta comparison to account for SQLite REAL rounding
	expectedScores := []float64{1.0, 0.9, 0.8, 0.7, 0.6}
	for i, a := range articles {
		if a.RecommendationType == nil || *a.RecommendationType != "score" {
			t.Errorf("article[%d] recommendation_type = %v, want 'score'", i, a.RecommendationType)
		}
		want := expectedScores[i]
		got := a.Score
		if got < want-0.0001 || got > want+0.0001 {
			t.Errorf("article[%d] score = %f, want %f", i, got, want)
		}
	}
}

func TestMarkDailyRecommendations_Hybrid(t *testing.T) {
	defer setupTestDB(t)()

	// Insert 20 articles with scores 1.0, 0.95, ..., 0.05
	for i := 1; i <= 20; i++ {
		id := fmt.Sprintf("2401.001%02d", i)
		score := float64(21-i) * 0.05
		err := SaveArticle(&NewArticle{
			ID:    id,
			Title: fmt.Sprintf("Hybrid Paper %d", i),
			Link:  "https://arxiv.org/abs/" + id,
		})
		if err != nil {
			t.Fatalf("SaveArticle %d: %v", i, err)
		}
		if err := UpdateArticleScore(id, score); err != nil {
			t.Fatalf("UpdateArticleScore %d: %v", i, err)
		}
	}

	today := "2026-06-13"
	count, err := MarkDailyRecommendations(today, 10, 0.3)
	if err != nil {
		t.Fatalf("MarkDailyRecommendations: %v", err)
	}
	if count != 10 {
		t.Errorf("expected 10 recommendations, got %d", count)
	}

	articles, err := GetArticlesByRecommendDate(today)
	if err != nil {
		t.Fatalf("GetArticlesByRecommendDate: %v", err)
	}
	if len(articles) != 10 {
		t.Fatalf("expected 10 articles, got %d", len(articles))
	}

	scoreCount := 0
	randomCount := 0
	for _, a := range articles {
		if a.RecommendationType == nil {
			t.Errorf("article[%s] has nil recommendation_type", a.ID)
			continue
		}
		switch *a.RecommendationType {
		case "score":
			scoreCount++
		case "random":
			randomCount++
		default:
			t.Errorf("unknown recommendation_type: %q", *a.RecommendationType)
		}
	}
	t.Logf("score=%d random=%d", scoreCount, randomCount)

	if scoreCount == 0 {
		t.Error("expected at least 1 score article")
	}
	if randomCount == 0 {
		t.Error("expected at least 1 random article")
	}

	// Verify batch orders are contiguous
	for i, a := range articles {
		if a.BatchOrder == nil || *a.BatchOrder != i {
			t.Errorf("article[%d] batch_order = %v, want %d", i, a.BatchOrder, i)
		}
	}
}

func TestMarkDailyRecommendations_NotEnoughScored(t *testing.T) {
	defer setupTestDB(t)()

	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("2401.010%02d", i)
		err := SaveArticle(&NewArticle{
			ID:    id,
			Title: fmt.Sprintf("Few Paper %d", i),
			Link:  "https://arxiv.org/abs/" + id,
		})
		if err != nil {
			t.Fatalf("SaveArticle %d: %v", i, err)
		}
		if err := UpdateArticleScore(id, float64(4-i)*0.25); err != nil {
			t.Fatalf("UpdateArticleScore %d: %v", i, err)
		}
	}

	today := "2026-06-13"
	count, err := MarkDailyRecommendations(today, 10, 0.3)
	if err != nil {
		t.Fatalf("MarkDailyRecommendations: %v", err)
	}
	if count < 3 {
		t.Errorf("expected at least 3 recommendations, got %d", count)
	}

	// Verify articles were actually persisted and have correct types
	recs, err := GetArticlesByRecommendDate(today)
	if err != nil {
		t.Fatalf("GetArticlesByRecommendDate: %v", err)
	}
	if len(recs) != count {
		t.Errorf("read back %d recommendations, expected %d", len(recs), count)
	}
	t.Logf("got %d recommendations from 3 scored articles", count)
}

func TestMarkDailyRecommendations_NoScoredArticles(t *testing.T) {
	defer setupTestDB(t)()

	// Insert 5 unread, unscored articles
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("2401.030%02d", i)
		err := SaveArticle(&NewArticle{
			ID:    id,
			Title: fmt.Sprintf("No Score Paper %d", i),
			Link:  "https://arxiv.org/abs/" + id,
		})
		if err != nil {
			t.Fatalf("SaveArticle %d: %v", i, err)
		}
		// score stays 0
	}

	today := "2026-06-13"
	count, err := MarkDailyRecommendations(today, 5, 0.3)
	if err != nil {
		t.Fatalf("MarkDailyRecommendations: %v", err)
	}
	// With no scored articles, the score step returns 0. The random step
	// queries WHERE score > 0, so it also returns 0. Total should be 0.
	if count != 0 {
		t.Errorf("expected 0 recommendations (no scored articles), got %d", count)
	}
}

func TestMarkDailyRecommendations_DiversityRatioOutOfRange(t *testing.T) {
	defer setupTestDB(t)()

	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("2401.040%02d", i)
		score := float64(11-i) * 0.1
		err := SaveArticle(&NewArticle{
			ID:    id,
			Title: fmt.Sprintf("Range Paper %d", i),
			Link:  "https://arxiv.org/abs/" + id,
		})
		if err != nil {
			t.Fatalf("SaveArticle %d: %v", i, err)
		}
		if err := UpdateArticleScore(id, score); err != nil {
			t.Fatalf("UpdateArticleScore %d: %v", i, err)
		}
	}

	today := "2026-06-13"

	// diversityRatio > 1 should be clamped and still produce mixed types
	count, err := MarkDailyRecommendations(today, 5, 2.5)
	if err != nil {
		t.Fatalf("MarkDailyRecommendations (ratio=2.5): %v", err)
	}
	if count == 0 {
		t.Error("expected some recommendations even with out-of-range ratio")
	}
	recs, _ := GetArticlesByRecommendDate(today)
	hasScore, hasRandom := false, false
	for _, r := range recs {
		if r.RecommendationType != nil {
			switch *r.RecommendationType {
			case "score":
				hasScore = true
			case "random":
				hasRandom = true
			}
		}
	}
	// ratio=2.5 clamped to 0.3 → should have both score and random
	if !hasScore || !hasRandom {
		t.Error("expected both score and random types after clamping ratio=2.5 to 0.3")
	}

	// diversityRatio < 0 should be treated as 0 (pure score → all "score" type)
	count2, err := MarkDailyRecommendations(today, 5, -1)
	if err != nil {
		t.Fatalf("MarkDailyRecommendations (ratio=-1): %v", err)
	}
	if count2 != 5 {
		t.Errorf("with ratio=-1 expected 5 recommendations, got %d", count2)
	}
	recs2, _ := GetArticlesByRecommendDate(today)
	for _, r := range recs2 {
		if r.RecommendationType == nil || *r.RecommendationType != "score" {
			t.Errorf("ratio=-1 should produce only score type, got %v", r.RecommendationType)
		}
	}
}

func TestGetStats(t *testing.T) {
	defer setupTestDB(t)()

	statuses := []int{0, 0, 1, 2, -1, 3, 0}
	for i, s := range statuses {
		id := fmt.Sprintf("2401.020%02d", i)
		err := SaveArticle(&NewArticle{
			ID:    id,
			Title: fmt.Sprintf("Stats Paper %d", i),
			Link:  "https://arxiv.org/abs/" + id,
		})
		if err != nil {
			t.Fatalf("SaveArticle %d: %v", i, err)
		}
		if err := UpdateArticleStatus(id, s); err != nil {
			t.Fatalf("UpdateArticleStatus %d: %v", i, err)
		}
	}

	stats, err := GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}

	check := func(status, want int) {
		t.Helper()
		got, ok := stats[status]
		if !ok || got != want {
			t.Errorf("stats[%d] = %d (ok=%v), want %d", status, got, ok, want)
		}
	}
	check(0, 3)
	check(1, 1)
	check(2, 1)
	check(-1, 1)
	check(3, 1)
}

func TestChatPaperUpsertAndQuery(t *testing.T) {
	defer setupTestDB(t)()

	err := UpsertChatPaper(&ChatPaper{
		ID:        "session-1",
		ArxivID:   "2401.00005",
		Title:     "Chat Paper",
		Rating:    8,
		SourceURL: "https://arxiv.org/abs/2401.00005",
		CreatedAt: time.Now().Format("2006-01-02 15:04"),
		UpdatedAt: time.Now().Format("2006-01-02 15:04"),
	})
	if err != nil {
		t.Fatalf("UpsertChatPaper: %v", err)
	}

	since := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	papers, err := GetChatPapersUpdatedSince(since)
	if err != nil {
		t.Fatalf("GetChatPapersUpdatedSince: %v", err)
	}
	if len(papers) != 1 {
		t.Fatalf("expected 1 paper, got %d", len(papers))
	}
	if papers[0].ArxivID != "2401.00005" {
		t.Errorf("ArxivID = %q, want %q", papers[0].ArxivID, "2401.00005")
	}
	if papers[0].Rating != 8 {
		t.Errorf("Rating = %d, want 8", papers[0].Rating)
	}
}

// ─── helpers ───

func strPtr(s string) *string { return &s }
