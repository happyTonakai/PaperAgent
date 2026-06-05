package urlparse

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// FetchArxivAsMarkdownFromTeX downloads and converts arXiv TeX source to Markdown.
func FetchArxivAsMarkdownFromTeX(arxivID string) (string, error) {
	tex, err := downloadTeXSource(arxivID)
	if err != nil {
		return "", fmt.Errorf("download TeX source: %w", err)
	}
	return TeXToMarkdown(tex), nil
}

// downloadTeXSource downloads and extracts the main .tex content from arXiv e-print.
func downloadTeXSource(arxivID string) (string, error) {
	url := fmt.Sprintf("https://arxiv.org/e-print/%s", arxivID)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Try as tar.gz archive first
	tmpDir, err := os.MkdirTemp("", "arxiv2md-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	if tex, err := extractAndFlatten(data, tmpDir); err == nil && tex != "" {
		return tex, nil
	}

	// Plain .tex file
	if looksLikeTeX(string(data)) {
		return string(data), nil
	}

	return "", fmt.Errorf("unable to extract TeX source")
}

func extractAndFlatten(data []byte, tmpDir string) (string, error) {
	// Try gzip + tar
	reader := bytes.NewReader(data)
	gr, err := gzip.NewReader(reader)
	isGzip := err == nil
	if isGzip {
		defer gr.Close()
		if err := untarToDir(gr, tmpDir); err == nil {
			return flattenFromDir(tmpDir)
		}
	}

	// Try plain tar (no gzip)
	reader.Seek(0, io.SeekStart)
	if err := untarToDir(reader, tmpDir); err == nil {
		return flattenFromDir(tmpDir)
	}

	return "", fmt.Errorf("not a valid tar or tar.gz archive")
}

func untarToDir(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// Prevent path traversal
		if strings.Contains(hdr.Name, "..") || strings.HasPrefix(hdr.Name, "/") {
			continue
		}
		target := filepath.Join(dir, hdr.Name)
		// Ensure target is still within dir (defense in depth)
		if !strings.HasPrefix(target, filepath.Clean(dir)+string(filepath.Separator)) && target != filepath.Clean(dir) {
			continue
		}
		if hdr.FileInfo().IsDir() {
			os.MkdirAll(target, 0755)
			continue
		}
		os.MkdirAll(filepath.Dir(target), 0755)
		f, err := os.Create(target)
		if err != nil {
			continue
		}
		io.Copy(f, tr)
		f.Close()
	}
	return nil
}

func flattenFromDir(dir string) (string, error) {
	mainFile := findMainTeX(dir)
	if mainFile == "" {
		return "", fmt.Errorf("no main .tex file in %s", dir)
	}
	return flattenWithBase(dir, mainFile), nil
}

func findMainTeX(dir string) string {
	// First pass: look for common names with \documentclass
	common := []string{"main.tex", "paper.tex", "index.tex"}
	for _, name := range common {
		path := filepath.Join(dir, name)
		if content, err := os.ReadFile(path); err == nil {
			if looksLikeMainTeX(string(content)) {
				return path
			}
		}
	}

	// Second pass: find the longest .tex with \documentclass
	var bestPath string
	var bestLen int
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".tex") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if looksLikeMainTeX(string(content)) && len(content) > bestLen {
			bestPath = path
			bestLen = len(content)
		}
		return nil
	})
	return bestPath
}

func flattenWithBase(dir, mainFile string) string {
	visited := make(map[string]bool)
	var flatten func(string) string
	flatten = func(path string) string {
		content, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		tex := string(content)
		return inputRe.ReplaceAllStringFunc(tex, func(m string) string {
			parts := inputRe.FindStringSubmatch(m)
			if len(parts) < 2 {
				return m
			}
			name := strings.TrimSpace(parts[1])
			// Resolve relative to the current file's directory
			baseDir := filepath.Dir(path)
			resolved := resolveInputPath(baseDir, name)
			if resolved == "" {
				return "" // file not found, skip
			}
			canonical, _ := filepath.Abs(resolved)
			if visited[canonical] {
				return "" // circular
			}
			visited[canonical] = true
			return flatten(resolved)
		})
	}
	return flatten(mainFile)
}

