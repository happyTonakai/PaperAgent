package urlparse

import (
	"fmt"
	"regexp"
	"strings"
)

// HTMLToMarkdown converts arXiv HTML (LaTeXML format) to Markdown, preserving
// tables, math notation, and other document structure.
func HTMLToMarkdown(html string) string {
	if html == "" {
		return ""
	}

	// 0. Convert MathML <math> tags to $LaTeX$ notation BEFORE any stripping
	html = convertMathMLToLaTeX(html)

	// 1. Extract document title
	title := extractTitle(html)

	// 2. Extract all tables and replace with placeholders
	// First: figure-wrapped tables (<figure class="ltx_table">)
	// Then: bare tables (<table class="ltx_tabular"> not inside a figure)
	tables := extractAllTables(html)
	html = replaceAllTablePlaceholders(html)

	// 3. Extract body content (inside ltx_page_content div, before </article>)
	html = extractArticleBody(html)

	// 4. Convert headings (h2-h6) to Markdown ## notation BEFORE tag stripping
	html = convertHTMLHeadings(html)

	// 5. Strip all HTML tags (keeping placeholder text intact)
	html = stripAllHTMLTags(html)

	// 5. Decode HTML entities
	html = decodeEntities(html)

	// 6. Normalize whitespace
	html = normalizeWhitespace(html)

	// 7. Restore table markdown
	html = restoreTables(html, tables)

	// 8. Prepend title
	if title != "" {
		html = "# " + title + "\n\n" + html
	}

	return strings.TrimSpace(html)
}

// ---------------------------------------------------------------------------
// Heading conversion
// ---------------------------------------------------------------------------

var headingRe = regexp.MustCompile(`(?is)<h(\d)\s+class="ltx_title[^"]*"[^>]*>\s*<a[^>]*>.*?</a>\s*(.*?)</h\d>`)
var headingPlainRe = regexp.MustCompile(`(?is)<h(\d)[^>]*>(.*?)</h\d>`)

// convertHTMLHeadings converts LaTeXML headings (h2 class="ltx_title ltx_title_section") to
// Markdown ## notation. h1 is skipped (reserved for document title).
func convertHTMLHeadings(html string) string {
	// First try the structured form with <a> anchor inside
	html = headingRe.ReplaceAllStringFunc(html, func(m string) string {
		parts := headingRe.FindStringSubmatch(m)
		if len(parts) < 3 {
			return m
		}
		level := parts[1]
		text := stripHTMLTags(parts[2])
		text = decodeEntities(text)
		text = strings.TrimSpace(text)
		if text == "" {
			return ""
		}
		prefix := strings.Repeat("#", parseHeadingLevel(level))
		return "\n" + prefix + " " + text + "\n"
	})
	// Fallback: plain heading without anchor
	html = headingPlainRe.ReplaceAllStringFunc(html, func(m string) string {
		// Skip if already matched by the structured regex (looks like ## already there)
		if strings.HasPrefix(strings.TrimSpace(m), "#") {
			return m
		}
		parts := headingPlainRe.FindStringSubmatch(m)
		if len(parts) < 3 {
			return m
		}
		level := parts[1]
		if level == "1" {
			return m // h1 is handled separately
		}
		text := stripHTMLTags(parts[2])
		text = decodeEntities(text)
		text = strings.TrimSpace(text)
		if text == "" {
			return ""
		}
		prefix := strings.Repeat("#", parseHeadingLevel(level))
		return "\n" + prefix + " " + text + "\n"
	})
	return html
}

func parseHeadingLevel(level string) int {
	switch level {
	case "2":
		return 2
	case "3":
		return 3
	case "4":
		return 4
	case "5":
		return 5
	default:
		return 6
	}
}

// ---------------------------------------------------------------------------
// MathML → LaTeX conversion
// ---------------------------------------------------------------------------

var mathMLAnnotationRe = regexp.MustCompile(`(?is)<math[^>]*>.*?<annotation[^>]*encoding="application/x-tex"[^>]*>(.*?)</annotation>.*?</math>`)

