# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository. Referenced by CLAUDE.md.

## Project Overview

PaperPaper is a terminal-based AI paper reading assistant built in Go. Users provide an academic paper (file, URL, or paste), and the AI generates a detailed summary, then enters multi-round Q&A mode. The UI and documentation are in Chinese.

## Build & Run

```bash
go build -o paperpaper .          # Build binary
go install github.com/paperpaper/paperpaper@latest  # Install globally

./paperpaper ./paper.txt          # Load from file
./paperpaper https://arxiv.org/... # Load from URL
./paperpaper                      # Interactive paste mode
```

## Testing

```bash
# All tests (some hit real APIs)
go test ./... -v

# Unit tests only (no API calls)
go test ./internal/config/ ./internal/session/ ./internal/prompt/ ./internal/urlparse/ ./internal/export/ -v

# Lint
go vet ./...
```

## Architecture

**Tech stack**: Go 1.25+, Bubble Tea (TUI framework), Glamour (Markdown rendering), Lipgloss (styling), YAML config, JSON persistence.

**Core design principle**: The full paper text always stays in the LLM context (L1). Only the last 5 rounds of Q&A are retained (L2). The initial summary does NOT enter subsequent conversation context. This prevents hallucination from conversation history drowning out paper details.

### Two-phase state machine

- **INIT phase**: Paper content + `heavy.txt` prompt sent to API. Streams a detailed Markdown summary to the viewport. Title extracted async via light model.
- **CHAT phase**: Each question sends paper content + `light.txt` prompt + last 5 rounds. After each answer, a one-sentence digest is generated async (for UI navigation in `/list` view).

### Module layout (`internal/`)

| Package | Responsibility |
|---|---|
| `config/` | `~/.paperpaper/config.yaml` loading, env var overrides, path helpers |
| `api/` | OpenAI-compatible HTTP client. `ChatStream()` returns `<-chan StreamChunk` via SSE goroutine. `SummarizeQuestion()` and `ExtractTitle()` are async helpers using light model. |
| `session/` | `Paper` and `Message` data models. Thread-safe `Manager` (mutex-protected) for CRUD + persistence to `~/.paperpaper/papers/{id}.json`. Uses UUID-based session IDs. |
| `prompt/` | `//go:embed` templates (`heavy.txt`, `light.txt`, `digest.txt`). `Get(name, fallback)` checks user override at `~/.paperpaper/prompts/{name}.txt` first. |
| `tui/` | Bubble Tea Elm architecture. `model.go` (state), `update.go` (commands & events), `view.go` (rendering), `selection.go` (mouse text selection). Three modes: Normal, Input, List. |
| `urlparse/` | `FetchURL()` tries external `arxiv2text` binary first, falls back to HTTP GET. Supports arxiv URL normalization and PDF download. `LoadFile()` reads with `~` expansion. |
| `export/` | `ExportToObsidian()` writes Markdown with YAML frontmatter to Obsidian vault. Customizable template at `~/.paperpaper/prompts/export.md`. |

### Data flow

1. User provides paper → `urlparse` fetches content
2. `session.NewPaper()` creates paper object
3. INIT: full paper + HEAVY_PROMPT → streamed summary
4. CHAT: each question → paper + LIGHT_PROMPT + last 5 rounds → streamed answer
5. Async: title extraction + per-question digests via light model
6. All persisted as JSON in `~/.paperpaper/papers/`

### Entry point

`main.go` loads config, checks API key, creates TUI model, optionally loads paper from CLI arg, runs Bubble Tea program.

### UX features

- **Terminal title**: Shows paper title or URL in terminal tab while app is running
- **Mouse selection**: Text can be selected with mouse for copying
- **Export feedback**: `/export` shows status bar message instead of replacing conversation view
- **Question digests**: Light model generates compact topic labels for Q&A round navigation

## Key Commands (in-app)

`/new`, `/resume`, `/list`, `/open`, `/delete`, `/edit`, `/del`, `/summarize`, `/export`, `/model`, `/config`, `/help`, `/quit`

- `/resume` — Open saved paper sessions (replaces old /list for session browsing)
- `/list` — Jump list for current paper's Q&A rounds
- Tab completion available for command names
- Double Ctrl+C required to quit (prevents accidental exit)

## Token estimation

Uses `len(text) / 4` — lightweight, no external dependency.

## Configuration

Three layers (in priority order): environment variables (`PAPER_API_KEY`, `PAPER_BASE_URL`, etc.) > `~/.paperpaper/config.yaml` > built-in defaults. Custom prompts override embedded defaults from `~/.paperpaper/prompts/`.
