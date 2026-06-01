# AGENTS.md

This file provides guidance to coding agents when working with code in this repository.

## Project Overview

PaperAgent is an AI paper reading assistant built in Go with a Web UI (React SPA). Users provide an academic paper (URL or paste), and the AI generates a detailed summary, then enters multi-round Q&A mode. The UI and documentation are in Chinese.

## Build & Run

```bash
go build -o paperagent .          # Build binary
go install github.com/happyTonakai/paperagent@latest  # Install globally

./paperagent ./paper.txt          # Load from file
./paperagent https://arxiv.org/... # Load from URL
./paperagent                      # Interactive paste mode
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

**Tech stack**: Go 1.25+, React 19 + TypeScript + Tailwind CSS (frontend), YAML config, JSON persistence.

**Core design principle**: The full paper text always stays in the LLM context (L1). Only the last 5 rounds of Q&A are retained (L2). The initial summary does NOT enter subsequent conversation context. This prevents hallucination from conversation history drowning out paper details.

### Two-phase state machine

- **INIT phase**: Paper content + `heavy.txt` prompt sent to API. Streams a detailed Markdown summary via SSE. Title extracted async via light model.
- **CHAT phase**: Each question sends paper content + `light.txt` prompt + last 5 rounds.

### Module layout (`internal/`)

| Package | Responsibility |
|---|---|
| `config/` | `~/.paperagent/config.yaml` loading, env var overrides, path helpers |
| `api/` | OpenAI-compatible HTTP client. `ChatStream()` returns `<-chan StreamChunk` via SSE goroutine. `ExtractTitle()` is an async helper using light model. |
| `session/` | `Paper` and `Message` data models. Thread-safe `Manager` (mutex-protected) for CRUD + persistence to `~/.paperagent/papers/{id}.json`. Uses UUID-based session IDs. |
| `prompt/` | `//go:embed` templates (`heavy.txt`, `light.txt`, `summarize.txt`). `Get(name, fallback)` checks user override at `~/.paperagent/prompts/{name}.txt` first. |
| `urlparse/` | `FetchURL()` tries external `arxiv2text` binary first, falls back to HTTP GET. Supports arxiv URL normalization and PDF download. `LoadFile()` reads with `~` expansion. |
| `export/` | `ExportToObsidian()` writes Markdown with YAML frontmatter to Obsidian vault. Customizable template at `~/.paperagent/prompts/export.md`. |

### Data flow

1. User provides paper → `urlparse` fetches content
2. `session.NewPaper()` creates paper object
3. INIT: full paper + HEAVY_PROMPT → streamed summary
4. CHAT: each question → paper + LIGHT_PROMPT + last 5 rounds → streamed answer
5. Async: title extraction via light model
6. All persisted as JSON in `~/.paperagent/papers/`

### Entry point

`main.go` loads config, checks API key, starts HTTP server with embedded frontend, and runs system tray.

## Token estimation

Uses `len(text) / 4` — lightweight, no external dependency.

## Configuration

Three layers (in priority order): environment variables (`PAPER_API_KEY`, `PAPER_BASE_URL`, etc.) > `~/.paperagent/config.yaml` > built-in defaults. Custom prompts override embedded defaults from `~/.paperagent/prompts/`.
