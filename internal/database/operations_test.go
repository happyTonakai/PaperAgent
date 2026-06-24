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

func TestUpsertChatPaperAbstract(t *testing.T) {
	defer setupTestDB(t)()

	const arxivID = "2401.00002"
	const first = "Abstract from Q&A paper."
	const second = "Updated abstract."

	if err := UpsertChatPaperAbstract(arxivID, first); err != nil {
		t.Fatalf("UpsertChatPaperAbstract (insert): %v", err)
	}
	got, err := GetChatPaperAbstract(arxivID)
	if err != nil {
		t.Fatalf("GetChatPaperAbstract: %v", err)
	}
	if got != first {
		t.Errorf("abstract after insert = %q, want %q", got, first)
	}

	// Upsert with the same id should replace the abstract.
	if err := UpsertChatPaperAbstract(arxivID, second); err != nil {
		t.Fatalf("UpsertChatPaperAbstract (update): %v", err)
	}
	got, err = GetChatPaperAbstract(arxivID)
	if err != nil {
		t.Fatalf("GetChatPaperAbstract (update): %v", err)
	}
	if got != second {
		t.Errorf("abstract after update = %q, want %q", got, second)
	}
}

// TestChatPaperAbstractStaysOutOfArticles guards against the historical bug
// where Q&A paper upserts landed in the `articles` table and got picked up
// by the daily-recommendation pipeline. The dedicated chat_paper_abstracts
// table must be the only place these abstracts live.
func TestChatPaperAbstractStaysOutOfArticles(t *testing.T) {
	defer setupTestDB(t)()

	const arxivID = "2401.00042"
	if err := UpsertChatPaperAbstract(arxivID, "Q&A abstract that must not bleed into articles"); err != nil {
		t.Fatalf("UpsertChatPaperAbstract: %v", err)
	}

	exists, err := ArticleExists(arxivID)
	if err != nil {
		t.Fatalf("ArticleExists: %v", err)
	}
	if exists {
		t.Errorf("Q&A abstract upsert leaked into articles table for %s", arxivID)
	}

	got, err := GetChatPaperAbstract(arxivID)
	if err != nil {
		t.Fatalf("GetChatPaperAbstract: %v", err)
	}
	if got == "" {
		t.Errorf("expected cached abstract, got empty string")
	}
}

func TestGetChatPaperAbstractMissing(t *testing.T) {
	defer setupTestDB(t)()

	got, err := GetChatPaperAbstract("0000.00000")
	if err != nil {
		t.Fatalf("GetChatPaperAbstract on missing id: %v", err)
	}
	if got != "" {
		t.Errorf("missing id should return empty string, got %q", got)
	}

	// Empty arxiv id is a no-op and must not error.
	got, err = GetChatPaperAbstract("")
	if err != nil {
		t.Fatalf("GetChatPaperAbstract on empty id: %v", err)
	}
	if got != "" {
		t.Errorf("empty arxiv id should return empty string, got %q", got)
	}
}

func TestUpsertChatPaperAbstractEmptyInput(t *testing.T) {
	defer setupTestDB(t)()

	// Empty abstract should be a no-op (not store an empty row).
	if err := UpsertChatPaperAbstract("2401.00099", ""); err != nil {
		t.Fatalf("UpsertChatPaperAbstract empty abstract: %v", err)
	}
	got, err := GetChatPaperAbstract("2401.00099")
	if err != nil {
		t.Fatalf("GetChatPaperAbstract: %v", err)
	}
	if got != "" {
		t.Errorf("empty abstract must not be stored, got %q", got)
	}
}