// convertMathMLToLaTeX replaces <math> tags containing x-tex annotations with
// inline ($...$) or display ($$...$$) LaTeX notation.
func convertMathMLToLaTeX(html string) string {
	return mathMLAnnotationRe.ReplaceAllStringFunc(html, func(m string) string {
		parts := mathMLAnnotationRe.FindStringSubmatch(m)
		if len(parts) < 2 {
			return m
		}
		latex := strings.TrimSpace(parts[1])
		if latex == "" {
			return ""
		}
		// Check if the opening <math> tag has display="block" — only look in the tag prefix,
		// not in the annotation body (which may contain the literal string)
		tagEnd := strings.Index(m, ">")
		tagPrefix := m[:tagEnd]
		if strings.Contains(tagPrefix, `display="block"`) || strings.Contains(tagPrefix, `display=block`) {
			return "\n$$\n" + latex + "\n$$\n"
		}
		return "$" + latex + "$"
	})
}

// ---------------------------------------------------------------------------
// Title
// ---------------------------------------------------------------------------

var titleRe = regexp.MustCompile(`(?is)<h1\s+class="ltx_title\s+ltx_title_document"[^>]*>(.*?)</h1>`)

func extractTitle(html string) string {
	m := titleRe.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return stripHTMLTags(m[1])
}

// ---------------------------------------------------------------------------
// Body extraction
// ---------------------------------------------------------------------------

func extractArticleBody(html string) string {
	// Match class containing ltx_page_content (possibly multi-class like "ltx_page_main ltx_page_content")
	re := regexp.MustCompile(`class="[^"]*\bltx_page_content\b[^"]*"`)
	loc := re.FindStringIndex(html)
	if loc == nil {
		return html
	}
	idx := loc[0]
	openEnd := strings.Index(html[idx:], ">")
	if openEnd < 0 {
		return html
	}
	contentStart := idx + openEnd + 1
	articleEnd := strings.Index(html[contentStart:], "</article>")
	if articleEnd < 0 {
		return html[contentStart:]
	}
	return html[contentStart : contentStart+articleEnd]
}

// ---------------------------------------------------------------------------
// Table extraction
// ---------------------------------------------------------------------------

var tableFigureRe = regexp.MustCompile(`(?is)<figure[^>]*class="[^"]*ltx_table[^"]*"[^>]*>(.*?)</figure>`)
var tableCaptionRe = regexp.MustCompile(`(?is)<figcaption[^>]*>(.*?)</figcaption>`)

// Some LaTeXML "tables" carry no table at all: a <figure class="ltx_table"> wrapping
// a verbatim block, used for prompt templates and similar listings. Keep the text
// instead of emitting a caption with no content.
var verbatimBlockRe = regexp.MustCompile(`(?is)<pre[^>]*>(.*?)</pre>`)

// LaTeXML sometimes emits a table built purely from <span> elements (no <table>
// at all) when the table sits inside a flex panel: the ltx_tabular / ltx_tr /
// ltx_td classes end up on spans. Those are real tables and are recovered below.
// Span elements nest heavily, so the extent of each element is found by counting
// span depth rather than by a non-greedy regex.
var spanTabularRe = regexp.MustCompile(`(?is)<span[^>]*class="[^"]*ltx_tabular[^"]*"[^>]*>`)
var spanTrRe = regexp.MustCompile(`(?is)<span[^>]*class="[^"]*ltx_tr[^"]*"[^>]*>`)
var spanTdRe = regexp.MustCompile(`(?is)<span[^>]*class="[^"]*ltx_td[^"]*"[^>]*>`)
var spanAnyRe = regexp.MustCompile(`(?is)<span\b[^>]*>|</span>`)

