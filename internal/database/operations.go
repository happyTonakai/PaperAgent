package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Article represents a paper in the recommendation system.
type Article struct {
	ID                 string  `json:"id"`
	Title              string  `json:"title"`
	Link               string  `json:"link"`
	Abstract           *string `json:"abstract"`
	Status             int     `json:"status"`
	Score              float64 `json:"score"`
	Author             *string `json:"author"`
	Category           *string `json:"category"`
	HFUpvotes          *int    `json:"hf_upvotes"`
	AXNetVotes         *int    `json:"ax_net_votes"`
	VotesUpdatedAt     *string `json:"votes_updated_at"`
	Comment            *string `json:"comment"`
	RecommendDate      *string `json:"recommend_date"`
	BatchOrder         *int    `json:"batch_order"`
	TranslatedTitle    *string `json:"translated_title,omitempty"`
	TranslatedAbstract *string `json:"translated_abstract,omitempty"`
	RecommendationType *string `json:"recommendation_type,omitempty"`
	CreatedAt          string  `json:"created_at"`
	// PushedAt records when this article was last pushed to the user via
	// Feishu. NULL means it has never been pushed and is still in the
	// pending backlog. The holiday-skip feature relies on this: if push
	// is skipped on a holiday, articles stay NULL until a later workday
	// drains them.
	PushedAt *string `json:"pushed_at,omitempty"`
}

// NewArticle is the data needed to insert a new article from RSS.
type NewArticle struct {
	ID       string
	Title    string
	Link     string
	Abstract *string
	Author   *string
	Category *string
}

const articleCols = `id, title, link, abstract, status, score, author, category,
	hf_upvotes, ax_net_votes, votes_updated_at, comment,
	recommend_date, batch_order, translated_title, translated_abstract,
	recommendation_type, created_at, pushed_at`

func scanArticle(scanner interface {
	Scan(dest ...interface{}) error
}) (Article, error) {
	var a Article
	err := scanner.Scan(
		&a.ID, &a.Title, &a.Link, &a.Abstract,
		&a.Status, &a.Score, &a.Author, &a.Category,
		&a.HFUpvotes, &a.AXNetVotes, &a.VotesUpdatedAt,
		&a.Comment, &a.RecommendDate, &a.BatchOrder,
		&a.TranslatedTitle, &a.TranslatedAbstract,
		&a.RecommendationType, &a.CreatedAt, &a.PushedAt,
	)
	return a, err
}

