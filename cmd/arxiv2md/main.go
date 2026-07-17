// arxiv2md — standalone binary to convert arXiv papers to Markdown.
//
// Priority:
//  1. HTML version → HTMLToMarkdown (with tables, math)
//  2. TeX source  → TeXToMarkdown (with tables, math)
//
// Examples:
//
//	arxiv2md 2509.06926
//	arxiv2md https://arxiv.org/abs/2509.06926
//	arxiv2md https://arxiv.org/pdf/2509.06926
//	arxiv2md arxiv:2509.06926
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/happyTonakai/paperagent/internal/session"
	"github.com/happyTonakai/paperagent/internal/urlparse"
)

// version is set via ldflags at build time: -ldflags "-X main.version=v1.2.3"
var version = "dev"

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--version":
			fmt.Printf("arxiv2md %s\n", version)
			return
		case "--help", "-h":
			printUsage()
			return
		}
	}

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	input := strings.TrimSpace(os.Args[1])
	_, id, ok := urlparse.NormalizeArxivInput(input)
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: not a valid arXiv ID or URL: %s\n", input)
		os.Exit(1)
	}

	// Priority 1: HTML version → markdown (with tables preserved)
	content, err := urlparse.FetchArxivAsMarkdown(context.Background(), id)
	if err == nil && content != "" {
		body, refs := session.ExtractReferences(content)
		fmt.Println(body)
		if refs != "" {
			fmt.Fprintf(os.Stderr, "[arxiv2md] removed %d chars of references\n", len(refs))
		}
		return
	}
	fmt.Fprintf(os.Stderr, "HTML unavailable (%v), trying TeX source...\n", err)

	// Priority 2: TeX source → markdown (with tables preserved)
	content, err = urlparse.FetchArxivAsMarkdownFromTeX(context.Background(), id)
	if err == nil && content != "" {
		body, refs := session.ExtractReferences(content)
		fmt.Println(body)
		if refs != "" {
			fmt.Fprintf(os.Stderr, "[arxiv2md] removed %d chars of references\n", len(refs))
		}
		return
	}

	fmt.Fprintf(os.Stderr, "Error: both HTML and TeX source unavailable for %s\n", id)
	os.Exit(1)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: arxiv2md [--version] [--help] <arxiv-id-or-url>\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "Accepts arXiv ID, abs/PDF/HTML URL, or arxiv: prefix.\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "  arxiv2md 2509.06926\n")
	fmt.Fprintf(os.Stderr, "  arxiv2md https://arxiv.org/abs/2509.06926\n")
	fmt.Fprintf(os.Stderr, "  arxiv2md https://arxiv.org/pdf/2509.06926\n")
	fmt.Fprintf(os.Stderr, "  arxiv2md arxiv:2509.06926\n")
}
