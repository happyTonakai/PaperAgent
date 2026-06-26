package feishu

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/happyTonakai/paperagent/internal/session"
)

// maxCardJSONBytes is Feishu interactive card payload limit (~30KB).
// We leave 2KB margin for safety.
const maxCardJSONBytes = 28000

// maxCardElements is Feishu JSON 2.0 card element limit (200).
// We leave 20 margin for hr + note + fixed card overhead.
const maxCardElements = 180

// maxCardMdTables is the maximum number of markdown tables Feishu allows
// per interactive card. Exceeding this causes error 11310 (card table number over limit).
const maxCardMdTables = 5

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

// normalizeBlockquotes strips leading whitespace from blockquote markers (>)
// to ensure compatibility with Feishu's card markdown parser, which expects
// `>` at column 0 (no indentation). Lines inside code blocks are left untouched.
func normalizeBlockquotes(content string) string {
	lines := strings.Split(content, "\n")
	inCodeBlock := false
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			result = append(result, line)
			continue
		}
		if !inCodeBlock && strings.HasPrefix(trimmed, ">") {
			// Strip leading whitespace to put `>` at column 0
			result = append(result, strings.TrimLeft(line, " \t"))
		} else {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

func mdElement(content string) map[string]any {
	return map[string]any{
		"tag":     "markdown",
		"content": normalizeBlockquotes(content),
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

		// Table row: count as row + moderate cell overhead.
		// Feishu renders the entire markdown table as a single <table> element,
		// so individual rows don't each generate cells/2 card elements.
		// Use cells/4 to avoid overcounting wide tables and causing premature splits.
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			cells := strings.Count(trimmed, "|") - 1
			count += 1 + cells/4 // row + moderate cell overhead
			continue
		}

		// Regular line: paragraph, heading, list, etc.
		count++
	}

	return count
}

// countMdTables counts the number of markdown tables in a string.
// A table starts at a line matching |...| when not already inside a table.
func countMdTables(s string) int {
	count := 0
	inTable := false
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		isTable := len(trimmed) > 1 && trimmed[0] == '|' && trimmed[len(trimmed)-1] == '|'
		if isTable && !inTable {
			count++
			inTable = true
		} else if !isTable {
			inTable = false
		}
	}
	return count
}

// stripMdTables removes every markdown-table line from s and replaces
// each contiguous table block with a single "(table omitted)" placeholder,
// so callers can stay under Feishu's 5-table hard limit without losing
// the surrounding prose. Used as a fallback by buildDailyRecommendCard
// when an abstract contains a table but is short enough that the normal
// rune-truncation fallback would not help.
func stripMdTables(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	inTable := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isTable := len(trimmed) > 1 && trimmed[0] == '|' && trimmed[len(trimmed)-1] == '|'
		if isTable {
			if !inTable {
				out = append(out, "_(table omitted)_")
				inTable = true
			}
			continue
		}
		inTable = false
		out = append(out, line)
	}
	// Collapse runs of blank lines created by the removal.
	collapsed := make([]string, 0, len(out))
	prevBlank := false
	for _, line := range out {
		blank := strings.TrimSpace(line) == ""
		if blank && prevBlank {
			continue
		}
		collapsed = append(collapsed, line)
		prevBlank = blank
	}
	return strings.TrimSpace(strings.Join(collapsed, "\n"))
}

// cardFits checks whether markdown content fits in a Feishu card, considering
// the JSON byte size limit, the 200-element limit, and the table count limit (5).
func cardFits(content string, builder func(content string) string) bool {
	cardJSON := builder(content)
	if len(cardJSON) > maxCardJSONBytes {
		return false
	}
	if estimateContentElements(content) > maxCardElements {
		return false
	}
	if countMdTables(content) > maxCardMdTables {
		return false
	}
	return true
}