// NOTE: the class attribute is matched with a leading `[^>]*\b`, not as the first
// attribute. LaTeXML emits <table id="S3.T1.4" class="ltx_tabular ..."> so anchoring
// on `<table\s+class=` never matched and every table silently degraded to a
// caption-only placeholder.
var tabularRe = regexp.MustCompile(`(?is)<table[^>]*\bclass="[^"]*ltx_tabular[^"]*"[^>]*>(.*?)</table>`)
var bareTableRe = regexp.MustCompile(`(?is)<table[^>]*\bclass="[^"]*ltx_tabular[^"]*"[^>]*>.*?</table>`)
var trRe = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
var tdRe = regexp.MustCompile(`(?is)<t[dh][^>]*>(.*?)</t[dh]>`)
var wsRe = regexp.MustCompile(`\s+`)

type tableInfo struct {
	placeholder string
	markdown    string
}

func extractAllTables(html string) []tableInfo {
	var tables []tableInfo

	// Step 1: figure-wrapped tables (<figure class="ltx_table">)
	figMatches := tableFigureRe.FindAllStringSubmatch(html, -1)
	for _, m := range figMatches {
		tableMD := convertTableToMarkdown(m[0])
		tables = append(tables, tableInfo{
			placeholder: fmt.Sprintf("__TABLE_PLACEHOLDER_%d__", len(tables)),
			markdown:    tableMD,
		})
	}

	// Step 2: bare tables (<table class="ltx_tabular"> not inside a <figure>)
	// Remove figure-wrapped tables first to avoid double-counting
	htmlStripped := tableFigureRe.ReplaceAllLiteralString(html, "")
	bareMatches := bareTableRe.FindAllStringSubmatch(htmlStripped, -1)
	for _, m := range bareMatches {
		tableMD := convertTableToMarkdown(m[0])
		tables = append(tables, tableInfo{
			placeholder: fmt.Sprintf("__TABLE_PLACEHOLDER_%d__", len(tables)),
			markdown:    tableMD,
		})
	}

	return tables
}

func replaceAllTablePlaceholders(html string) string {
	counter := 0

	// Step 1: Replace figure-wrapped tables (<figure class="ltx_table">)
	result := tableFigureRe.ReplaceAllStringFunc(html, func(m string) string {
		p := fmt.Sprintf("__TABLE_PLACEHOLDER_%d__", counter)
		counter++
		return p
	})

	// Step 2: Replace bare tables (<table class="ltx_tabular"> not yet replaced)
	result = bareTableRe.ReplaceAllStringFunc(result, func(m string) string {
		p := fmt.Sprintf("__TABLE_PLACEHOLDER_%d__", counter)
		counter++
		return p
	})

	return result
}

func convertTableToMarkdown(figureHTML string) string {
	caption := ""
	if m := tableCaptionRe.FindStringSubmatch(figureHTML); len(m) >= 2 {
		caption = stripHTMLTags(m[1])
		caption = strings.TrimSpace(caption)
	}

	m := tabularRe.FindStringSubmatch(figureHTML)
	if len(m) >= 2 {
		return renderMarkdownTable(parseHTMLTableRows(m[1]), detectHTMLColumnAlignments(m[1]), caption)
	}

	// Span-based table (no <table> element).
	if loc := spanTabularRe.FindStringIndex(figureHTML); loc != nil {
		if rows, aligns, ok := parseSpanTableRows(figureHTML[loc[0]:]); ok {
			return renderMarkdownTable(rows, aligns, caption)
		}
	}

	// No real table: fall back to a verbatim block if the figure wraps one,
	// so prompt templates and similar listings are not silently dropped.
	if pre := verbatimBlockRe.FindStringSubmatch(figureHTML); len(pre) >= 2 {
		// Strip tags before decoding entities: a literal "<|...|>" in the source
		// is entity-escaped and must survive tag stripping.
		body := decodeEntities(stripHTMLTags(pre[1]))
		body = strings.Trim(body, " \t\n")
		if body != "" {
			if caption != "" {
				return "> " + caption + "\n\n```\n" + body + "\n```\n"
			}
			return "```\n" + body + "\n```\n"
		}
	}

	// Nothing recoverable beyond the caption.
	if caption != "" {
		return "> " + caption + "\n"
	}
	return ""
}

