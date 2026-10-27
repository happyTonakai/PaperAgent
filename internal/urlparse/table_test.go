package urlparse

import (
	"regexp"
	"strings"
	"testing"
)

// helper: number of cells in a rendered markdown row, honouring \ escapes.
func mdCells(line string) int {
	return strings.Count(strings.ReplaceAll(line, `\|`, ""), "|") - 1
}

func tableBlocks(md string) []string {
	lines := strings.Split(md, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "|") && i+1 < len(lines) &&
			regexp.MustCompile(`^\|[\s:|-]+\|$`).MatchString(lines[i+1]) {
			j := i
			for j < len(lines) && strings.HasPrefix(lines[j], "|") {
				j++
			}
			out = append(out, strings.Join(lines[i:j], "\n"))
			i = j
		}
	}
	return out
}

const twoLevelHeaderFigure = `<figure id="S4.T2" class="ltx_table">
<figcaption class="ltx_caption"><span class="ltx_tag ltx_tag_table">Table 2: </span>Factual memory results.</figcaption>
<div class="ltx_flex_figure"><div class="ltx_flex_cell">
<table id="S4.T2.4" class="ltx_tabular ltx_figure_panel">
<tr><td class="ltx_td ltx_align_left"><span class="ltx_text ltx_font_bold">Method</span></td>
    <td class="ltx_td ltx_align_center" colspan="2">LoCoMo</td>
    <td class="ltx_td ltx_align_center" colspan="2">Memora</td>
    <td class="ltx_td">Avg.</td></tr>
<tr><td class="ltx_td"></td><td class="ltx_td">Single</td><td class="ltx_td ltx_nopad_r">Multi</td>
    <td class="ltx_td">Rem.</td><td class="ltx_td ltx_nopad_r">Rea.</td><td></td></tr>
<tr><td class="ltx_td ltx_align_left" colspan="5">Reference</td></tr>
<tr><td class="ltx_td">Full-Context</td><td>75.87</td><td>72.27</td><td>45.45</td><td>73.92</td><td>60.49</td></tr>
</table>
</div></div>
</figure>`

// The header of a real arXiv table (VoiceMem, Table 2): the first row carries 5
// physical cells for a 6-column table. Emitting them verbatim produced a header
// with fewer columns than the data rows, so every renderer dropped the tail.
func TestConvertTable_ColspanHeaderExpandedToGridWidth(t *testing.T) {
	md := convertFigureTable(twoLevelHeaderFigure)
	blocks := tableBlocks(md)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 table block, got %d:\n%s", len(blocks), md)
	}
	lines := strings.Split(blocks[0], "\n")
	if len(lines) < 3 {
		t.Fatalf("table too short: %q", blocks[0])
	}
	want := mdCells(lines[0])
	if want != 6 {
		t.Errorf("header has %d columns, want 6: %q", want, lines[0])
	}
	for _, l := range lines[1:] {
		if mdCells(l) != want {
			t.Errorf("row has %d columns, want %d: %q", mdCells(l), want, l)
		}
	}
	// The two header levels are flattened into one row, group label first.
	if !strings.Contains(lines[0], "| LoCoMo / Single | Multi |") || !strings.Contains(lines[0], "| Memora / Rem. | Rea. |") {
		t.Errorf("two-level header not merged: %q", lines[0])
	}
	if !strings.HasPrefix(md, "*Table 2: Factual memory results.*") {
		t.Errorf("caption should precede the table, got %q", md)
	}
	// A full-width label row is a section separator: keep it, bolded, padded.
	if !strings.Contains(md, "| **Reference** |  |  |  |  |  |") {
		t.Errorf("section label row wrong: %q", md)
	}
}

