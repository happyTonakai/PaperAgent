package feishu

import (
	"testing"
)

func TestLatexToUnicode_Simple(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no math",
			in:   "Hello world",
			want: "Hello world",
		},
		{
			name: "inline greek letter",
			in:   "参数 $\\lambda$ 是权重",
			want: "参数 λ 是权重",
		},
		{
			name: "display greek letter",
			in:   "公式：$$\\lambda$$",
			want: "公式：λ",
		},
		{
			name: "simple subscript digit",
			in:   "$x_1$",
			want: "x₁",
		},
		{
			name: "subscript letter t",
			in:   "$\\lambda_t$",
			want: "λₜ",
		},
		{
			name: "subscript group with convertible chars",
			in:   "$x_{t+1}$",
			want: "xₜ₊₁",
		},
		{
			name: "superscript digit",
			in:   "$x^2$",
			want: "x²",
		},
		{
			name: "superscript group",
			in:   "$x^{ab}$",
			want: "xᵃᵇ",
		},
		{
			name: "mathbf becomes markdown bold",
			in:   "$\\mathbf{x}$",
			want: "**x**",
		},
		{
			name: "mathcal becomes markdown italic",
			in:   "$\\mathcal{L}$",
			want: "*L*",
		},
		{
			name: "text stripped inside formula fails for distill",
			in:   "$\\lambda_{\\text{distill}}$",
			want: "$\\lambda_{\\text{distill}}$",
		},
		{
			name: "sqrt single char",
			in:   "$\\sqrt{k}$",
			want: "√k",
		},
		{
			name: "sqrt multi char with subscript",
			in:   "$\\sqrt{k_s}$",
			want: "√(kₛ)",
		},
		{
			name: "mathbb becomes plain",
			in:   "$\\mathbb{R}$",
			want: "R",
		},
		{
			name: "tilde with combining mark",
			in:   "$\\tilde{x}$",
			want: "x̃",
		},
		{
			name: "tilde with mathbf inside (bold x with tilde)",
			in:   "$\\tilde{\\mathbf{x}}$",
			want: "**x̃**",
		}, {
			name: "thin space comma ignored",
			in:   "$x\\,y$",
			want: "x y",
		},
		{
			name: "escaped braces literal",
			in:   "$\\{1, 2\\}$",
			want: "{1, 2}",
		},
		{
			name: "in and ldots",
			in:   "$s \\in \\{1, \\ldots, S\\}$",
			want: "s ∈ {1, …, S}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := latexToUnicode(tt.in)
			if got != tt.want {
				t.Errorf("latexToUnicode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLatexToUnicode_PreserveOnFailure(t *testing.T) {
	// Formulas with characters that have no Unicode subscript/superscript
	// should be preserved as-is.
	tests := []struct {
		name string
		in   string
		want string // should keep original LaTeX
	}{
		{
			name: "subscript letter b has no Unicode",
			in:   "其中 $x_b$ 是参数",
			want: "其中 $x_b$ 是参数",
		},
		{
			name: "subscript letter c has no Unicode",
			in:   "$x_c$",
			want: "$x_c$",
		},
		{
			name: "subscript letter d has no Unicode",
			in:   "$x_d$",
			want: "$x_d$",
		},
		{
			name: "subscript letter f has no Unicode",
			in:   "$x_f$",
			want: "$x_f$",
		},
		{
			name: "subscript letter g has no Unicode",
			in:   "$x_g$",
			want: "$x_g$",
		},
		{
			name: "subscript letter q has no Unicode",
			in:   "$x_q$",
			want: "$x_q$",
		},
		{
			name: "subscript letter y has no Unicode",
			in:   "$x_y$",
			want: "$x_y$",
		},
		{
			name: "superscript uppercase has no Unicode",
			in:   "$x^{B}$",
			want: "$x^{B}$",
		},
		{
			name: "unknown command cannot convert",
			in:   "$\\int x dx$",
			want: "$\\int x dx$",
		},
		{
			name: "complex formula with integral",
			in:   "$\\int_{0}^{\\infty} e^{-x} dx$",
			want: "$\\int_{0}^{\\infty} e^{-x} dx$",
		},
		{
			name: "double subscript letter fails",
			in:   "$x_{ab}$",
			want: "$x_{ab}$",
		},
		{
			name: "mathbf z text long s — g has no subscript",
			in:   "$\\mathbf{z}_{\\text{long}}^s$",
			want: "$\\mathbf{z}_{\\text{long}}^s$",
		},
		{
			name: "sqrt with nth root not supported",
			in:   "$\\sqrt[3]{x}$",
			want: "$\\sqrt[3]{x}$",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := latexToUnicode(tt.in)
			if got != tt.want {
				t.Errorf("latexToUnicode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLatexToUnicode_TextStripping(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "text wrapper stripped in subscript fails for distill",
			in:   "$\\lambda_{\\text{distill}}$",
			want: "$\\lambda_{\\text{distill}}$", // d has no subscript
		},
		{
			name: "text wrapper min all convertible",
			in:   "$\\lambda_{\\text{min}}$",
			want: "λₘᵢₙ", // m,i,n all have subscripts
		},
		{
			name: "mathcal with text subscript",
			in:   "$\\mathcal{L}_{\\text{VAE}}$",
			want: "$\\mathcal{L}_{\\text{VAE}}$",
		},
		{
			name: "lambda text t - t has subscript",
			in:   "$\\lambda_{\\text{t}}$",
			want: "λₜ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := latexToUnicode(tt.in)
			if got != tt.want {
				t.Errorf("latexToUnicode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLatexToUnicode_ComplexFormula(t *testing.T) {
	// User's example: \lambda{\text{t}} is NOT \lambda_{\text{t}} — no underscore!
	// After stripping \text{}, the {t} are braced groups, not subscripts.
	// All parts are convertible, so the formula should be replaced.
	in := "$$\\mathcal{L}{\\text{VAE}} = \\lambda{\\text{t}}\\mathcal{L}{\\text{t}} + \\lambda{\\text{f}}\\mathcal{L}{\\text{f}} + \\lambda{\\text{adv}}\\mathcal{L}{\\text{adv}} + \\lambda{\\text{feat}}\\mathcal{L}{\\text{feat}} + \\lambda{\\text{KL}}\\mathcal{L}{\\text{KL}} + \\lambda{\\text{distill}}\\mathcal{L}{\\text{distill}}$$"
	got := latexToUnicode(in)
	if got == in {
		t.Errorf("complex formula should be convertible (no subscripts with missing chars), but was preserved")
	}
	t.Logf("converted: %s", got)
}

func TestLatexToUnicode_InlineFormulaPreserved(t *testing.T) {
	// The inline formula from the user's example
	in := "其中 $\\mathcal{L}{\\text{distill}}$ 即WavLM蒸馏损失，权重 $\\lambda_{\\text{distill}}=25$（附录H表13）"
	// \mathcal{L}{distill} → *L*distill (after text stripping)
	// But \lambda_{distill} → λ followed by subscript "distill" which has 'd' without subscript
	// So \lambda_{\text{distill}} should be preserved

	// Actually, let me think about what the expected output should be:
	// First formula: $\mathcal{L}{\text{distill}}$
	//   After \text{} stripping: $\mathcal{L}{distill}$
	//   \mathcal{L} → *L*, then {distill} → distill
	//   But wait, the original is $\mathcal{L}{\text{distill}}$ with NO underscore!
	//   So it becomes *L*distill — this should work!
	//
	// Second formula: $\lambda_{\text{distill}}=25$
	//   After \text{} stripping: $\lambda_{distill}=25$
	//   \lambda → λ, _{distill} → d doesn't have subscript → FAIL
	//   So preserved as-is.
	//
	// Actually wait, \text{distill} — after stripping, we get "distill"
	// d, i, s, t, i, l, l — d has no subscript, so _{distill} fails
	// But _{t} works for single char!

	// With braces now preserved for standalone groups:
	// \mathcal{L}{\text{distill}} → *L*{distill}
	want := "其中 *L*{distill} 即WavLM蒸馏损失，权重 $\\lambda_{\\text{distill}}=25$（附录H表13）"
	got := latexToUnicode(in)
	if got != want {
		t.Errorf("inline formula\n  got:  %q\n  want: %q", got, want)
	}
}

func TestLatexToUnicode_MultiCharSubscript(t *testing.T) {
	// Multi-char subscript where all chars have Unicode forms
	in := "$x_{t+1}$"
	want := "xₜ₊₁"
	got := latexToUnicode(in)
	if got != want {
		t.Errorf("latexToUnicode(%q) = %q, want %q", in, got, want)
	}
}

func TestLatexToUnicode_Empty(t *testing.T) {
	if got := latexToUnicode(""); got != "" {
		t.Errorf("empty input should return empty, got %q", got)
	}
}

func TestLatexToUnicode_NoDollars(t *testing.T) {
	in := "纯文本，没有公式"
	if got := latexToUnicode(in); got != in {
		t.Errorf("no-dollars text should be unchanged: got %q, want %q", got, in)
	}
}

func TestLatexToUnicode_Superscript(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "simple superscript",
			in:   "$x^2 + y^2$",
			want: "x² + y²",
		},
		{
			name: "superscript group at",
			in:   "$e^{at}$",
			want: "eᵃᵗ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := latexToUnicode(tt.in)
			if got != tt.want {
				t.Errorf("latexToUnicode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStripTextCommands(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "simple text",
			in:   "\\text{hello}",
			want: "hello",
		},
		{
			name: "texttt",
			in:   "\\texttt{world}",
			want: "world",
		},
		{
			name: "adjacent text commands",
			in:   "\\text{a}\\text{b}",
			want: "ab",
		},
		{
			name: "nested braces not handled",
			in:   "\\text{hello {there}}",
			want: "hello {there}", // the first } closes \text, leaving " {there}}"
		},
		{
			name: "text with no braces",
			in:   "\\text ",
			want: "\\text ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripTextCommands(tt.in)
			if got != tt.want {
				t.Errorf("stripTextCommands(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestProcessMathSpans_NoCompleteSpan(t *testing.T) {
	// Unclosed dollar sign — should be preserved
	in := "公式 $\\lambda 还没写完"
	if got := latexToUnicode(in); got != in {
		t.Errorf("unclosed math should be preserved: got %q, want %q", got, in)
	}
}

func TestProcessMathSpans_Mixed(t *testing.T) {
	// Mix of convertible and non-convertible formulas
	in := "已知 $\\alpha = \\lambda_t$，且 $x_b$ 是参数" // \alpha convertible, \lambda_t convertible, x_b not

	// After conversion:
	// $α = λₜ$ — convertible!
	// $x_b$ — not convertible (b has no subscript)
	want := "已知 α = λₜ，且 $x_b$ 是参数"
	got := latexToUnicode(in)
	if got != want {
		t.Errorf("mixed conversion\n  got:  %q\n  want: %q", got, want)
	}
}

func TestProcessMathSpans_DisplayAndInline(t *testing.T) {
	in := "公式 $$\\alpha$$ 和 $\\beta$"
	want := "公式 α 和 β"
	got := latexToUnicode(in)
	if got != want {
		t.Errorf("display+inline: got %q, want %q", got, want)
	}
}