func resolveInputPath(baseDir, name string) string {
	candidates := []string{name}
	if !strings.HasSuffix(name, ".tex") {
		candidates = append(candidates, name+".tex")
	}
	for _, c := range candidates {
		p := filepath.Join(baseDir, c)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Also try base name only (for \input{intro} where file is at root)
	base := filepath.Base(name)
	if base != name {
		p := filepath.Join(baseDir, base)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if !strings.HasSuffix(base, ".tex") {
			p := filepath.Join(baseDir, base+".tex")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

func looksLikeTeX(s string) bool {
	return strings.Contains(s, "\\documentclass") ||
		strings.Contains(s, "\\begin{document}") ||
		strings.Contains(s, "\\section")
}

func looksLikeMainTeX(s string) bool {
	return strings.Contains(s, "\\documentclass") || strings.Contains(s, "\\documentstyle")
}

var inputRe = regexp.MustCompile(`\\(?:input|include)\{([^}]+)\}`)

// ---------------------------------------------------------------------------
// TeX to Markdown conversion
// ---------------------------------------------------------------------------

// TeXToMarkdown converts LaTeX source to Markdown, preserving tables,
// math notation, section headings, and formatting.
func TeXToMarkdown(tex string) string {
	if tex == "" {
		return ""
	}

	// 1. Remove \iffalse ... \fi blocks
	tex = removeIfFalse(tex)

	// 2. Strip preamble (everything before \begin{document})
	tex = stripPreamble(tex)

	// 3. Remove \end{document} and anything after
	if idx := strings.Index(tex, "\\end{document}"); idx >= 0 {
		tex = tex[:idx]
	}

	// 4. Strip comments
	tex = stripComments(tex)

	// 5. Expand basic command definitions (\newcommand, \def, \DeclareMathOperator)
	tex = expandMacros(tex)

	// 6. Convert environments
	tex = convertTeXEnvironments(tex)

	// 7. Convert macros to Markdown
	tex = convertTeXMacros(tex)

	// 8. Convert special characters
	tex = convertTeXSpecialChars(tex)

	// 9. Clean up whitespace
	tex = cleanTeXWhitespace(tex)

	return strings.TrimSpace(tex)
}

// ---------------------------------------------------------------------------
// Comment / preamble / iffalse handling
// ---------------------------------------------------------------------------

func removeIfFalse(tex string) string {
	re := regexp.MustCompile(`(?s)\\iffalse\b.*?\\fi\b`)
	return re.ReplaceAllString(tex, "")
}

func stripPreamble(tex string) string {
	idx := strings.Index(tex, "\\begin{document}")
	if idx < 0 {
		return tex
	}
	return tex[idx+len("\\begin{document}"):]
}

func stripComments(tex string) string {
	lines := strings.Split(tex, "\n")
	var result []string
	for _, line := range lines {
		cleaned := stripLineComment(line)
		if cleaned != "" {
			result = append(result, cleaned)
		}
	}
	return strings.Join(result, "\n")
}

func stripLineComment(line string) string {
	var b strings.Builder
	escaped := false
	for _, ch := range line {
		if ch == '\\' {
			escaped = !escaped
			b.WriteRune(ch)
			continue
		}
		if ch == '%' && !escaped {
			break // comment starts here
		}
		escaped = false
		b.WriteRune(ch)
	}
	return strings.TrimRight(b.String(), " \t")
}

// ---------------------------------------------------------------------------
// Macro expansion (simplified)
// ---------------------------------------------------------------------------

type texMacroDef struct {
	name    string
	nargs   int
	optDef  string
	body    string
}

func expandMacros(tex string) string {
	// Remove \newcommand / \renewcommand and their definitions
	// Supports both \newcommand\foo{body} and \newcommand{\foo}{body}
	// (?s) is needed because body often spans multiple lines
	reNewCmd := regexp.MustCompile(`(?s)\\(?:newcommand|renewcommand|providecommand)\*?\s*(?:\{(\\(?:[a-zA-Z@]+|\w))\}|(\\[a-zA-Z@]+))\s*(?:\[(\d+)\])?\s*(?:\[([^\]]*)\])?\s*\{((?:[^{}]|\{[^}]*\})*)\}`)
	tex = reNewCmd.ReplaceAllString(tex, "")

	// Remove \def\cmd{body} (with optional parameter text like #1#2)
	reDef := regexp.MustCompile(`\\def\s*\\([a-zA-Z@]+)\s*(?:#[0-9]+)*\s*\{`)
	// Find all \def occurrences (from \def to matching close brace) and strip them
	for {
		loc := reDef.FindStringIndex(tex)
		if loc == nil {
			break
		}
		// Find matching closing brace (counting depth)
		start := loc[1] // position after opening {
		closeIdx := findMatchingBrace(tex, start-1)
		if closeIdx < 0 {
			// Malformed, skip
			tex = tex[:loc[0]] + tex[loc[1]:]
			continue
		}
		tex = tex[:loc[0]] + tex[closeIdx+1:]
	}

	// Remove \DeclareMathOperator
	reDecl := regexp.MustCompile(`\\DeclareMathOperator\*?\s*\{[^}]*\}\s*\{[^}]*\}`)
	tex = reDecl.ReplaceAllString(tex, "")

	// Remove \usepackage
	reUsg := regexp.MustCompile(`\\usepackage(?:\[[^\]]*\])?\{[^}]*\}`)
	tex = reUsg.ReplaceAllString(tex, "")

	// Remove \documentclass / \documentstyle
	reDoc := regexp.MustCompile(`\\(?:documentclass|documentstyle)(?:\[[^\]]*\])?\{[^}]*\}`)
	tex = reDoc.ReplaceAllString(tex, "")

	return tex
}

// ---------------------------------------------------------------------------
// Environment conversion
// ---------------------------------------------------------------------------

func convertTeXEnvironments(tex string) string {
	// ORDER MATTERS: process tabular before table, since tabular is inside table

	// --- tabular → Markdown table ---
	// Supports tabular{colspec} and tabular*{width}{colspec}
	reTabular := regexp.MustCompile(`(?s)\\begin\{tabular(\*?)\}\{(.*?)\}(.*?)\\end\{tabular\*?\}`)
	tex = reTabular.ReplaceAllStringFunc(tex, func(m string) string {
		parts := reTabular.FindStringSubmatch(m)
		if len(parts) < 4 {
			return m
		}
		// For tabular*, parts[1] = "*", parts[2] = width, parts[3] = rest after first {}
		// Need to re-parse: tabular*{width}{colspec}...  means parts[2] is actually width
		// and colspec + body is in parts[3]
		if parts[1] == "*" {
			// parts[2] = width, parts[3] = rest starting from "{colspec}..."
			// Extract colspec from parts[3]
			if len(parts[3]) > 0 && parts[3][0] == '{' {
				closeBrace := findMatchingBrace(parts[3], 0)
				if closeBrace > 0 {
					colSpec := parts[3][1:closeBrace]
					body := parts[3][closeBrace+1:]
					return convertTabular(colSpec, body)
				}
			}
			return convertTabular(parts[2], parts[3]) // best effort
		}
		// tabular{colspec}... — parts[2] = colspec, parts[3] = body
		return convertTabular(parts[2], parts[3])
	})

	// --- table / figure → caption ---
	for _, env := range []string{"table", "table*", "figure", "figure*"} {
		pat := fmt.Sprintf(`(?s)\\begin\{%s\}(.*?)\\end\{%s\}`, env, env)
		re := regexp.MustCompile(pat)
		tex = re.ReplaceAllStringFunc(tex, func(m string) string {
			parts := re.FindStringSubmatch(m)
			if len(parts) < 2 {
				return m
			}
			return extractCaption(parts[1])
		})
	}

	// --- itemize ---
	reItemize := regexp.MustCompile(`(?s)\\begin\{itemize\}(.*?)\\end\{itemize\}`)
	tex = reItemize.ReplaceAllStringFunc(tex, func(m string) string {
		parts := reItemize.FindStringSubmatch(m)
		if len(parts) < 2 {
			return m
		}
		return convertItemize(parts[1])
	})

	// --- enumerate ---
	reEnumerate := regexp.MustCompile(`(?s)\\begin\{enumerate\}(.*?)\\end\{enumerate\}`)
	tex = reEnumerate.ReplaceAllStringFunc(tex, func(m string) string {
		parts := reEnumerate.FindStringSubmatch(m)
		if len(parts) < 2 {
			return m
		}
		return convertEnumerate(parts[1])
	})

	// --- abstract ---
	reAbstract := regexp.MustCompile(`(?s)\\begin\{abstract\}(.*?)\\end\{abstract\}`)
	tex = reAbstract.ReplaceAllString(tex, "Abstract\n$1")

	// --- equation / align / gather → $$ ... $$ ---
	for _, env := range []string{"equation", "equation*", "align", "align*", "gather", "gather*", "multline", "multline*", "eqnarray", "eqnarray*"} {
		pat := fmt.Sprintf(`(?s)\\begin\{%s\}(.*?)\\end\{%s\}`, env, env)
		re := regexp.MustCompile(pat)
		tex = re.ReplaceAllString(tex, "\n$$\n$1\n$$\n")
	}

	// --- verbatim ---
	reVerbatim := regexp.MustCompile(`(?s)\\begin\{verbatim\}(.*?)\\end\{verbatim\}`)
	tex = reVerbatim.ReplaceAllString(tex, "\n```\n$1\n```\n")

	// Remove remaining \begin{...} / \end{...} tags
	reRemaining := regexp.MustCompile(`\\(?:begin|end)\{[^}]*\}`)
	tex = reRemaining.ReplaceAllString(tex, "")

	return tex
}

// convertTabular converts a LaTeX tabular environment to a Markdown table.
func convertTabular(colSpec, body string) string {
	// Parse column alignment from {l|c|r} spec
	alignments := parseTabularAlignment(colSpec)

	// Extract rows (split by \\)
	rows := splitTabularRows(body)

	if len(rows) == 0 {
		return ""
	}

	var buf strings.Builder

	// Check for \hline patterns to identify header rows
	// Simple heuristic: first row before first \hline (excluding empty rows) is header
	var headerRow []string
	var dataRows [][]string
	hlineSeen := false
	headerDone := false

	for _, rawRow := range rows {
		trimmed := strings.TrimSpace(rawRow)
		if trimmed == "" || trimmed == "\\" {
			continue
		}
		if strings.TrimSpace(trimmed) == "\\hline" {
			if !hlineSeen {
				hlineSeen = true
			} else {
				headerDone = true
			}
			continue
		}

		// Remove trailing \\
		trimmed = strings.TrimRight(trimmed, " \\")
		trimmed = strings.TrimSpace(trimmed)

		cells := splitTabularCells(trimmed)
		if len(cells) == 0 {
			continue
		}

		if !hlineSeen || !headerDone {
			headerRow = cells
			hlineSeen = false // reset: now we expect data rows
		} else {
			dataRows = append(dataRows, cells)
		}
	}

	if len(headerRow) == 0 {
		// No \hline found; first non-empty row is header
		if len(rows) > 0 {
			headerRow = splitTabularCells(rows[0])
			for _, r := range rows[1:] {
				cells := splitTabularCells(r)
				if len(cells) > 0 {
					dataRows = append(dataRows, cells)
				}
			}
		}
	}

	if len(headerRow) == 0 {
		return ""
	}

	// Build Markdown table
	writeRow := func(cells []string) {
		buf.WriteString("| ")
		for i, c := range cells {
			if i > 0 {
				buf.WriteString(" | ")
			}
			buf.WriteString(cleanCellText(c))
		}
		// Pad if fewer cells than header
		for i := len(cells); i < len(headerRow); i++ {
			buf.WriteString(" |")
		}
		buf.WriteString(" |\n")
	}

	// Header
	writeRow(headerRow)

	// Separator
	buf.WriteString("|")
	for i := 0; i < len(headerRow); i++ {
		align := ""
		if i < len(alignments) {
			align = alignments[i]
		}
		switch align {
		case "r":
			buf.WriteString(" ---:|")
		case "c":
			buf.WriteString(" :---:|")
		default:
			buf.WriteString(" :---|")
		}
	}
	buf.WriteString("\n")

	// Data rows
	for _, row := range dataRows {
		writeRow(row)
	}

	return buf.String()
}

func parseTabularAlignment(colSpec string) []string {
	var aligns []string
	runes := []rune(colSpec)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch ch {
		case 'l':
			aligns = append(aligns, "l")
		case 'c':
			aligns = append(aligns, "c")
		case 'r':
			aligns = append(aligns, "r")
		case '|', ' ', '\t':
			// column separator, no alignment
		case '*':
			// *{N}{spec} — repeat spec N times
			// Find the {N} part
			if i+1 < len(runes) && runes[i+1] == '{' {
				closeBrace := findMatchingBrace(string(runes), i+1)
				if closeBrace > i+1 {
					countStr := string(runes[i+2 : closeBrace])
					count := 0
					for _, d := range countStr {
						if d >= '0' && d <= '9' {
							count = count*10 + int(d-'0')
						}
					}
					// After {N}, find {spec}
					specStart := closeBrace + 1
					if specStart < len(runes) && runes[specStart] == '{' {
						specClose := findMatchingBrace(string(runes), specStart)
						if specClose > specStart {
							innerSpec := string(runes[specStart+1 : specClose])
							innerAligns := parseTabularAlignment(innerSpec)
							for j := 0; j < count; j++ {
								aligns = append(aligns, innerAligns...)
							}
							i = specClose
						}
					}
				}
			}
		case '@':
			// @{...} expression — skip until matching }
			if i+1 < len(runes) && runes[i+1] == '{' {
				closeAt := findMatchingBrace(string(runes), i+1)
				if closeAt > i {
					i = closeAt
				}
			}
		case 'p', 'm', 'b':
			// p{width}, m{width}, b{width} — need to skip the {width} part
			aligns = append(aligns, "l")
			// Find and skip {width}
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '{' {
					closeW := findMatchingBrace(string(runes), j)
					if closeW > j {
						i = closeW
					}
					break
				}
			}
		case '>', '<':
			// >{...}, <{...} — array package column modifiers, skip
			if i+1 < len(runes) && runes[i+1] == '{' {
				closeMod := findMatchingBrace(string(runes), i+1)
				if closeMod > i {
					i = closeMod
				}
			}
		default:
			// unknown, skip
		}
	}
	return aligns
}

func splitTabularRows(body string) []string {
	// Split by \\ but not inside { }
	var rows []string
	depth := 0
	start := 0
	runes := []rune(body)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
		} else if ch == '\\' && i+1 < len(runes) && runes[i+1] == '\\' && depth == 0 {
			// Skip past \\, optional *, and optional [dimension]
			end := i + 2
			// Skip * (e.g. \\*)
			for end < len(runes) && runes[end] == '*' {
				end++
			}
			// Skip [dimension] (e.g. \\[2pt] or \\*[2pt])
			if end < len(runes) && runes[end] == '[' {
				for end < len(runes) && runes[end] != ']' {
					end++
				}
				if end < len(runes) && runes[end] == ']' {
					end++
				}
			}
			rows = append(rows, string(runes[start:i]))
			i = end - 1
			start = end
		}
	}
	if start < len(runes) {
		rows = append(rows, string(runes[start:]))
	}
	return rows
}