// A flex figure with two panels side by side used to contribute only its first
// <table>; the second panel disappeared without a trace.
const multiPanelFigure = `<figure id="S4.T2" class="ltx_table">
<figcaption class="ltx_caption">Table 2: Results.</figcaption>
<div class="ltx_flex_figure">
<div class="ltx_flex_cell"><table id="S4.T2.4" class="ltx_tabular ltx_figure_panel">
<tr><td>Method</td><td>LoCoMo</td></tr><tr><td>Ours</td><td>76.39</td></tr></table></div>
<div class="ltx_flex_cell"><table id="S4.T2.7" class="ltx_tabular ltx_figure_panel">
<tr><td>Method</td><td>PersonaMem</td></tr><tr><td>Ours</td><td>74.16</td></tr></table></div>
</div></figure>`

func TestConvertTable_MultiPanelFigureKeepsEveryPanel(t *testing.T) {
	md := convertFigureTable(multiPanelFigure)
	if !strings.Contains(md, "76.39") || !strings.Contains(md, "74.16") {
		t.Errorf("a panel was dropped:\n%s", md)
	}
	if n := strings.Count(md, "Table 2:"); n != 1 {
		t.Errorf("caption repeated %d times, want 1", n)
	}
	if n := len(tableBlocks(md)); n != 2 {
		t.Errorf("got %d table blocks, want 2", n)
	}
}

// rowspan (here on a body row label) must fill the covered rows: markdown cannot
// merge, and blank cells would silently lose the grouping.
const rowspanFigure = `<figure class="ltx_table"><figcaption>Table 5: Ablation.</figcaption>
<table class="ltx_tabular">
<tr><td>Dataset</td><td>Strategy</td><td>Acc.</td></tr>
<tr><td rowspan="2">LoCoMo</td><td>description</td><td>91.2</td></tr>
<tr><td>macro-expansion</td><td>91.10</td></tr>
<tr><td rowspan="2">LongMemEval</td><td>description</td><td>82.0</td></tr>
<tr><td>macro-expansion</td><td>80.4</td></tr>
</table></figure>`

func TestConvertTable_RowspanRepeatsLabel(t *testing.T) {
	md := convertFigureTable(rowspanFigure)
	for _, want := range []string{
		"| LoCoMo | description | 91.2 |",
		"| LoCoMo | macro-expansion | 91.10 |",
		"| LongMemEval | macro-expansion | 80.4 |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q, got:\n%s", want, md)
		}
	}
}

// LaTeXML's ltx_guessed_headers marks the label column of *every* row with
// ltx_th_column. Treating that class as header material merges the whole table
// into one header row and destroys every data row.
const guessedHeaderFigure = `<figure class="ltx_table"><figcaption>Table 1: Reconstruction.</figcaption>
<table class="ltx_tabular ltx_guessed_headers">
<tr><td class="ltx_td ltx_th ltx_th_column ltx_align_left">Dataset</td><td class="ltx_td ltx_align_right">PSNR</td></tr>
<tr><td class="ltx_td ltx_th ltx_th_column ltx_align_left" colspan="2">GenericShapes</td></tr>
<tr><td class="ltx_td ltx_th ltx_th_column ltx_align_left">AE</td><td class="ltx_td ltx_align_right">9.64</td></tr>
<tr><td class="ltx_td ltx_th ltx_th_column ltx_align_left">VAE</td><td class="ltx_td ltx_align_right">9.47</td></tr>
</table></figure>`

func TestConvertTable_GuessedHeadersDoNotSwallowBody(t *testing.T) {
	md := convertFigureTable(guessedHeaderFigure)
	blocks := tableBlocks(md)
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d:\n%s", len(blocks), md)
	}
	lines := strings.Split(blocks[0], "\n")
	if lines[0] != "| Dataset | PSNR |" {
		t.Errorf("header row wrong: %q", lines[0])
	}
	if len(lines) != 5 { // header + separator + section label + 2 data rows
		t.Errorf("want 5 lines, got %d:\n%s", len(lines), blocks[0])
	}
	if !strings.Contains(md, "| AE | 9.64 |") || !strings.Contains(md, "| VAE | 9.47 |") {
		t.Errorf("body rows lost:\n%s", md)
	}
}

