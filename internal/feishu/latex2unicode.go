package feishu

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ─── LaTeX → Unicode conversion ───
//
// latexToUnicode converts LaTeX math expressions ($...$ and $$...$$) to
// Unicode equivalents when every token in the formula is convertible.
// If any part cannot be converted (e.g., a subscript letter without Unicode
// coverage), the original LaTeX is preserved as-is.
//
// This is applied only at display time (streaming cards, text messages to Feishu)
// and NEVER persisted to the paper JSON.

// unicodeMap maps LaTeX commands to their Unicode equivalents.
var unicodeMap = map[string]string{
	// lowercase Greek
	"alpha":      "α",
	"beta":       "β",
	"gamma":      "γ",
	"delta":      "δ",
	"epsilon":    "ε",
	"varepsilon": "ε",
	"zeta":       "ζ",
	"eta":        "η",
	"theta":      "θ",
	"vartheta":   "θ",
	"iota":       "ι",
	"kappa":      "κ",
	"lambda":     "λ",
	"mu":         "μ",
	"nu":         "ν",
	"xi":         "ξ",
	"omicron":    "ο",
	"pi":         "π",
	"varpi":      "ϖ",
	"rho":        "ρ",
	"varrho":     "ϱ",
	"sigma":      "σ",
	"varsigma":   "ς",
	"tau":        "τ",
	"upsilon":    "υ",
	"phi":        "φ",
	"varphi":     "ϕ",
	"chi":        "χ",
	"psi":        "ψ",
	"omega":      "ω",

	// uppercase Greek
	"Gamma":  "Γ",
	"Delta":  "Δ",
	"Theta":  "Θ",
	"Lambda": "Λ",
	"Xi":     "Ξ",
	"Pi":     "Π",
	"Sigma":  "Σ",
	"Phi":    "Φ",
	"Psi":    "Ψ",
	"Omega":  "Ω",

	// math symbols
	"times":          "×",
	"div":            "÷",
	"pm":             "±",
	"mp":             "∓",
	"cdot":           "·",
	"ldots":          "…",
	"cdots":          "⋯",
	"vdots":          "⋮",
	"ddots":          "⋱",
	"infty":          "∞",
	"partial":        "∂",
	"nabla":          "∇",
	"forall":         "∀",
	"exists":         "∃",
	"emptyset":       "∅",
	"varnothing":     "∅",
	"in":             "∈",
	"notin":          "∉",
	"subset":         "⊂",
	"supset":         "⊃",
	"subseteq":       "⊆",
	"supseteq":       "⊇",
	"cup":            "∪",
	"cap":            "∩",
	"angle":          "∠",
	"perp":           "⊥",
	"approx":         "≈",
	"equiv":          "≡",
	"neq":            "≠",
	"ne":             "≠",
	"leq":            "≤",
	"le":             "≤",
	"geq":            "≥",
	"ge":             "≥",
	"to":             "→",
	"mapsto":         "↦",
	"gets":           "←",
	"leftarrow":      "←",
	"rightarrow":     "→",
	"leftrightarrow": "↔",
	"Rightarrow":     "⇒",
	"Leftarrow":      "⇐",
	"Leftrightarrow": "⇔",
	"propto":         "∝",
	"sim":            "∼",
	"simeq":          "≃",
	"cong":           "≅",
	"parallel":       "∥",
	"mid":            "∣",
	"triangle":       "△",
	"triangleq":      "≜",
	"ell":            "ℓ",
	"aleph":          "ℵ",
	"hbar":           "ℏ",
	"imath":          "ı",
	"jmath":          "ȷ",
	"Re":             "ℜ",
	"Im":             "ℑ",

	// function names (often in \text{} but sometimes bare)
	"sin":    "sin",
	"cos":    "cos",
	"tan":    "tan",
	"log":    "log",
	"ln":     "ln",
	"exp":    "exp",
	"max":    "max",
	"min":    "min",
	"arg":    "arg",
	"argmin": "argmin",
	"argmax": "argmax",
	"sup":    "sup",
	"inf":    "inf",
	"lim":    "lim",
	"det":    "det",
	"tr":     "tr",
	"rank":   "rank",
}