func splitTabularCells(row string) []string {
	// Split by & but not inside { }
	var cells []string
	depth := 0
	start := 0
	runes := []rune(row)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
		} else if ch == '&' && depth == 0 {
			cells = append(cells, string(runes[start:i]))
			start = i + 1
		}
	}
	if start <= len(runes) {
		cells = append(cells, string(runes[start:]))
	}
	return cells
}

func cleanCellText(s string) string {
	s = strings.TrimSpace(s)

	// Remove \hline, \cline, \vline, \rule
	s = regexp.MustCompile(`\\(?:hline|cline|vline|rule)\b`).ReplaceAllString(s, "")
	// \multicolumn{N}{align}{content} → keep content
	s = regexp.MustCompile(`\\multicolumn\{[^}]*\}\{[^}]*\}\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(s, "$1")
	// \multirow{N}{width}{content} → keep content
	s = regexp.MustCompile(`\\multirow\{[^}]*\}\{[^}]*\}\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`\\hfill\b`).ReplaceAllString(s, "")

	// Expand ~ to space
	s = strings.ReplaceAll(s, "~", " ")

	// Collapse spaces
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")

	return strings.TrimSpace(s)
}

func convertItemize(body string) string {
	// Split by \item (hand-coded, no lookahead)
	items := splitByItem(body)
	var result []string
	for _, text := range items {
		text = strings.TrimSpace(text)
		if text != "" {
			result = append(result, "- "+text)
		}
	}
	return "\n" + strings.Join(result, "\n") + "\n"
}

