package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/paperpaper/paperpaper/internal/config"
	"github.com/paperpaper/paperpaper/internal/session"
	"github.com/paperpaper/paperpaper/internal/tui"
	"github.com/paperpaper/paperpaper/internal/urlparse"
)

func main() {
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Check API key
	if cfg.API.APIKey == "" || cfg.API.APIKey == "${OPENAI_API_KEY}" {
		fmt.Fprintln(os.Stderr, "Error: No API key configured.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Please configure your API key in one of the following ways:")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "  1. Set environment variable:")
		fmt.Fprintln(os.Stderr, "     export OPENAI_API_KEY=your-key-here")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "  2. Create config file:")
		fmt.Fprintln(os.Stderr, "     mkdir -p ~/.paperpaper")
		fmt.Fprintln(os.Stderr, "     cat > ~/.paperpaper/config.yaml << 'EOF'")
		fmt.Fprintln(os.Stderr, "     api:")
		fmt.Fprintln(os.Stderr, "       base_url: \"https://api.openai.com/v1\"")
		fmt.Fprintln(os.Stderr, "       api_key: \"your-key-here\"")
		fmt.Fprintln(os.Stderr, "       default_model: \"gpt-4o\"")
		fmt.Fprintln(os.Stderr, "       light_model: \"gpt-4o-mini\"")
		fmt.Fprintln(os.Stderr, "     EOF")
		os.Exit(1)
	}

	// Ensure directories exist
	os.MkdirAll(config.PapersDir(), 0755)
	os.MkdirAll(config.PromptsDir(), 0755)

	m := tui.NewModel(cfg)

	// If an argument is provided, load it (file path or URL)
	if flag.NArg() > 0 {
		input := flag.Arg(0)
		var content string
		var sourceURL string

		if urlparse.IsURL(input) {
			sourceURL = input
			content, err = urlparse.FetchURL(input)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error fetching URL: %v\n", err)
				os.Exit(1)
			}
		} else {
			content, err = urlparse.LoadFile(input)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
				os.Exit(1)
			}
		}

		p := session.NewPaper(content, sourceURL)
		m.LoadPaper(p)
	}

	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