// ─── Content fitting ───
// fitMarkdownContent tries to fit as much markdown content as possible into a card
// builder function. Returns (fittedContent, overflow). If everything fits, overflow is "".
// The builder receives the content and returns a card JSON string.
// Checks JSON byte size, element count, and table count.
// Ensures the split never falls inside a markdown table or code block.
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

	// The binary search may land inside a table or code block.
	// Adjust backward to the nearest safe line boundary.
	safePos := findSafeBoundary(content, len([]byte(string(runes[:lo]))))
	if safePos > 0 {
		fits = content[:safePos]
		overflow = content[safePos:]
	} else {
		fits = string(runes[:lo])
		overflow = string(runes[lo:])
	}
	return fits, overflow
}

// findSafeBoundary scans lines from the start of content and finds the last
// line boundary at or before maxBytes that is NOT inside a table, code block,
// or LaTeX math formula ($...$ or $$...$$).
// Returns the byte position of that boundary (right after the newline), or 0.
// Blank line boundaries are preferred (quality=2), then any safe boundary (quality=1).
func findSafeBoundary(content string, maxBytes int) int {
	if maxBytes <= 0 {
		return 0
	}

	lines := strings.Split(content, "\n")
	inTable := false
	inCodeBlock := false
	inMath := false // inside $...$ or $$...$$
	bytePos := 0
	bestPos := 0
	lastSafePos := 0

	for i, line := range lines {
		// End position after this line (including newline)
		lineEnd := bytePos + len(line)
		if i+1 < len(lines) {
			lineEnd++ // +1 for the \n that was removed by Split
		}

		// If this line end exceeds maxBytes, stop looking
		if lineEnd > maxBytes {
			break
		}

		trimmed := strings.TrimSpace(line)

		// Track code block state
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
		}

		// Track table state (line is a table continuation)
		isTableLine := len(trimmed) > 1 && trimmed[0] == '|' && trimmed[len(trimmed)-1] == '|'
		if isTableLine && !inTable {
			inTable = true
		} else if !isTableLine && inTable {
			inTable = false
		}

		// Track math mode using $ count parity
		// (simple heuristic: odd $ means math crosses this line)
		if !inCodeBlock {
			dollarCount := strings.Count(trimmed, "$")
			if dollarCount%2 == 1 {
				inMath = !inMath
			}
		}

		// Record position AFTER this line
		if !inCodeBlock && !inTable && !inMath {
			lastSafePos = lineEnd
			if trimmed == "" {
				bestPos = lineEnd // blank line is ideal
			}
		}

		bytePos = lineEnd
	}

	if bestPos > 0 {
		return bestPos
	}
	return lastSafePos
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