// SaveArticle inserts a new article, skipping if it already exists.
func SaveArticle(a *NewArticle) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		`INSERT OR IGNORE INTO articles (id, title, link, abstract, author, category)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		a.ID, a.Title, a.Link, a.Abstract, a.Author, a.Category,
	)
	return err
}

// SaveArticles batch-inserts articles, skipping duplicates.
func SaveArticles(articles []NewArticle) (int, error) {
	db, err := GetDB()
	if err != nil {
		return 0, err
	}
	inserted := 0
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT OR IGNORE INTO articles (id, title, link, abstract, author, category)
		 VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for _, a := range articles {
		res, err := stmt.Exec(a.ID, a.Title, a.Link, a.Abstract, a.Author, a.Category)
		if err != nil {
			return inserted, err
		}
		n, _ := res.RowsAffected()
		inserted += int(n)
	}
	if err := tx.Commit(); err != nil {
		return inserted, err
	}
	return inserted, nil
}

// UpsertArticleByArxivID inserts or updates an article identified by its arXiv ID.
// Used when a paper is created via the Q&A system to ensure its abstract
// is available for preference updates.
func UpsertArticleByArxivID(id, title, link string, abstract *string) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		`INSERT INTO articles (id, title, link, abstract, created_at)
		 VALUES (?, ?, ?, ?, datetime('now'))
		 ON CONFLICT(id) DO UPDATE SET
		   title = COALESCE(NULLIF(?,''), title),
		   link  = COALESCE(NULLIF(?,''), link),
		   abstract = COALESCE(?, abstract)`,
		id, title, link, abstract,
		title, link, abstract,
	)
	return err
}

// GetArticles returns articles filtered by optional status, with limit and offset.
func GetArticles(status *int, limit, offset int) ([]Article, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	var rows *sql.Rows
	if status != nil {
		rows, err = db.Query(
			`SELECT `+articleCols+` FROM articles WHERE status = ? ORDER BY score DESC, created_at DESC LIMIT ? OFFSET ?`,
			*status, limit, offset,
		)
	} else {
		rows, err = db.Query(
			`SELECT `+articleCols+` FROM articles ORDER BY score DESC, created_at DESC LIMIT ? OFFSET ?`,
			limit, offset,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// GetRecommendedArticles returns scored unread articles ordered by score DESC.
func GetRecommendedArticles(limit int) ([]Article, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT `+articleCols+` FROM articles
		 WHERE score > 0 AND status = 0
		 ORDER BY score DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// GetUnscoredArticles returns articles that have score=0 and are unread (status=0).
func GetUnscoredArticles(limit int) ([]Article, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT `+articleCols+` FROM articles
		 WHERE score = 0 AND status = 0
		 ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// UpdateArticleScore updates the LLM score for a single article.
func UpdateArticleScore(id string, score float64) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE articles SET score = ? WHERE id = ?", score, id)
	return err
}

// UpdateArticleScores batch-updates scores for multiple articles.
func UpdateArticleScores(scores map[string]float64) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("UPDATE articles SET score = ? WHERE id = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for id, score := range scores {
		if _, err := stmt.Exec(score, id); err != nil {
			return fmt.Errorf("update score for %s: %w", id, err)
		}
	}
	return tx.Commit()
}

// UpdateArticleTranslations saves translated title and abstract for an article.
func UpdateArticleTranslations(id string, translatedTitle, translatedAbstract string) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	var titlePtr, abstractPtr *string
	if translatedTitle != "" {
		titlePtr = &translatedTitle
	}
	if translatedAbstract != "" {
		abstractPtr = &translatedAbstract
	}
	_, err = db.Exec(
		"UPDATE articles SET translated_title = ?, translated_abstract = ? WHERE id = ?",
		titlePtr, abstractPtr, id,
	)
	return err
}

// UpdateArticleStatus updates the status for an article.
func UpdateArticleStatus(id string, status int) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE articles SET status = ? WHERE id = ?", status, id)
	return err
}

// BatchUpdateArticleStatus updates the status for many articles in one
// statement. Empty IDs is a no-op. Caller is responsible for capping
// len(ids) to avoid unbounded IN clauses.
func BatchUpdateArticleStatus(ids []string, status int) error {
	if len(ids) == 0 {
		return nil
	}
	db, err := GetDB()
	if err != nil {
		return fmt.Errorf("batch update article status: %w", err)
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = strings.TrimSuffix(placeholders, ",")
	args := make([]any, len(ids)+1)
	args[0] = status
	for i, id := range ids {
		args[i+1] = id
	}
	_, err = db.Exec("UPDATE articles SET status = ? WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return fmt.Errorf("batch update article status (n=%d): %w", len(ids), err)
	}
	return nil
}

// UpdateArticleComment updates the user comment for an article.
func UpdateArticleComment(id string, comment string) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	var val *string
	if comment != "" {
		val = &comment
	}
	_, err = db.Exec("UPDATE articles SET comment = ? WHERE id = ?", val, id)
	return err
}

// UpdateArticleVotes updates community vote data for an article.
func UpdateArticleVotes(id string, hfUpvotes, axNetVotes *int) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		`UPDATE articles SET hf_upvotes = ?, ax_net_votes = ?, votes_updated_at = datetime('now')
		 WHERE id = ?`,
		hfUpvotes, axNetVotes, id,
	)
	return err
}

// GetArticlesNeedingVotes returns article IDs that need vote data fetched.
func GetArticlesNeedingVotes(limit int) ([]string, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT id FROM articles
		 WHERE votes_updated_at IS NULL
		   AND created_at > datetime('now', '-30 days')
		 ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkDailyRecommendations assigns a recommend_date batch using a hybrid strategy:
//   - (1 - diversityRatio) * count  top-scored articles
//   - diversityRatio * count        random articles from the remaining scored pool
// Each article gets a recommendation_type tag ("score" or "random").
func MarkDailyRecommendations(date string, count int, diversityRatio float64) (int, error) {
	if diversityRatio < 0 {
		diversityRatio = 0
	}
	if diversityRatio > 1 {
		diversityRatio = 0.3
	}

	db, err := GetDB()
	if err != nil {
		return 0, err
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Clear previous recommendations for this date
	if _, err := tx.Exec(
		"UPDATE articles SET recommend_date = NULL, batch_order = NULL, recommendation_type = NULL WHERE recommend_date = ?",
		date,
	); err != nil {
		return 0, err
	}

	// Calculate counts
	scoreCount := int(float64(count) * (1 - diversityRatio))
	randomCount := count - scoreCount
	if scoreCount < 0 {
		scoreCount = 0
	}
	if randomCount < 0 {
		randomCount = 0
	}

	total := 0

	// Step 1: Top-scored articles
	if scoreCount > 0 {
		// score >= 0 (not > 0) so that unscored articles (score = 0, e.g. when
		// preferences are empty) still get picked. created_at DESC is the
		// tiebreaker for all-zero scores — newest unread first.
		rows, err := tx.Query(
			`SELECT id FROM articles
			 WHERE status = 0 AND score >= 0
			 ORDER BY score DESC, created_at DESC LIMIT ?`,
			scoreCount,
		)
		if err != nil {
			return 0, err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}

		stmt, err := tx.Prepare("UPDATE articles SET recommend_date = ?, batch_order = ?, recommendation_type = 'score' WHERE id = ?")
		if err != nil {
			return 0, err
		}

		for i, id := range ids {
			if _, err := stmt.Exec(date, total+i, id); err != nil {
				stmt.Close()
				return 0, fmt.Errorf("tag score %s: %w", id, err)
			}
		}
		stmt.Close()
		total += len(ids)

		// Reduce randomCount if we got fewer score articles than requested
		if len(ids) < scoreCount {
			extraRandom := scoreCount - len(ids)
			randomCount += extraRandom
		}
	}

	// Step 2: Random exploration from remaining scored articles
	if randomCount > 0 {
		// score >= 0 (not > 0) to include unscored articles in random pool
		// when preferences are empty.
		rows, err := tx.Query(
			`SELECT id FROM articles
			 WHERE status = 0 AND score >= 0 AND recommend_date IS NULL
			 ORDER BY RANDOM() LIMIT ?`,
			randomCount,
		)
		if err != nil {
			return 0, err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}

		stmt, err := tx.Prepare("UPDATE articles SET recommend_date = ?, batch_order = ?, recommendation_type = 'random' WHERE id = ?")
		if err != nil {
			return 0, err
		}

		for i, id := range ids {
			if _, err := stmt.Exec(date, total+i, id); err != nil {
				stmt.Close()
				return 0, fmt.Errorf("tag random %s: %w", id, err)
			}
		}
		stmt.Close()
		total += len(ids)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return total, nil
}

// GetArticlesByRecommendDate returns articles recommended on a specific date.
func GetArticlesByRecommendDate(date string) ([]Article, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT `+articleCols+` FROM articles
		 WHERE recommend_date = ? ORDER BY batch_order ASC`,
		date,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// GetRecommendationDates returns all dates that have recommended articles, newest first.
