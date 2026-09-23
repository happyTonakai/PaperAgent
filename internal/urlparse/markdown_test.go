package urlparse

import (
	"strings"
	"testing"
)

// LaTeXML puts the id attribute before class, which is what the old regexes
// failed to match.
const tableFigureClassNotFirst = `<figure id="S3.T1" class="ltx_table">
<figcaption class="ltx_caption"><span class="ltx_tag ltx_tag_table">Table 1: </span>Dataset overview.</figcaption>
<table id="S3.T1.4" class="ltx_tabular ltx_centering ltx_guessed_headers ltx_align_middle">
<thead>
<tr><td class="ltx_td ltx_th ltx_align_left">Name</td><td class="ltx_td ltx_th ltx_align_right">Hours</td></tr>
</thead>
<tbody>
<tr><td class="ltx_td ltx_align_left">Fisher</td><td class="ltx_td ltx_align_right">2,000</td></tr>
</tbody>
</table>
</figure>`

func TestConvertTableToMarkdown_ClassAttributeNotFirst(t *testing.T) {
	// Regression: LaTeXML emits <table id="..." class="ltx_tabular ...">. The
	// previous regexes required class to be the first attribute, so every table
	// silently degraded to a caption-only placeholder ("&gt; Table N:").
	md := convertTableToMarkdown(tableFigureClassNotFirst)
	if !strings.Contains(md, "| Name | Hours |") {
		t.Errorf("header row missing, got: %q", md)
	}
	if !strings.Contains(md, "| Fisher | 2,000 |") {
		t.Errorf("body row missing, got: %q", md)
	}
	if !strings.Contains(md, "| :---| ---:|") {
		t.Errorf("column alignment row missing or wrong, got: %q", md)
	}
	if !strings.Contains(md, "Table 1:") {
		t.Errorf("caption missing, got: %q", md)
	}
}

func TestConvertTableToMarkdown_ClassAttributeFirst(t *testing.T) {
	// The original (class-first) form must keep working.
	figure := `<figure class="ltx_table"><figcaption>Table 2: X.</figcaption>
<table class="ltx_tabular"><tr><td>A</td><td>B</td></tr><tr><td>1</td><td>2</td></tr></table></figure>`
	md := convertTableToMarkdown(figure)
	if !strings.Contains(md, "| A | B |") || !strings.Contains(md, "| 1 | 2 |") {
		t.Errorf("table not converted, got: %q", md)
	}
}

func TestConvertTableToMarkdown_HeaderCellsWithTH(t *testing.T) {
	figure := `<figure class="ltx_table"><figcaption>Table 3: Y.</figcaption>
<table class="ltx_tabular"><thead><tr><th>Col</th><th>Val</th></tr></thead>
<tbody><tr><td>a</td><td>1</td></tr></tbody></table></figure>`
	md := convertTableToMarkdown(figure)
	if !strings.Contains(md, "| Col | Val |") {
		t.Errorf("<th> header cells not converted, got: %q", md)
	}
}

func TestConvertTableToMarkdown_HTMLIsEscapedInCells(t *testing.T) {
	figure := `<figure class="ltx_table"><figcaption>Table 4: Z.</figcaption>
<table class="ltx_tabular"><tr><td>a &lt; b &amp; c</td><td>&gt;= 3</td></tr></table></figure>`
	md := convertTableToMarkdown(figure)
	if !strings.Contains(md, "a < b & c") || !strings.Contains(md, ">= 3") {
		t.Errorf("entities not decoded, got: %q", md)
	}
}

// LaTeXML emits this variant when a table sits inside a flex panel: no <table>
// element at all, the ltx_tabular/ltx_tr/ltx_td classes are on <span>s.
const spanBasedTableFigure = `<figure id="A6.T10" class="ltx_table">
<figcaption class="ltx_caption">Table 10: SteerBench results.</figcaption>
<span class="ltx_inline-block"><span class="ltx_tabular ltx_guessed_headers ltx_align_middle">
<span class="ltx_thead"><span class="ltx_tr">
<span class="ltx_td ltx_align_left ltx_th ltx_th_column"><span class="ltx_text">System</span></span>
<span class="ltx_td ltx_nopad_r ltx_align_right ltx_th ltx_th_column"><span class="ltx_text">Audio APR (%)</span></span>
</span></span>
<span class="ltx_tbody"><span class="ltx_tr">
<span class="ltx_td ltx_align_left ltx_th ltx_th_row"><span class="ltx_text">Moshi</span></span>
<span class="ltx_td ltx_nopad_r ltx_align_right"><span class="ltx_text">12.5</span></span>
</span><span class="ltx_tr">
<span class="ltx_td ltx_align_left ltx_th ltx_th_row"><span class="ltx_text">SFT+DPO</span></span>
<span class="ltx_td ltx_nopad_r ltx_align_right"><span class="ltx_text">88.9</span></span>
</span></span>
</span></span>
</figure>`

func TestConvertTableToMarkdown_SpanBasedTable(t *testing.T) {
	md := convertTableToMarkdown(spanBasedTableFigure)
	if !strings.Contains(md, "| System | Audio APR (%) |") {
		t.Errorf("span-based header row missing, got: %q", md)
	}
	if !strings.Contains(md, "| Moshi | 12.5 |") {
		t.Errorf("span-based body row missing, got: %q", md)
	}
	if !strings.Contains(md, "| SFT+DPO | 88.9 |") {
		t.Errorf("span-based second row missing, got: %q", md)
	}
}

func TestConvertTableToMarkdown_VerbatimFigure(t *testing.T) {
	// Some "tables" are verbatim listings (e.g. prompt templates) wrapped in a
	// <figure class="ltx_table">. The text must be kept, not dropped.
	figure := `<figure class="ltx_table"><figcaption>Table 21: Judge prompt.</figcaption>
<pre class="ltx_verbatim">ROLE: {role_name}
PERSONA: {persona}</pre></figure>`
	md := convertTableToMarkdown(figure)
	if !strings.Contains(md, "ROLE: {role_name}") || !strings.Contains(md, "PERSONA: {persona}") {
		t.Errorf("verbatim content dropped, got: %q", md)
	}
	if !strings.Contains(md, "Table 21:") {
		t.Errorf("caption missing, got: %q", md)
	}
}

func TestConvertTableToMarkdown_PictureOnlyFigure(t *testing.T) {
	// A figure whose "table" is an inline SVG image has no recoverable text; the
	// caption is the only sensible output.
	figure := `<figure id="S5.T4" class="ltx_table"><figcaption>Table 4: Qualitative example.</figcaption>
<span class="ltx_inline-block"><svg class="ltx_picture"><g><path d="M0 0"/></g></svg></span></figure>`
	md := convertTableToMarkdown(figure)
	if !strings.Contains(md, "Table 4:") {
		t.Errorf("caption should be kept, got: %q", md)
	}
	if strings.Contains(md, "|") {
		t.Errorf("no table should be produced, got: %q", md)
	}
}
