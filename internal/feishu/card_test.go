package feishu

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/session"
)

// loadLatestPaper loads the most recently modified paper from the user's papers directory.
func loadLatestPaper(t *testing.T) *session.Paper {
	t.Helper()
	dir := config.PapersDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var newest string
	var newestMod int64
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		mod := info.ModTime().Unix()
		if mod > newestMod {
			newestMod = mod
			newest = filepath.Join(dir, e.Name())
		}
	}
	if newest == "" {
		t.Fatal("no paper JSON found")
	}
	data, err := os.ReadFile(newest)
	if err != nil {
		t.Fatalf("read %s: %v", newest, err)
	}
	var p session.Paper
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal %s: %v", newest, err)
	}
	t.Logf("loaded paper: %s (summary=%d chars)", p.Title, len(p.InitialSummary))
	return &p
}

func TestMultiCardSplit_RealPaper(t *testing.T) {
	paper := loadLatestPaper(t)
	if paper.InitialSummary == "" {
		t.Skip("paper has no initial summary")
	}

	content := paper.InitialSummary
	title := paper.Title
	if title == "" {
		title = "Test Paper"
	}

	// Run the same split logic as the streaming code
	type slot struct {
		id      string
		startAt int
	}
	var slots []slot
	slots = append(slots, slot{id: "card_0", startAt: 0})

	var total strings.Builder
	lastPatch := 0

	// Feed content in random-sized chunks (50-500 chars) to simulate streaming
	rng := rand.New(rand.NewPCG(42, 7))
	remaining := content
	for len(remaining) > 0 {
		chunkSize := 50 + rng.IntN(450)
		if chunkSize > len(remaining) {
			chunkSize = len(remaining)
		}
		total.WriteString(remaining[:chunkSize])
		remaining = remaining[chunkSize:]
		totalStr := total.String()

		if len(totalStr)-lastPatch < 200 {
			continue
		}
		lastPatch = len(totalStr)

		active := &slots[len(slots)-1]
		cardContent := totalStr[active.startAt:]
		isFirst := len(slots) == 1

		fits, overflow := fitMarkdownContent(cardContent, func(c string) string {
			if isFirst {
				return buildStreamingCard("test", title, c)
			}
			return buildStreamingContinuationCard(c)
		})

		if overflow != "" {
			t.Logf("split at card #%d: fits=%dB overflow=%dB", len(slots), len(fits), len(overflow))

			overflowStart := active.startAt + len(fits)
			slots = append(slots, slot{id: fmt.Sprintf("card_%d", len(slots)), startAt: overflowStart})
		} else {
			_ = fits
		}
	}

	// Finalize: check each card
	totalStr := total.String()
	t.Logf("total content: %d chars across %d cards", len(totalStr), len(slots))

	// Verify no content loss
	reconstructed := ""
	for i, s := range slots {
		end := len(totalStr)
		if i+1 < len(slots) {
			end = slots[i+1].startAt
		}
		cardContent := totalStr[s.startAt:end]
		reconstructed += cardContent

		// Verify each card fits within limits
		var cardJSON string
		if i == 0 {
			cardJSON = buildDoneCard("test", title, cardContent, 0, 0, 0)
		} else {
			cardJSON = buildContinuationCard(cardContent)
		}
		jsonSize := len(cardJSON)
		elements := estimateContentElements(cardContent)
		oversize := ""
		if jsonSize > maxCardJSONBytes {
			oversize = fmt.Sprintf(" ⚠️ JSON %dB > %dB", jsonSize, maxCardJSONBytes)
			t.Errorf("card #%d JSON exceeds limit: %dB > %dB", i, jsonSize, maxCardJSONBytes)
		}
		if elements > maxCardElements {
			oversize += fmt.Sprintf(" ⚠️ elements %d > %d", elements, maxCardElements)
			t.Errorf("card #%d elements %d > %d", i, elements, maxCardElements)
		}
		t.Logf("  card #%d: content=%dB json=%dB elements=%d%s", i, len(cardContent), jsonSize, elements, oversize)
	}

	if reconstructed != totalStr {
		t.Fatalf("content reconstruction mismatch: got %d chars, want %d", len(reconstructed), len(totalStr))
	}
	t.Logf("✅ all %d cards within limits, content fully preserved (%d chars)", len(slots), len(totalStr))
}