func convertEnumerate(body string) string {
	items := splitByItem(body)
	var result []string
	for i, text := range items {
		text = strings.TrimSpace(text)
		if text != "" {
			result = append(result, fmt.Sprintf("%d. %s", i+1, text))
		}
	}
	return "\n" + strings.Join(result, "\n") + "\n"
}

func splitByItem(body string) []string {
	marker := "\\item"
	var items []string
	for {
		idx := strings.Index(body, marker)
		if idx < 0 {
			break
		}
		// Skip past \item
		rest := body[idx+len(marker):]

		// Handle empty \item (consecutive \item, \item\item)
		rest = strings.TrimLeft(rest, " \t\n\r")
		if strings.HasPrefix(rest, "\\item") || strings.HasPrefix(rest, "\n\\item") {
			items = append(items, "")
			body = rest
			continue
		}

		// Find next \item
		nextIdx := strings.Index(rest, marker)
		if nextIdx < 0 {
			items = append(items, rest)
			break
		}
		items = append(items, rest[:nextIdx])
		body = rest[nextIdx:]
	}
	return items
}

func extractCaption(envBody string) string {
	// Extract caption text
	reCap := regexp.MustCompile(`(?s)\\caption\s*\{((?:[^{}]|\{[^}]*\})*)\}`)
	caption := ""
	if m := reCap.FindStringSubmatch(envBody); len(m) >= 2 {
		caption = strings.TrimSpace(m[1])
	}

	// Strip caption command from body, keep the rest (which may already contain
	// a markdown table from convertTabular)
	body := reCap.ReplaceAllString(envBody, "")
	body = strings.TrimSpace(body)

	var result string
	if body != "" {
		result = "\n" + body + "\n"
	}
	if caption != "" {
		result += "\n> Caption: " + caption + "\n"
	}
	return result
}

