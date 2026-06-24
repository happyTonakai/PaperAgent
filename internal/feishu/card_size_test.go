package feishu

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestDailyCardSize_DefaultFitsChinese verifies that 10 articles with
// realistic Chinese-translated abstracts (the common case) fit in one card
// at full length.
func TestDailyCardSize_DefaultFitsChinese(t *testing.T) {
	// Typical translated-abstract length: ~500 Chinese characters.
	cnAbstract := strings.Repeat("本文提出了一种新的多模态推理方法。", 20) // ~500 chars
	items := make([]RecommendCardItem, 10)
	votes := 42
	for i := range items {
		items[i] = RecommendCardItem{
			ID:         fmt.Sprintf("2401.%05dv1", i+1),
			Title:      fmt.Sprintf("第 %d 篇：一种新的多模态推理方法", i+1),
			Abstract:   cnAbstract,
			Score:      0.876,
			AXNetVotes: &votes,
		}
	}
	cardJSON := buildDailyRecommendCard(items, 1, 2)
	if len(cardJSON) > maxCardJSONBytes {
		t.Errorf("Chinese abstracts should fit: got %dB, limit %dB", len(cardJSON), maxCardJSONBytes)
	}
	t.Logf("Chinese 10×~500chars: %dB (limit %dB)", len(cardJSON), maxCardJSONBytes)
}

// TestDailyCardSize_LongAbstractFallback verifies that when abstracts are
// unusually long (e.g. 1500 English chars), the card automatically falls
// back to truncated abstracts and still fits.
func TestDailyCardSize_LongAbstractFallback(t *testing.T) {
	longAbstract := strings.Repeat("This paper proposes a novel approach. ", 100) // ~1500 chars
	items := make([]RecommendCardItem, 10)
	votes := 42
	for i := range items {
		items[i] = RecommendCardItem{
			ID:         fmt.Sprintf("2401.%05dv1", i+1),
			Title:      fmt.Sprintf("Long-abstract paper %d", i+1),
			Abstract:   longAbstract,
			Score:      0.876,
			AXNetVotes: &votes,
		}
	}
	cardJSON := buildDailyRecommendCard(items, 1, 2)
	if len(cardJSON) > maxCardJSONBytes {
		t.Errorf("Fallback should have truncated: got %dB, limit %dB", len(cardJSON), maxCardJSONBytes)
	}
	// Sanity: every abstract in the rendered card should now end with "..."
	// (since each was > 500 runes and got truncated).
	if !strings.Contains(cardJSON, "...") {
		t.Errorf("Expected truncated abstracts to contain \"...\" in JSON")
	}
	t.Logf("Long 10×~1500chars fallback: %dB (limit %dB)", len(cardJSON), maxCardJSONBytes)
}

// TestDailyCardSize_TableLimitFallback reproduces a real failure seen in
// the wild: an arXiv abstract that contained a markdown table (rendered
// from a LaTeX tabular) caused the card to exceed Feishu's 5-table hard
// limit (error 11310), so the whole page send failed.
//
// The card builder must detect this and truncate the offending abstracts
// so the send succeeds.
func TestDailyCardSize_TableLimitFallback(t *testing.T) {
	// 6 of 10 articles have abstracts with 1 markdown table each.
	// 6 tables > maxCardMdTables=5 → must be auto-truncated to fit.
	tableRow := func(cells ...string) string {
		return "| " + strings.Join(cells, " | ") + " |"
	}
	absWithTable := strings.Join([]string{
		"This paper presents a benchmark comparison.",
		"",
		tableRow("Model", "F1", "EM"),
		tableRow("BERT", "82.1", "78.4"),
		tableRow("RoBERTa", "84.7", "81.0"),
	}, "\n")
	plainAbs := "This paper proposes a novel approach to the problem of X."

	items := make([]RecommendCardItem, 10)
	votes := 42
	for i := range items {
		if i < 6 {
			items[i] = RecommendCardItem{
				ID:         fmt.Sprintf("2401.%05dv1", i+1),
				Title:      fmt.Sprintf("Paper with table %d", i+1),
				Abstract:   absWithTable,
				Score:      0.876,
				AXNetVotes: &votes,
			}
		} else {
			items[i] = RecommendCardItem{
				ID:         fmt.Sprintf("2401.%05dv1", i+1),
				Title:      fmt.Sprintf("Plain paper %d", i+1),
				Abstract:   plainAbs,
				Score:      0.876,
				AXNetVotes: &votes,
			}
		}
	}
	cardJSON := buildDailyRecommendCard(items, 1, 2)

	// Count actual tables in the rendered JSON content. Re-marshal the
	// card and walk the body.elements[*].text.content fields to find them.
	if got := countTablesInCardJSON(cardJSON); got > maxCardMdTables {
		t.Errorf("card still has %d tables after build (limit %d), would be rejected by Feishu", got, maxCardMdTables)
	}
	if len(cardJSON) > maxCardJSONBytes {
		t.Errorf("card JSON exceeds limit after fallback: %dB > %dB", len(cardJSON), maxCardJSONBytes)
	}
	// At least the 6 table-containing abstracts should now carry the
	// "_(table omitted)_" placeholder so the surrounding prose is preserved.
	if got := strings.Count(cardJSON, "_(table omitted)_"); got < 6 {
		t.Errorf("expected at least 6 table-omitted placeholders, got %d", got)
	}
	t.Logf("10×abstracts(6 with tables) fallback: %dB, %d tables in output (limit %d), %d placeholders",
		len(cardJSON), countTablesInCardJSON(cardJSON), maxCardMdTables,
		strings.Count(cardJSON, "_(table omitted)_"))
}

// countTablesInCardJSON extracts the concatenated markdown content of a
// daily recommend card JSON and counts tables in it. Used by table-limit
// tests to verify the rendered card respects Feishu's 5-table hard limit.
func countTablesInCardJSON(cardJSON string) int {
	var card map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		return 0
	}
	body, _ := card["body"].(map[string]any)
	elems, _ := body["elements"].([]any)
	var buf strings.Builder
	for _, e := range elems {
		em, _ := e.(map[string]any)
		text, _ := em["text"].(map[string]any)
		content, _ := text["content"].(string)
		if content != "" {
			buf.WriteString(content)
			buf.WriteString("\n")
		}
	}
	return countMdTables(buf.String())
}