// parseHTMLTableRows extracts rows from <table>-based LaTeXML markup.
func parseHTMLTableRows(tabularHTML string) [][]string {
	var rows [][]string
	for _, tr := range trRe.FindAllStringSubmatch(tabularHTML, -1) {
		var cells []string
		for _, td := range tdRe.FindAllStringSubmatch(tr[1], -1) {
			cells = append(cells, cleanHTMLCellText(td[1]))
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}
	return rows
}

// parseSpanTableRows recovers rows and column alignments from span-based table
// markup. Returns ok=false when no rows could be recovered.
func parseSpanTableRows(html string) ([][]string, []string, bool) {
	inner, _, ok := innerOfBalancedSpan(html)
	if !ok {
		return nil, nil, false
	}

	var rows [][]string
	var alignments []string
	for _, trLoc := range spanTrRe.FindAllStringIndex(inner, -1) {
		trHTML := inner[trLoc[0]:]
		trInner, _, ok := innerOfBalancedSpan(trHTML)
		if !ok {
			continue
		}

		var cells []string
		var aligns []string
		for _, tdLoc := range spanTdRe.FindAllStringIndex(trInner, -1) {
			tdHTML := trInner[tdLoc[0]:]
			tdInner, _, ok := innerOfBalancedSpan(tdHTML)
			if !ok {
				continue
			}
			cells = append(cells, cleanHTMLCellText(tdInner))
			aligns = append(aligns, alignFromAttrs(tdHTML[:tdLoc[1]-tdLoc[0]]))
		}
		if len(cells) == 0 {
			continue
		}
		if len(alignments) == 0 {
			alignments = aligns
		}
		rows = append(rows, cells)
	}

	if len(rows) == 0 {
		return nil, nil, false
	}
	return rows, alignments, true
}

// innerOfBalancedSpan returns the inner HTML of the <span> element that html
// starts with, plus the offset just past its closing tag. Span elements nest, so
// the extent is determined by counting open/close span tags.
func innerOfBalancedSpan(html string) (string, int, bool) {
	tags := spanAnyRe.FindAllStringIndex(html, -1)
	if len(tags) == 0 || tags[0][0] != 0 {
		return "", 0, false
	}
	if strings.HasPrefix(html[tags[0][0]:tags[0][1]], "</") {
		return "", 0, false
	}

	depth := 0
	for _, t := range tags {
		if strings.HasPrefix(html[t[0]:t[1]], "</") {
			depth--
			if depth == 0 {
				return html[tags[0][1]:t[0]], t[1], true
			}
			continue
		}
		depth++
	}
	return "", 0, false
}

// cleanHTMLCellText normalises the text content of a single HTML table cell.
// (Distinct from cleanCellText in tex2md.go, which handles LaTeX cell source.)
func cleanHTMLCellText(cellHTML string) string {
	c := stripHTMLTags(cellHTML)
	c = decodeEntities(c)
	c = wsRe.ReplaceAllString(c, " ")
	return strings.TrimSpace(c)
}

// alignFromAttrs maps LaTeXML alignment classes to a markdown column alignment.
func alignFromAttrs(attrs string) string {
	switch {
	case strings.Contains(attrs, "ltx_align_right"):
		return "right"
	case strings.Contains(attrs, "ltx_align_center"):
		return "center"
	default:
		return "left"
	}
}

// renderMarkdownTable renders parsed rows as a GitHub-flavoured markdown table.
func renderMarkdownTable(rows [][]string, alignments []string, caption string) string {
	if len(rows) == 0 {
		if caption != "" {
			return "> " + caption + "\n"
		}
		return ""
	}

	// Build markdown table
	var buf strings.Builder
	header := rows[0]
	buf.WriteString("| ")
	for i, c := range header {
		if i > 0 {
			buf.WriteString(" | ")
		}
		buf.WriteString(c)
	}
	buf.WriteString(" |\n|")
	for i := 0; i < len(header); i++ {
		align := ""
		if i < len(alignments) {
			align = alignments[i]
		}
		switch align {
		case "right":
			buf.WriteString(" ---:|")
		case "center":
			buf.WriteString(" :---:|")
		default:
			buf.WriteString(" :---|")
		}
	}
	buf.WriteString("\n")
	for _, row := range rows[1:] {
		buf.WriteString("| ")
		for i, c := range row {
			if i > 0 {
				buf.WriteString(" | ")
			}
			buf.WriteString(c)
		}
		for i := len(row); i < len(header); i++ {
			buf.WriteString(" |")
		}
		buf.WriteString(" |\n")
	}
	if caption != "" {
		buf.WriteString("\n*" + caption + "*\n")
	}
	return buf.String()
}

// detectHTMLColumnAlignments extracts alignment from the first row's td classes
// (ltx_align_left, ltx_align_center, ltx_align_right).
func detectHTMLColumnAlignments(tabularHTML string) []string {
	trMatch := trRe.FindStringSubmatch(tabularHTML)
	if len(trMatch) < 2 {
		return nil
	}

	tdRe := regexp.MustCompile(`(?is)<t[dh]\b([^>]*)>(.*?)</t[dh]>`)
	tdMatches := tdRe.FindAllStringSubmatch(trMatch[1], -1)

	var aligns []string
	for _, td := range tdMatches {
		aligns = append(aligns, alignFromAttrs(td[1]))
	}
	return aligns
}

// ---------------------------------------------------------------------------
// HTML tag stripping
// ---------------------------------------------------------------------------

func stripAllHTMLTags(html string) string {
	// Remove <script> and <style> blocks entirely
	reScript := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = reScript.ReplaceAllString(html, "")
	reStyle := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = reStyle.ReplaceAllString(html, "")

	// Convert <br> to newlines
	reBr := regexp.MustCompile(`(?is)<br\s*/?>`)
	html = reBr.ReplaceAllString(html, "\n")

	// Replace block-level tags with newlines for readability
	// h[1-6] is excluded because headings are already converted to ## notation
	reBlock := regexp.MustCompile(`(?is)</?(?:p|div|li|tr|td|th|blockquote|section|pre|article|header|footer|nav|figure|figcaption|table|thead|tbody|tfoot|caption|col|colgroup|dl|dt|dd|address)[^>]*>`)
	html = reBlock.ReplaceAllString(html, "\n")

	// Strip all remaining tags
	reTag := regexp.MustCompile(`<[^>]+>`)
	html = reTag.ReplaceAllString(html, "")

	return html
}

// stripHTMLTags removes all HTML tags but keeps text content.
func stripHTMLTags(s string) string {
	reTag := regexp.MustCompile(`<[^>]+>`)
	return reTag.ReplaceAllString(s, "")
}

// ---------------------------------------------------------------------------
// Entity decoding
// ---------------------------------------------------------------------------

func decodeEntities(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&#x27;", "'")
	s = strings.ReplaceAll(s, "&#x60;", "`")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&mdash;", "—")
	s = strings.ReplaceAll(s, "&ndash;", "–")
	s = strings.ReplaceAll(s, "&hellip;", "…")
	s = strings.ReplaceAll(s, "&dagger;", "†")
	s = strings.ReplaceAll(s, "&Dagger;", "‡")

	// Numeric entities
	reNum := regexp.MustCompile(`&#(\d+);`)
	s = reNum.ReplaceAllStringFunc(s, func(m string) string {
		parts := reNum.FindStringSubmatch(m)
		if len(parts) >= 2 {
			return string(rune(parseInt(parts[1])))
		}
		return m
	})
	return s
}

func parseInt(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Whitespace normalization
// ---------------------------------------------------------------------------

func normalizeWhitespace(s string) string {
	// Collapse multiple blank lines to at most 2
	re := regexp.MustCompile(`\n{3,}`)
	s = re.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// ---------------------------------------------------------------------------
// Table placeholder restoration
// ---------------------------------------------------------------------------

func restoreTables(html string, tables []tableInfo) string {
	for i, t := range tables {
		placeholder := fmt.Sprintf("__TABLE_PLACEHOLDER_%d__", i)
		html = strings.Replace(html, placeholder, "\n\n"+t.markdown+"\n\n", 1)
	}
	return html
}
