package recommend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArxivRSS_FromFile(t *testing.T) {
	// Load the local test RSS file
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "arxiv_cs.SD.rss.xml"))
	if err != nil {
		t.Skipf("testdata not available: %v", err)
	}

	articles, err := parseArxivRSS(data, "cs.SD")
	if err != nil {
		t.Fatalf("parseArxivRSS: %v", err)
	}

	if len(articles) == 0 {
		t.Fatal("expected at least 1 article, got 0")
	}
	t.Logf("parsed %d articles from cs.SD RSS", len(articles))

	// Verify first article has all required fields
	a := articles[0]
	if a.ID == "" {
		t.Error("first article has empty ID")
	}
	if a.Title == "" {
		t.Error("first article has empty Title")
	}
	if a.Link == "" {
		t.Error("first article has empty Link")
	}
	if a.Abstract == nil || *a.Abstract == "" {
		t.Error("first article has empty Abstract")
	}
	if a.Category == nil || *a.Category == "" {
		t.Error("first article has empty Category")
	}

	// Check ID format (arxiv ID like 2606.12495)
	if !strings.Contains(a.ID, ".") {
		t.Errorf("article ID %q doesn't look like an arXiv ID", a.ID)
	}

	// Check Link format
	if !strings.Contains(a.Link, "arxiv.org/abs/") {
		t.Errorf("article Link %q doesn't look like an arXiv URL", a.Link)
	}

	t.Logf("first article: ID=%s Title=%q", a.ID, a.Title)
	if a.Author != nil {
		t.Logf("  author: %s", *a.Author)
	}
	if a.Abstract != nil {
		abs := *a.Abstract
		if len(abs) > 100 {
			abs = abs[:100] + "..."
		}
		t.Logf("  abstract: %s", abs)
	}
}

func TestParseArxivRSS_MultipleCategories(t *testing.T) {
	// Some articles in cs.SD may have cross-listings (e.g. cs.SD, cs.AI, cs.LG)
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "arxiv_cs.SD.rss.xml"))
	if err != nil {
		t.Skipf("testdata not available: %v", err)
	}

	articles, err := parseArxivRSS(data, "cs.SD")
	if err != nil {
		t.Fatalf("parseArxivRSS: %v", err)
	}

	// All articles should have ID and Title
	for i, a := range articles {
		if a.ID == "" {
			t.Errorf("article[%d] has empty ID", i)
		}
		if a.Title == "" {
			t.Errorf("article[%d] has empty Title", i)
		}
	}
}

func TestExtractArxivID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://arxiv.org/abs/2606.12495", "2606.12495"},
		{"https://arxiv.org/abs/2606.12495v1", "2606.12495"},
		{"http://arxiv.org/abs/2301.00001", "2301.00001"},
		{"2606.12495", "2606.12495"},
		{"2606.12495v2", "2606.12495"},
		{"https://arxiv.org/pdf/2606.12495.pdf", "2606.12495"},
		{"not an id", ""},
		{"", ""},
	}

	for _, tt := range tests {
		result := extractArxivID(tt.input)
		if result != tt.expected {
			t.Errorf("extractArxivID(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}


