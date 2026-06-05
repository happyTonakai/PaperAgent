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

**Core design principle**: The full paper text always stays in the LLM context (L1). Q&A rounds use anchor-based dynamic truncation: a window grows from the last truncation anchor, and when estimated input tokens exceed `max_input_tokens` (default 30000), a hard truncation drops to `min_recent_rounds` (default 2). This gives KV-cache-friendly stable prefixes between truncations. The initial summary does NOT enter subsequent conversation context. BTW messages (via `/btw`) are excluded.

### Two-phase state machine

- **INIT phase**: `system.txt` + paper content + `heavy.txt` task prompt sent to API. Streams a detailed Markdown summary via SSE. Title extracted from URL via HTML parsing.
- **CHAT phase**: Each question sends `system.txt` + paper content + `light.txt` task prompt + dynamic context window. Window grows from `TruncationAnchor`; hard-truncates to `min_recent_rounds` when exceeding `max_input_tokens`.

### Module layout (`internal/`)

| Package | Responsibility |
|---|---|
| `config/` | `~/.config/paperagent/config.yaml` loading, env var overrides, path helpers |
| `api/` | OpenAI-compatible HTTP client. `ChatStream()` returns `<-chan StreamChunk` via SSE goroutine. `ExtractTitle()` helper for title extraction. |
| `session/` | `Paper` data model (includes `Pinned`, `Rating`, `SkipContext`, `TruncationAnchor` fields) and `Message` data model. Thread-safe `Manager` (mutex-protected) for CRUD + persistence to `~/.config/paperagent/papers/{id}.json`. Uses UUID-based session IDs. `RecentContextMessages(minRounds, maxInputTokens, baseTokens)` applies anchor-based dynamic truncation. `TruncateContextMessages()` is the standalone variant used by retry-chat. `EstimateTokens(text)` returns `len(text)/4`. |
| `prompt/` | `//go:embed` templates (`system.txt`, `heavy.txt`, `light.txt`, `summarize.txt`). `Get(name, fallback)` checks user override at `~/.config/paperagent/prompts/{name}.txt` first. Messages are structured as `[system: system.txt, user: paper content, user: task prompt]` for prompt caching optimization. |
| `urlparse/` | `FetchURL()` tries external `arxiv2text` binary first, falls back to HTTP GET. Supports arxiv URL normalization and PDF download. `LoadFile()` reads with `~` expansion. |
| `export/` | `ExportToObsidian()` writes Markdown with YAML frontmatter to Obsidian vault. Customizable template at `~/.config/paperagent/prompts/export.md`. |
| `feishu/` | Feishu bot via `larksuite/oapi-sdk-go/v3`. WebSocket event handling, slash commands (`/new`, `/list`, `/summary`, `/fetch`, `/btw`, `/help`), streaming card updates via Patch API, token refresh + transient retry. Per-chat session tracking for active paper. |

### Data flow

1. User provides paper → `urlparse` fetches content
2. `session.NewPaper()` creates paper object
3. INIT: SYSTEM_PROMPT + full paper + HEAVY_PROMPT → streamed summary
4. CHAT: each question → SYSTEM_PROMPT + paper + LIGHT_PROMPT + dynamic context (anchor-based, hard-truncate on budget exceeded) → streamed answer
5. BTW questions (sent via `/btw <question>`) are persisted with `SkipContext: true` and excluded from step 4's context
6. Title extracted from URL via HTML parsing (urlparse)
7. All persisted as JSON in `~/.config/paperagent/papers/`

### Entry point

`main.go` loads config, checks API key, starts HTTP server with embedded frontend, starts Feishu bot (if enabled), and runs system tray.

## Token estimation

Uses `len(text) / 4` — lightweight, no external dependency. `EstimateTokens(text)` is the public helper. The dynamic truncation algorithm sums `Message.TokenCount` from the `TruncationAnchor` onward; when cumulative tokens exceed `max_input_tokens`, a hard truncation drops to `min_recent_rounds` and sets a new anchor. `TruncateContextMessages(messages, minRounds, maxTokens, baseTokens)` is the standalone variant used by retry-chat for context budgeting without mutating paper state.

Key config fields (in `UI` section):
- `min_recent_rounds` (default 2): floor — at least this many rounds kept after truncation
- `max_input_tokens` (default 30000): budget — truncation triggers when exceeded

## Configuration

Three layers (in priority order): environment variables (`PAPER_API_KEY`, `PAPER_BASE_URL`, etc.) > `~/.config/paperagent/config.yaml` > built-in defaults. Custom prompts override embedded defaults from `~/.config/paperagent/prompts/` (`system.txt`, `heavy.txt`, `light.txt`, `summarize.txt`).

### Feishu bot

When `feishu.enabled: true` and `feishu.app_id`/`feishu.app_secret` are configured, the bot connects via WebSocket and responds to slash commands. Configure in the Web UI settings page (Settings → API Config → Feishu Bot) or directly in `config.yaml`:

```yaml
feishu:
  enabled: true
  app_id: "cli_xxxxx"
  app_secret: "xxxxx"
```

Changes take effect immediately (hot-reload). The bot must be added to a Feishu group chat or used in 1:1 bot chat. Requires `im:message` and `im:message:send_as_bot` permissions in the Feishu developer console, and the `card.action.trigger` event subscription.
