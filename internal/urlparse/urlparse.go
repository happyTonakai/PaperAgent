package urlparse

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	arxivNewIDPattern = regexp.MustCompile(`^\d{4}\.\d{4,5}(v\d+)?$`)
	arxivOldIDPattern = regexp.MustCompile(`^[A-Za-z-]+(\.[A-Za-z]{2})?/\d{7}(v\d+)?$`)
)

// NormalizeArxivInput recognizes an arXiv ID or arXiv URL and returns a
// canonical arXiv abs URL plus the extracted ID.
func NormalizeArxivInput(input string) (canonicalURL, id string, ok bool) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", "", false
	}

	if len(s) >= len("arxiv:") && strings.EqualFold(s[:len("arxiv:")], "arxiv:") {
		s = strings.TrimSpace(s[len("arxiv:"):])
	}

	if IsURL(s) {
		id, ok := extractArxivIDFromURL(s)
		if !ok {
			return "", "", false
		}
		return "https://arxiv.org/abs/" + id, id, true
	}

	s = strings.TrimSuffix(s, ".pdf")
	if isArxivID(s) {
		return "https://arxiv.org/abs/" + s, s, true
	}

	return "", "", false
}

// IsArxivInput reports whether input is an arXiv ID or arXiv URL.
func IsArxivInput(input string) bool {
	_, _, ok := NormalizeArxivInput(input)
	return ok
}

func isArxivID(s string) bool {
	return arxivNewIDPattern.MatchString(s) || arxivOldIDPattern.MatchString(s)
}

func extractArxivIDFromURL(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "http://arxiv.org/") &&
		!strings.HasPrefix(lower, "https://arxiv.org/") &&
		!strings.HasPrefix(lower, "http://www.arxiv.org/") &&
		!strings.HasPrefix(lower, "https://www.arxiv.org/") &&
		!strings.HasPrefix(lower, "http://export.arxiv.org/") &&
		!strings.HasPrefix(lower, "https://export.arxiv.org/") {
		return "", false
	}

	// Avoid pulling in net/url just for this simple path extraction; arXiv IDs
	// cannot contain '?' or '#'.
	path := trimmed
	if idx := strings.Index(path, "://"); idx >= 0 {
		path = path[idx+3:]
		if slash := strings.Index(path, "/"); slash >= 0 {
			path = path[slash+1:]
		} else {
			return "", false
		}
	}
	if q := strings.IndexAny(path, "?#"); q >= 0 {
		path = path[:q]
	}
	path = strings.Trim(path, "/")

	for _, prefix := range []string{"abs/", "pdf/", "html/", "e-print/"} {
		if strings.HasPrefix(path, prefix) {
			id := strings.TrimPrefix(path, prefix)
			id = strings.TrimSuffix(id, ".pdf")
			if isArxivID(id) {
				return id, true
			}
		}
	}

	return "", false
}

// FetchURL fetches content from an arXiv URL.
// Priority:
//  1. HTML version (https://arxiv.org/html/{id}/)
//  2. TeX source (via arxiv2text binary)
func FetchURL(url string) (string, error) {
	_, id, ok := extractArxivIDFromAbsURL(url)
	if !ok {
		return "", fmt.Errorf("not a valid arXiv URL: %s", url)
	}

	// Priority 1: Try HTML version
	htmlURL := fmt.Sprintf("https://arxiv.org/html/%s/", id)
	if content, err := fetchArxivHTML(htmlURL); err == nil && content != "" {
		return content, nil
	}

	// Priority 2: Fallback to TeX source via arxiv2text
	absURL := fmt.Sprintf("https://arxiv.org/abs/%s", id)
	if content, err := tryArxiv2Text(absURL); err == nil && content != "" {
		return content, nil
	}

	return "", fmt.Errorf("failed to fetch paper from arXiv: HTML and TeX source both unavailable for %s", id)
}

// extractArxivIDFromAbsURL extracts the arXiv ID from an arXiv abs URL like
// https://arxiv.org/abs/2301.00001v2.
func extractArxivIDFromAbsURL(url string) (string, string, bool) {
	trimmed := strings.TrimSpace(url)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "http://arxiv.org/") &&
		!strings.HasPrefix(lower, "https://arxiv.org/") &&
		!strings.HasPrefix(lower, "http://www.arxiv.org/") &&
		!strings.HasPrefix(lower, "https://www.arxiv.org/") {
		return "", "", false
	}

	path := trimmed
	if idx := strings.Index(path, "://"); idx >= 0 {
		path = path[idx+3:]
		if slash := strings.Index(path, "/"); slash >= 0 {
			path = path[slash+1:]
		} else {
			return "", "", false
		}
	}
	if q := strings.IndexAny(path, "?#"); q >= 0 {
		path = path[:q]
	}
	path = strings.Trim(path, "/")

	if !strings.HasPrefix(path, "abs/") {
		return "", "", false
	}

	id := strings.TrimPrefix(path, "abs/")
	if !isArxivID(id) {
		return "", "", false
	}

	return "https://arxiv.org/abs/" + id, id, true
}

// FetchArxivAsMarkdown fetches the HTML version of an arXiv paper and converts
// it to Markdown, preserving tables and document structure.
func FetchArxivAsMarkdown(arxivID string) (string, error) {
	htmlURL := fmt.Sprintf("https://arxiv.org/html/%s/", arxivID)

	html, err := fetchArxivHTMLRaw(htmlURL)
	if err != nil {
		return "", err
	}

	return HTMLToMarkdown(html), nil
}