func buildDoneCard(paperID, title, content string, promptTokens, completionTokens, cachedTokens int) string {
	c := cardBase()
	hdrTitle := "✅ 总结完成"
	if title != "" {
		hdrTitle = "✅ " + truncateTitle(title, 40)
	}
	c["header"] = cardHeader(hdrTitle, "green")

	tokenNote := "直接在聊天中提问即可 🎉"
	if promptTokens > 0 || completionTokens > 0 {
		// 输入 = 真实输入（剔除缓存命中部分）
		input := promptTokens - cachedTokens
		if input < 0 {
			input = 0
		}
		tokenNote = fmt.Sprintf("输入 %s tokens · 输出 %s tokens · 缓存命中 %s tokens", formatInt(input), formatInt(completionTokens), formatInt(cachedTokens))
	}

	elements := []map[string]any{
		mdElement(content),
		hrElement(),
		noteElement(tokenNote),
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

func buildThinkingCard(paperID, title string) string {
	c := cardBase()
	hdrTitle := "💭 思考中..."
	if title != "" {
		hdrTitle = "💭 " + truncateTitle(title, 40)
	}
	c["header"] = cardHeader(hdrTitle, "blue")

	elements := []map[string]any{
		mdElement("⏳ 正在思考回答..."),
	}
	c["body"] = map[string]any{"elements": elements}
	return marshalCard(c)
}

// ─── Chat streaming card ───

func buildChatStreamingCard(paperID, title, content string) string {
	c := cardBase()
	c["config"].(map[string]any)["update_multi"] = true
	hdrTitle := "💭 回答中..."
	if title != "" {
		hdrTitle = "💭 " + truncateTitle(title, 40)
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

// ─── Chat done card ───

func buildChatDoneCard(paperID, title, answer string, round int, promptTokens, completionTokens, cachedTokens int) string {
	c := cardBase()
	hdrTitle := "✅ 回答完成"
	if title != "" {
		hdrTitle = "✅ " + truncateTitle(title, 40)
	}
	c["header"] = cardHeader(hdrTitle, "green")

	tokenNote := "继续提问即可进行多轮对话 ✨"
	if promptTokens > 0 || completionTokens > 0 {
		// 输入 = 真实输入（剔除缓存命中部分）
		input := promptTokens - cachedTokens
		if input < 0 {
			input = 0
		}
		tokenNote = fmt.Sprintf("第 %s 轮 · 输入 %s tokens · 输出 %s tokens · 缓存命中 %s tokens", formatInt(round), formatInt(input), formatInt(completionTokens), formatInt(cachedTokens))
	}

	elements := []map[string]any{
		mdElement(answer),
		hrElement(),
		noteElement(tokenNote),
	}
	c["body"] = map[string]any{"elements": elements}
	return marshalCard(c)
}

// ─── Chat streaming continuation card ───

func buildChatStreamingContinuationCard(content string) string {
	c := cardBase()
	c["config"].(map[string]any)["update_multi"] = true
	c["header"] = cardHeader("💭 回答（续）", "blue")
	c["body"] = map[string]any{
		"elements": []map[string]any{
			mdElement(content),
			hrElement(),
			noteElement("⏳ 正在更新中..."),
		},
	}
	return marshalCard(c)
}

// ─── Paper list card (paginated) ───

func buildPaperListCardPaginated(pagePapers []session.PaperSummary, totalCount, page, pageSize int, selectedID string, headerTitle string, searchKeyword string) map[string]any {
	c := cardBase()
	if headerTitle == "" {
		headerTitle = "📚 最近的文章"
	}
	c["header"] = cardHeader(headerTitle, "blue")

	totalPages := (totalCount + pageSize - 1) / pageSize
	start := page*pageSize + 1

	elements := make([]map[string]any, 0, len(pagePapers)+6)

	// Header info
	headerText := fmt.Sprintf("共 **%d** 篇文章", totalCount)
	if totalCount > pageSize {
		end := page*pageSize + len(pagePapers)
		headerText = fmt.Sprintf("共 **%d** 篇文章（第 %d-%d 篇）", totalCount, start, end)
	}
	elements = append(elements, mdElement(headerText))

	// Helper: conditionally attach search keyword to button value
	btnExtra := func(base map[string]string) map[string]string {
		if searchKeyword != "" {
			base["search"] = searchKeyword
		}
		return base
	}

	// Paper entries
	for i, p := range pagePapers {
		title := p.Title
		if title == "" {
			title = "Paper " + p.ShortRef()
		}
		ratingStr := ""
		if p.Rating > 0 {
			ratingStr = fmt.Sprintf(" ⭐%d", p.Rating)
		}
		timeStr := p.UpdatedAt.Format("01-02 15:04")

		text := fmt.Sprintf("**%d.** %s%s\n_%s · %s_", start+i, title, ratingStr, timeStr, p.ShortRef())

		var btn map[string]any
		if p.Ref() == selectedID {
			btn = map[string]any{
				"tag":      "button",
				"text":     plainText("已选中 ✓"),
				"type":     "primary",
				"disabled": true,
				"width":    "default",
			}
		} else {
			btn = buttonElement("选择", "open:"+p.Ref(), "default", btnExtra(map[string]string{"paper_id": p.Ref(), "page": strconv.Itoa(page)}))
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
					"elements":       []map[string]any{btn},
				},
			},
		}
		elements = append(elements, colSet)
		if i < len(pagePapers)-1 {
			elements = append(elements, hrElement())
		}
	}

	// Pagination controls
	if totalCount > pageSize {
		elements = append(elements, hrElement())

		prevBtn := map[string]any{
			"tag":   "button",
			"text":  plainText("« 上一页"),
			"type":  "default",
			"width": "default",
		}
		if page > 0 {
			prevBtn["value"] = btnExtra(map[string]string{"action": "page_nav", "page": strconv.Itoa(page - 1)})
		} else {
			prevBtn["disabled"] = true
		}

		nextBtn := map[string]any{
			"tag":   "button",
			"text":  plainText("下一页 »"),
			"type":  "default",
			"width": "default",
		}
		if page < totalPages-1 {
			nextBtn["value"] = btnExtra(map[string]string{"action": "page_nav", "page": strconv.Itoa(page + 1)})
		} else {
			nextBtn["disabled"] = true
		}

		pageInfo := map[string]any{
			"tag":       "column_set",
			"flex_mode": "bisect",
			"columns": []map[string]any{
				{
					"tag":            "column",
					"width":          "weighted",
					"weight":         1,
					"vertical_align": "center",
					"elements":       []map[string]any{prevBtn},
				},
				{
					"tag":            "column",
					"width":          "weighted",
					"weight":         1,
					"vertical_align": "center",
					"elements":       []map[string]any{nextBtn},
				},
			},
		}
		elements = append(elements, pageInfo)
		elements = append(elements, noteElement(fmt.Sprintf("第 %d/%d 页 · 点击「选择」切换当前文章", page+1, totalPages)))
	} else {
		elements = append(elements, hrElement())
		elements = append(elements, noteElement("点击「选择」切换当前文章，然后可以直接提问。"))
	}

	c["body"] = map[string]any{"elements": elements}
	return c
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

// RecommendCardItem holds the display data for one article in the daily recommendation card.
type RecommendCardItem struct {
	ID       string
	Title    string // translated (if available) or original
	Abstract string // translated (if available) or original, full text
	// PDFURL is the URL the per-article arXiv icon button opens. Derived
	// from the abs URL at construction time (abs → pdf). Empty for non-arXiv
	// sources; the card builder hides the button in that case.
	PDFURL string
	Score  float64
	// Status mirrors articles.status: 0=unread, 1=activated, 2=liked,
	// -1=disliked, 3=read. The card builder uses it to render buttons in
	// their post-action state so the user gets visual feedback after a click.
	Status     int
	AXNetVotes *int
}

// renderRecommendButton builds one of the like / dislike / activate buttons
// on the daily recommendation card. When active is true the button is shown
// in its post-action state by changing its `type` to activeType (e.g.
// "primary" → blue background, "danger" → red background). The text is
// kept identical so the icon stays clean; the color change is the only
// visual cue. We intentionally do NOT set disabled: true, because in
// Feishu's renderer disabled overrides the type and forces the button
// back to the default grey, which defeats the purpose.
func renderRecommendButton(emoji, articleID, action string, active bool, activeType string) map[string]any {
	val := map[string]string{"action": action + ":" + articleID, "paper_id": articleID}
	btnType := "default"
	if active {
		btnType = activeType
	}
	return map[string]any{
		"tag":   "button",
		"text":  plainText(emoji),
		"type":  btnType,
		"value": val,
		"width": "default",
	}
}

// renderRecommendLinkButton builds the per-article arXiv "open PDF" button.
// It uses Card 2.0's `behaviors: open_url` (the modern way; the older
// top-level `url` field is deprecated per Feishu docs) so a click opens
// the PDF in the system browser on both desktop and mobile without any
// bot round-trip. The four platform URL fields are all set to the same
// arXiv URL — Feishu falls back to default_url when a platform-specific
// one is omitted, but being explicit is clearer.
func renderRecommendLinkButton(emoji, pdfURL string) map[string]any {
	return map[string]any{
		"tag":  "button",
		"text": plainText(emoji),
		"type": "default",
		"behaviors": []map[string]any{
			{
				"type":        "open_url",
				"default_url": pdfURL,
				"android_url": pdfURL,
				"ios_url":     pdfURL,
				"pc_url":      pdfURL,
			},
		},
		"width": "default",
	}
}

// arxivAbsToPDF converts `https://arxiv.org/abs/<id>` to
// `https://arxiv.org/pdf/<id>` (with `.pdf` suffix on the id). Returns
// the original URL unchanged when it's not an arXiv abs link, so
// non-arXiv RSS sources (defensive, doesn't happen today) keep their
// original link target.
func arxivAbsToPDF(absURL string) string {
	const prefix = "https://arxiv.org/abs/"
	if !strings.HasPrefix(absURL, prefix) {
		return absURL
	}
	return "https://arxiv.org/pdf/" + strings.TrimPrefix(absURL, prefix) + ".pdf"
}

// recommendPageSize is the number of articles per card when the daily
// recommendation is split into multiple cards. Keeps each card well under
// Feishu's ~30KB JSON / 200-element limits while showing full abstracts.
//
// At 8 articles the tree-element count (divs, columns, buttons, texts,
// hrs, footer) stays at ~166 which leaves ~34 headroom for any
// table-expanded elements or longer abstracts.
const recommendPageSize = 8

// ─── Daily Recommendation Card ───

// recommendAbstractFallbackLen is the per-abstract truncation length used
// when a page of full abstracts would push the card JSON over maxCardJSONBytes.
// Full abstracts are the default; this only kicks in for unusually long abstracts.
const recommendAbstractFallbackLen = 500

// buildDailyRecommendCard renders one page of the daily recommendation.
// page is 1-indexed; totalPages is the number of cards that will be sent.
// If the full-abstract card would exceed ANY of Feishu's hard limits
// (~30KB JSON, 200 elements, 5 markdown tables), abstracts are first
// stripped of any markdown tables (replaced with "_(table omitted)_")
// and then truncated to recommendAbstractFallbackLen runes. A warning
// is logged. Without the table-count check, a single LaTeX tabular
// rendered as a markdown table in the abstract of >5 articles makes
// Feishu reject the card with error 11310 (card table number over limit).
func buildDailyRecommendCard(items []RecommendCardItem, page, totalPages int) string {
	cardJSON := buildDailyRecommendCardRaw(items, page, totalPages)
	if dailyCardFitsAllLimits(items, cardJSON) {
		return cardJSON
	}

	// Fallback: strip tables and truncate abstracts so the card fits.
	log.Printf("[feishu] daily card %d/%d exceeds limit (json=%dB tables=%d elems≈%d), stripping tables + truncating abstracts to %d runes",
		page, totalPages, len(cardJSON),
		countCardMdTables(items), estimateCardElements(items),
		recommendAbstractFallbackLen)
	truncated := make([]RecommendCardItem, len(items))
	copy(truncated, items)
	for i := range truncated {
		if countMdTables(truncated[i].Abstract) > 0 {
			truncated[i].Abstract = stripMdTables(truncated[i].Abstract)
		}
		if runes := []rune(truncated[i].Abstract); len(runes) > recommendAbstractFallbackLen {
			truncated[i].Abstract = string(runes[:recommendAbstractFallbackLen]) + "..."
		}
	}
	return buildDailyRecommendCardRaw(truncated, page, totalPages)
}

// dailyCardFitsAllLimits returns true when the rendered card respects all
// three Feishu hard limits: JSON byte size, 200-element count, and 5-table
// count. See buildDailyRecommendCard for why we must check all three.
func dailyCardFitsAllLimits(items []RecommendCardItem, cardJSON string) bool {
	if len(cardJSON) > maxCardJSONBytes {
		return false
	}
	if countCardMdTables(items) > maxCardMdTables {
		return false
	}
	if estimateCardElements(items) > maxCardElements {
		return false
	}
	return true
}

// countCardMdTables sums the markdown table count across every abstract in
// the card. Feishu caps a single card at 5 tables total (error 11310).
func countCardMdTables(items []RecommendCardItem) int {
	n := 0
	for _, it := range items {
		n += countMdTables(it.Abstract)
	}
	return n
}

// estimateCardElements counts the number of tree-level elements (nodes
// with a "tag" field) that Feishu will see in a daily-recommend card.
// Every div, lark_md text, column_set, column, button, plain_text,
// hr, and the header title all count toward the 200-element hard limit.
// Per article with all 4 buttons: 3 divs + 3 lark_md texts + 1 column_set
// + 4 columns + 4 buttons + 4 plain_texts = 19 tree elements. With 3
// buttons (no PDFURL) it's 16. Between articles we add 1 hr.
func estimateCardElements(items []RecommendCardItem) int {
	perArticle := 19 // 3 divs + 3 lark_md + 1 column_set + 4 cols + 4 btns + 4 plain_texts
	// Defensive: if anyone adjusts the button count later, don't crash.
	if len(items) > 0 && items[0].PDFURL == "" {
		perArticle = 16 // 3 divs + 3 lark_md + 1 column_set + 3 cols + 3 btns + 3 plain_texts
	}
	n := len(items) * perArticle
	if n > 0 {
		n += len(items) - 1 // hr between items
	}
	n += 1 // card header title (plain_text)
	n += 1 // hr before mark-read button
	n += 1 // mark-read button
	n += 1 // mark-read button text (plain_text)
	n += 1 // hr after mark-read
	n += 1 // footer note (div)
	n += 1 // footer note text (plain_text)
	return n
}

// buildDailyRecommendCardRaw is the inner builder that actually constructs
// the card JSON. Callers should use buildDailyRecommendCard instead, which
// applies the size fallback.
func buildDailyRecommendCardRaw(items []RecommendCardItem, page, totalPages int) string {
	c := cardBase()
	if totalPages > 1 {
		c["header"] = cardHeader(fmt.Sprintf("📅 今日论文推荐 (%d/%d)", page, totalPages), "blue")
	} else {
		c["header"] = cardHeader("📅 今日论文推荐", "blue")
	}

	elements := make([]map[string]any, 0, len(items)+4)
	pageIDs := make([]string, 0, len(items))

	for i, item := range items {
		title := item.Title
		pageIDs = append(pageIDs, item.ID)
		scoreStr := fmt.Sprintf("%.2f", item.Score)
		voteStr := ""
		if item.AXNetVotes != nil {
			voteStr = fmt.Sprintf(" 🔬 %d", *item.AXNetVotes)
		}

		// Title row uses heading size; score + abstract use normal size.
		// (Markdown elements cannot change text_size, so we use div+lark_md.)
		titleEl := map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":       "lark_md",
				"content":   fmt.Sprintf("**%d. %s**", i+1, title),
				"text_size": "heading",
			},
		}
		scoreEl := map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":       "lark_md",
				"content":   fmt.Sprintf("_兴趣分: %s%s_", scoreStr, voteStr),
				"text_size": "normal",
			},
		}
		var abstractEl map[string]any
		if item.Abstract != "" {
			abstractEl = map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":       "lark_md",
					"content":   item.Abstract,
					"text_size": "normal",
				},
			}
		}

		// Like/Dislike/Activate buttons. Status-driven so a previous click
		// (from this card or the WebUI) is reflected as a highlighted +
		// disabled button instead of looking like a fresh unclicked button.
		btnLike := renderRecommendButton("👍", item.ID, "recommend:like", item.Status == 2, "primary")
		btnDislike := renderRecommendButton("👎", item.ID, "recommend:dislike", item.Status == -1, "danger")
		btnActivate := renderRecommendButton("🤖", item.ID, "recommend:activate", item.Status == 1, "primary")

		// arXiv link button — opens the PDF in the system browser. Renders
		// only when we have a PDF URL (we always do for arXiv RSS items; the
		// guard is defensive in case the pool is ever seeded from non-arXiv
		// sources).
		var btnArxiv map[string]any
		if item.PDFURL != "" {
			btnArxiv = renderRecommendLinkButton("📑", item.PDFURL)
		}

		// Layout: title, then score+votes, then abstract, then 4 horizontal buttons
		// stacked as their own row. Putting the buttons on the same row as the score
		// forced a wrap on narrow phone screens; giving them their own row keeps
		// each row single-purpose and easy to scan.
		columns := []map[string]any{
			{
				"tag":            "column",
				"width":          "auto",
				"vertical_align": "center",
				"elements":       []map[string]any{btnLike},
			},
			{
				"tag":            "column",
				"width":          "auto",
				"vertical_align": "center",
				"elements":       []map[string]any{btnDislike},
			},
			{
				"tag":            "column",
				"width":          "auto",
				"vertical_align": "center",
				"elements":       []map[string]any{btnActivate},
			},
		}
		if btnArxiv != nil {
			columns = append(columns, map[string]any{
				"tag":            "column",
				"width":          "auto",
				"vertical_align": "center",
				"elements":       []map[string]any{btnArxiv},
			})
		}
		btnRow := map[string]any{
			"tag":       "column_set",
			"flex_mode": "none",
			"columns":   columns,
		}
		elements = append(elements, titleEl, scoreEl)
		if abstractEl != nil {
			elements = append(elements, abstractEl)
		}
		elements = append(elements, btnRow)
		if i < len(items)-1 {
			elements = append(elements, hrElement())
		}
	}

	// Separator before the bulk-action button so it reads as a footer-level
	// action, distinct from the per-article buttons above.
	elements = append(elements, hrElement())

	// "Mark all as read" button — bulk-marks every article in this page.
	// (Hover-to-read is the WebUI affordance; this is the Feishu equivalent.)
	// The button highlights (primary blue + "已标记" label) when no article
	// on the page is still in the unread state (status==0). Because status
	// is now priority-merged (liked/disliked/activated statuses are not
	// overwritten by a bulk mark-read), the natural state after the user
	// clicks "一键已阅" AND any later like/dislike interaction still leaves
	// every article non-unread, so the highlight is stable across clicks
	// instead of flickering. We do NOT set disabled: true because the
	// Feishu renderer turns disabled buttons grey regardless of type, which
	// would hide the blue.
	markReadBtn := map[string]any{
		"tag":  "button",
		"text": plainText(fmt.Sprintf("✅ 一键已阅本页 %d 篇", len(pageIDs))),
		"type": "default",
		"value": map[string]any{
			"action":    "recommend:mark-read-page",
			"paper_ids": pageIDs,
		},
		"width": "default",
	}
	allRead := len(items) > 0
	for _, it := range items {
		if it.Status == 0 {
			allRead = false
			break
		}
	}
	if allRead {
		markReadBtn["text"] = plainText(fmt.Sprintf("✅ 已标记本页 %d 篇", len(pageIDs)))
		markReadBtn["type"] = "primary"
	}
	elements = append(elements, markReadBtn)

	// Footer
	elements = append(elements, hrElement())
	elements = append(elements, noteElement("👍 点赞 · 👎 点踩 · 🤖 总结后对话 · 📑 打开 PDF"))

	c["body"] = map[string]any{"elements": elements}
	return marshalCard(c)
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
		pos  int
		good int // quality: 2=double newline, 1=single newline, 0=forced
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

// formatInt formats an integer with thousands separator (e.g. 12345 → "12,345").
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	// Convert to string in chunks of 3 digits
	var parts []string
	for n > 0 {
		part := n % 1000
		n /= 1000
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%03d", part))
		} else {
			parts = append(parts, fmt.Sprintf("%d", part))
		}
	}
	// Reverse and join
	result := strings.Join(reverse(parts), ",")
	if neg {
		result = "-" + result
	}
	return result
}

func reverse(s []string) []string {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return s
}

func paperShortRef(p session.Paper) string {
	if p.SessionID != "" && len(p.SessionID) >= 8 {
		return p.SessionID[:8]
	}
	return fmt.Sprintf("%d", p.ID)
}
