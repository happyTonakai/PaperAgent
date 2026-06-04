// arxiv2md — standalone binary to convert arXiv papers to Markdown.
//
// Priority:
//  1. HTML version → HTMLToMarkdown (with tables, math)
//  2. TeX source  → TeXToMarkdown (with tables, math)
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/happyTonakai/paperagent/internal/urlparse"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: arxiv2md <arxiv-id-or-url>\n")
		os.Exit(1)
	}

	input := strings.TrimSpace(os.Args[1])
	_, id, ok := urlparse.NormalizeArxivInput(input)
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: not a valid arXiv ID or URL: %s\n", input)
		os.Exit(1)
	}

	// Priority 1: HTML version → markdown (with tables preserved)
	content, err := urlparse.FetchArxivAsMarkdown(id)
	if err == nil && content != "" {
		fmt.Println(content)
		return
	}
	fmt.Fprintf(os.Stderr, "HTML unavailable (%v), trying TeX source...\n", err)

	// Priority 2: TeX source → markdown (with tables preserved)
	content, err = urlparse.FetchArxivAsMarkdownFromTeX(id)
	if err == nil && content != "" {
		fmt.Println(content)
		return
	}

	fmt.Fprintf(os.Stderr, "Error: both HTML and TeX source unavailable for %s\n", id)
	os.Exit(1)
}