// fetchArxivHTML fetches the HTML version of an arXiv paper, converting to
// plain text for LLM consumption.
func fetchArxivHTML(url string) (string, error) {
	html, err := fetchArxivHTMLRaw(url)
	if err != nil {
		return "", err
	}
	return stripHTML(html), nil
}

// fetchArxivHTMLRaw fetches the raw HTML from an arXiv HTML URL.
func fetchArxivHTMLRaw(url string) (string, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Don't follow redirects; if arXiv redirects away from /html/, the
			// HTML version likely doesn't exist.
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetching arxiv HTML: %w", err)
	}
	defer resp.Body.Close()

	// arXiv returns 302 or 404 when HTML version is not available
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("arxiv HTML HTTP %d", resp.StatusCode)
	}

	// Check content type is HTML
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(ct), "text/html") {
		return "", fmt.Errorf("unexpected content type: %s", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading arxiv HTML: %w", err)
	}

	if len(body) < 1000 {
		return "", fmt.Errorf("arxiv HTML too short (%d bytes), likely not a real paper HTML", len(body))
	}

	return string(body), nil
}

// stripHTML removes HTML tags from the input, returning plain text with
// whitespace normalized.
func stripHTML(html string) string {
	// Remove scripts and styles first
	reScript := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = reScript.ReplaceAllString(html, "")
	reStyle := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = reStyle.ReplaceAllString(html, "")

	// Replace <br> and block-level tags with newlines
	reBr := regexp.MustCompile(`(?is)<br\s*/?>`)
	html = reBr.ReplaceAllString(html, "\n")
	reBlock := regexp.MustCompile(`(?is)</?(p|div|h[1-6]|li|tr|blockquote|section|pre|article)[^>]*>`)
	html = reBlock.ReplaceAllString(html, "\n")

	// Remove remaining tags
	reTag := regexp.MustCompile(`<[^>]*>`)
	html = reTag.ReplaceAllString(html, "")

	// Decode common HTML entities
	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&quot;", "\"")
	html = strings.ReplaceAll(html, "&#39;", "'")
	html = strings.ReplaceAll(html, "&nbsp;", " ")

	// Normalize whitespace: collapse multiple newlines, trim
	reBlankLines := regexp.MustCompile(`\n{3,}`)
	html = reBlankLines.ReplaceAllString(html, "\n\n")

	return strings.TrimSpace(html)
}

var arxivTitleRe = regexp.MustCompile(`<title>\[([^\]]+)\]\s+(.*?)</title>`)
var arxivAbstractRe = regexp.MustCompile(`<blockquote class="abstract[^"]*">\s*(.*?)</blockquote>`)
var arxivMetaAbstractRe = regexp.MustCompile(`<meta\s+name="citation_abstract"\s+content="([^"]+)"`)
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// FetchArxivTitle fetches the arXiv abstract page and extracts the paper title from the HTML <title> tag.
// Format: "[arxivID] Paper Title" → returns "Paper Title".
func FetchArxivTitle(arxivID string) (string, error) {
	url := "https://arxiv.org/abs/" + arxivID

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetching arxiv page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("arxiv HTTP %d", resp.StatusCode)
	}

	// Read only first 8KB — <title> is always in <head>
	buf := make([]byte, 8192)
	n, err := resp.Body.Read(buf)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("reading arxiv page: %w", err)
	}

	matches := arxivTitleRe.FindSubmatch(buf[:n])
	if len(matches) < 3 {
		return "", fmt.Errorf("title not found in arxiv page")
	}

	return string(matches[2]), nil
}

// FetchArxivAbstract fetches the arXiv abstract page and extracts the paper abstract.
// Returns the plain-text abstract (stripped of HTML tags).
func FetchArxivAbstract(arxivID string) (string, error) {
	url := "https://arxiv.org/abs/" + arxivID

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetching arxiv page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("arxiv HTTP %d", resp.StatusCode)
	}

	// Read full body (abstract pages are small, typically <50KB)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading arxiv page: %w", err)
	}

	// Try meta tag first (citation_abstract)
	if matches := arxivMetaAbstractRe.FindSubmatch(body); len(matches) >= 2 {
		return strings.TrimSpace(string(matches[1])), nil
	}

	// Fallback: blockquote.abstract
	if matches := arxivAbstractRe.FindSubmatch(body); len(matches) >= 2 {
		raw := string(matches[1])
		// Strip HTML tags inside the blockquote
		raw = htmlTagRe.ReplaceAllString(raw, "")
		// Decode common entities
		raw = strings.NewReplacer(
			"&amp;", "&", "&lt;", "<", "&gt;", ">",
			"&quot;", "\"", "&#39;", "'",
		).Replace(raw)
		return strings.TrimSpace(raw), nil
	}

	return "", fmt.Errorf("abstract not found in arxiv page")
}

func tryArxiv2Text(url string) (string, error) {
	// Check if arxiv2text is available
	path, err := exec.LookPath("arxiv2text")
	if err != nil {
		return "", fmt.Errorf("arxiv2text not found in PATH")
	}

	cmd := exec.Command(path, url)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("arxiv2text failed: %w", err)
	}

	return string(output), nil
}

func httpFetch(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetching URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	return string(body), nil
}

// LoadFile loads content from a file path.
func LoadFile(path string) (string, error) {
	// Expand ~ to home directory
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[1:])
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading file: %w", err)
	}

	return string(data), nil
}

// IsURL checks if a string looks like a URL.
func IsURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// IsFilePath checks if a string looks like a file path.
func IsFilePath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "~")
}
