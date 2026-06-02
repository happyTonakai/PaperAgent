package feishu

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/happyTonakai/paperagent/internal/session"
)

// maxCardJSONBytes is Feishu interactive card payload limit (~30KB).
// We leave 2KB margin for safety.
const maxCardJSONBytes = 28000

// maxCardElements is Feishu JSON 2.0 card element limit (200).
// We leave 20 margin for hr + note + fixed card overhead.
const maxCardElements = 180

// ─── Card Schema 2.0 helpers ───

func cardBase() map[string]any {
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
		},
	}
}

func plainText(content string) map[string]any {
	return map[string]any{"tag": "plain_text", "content": content}
}

func cardHeader(title, template string) map[string]any {
	return map[string]any{
		"title":    plainText(title),
		"template": template,
	}
}

func mdElement(content string) map[string]any {
	return map[string]any{
		"tag":     "markdown",
		"content": content,
	}
}

func buttonElement(text, value string, btnType string, extra map[string]string) map[string]any {
	if btnType == "" {
		btnType = "default"
	}
	valMap := map[string]string{"action": value}
	for k, v := range extra {
		valMap[k] = v
	}
	return map[string]any{
		"tag":   "button",
		"text":  plainText(text),
		"type":  btnType,
		"value": valMap,
		"width": "default",
	}
}

func noteElement(text string) map[string]any {
	return map[string]any{
		"tag": "div",
		"text": map[string]any{
			"tag":        "plain_text",
			"content":    text,
			"text_size":  "notation",
			"text_color": "grey",
		},
	}
}

func hrElement() map[string]any {
	return map[string]any{"tag": "hr"}
}

func marshalCard(card map[string]any) string {
	b, err := json.Marshal(card)
	if err != nil {
		log.Printf("[feishu] marshal card error: %v", err)
		return `{"schema":"2.0","config":{"wide_screen_mode":true},"body":{"elements":[{"tag":"markdown","content":"内部错误"}]}}`
	}
	return string(b)
}

// estimateContentElements estimates how many internal elements Feishu will
// generate from the markdown content inside a card. This is a rough heuristic
// to stay under the 200-element hard limit.
//
// Each non-blank line counts as ~1 element. Table rows are heavy so we
// count them conservatively as row + cells. Blank lines are ignored.
func estimateContentElements(content string) int {
	count := 0
	lines := strings.Split(content, "\n")
	inCodeBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue // blank lines don't generate elements
		}

		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			count++ // fence
			continue
		}

		if inCodeBlock {
			count++ // each code line
			continue
		}

		// Table row: conservatively count as row + cells
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			cells := strings.Count(trimmed, "|") - 1
			count += 1 + cells/2 // row + rough cell overhead
			continue
		}

		// Regular line: paragraph, heading, list, etc.
		count++
	}

	return count
}

// cardFits checks whether markdown content fits in a Feishu card, considering
// both the JSON byte size limit and the 200-element limit.
func cardFits(content string, builder func(content string) string) bool {
	cardJSON := builder(content)
	if len(cardJSON) > maxCardJSONBytes {
		return false
	}
	if estimateContentElements(content) > maxCardElements {
		return false
	}
	return true
}

// ─── Content fitting ───
// fitMarkdownContent tries to fit as much markdown content as possible into a card
// builder function. Returns (fittedContent, overflow). If everything fits, overflow is "".
// The builder receives the content and returns a card JSON string.
// Checks both JSON byte size AND estimated element count.
func fitMarkdownContent(content string, builder func(content string) string) (fits string, overflow string) {
	if cardFits(content, builder) {
		return content, ""
	}

	// Binary search for the right truncation point (by rune count for CJK safety)
	runes := []rune(content)
	lo, hi := 0, len(runes)

	for lo < hi {
		mid := (lo + hi + 1) / 2
		testContent := string(runes[:mid])
		if cardFits(testContent, builder) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}

	if lo == 0 {
		// Can't even fit one rune? Fallback to short message.
		return "（内容过长，无法在卡片中展示）", content
	}

	fits = string(runes[:lo])
	overflow = string(runes[lo:])
	return fits, overflow
}

// ─── Loading card (initial state when summary starts) ───