func TestMultiCardSplit_LargeContent(t *testing.T) {
	// Generate a very long summary with lots of tables to stress test
	var sb strings.Builder
	sb.WriteString("# Very Long Summary\n\n")

	// Many markdown tables (each generates many internal Feishu elements)
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&sb, "## Table %d\n\n", i+1)
		sb.WriteString("| Method | Param (M) | Acc (%) | F1 | Speed |\n")
		sb.WriteString("|--------|-----------|---------|-----|-------|\n")
		for j := 0; j < 10; j++ {
			fmt.Fprintf(&sb, "| Model-%d | %.1f | %2.1f | %.3f | fast |\n", j, float64(j)*5.2, float64(j)*3.7+85.0, float64(j)*0.02+0.88)
		}
		sb.WriteString("\n")
	}

	// Code blocks — should not be split
	sb.WriteString("\n## Key Implementation\n\n")
	sb.WriteString("```python\n")
	sb.WriteString("def compute(x):\n")
	sb.WriteString("    # This is a very long code block that should never be split\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&sb, "    result = layer_%d.forward(x)  # step %d\n", i, i)
	}
	sb.WriteString("    return x\n")
	sb.WriteString("```\n")

	// More paragraphs
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&sb, "This is paragraph number %d. It contains enough text to make the summary really long and force multiple card splits during streaming. ", i+1)
	}
	sb.WriteString("\n\n## Conclusion\n\n")
	sb.WriteString("This is the conclusion of the paper. It summarizes all the findings and future work.\n")

	content := sb.String()
	t.Logf("generated %d chars of test content", len(content))

	// Run the same multi-card split
	type slot struct {
		startAt int
	}
	var slots []slot
	slots = append(slots, slot{startAt: 0})

	var total strings.Builder
	lastPatch := 0

	rng := rand.New(rand.NewPCG(42, 7))
	remaining := content
	for len(remaining) > 0 {
		chunkSize := 50 + rng.IntN(450)
		if chunkSize > len(remaining) {
			chunkSize = len(remaining)
		}
		total.WriteString(remaining[:chunkSize])
		remaining = remaining[chunkSize:]
		totalStr := total.String()

		if len(totalStr)-lastPatch < 200 {
			continue
		}
		lastPatch = len(totalStr)

		active := &slots[len(slots)-1]
		cardContent := totalStr[active.startAt:]
		isFirst := len(slots) == 1

		fits, overflow := fitMarkdownContent(cardContent, func(c string) string {
			if isFirst {
				return buildStreamingCard("test", "Stress Test", c)
			}
			return buildStreamingContinuationCard(c)
		})

		if overflow != "" {
			overflowStart := active.startAt + len(fits)
			slots = append(slots, slot{startAt: overflowStart})
		} else {
			_ = fits
		}
	}

	totalStr := total.String()
	t.Logf("total: %d chars across %d cards", len(totalStr), len(slots))

	// Verify each card JSON is within limits & content is preserved
	reconstructed := ""
	for i, s := range slots {
		end := len(totalStr)
		if i+1 < len(slots) {
			end = slots[i+1].startAt
		}
		cardContent := totalStr[s.startAt:end]
		reconstructed += cardContent

		var cardJSON string
		if i == 0 {
			cardJSON = buildDoneCard("test", "Stress Test", cardContent, 0, 0, 0)
		} else {
			cardJSON = buildContinuationCard(cardContent)
		}
		jsonSize := len(cardJSON)
		elements := estimateContentElements(cardContent)
		oversize := ""
		if jsonSize > maxCardJSONBytes {
			oversize = fmt.Sprintf(" ⚠️ JSON %dB > %dB", jsonSize, maxCardJSONBytes)
			t.Errorf("card #%d JSON exceeds limit: %dB > %dB", i, jsonSize, maxCardJSONBytes)
		}
		if elements > maxCardElements {
			oversize += fmt.Sprintf(" ⚠️ elements %d > %d", elements, maxCardElements)
			t.Errorf("card #%d elements %d > %d", i, elements, maxCardElements)
		}
		t.Logf("  card #%d: content=%dB json=%dB elements=%d%s", i, len(cardContent), jsonSize, elements, oversize)
	}

	if reconstructed != totalStr {
		t.Fatal("content loss detected")
	}
	t.Logf("✅ all %d cards within limits, %d chars preserved", len(slots), len(totalStr))
}

func TestFitMarkdownContent_SafetyMargin(t *testing.T) {
	// Verify that fitMarkdownContent correctly truncates to stay under limit
	longText := strings.Repeat("这是一段中文测试文本。", 5000)

	fits, overflow := fitMarkdownContent(longText, func(c string) string {
		return buildCardMarkdown(c)
	})

	if overflow == "" {
		t.Log("content fits in single card")
	} else {
		cardJSON := buildCardMarkdown(fits)
		t.Logf("fits=%dB overflow=%dB cardJSON=%dB (limit=%dB)",
			len(fits), len(overflow), len(cardJSON), maxCardJSONBytes)
		if len(cardJSON) > maxCardJSONBytes {
			t.Errorf("card JSON %dB exceeds limit %dB", len(cardJSON), maxCardJSONBytes)
		}
	}
}