// subscriptMap maps characters to their Unicode subscript equivalents.
// Coverage is limited — many lowercase letters and all uppercase are missing.
var subscriptMap = map[rune]rune{
	'0': '₀',
	'1': '₁',
	'2': '₂',
	'3': '₃',
	'4': '₄',
	'5': '₅',
	'6': '₆',
	'7': '₇',
	'8': '₈',
	'9': '₉',
	'+': '₊',
	'-': '₋',
	'(': '₍',
	')': '₎',
	'a': 'ₐ',
	'e': 'ₑ',
	'h': 'ₕ',
	'i': 'ᵢ',
	'j': 'ⱼ',
	'k': 'ₖ',
	'l': 'ₗ',
	'm': 'ₘ',
	'n': 'ₙ',
	'o': 'ₒ',
	'p': 'ₚ',
	'r': 'ᵣ',
	's': 'ₛ',
	't': 'ₜ',
	'u': 'ᵤ',
	'v': 'ᵥ',
	'x': 'ₓ',
	'β': 'ᵦ', // beta
	'γ': 'ᵧ', // gamma
	'ρ': 'ᵨ', // rho
	'φ': 'ᵩ', // phi
	'χ': 'ᵪ', // chi
}

// superscriptMap maps characters to their Unicode superscript equivalents.
// Covers digits, basic operators, and lowercase a-z; no uppercase.
var superscriptMap = map[rune]rune{
	'0': '⁰',
	'1': '¹',
	'2': '²',
	'3': '³',
	'4': '⁴',
	'5': '⁵',
	'6': '⁶',
	'7': '⁷',
	'8': '⁸',
	'9': '⁹',
	'+': '⁺',
	'-': '⁻',
	'=': '⁼',
	'(': '⁽',
	')': '⁾',
	// Exact duplicates in input (lowercase letters map to superscript)
	// Go map keys are unique runes, but the source maps from e.g. 'a' to 'ᵃ'
	// Different source runes CANNOT map to the same key in this map since
	// this is a forward lookup. Each source char is unique.
	'a': 'ᵃ',
	'b': 'ᵇ',
	'c': 'ᶜ',
	'd': 'ᵈ',
	'e': 'ᵉ',
	'f': 'ᶠ',
	'g': 'ᵍ',
	'h': 'ʰ',
	'i': 'ⁱ',
	'j': 'ʲ',
	'k': 'ᵏ',
	'l': 'ˡ',
	'm': 'ᵐ',
	'n': 'ⁿ',
	'o': 'ᵒ',
	'p': 'ᵖ',
	'r': 'ʳ',
	's': 'ˢ',
	't': 'ᵗ',
	'u': 'ᵘ',
	'v': 'ᵛ',
	'w': 'ʷ',
	'x': 'ˣ',
	'y': 'ʸ',
	'z': 'ᶻ',
}

// latexToUnicode is the main entry point. It finds all $...$ and $$...$$
// spans in the text, attempts full Unicode conversion on each, and replaces
// the span only if every token is convertible.
func latexToUnicode(text string) string {
	if !strings.Contains(text, "$") {
		return text
	}
	return processMathSpans(text)
}

// ─── Math span finding ───

type mathSpan struct {
	start, end int  // byte offsets in the original text (including $ delimiters)
	isDisplay  bool // true for $$, false for $
}

// processMathSpans scans text for $...$ and $$...$$ spans, attempts
// conversion on each, and builds the result.
func processMathSpans(text string) string {
	var spans []mathSpan

	for i := 0; i < len(text); {
		if text[i] != '$' {
			i++
			continue
		}
		isDisplay := i+1 < len(text) && text[i+1] == '$'
		if isDisplay {
			if i+2 >= len(text) {
				break // incomplete $$
			}
			innerStart := i + 2
			closeIdx := findClosingDollars(text, innerStart, true)
			if closeIdx < 0 {
				break // unclosed $$
			}
			spans = append(spans, mathSpan{start: i, end: closeIdx + 2, isDisplay: true})
			i = closeIdx + 2
		} else {
			innerStart := i + 1
			closeIdx := findClosingDollars(text, innerStart, false)
			if closeIdx < 0 {
				break // unclosed $
			}
			// Guard: content between $...$ contains another $ →
			// this $ is likely the START of a different formula.
			// Stop scanning; on next cycle with more text the
			// formulas may be complete and properly separated.
			content := text[innerStart:closeIdx]
			if strings.ContainsRune(content, '$') {
				break // crossed into another formula
			}
			spans = append(spans, mathSpan{start: i, end: closeIdx + 1, isDisplay: false})
			i = closeIdx + 1
		}
	}

	// Build result — replace spans that are fully convertible
	var sb strings.Builder
	prevEnd := 0
	for _, sp := range spans {
		// Copy text before this span
		sb.WriteString(text[prevEnd:sp.start])

		// Extract inner LaTeX (without $ delimiters)
		innerStart := sp.start
		innerEnd := sp.end
		if sp.isDisplay {
			innerStart += 2
			innerEnd -= 2
		} else {
			innerStart += 1
			innerEnd -= 1
		}
		inner := text[innerStart:innerEnd]
		inner = strings.TrimSpace(inner)

		if converted, ok := tryConvertFormula(inner); ok {
			sb.WriteString(converted)
		} else {
			// Keep original LaTeX
			sb.WriteString(text[sp.start:sp.end])
		}
		prevEnd = sp.end
	}
	sb.WriteString(text[prevEnd:])

	return sb.String()
}