func buildLoadingCard(paperID, title string) string {
	c := cardBase()
	c["header"] = cardHeader("📄 正在总结论文...", "blue")
	if title != "" {
		c["header"] = cardHeader("📄 "+truncateTitle(title, 40), "blue")
	}
	c["body"] = map[string]any{
		"elements": []map[string]any{
			mdElement("⏳ 正在生成论文总结，请耐心等待...\n\n摘要将实时更新在此卡片中。"),
		},
	}
	return marshalCard(c)
}

// ─── Streaming card (updates during summary) ───

func buildStreamingCard(paperID, title, content string) string {
	c := cardBase()
	c["config"].(map[string]any)["update_multi"] = true
	hdrTitle := "📄 正在总结论文..."
	if title != "" {
		hdrTitle = "📄 " + truncateTitle(title, 40)
	}
	c["header"] = cardHeader(hdrTitle, "blue")

	elements := []map[string]any{
		mdElement(content),
		hrElement(),
		noteElement("⏳ 正在更新中..."),
	}
	c["body"] = map[string]any{"elements": elements}
	return marshalCard(c)
}

// ─── Done card (after summary completes) ───

func buildDoneCard(paperID, title, content string) string {
	c := cardBase()
	hdrTitle := "✅ 总结完成"
	if title != "" {
		hdrTitle = "✅ " + truncateTitle(title, 40)
	}
	c["header"] = cardHeader(hdrTitle, "green")

	elements := []map[string]any{
		mdElement(content),
		hrElement(),
		noteElement("直接在聊天中提问即可 🎉"),
	}

	c["body"] = map[string]any{"elements": elements}
	return marshalCard(c)
}

// ─── Frozen continuation card (mid-stream, no more updates) ───

func buildContinuationCard(content string) string {
	c := cardBase()
	c["header"] = cardHeader("📄 总结（续）", "green")
	c["body"] = map[string]any{
		"elements": []map[string]any{
			mdElement(content),
			hrElement(),
			noteElement("后续内容在下方卡片中继续 ✨"),
		},
	}
	return marshalCard(c)
}

// ─── Streaming continuation card (updates during overflow) ───

func buildStreamingContinuationCard(content string) string {
	c := cardBase()
	c["config"].(map[string]any)["update_multi"] = true
	c["header"] = cardHeader("📄 总结（续）", "blue")
	c["body"] = map[string]any{
		"elements": []map[string]any{
			mdElement(content),
			hrElement(),
			noteElement("⏳ 正在更新中..."),
		},
	}
	return marshalCard(c)
}

// ─── Thinking card (initial state when Q&A starts) ───

func buildThinkingCard(paperID, title, question string) string {
	c := cardBase()
	hdrTitle := "💭 思考中..."
	if title != "" {
		hdrTitle = "💭 " + truncateTitle(title, 40)
	}
	c["header"] = cardHeader(hdrTitle, "blue")

	elements := []map[string]any{
		mdElement(fmt.Sprintf("**Q:** %s\n\n⏳ 正在思考回答...", question)),
	}
	c["body"] = map[string]any{"elements": elements}
	return marshalCard(c)
}

// ─── Chat streaming card ───

func buildChatStreamingCard(paperID, title, question, content string) string {
	c := cardBase()
	c["config"].(map[string]any)["update_multi"] = true
	hdrTitle := "💭 回答中..."
	if title != "" {
		hdrTitle = "💭 " + truncateTitle(title, 40)
	}
	c["header"] = cardHeader(hdrTitle, "blue")

	elements := []map[string]any{
		mdElement(fmt.Sprintf("**Q:** %s\n\n%s", question, content)),
		hrElement(),
		noteElement("⏳ 正在更新中..."),
	}
	c["body"] = map[string]any{"elements": elements}
	return marshalCard(c)
}

// ─── Chat done card ───

func buildChatDoneCard(paperID, title, question, answer string) string {
	c := cardBase()
	hdrTitle := "✅ 回答完成"
	if title != "" {
		hdrTitle = "✅ " + truncateTitle(title, 40)
	}
	c["header"] = cardHeader(hdrTitle, "green")

	elements := []map[string]any{
		mdElement(fmt.Sprintf("**Q:** %s\n\n%s", question, answer)),
		hrElement(),
		noteElement("继续提问即可进行多轮对话 ✨"),
	}
	c["body"] = map[string]any{"elements": elements}
	return marshalCard(c)
}

// ─── Chat streaming continuation card ───