// TestUpsertChatPaperRoundTripGithubURL ensures the github_url field (added in
// schema v7) survives both insert and update via UpsertChatPaper, and is
// correctly returned by GetChatPapersUpdatedSince.
func TestUpsertChatPaperRoundTripGithubURL(t *testing.T) {
	defer setupTestDB(t)()

	now := time.Now().Format("2006-01-02 15:04")
	p := &ChatPaper{
		ID:        "session-1",
		ArxivID:   "2401.00100",
		Title:     "Paper with GitHub repo",
		Rating:    4,
		SourceURL: "https://arxiv.org/abs/2401.00100",
		CreatedAt: now,
		UpdatedAt: now,
		GitHubURL: "https://github.com/owner/repo",
	}
	if err := UpsertChatPaper(p); err != nil {
		t.Fatalf("UpsertChatPaper: %v", err)
	}

	// Update with new github_url + rating.
	p.Rating = 5
	p.GitHubURL = "https://github.com/owner/repo2"
	if err := UpsertChatPaper(p); err != nil {
		t.Fatalf("UpsertChatPaper (update): %v", err)
	}

	got, err := GetChatPapersUpdatedSince("2000-01-01")
	if err != nil {
		t.Fatalf("GetChatPapersUpdatedSince: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 chat_paper, got %d", len(got))
	}
	if got[0].GitHubURL != "https://github.com/owner/repo2" {
		t.Errorf("GitHubURL = %q, want %q", got[0].GitHubURL, "https://github.com/owner/repo2")
	}
	if got[0].Rating != 5 {
		t.Errorf("Rating = %d, want 5", got[0].Rating)
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

	// Insert 5 unread, unscored articles (score stays 0 — e.g. when
	// preferences are empty and LLM scoring was skipped).
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
	// With score >= 0 (instead of > 0), both the score step (3 newest by
	// created_at DESC) and the random step (2 from the remaining 2) pick
	// articles. All 5 should be recommended.
	if count != 5 {
		t.Errorf("expected 5 recommendations (fallback to all unread), got %d", count)
	}

	recs, err := GetArticlesByRecommendDate(today)
	if err != nil {
		t.Fatalf("GetArticlesByRecommendDate: %v", err)
	}
	if len(recs) != 5 {
		t.Fatalf("expected 5 recommended articles, got %d", len(recs))
	}

	// All 5 should be tagged (either 'score' or 'random').
	scoreCount, randomCount := 0, 0
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
			t.Errorf("unknown recommendation_type: %q", *r.RecommendationType)
		}
	}
	if scoreCount == 0 {
		t.Error("expected at least 1 score-type rec (newest by created_at)")
	}
	if randomCount == 0 {
		t.Error("expected at least 1 random-type rec from remaining")
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

// ─── pushed_at: pending backlog & mark-pushed ────────────────────────

// seedPushedAtArticles inserts a configurable list of articles with explicit
// recommend_date / batch_order / pushed_at values. Used to control the test
// fixture precisely (rather than running the full MarkDailyRecommendations
// pipeline).
func seedPushedAtArticles(t *testing.T, recs []struct {
	id, recDate string
	batchOrder  int
	pushedAt    *string
}) {
	t.Helper()
	db, err := GetDB()
	if err != nil {
		t.Fatalf("GetDB: %v", err)
	}
	for _, r := range recs {
		var pushed interface{}
		if r.pushedAt != nil {
			pushed = *r.pushedAt
		}
		_, err := db.Exec(
			`INSERT INTO articles (id, title, link, recommend_date, batch_order, pushed_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			r.id, "t-"+r.id, "https://arxiv.org/abs/"+r.id, r.recDate, r.batchOrder, pushed,
		)
		if err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}
}

func TestGetUnpushedArticles_FiltersAndOrders(t *testing.T) {
	defer setupTestDB(t)()

	pushedYesterday := "2026-06-15 08:00:00"
	seedPushedAtArticles(t, []struct {
		id, recDate string
		batchOrder  int
		pushedAt    *string
	}{
		{"a1", "2026-06-14", 0, nil},                          // pending, oldest
		{"a2", "2026-06-14", 1, nil},                          // pending, same date later
		{"a3", "2026-06-14", 0, &pushedYesterday},             // already pushed, must be excluded
		{"a4", "2026-06-15", 0, nil},                          // pending, newer date
		{"a5", "2026-06-15", 0, nil},                          // pending, same date
		{"a6", "2026-06-15", 1, &pushedYesterday},             // already pushed, newer date
		{"a7", "2026-06-13", 0, nil},                          // pending, oldest of all
		{"a8", "2026-06-15", 0, nil},                          // no recommend_date
		// a8 has recommend_date=nil so should be excluded by the WHERE clause.
	})
	// a8 must be excluded by the WHERE clause (recommend_date IS NOT NULL),
	// so clear it after seeding. Fail the test loudly if setup didn't
	// take — silent failures here would mask off-by-one in fixture shape.
	db, err := GetDB()
	if err != nil {
		t.Fatalf("GetDB: %v", err)
	}
	if _, err := db.Exec(`UPDATE articles SET recommend_date = NULL WHERE id = 'a8'`); err != nil {
		t.Fatalf("clear a8.recommend_date: %v", err)
	}

	got, err := GetUnpushedArticles(100)
	if err != nil {
		t.Fatalf("GetUnpushedArticles: %v", err)
	}

	wantIDs := []string{"a7", "a1", "a2", "a4", "a5"}
	if len(got) != len(wantIDs) {
		t.Fatalf("len(got) = %d, want %d (got IDs: %v)", len(got), len(wantIDs), idsOf(got))
	}
	for i, w := range wantIDs {
		if got[i].ID != w {
			t.Errorf("got[%d].ID = %q, want %q (full: %v)", i, got[i].ID, w, idsOf(got))
		}
	}
}

func TestGetUnpushedArticles_LimitsResult(t *testing.T) {
	defer setupTestDB(t)()
	seedPushedAtArticles(t, []struct {
		id, recDate string
		batchOrder  int
		pushedAt    *string
	}{
		{"a1", "2026-06-14", 0, nil},
		{"a2", "2026-06-15", 0, nil},
		{"a3", "2026-06-16", 0, nil},
	})
	got, err := GetUnpushedArticles(2)
	if err != nil {
		t.Fatalf("GetUnpushedArticles: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestMarkArticlesPushed_UpdatesAndPreservesExisting(t *testing.T) {
	defer setupTestDB(t)()
	oldPush := "2026-06-10 08:00:00"
	seedPushedAtArticles(t, []struct {
		id, recDate string
		batchOrder  int
		pushedAt    *string
	}{
		{"a1", "2026-06-14", 0, nil},
		{"a2", "2026-06-15", 0, &oldPush}, // already pushed
		{"a3", "2026-06-16", 0, nil},
	})

	ts := "2026-06-17 08:00:00"
	if err := MarkArticlesPushed([]string{"a1", "a3"}, ts); err != nil {
		t.Fatalf("MarkArticlesPushed: %v", err)
	}

	a1, _ := GetArticleByID("a1")
	if a1 == nil || a1.PushedAt == nil || *a1.PushedAt != ts {
		t.Errorf("a1.PushedAt = %v, want %s", a1.PushedAt, ts)
	}
	a2, _ := GetArticleByID("a2")
	if a2 == nil || a2.PushedAt == nil || *a2.PushedAt != oldPush {
		t.Errorf("a2.PushedAt = %v, want %s (preserved)", a2.PushedAt, oldPush)
	}
	a3, _ := GetArticleByID("a3")
	if a3 == nil || a3.PushedAt == nil || *a3.PushedAt != ts {
		t.Errorf("a3.PushedAt = %v, want %s", a3.PushedAt, ts)
	}
}

func TestMarkArticlesPushed_EmptyIsNoop(t *testing.T) {
	defer setupTestDB(t)()
	if err := MarkArticlesPushed(nil, "2026-06-17 08:00:00"); err != nil {
		t.Errorf("nil ids should be no-op, got: %v", err)
	}
	if err := MarkArticlesPushed([]string{}, "2026-06-17 08:00:00"); err != nil {
		t.Errorf("empty ids should be no-op, got: %v", err)
	}
}

func TestMarkArticlesPushed_RejectsTooManyIDs(t *testing.T) {
	defer setupTestDB(t)()
	ids := make([]string, MaxINClauseIDs+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("a%d", i)
	}
	err := MarkArticlesPushed(ids, "2026-06-17 08:00:00")
	if err == nil {
		t.Fatal("expected error when len(ids) > MaxINClauseIDs")
	}
}

func TestLastPushAt_ReturnsMostRecent(t *testing.T) {
	defer setupTestDB(t)()
	seedPushedAtArticles(t, []struct {
		id, recDate string
		batchOrder  int
		pushedAt    *string
	}{
		{"a1", "2026-06-14", 0, strPtr("2026-06-15 08:00:00")},
		{"a2", "2026-06-15", 0, strPtr("2026-06-20 08:00:00")},
		{"a3", "2026-06-16", 0, strPtr("2026-06-18 08:00:00")},
		{"a4", "2026-06-17", 0, nil}, // not pushed
	})
	got, err := LastPushAt()
	if err != nil {
		t.Fatalf("LastPushAt: %v", err)
	}
	if got != "2026-06-20 08:00:00" {
		t.Errorf("LastPushAt = %q, want 2026-06-20 08:00:00", got)
	}
}

func TestLastPushAt_EmptyWhenNoPushes(t *testing.T) {
	defer setupTestDB(t)()
	got, err := LastPushAt()
	if err != nil {
		t.Fatalf("LastPushAt: %v", err)
	}
	if got != "" {
		t.Errorf("LastPushAt = %q, want empty", got)
	}
}

func TestCountUnpushedArticles(t *testing.T) {
	defer setupTestDB(t)()
	seedPushedAtArticles(t, []struct {
		id, recDate string
		batchOrder  int
		pushedAt    *string
	}{
		{"a1", "2026-06-14", 0, nil},
		{"a2", "2026-06-15", 0, strPtr("2026-06-20 08:00:00")},
		{"a3", "2026-06-16", 0, nil},
	})
	n, err := CountUnpushedArticles()
	if err != nil {
		t.Fatalf("CountUnpushedArticles: %v", err)
	}
	if n != 2 {
		t.Errorf("CountUnpushedArticles = %d, want 2", n)
	}
}

func idsOf(articles []Article) []string {
	out := make([]string, len(articles))
	for i, a := range articles {
		out[i] = a.ID
	}
	return out
}