func GetRecommendationDates() ([]string, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT DISTINCT recommend_date FROM articles
		 WHERE recommend_date IS NOT NULL ORDER BY recommend_date DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dates []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		dates = append(dates, d)
	}
	return dates, rows.Err()
}

// GetUnpushedArticles returns articles that have been assigned a
// recommend_date but never pushed to the user (pushed_at IS NULL). They are
// ordered by recommend_date ASC, batch_order ASC so that older backlogs
// drain first when a push finally goes out.
//
// `limit` caps the result; callers that want to drain everything should
// loop until len(returned) < limit.
func GetUnpushedArticles(limit int) ([]Article, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT `+articleCols+` FROM articles
		 WHERE pushed_at IS NULL AND recommend_date IS NOT NULL
		 ORDER BY recommend_date ASC, batch_order ASC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// MaxINClauseIDs caps the number of IDs in a single MarkArticlesPushed
// call. SQLite's default SQLITE_MAX_VARIABLE_NUMBER is 999; we stay under
// 900 to leave headroom for other parameters. RunPush drains the backlog
// in batches of 500 to stay well under this limit.
const MaxINClauseIDs = 900

// MarkArticlesPushed sets pushed_at = ts for the given article IDs.
// An empty IDs slice is a no-op. `ts` should be a SQLite-friendly timestamp
// (e.g. time.Now().Format("2006-01-02 15:04:05")).
func MarkArticlesPushed(ids []string, ts string) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > MaxINClauseIDs {
		return fmt.Errorf("mark articles pushed: too many ids %d (max %d)", len(ids), MaxINClauseIDs)
	}
	db, err := GetDB()
	if err != nil {
		return fmt.Errorf("mark articles pushed: %w", err)
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = strings.TrimSuffix(placeholders, ",")
	args := make([]any, len(ids)+1)
	args[0] = ts
	for i, id := range ids {
		args[i+1] = id
	}
	_, err = db.Exec("UPDATE articles SET pushed_at = ? WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return fmt.Errorf("mark articles pushed (n=%d): %w", len(ids), err)
	}
	return nil
}

// LastPushAt returns the most recent pushed_at across all articles, or an
// empty string if nothing has been pushed yet.
func LastPushAt() (string, error) {
	db, err := GetDB()
	if err != nil {
		return "", err
	}
	var ts *string
	err = db.QueryRow("SELECT MAX(pushed_at) FROM articles WHERE pushed_at IS NOT NULL").Scan(&ts)
	if err != nil {
		return "", err
	}
	if ts == nil {
		return "", nil
	}
	return *ts, nil
}

// CountUnpushedArticles returns the number of articles that have a
// recommend_date but have not been pushed yet.
func CountUnpushedArticles() (int, error) {
	db, err := GetDB()
	if err != nil {
		return 0, err
	}
	var n int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM articles WHERE pushed_at IS NULL AND recommend_date IS NOT NULL`,
	).Scan(&n)
	return n, err
}

// GetStats returns counts of articles by status.
func GetStats() (map[int]int, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query("SELECT status, COUNT(*) FROM articles GROUP BY status")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[int]int)
	for rows.Next() {
		var status, count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		stats[status] = count
	}
	return stats, rows.Err()
}

// GetArticleByID returns a single article by its ID.
func GetArticleByID(id string) (*Article, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	row := db.QueryRow(`SELECT `+articleCols+` FROM articles WHERE id = ?`, id)
	a, err := scanArticle(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

// ArticleExists checks if an article with the given ID already exists.
func ArticleExists(id string) (bool, error) {
	db, err := GetDB()
	if err != nil {
		return false, err
	}
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM articles WHERE id = ?", id).Scan(&count)
	return count > 0, err
}

// GetExistingIDs returns the set of article IDs that already exist in the database.
func GetExistingIDs(ids []string) (map[string]bool, error) {
	if len(ids) == 0 {
		return map[string]bool{}, nil
	}
	db, err := GetDB()
	if err != nil {
		return nil, err
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := "SELECT id FROM articles WHERE id IN (" + strings.Join(placeholders, ",") + ")"
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	existing := make(map[string]bool, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		existing[id] = true
	}
	return existing, rows.Err()
}

// ClearAllData deletes all articles.
func ClearAllData() error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	_, err = db.Exec("DELETE FROM articles")
	return err
}

// ── chat_papers (Q&A metadata for preference updates) ──

// ChatPaper represents Q&A paper metadata stored in SQLite for preference aggregation.
type ChatPaper struct {
	ID        string `json:"id"`
	ArxivID   string `json:"arxiv_id"`
	Title     string `json:"title"`
	Rating    int    `json:"rating"`
	SourceURL string `json:"source_url"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// UpsertChatPaper inserts or updates a chat_paper record.
func UpsertChatPaper(p *ChatPaper) error {
	db, err := GetDB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		`INSERT OR REPLACE INTO chat_papers (id, arxiv_id, title, rating, source_url, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.ArxivID, p.Title, p.Rating, p.SourceURL, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

// GetChatPapersUpdatedSince returns chat papers with updated_at >= sinceDate.
func GetChatPapersUpdatedSince(sinceDate string) ([]ChatPaper, error) {
	db, err := GetDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT id, arxiv_id, title, rating, source_url, created_at, updated_at
		 FROM chat_papers WHERE updated_at >= ? AND arxiv_id IS NOT NULL AND arxiv_id != ''
		 ORDER BY updated_at DESC`,
		sinceDate,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var papers []ChatPaper
	for rows.Next() {
		var p ChatPaper
		if err := rows.Scan(&p.ID, &p.ArxivID, &p.Title, &p.Rating, &p.SourceURL, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		papers = append(papers, p)
	}
	return papers, rows.Err()
}

// ChatPaperCount returns the number of chat_paper records.
func ChatPaperCount() (int, error) {
	db, err := GetDB()
	if err != nil {
		return 0, err
	}
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM chat_papers").Scan(&count)
	return count, err
}

// MigrateChatPapersFromJSON scans the JSON papers directory and imports metadata into chat_papers.
func MigrateChatPapersFromJSON(jsonDir string) (int, error) {
	imported := 0
	entries, err := os.ReadDir(jsonDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(jsonDir, e.Name()))
		if err != nil {
			continue
		}

		var raw struct {
			SessionID string `json:"session_id"`
			Title     string `json:"title"`
			ArxivID   string `json:"arxiv_id"`
			Rating    int    `json:"rating"`
			SourceURL string `json:"source_url"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}
		if raw.ArxivID == "" {
			continue
		}

		id := raw.SessionID
		if id == "" {
			id = strings.TrimSuffix(e.Name(), ".json")
		}

		p := &ChatPaper{
			ID:        id,
			ArxivID:   raw.ArxivID,
			Title:     raw.Title,
			Rating:    raw.Rating,
			SourceURL: raw.SourceURL,
			CreatedAt: raw.CreatedAt,
			UpdatedAt: raw.UpdatedAt,
		}
		if p.Title == "" {
			p.Title = "Paper " + id
		}
		if p.CreatedAt == "" {
			p.CreatedAt = time.Now().Format("2006-01-02 15:04")
		}
		if p.UpdatedAt == "" {
			p.UpdatedAt = p.CreatedAt
		}
		if err := UpsertChatPaper(p); err != nil {
			continue
		}
		imported++
	}
	return imported, nil
}
