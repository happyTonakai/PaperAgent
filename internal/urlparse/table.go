package urlparse

// LaTeXML table -> GitHub-flavoured markdown table conversion.
//
// Tables are lifted out of the document before the generic tag stripping
// (the placeholder round-trip in HTMLToMarkdown) and converted structurally:
//
//   - colspan / rowspan are expanded onto a rectangular grid, so the header
//     keeps the same column count as the data rows. LaTeXML headers carry
//     fewer physical cells than the table is wide, which is why the old
//     cell-list renderer produced a mis-aligned table.
//   - multi-row headers are collapsed into one header row ("LoCoMo / Single"),
//     because markdown only supports a single header row.
//   - a <figure class="ltx_table"> may wrap several side-by-side panels
//     (<div class="ltx_flex_figure">); every panel is converted, not just the
//     first one.
//   - span-based tabulars (no <table> element at all) are recovered too.
//   - decorative tabulars holding no text (LaTeXML emits them for \rule
//     separators) are dropped instead of yielding an empty one-cell table.
//
// Element extents are resolved by counting matching tags instead of using a
// non-greedy regex, so a tabular nested inside another tabular's cell no longer
// truncates the outer match.

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Element scanning
// ---------------------------------------------------------------------------

// elemRange is the byte range of one element inside the string it was found in.
// start/end span the whole element, innerStart/innerEnd span its content.
type elemRange struct {
	start, end           int
	innerStart, innerEnd int
}

func (e elemRange) full(s string) string  { return s[e.start:e.end] }
func (e elemRange) inner(s string) string { return s[e.innerStart:e.innerEnd] }
func (e elemRange) openTag(s string) string {
	return s[e.start:e.innerStart]
}

var (
	figureTokRe     = regexp.MustCompile(`(?is)<figure\b[^>]*>|</figure\s*>`)
	tableTokRe      = regexp.MustCompile(`(?is)<table\b[^>]*>|</table\s*>`)
	trTokRe         = regexp.MustCompile(`(?is)<tr\b[^>]*>|</tr\s*>`)
	theadTokRe      = regexp.MustCompile(`(?is)<thead\b[^>]*>|</thead\s*>`)
	spanTokRe       = regexp.MustCompile(`(?is)<span\b[^>]*>|</span\s*>`)
	figcaptionTokRe = regexp.MustCompile(`(?is)<figcaption\b[^>]*>|</figcaption\s*>`)
	preTokRe        = regexp.MustCompile(`(?is)<pre\b[^>]*>|</pre\s*>`)

	// group 1 = the attribute text (without the angle brackets); whether the cell
	// is a <th> is read off the token itself, not from a capture group.
	cellTokRe = regexp.MustCompile(`(?is)<t[dh]([^>]*)>|</t[dh]\s*>`)
)

// scanElements returns every depth-1 element whose opening tag passes accept.
// Elements of the same tag nested inside a returned element belong to it and are
// not reported separately. Unaccepted top-level elements are transparent, so a
// tabular inside a plain layout <table> is still found.
func scanElements(s string, tokRe *regexp.Regexp, accept func(openTag string) bool) []elemRange {
	var out []elemRange

	type frame struct {
		start, innerStart int
	}
	var stack []frame

	for _, m := range tokRe.FindAllStringIndex(s, -1) {
		tok := s[m[0]:m[1]]

		if strings.HasPrefix(tok, "</") {
			if len(stack) > 0 {
				f := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					out = append(out, elemRange{start: f.start, end: m[1], innerStart: f.innerStart, innerEnd: m[0]})
				}
			}
			continue
		}

		if len(stack) > 0 {
			stack = append(stack, frame{m[0], m[1]})
			continue
		}
		if accept != nil && !accept(tok) {
			continue
		}
		if strings.HasSuffix(strings.TrimRight(tok, " \t\r\n"), "/>") {
			out = append(out, elemRange{start: m[0], end: m[1], innerStart: m[1], innerEnd: m[1]})
			continue
		}
		stack = append(stack, frame{m[0], m[1]})
	}
	return out
}

// cellSource is one <td>/<th> element of a row.
type cellSource struct {
	attrs string
	inner string
	tag   string // "td" or "th"
}

