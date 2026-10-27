package urlparse

import (
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

	// 2. Extract every table (figure-wrapped or stand-alone tabular) and swap it
	// for a placeholder, so stripping cannot mangle the cell structure.
	segs, tables := extractTables(html)
	html = insertTablePlaceholders(html, segs, tables)

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

	// 8. A table dropped on purpose (a decorative tabular) leaves its blank
	// lines behind, and restored blocks add their own. Fence-aware: restored
	// prompt listings keep the empty lines inside them.
	html = collapseBlankLines(html)

	// 9. Prepend title
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

// stripHTMLTags removes all HTML tags but keeps text content. htmlTagRe is
// package level (shared with the title/abstract helpers in urlparse.go) because
// this runs once per table cell.
func stripHTMLTags(s string) string {
	return htmlTagRe.ReplaceAllString(s, "")
}

// ---------------------------------------------------------------------------
// Entity decoding
// ---------------------------------------------------------------------------

// numericEntityRe is package level: decodeEntities runs once per table cell.
var numericEntityRe = regexp.MustCompile(`&#(\d+);`)

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
	s = numericEntityRe.ReplaceAllStringFunc(s, func(m string) string {
		parts := numericEntityRe.FindStringSubmatch(m)
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

var blankLinesRe = regexp.MustCompile(`\n{3,}`)

func normalizeWhitespace(s string) string {
	// Collapse multiple blank lines to at most 2
	s = blankLinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// collapseBlankLines does the same for the finished document, but leaves fenced
// code blocks alone: their empty lines are content, and the verbatim fallback for
// <figure class="ltx_table"> listings restores one of those fences.
func collapseBlankLines(s string) string {
	var out []string
	inFence, blanks := false, 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence, blanks = !inFence, 0
			out = append(out, line)
			continue
		}
		if strings.TrimSpace(line) == "" {
			if !inFence {
				blanks++
				if blanks > 1 {
					continue
				}
			}
			out = append(out, line)
			continue
		}
		blanks = 0
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