// findClosingDollars finds the matching closing $$ or $ starting from pos.
// Returns the index of the first closing $, or -1 if not found.
// For inline $...$, scanning stops early on:
//   - CJK characters (Chinese/Japanese/Korean) — they belong outside formulas
//   - newlines — inline formulas don't span lines in practice
//
// For display $$...$$, no such restriction (newlines and CJK inside \text{} are valid).
func findClosingDollars(text string, pos int, isDisplay bool) int {
	if isDisplay {
		for i := pos; i+1 < len(text); i++ {
			if text[i] == '$' && text[i+1] == '$' {
				return i
			}
		}
		return -1
	}
	for i := pos; i < len(text); i++ {
		ch := text[i]

		// Newline → inline formula should not span lines
		if ch == '\n' {
			return -1
		}

		// Non-ASCII → check if CJK (CJK means we left the formula)
		if ch >= 0x80 {
			r, size := utf8.DecodeRuneInString(text[i:])
			if unicode.Is(unicode.Han, r) ||
				unicode.Is(unicode.Hiragana, r) ||
				unicode.Is(unicode.Katakana, r) ||
				unicode.Is(unicode.Hangul, r) {
				return -1
			}
			// Non-CJK non-ASCII (e.g. math operator in BMP): skip it
			i += size - 1 // loop will i++
			continue
		}

		if ch == '$' {
			// Make sure it's not part of $$
			if i+1 < len(text) && text[i+1] == '$' {
				i++ // skip the pair
				continue
			}
			return i
		}
	}
	return -1
}

// ─── Formula conversion ───

// tryConvertFormula attempts to fully convert a LaTeX formula (without $ delimiters)
// to Unicode. Returns the converted string and whether full conversion succeeded.
func tryConvertFormula(latex string) (string, bool) {
	if latex == "" {
		return "", true
	}

	// Step 1: Strip \text{...} and \texttt{...} wrappers
	cleaned := stripTextCommands(latex)

	// Step 2: Try to convert the remaining LaTeX tokens
	return convertLatexTokens(cleaned)
}

// stripTextCommands removes \text{...} and \texttt{...} wrappers,
// keeping only the inner content. Handles non-nested cases.
func stripTextCommands(s string) string {
	result := s
	// Loop to handle adjacent \text{...}\text{...} patterns
	for {
		prev := result
		result = stripOnePass(result)
		if result == prev {
			break
		}
	}
	return result
}

// stripOnePass does one pass of \text{...} removal.
func stripOnePass(s string) string {
	var sb strings.Builder
	i := 0
	for i < len(s) {
		// Look for \text or \texttt
		if i+4 < len(s) && s[i] == '\\' {
			cmdLen := 0
			// Check longer commands first to avoid prefix matching (\texttt vs \text)
			if i+7 <= len(s) && s[i:i+7] == "\\texttt" {
				cmdLen = 7
			} else if i+5 <= len(s) && s[i:i+5] == "\\text" {
				cmdLen = 5
			}
			if cmdLen > 0 {
				// Skip the command
				bracePos := i + cmdLen
				// Skip any whitespace between command and {
				for bracePos < len(s) && (s[bracePos] == ' ' || s[bracePos] == '\t') {
					bracePos++
				}
				if bracePos < len(s) && s[bracePos] == '{' {
					// Find matching closing brace
					inner, endPos := extractMatchingBrace(s, bracePos)
					if endPos > bracePos {
						sb.WriteString(inner)
						i = endPos
						continue
					}
				}
			}
		}
		sb.WriteByte(s[i])
		i++
	}
	return sb.String()
}