// LaTeXML emits a one-cell tabular around \rule separators in the header block.
// It is decoration and must not become a table.
func TestConvertTable_DecorativeTabularDropped(t *testing.T) {
	figure := `<table id="p1.1" class="ltx_tabular ltx_centering">
<tr id="p1.1.1" class="ltx_tr"><td id="p1.1.1.1" class="ltx_td ltx_align_center"><span class="ltx_rule" style="height:24.0pt;"> </span></td></tr>
</table>`
	if md := convertFigureTable(figure); md != "" {
		t.Errorf("decorative tabular should be dropped, got %q", md)
	}
}

func TestConvertTable_PipeInCellEscaped(t *testing.T) {
	figure := `<table class="ltx_tabular"><tr><td>Method</td><td>Score</td></tr>
<tr><td>a | b</td><td>1</td></tr></table>`
	md := convertFigureTable(figure)
	if !strings.Contains(md, `| a \| b | 1 |`) {
		t.Errorf("pipe not escaped, got %q", md)
	}
	if len(tableBlocks(md)) != 1 {
		t.Errorf("escaped pipe broke the table block: %q", md)
	}
}

// Numeric columns are centred in the source, but LaTeXML puts ltx_align_left on
// the <td> and centres the content in a nested paragraph.
func TestConvertTable_AlignmentFromCellContent(t *testing.T) {
	figure := `<table class="ltx_tabular">
<tr><td class="ltx_td ltx_align_left">Method</td><td class="ltx_td ltx_align_left ltx_align_top"><span class="ltx_p ltx_align_center">Acc.</span></td></tr>
<tr><td class="ltx_td ltx_align_left">Ours</td><td class="ltx_td ltx_align_left ltx_align_top"><span class="ltx_p ltx_align_center">94.6</span></td></tr>
</table>`
	md := convertFigureTable(figure)
	if !strings.Contains(md, "| :---| :---:|") {
		t.Errorf("column alignment not derived from content, got %q", md)
	}
}

// A tabular nested inside a cell used to truncate the outer match (the old
// regexes were non-greedy), producing two half tables.
func TestConvertTable_NestedTabularStaysInItsCell(t *testing.T) {
	figure := `<figure class="ltx_table"><figcaption>Table 9: Nested.</figcaption>
<table class="ltx_tabular">
<tr><td>Outer</td><td>Inner</td></tr>
<tr><td>row</td><td><table class="ltx_tabular"><tr><td>n1</td><td>n2</td></tr><tr><td>n3</td><td>n4</td></tr></table></td></tr>
<tr><td>tail</td><td>9</td></tr>
</table></figure>`
	md := convertFigureTable(figure)
	blocks := tableBlocks(md)
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d:\n%s", len(blocks), md)
	}
	if !strings.Contains(blocks[0], "| row | n1 n2 n3 n4 |") {
		t.Errorf("nested tabular not flattened into its cell:\n%s", blocks[0])
	}
	if !strings.Contains(blocks[0], "| tail | 9 |") {
		t.Errorf("rows after the nested table lost:\n%s", blocks[0])
	}
}

// The placeholder round-trip: figure tables, stand-alone tabulars and span
// tables must all come back in document order (the old code located them twice,
// with two different regex sets).
func TestHTMLToMarkdown_TablePlaceholdersRoundTrip(t *testing.T) {
	html := `<html><body><div class="ltx_page_content"><article>
<p>before</p>
<table id="p1.1" class="ltx_tabular"><tr><td>A</td><td>B</td></tr><tr><td>1</td><td>2</td></tr></table>
<p>middle</p>
<figure id="S2.T1" class="ltx_table"><figcaption>Table 1: Caption.</figcaption>
<table id="S2.T1.1" class="ltx_tabular"><tr><td>C</td><td>D</td></tr></table></figure>
<p>after</p>
</article></div></body></html>`
	md := HTMLToMarkdown(html)
	if strings.Contains(md, tablePlaceholderMark) {
		t.Errorf("placeholder leaked:\n%s", md)
	}
	blocks := tableBlocks(md)
	if len(blocks) != 2 {
		t.Fatalf("want 2 tables, got %d:\n%s", len(blocks), md)
	}
	if !strings.HasPrefix(blocks[0], "| A | B |") {
		t.Errorf("first table wrong: %q", blocks[0])
	}
	if !strings.Contains(blocks[1], "| C | D |") {
		t.Errorf("second table wrong: %q", blocks[1])
	}
	before := strings.Index(md, "before")
	middle := strings.Index(md, "middle")
	ta := strings.Index(md, "| A | B |")
	tb := strings.Index(md, "| C | D |")
	if !(before < ta && ta < middle && middle < tb) {
		t.Errorf("tables came back out of order: %d %d %d %d", before, ta, middle, tb)
	}
}

