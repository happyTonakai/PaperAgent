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

func TestParseArxivRSS_FiltersReplace(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "arxiv_cs.SD.rss.xml"))
	if err != nil {
		t.Skipf("testdata not available: %v", err)
	}

	articles, err := parseArxivRSS(data, "cs.SD")
	if err != nil {
		t.Fatalf("parseArxivRSS: %v", err)
	}

	// testdata has 23 items: 8 new + 8 cross + 7 replace. Replace must be
	// dropped, leaving 16.
	if len(articles) != 16 {
		t.Errorf("expected 16 articles (new+cross), got %d", len(articles))
	}

	// Every kept article's abstract must not start with the arXiv metadata
	// line or the "Abstract:" prefix.
	for i, a := range articles {
		if a.Abstract == nil {
			t.Errorf("article[%d] (%s) has nil Abstract", i, a.ID)
			continue
		}
		abs := *a.Abstract
		if strings.HasPrefix(abs, "arXiv:") {
			t.Errorf("article[%d] (%s) abstract still has metadata prefix: %q", i, a.ID, abs[:min(60, len(abs))])
		}
		if strings.HasPrefix(abs, "Abstract:") {
			t.Errorf("article[%d] (%s) abstract still has 'Abstract:' prefix: %q", i, a.ID, abs[:min(60, len(abs))])
		}
	}
}

func TestParseArxivDescription(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		wantAbstract     string
		wantAnnounceType string
	}{
		{
			name:             "new announcement",
			input:            "arXiv:2606.12495v1 Announce Type: new \nAbstract: Hello world.",
			wantAbstract:     "Hello world.",
			wantAnnounceType: "new",
		},
		{
			name:             "cross announcement",
			input:            "arXiv:2606.12555v1 Announce Type: cross \nAbstract: Some text.",
			wantAbstract:     "Some text.",
			wantAnnounceType: "cross",
		},
		{
			name:             "replace announcement",
			input:            "arXiv:2606.12555v1 Announce Type: replace \nAbstract: Old text.",
			wantAbstract:     "Old text.",
			wantAnnounceType: "replace",
		},
		{
			name:             "no body, metadata only",
			input:            "arXiv:2606.12555v1 Announce Type: new",
			wantAbstract:     "",
			wantAnnounceType: "new",
		},
		{
			name:             "empty input",
			input:            "",
			wantAbstract:     "",
			wantAnnounceType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			abs, at := parseArxivDescription(tt.input)
			if abs != tt.wantAbstract {
				t.Errorf("abstract = %q, want %q", abs, tt.wantAbstract)
			}
			if at != tt.wantAnnounceType {
				t.Errorf("announceType = %q, want %q", at, tt.wantAnnounceType)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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