// extractMatchingBrace extracts content from s[pos] = '{' to matching '}'.
// Returns (innerContent, endPos) where endPos points past the closing '}'.
func extractMatchingBrace(s string, pos int) (string, int) {
	if pos >= len(s) || s[pos] != '{' {
		return "", pos
	}
	depth := 1
	i := pos + 1
	for i < len(s) && depth > 0 {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
		i++
	}
	if depth != 0 {
		return "", pos + 1 // unmatched
	}
	return s[pos+1 : i-1], i
}

// convertLatexTokens attempts to convert a LaTeX math expression (without
// \text wrappers) to Unicode. This is the core recursive conversion.
func convertLatexTokens(s string) (string, bool) {
	if s == "" {
		return "", true
	}

	var result strings.Builder
	for i := 0; i < len(s); {
		ch := s[i]

		switch ch {
		case '^':
			// Superscript
			i++
			sub, n, ok := parseSubSuper(s, i)
			if !ok {
				return "", false
			}
			conv, ok := toSuperscript(sub)
			if !ok {
				return "", false
			}
			result.WriteString(conv)
			i = n

		case '_':
			// Subscript
			i++
			sub, n, ok := parseSubSuper(s, i)
			if !ok {
				return "", false
			}
			conv, ok := toSubscript(sub)
			if !ok {
				return "", false
			}
			result.WriteString(conv)
			i = n

		case '{':
			// Standalone group (not consumed by a command handler above):
			// preserve braces and convert content recursively.
			inner, endPos := extractMatchingBrace(s, i)
			if endPos <= i {
				result.WriteByte('{')
				i++
				continue
			}
			conv, ok := convertLatexTokens(inner)
			if !ok {
				return "", false
			}
			result.WriteByte('{')
			result.WriteString(conv)
			result.WriteByte('}')
			i = endPos

		case '}':
			// Stray closing brace — treat as literal
			result.WriteByte('}')
			i++

		case '\\':
			// Command
			i++
			cmd, n := parseCommand(s, i)
			if cmd == "" {
				// Non-letter command: spaces and escaped braces
				if i < len(s) {
					switch s[i] {
					case ',', ':', ';':
						result.WriteByte(' ') // thin/medium/thick space → space
						i++
						continue
					case '!':
						i++ // negative thin space → omit
						continue
					case ' ':
						result.WriteByte(' ') // escaped space
						i++
						continue
					case '{':
						result.WriteByte('{') // \{ → literal {
						i++
						continue
					case '}':
						result.WriteByte('}') // \} → literal }
						i++
						continue
					}
				}
				return "", false // lone backslash or non-alpha command
			}

			if replacement, ok := unicodeMap[cmd]; ok {
				result.WriteString(replacement)
				i = n
				// Don't consume following {group} or whitespace — they
				// will be handled by the '{' or default case in the
				// next iteration.
			} else if cmd == "mathbf" {
				// \mathbf{...} → **...** (markdown bold)
				arg, endPos, ok := consumeGroup(s, n)
				if !ok {
					return "", false
				}
				convArg, ok := convertLatexTokens(arg)
				if !ok {
					return "", false
				}
				result.WriteString("**")
				result.WriteString(convArg)
				result.WriteString("**")
				i = endPos

			} else if cmd == "mathbb" {
				// \mathbb{...} → plain text (strip command, keep content)
				arg, endPos, ok := consumeGroup(s, n)
				if !ok {
					return "", false
				}
				convArg, ok := convertLatexTokens(arg)
				if !ok {
					return "", false
				}
				result.WriteString(convArg)
				i = endPos

			} else if cmd == "tilde" {
				// \tilde{x} → x̃ (combining tilde U+0303 on last char)
				// Handle nested formatting: **x** → **x̃** (tilde before closing **)
				arg, endPos, ok := consumeGroup(s, n)
				if !ok {
					return "", false
				}
				convArg, ok := convertLatexTokens(arg)
				if !ok {
					return "", false
				}
				// If convArg ends with markdown markers, insert tilde before them
				suffix := ""
				if strings.HasSuffix(convArg, "**") {
					suffix = "**"
					convArg = convArg[:len(convArg)-2]
				} else if strings.HasSuffix(convArg, "*") {
					suffix = "*"
					convArg = convArg[:len(convArg)-1]
				}
				result.WriteString(convArg)
				result.WriteRune('\u0303') // combining tilde on last actual char
				result.WriteString(suffix)
				i = endPos

			} else if cmd == "mathcal" || cmd == "mathit" {
				// \mathcal{...} / \mathit{...} → *...* (markdown italic)
				arg, endPos, ok := consumeGroup(s, n)
				if !ok {
					return "", false
				}
				convArg, ok := convertLatexTokens(arg)
				if !ok {
					return "", false
				}
				result.WriteString("*")
				result.WriteString(convArg)
				result.WriteString("*")
				i = endPos

			} else if cmd == "sqrt" {
				// \sqrt{x} → √x (single char) or √(multi char)
				arg, endPos, ok := consumeGroup(s, n)
				if !ok {
					return "", false
				}
				convArg, ok := convertLatexTokens(arg)
				if !ok {
					return "", false
				}
				result.WriteRune('√')
				// If the argument is a single rune, no parentheses needed
				if len([]rune(convArg)) <= 1 {
					result.WriteString(convArg)
				} else {
					result.WriteByte('(')
					result.WriteString(convArg)
					result.WriteByte(')')
				}
				i = endPos

			} else if cmd == "mathrm" || cmd == "text" || cmd == "texttt" || cmd == "textbf" {
				// These should have been stripped already, but handle
				// residual cases: strip the command, keep content.
				arg, endPos, ok := consumeGroup(s, n)
				if !ok {
					return "", false
				}
				result.WriteString(arg)
				i = endPos

			} else {
				// Unknown command — cannot convert
				return "", false
			}

		case ' ', '\t':
			// Whitespace inside formula: preserve
			result.WriteByte(ch)
			i++

		default:
			// Regular character — pass through
			result.WriteByte(ch)
			i++
		}
	}

	return result.String(), true
}