// ---------------------------------------------------------------------------
// Macro conversion
// ---------------------------------------------------------------------------

func convertTeXMacros(tex string) string {
	// Order matters: more specific before general

	// \label{...}, \index{...} → remove
	tex = regexp.MustCompile(`\\label\{[^}]*\}`).ReplaceAllString(tex, "")
	tex = regexp.MustCompile(`\\index\{[^}]*\}`).ReplaceAllString(tex, "")

	// \section{...} → ## ...
	tex = regexp.MustCompile(`(?s)\\section\*?\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "\n## $1\n")
	// \subsection{...} → ### ...
	tex = regexp.MustCompile(`(?s)\\subsection\*?\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "\n### $1\n")
	// \subsubsection{...} → #### ...
	tex = regexp.MustCompile(`(?s)\\subsubsection\*?\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "\n#### $1\n")

	// \textbf{...} → **...**
	tex = regexp.MustCompile(`(?s)\\textbf\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "**$1**")
	// \textit{...} → *...*
	tex = regexp.MustCompile(`(?s)\\textit\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "*$1*")
	// \emph{...} → *...*
	tex = regexp.MustCompile(`(?s)\\emph\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "*$1*")
	// \texttt{...} → `...`
	tex = regexp.MustCompile(`(?s)\\texttt\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "`$1`")
	// \textsc{...} → ... (small caps, just plain text)
	tex = regexp.MustCompile(`(?s)\\textsc\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "$1")
	// \textsf{...} → ... (sans-serif)
	tex = regexp.MustCompile(`(?s)\\textsf\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "$1")
	// \textsl{...} → *...*
	tex = regexp.MustCompile(`(?s)\\textsl\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "*$1*")

	// \cite{...} → removes the citation marker; keep as plain
	tex = regexp.MustCompile(`\\cite(?:\[[^\]]*\])?\{[^}]*\}`).ReplaceAllString(tex, "")
	// \citet, \citep
	tex = regexp.MustCompile(`\\(?:citet|citep)\{([^}]*)\}`).ReplaceAllString(tex, "")
	// \ref{...} → remove
	tex = regexp.MustCompile(`\\ref\{[^}]*\}`).ReplaceAllString(tex, "")
	// \eqref{...} → remove
	tex = regexp.MustCompile(`\\eqref\{[^}]*\}`).ReplaceAllString(tex, "")

	// \url{...} → URL text
	tex = regexp.MustCompile(`\\url\{([^}]*)\}`).ReplaceAllString(tex, "$1")
	// \href{url}{text} → text (url)
	tex = regexp.MustCompile(`(?s)\\href\{([^}]*)\}\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "$2 ($1)")

	// \footnote{...} → (note: ...)
	tex = regexp.MustCompile(`(?s)\\footnote\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, " ($1)")

	// \includegraphics[...]{...} → [Figure: filename]
	tex = regexp.MustCompile(`\\includegraphics(?:\[[^\]]*\])?\{([^}]*)\}`).ReplaceAllString(tex, "[Figure: $1]")

	// \maketitle, \title, \author, \date → remove
	tex = regexp.MustCompile(`\\maketitle\b`).ReplaceAllString(tex, "")
	tex = regexp.MustCompile(`(?s)\\title\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "# $1\n")
	tex = regexp.MustCompile(`(?s)\\author\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "**$1**\n")
	tex = regexp.MustCompile(`(?s)\\date\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "")

	// \today → (date placeholder)
	tex = regexp.MustCompile(`\\today\b`).ReplaceAllString(tex, "")

	// \par → double newline
	tex = regexp.MustCompile(`\\par\b`).ReplaceAllString(tex, "\n\n")
	// \\  → newline (simple replacement, not inside math regions)
	tex = replaceDoubleBackslash(tex)

	// \centering, \noindent, \newpage, \clearpage → remove
	for _, cmd := range []string{"centering", "noindent", "newpage", "clearpage", "cleardoublepage", "vfill", "hfill", "smallskip", "medskip", "bigskip"} {
		tex = regexp.MustCompile(fmt.Sprintf(`\\%s\b`, cmd)).ReplaceAllString(tex, "")
	}

	// \hspace{...}, \vspace{...}
	tex = regexp.MustCompile(`\\[hv]space\*?\{[^}]*\}`).ReplaceAllString(tex, "")

	// \item → -
	tex = regexp.MustCompile(`\\item\s*`).ReplaceAllString(tex, "- ")

	// \quad, \qquad → space
	tex = strings.ReplaceAll(tex, "\\qquad", "  ")
	tex = strings.ReplaceAll(tex, "\\quad", " ")

	// \enspace, \thinspace → space
	tex = strings.ReplaceAll(tex, "\\enspace", " ")
	tex = strings.ReplaceAll(tex, "\\,", " ")

	// \left, \right → remove (math delimiters)
	tex = regexp.MustCompile(`\\(?:left|right|bigl|bigr|Bigl|Bigr|biggl|biggr|Biggl|Biggr)\b`).ReplaceAllString(tex, "")

	// \mathbb{X}, \mathcal{X}, \mathbf{X}, \mathit{X}, \mathrm{X}, \mathsf{X}, \mathfrak{X} → keep content
	tex = regexp.MustCompile(`\\math(?:bb|cal|bf|it|rm|sf|tt|frak|scr)\{([^}]*)\}`).ReplaceAllString(tex, "$1")

	// \operatorname{...} → keep text
	tex = regexp.MustCompile(`\\operatorname\*?\{([^}]*)\}`).ReplaceAllString(tex, "$1")

	// \overline{...}, \underline{...} → keep content
	tex = regexp.MustCompile(`\\(?:over|under)line\{([^}]*)\}`).ReplaceAllString(tex, "$1")

	// \sqrt[...]{...} / \sqrt{...} → sqrt(...)
	tex = regexp.MustCompile(`\\sqrt(?:\[[^\]]*\])?\{([^}]*)\}`).ReplaceAllString(tex, "sqrt($1)")

	// \frac{a}{b} → (a/b)
	tex = regexp.MustCompile(`\\frac\{([^}]*)\}\{([^}]*)\}`).ReplaceAllString(tex, "($1/$2)")

	// \text{...} → ...
	tex = regexp.MustCompile(`(?s)\\text\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "$1")

	// \substack{...} → ...
	tex = regexp.MustCompile(`(?s)\\substack\{((?:[^{}]|\{[^}]*\})*)\}`).ReplaceAllString(tex, "$1")

	// \colon → :
	tex = strings.ReplaceAll(tex, "\\colon", ":")

	// \dots, \ldots, \cdots, \vdots, \ddots
	tex = strings.ReplaceAll(tex, "\\ldots", "...")
	tex = strings.ReplaceAll(tex, "\\dots", "...")
	tex = strings.ReplaceAll(tex, "\\cdots", "...")
	tex = strings.ReplaceAll(tex, "\\vdots", "...")
	tex = strings.ReplaceAll(tex, "\\ddots", "...")

	// \textellipsis
	tex = strings.ReplaceAll(tex, "\\textellipsis", "...")

	// \textdegree
	tex = strings.ReplaceAll(tex, "\\textdegree", "°")
	tex = strings.ReplaceAll(tex, "\\degree", "°")

	// | (as text)
	tex = strings.ReplaceAll(tex, "\\textbar", "|")
	tex = strings.ReplaceAll(tex, "\\textless", "<")
	tex = strings.ReplaceAll(tex, "\\textgreater", ">")
	tex = strings.ReplaceAll(tex, "\\textasciitilde", "~")
	tex = strings.ReplaceAll(tex, "\\textasciicircum", "^")
	tex = strings.ReplaceAll(tex, "\\textbackslash", "\\")

	// \textregistered, \copyright, \texttrademark
	tex = strings.ReplaceAll(tex, "\\textregistered", "®")
	tex = strings.ReplaceAll(tex, "\\copyright", "©")
	tex = strings.ReplaceAll(tex, "\\texttrademark", "™")

	// \P, \S
	tex = strings.ReplaceAll(tex, "\\P", "¶")
	tex = strings.ReplaceAll(tex, "\\S", "§")

	// \dag, \ddag
	tex = strings.ReplaceAll(tex, "\\dag", "†")
	tex = strings.ReplaceAll(tex, "\\ddag", "‡")

	// \SS → SS
	tex = strings.ReplaceAll(tex, "\\SS", "SS")

	return tex
}

