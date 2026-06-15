package recommend

import (
	"testing"
)

func TestParseScoringResponse_Simple(t *testing.T) {
	raw := `[{"id":"2401.00001","score":0.85},{"id":"2401.00002","score":0.42}]`
	scores := parseScoringResponse(raw)
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
	if scores["2401.00001"] != 0.85 {
		t.Errorf("score for 2401.00001 = %f, want 0.85", scores["2401.00001"])
	}
	if scores["2401.00002"] != 0.42 {
		t.Errorf("score for 2401.00002 = %f, want 0.42", scores["2401.00002"])
	}
}

func TestParseScoringResponse_WithCodeFence(t *testing.T) {
	raw := "```json\n[{\"id\":\"2401.00001\",\"score\":0.95}]\n```"
	scores := parseScoringResponse(raw)
	if len(scores) != 1 {
		t.Fatalf("expected 1 score, got %d", len(scores))
	}
	if scores["2401.00001"] != 0.95 {
		t.Errorf("score = %f, want 0.95", scores["2401.00001"])
	}
}

func TestParseScoringResponse_ClampBounds(t *testing.T) {
	raw := `[{"id":"a","score":1.5},{"id":"b","score":-0.5}]`
	scores := parseScoringResponse(raw)
	if scores["a"] > 1.0 || scores["a"] < 0 {
		t.Errorf("score a=%f not clamped to [0,1]", scores["a"])
	}
	if scores["b"] > 1.0 || scores["b"] < 0 {
		t.Errorf("score b=%f not clamped to [0,1]", scores["b"])
	}
}

func TestParseScoringResponse_Empty(t *testing.T) {
	scores := parseScoringResponse("")
	if len(scores) != 0 {
		t.Errorf("expected empty map, got %d items", len(scores))
	}

	scores = parseScoringResponse("[]")
	if len(scores) != 0 {
		t.Errorf("expected empty map from empty array, got %d items", len(scores))
	}
}

func TestParseScoringResponse_InvalidJSON(t *testing.T) {
	// Invalid JSON should return nil (not crash)
	scores := parseScoringResponse("not json at all")
	if scores != nil {
		t.Errorf("expected nil for invalid JSON, got %v", scores)
	}
}

func TestScoreArticlesBatch_EmptyArticles(t *testing.T) {
	// Should handle empty input gracefully
	scores, err := ScoreArticlesBatch(nil, "", "user prefs", nil, 10, nil)
	if err != nil {
		t.Fatalf("ScoreArticlesBatch with nil articles: %v", err)
	}
	if len(scores) != 0 {
		t.Errorf("expected 0 scores, got %d", len(scores))
	}

	scores, err = ScoreArticlesBatch(nil, "", "user prefs", []ArticleInfo{}, 10, nil)
	if err != nil {
		t.Fatalf("ScoreArticlesBatch with empty articles: %v", err)
	}
	if len(scores) != 0 {
		t.Errorf("expected 0 scores, got %d", len(scores))
	}
}

func TestParseScoringResponse_EmptyID(t *testing.T) {
	// Articles with empty IDs should be skipped
	raw := `[{"id":"","score":0.5},{"id":"valid","score":0.8}]`
	scores := parseScoringResponse(raw)
	if _, ok := scores[""]; ok {
		t.Error("empty ID should not be in results")
	}
	if scores["valid"] != 0.8 {
		t.Errorf("valid score = %f, want 0.8", scores["valid"])
	}
}