func TestMultiCardSplit_Chat(t *testing.T) {
	// Test the chat streaming card split logic
	paperID := "test-paper-123"
	title := "Test Paper Title"

	// Generate a long answer
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&sb, "Finding number %d: The model demonstrates significant improvements in benchmark evaluations. ", i+1)
	}
	answer := sb.String()

	type slot struct {
		startAt int
	}
	var slots []slot
	slots = append(slots, slot{startAt: 0})

	var total strings.Builder
	lastPatch := 0

	rng := rand.New(rand.NewPCG(42, 7))
	remaining := answer
	for len(remaining) > 0 {
		chunkSize := 50 + rng.IntN(450)
		if chunkSize > len(remaining) {
			chunkSize = len(remaining)
		}
		total.WriteString(remaining[:chunkSize])
		remaining = remaining[chunkSize:]
		totalStr := total.String()

		if len(totalStr)-lastPatch < 200 {
			continue
		}
		lastPatch = len(totalStr)

		active := &slots[len(slots)-1]
		cardContent := totalStr[active.startAt:]
		isFirst := len(slots) == 1

		fits, overflow := fitMarkdownContent(cardContent, func(c string) string {
			if isFirst {
				return buildChatStreamingCard(paperID, title, c)
			}
			return buildChatStreamingContinuationCard(c)
		})

		if overflow != "" {
			overflowStart := active.startAt + len(fits)
			slots = append(slots, slot{startAt: overflowStart})
		} else {
			_ = fits
		}
	}

	totalStr := total.String()
	t.Logf("chat answer: %d chars across %d cards", len(totalStr), len(slots))

	reconstructed := ""
	for i, s := range slots {
		end := len(totalStr)
		if i+1 < len(slots) {
			end = slots[i+1].startAt
		}
		cardContent := totalStr[s.startAt:end]
		reconstructed += cardContent

		var cardJSON string
		if i == 0 {
			cardJSON = buildChatDoneCard(paperID, title, cardContent, 0, 0, 0, 0)
		} else {
			cardJSON = buildChatStreamingContinuationCard(cardContent)
		}
		jsonSize := len(cardJSON)
		elements := estimateContentElements(cardContent)
		oversize := ""
		if jsonSize > maxCardJSONBytes {
			oversize = fmt.Sprintf(" ⚠️ JSON %dB > %dB", jsonSize, maxCardJSONBytes)
			t.Errorf("card #%d JSON exceeds limit: %dB > %dB", i, jsonSize, maxCardJSONBytes)
		}
		if elements > maxCardElements {
			oversize += fmt.Sprintf(" ⚠️ elements %d > %d", elements, maxCardElements)
			t.Errorf("card #%d elements %d > %d", i, elements, maxCardElements)
		}
		t.Logf("  card #%d: content=%dB json=%dB elements=%d%s", i, len(cardContent), jsonSize, elements, oversize)
	}

	if reconstructed != totalStr {
		t.Fatal("content loss detected in chat")
	}
	t.Logf("✅ chat: all %d cards within limits", len(slots))
}