func buildChatStreamingContinuationCard(question, content string) string {
	c := cardBase()
	c["config"].(map[string]any)["update_multi"] = true
	c["header"] = cardHeader("💭 回答（续）", "blue")
	c["body"] = map[string]any{
		"elements": []map[string]any{
			mdElement(fmt.Sprintf("**Q:** %s\n\n%s", question, content)),
			hrElement(),
			noteElement("⏳ 正在更新中..."),
		},
	}
	return marshalCard(c)
}

// ─── Paper list card ───

func buildPaperListCard(papers []session.PaperSummary) string {
	c := cardBase()
	c["header"] = cardHeader("📚 最近的文章", "blue")

	elements := make([]map[string]any, 0, len(papers)+2)
	elements = append(elements, mdElement(fmt.Sprintf("共 **%d** 篇文章，以下是最近 **%d** 篇：", len(papers), len(papers))))

	for i, p := range papers {
		title := p.Title
		if title == "" {
			title = "Paper " + p.ShortRef()
		}
		ratingStr := ""
		if p.Rating > 0 {
			ratingStr = fmt.Sprintf(" ⭐%d", p.Rating)
		}
		timeStr := p.UpdatedAt.Format("01-02 15:04")

		text := fmt.Sprintf("**%d.** %s%s\n_%s · %s_", i+1, title, ratingStr, timeStr, p.ShortRef())

		colSet := map[string]any{
			"tag":       "column_set",
			"flex_mode": "none",
			"columns": []map[string]any{
				{
					"tag":            "column",
					"width":          "weighted",
					"weight":         4,
					"vertical_align": "center",
					"elements": []map[string]any{
						{"tag": "markdown", "content": text},
					},
				},
				{
					"tag":            "column",
					"width":          "auto",
					"vertical_align": "center",
					"elements": []map[string]any{
						buttonElement("选择", "open:"+p.Ref(), "default", map[string]string{"paper_id": p.Ref()}),
					},
				},
			},
		}
		elements = append(elements, colSet)
		if i < len(papers)-1 {
			elements = append(elements, hrElement())
		}
	}

	elements = append(elements, hrElement())
	elements = append(elements, noteElement("点击「选择」切换当前文章，然后可以直接提问。"))

	c["body"] = map[string]any{"elements": elements}
	return marshalCard(c)
}

// ─── Paper detail card (when clicking a paper from list) ───

func buildPaperDetailCard(paper *session.Paper) map[string]any {
	c := cardBase()

	title := paper.Title
	if title == "" {
		title = "Paper " + paperShortRef(*paper)
	}
	c["header"] = cardHeader("📄 "+truncateTitle(title, 40), "blue")

	summary := paper.InitialSummary
	if len(summary) > 3000 {
		summary = summary[:3000] + "\n\n...（内容过长，已截断）"
	}

	elements := []map[string]any{
		mdElement(summary),
		hrElement(),
		buttonElement("💬 开始问答", "qa:"+paper.Ref(), "primary", map[string]string{"paper_id": paper.Ref()}),
		noteElement("已设为当前文章，可以直接在聊天中提问 ✨"),
	}

	c["body"] = map[string]any{"elements": elements}
	return c
}

// ─── Simple markdown card (for auto-upgraded text messages) ───

func buildCardMarkdown(content string) string {
	c := cardBase()
	c["body"] = map[string]any{
		"elements": []map[string]any{
			mdElement(content),
		},
	}
	return marshalCard(c)
}

// ─── Paper list card with selection highlight ───

