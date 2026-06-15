package feishu

import (
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
