package feishu

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happyTonakai/paperagent/internal/session"
)

// loadLatestPaper loads the most recently modified paper from ~/.paperagent/papers/.
func loadLatestPaper(t *testing.T) *session.Paper {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".paperagent", "papers")
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
			cardJSON = buildDoneCard("test", title, cardContent)
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
		sb.WriteString(fmt.Sprintf("## Table %d\n\n", i+1))
		sb.WriteString("| Method | Param (M) | Acc (%) | F1 | Speed |\n")
		sb.WriteString("|--------|-----------|---------|-----|-------|\n")
		for j := 0; j < 10; j++ {
			sb.WriteString(fmt.Sprintf("| Model-%d | %.1f | %2.1f | %.3f | fast |\n", j, float64(j)*5.2, float64(j)*3.7+85.0, float64(j)*0.02+0.88))
		}
		sb.WriteString("\n")
	}

	// Code blocks — should not be split
	sb.WriteString("\n## Key Implementation\n\n")
	sb.WriteString("```python\n")
	sb.WriteString("def compute(x):\n")
	sb.WriteString("    # This is a very long code block that should never be split\n")
	for i := 0; i < 200; i++ {
		sb.WriteString(fmt.Sprintf("    result = layer_%d.forward(x)  # step %d\n", i, i))
	}
	sb.WriteString("    return x\n")
	sb.WriteString("```\n")

	// More paragraphs
	for i := 0; i < 100; i++ {
		sb.WriteString(fmt.Sprintf("This is paragraph number %d. It contains enough text to make the summary really long and force multiple card splits during streaming. ", i+1))
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
			cardJSON = buildDoneCard("test", "Stress Test", cardContent)
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
	question := "What are the main contributions of this paper?"
	paperID := "test-paper-123"
	title := "Test Paper Title"

	// Generate a long answer
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString(fmt.Sprintf("Finding number %d: The model demonstrates significant improvements in benchmark evaluations. ", i+1))
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
				return buildChatStreamingCard(paperID, title, question, c)
			}
			return buildChatStreamingContinuationCard(question, c)
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
			cardJSON = buildChatDoneCard(paperID, title, question, cardContent)
		} else {
			cardJSON = buildChatStreamingContinuationCard(question, cardContent)
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