func buildPaperListCardWithSelection(papers []session.PaperSummary, selectedID string) map[string]any {
	c := cardBase()
	c["header"] = cardHeader("📚 最近的文章", "blue")

	elements := make([]map[string]any, 0, len(papers)+2)
	elements = append(elements, mdElement(fmt.Sprintf("共 **%d** 篇文章，以下是最近 **%d** 篇：", len(papers), len(papers))))

	for i, p := range papers {
		title := p.Title
		if title == "" {
			title = "Paper " + p.ShortRef()
		}
		ratingStr := ""
		if p.Rating > 0 {
			ratingStr = fmt.Sprintf(" ⭐%d", p.Rating)
		}
		timeStr := p.UpdatedAt.Format("01-02 15:04")

		text := fmt.Sprintf("**%d.** %s%s\n_%s · %s_", i+1, title, ratingStr, timeStr, p.ShortRef())

		var btn map[string]any
		if p.Ref() == selectedID {
			btn = map[string]any{
				"tag":   "button",
				"text":  plainText("已选中 ✓"),
				"type":  "primary",
				"disabled": true,
				"width": "default",
			}
		} else {
			btn = buttonElement("选择", "open:"+p.Ref(), "default", map[string]string{"paper_id": p.Ref()})
		}

		colSet := map[string]any{
			"tag":       "column_set",
			"flex_mode": "none",
			"columns": []map[string]any{
				{
					"tag":            "column",
					"width":          "weighted",
					"weight":         4,
					"vertical_align": "center",
					"elements": []map[string]any{
						{"tag": "markdown", "content": text},
					},
				},
				{
					"tag":            "column",
					"width":          "auto",
					"vertical_align": "center",
					"elements": []map[string]any{btn},
				},
			},
		}
		elements = append(elements, colSet)
		if i < len(papers)-1 {
			elements = append(elements, hrElement())
		}
	}

	elements = append(elements, hrElement())
	elements = append(elements, noteElement("点击「选择」切换当前文章，然后可以直接提问。"))

	c["body"] = map[string]any{"elements": elements}
	return c
}

// ─── Helpers ───

func truncateTitle(title string, maxLen int) string {
	runes := []rune(title)
	if len(runes) <= maxLen {
		return title
	}
	return string(runes[:maxLen]) + "..."
}

// splitTextByBytes splits text into chunks of approximately maxBytes,
// avoiding splits inside markdown code blocks (```) and tables (|...|).
func splitTextByBytes(text string, maxBytes int) []string {
	if len(text) <= maxBytes {
		return []string{text}
	}

	// Find safe split points: positions where we are NOT inside a code block or table
	type safePoint struct {
		pos   int
		good  int // quality: 2=double newline, 1=single newline, 0=forced
	}

	lines := strings.Split(text, "\n")
	var safePositions []safePoint
	inCodeBlock := false
	inTable := false
	pos := 0

	for _, line := range lines {
		lineLen := len(line) + 1 // +1 for \n
		trimmed := strings.TrimSpace(line)

		// Track code block state
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
		}

		// Track table state
		isTableLine := len(trimmed) > 1 && trimmed[0] == '|' && trimmed[len(trimmed)-1] == '|'
		isTableSep := len(trimmed) > 1 && trimmed[0] == '|' && trimmed[len(trimmed)-1] == '|' &&
			strings.Contains(trimmed, "---")
		if isTableLine && !inTable {
			inTable = true
		} else if !isTableLine && !isTableSep && inTable {
			inTable = false
		}

		endPos := pos + lineLen

		if !inCodeBlock && !inTable {
			quality := 0
			if trimmed == "" {
				quality = 2 // paragraph break is ideal
			} else if endPos > maxBytes*4/5 {
				quality = 1 // line break above threshold
			}
			if quality > 0 {
				safePositions = append(safePositions, safePoint{endPos, quality})
			}
		}

		pos = endPos
	}

	// Now split at safe positions
	var chunks []string
	remaining := text
	for len(remaining) > maxBytes && len(safePositions) > 0 {
		// Find the best safe point before maxBytes
		bestIdx := -1
		for i, sp := range safePositions {
			if sp.pos > maxBytes {
				break
			}
			bestIdx = i
		}

		if bestIdx < 0 {
			// No safe point found, force split at maxBytes (worst case)
			chunks = append(chunks, remaining[:maxBytes])
			remaining = remaining[maxBytes:]
		} else {
			sp := safePositions[bestIdx]
			chunks = append(chunks, remaining[:sp.pos])
			remaining = remaining[sp.pos:]
			safePositions = safePositions[bestIdx+1:]
			// Adjust remaining positions
			for i := range safePositions {
				safePositions[i].pos -= sp.pos
			}
		}

		// Recalculate maxBytes proportionally for remaining text
		if len(chunks) > 0 && len(remaining) > 0 {
			// Keep using the same maxBytes for subsequent chunks
		}
	}

	if remaining != "" {
		chunks = append(chunks, remaining)
	}

	if len(chunks) == 0 {
		chunks = append(chunks, text)
	}

	return chunks
}

func paperShortRef(p session.Paper) string {
	if p.SessionID != "" && len(p.SessionID) >= 8 {
		return p.SessionID[:8]
	}
	return fmt.Sprintf("%d", p.ID)
}