// --- Review round 2: structures that used to corrupt the table silently -------

// A header cell may rowspan past the header block ("Setting" over three rows,
// the last of which is data). Letting the rowspan decide the header size turns
// that data row into column titles, which no reader can notice.
func TestConvertTable_HeaderRowspanIntoBodyKeepsDataRow(t *testing.T) {
	md := convertFigureTable(`<table class="ltx_tabular">
<thead><tr><th rowspan="3">Setting</th><th colspan="2">LoCoMo</th></tr>
<tr><th>Single</th><th>Multi</th></tr></thead>
<tbody><tr><td>9.91</td><td>1.1</td><td>2.2</td></tr>
<tr><td>Base</td><td>5.5</td><td>6.6</td></tr></tbody></table>`)
	if strings.Contains(md, "Single / 9.91") || strings.Contains(md, "Multi / 1.1") {
		t.Errorf("data row merged into the header:\n%s", md)
	}
	if !strings.Contains(md, "9.91") || !strings.Contains(md, "2.2") {
		t.Errorf("data row lost:\n%s", md)
	}
}

// Same failure mode from the other side: the row under a group header is data
// (measurements), not the leaf label row, even though column 0 is empty.
func TestConvertTable_NumericRowUnderGroupStaysData(t *testing.T) {
	md := convertFigureTable(`<table class="ltx_tabular">
<tr><td></td><td colspan="2">LoCoMo</td></tr>
<tr><td></td><td>75.87</td><td>72.27</td></tr>
<tr><td>Ours</td><td>80.1</td><td>81.2</td></tr></table>`)
	if strings.Contains(md, "LoCoMo / 75.87") {
		t.Errorf("measurement row merged into the header:\n%s", md)
	}
	if !strings.Contains(md, "| 75.87 | 72.27 |") {
		t.Errorf("measurement row lost:\n%s", md)
	}
}

// VoiceMem (arXiv 2608.26005) Table 2 is one <figure class="ltx_table"> holding
// two panels, each with its own <figcaption>. Taking the first caption only left
// "Table 2: Persona memory results." out of the document entirely.
func TestConvertTable_FlexFigurePausesEachCaptionWithItsPanel(t *testing.T) {
	md := convertFigureTable(`<figure id="S4.T2" class="ltx_table">
<figcaption class="ltx_caption"><span class="ltx_tag ltx_tag_table">Table 1: </span>Factual memory results.</figcaption>
<div class="ltx_flex_cell"><table id="S4.T2.4" class="ltx_tabular ltx_figure_panel">
<tr><td>Method</td><td>LoCoMo</td></tr><tr><td>Ours</td><td>76.39</td></tr></table></div>
<figcaption class="ltx_caption"><span class="ltx_tag ltx_tag_table">Table 2: </span>Persona memory results.</figcaption>
<div class="ltx_flex_cell"><table id="S4.T2.7" class="ltx_tabular ltx_figure_panel">
<tr><td>Method</td><td>PersonaMem</td></tr><tr><td>Ours</td><td>74.16</td></tr></table></div>
</figure>`)
	first := strings.Index(md, "Table 1:")
	one := strings.Index(md, "76.39")
	second := strings.Index(md, "Table 2:")
	two := strings.Index(md, "74.16")
	if second < 0 {
		t.Fatalf("second caption dropped:\n%s", md)
	}
	if !(first < one && one < second && second < two) {
		t.Errorf("caption/panel pairing wrong (first=%d one=%d second=%d two=%d):\n%s", first, one, second, two, md)
	}
}