func TestFitMarkdownContent_NoMidTableSplit(t *testing.T) {
	// Content with exactly 5 tables that triggers table limit split.
	// Verify that no table is split mid-table across cards.
	var sb strings.Builder
	sb.WriteString("## Introduction\n\nSome text before tables.\n\n")

	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&sb, "**表%d：测试表格\n\n", i)
		sb.WriteString("| Col A | Col B | Col C |\n")
		sb.WriteString("|-------|-------|-------|\n")
		for j := 0; j < 7; j++ {
			fmt.Fprintf(&sb, "| row-%d | value-%d | data-%d |\n", j, j, j)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Conclusion\n\nSome final text.\n")

	content := sb.String()
	t.Logf("content: %d chars with 5 tables", len(content))

	// Run the same streaming simulation
	type slot struct {
		startAt int
	}
	var slots []slot
	slots = append(slots, slot{startAt: 0})

	var total strings.Builder
	lastPatch := 0

	rng := rand.New(rand.NewPCG(42, 7))
	remaining := content
	for len(remaining) > 0 {
		chunkSize := 50 + rng.IntN(450)
		if chunkSize > len(remaining) {
			chunkSize = len(remaining)
		}
		total.WriteString(remaining[:chunkSize])
		remaining = remaining[chunkSize:]
		totalStr := total.String()

		if len(totalStr)-lastPatch < 200 {
			continue
		}
		lastPatch = len(totalStr)

		active := &slots[len(slots)-1]
		cardContent := totalStr[active.startAt:]

		fits, overflow := fitMarkdownContent(cardContent, func(c string) string {
			if len(slots) == 1 {
				return buildStreamingCard("test", "Table Split Test", c)
			}
			return buildStreamingContinuationCard(c)
		})

		if overflow != "" {
			overflowStart := active.startAt + len(fits)
			slots = append(slots, slot{startAt: overflowStart})
		}
	}

	totalStr := total.String()
	t.Logf("total: %d chars across %d cards", len(totalStr), len(slots))

	// Verify: no card contains a partial table
	for i, s := range slots {
		end := len(totalStr)
		if i+1 < len(slots) {
			end = slots[i+1].startAt
		}
		cardContent := totalStr[s.startAt:end]

		// A partial table would start with |...| but not end with |...| (or vice versa on next card)
		lines := strings.Split(cardContent, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) > 1 && trimmed[0] == '|' && trimmed[len(trimmed)-1] != '|' {
				t.Errorf("card #%d has truncated table row: %q", i, trimmed)
			}
		}

		// Check card fits its limits
		var cardJSON string
		if i == 0 {
			cardJSON = buildDoneCard("test", "Table Split Test", cardContent, 0, 0, 0)
		} else {
			cardJSON = buildContinuationCard(cardContent)
		}
		t.Logf("  card #%d: content=%dB json=%dB tables=%d", i, len(cardContent), len(cardJSON), countMdTables(cardContent))
		if len(cardJSON) > maxCardJSONBytes {
			t.Errorf("card #%d JSON exceeds limit: %dB > %dB", i, len(cardJSON), maxCardJSONBytes)
		}
		if countMdTables(cardContent) > maxCardMdTables {
			t.Errorf("card #%d has %d tables, limit is %d", i, countMdTables(cardContent), maxCardMdTables)
		}
	}

	t.Logf("✅ no tables split across cards")
}

// TestRenderRecommendButton covers the visual states a daily-recommendation
// action button can be in: default (white/grey) vs colored (post-click).
// The button text is kept identical to the inactive state; the type change
// (primary/danger) is the only visual cue. We deliberately avoid
// disabled: true because the Feishu renderer turns disabled buttons grey
// regardless of type, which would hide the colored background.
func TestRenderRecommendButton(t *testing.T) {
	tests := []struct {
		name       string
		emoji      string
		articleID  string
		action     string
		active     bool
		activeType string
		wantText   string
		wantType   string
	}{
		{
			name: "inactive like", emoji: "👍", articleID: "2401.00001", action: "recommend:like",
			wantText: "👍", wantType: "default",
		},
		{
			name: "active like (primary)", emoji: "👍", articleID: "2401.00001", action: "recommend:like",
			active: true, activeType: "primary",
			wantText: "👍", wantType: "primary",
		},
		{
			name: "active dislike (danger)", emoji: "👎", articleID: "2401.00002", action: "recommend:dislike",
			active: true, activeType: "danger",
			wantText: "👎", wantType: "danger",
		},
		{
			name: "active activate (primary)", emoji: "🤖", articleID: "2401.00003", action: "recommend:activate",
			active: true, activeType: "primary",
			wantText: "🤖", wantType: "primary",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			btn := renderRecommendButton(tt.emoji, tt.articleID, tt.action, tt.active, tt.activeType)
			if got := btn["text"].(map[string]any)["content"]; got != tt.wantText {
				t.Errorf("text = %q, want %q (text must NOT include ✓/✗ suffixes)", got, tt.wantText)
			}
			if got := btn["type"]; got != tt.wantType {
				t.Errorf("type = %q, want %q", got, tt.wantType)
			}
			// Ensure we never set disabled — the Feishu renderer turns disabled
			// buttons grey and would mask the type's color.
			if d, ok := btn["disabled"]; ok {
				t.Errorf("disabled must not be set, got %v", d)
			}
			val, ok := btn["value"].(map[string]string)
			if !ok {
				t.Fatalf("value is not map[string]string: %T", btn["value"])
			}
			wantAction := tt.action + ":" + tt.articleID
			if val["action"] != wantAction {
				t.Errorf("action = %q, want %q", val["action"], wantAction)
			}
			if val["paper_id"] != tt.articleID {
				t.Errorf("paper_id = %q, want %q", val["paper_id"], tt.articleID)
			}
		})
	}
}

