package feishu

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/session"
)

type auditFormulaSpan struct {
	source    string // which field: "Title", "Summary", "Msg#N"
	latex     string // the raw LaTeX (with $ delimiters)
	converted bool   // could latexToUnicode convert it?
	result    string // conversion result (kept as-is if not converted)
}

type auditPaperReport struct {
	ref   string
	title string
	spans []auditFormulaSpan
	allOK bool
}

// TestLatexAudit loads every paper from ~/.config/paperagent/papers/,
// extracts all $...$ and $$...$$ formula spans from every text field
// (Title, InitialSummary, each Message.Content), runs latexToUnicode on
// each paper's text, and reports per-paper and aggregate statistics.
func TestLatexAudit(t *testing.T) {
	dir := config.PapersDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("papers dir does not exist:", dir)
		}
		t.Fatal(err)
	}

	var reports []auditPaperReport
	totalSpans := 0
	totalConverted := 0
	totalSkipped := 0

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// Load the paper
		p, err := session.LoadPaperByRef(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil || p == nil {
			continue
		}

		report := auditPaperReport{
			ref:   p.Ref(),
			title: p.Title,
		}

		// Helper: scan text for all $...$ and $$...$$ spans, test conversion
		scanField := func(text, source string) {
			if text == "" {
				return
			}
			// Use processMathSpans to find actual spans (it does the scan)
			// But we also want to log the *original* formula + conversion result.
			// We'll parse $$ and $ spans manually (same logic as processMathSpans).
			extractAndTestSpans(text, source, &report)
		}

		if p.Title != "" {
			scanField(p.Title, "Title")
		}
		if p.InitialSummary != "" {
			scanField(p.InitialSummary, "Summary")
		}
		for _, msg := range p.Messages {
			src := fmt.Sprintf("Msg#%d[%s]", msg.RoundNumber, msg.Role)
			scanField(msg.Content, src)
		}

		report.allOK = true
		for _, sp := range report.spans {
			if !sp.converted {
				report.allOK = false
			}
		}

		reports = append(reports, report)
		totalSpans += len(report.spans)
		for _, sp := range report.spans {
			if sp.converted {
				totalConverted++
			} else {
				totalSkipped++
			}
		}
	}

	// ─── Print detailed report ───
	t.Log("")
	t.Log("══════════════════════════════════════════════")
	t.Log("  LaTeX → Unicode 审计报告")
	t.Log("══════════════════════════════════════════════")
	t.Logf("  论文总数: %d", len(reports))
	t.Logf("  公式总数: %d", totalSpans)
	t.Logf("  已转换:   %d (%.1f%%)", totalConverted, pct(totalConverted, totalSpans))
	t.Logf("  保留原始: %d (%.1f%%)", totalSkipped, pct(totalSkipped, totalSpans))
	t.Log("")

	for _, r := range reports {
		if !r.allOK {
			t.Logf("── ✗ %s (%s)", truncStr(r.title, 60), r.ref)
		} else {
			t.Logf("── ✓ %s (%s)", truncStr(r.title, 60), r.ref)
		}
		if len(r.spans) > 0 {
			t.Logf("    公式数: %d | 转换: %d | 保留: %d",
				len(r.spans), countConverted(r.spans), countSkipped(r.spans))
		}
		// Only print details for unconverted formulas to reduce noise
		for _, sp := range r.spans {
			if !sp.converted {
				t.Logf("    [✗] %s: %s", sp.source, truncStr(sp.latex, 80))
			}
		}
	}

	// ─── Summary (aggregate only) ───

	// ─── Check for potential issues ───
	t.Logf("")
	t.Logf("──────────────────────────────────────────")
	t.Logf("  流水线检查:")
	t.Logf("")

	// Issue 1: Unclosed $ spans (inner content still has $)
	unclosedCount := 0
	for _, r := range reports {
		for _, sp := range r.spans {
			inner := sp.latex
			if strings.HasPrefix(inner, "$$") {
				inner = strings.TrimPrefix(inner, "$$")
				inner = strings.TrimSuffix(inner, "$$")
			} else if strings.HasPrefix(inner, "$") {
				inner = strings.TrimPrefix(inner, "$")
				inner = strings.TrimSuffix(inner, "$")
			}
			if strings.Contains(inner, "$") {
				unclosedCount++
			}
		}
	}
	if unclosedCount > 0 {
		t.Logf("  ⚠  发现 %d 个未闭合的 $ 符号（可能被截断的内容）", unclosedCount)
	} else {
		t.Logf("  ✓  所有论文的 $ 符号均已闭合")
	}

	// Issue 2: \text{} in subscript that causes conversion failure
	textSubFailCount := 0
	for _, r := range reports {
		for _, sp := range r.spans {
			if !sp.converted && strings.Contains(sp.latex, "\\text") {
				textSubFailCount++
			}
		}
	}
	if textSubFailCount > 0 {
		t.Logf("  ⚠  发现 %d 个包含 \\text{} 的公式因上下标字符缺失而未能转换", textSubFailCount)
	} else {
		t.Logf("  ✓  包含 \\text{} 的公式处理无异常")
	}

	// Issue 3: Cross-line inline formulas
	crossLineCount := 0
	for _, r := range reports {
		for _, sp := range r.spans {
			if strings.Contains(sp.latex, "\n") {
				crossLineCount++
			}
		}
	}
	if crossLineCount > 0 {
		t.Logf("  ⚠  发现 %d 个跨行公式", crossLineCount)
	} else {
		t.Logf("  ✓  无跨行内联公式")
	}

	// Issue 4: Papers with 0 formulas
	noFormulaPapers := 0
	for _, r := range reports {
		if len(r.spans) == 0 {
			noFormulaPapers++
		}
	}
	t.Logf("  %d/%d 篇论文不含 LaTeX 公式", noFormulaPapers, len(reports))

	t.Logf("")
	t.Logf("══════════════════════════════════════════════")
}

// ─── Helpers ───

func extractAndTestSpans(text, source string, report *auditPaperReport) {
	// Same scanning logic as processMathSpans
	for i := 0; i < len(text); {
		if text[i] != '$' {
			i++
			continue
		}
		isDisplay := i+1 < len(text) && text[i+1] == '$'
		var spanStart, spanEnd int
		if isDisplay {
			if i+2 >= len(text) {
				break
			}
			closeIdx := findClosingDollars(text, i+2, true)
			if closeIdx < 0 {
				break
			}
			spanStart = i
			spanEnd = closeIdx + 2
			i = closeIdx + 2
		} else {
			closeIdx := findClosingDollars(text, i+1, false)
			if closeIdx < 0 {
				break
			}
			content := text[i+1 : closeIdx]
			if strings.ContainsRune(content, '$') {
				break
			}
			spanStart = i
			spanEnd = closeIdx + 1
			i = closeIdx + 1
		}

		rawLatex := text[spanStart:spanEnd]
		// Run through latexToUnicode and check if it changed
		converted := latexToUnicode(rawLatex)
		changed := converted != rawLatex

		report.spans = append(report.spans, auditFormulaSpan{
			source:    source,
			latex:     rawLatex,
			converted: changed,
			result:    converted,
		})
	}
}

func countConverted(spans []auditFormulaSpan) int {
	n := 0
	for _, sp := range spans {
		if sp.converted {
			n++
		}
	}
	return n
}

func countSkipped(spans []auditFormulaSpan) int {
	n := 0
	for _, sp := range spans {
		if !sp.converted {
			n++
		}
	}
	return n
}

func truncStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}