// findMatchingBrace finds the matching closing brace for an opening brace at pos.
func findMatchingBrace(s string, pos int) int {
	if pos >= len(s) || s[pos] != '{' {
		return -1
	}
	depth := 0
	for i := pos; i < len(s); i++ {
		ch := s[i]
		if ch == '\\' && i+1 < len(s) {
			i++ // skip escaped char
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Special character conversion
// ---------------------------------------------------------------------------

func convertTeXSpecialChars(tex string) string {
	// Escaped special chars
	tex = strings.ReplaceAll(tex, "\\&", "&")
	tex = strings.ReplaceAll(tex, "\\$", "$")
	tex = strings.ReplaceAll(tex, "\\%", "%")
	tex = strings.ReplaceAll(tex, "\\_", "_")
	tex = strings.ReplaceAll(tex, "\\#", "#")
	tex = strings.ReplaceAll(tex, "\\{", "{")
	tex = strings.ReplaceAll(tex, "\\}", "}")
	tex = strings.ReplaceAll(tex, "\\~", "~")
	tex = strings.ReplaceAll(tex, "\\^", "^")

	// Ligatures
	tex = strings.ReplaceAll(tex, "\\ae", "æ")
	tex = strings.ReplaceAll(tex, "\\AE", "Æ")
	tex = strings.ReplaceAll(tex, "\\oe", "œ")
	tex = strings.ReplaceAll(tex, "\\OE", "Œ")
	tex = strings.ReplaceAll(tex, "\\aa", "å")
	tex = strings.ReplaceAll(tex, "\\AA", "Å")
	tex = strings.ReplaceAll(tex, "\\ss", "ß")
	tex = strings.ReplaceAll(tex, "\\o", "ø")
	tex = strings.ReplaceAll(tex, "\\O", "Ø")
	tex = strings.ReplaceAll(tex, "\\l", "ł")
	tex = strings.ReplaceAll(tex, "\\L", "Ł")
	tex = strings.ReplaceAll(tex, "\\i", "ı")
	tex = strings.ReplaceAll(tex, "\\j", "ȷ")

	// Common math symbols (with space after)
	tex = strings.ReplaceAll(tex, "\\infty", "∞")
	tex = strings.ReplaceAll(tex, "\\partial", "∂")
	tex = strings.ReplaceAll(tex, "\\nabla", "∇")

	tex = strings.ReplaceAll(tex, "\\rightarrow", "→")
	tex = strings.ReplaceAll(tex, "\\leftarrow", "←")
	tex = strings.ReplaceAll(tex, "\\Rightarrow", "⇒")
	tex = strings.ReplaceAll(tex, "\\Leftarrow", "⇐")
	tex = strings.ReplaceAll(tex, "\\leftrightarrow", "↔")
	tex = strings.ReplaceAll(tex, "\\Leftrightarrow", "⇔")
	tex = strings.ReplaceAll(tex, "\\uparrow", "↑")
	tex = strings.ReplaceAll(tex, "\\downarrow", "↓")

	tex = strings.ReplaceAll(tex, "\\times", "×")
	tex = strings.ReplaceAll(tex, "\\div", "÷")
	tex = strings.ReplaceAll(tex, "\\pm", "±")
	tex = strings.ReplaceAll(tex, "\\mp", "∓")
	tex = strings.ReplaceAll(tex, "\\leq", "≤")
	tex = strings.ReplaceAll(tex, "\\geq", "≥")
	tex = strings.ReplaceAll(tex, "\\neq", "≠")
	tex = strings.ReplaceAll(tex, "\\approx", "≈")
	tex = strings.ReplaceAll(tex, "\\equiv", "≡")
	tex = strings.ReplaceAll(tex, "\\sim", "∼")
	tex = strings.ReplaceAll(tex, "\\simeq", "≃")
	tex = strings.ReplaceAll(tex, "\\cong", "≅")
	tex = strings.ReplaceAll(tex, "\\propto", "∝")
	tex = strings.ReplaceAll(tex, "\\in", "∈")
	tex = strings.ReplaceAll(tex, "\\notin", "∉")
	tex = strings.ReplaceAll(tex, "\\subset", "⊂")
	tex = strings.ReplaceAll(tex, "\\supset", "⊃")
	tex = strings.ReplaceAll(tex, "\\cup", "∪")
	tex = strings.ReplaceAll(tex, "\\cap", "∩")
	tex = strings.ReplaceAll(tex, "\\emptyset", "∅")
	tex = strings.ReplaceAll(tex, "\\forall", "∀")
	tex = strings.ReplaceAll(tex, "\\exists", "∃")

	tex = strings.ReplaceAll(tex, "\\bullet", "•")
	tex = strings.ReplaceAll(tex, "\\circ", "∘")

	// Greek letters
	greek := map[string]string{
		"\\alpha": "α", "\\beta": "β", "\\gamma": "γ", "\\delta": "δ",
		"\\epsilon": "ε", "\\varepsilon": "ε", "\\zeta": "ζ", "\\eta": "η",
		"\\theta": "θ", "\\vartheta": "θ", "\\iota": "ι", "\\kappa": "κ",
		"\\lambda": "λ", "\\mu": "μ", "\\nu": "ν", "\\xi": "ξ",
		"\\pi": "π", "\\varpi": "π", "\\rho": "ρ", "\\varrho": "ρ",
		"\\sigma": "σ", "\\varsigma": "ς", "\\tau": "τ", "\\upsilon": "υ",
		"\\phi": "φ", "\\varphi": "φ", "\\chi": "χ", "\\psi": "ψ",
		"\\omega": "ω",
		"\\Gamma": "Γ", "\\Delta": "Δ", "\\Theta": "Θ", "\\Lambda": "Λ",
		"\\Xi": "Ξ", "\\Pi": "Π", "\\Sigma": "Σ", "\\Phi": "Φ",
		"\\Psi": "Ψ", "\\Omega": "Ω",
	}
	for from, to := range greek {
		tex = strings.ReplaceAll(tex, from, to)
	}

	return tex
}

// ---------------------------------------------------------------------------
// Whitespace cleaning
// ---------------------------------------------------------------------------

func replaceDoubleBackslash(tex string) string {
	// Protect math and code regions, then replace \\ with newline.
	var buf strings.Builder
	inCode := false
	i := 0
	runes := []rune(tex)
	for i < len(runes) {
		// Track code blocks (triple backticks)
		if i+2 < len(runes) && runes[i] == '`' && runes[i+1] == '`' && runes[i+2] == '`' {
			buf.WriteString("```")
			inCode = !inCode
			i += 3
			continue
		}

		if inCode {
			buf.WriteRune(runes[i])
			i++
			continue
		}

		if i+1 < len(runes) && runes[i] == '\\' && runes[i+1] == '\\' {
			if !isInsideMath(runes[:i]) {
				buf.WriteRune('\n')
				i += 2
				continue
			}
		}
		buf.WriteRune(runes[i])
		i++
	}
	return buf.String()
}

func isInsideMath(runes []rune) bool {
	depth := 0
	i := 0
	for i < len(runes) {
		// Skip escaped characters (e.g. \$, \_)
		if runes[i] == '\\' && i+1 < len(runes) {
			i += 2
			continue
		}
		if runes[i] == '$' {
			if i+1 < len(runes) && runes[i+1] == '$' {
				depth++
				i += 2
				continue
			}
			depth++
		}
		i++
	}
	return depth%2 == 1
}

func cleanTeXWhitespace(tex string) string {
	// Collapse blank lines
	tex = regexp.MustCompile(`\n{3,}`).ReplaceAllString(tex, "\n\n")
	// Remove leading/trailing whitespace on each line
	tex = regexp.MustCompile(`(?m)^[ \t]+|[ \t]+$`).ReplaceAllString(tex, "")
	return strings.TrimSpace(tex)
}