func TestArxivAbsToPDF(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://arxiv.org/abs/2401.12345", "https://arxiv.org/pdf/2401.12345.pdf"},
		{"https://arxiv.org/abs/2401.12345v2", "https://arxiv.org/pdf/2401.12345v2.pdf"},
		{"https://arxiv.org/abs/cs/9901001", "https://arxiv.org/pdf/cs/9901001.pdf"},
		{"https://example.com/abs/2401.12345", "https://example.com/abs/2401.12345"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := arxivAbsToPDF(tt.in); got != tt.want {
			t.Errorf("arxivAbsToPDF(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestRenderRecommendLinkButton ensures the per-article "open PDF" button
// uses Card 2.0's `behaviors: open_url` (the modern equivalent of the
// deprecated top-level `url` field) with all four platform URLs set so a
// click opens the PDF in the system browser on desktop + mobile without
// any bot round-trip.
func TestRenderRecommendLinkButton(t *testing.T) {
	const pdfURL = "https://arxiv.org/pdf/2401.12345.pdf"
	btn := renderRecommendLinkButton("📑", pdfURL)

	if btn["tag"] != "button" {
		t.Errorf("tag = %v, want button", btn["tag"])
	}
	if text := btn["text"].(map[string]any)["content"]; text != "📑" {
		t.Errorf("text = %v, want 📑", text)
	}
	// Link buttons must NOT have a value (that's for callback buttons)
	// and must NOT be disabled.
	if _, hasValue := btn["value"]; hasValue {
		t.Errorf("link button must not have a value field, got %v", btn["value"])
	}
	if d, ok := btn["disabled"]; ok {
		t.Errorf("link button must not be disabled, got %v", d)
	}

	behaviors, ok := btn["behaviors"].([]map[string]any)
	if !ok || len(behaviors) != 1 {
		t.Fatalf("behaviors = %v, want single open_url entry", btn["behaviors"])
	}
	b := behaviors[0]
	if b["type"] != "open_url" {
		t.Errorf("behavior type = %v, want open_url", b["type"])
	}
	for _, platform := range []string{"default_url", "android_url", "ios_url", "pc_url"} {
		if got := b[platform]; got != pdfURL {
			t.Errorf("%s = %v, want %s", platform, got, pdfURL)
		}
	}
}

// TestBuildDailyRecommendCard_ArxivLinkButton verifies the per-article
// 📑 button is present in the button row when PDFURL is set, and uses
// the open_url behavior with the PDF URL derived from the abs link.
func TestBuildDailyRecommendCard_ArxivLinkButton(t *testing.T) {
	items := []RecommendCardItem{
		{
			ID: "2401.00001", Title: "P", Abstract: "abs",
			PDFURL: "https://arxiv.org/pdf/2401.00001.pdf",
			Score:  0.9,
		},
	}
	cardJSON := buildDailyRecommendCard(items, 1, 1)

	var card map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	body, _ := card["body"].(map[string]any)
	elements, _ := body["elements"].([]any)

	var linkBtn map[string]any
	for _, e := range elements {
		em, ok := e.(map[string]any)
		if !ok || em["tag"] != "column_set" {
			continue
		}
		cols, _ := em["columns"].([]any)
		for _, c := range cols {
			cm, _ := c.(map[string]any)
			celems, _ := cm["elements"].([]any)
			for _, ce := range celems {
				cb, ok := ce.(map[string]any)
				if !ok || cb["tag"] != "button" {
					continue
				}
				behaviors, hasBehaviors := cb["behaviors"]
				if !hasBehaviors {
					continue
				}
				bl, ok := behaviors.([]any)
				if !ok || len(bl) == 0 {
					continue
				}
				if b0, _ := bl[0].(map[string]any); b0["type"] == "open_url" {
					linkBtn = cb
					break
				}
			}
			if linkBtn != nil {
				break
			}
		}
		if linkBtn != nil {
			break
		}
	}
	if linkBtn == nil {
		t.Fatal("arxiv link button not found in card")
	}
	if text := linkBtn["text"].(map[string]any)["content"]; text != "📑" {
		t.Errorf("link button text = %v, want 📑", text)
	}
	behaviors := linkBtn["behaviors"].([]any)
	b0 := behaviors[0].(map[string]any)
	if b0["default_url"] != "https://arxiv.org/pdf/2401.00001.pdf" {
		t.Errorf("default_url = %v", b0["default_url"])
	}
}

// TestBuildDailyRecommendCard_NoArxivLinkWhenPDFURLEmpty ensures the 📑
// button is hidden when an item has no PDFURL (defensive guard for any
// future non-arXiv data source — today the pool is always arXiv).
func TestBuildDailyRecommendCard_NoArxivLinkWhenPDFURLEmpty(t *testing.T) {
	items := []RecommendCardItem{
		{ID: "x", Title: "X", Abstract: "abs", Score: 0.5, PDFURL: ""},
	}
	cardJSON := buildDailyRecommendCard(items, 1, 1)

	var card map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	body, _ := card["body"].(map[string]any)
	elements, _ := body["elements"].([]any)
	for _, e := range elements {
		em, ok := e.(map[string]any)
		if !ok || em["tag"] != "column_set" {
			continue
		}
		cols, _ := em["columns"].([]any)
		for _, c := range cols {
			cm, _ := c.(map[string]any)
			celems, _ := cm["elements"].([]any)
			for _, ce := range celems {
				cb, ok := ce.(map[string]any)
				if !ok || cb["tag"] != "button" {
					continue
				}
				if _, hasBehaviors := cb["behaviors"]; hasBehaviors {
					t.Errorf("did not expect link button when PDFURL is empty, got button with behaviors: %v", cb)
				}
			}
		}
	}
}

// findButtonByAction walks the card body elements and returns the first
// button element whose value.action matches the given prefix. Used by the
// recommend-card tests to assert button state after building the card.
func findButtonByAction(t *testing.T, cardJSON, actionPrefix string) map[string]any {
	t.Helper()
	var card map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	body, _ := card["body"].(map[string]any)
	elements, _ := body["elements"].([]any)
	for _, e := range elements {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if em["tag"] != "column_set" {
			continue
		}
		cols, _ := em["columns"].([]any)
		for _, c := range cols {
			cm, _ := c.(map[string]any)
			celems, _ := cm["elements"].([]any)
			for _, ce := range celems {
				cb, ok := ce.(map[string]any)
				if !ok {
					continue
				}
				if cb["tag"] != "button" {
					continue
				}
				val, _ := cb["value"].(map[string]any)
				act, _ := val["action"].(string)
				if strings.HasPrefix(act, actionPrefix) {
					return cb
				}
			}
		}
	}
	return nil
}

// TestBuildDailyRecommendCard_StatusHighlight verifies that the card builder
// reflects the per-article status in the per-article buttons. This is the
// regression test for the "user clicks 👍 but button doesn't highlight" bug.
// Visual state is conveyed by the button type (primary / danger / default)
// only — the emoji text stays unchanged.
func TestBuildDailyRecommendCard_StatusHighlight(t *testing.T) {
	votes := 42
	items := []RecommendCardItem{
		{ID: "2401.00001", Title: "A Liked Paper", Abstract: "abs A", Score: 0.9, Status: 2, AXNetVotes: &votes},
		{ID: "2401.00002", Title: "B Disliked Paper", Abstract: "abs B", Score: 0.8, Status: -1},
		{ID: "2401.00003", Title: "C Activated Paper", Abstract: "abs C", Score: 0.7, Status: 1},
		{ID: "2401.00004", Title: "D Fresh Paper", Abstract: "abs D", Score: 0.6, Status: 0},
	}
	cardJSON := buildDailyRecommendCard(items, 1, 1)

	// A: liked — 👍 should be primary. Text is still "👍" (no ✓/✗ suffix).
	btn := findButtonByAction(t, cardJSON, "recommend:like:2401.00001")
	if btn == nil {
		t.Fatal("like button for A not found")
	}
	if btn["type"] != "primary" {
		t.Errorf("A like type = %v, want primary", btn["type"])
	}
	if d, ok := btn["disabled"]; ok {
		t.Errorf("A like should not set disabled (would mask the color), got %v", d)
	}
	if text := btn["text"].(map[string]any)["content"]; text != "👍" {
		t.Errorf("A like text = %v, want \"👍\" (no suffix)", text)
	}

	// A: 👎 and 🤖 stay default
	for _, prefix := range []string{"recommend:dislike:2401.00001", "recommend:activate:2401.00001"} {
		b := findButtonByAction(t, cardJSON, prefix)
		if b == nil {
			t.Fatalf("%s not found", prefix)
		}
		if b["type"] != "default" {
			t.Errorf("%s type = %v, want default", prefix, b["type"])
		}
	}

	// B: disliked — 👎 should be danger
	btn = findButtonByAction(t, cardJSON, "recommend:dislike:2401.00002")
	if btn == nil {
		t.Fatal("dislike button for B not found")
	}
	if btn["type"] != "danger" {
		t.Errorf("B dislike type = %v, want danger", btn["type"])
	}
	if text := btn["text"].(map[string]any)["content"]; text != "👎" {
		t.Errorf("B dislike text = %v, want \"👎\" (no suffix)", text)
	}

	// C: activated — 🤖 should be primary
	btn = findButtonByAction(t, cardJSON, "recommend:activate:2401.00003")
	if btn == nil {
		t.Fatal("activate button for C not found")
	}
	if btn["type"] != "primary" {
		t.Errorf("C activate type = %v, want primary", btn["type"])
	}
	if text := btn["text"].(map[string]any)["content"]; text != "🤖" {
		t.Errorf("C activate text = %v, want \"🤖\" (no suffix)", text)
	}

	// D: fresh — all three buttons are default type, no disabled
	for _, prefix := range []string{"recommend:like:2401.00004", "recommend:dislike:2401.00004", "recommend:activate:2401.00004"} {
		btn := findButtonByAction(t, cardJSON, prefix)
		if btn == nil {
			t.Fatalf("button %s not found", prefix)
		}
		if btn["type"] != "default" {
			t.Errorf("%s type = %v, want default", prefix, btn["type"])
		}
		if _, ok := btn["disabled"]; ok {
			t.Errorf("%s should not set disabled", prefix)
		}
	}

	// Mutual exclusion: among the three per-article buttons for a given
	// article, at most one is non-default. (Status is a single int, so this
	// is true by construction — but we assert it explicitly because the
	// question keeps coming up.)
	for _, id := range []string{"2401.00001", "2401.00002", "2401.00003", "2401.00004"} {
		like := findButtonByAction(t, cardJSON, "recommend:like:"+id)
		dislike := findButtonByAction(t, cardJSON, "recommend:dislike:"+id)
		activate := findButtonByAction(t, cardJSON, "recommend:activate:"+id)
		highlighted := 0
		for _, b := range []map[string]any{like, dislike, activate} {
			if b["type"] != "default" {
				highlighted++
			}
		}
		if highlighted > 1 {
			t.Errorf("article %s: expected at most 1 highlighted button, got %d", id, highlighted)
		}
	}

	t.Logf("✅ button states correctly reflect per-article status; mutual exclusion holds")
}

// TestBuildDailyRecommendCard_MarkReadPageFooter verifies the bulk
// "mark all as read" button reflects the all-read state in the footer.
// Visual state is conveyed by the button type (primary when all read) and
// the label change ("一键已阅" → "已标记"). We do NOT set disabled.
func TestBuildDailyRecommendCard_MarkReadPageFooter(t *testing.T) {
	// Case 1: nothing read — button is default type, "一键已阅本页 N 篇".
	items := []RecommendCardItem{
		{ID: "2401.00001", Title: "A", Abstract: "a", Status: 0},
		{ID: "2401.00002", Title: "B", Abstract: "b", Status: 0},
	}
	cardJSON := buildDailyRecommendCard(items, 1, 1)
	btn := findFooterMarkReadBtn(t, cardJSON)
	if btn == nil {
		t.Fatal("mark-read-page footer button not found")
	}
	if btn["type"] != "default" {
		t.Errorf("mark-read-page type = %v, want default", btn["type"])
	}
	if d, ok := btn["disabled"]; ok {
		t.Errorf("mark-read-page should not set disabled, got %v", d)
	}
	if text := btn["text"].(map[string]any)["content"]; text != "✅ 一键已阅本页 2 篇" {
		t.Errorf("mark-read-page text = %v", text)
	}

	// Case 2: all read — button is primary, "✅ 已标记本页 N 篇".
	items = []RecommendCardItem{
		{ID: "2401.00001", Title: "A", Abstract: "a", Status: 3},
		{ID: "2401.00002", Title: "B", Abstract: "b", Status: 3},
	}
	cardJSON = buildDailyRecommendCard(items, 1, 1)
	btn = findFooterMarkReadBtn(t, cardJSON)
	if btn == nil {
		t.Fatal("mark-read-page footer button not found (all-read case)")
	}
	if btn["type"] != "primary" {
		t.Errorf("mark-read-page type = %v, want primary", btn["type"])
	}
	if d, ok := btn["disabled"]; ok {
		t.Errorf("mark-read-page should not set disabled (would mask color), got %v", d)
	}
	if text := btn["text"].(map[string]any)["content"]; text != "✅ 已标记本页 2 篇" {
		t.Errorf("mark-read-page text = %v, text=\"✅ 已标记本页 2 篇\"", text)
	}

	// Case 3: mixed — button stays default.
	items = []RecommendCardItem{
		{ID: "2401.00001", Title: "A", Abstract: "a", Status: 3},
		{ID: "2401.00002", Title: "B", Abstract: "b", Status: 0},
	}
	cardJSON = buildDailyRecommendCard(items, 1, 1)
	btn = findFooterMarkReadBtn(t, cardJSON)
	if btn == nil {
		t.Fatal("mark-read-page footer button not found (mixed case)")
	}
	if btn["type"] != "default" {
		t.Errorf("mark-read-page type = %v, want default", btn["type"])
	}
}

// TestBuildDailyRecommendCard_MarkReadPageFooter_NonUnreadHighlights
// extends the all-read rule from "every status == 3" to "every status != 0".
// This is the user-facing fix for the Feishu card interaction bug:
// when a user likes some articles (status=2) and then bulk-mark-reads the
// rest (status=3), the like buttons must stay red AND the mark-read footer
// must stay highlighted, instead of the two states fighting each other.
func TestBuildDailyRecommendCard_MarkReadPageFooter_NonUnreadHighlights(t *testing.T) {
	cases := []struct {
		name       string
		statuses   []int
		wantType   string
		wantLabel  string
		commentary string
	}{
		{
			name:       "all-liked",
			statuses:   []int{2, 2, 2},
			wantType:   "primary",
			wantLabel:  "✅ 已标记本页 3 篇",
			commentary: "every article is liked; the page is fully non-unread",
		},
		{
			name:       "mixed-liked-read",
			statuses:   []int{2, 3, 2},
			wantType:   "primary",
			wantLabel:  "✅ 已标记本页 3 篇",
			commentary: "likes survive a bulk mark-read, so the page is still non-unread",
		},
		{
			name:       "all-activated",
			statuses:   []int{1, 1},
			wantType:   "primary",
			wantLabel:  "✅ 已标记本页 2 篇",
			commentary: "activating every article (🤖 on each) also counts as non-unread",
		},
		{
			name:       "still-has-unread",
			statuses:   []int{2, 0, 3},
			wantType:   "default",
			wantLabel:  "✅ 一键已阅本页 3 篇",
			commentary: "one unread keeps the bulk button at its default state",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]RecommendCardItem, len(tc.statuses))
			for i, s := range tc.statuses {
				items[i] = RecommendCardItem{
					ID:    fmt.Sprintf("2401.0000%d", i+1),
					Title: fmt.Sprintf("P%d", i+1), Abstract: "x", Status: s,
				}
			}
			cardJSON := buildDailyRecommendCard(items, 1, 1)
			btn := findFooterMarkReadBtn(t, cardJSON)
			if btn == nil {
				t.Fatalf("mark-read-page footer button not found (%s)", tc.commentary)
			}
			if btn["type"] != tc.wantType {
				t.Errorf("type = %v, want %v (%s)", btn["type"], tc.wantType, tc.commentary)
			}
			if got := btn["text"].(map[string]any)["content"]; got != tc.wantLabel {
				t.Errorf("text = %v, want %q (%s)", got, tc.wantLabel, tc.commentary)
			}
		})
	}
}

// findFooterMarkReadBtn returns the bulk "mark all as read" footer button.
// It is the only button on the card whose value.action is
// "recommend:mark-read-page".
func findFooterMarkReadBtn(t *testing.T, cardJSON string) map[string]any {
	t.Helper()
	var card map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	body, _ := card["body"].(map[string]any)
	elements, _ := body["elements"].([]any)
	for _, e := range elements {
		em, ok := e.(map[string]any)
		if !ok || em["tag"] != "button" {
			continue
		}
		val, _ := em["value"].(map[string]any)
		if act, _ := val["action"].(string); act == "recommend:mark-read-page" {
			return em
		}
	}
	return nil
}

// TestFindRecommendPage verifies the page-number calculation that lets the
// card-action handlers re-render only the page that contains the clicked
// article. The article list is in batch_order, and pages are
// recommendPageSize items each.
func TestFindRecommendPage(t *testing.T) {
	items := make([]RecommendCardItem, 25)
	for i := range items {
		items[i] = RecommendCardItem{ID: fmt.Sprintf("2401.%05d", i+1)}
	}
	tests := []struct {
		id   string
		want int
	}{
		{"2401.00001", 1}, // first
		{"2401.00008", 1}, // last on page 1 (pageSize=8)
		{"2401.00009", 2}, // first on page 2
		{"2401.00016", 2}, // last on page 2
		{"2401.00017", 3}, // first on page 3
		{"2401.00024", 3}, // last on page 3
		{"2401.00025", 4}, // item 25 → page 4
		{"2401.99999", 1}, // not in list → fallback to page 1
	}
	for _, tt := range tests {
		if got := findRecommendPage(items, tt.id); got != tt.want {
			t.Errorf("findRecommendPage(%s) = %d, want %d", tt.id, got, tt.want)
		}
	}
}
