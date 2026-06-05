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
	tables := extractAllTables(html)
	html = replaceTableFigures(html)

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
var tabularRe = regexp.MustCompile(`(?is)<table\s+class="ltx_tabular[^"]*"[^>]*>(.*?)</table>`)
var trRe = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
var tdRe = regexp.MustCompile(`(?is)<td[^>]*>(.*?)</td>`)
var wsRe = regexp.MustCompile(`\s+`)

type tableInfo struct {
	placeholder string
	markdown    string
}

func extractAllTables(html string) []tableInfo {
	var tables []tableInfo
	matches := tableFigureRe.FindAllStringSubmatch(html, -1)
	for _, m := range matches {
		tableMD := convertTableToMarkdown(m[0])
		tables = append(tables, tableInfo{
			placeholder: fmt.Sprintf("__TABLE_PLACEHOLDER_%d__", len(tables)),
			markdown:    tableMD,
		})
	}
	return tables
}

func replaceTableFigures(html string) string {
	matches := tableFigureRe.FindAllStringSubmatchIndex(html, -1)
	if len(matches) == 0 {
		return html
	}
	var parts []string
	lastEnd := 0
	for i, loc := range matches {
		parts = append(parts, html[lastEnd:loc[0]])
		parts = append(parts, fmt.Sprintf("__TABLE_PLACEHOLDER_%d__", i))
		lastEnd = loc[1]
	}
	parts = append(parts, html[lastEnd:])
	return strings.Join(parts, "")
}

func convertTableToMarkdown(figureHTML string) string {
	caption := ""
	if m := tableCaptionRe.FindStringSubmatch(figureHTML); len(m) >= 2 {
		caption = stripHTMLTags(m[1])
		caption = strings.TrimSpace(caption)
	}

	m := tabularRe.FindStringSubmatch(figureHTML)
	if len(m) < 2 {
		if caption != "" {
			return "> " + caption + "\n"
		}
		return ""
	}

	tabularHTML := m[1]

	// Detect column alignments from the first row's td classes
	alignments := detectHTMLColumnAlignments(tabularHTML)

	// Parse rows
	trMatches := trRe.FindAllStringSubmatch(tabularHTML, -1)

	var rows [][]string
	for _, tr := range trMatches {
		tdMatches := tdRe.FindAllStringSubmatch(tr[1], -1)
		var cells []string
		for _, td := range tdMatches {
			c := stripHTMLTags(td[1])
			c = decodeEntities(c)
			c = wsRe.ReplaceAllString(c, " ")
			cells = append(cells, strings.TrimSpace(c))
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}

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

	tdRe := regexp.MustCompile(`(?is)<td\s+([^>]*)>(.*?)</td>`)
	tdMatches := tdRe.FindAllStringSubmatch(trMatch[1], -1)

	var aligns []string
	for _, td := range tdMatches {
		attrs := td[1]
		switch {
		case strings.Contains(attrs, "ltx_align_right"):
			aligns = append(aligns, "right")
		case strings.Contains(attrs, "ltx_align_center"):
			aligns = append(aligns, "center")
		default:
			aligns = append(aligns, "left")
		}
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