// A caption is lifted out of the document before the document-wide entity
// decoding runs, so it has to decode its own entities.
func TestConvertTable_CaptionDecodesEntities(t *testing.T) {
	md := convertFigureTable(`<figure class="ltx_table"><figcaption>Table 5: R&amp;D &lt;baseline&gt; &#215;2.</figcaption>
<table class="ltx_tabular"><tr><td>a</td><td>b</td></tr></table></figure>`)
	if !strings.Contains(md, "R&D <baseline> ×2") {
		t.Errorf("caption entities left raw: %q", md)
	}
}

// Inside a formula "\|" is the norm double bar: escaping a pipe that way would
// change the maths (and KaTeX would happily render it). "\vert" keeps the bar.
func TestConvertTable_PipeInsideFormulaStaysASingleBar(t *testing.T) {
	md := convertFigureTable(`<table class="ltx_tabular">
<tr><td>Dataset</td><td>Num</td></tr>
<tr><td>x</td><td>$\max(|{\mathcal{C}}|)$</td></tr></table>`)
	if !strings.Contains(md, `$\max(\vert{\mathcal{C}}\vert)$`) {
		t.Errorf("formula pipe not converted to \\vert: %q", md)
	}
	if strings.Contains(md, `\|`) {
		t.Errorf("formula pipe escaped as norm: %q", md)
	}
	if n := len(tableBlocks(md)); n != 1 {
		t.Errorf("want 1 block, got %d", n)
	}
}

// A paper that quotes the placeholder itself must not be able to steal a table's
// slot when placeholders are filled back in.
func TestHTMLToMarkdown_LiteralPlaceholderTextCannotHijackTable(t *testing.T) {
	html := `<html><body><div class="ltx_page_content"><article>
<p>We mention __TABLE_PLACEHOLDER_0__ in the prose.</p>
<figure class="ltx_table"><figcaption>Table 1: C.</figcaption>
<table class="ltx_tabular"><tr><td>A</td><td>B</td></tr><tr><td>1</td><td>2</td></tr></table></figure>
</article></div></body></html>`
	md := HTMLToMarkdown(html)
	if !strings.Contains(md, "We mention __TABLE_PLACEHOLDER_0__ in the prose.") {
		t.Errorf("prose text was replaced by a table:\n%s", md)
	}
	if !strings.Contains(md, "| 1 | 2 |") {
		t.Errorf("table lost:\n%s", md)
	}
}

// Dropping a decorative tabular must not leave a hole of blank lines behind.
func TestHTMLToMarkdown_DroppedTableLeavesNoBlankRun(t *testing.T) {
	html := `<html><body><div class="ltx_page_content"><article>
<p>before</p>
<table class="ltx_tabular"><tr><td class="ltx_align_center"><span class="ltx_rule" style="height:24.0pt;"> </span></td></tr></table>
<p>after</p></article></div></body></html>`
	md := HTMLToMarkdown(html)
	if strings.Contains(md, "\n\n\n") {
		t.Errorf("blank run left where the table was dropped: %q", md)
	}
}

// Unbalanced markup must not swallow the numbers: if no panel can be parsed out
// of a table that is plainly there, the leftover text is kept.
func TestConvertTable_UnparsableMarkupKeepsText(t *testing.T) {
	md := convertFigureTable(`<figure class="ltx_table"><figcaption>Table 8: Broken.</figcaption>
<table class="ltx_tabular"><tr><td>Model</td><td>Score</td></tr>
<tr><td>Ours</td><td>94.6</td></tr>
</figure>`)
	if !strings.Contains(md, "94.6") || !strings.Contains(md, "Ours") {
		t.Errorf("cell text dropped with the markup: %q", md)
	}
}