// scanCells returns the depth-1 <td>/<th> elements of s. <td> and <th> are
// treated as the same element name, as HTML parsers do.
func scanCells(s string) []cellSource {
	var out []cellSource

	type frame struct {
		attrs      string
		innerStart int
		tag        string
	}
	var stack []frame

	for _, m := range cellTokRe.FindAllStringSubmatchIndex(s, -1) {
		tok := s[m[0]:m[1]]

		if strings.HasPrefix(tok, "</") {
			if len(stack) > 0 {
				f := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					out = append(out, cellSource{attrs: f.attrs, inner: s[f.innerStart:m[0]], tag: f.tag})
				}
			}
			continue
		}

		attrs, tag := "", "td"
		if m[2] >= 0 {
			attrs = s[m[2]:m[3]]
		}
		if len(tok) > 2 && (tok[2] == 'h' || tok[2] == 'H') {
			tag = "th"
		}
		if strings.HasSuffix(strings.TrimRight(tok, " \t\r\n"), "/>") {
			if len(stack) == 0 {
				out = append(out, cellSource{attrs: attrs, tag: tag})
			}
			continue
		}
		stack = append(stack, frame{attrs: attrs, innerStart: m[1], tag: tag})
	}
	return out
}

// ---------------------------------------------------------------------------
// Cell / row model
// ---------------------------------------------------------------------------

var wsRe = regexp.MustCompile(`\s+`)

// span limits guard against malformed or pathological rowspan/colspan values.
const (
	maxCellSpan = 64
	maxGridCols = 256
	maxGridRows = 4000
)

type tableCell struct {
	text  string
	align string
	cs    int
	rs    int
	th    bool // an actual <th> cell (the ltx_th *class* is not trustworthy)
}

type tableRow struct {
	cells []tableCell
	head  bool
}

// cellBreakRe marks the boundaries inside a cell: LaTeXML wraps each line in its
// own <span class="ltx_p"> and a cell can even hold a nested tabular.
var cellBreakRe = regexp.MustCompile(`(?is)</(?:tr|td|th|p|div|h\d)>|<br\s*/?>`)

// cellParaRe matches the <span class="ltx_p"> LaTeXML uses for each line of a
// multi-line cell. It is inline in HTML but a hard line break in the source.
var cellParaRe = regexp.MustCompile(`(?is)<span[^>]*class="[^"]*\bltx_p\b[^"]*"[^>]*>`)

// cleanHTMLCellText normalises the text content of a single HTML table cell.
// (Distinct from cleanCellText in tex2md.go, which handles LaTeX cell source.)
func cleanHTMLCellText(cellHTML string) string {
	c := cellBreakRe.ReplaceAllString(cellHTML, " ")
	c = cellParaRe.ReplaceAllString(c, " ")
	c = stripHTMLTags(c)
	c = decodeEntities(c)
	c = wsRe.ReplaceAllString(c, " ")
	c = strings.TrimSpace(c)
	return escapeCellPipes(c)
}

// mathSpanEnd reports the extent [i,end) of a $...$ or $$...$$ formula starting
// at position i, the way LaTeX reads them: the delimiters must hug their content.
// A "$" that does not open a formula (an unmatched one, or "US$ 5 and ...$") is
// left alone, so prose dollar signs cannot pair up and swallow a pipe between
// them.
func mathSpanEnd(s string, i int) (int, bool) {
	d := 1
	if i+1 < len(s) && s[i+1] == '$' {
		d = 2
	}
	open := i + d
	if open >= len(s) || s[open] == ' ' || s[open] == '\t' || s[open] == '\n' || s[open] == '$' {
		return 0, false
	}
	for j := open; j < len(s); j++ {
		if s[j] == '\\' {
			j++ // skip the escaped character
			continue
		}
		if s[j] != '$' {
			continue
		}
		if d == 2 && !(j+1 < len(s) && s[j+1] == '$') {
			continue
		}
		if prev := s[j-1]; prev == ' ' || prev == '\t' || prev == '\n' {
			return 0, false
		}
		return j + d, true
	}
	return 0, false
}

