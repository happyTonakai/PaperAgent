//go:build e2e

// End-to-end test for the recommendation pipeline. Runs the *real*
// FetchAndRecommend() production path against the real SQLite database
// (~/.config/paperagent/zenflow.db) and the real LLM configured in
// config.yaml. Only the RSS fetch is short-circuited via
// PAPER_RECOMMEND_RSS_FILE to a local XML fixture, so the test stays
// offline.
//
// Run with:
//
//	PAPER_RECOMMEND_RSS_FILE=$PWD/testdata/arxiv_cs.SD.rss.xml \
//	  go test -tags=e2e -v -count=1 -run TestE2E_RealFetchAndRecommend \
//	  ./internal/recommend/
//
// This test will spend real LLM tokens (≈3 scoring batches of 10
// abstracts by default). Re-running will re-score new unscored articles
// and re-mark today's daily recommendations.
package recommend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/happyTonakai/paperagent/internal/api"
	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/database"
)

func TestE2E_RealFetchAndRecommend(t *testing.T) {
	// --- 1. Short-circuit RSS fetch to local fixture ---
	const fixtureRel = "../../testdata/arxiv_cs.SD.rss.xml"
	rssPath, err := filepath.Abs(fixtureRel)
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	t.Setenv(fetchCategoryRSSFileEnv, rssPath)
	t.Logf("RSS bypass file: %s", rssPath)

	// --- 2a. Seed a stub preferences file so the scoring step actually runs. ---
	// On a fresh machine ~/.config/paperagent/preferences.md does not exist
	// and GenerateDailyRecommendations skips scoring entirely. We back up
	// whatever is there (if anything) and restore it on cleanup so the
	// test never permanently mutates the user's preferences.
	prefPath := PreferencesPath()
	origPrefs, readErr := os.ReadFile(prefPath)
	hadOrigPrefs := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read existing preferences: %v", readErr)
	}
	stubPrefs := "User is interested in speech synthesis, audio generation, " +
		"speaker identification, music information retrieval, and other " +
		"audio/speech machine-learning topics. Prefer novel, technically " +
		"rigorous work over incremental benchmark-tweaks."
	if err := os.WriteFile(prefPath, []byte(stubPrefs), 0644); err != nil {
		t.Fatalf("write stub preferences: %v", err)
	}
	t.Cleanup(func() {
		if hadOrigPrefs {
			_ = os.WriteFile(prefPath, origPrefs, 0644)
		} else {
			_ = os.Remove(prefPath)
		}
	})
	t.Logf("seeded stub preferences at: %s", prefPath)

	// --- 2b. Open the real production SQLite database ---
	// database.Open() lazily creates ~/.config/paperagent/zenflow.db and
	// runs migrations. SetDB makes GetDB() return it without going
	// through sync.Once, so this test is isolated from any in-process
	// state.
	conn, err := database.Open()
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	database.SetDB(conn)
	t.Cleanup(func() {
		database.SetDB(nil)
		_ = database.Close()
	})
	t.Logf("using DB: %s", database.DBPath())

	// --- 3. Build the real scoring client from config.yaml ---
	// Mirrors server.scoringClient() (handlers_recommend.go): prefer the
	// dedicated scoring endpoint, fall back to the main API only if the
	// scoring block is missing/incomplete.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	ep := cfg.API.Scoring
	if ep == nil || ep.BaseURL == "" || ep.APIKey == "" || ep.Model == "" {
		t.Logf("no dedicated scoring endpoint in config.yaml, falling back to main API")
		ep = &config.APIEndpoint{
			BaseURL: cfg.API.BaseURL,
			APIKey:  cfg.API.APIKey,
			Model:   cfg.API.DefaultModel,
		}
	}
	if ep.BaseURL == "" || ep.APIKey == "" || ep.Model == "" {
		t.Fatal("config.yaml has no usable API endpoint — e2e test needs a real LLM")
	}
	scoring := &ScoringClient{
		Client: api.NewClientFromEndpoint(ep.BaseURL, ep.APIKey, ep.Model),
		Model:  ep.Model,
	}
	t.Logf("scoring endpoint: %s model=%s", ep.BaseURL, ep.Model)

	// --- 4. Run the real FetchAndRecommend pipeline ---
	categories := []string{"cs.SD"}
	if len(cfg.ArxivCategories) > 0 {
		// Use whatever the user has configured, but keep cs.SD in
		// there so the local XML actually matches one of the calls.
		categories = append(categories, "cs.SD")
	}
	recs, err := FetchAndRecommend(
		categories,
		scoring,
		cfg.Recommend.DailyPapers,
		cfg.Recommend.ScoringBatchSize,
		cfg.Recommend.DiversityRatio,
	)
	if err != nil {
		t.Fatalf("FetchAndRecommend: %v", err)
	}

	// --- 5. Report the daily recommendations ---
	t.Logf("===== %d daily recommendations =====", len(recs))
	for i, a := range recs {
		rt := "<nil>"
		if a.RecommendationType != nil {
			rt = *a.RecommendationType
		}
		bo := -1
		if a.BatchOrder != nil {
			bo = *a.BatchOrder
		}
		t.Logf("  [%d] %s score=%.3f type=%s batch=%d — %s", i, a.ID, a.Score, rt, bo, a.Title)
	}
	if len(recs) == 0 {
		t.Error("expected at least 1 recommendation")
	}
}