// --- Review round 3: escapes and pairings that the corpus cannot see ---------

// "\vert" is a TeX command name and command names run to the end of the letters:
// "$|V|$" must not become "\vertV".
func TestEscapeCellPipesTerminatesCommandBeforeLetter(t *testing.T) {
	tests := []struct{ in, want string }{
		{`$|V|$`, `$\vert{}V\vert$`},
		{`$p(x|z)$`, `$p(x\vert{}z)$`},
		{`$|2|$`, `$\vert2\vert$`},
		{`$\max(|{\mathcal{C}}|)$`, `$\max(\vert{\mathcal{C}}\vert)$`},
		// Already escaped by the source: leave the pair alone, in maths and in text.
		{`$\|x\|_1$`, `$\|x\|_1$`},
		{`pre \| escaped`, `pre \| escaped`},
		// Text mode keeps the markdown escape.
		{`a|b`, `a\|b`},
		// Stray dollar signs must not be paired into a fake formula: that would
		// inject \vert into ordinary prose.
		{`two $ dollars | between $ them`, `two $ dollars \| between $ them`},
		{`US$ 5 and 6$ off`, `US$ 5 and 6$ off`},
		{`$a|b$ and c|d`, `$a\vert{}b$ and c\|d`},
	}
	for _, tt := range tests {
		if got := escapeCellPipes(tt.in); got != tt.want {
			t.Errorf("escapeCellPipes(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A numbers-only row under a group header is data even in a narrow table and even
// when the values are wrapped in $\mathbf{...}$.
func TestConvertTable_NumericDataRowsNotMerged(t *testing.T) {
	narrow := convertFigureTable(`<table class="ltx_tabular">
<tr><td></td><td colspan="2">LoCoMo</td></tr>
<tr><td>Ours</td><td>75.87</td><td>72.27</td></tr></table>`)
	if strings.Contains(narrow, "LoCoMo / 75.87") || !strings.Contains(narrow, "| Ours | 75.87 | 72.27 |") {
		t.Errorf("row label lost: %q", narrow)
	}
	bold := convertFigureTable(`<table class="ltx_tabular">
<tr><td>Method</td><td colspan="2">LoCoMo</td></tr>
<tr><td>Ours</td><td>$\mathbf{94.6}$</td><td>$\mathbf{91.6}$</td></tr></table>`)
	if strings.Contains(bold, "LoCoMo / $\\mathbf{94.6}$") || !strings.Contains(bold, `| Ours | $\mathbf{94.6}$ | $\mathbf{91.6}$ |`) {
		t.Errorf("math-wrapped measurement merged into the header: %q", bold)
	}
	text := convertFigureTable(`<table class="ltx_tabular">
<tr><td></td><td colspan="2">Probe</td></tr>
<tr><td>Ours</td><td>yes</td><td>no</td></tr></table>`)
	if !strings.Contains(text, "| Ours | yes | no |") {
		t.Errorf("text-valued data row lost: %q", text)
	}
}

// The flip side: a leaf header row whose labels happen to be numbers ("100 | 1k |
// 10k" under "Steps") is still a header.
func TestConvertTable_NumericLeafLabelsStillMerge(t *testing.T) {
	md := convertFigureTable(`<table class="ltx_tabular">
<tr><th rowspan="2">Setting</th><th colspan="3">Steps</th></tr>
<tr><th>100</th><th>1k</th><th>10k</th></tr>
<tr><td>alpha</td><td>0.1</td><td>0.2</td><td>0.3</td></tr></table>`)
	if !strings.Contains(md, "| Setting | Steps / 100 | 1k | 10k |") {
		t.Errorf("numeric leaf labels not merged into the header: %q", md)
	}
	if !strings.Contains(md, "| alpha | 0.1 | 0.2 | 0.3 |") {
		t.Errorf("body row lost: %q", md)
	}
}

// A full-width title row above the real header ("MMLU (5-shot)") is header
// material too; the same shape lower down is a section label and must stay body.
func TestConvertTable_BannerRowAboveHeader(t *testing.T) {
	md := convertFigureTable(`<table class="ltx_tabular">
<tr><td colspan="3">MMLU (5-shot)</td></tr>
<tr><td>Model</td><td colspan="2">LoCoMo</td></tr>
<tr><td></td><td>Single</td><td>Multi</td></tr>
<tr><td>Ours</td><td>1.1</td><td>2.2</td></tr></table>`)
	if !strings.Contains(md, "| MMLU (5-shot) / Model | LoCoMo / Single | Multi |") {
		t.Errorf("banner not merged with the header: %q", md)
	}
	if !strings.Contains(md, "| Ours | 1.1 | 2.2 |") {
		t.Errorf("data row lost: %q", md)
	}
	section := convertFigureTable(twoLevelHeaderFigure)
	if !strings.Contains(section, "| **Reference** |") {
		t.Errorf("mid-table section label should stay a body row: %q", section)
	}
}

// LaTeXML puts \caption where the source has it: below the tabular in e.g. arXiv
// 2606.19451. Rendering that verbatim wedges the caption between two tables, and
// readers attach it to the following one.
func TestConvertTable_CaptionBelowTableIsRenderedAboveIt(t *testing.T) {
	md := convertFigureTable(`<figure class="ltx_table">
<table class="ltx_tabular"><tr><td>A</td></tr><tr><td>1</td></tr></table>
<figcaption>Table 1: Below the table.</figcaption></figure>`)
	if !strings.HasPrefix(md, "*Table 1: Below the table.*\n\n| A |") {
		t.Errorf("caption not moved in front of its own table: %q", md)
	}
}

// A figure can hold a listing beside a table, and a listing whose table is broken.
// Neither may be dropped, and neither may be printed twice.
func TestConvertTable_VerbatimAlongsideTable(t *testing.T) {
	both := convertFigureTable(`<figure class="ltx_table"><figcaption>Table 1: X.</figcaption>
<table class="ltx_tabular"><tr><td>A</td></tr><tr><td>1</td></tr></table>
<pre class="ltx_verbatim">PROMPT TEXT</pre></figure>`)
	if !strings.Contains(both, "PROMPT TEXT") || !strings.Contains(both, "| 1 |") {
		t.Errorf("listing next to a table dropped: %q", both)
	}
	if n := strings.Count(both, "PROMPT TEXT"); n != 1 {
		t.Errorf("listing printed %d times", n)
	}

	broken := convertFigureTable(`<figure class="ltx_table"><figcaption>Table 21: Judge prompt.</figcaption>
<pre class="ltx_verbatim">ROLE: {role_name}</pre><table class="ltx_tabular"><tr><td>half</td></figure>`)
	if n := strings.Count(broken, "ROLE: {role_name}"); n != 1 {
		t.Errorf("listing duplicated by the text fallback (%d copies):\n%s", n, broken)
	}
	if n := strings.Count(broken, "Table 21:"); n != 1 {
		t.Errorf("caption duplicated: %q", broken)
	}
	if !strings.Contains(broken, "half") {
		t.Errorf("text of the unparsable table lost: %q", broken)
	}
}

// The post-restore blank-line cleanup must not reach inside a restored listing.
func TestHTMLToMarkdown_BlankLinesInsideListingKept(t *testing.T) {
	html := `<html><body><div class="ltx_page_content"><article>
<figure class="ltx_table"><figcaption>Table 3: Prompt.</figcaption>
<pre class="ltx_verbatim">LINE1


LINE4</pre></figure>
<p>tail</p></article></div></body></html>`
	md := HTMLToMarkdown(html)
	if !strings.Contains(md, "LINE1\n\n\nLINE4") {
		t.Errorf("empty lines inside the listing were squeezed: %q", md)
	}
	if strings.Contains(strings.ReplaceAll(md, "LINE1\n\n\nLINE4", ""), "\n\n\n") {
		t.Errorf("blank run outside the listing not collapsed: %q", md)
	}
}

// A subtable layout puts each panel's caption *below* that panel. Pairing by
// proximity assigned both captions to the last panel, which relabelled the first
// table with the second one's caption.
func TestConvertTable_SubtableCaptionsStayWithTheirPanel(t *testing.T) {
	md := convertFigureTable(`<figure class="ltx_table"><div class="ltx_flex_figure">
<div class="ltx_flex_cell"><table class="ltx_tabular"><tr><td>Method</td><td>LoCoMo</td></tr><tr><td>Ours</td><td>76.4</td></tr></table><figcaption>(a) Alpha results.</figcaption></div>
<div class="ltx_flex_cell"><table class="ltx_tabular"><tr><td>Method</td><td>Memora</td></tr><tr><td>Ours</td><td>30.7</td></tr></table><figcaption>(b) Beta results.</figcaption></div>
</div></figure>`)
	blocks := strings.Split(md, "\n\n")
	want := []string{"| Method | LoCoMo |", "(a) Alpha results.", "| Method | Memora |", "(b) Beta results."}
	if len(blocks) != len(want) {
		t.Fatalf("want %d blocks, got %d:\n%s", len(want), len(blocks), md)
	}
	for i, w := range want {
		if !strings.Contains(blocks[i], w) {
			t.Errorf("block %d should hold %q, got %q", i, w, blocks[i])
		}
	}
}

// Two <span class="ltx_p"> lines in one cell are two lines, not one word.
func TestConvertTable_MultiLineCellKeepsWordBreak(t *testing.T) {
	md := convertFigureTable(`<table class="ltx_tabular">
<tr><td>Model</td><td>Notes</td></tr>
<tr><td>ours</td><td><span class="ltx_p ltx_align_left">first</span><span class="ltx_p ltx_align_left">line</span></td></tr></table>`)
	if !strings.Contains(md, "| ours | first line |") {
		t.Errorf("cell lines glued together: %q", md)
	}
}

// Leaf labels that happen to be numbers work the same without any <th> markup.
func TestConvertTable_NumericLeafLabelsWithoutTh(t *testing.T) {
	md := convertFigureTable(`<table class="ltx_tabular">
<tr><td rowspan="2">Setting</td><td colspan="3">Steps</td></tr>
<tr><td>100</td><td>1k</td><td>10k</td></tr>
<tr><td>alpha</td><td>0.1</td><td>0.2</td><td>0.3</td></tr></table>`)
	if !strings.Contains(md, "| Setting | Steps / 100 | 1k | 10k |") {
		t.Errorf("leaf row not recognised without th: %q", md)
	}
}

// Documented limits of the header heuristic, both in the "prefer to treat a row as
// data" direction: a table whose body rows are all <th>, and a data row that has
// neither a column-0 label nor numbers. If either ever shows up in real papers,
// these two tests say what the current (deliberate) output is.
func TestConvertTable_KnownHeaderLimits(t *testing.T) {
	allTh := convertFigureTable(`<table class="ltx_tabular">
<tr><th>a</th><th>1</th></tr><tr><th>b</th><th>2</th></tr><tr><th>c</th><th>3</th></tr></table>`)
	if !strings.Contains(allTh, "| a / b | 1 / 2 |") {
		t.Errorf("all-<th> table: header block expected to absorb rows, got %q", allTh)
	}
	blankLabel := convertFigureTable(`<table class="ltx_tabular">
<tr><td></td><td colspan="2">Probe</td></tr>
<tr><td></td><td>yes</td><td>no</td></tr>
<tr><td>ours</td><td>no</td><td>yes</td></tr></table>`)
	if !strings.Contains(blankLabel, "|  | Probe / yes | no |") {
		t.Errorf("label-less text row: expected it to be read as a leaf header, got %q", blankLabel)
	}
}