// escapeCellPipes keeps a literal "|" from splitting a markdown cell.
//
// The escape depends on context. Outside a formula it is the markdown one,
// "\|". Inside a formula the markdown form must not be used: KaTeX reads "\|"
// as the norm double bar, which silently changes the maths, so the bar becomes
// "\vert" — closed by "{}" when a letter follows, because a TeX command name
// runs to the end of the letters and "\vertV" would be an undefined command.
// Bars the source already escaped are left exactly as they are.
func escapeCellPipes(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		if s[i] == '$' {
			if end, ok := mathSpanEnd(s, i); ok {
				b.WriteString(escapeMathPipes(s[i:end]))
				i = end
				continue
			}
		}
		switch {
		case s[i] == '\\':
			b.WriteByte(s[i])
			i++
			if i < len(s) {
				b.WriteByte(s[i]) // keep an existing escape pair intact, "\|" included
				i++
			}
		case s[i] == '|':
			b.WriteString(`\|`)
			i++
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

func escapeMathPipes(span string) string {
	var b strings.Builder
	b.Grow(len(span) + 8)
	for i := 0; i < len(span); i++ {
		if span[i] == '\\' {
			b.WriteByte(span[i])
			if i+1 < len(span) {
				b.WriteByte(span[i+1])
				i++
			}
			continue
		}
		if span[i] != '|' {
			b.WriteByte(span[i])
			continue
		}
		b.WriteString(`\vert`)
		if i+1 < len(span) && isASCIILetter(span[i+1]) {
			b.WriteString("{}") // terminate the command name: "\vert V" would be \vertV
		}
	}
	return b.String()
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// alignFromAttrs maps LaTeXML alignment classes to a markdown column alignment.
// The cell's own classes win; when they carry no horizontal alignment (LaTeXML
// often marks the <td> ltx_align_left and centres the content in a nested
// <span class="ltx_p ltx_align_center">), the wrapper classes are consulted.
func alignFromAttrs(attrs, content string) string {
	switch {
	case strings.Contains(attrs, "ltx_align_right"):
		return "right"
	case strings.Contains(attrs, "ltx_align_center"):
		return "center"
	case strings.Contains(content, "ltx_align_right"):
		return "right"
	case strings.Contains(content, "ltx_align_center"):
		return "center"
	default:
		return "left"
	}
}

var colspanRe = regexp.MustCompile(`(?is)\bcolspan\s*=\s*"(\d+)"`)
var rowspanRe = regexp.MustCompile(`(?is)\browspan\s*=\s*"(\d+)"`)

// spanAttr reads a colspan/rowspan attribute, defaulting to 1 and clamped.
func spanAttr(attrs, name string) int {
	re := colspanRe
	if name == "rowspan" {
		re = rowspanRe
	}
	m := re.FindStringSubmatch(attrs)
	if len(m) < 2 {
		return 1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 {
		return 1
	}
	if n > maxCellSpan {
		n = maxCellSpan
	}
	return n
}

// parseTabularRows converts the inner HTML of a <table class="ltx_tabular">
// into rows of cells, keeping colspan/rowspan information.
func parseTabularRows(inner string) []tableRow {
	headRanges := scanElements(inner, theadTokRe, nil)
	inHead := func(start, end int) bool {
		for _, h := range headRanges {
			if start >= h.start && end <= h.end {
				return true
			}
		}
		return false
	}

	var rows []tableRow
	for _, tr := range scanElements(inner, trTokRe, nil) {
		head := inHead(tr.start, tr.end)
		var cells []tableCell
		for _, c := range scanCells(tr.inner(inner)) {
			cells = append(cells, tableCell{
				text:  cleanHTMLCellText(c.inner),
				align: alignFromAttrs(c.attrs, c.inner),
				cs:    spanAttr(c.attrs, "colspan"),
				rs:    spanAttr(c.attrs, "rowspan"),
				th:    c.tag == "th" && !strings.Contains(c.attrs, "ltx_th_row"),
			})
		}
		if len(cells) == 0 {
			continue
		}
		rows = append(rows, tableRow{cells: cells, head: head})
	}
	return rows
}

var classAttrRe = regexp.MustCompile(`(?is)class="([^"]*)"`)

// classHas reports whether an opening tag carries cls as one of its class names.
func classHas(openTag, cls string) bool {
	m := classAttrRe.FindStringSubmatch(openTag)
	if m == nil {
		return false
	}
	for _, name := range strings.FieldsFunc(m[1], func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ';'
	}) {
		if name == cls {
			return true
		}
	}
	return false
}

// parseSpanRows recovers rows from a span-based tabular, which LaTeXML emits
// when a table sits inside a flex panel: no <table>/<tr>/<td> elements at all,
// the ltx_tabular / ltx_tr / ltx_td classes end up on <span>s.
func parseSpanRows(inner string) []tableRow {
	isTr := func(t string) bool { return classHas(t, "ltx_tr") }
	isTd := func(t string) bool { return classHas(t, "ltx_td") }

	headRanges := scanElements(inner, spanTokRe, func(t string) bool { return classHas(t, "ltx_thead") })
	inHead := func(start, end int) bool {
		for _, h := range headRanges {
			if start >= h.start && end <= h.end {
				return true
			}
		}
		return false
	}

	var rows []tableRow
	for _, tr := range scanElements(inner, spanTokRe, isTr) {
		body := tr.full(inner)
		head := inHead(tr.start, tr.end)
		var cells []tableCell
		for _, td := range scanElements(body, spanTokRe, isTd) {
			open := td.openTag(body)
			cells = append(cells, tableCell{
				text:  cleanHTMLCellText(td.inner(body)),
				align: alignFromAttrs(open, td.inner(body)),
				cs:    spanAttr(open, "colspan"),
				rs:    spanAttr(open, "rowspan"),
			})
		}
		if len(cells) == 0 {
			continue
		}
		rows = append(rows, tableRow{cells: cells, head: head})
	}
	return rows
}

// ---------------------------------------------------------------------------
// Grid expansion
// ---------------------------------------------------------------------------

// tableGrid is the rectangular form of a table: every row has exactly width
// entries and merged cells only carry text in their top-left position.
type tableGrid struct {
	cells  [][]string
	taken  [][]bool
	width  int
	height int
	header int
	aligns []string
	votes  [][3]int
	first  []string
	placed [][]placedCell // where each row's own cells landed
}

// placedCell records the grid position of one cell written by its own row (as
// opposed to a cell covered by a rowspan from above).
type placedCell struct {
	col int
	cs  int
}

func buildGrid(rows []tableRow) *tableGrid {
	if len(rows) > maxGridRows {
		rows = rows[:maxGridRows]
	}
	g := &tableGrid{height: len(rows)}
	if len(rows) == 0 {
		return g
	}
	g.cells = make([][]string, len(rows))
	g.taken = make([][]bool, len(rows))
	g.placed = make([][]placedCell, len(rows))

	grow := func(r, c int) {
		for len(g.cells[r]) <= c {
			g.cells[r] = append(g.cells[r], "")
			g.taken[r] = append(g.taken[r], false)
		}
		if c+1 > g.width {
			g.width = c + 1
		}
	}

	for r, row := range rows {
		c := 0
		for _, cell := range row.cells {
			for c < len(g.cells[r]) && g.taken[r][c] {
				c++
			}
			cs := cell.cs
			if c+cs > maxGridCols {
				cs = maxGridCols - c
			}
			if cs < 1 {
				break
			}
			for i := 0; i < cs; i++ {
				for j := 0; j < cell.rs && r+j < len(rows); j++ {
					grow(r+j, c+i)
					switch {
					case i == 0 && j == 0:
						g.cells[r+j][c+i] = cell.text
					case i == 0 && j > 0:
						// A vertical merge repeats its label: markdown has no
						// rowspan, and a blank cell would drop the grouping.
						g.cells[r+j][c+i] = cell.text
						g.taken[r+j][c+i] = true
					default:
						g.taken[r+j][c+i] = true
					}
				}
			}
			if cs == 1 && cell.text != "" {
				g.voteAlign(c, cell.align)
			}
			g.placed[r] = append(g.placed[r], placedCell{col: c, cs: cs})
			c += cs
		}
	}
	g.resolveAligns()
	g.header = g.detectHeaderRows(rows)
	return g
}

// voteAlign records one column alignment observation. Columns are decided by
// majority in resolveAligns: a header cell is often aligned differently from the
// numbers below it, and the body carries the signal that matters.
func (g *tableGrid) voteAlign(c int, align string) {
	i := alignIndex(align)
	if i < 0 {
		return
	}
	for len(g.votes) <= c {
		g.votes = append(g.votes, [3]int{})
		g.first = append(g.first, "")
	}
	g.votes[c][i]++
	if g.first[c] == "" {
		g.first[c] = align
	}
}

func alignIndex(align string) int {
	switch align {
	case "center":
		return 0
	case "right":
		return 1
	case "left":
		return 2
	}
	return -1
}

func (g *tableGrid) resolveAligns() {
	g.aligns = make([]string, g.width)
	for c := 0; c < g.width; c++ {
		if c >= len(g.votes) || g.votes[c] == [3]int{} {
			g.aligns[c] = "left"
			continue
		}
		best, bi := -1, -1
		for i, n := range g.votes[c] {
			if n > best {
				best, bi = n, i
			}
		}
		switch bi {
		case 0:
			g.aligns[c] = "center"
		case 1:
			g.aligns[c] = "right"
		default:
			g.aligns[c] = "left"
		}
		if best == 1 && g.first[c] != "" && g.first[c] != g.aligns[c] {
			// A single observation is weak evidence; fall back to the first cell.
			g.aligns[c] = g.first[c]
		}
	}
}

// detectHeaderRows returns how many leading rows form the header block.
//
// A row joins the block when one of these holds:
//   - it sits in an explicit <thead> (or <span class="ltx_thead">), or is built
//     purely of <th> cells;
//   - it groups columns under a partial colspan ("LoCoMo" over 4 sub-columns). A
//     full-width row is a section label (\multicolumn{N}{l}{Reference}), never
//     header material;
//   - it is the leaf label row directly under a group row: it fills only columns
//     that no accepted header row already resolved one-to-one, and it holds no
//     numbers ("Single | Multi | Temp." under "LoCoMo").
//
// Deliberately *not* a signal:
//   - the ltx_th class. With ltx_guessed_headers LaTeXML tags the label column of
//     every row as a column header, which would swallow the whole table into the
//     header block and merge all rows away;
//   - a bare rowspan reach. A header cell may rowspan into the body ("Setting"
//     over three rows, the last of which is data); extending the block by the
//     rowspan alone turns that data row into column titles, which is silently
//     wrong. The leaf rule above is what ties a multi-level header together.
//
// Two shapes are still read as header material although they are data, both
// covered by TestConvertTable_KnownHeaderLimits: a table whose body rows are all
// <th> (nothing distinguishes it from a tall header), and a data row with neither a
// column-0 label nor numbers under a group row. Both err towards keeping the row
// visible — as a title — rather than dropping it, and neither occurs in the corpus.
func (g *tableGrid) detectHeaderRows(rows []tableRow) int {
	header := 0
	groupingSeen := false
	resolved := make(map[int]bool) // columns filled 1:1 by an accepted header row
	for r := 0; r < len(rows) && r < maxHeaderRows; r++ {
		grouping := g.groupingRow(rows[r])
		// A full-width row above everything else is a banner over the columns
		// ("MMLU (5-shot)"); below the first row it is a section label instead.
		banner := header == 0 && fullWidthLabel(rows, r, g.width)
		leaf := groupingSeen && !g.ownsRowLabel(r) && !g.fillsResolvedColumn(r, resolved) && !mostlyNumeric(rows[r])
		if !(rows[r].head || allHeaderCells(rows[r]) || grouping || banner || leaf) {
			break
		}
		for _, p := range g.placed[r] {
			if p.cs == 1 && g.cell(r, p.col) != "" {
				resolved[p.col] = true
			}
		}
		groupingSeen = groupingSeen || grouping
		header++
	}
	if header == 0 {
		header = 1 // no structural header: the first row is the best candidate
	}
	if len(rows) > 1 && header > len(rows)-1 {
		header = len(rows) - 1 // keep at least one body row
	}
	return header
}

// ownsRowLabel reports whether the row supplies its own non-empty text in the
// leftmost column. That is the shape of a data row (its label); a leaf header row
// either leaves column 0 to a rowspan from above or leaves it empty. This is the
// structural veto behind mostlyNumeric, which only catches rows of bare numbers.
func (g *tableGrid) ownsRowLabel(r int) bool {
	for _, p := range g.placed[r] {
		if p.col == 0 {
			return g.cell(r, 0) != ""
		}
	}
	return false
}

// fillsResolvedColumn reports whether any of the row's own non-empty cells lands
// in a column an accepted header row already gave its own label to. That is the
// case for a data row (its row label sits in column 0) but not for the leaf row
// of a multi-level header, which only fills columns sitting under a group.
func (g *tableGrid) fillsResolvedColumn(r int, resolved map[int]bool) bool {
	for _, p := range g.placed[r] {
		if g.cell(r, p.col) == "" {
			continue
		}
		if resolved[p.col] {
			return true
		}
	}
	return false
}

// numericCellRe matches a measured value once its markup is stripped: 12, 12.5,
// -0.99, 12,345, 42%, 20–25, "12.5 $\pm$ 0.3", $\mathbf{94.6}$. Labels such as
// "Single", "10ms" or "$k=1$" keep letters and do not match.
var numericCellRe = regexp.MustCompile(`^[-+.,:%~<>=–—\s\d]*$`)

// latexNoiseRe strips the TeX scaffolding a numeric cell can hide behind:
// commands, groups, superscript/subscript markers and the $ delimiters.
var latexNoiseRe = regexp.MustCompile(`\\[a-zA-Z]+|[$~^{}]`)

// mostlyNumeric reports whether a row is measurements rather than labels. It is
// a secondary veto: the numeric share has to be high, because a leaf header row
// may legitimately hold numbers ("10ms", "top-5"), and those are not measurements
// in bulk.
func mostlyNumeric(row tableRow) bool {
	values, total := 0, 0
	for _, c := range row.cells {
		if c.text == "" {
			continue
		}
		total++
		plain := strings.TrimSpace(latexNoiseRe.ReplaceAllString(c.text, ""))
		if strings.ContainsAny(plain, "0123456789") && numericCellRe.MatchString(plain) {
			values++
		}
	}
	return total >= 2 && values*10 >= total*7
}

// maxHeaderRows bounds the header block, so that a header cell with an absurd
// rowspan cannot absorb the table body.
const maxHeaderRows = 6

// allHeaderCells reports whether the row is nothing but column headers: every
// cell is a <th> or carries no text (an empty header column).
func allHeaderCells(row tableRow) bool {
	header := false
	for _, c := range row.cells {
		if c.text == "" {
			continue
		}
		if !c.th {
			return false
		}
		header = true
	}
	return header
}

// groupingRow reports whether the row labels groups of columns, i.e. holds a
// colspan over a strict subset of the table's columns. A row made of a single
// merged cell is a section label ("Reference", "Memory Engines"), not a header.
func (g *tableGrid) groupingRow(row tableRow) bool {
	if len(row.cells) < 2 {
		return false
	}
	for _, c := range row.cells {
		if c.cs > 1 && c.cs < g.width {
			return true
		}
	}
	return false
}

// mergedHeader flattens the header block into a single row, joining the levels
// with " / " and dropping the repetitions a merged cell produces.
func (g *tableGrid) mergedHeader() []string {
	out := make([]string, g.width)
	for c := 0; c < g.width; c++ {
		var parts []string
		for r := 0; r < g.header; r++ {
			t := g.cell(r, c)
			if t == "" {
				continue
			}
			seen := false
			for _, p := range parts {
				if p == t {
					seen = true
					break
				}
			}
			if !seen {
				parts = append(parts, t)
			}
		}
		out[c] = strings.Join(parts, " / ")
	}
	return out
}

func (g *tableGrid) cell(r, c int) string {
	if r < 0 || r >= len(g.cells) || c < 0 || c >= len(g.cells[r]) {
		return ""
	}
	return g.cells[r][c]
}

func (g *tableGrid) empty() bool {
	for r := 0; r < g.height; r++ {
		for c := 0; c < g.width; c++ {
			if g.cell(r, c) != "" {
				return false
			}
		}
	}
	return true
}

// fullWidthLabel reports whether row r is a section separator such as
// "\multicolumn{13}{l}{Reference}": a single merged cell and nothing else.
func fullWidthLabel(rows []tableRow, r, width int) bool {
	if r >= len(rows) {
		return false
	}
	row := rows[r]
	return len(row.cells) == 1 && row.cells[0].cs > 1 && row.cells[0].text != "" &&
		row.cells[0].cs >= width-1
}

// ---------------------------------------------------------------------------
// Markdown rendering
// ---------------------------------------------------------------------------

// renderTableMarkdown lays one tabular's rows out as a GitHub table.
func renderTableMarkdown(rows []tableRow) string {
	if len(rows) == 0 {
		return ""
	}
	g := buildGrid(rows)
	if g.width == 0 || g.empty() {
		return "" // decorative tabular (e.g. a \rule separator): nothing to show
	}

	var buf strings.Builder
	writeRow(&buf, g.mergedHeader())
	buf.WriteString("|")
	for c := 0; c < g.width; c++ {
		switch g.aligns[c] {
		case "right":
			buf.WriteString(" ---:|")
		case "center":
			buf.WriteString(" :---:|")
		default:
			buf.WriteString(" :---|")
		}
	}
	buf.WriteString("\n")

	for r := g.header; r < g.height; r++ {
		row := make([]string, g.width)
		for c := 0; c < g.width; c++ {
			row[c] = g.cell(r, c)
		}
		if fullWidthLabel(rows, r, g.width) {
			row[0] = "**" + row[0] + "**"
		}
		writeRow(&buf, row)
	}
	return buf.String()
}

func writeRow(buf *strings.Builder, row []string) {
	buf.WriteString("| ")
	for i, c := range row {
		if i > 0 {
			buf.WriteString(" | ")
		}
		buf.WriteString(c)
	}
	buf.WriteString(" |\n")
}

// ---------------------------------------------------------------------------
// Table segments: locating tables in the document and swapping in markdown
// ---------------------------------------------------------------------------

var (
	ltxTableClassRe = regexp.MustCompile(`(?is)class="[^"]*ltx_table[^"]*"`)
	tabularClassRe  = regexp.MustCompile(`(?is)class="[^"]*ltx_tabular[^"]*"`)
)

func isTabularOpen(openTag string) bool { return tabularClassRe.MatchString(openTag) }
func isFigureTable(openTag string) bool { return ltxTableClassRe.MatchString(openTag) }
func isSpanTabular(openTag string) bool { return classHas(openTag, "ltx_tabular") }

// tableSegment is one unit of the document that has to be replaced by a
// placeholder before stripping: either a <figure class="ltx_table"> (which may
// wrap several panel tabulars) or a stand-alone tabular.
type tableSegment struct {
	start, end int
	html       string
}

// tablePlaceholderMark is randomised per process. A paper quoting a literal
// placeholder of its own (a code listing of this very pipeline, say) must not be
// able to hijack the slot of a real table when the placeholders are filled back.
var tablePlaceholderMark = func() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "__TABLE_PLACEHOLDER_"
	}
	return "__TBL" + hex.EncodeToString(b[:]) + "_"
}()

func findTableSegments(html string) []tableSegment {
	var segs []tableSegment

	figures := scanElements(html, figureTokRe, isFigureTable)
	for _, f := range figures {
		segs = append(segs, tableSegment{
			start: f.start, end: f.end,
			html: f.full(html),
		})
	}
	insideFigure := func(r elemRange) bool {
		for _, f := range figures {
			if r.start >= f.start && r.end <= f.end {
				return true
			}
		}
		return false
	}

	for _, t := range scanElements(html, tableTokRe, isTabularOpen) {
		if insideFigure(t) {
			continue // already covered by its <figure class="ltx_table">
		}
		segs = append(segs, tableSegment{
			start: t.start, end: t.end,
			html: t.full(html),
		})
	}

	for _, s := range scanElements(html, spanTokRe, isSpanTabular) {
		if insideFigure(s) {
			continue
		}
		segs = append(segs, tableSegment{
			start: s.start, end: s.end,
			html: s.full(html),
		})
	}

	// Keep outermost, non-overlapping segments in document order: a span tabular
	// inside a table cell, or a table inside another table's cell, must not be
	// placeholdered twice (the inner one would leak into the text of the outer).
	sort.SliceStable(segs, func(i, j int) bool {
		if segs[i].start != segs[j].start {
			return segs[i].start < segs[j].start
		}
		return segs[i].end > segs[j].end
	})
	var kept []tableSegment
	limit := -1
	for _, s := range segs {
		if s.start < limit {
			continue
		}
		kept = append(kept, s)
		limit = s.end
	}
	return kept
}

type tableInfo struct {
	placeholder string
	markdown    string
}

// extractTables converts every table of the document, in document order.
func extractTables(html string) ([]tableSegment, []tableInfo) {
	segs := findTableSegments(html)
	infos := make([]tableInfo, 0, len(segs))
	for i, s := range segs {
		infos = append(infos, tableInfo{
			placeholder: tablePlaceholderMark + strconv.Itoa(i) + "__",
			markdown:    convertTableSegment(s),
		})
	}
	return segs, infos
}

// insertTablePlaceholders replaces the segments by their placeholders. It walks
// backwards so earlier offsets stay valid.
func insertTablePlaceholders(html string, segs []tableSegment, infos []tableInfo) string {
	for i := len(segs) - 1; i >= 0; i-- {
		html = html[:segs[i].start] + infos[i].placeholder + html[segs[i].end:]
	}
	return html
}

// contentBlock is one rendered piece of a table segment: a tabular panel, a
// verbatim listing, or the leftover text of markup this scanner cannot parse.
type contentBlock struct {
	start int
	end   int
	text  string
	table bool
}

// convertTableSegment renders one table segment: each caption of the segment
// followed by the block it belongs to.
//
// LaTeXML puts <figcaption> where \caption sits in the source: above the tabular
// in most papers, below it in others (all 15 tables of arXiv 2606.19451), and
// once per panel in a flex figure (VoiceMem's S4.T2 carries "Table 1: Factual
// memory results." and "Table 2: Persona memory results." — which is where the
// second caption used to vanish). Keeping the source position is not an option
// either: a caption wedged between two tables reads as the title of the *next*
// one, which mislabels numbers systematically. So the pairing is computed from
// the offsets and the caption is always rendered in front of its own block.
func convertTableSegment(seg tableSegment) string {
	s := seg.html

	var contents []contentBlock
	var drop []elemRange

	renderRows := func(elems []elemRange, parse func(string) []tableRow) {
		for _, e := range elems {
			if md := renderTableMarkdown(parse(e.inner(s))); md != "" {
				contents = append(contents, contentBlock{e.start, e.end, strings.TrimRight(md, "\n"), true})
				drop = append(drop, e)
			}
		}
	}
	if panels := scanElements(s, tableTokRe, isTabularOpen); len(panels) > 0 {
		renderRows(panels, parseTabularRows)
	} else {
		// Span-based tabular: emitted when a table sits inside a flex panel — no
		// <table>/<tr>/<td> elements at all.
		renderRows(scanElements(s, spanTokRe, isSpanTabular), parseSpanRows)
	}

	// A <figure class="ltx_table"> can wrap a verbatim listing (prompt templates
	// and the like) instead of, or beside, a tabular.
	for _, pre := range scanElements(s, preTokRe, nil) {
		if insideBlock(contents, pre) {
			continue // already part of a table cell
		}
		// Strip tags before decoding entities: a literal "<|...|>" in the source
		// is entity-escaped and must survive tag stripping.
		body := strings.Trim(decodeEntities(stripHTMLTags(pre.inner(s))), " \t\n")
		if body != "" {
			contents = append(contents, contentBlock{pre.start, pre.end, "```\n" + body + "\n```", false})
			drop = append(drop, pre)
		}
	}

	captions := scanElements(s, figcaptionTokRe, nil)

	if !hasTableBlock(contents) && strings.Contains(s, "<table") {
		// Tabular markup that produced nothing: unbalanced tags, or a nesting this
		// scanner does not model. Keeping the text beats dropping the numbers.
		if text := textOutside(s, append(drop, captions...)); text != "" {
			contents = append(contents, contentBlock{len(s), len(s), text, false})
		}
	}
	sort.SliceStable(contents, func(i, j int) bool { return contents[i].start < contents[j].start })

	// Rendering. A segment with a single block gets its captions moved in front of
	// it, whichever side \caption sat in the source: left where it was, a caption
	// trailing its own table ends up wedged between two tables and reads as the
	// title of the next one. With several blocks the source order is kept, because
	// there every caption already sits beside the panel it describes (above it in
	// VoiceMem's S4.T2, below it in a subtable layout) and no offset arithmetic can
	// tell those two layouts apart.
	var head []string
	pieces := make([]renderPiece, 0, len(contents))
	for _, c := range captions {
		text := cleanInlineText(c.inner(s))
		if text == "" {
			continue
		}
		if len(contents) <= 1 {
			head = append(head, "*"+text+"*") // single table, or picture-only
			continue
		}
		pieces = append(pieces, renderPiece{c.start, "*" + text + "*"})
	}
	for _, c := range contents {
		pieces = append(pieces, renderPiece{c.start, c.text})
	}
	if len(head)+len(pieces) == 0 {
		return ""
	}
	sort.SliceStable(pieces, func(i, j int) bool { return pieces[i].start < pieces[j].start })
	out := append(head, textOf(pieces)...)
	return strings.Join(out, "\n\n") + "\n"
}

// renderPiece is one unit of segment output positioned by its source offset.
type renderPiece struct {
	start int
	text  string
}

func textOf(pieces []renderPiece) []string {
	out := make([]string, len(pieces))
	for i, p := range pieces {
		out[i] = p.text
	}
	return out
}

func hasTableBlock(contents []contentBlock) bool {
	for _, c := range contents {
		if c.table {
			return true
		}
	}
	return false
}

func insideBlock(contents []contentBlock, r elemRange) bool {
	for _, c := range contents {
		if r.start >= c.start && r.end <= c.end {
			return true
		}
	}
	return false
}

// textOutside returns the readable text of s with the given ranges removed. The
// ranges arrive from different scans and are not necessarily ordered.
func textOutside(s string, drop []elemRange) string {
	sort.SliceStable(drop, func(i, j int) bool { return drop[i].start < drop[j].start })
	var b strings.Builder
	last := 0
	for _, d := range drop {
		if d.start > last {
			b.WriteString(s[last:d.start])
		}
		if d.end > last {
			last = d.end
		}
	}
	b.WriteString(s[last:])
	return cleanInlineText(b.String())
}

// cleanInlineText reduces a caption (or any inline fragment) to one line of
// markdown text. Entities are decoded here because table segments are lifted out
// of the document before the document-wide decoding pass runs.
func cleanInlineText(html string) string {
	t := decodeEntities(stripHTMLTags(html))
	return strings.TrimSpace(wsRe.ReplaceAllString(t, " "))
}

func restoreTables(html string, tables []tableInfo) string {
	for _, t := range tables {
		body := "\n\n" + t.markdown + "\n\n"
		if strings.TrimSpace(t.markdown) == "" {
			body = "" // dropped table (decorative tabular): leave no trace
		}
		html = strings.Replace(html, t.placeholder, body, 1)
	}
	return html
}