// parseCommand reads a LaTeX command name starting at position pos,
// where pos follows the backslash. Returns (commandName, endPos).
func parseCommand(s string, pos int) (string, int) {
	if pos >= len(s) {
		return "", pos
	}
	start := pos
	for pos < len(s) {
		r := rune(s[pos])
		if unicode.IsLetter(r) {
			pos++
		} else {
			break
		}
	}
	if pos == start {
		return "", pos
	}
	return s[start:pos], pos
}

// parseSubSuper parses the content of a subscript or superscript.
// After ^ or _, the content is either a single character or a {group}.
// Returns (content, endPos, ok).
func parseSubSuper(s string, pos int) (string, int, bool) {
	if pos >= len(s) {
		return "", pos, false
	}
	// Skip whitespace
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
		pos++
	}
	if pos >= len(s) {
		return "", pos, false
	}
	if s[pos] == '{' {
		inner, endPos := extractMatchingBrace(s, pos)
		return inner, endPos, true
	}
	// Single character (backslash command or plain char)
	if s[pos] == '\\' {
		cmdStart := pos + 1
		cmd, endPos := parseCommand(s, cmdStart)
		if cmd == "" {
			return "", pos, false
		}
		return "\\" + cmd, endPos, true
	}
	return string(s[pos]), pos + 1, true
}

// consumeGroup parses a group starting at pos and returns (content, endPos, ok).
// Skips whitespace before the group.
func consumeGroup(s string, pos int) (string, int, bool) {
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
		pos++
	}
	if pos >= len(s) || s[pos] != '{' {
		return "", pos, false
	}
	inner, endPos := extractMatchingBrace(s, pos)
	if endPos <= pos {
		return "", pos, false
	}
	return inner, endPos, true
}

// toSubscript tries to convert text to Unicode subscript.
// First runs through convertLatexTokens to resolve any commands (\mathbb{R}→R, \beta→β, etc.),
// then checks each resulting character in subscriptMap.
func toSubscript(s string) (string, bool) {
	if s == "" {
		return "", false
	}

	// Process through full token conversion first (handles \mathbb, \beta, etc.)
	converted, ok := convertLatexTokens(s)
	if !ok {
		return "", false
	}

	// Try to convert each resulting character to subscript
	var out strings.Builder
	for _, r := range converted {
		if sub, ok := subscriptMap[r]; ok {
			out.WriteRune(sub)
		} else {
			return "", false
		}
	}
	return out.String(), true
}

// toSuperscript tries to convert text to Unicode superscript.
// First runs through convertLatexTokens, then checks each character in superscriptMap.
func toSuperscript(s string) (string, bool) {
	if s == "" {
		return "", false
	}

	// Process through full token conversion first
	converted, ok := convertLatexTokens(s)
	if !ok {
		return "", false
	}

	// Try to convert each resulting character to superscript
	var out strings.Builder
	for _, r := range converted {
		if sup, ok := superscriptMap[r]; ok {
			out.WriteRune(sup)
		} else {
			return "", false
		}
	}
	return out.String(), true
}
