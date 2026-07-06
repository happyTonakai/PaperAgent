# AGENTS.md

This file provides guidance to coding agents when working with code in this repository.

## Project Overview

PaperAgent 是一个 Go + Web UI 的 AI 论文阅读助手，已经从「单功能 Q&A」演化为「**Q&A + 每日推荐**」双系统产品。中文 UI / 中文文档。

- **Q&A 系统**：用户给一篇论文（URL / arXiv ID / 粘贴），AI 生成详尽摘要后进入多轮问答
- **每日推荐**：定时从 arXiv RSS 拉取新论文，LLM 按用户偏好打分，每天生成一批推荐并可推送到飞书

两个系统共享：API 配置、Feishu 集成、配置与提示词覆盖。

## Build & Run

```bash
# 推荐：用 justfile（开发 / 构建 / 静态分析都封装好了）
just --list                    # 查看所有 recipe
just dev                       # 开发模式：Go + Vite HMR 并发
just build                     # 完整构建（前端 + Go），产出单二进制
just arxiv2md                  # 编译独立 arxiv2md 工具
just vet                       # Go vet
just typecheck                 # 前端 tsc --noEmit

# 不使用 just 的等价命令
cd frontend && npm install && npm run build           # 前端 → internal/server/frontend-dist/
GOCACHE=/tmp/gocache-$USER go build -o paperagent .  # 内嵌前端打二进制
GOCACHE=/tmp/gocache-$USER go build -o arxiv2md ./cmd/arxiv2md/

# 首次运行
./paperagent                   # 启动 HTTP server（默认 8686）+ 系统托盘
# 或开发模式：PAPER_NO_BROWSER=1 PAPER_FOREGROUND=1  ./paperagent
# 浏览器访问 http://localhost:8686（或 5173 HMR 模式）

# 首次启动若 ~/.config/paperagent/config.yaml 不存在，
# Web UI 会自动弹出设置对话框引导用户配置 API 密钥。
```

### 二进制与端点

- 主二进制 `paperagent`：HTTP server + 系统托盘 + 飞书机器人
- 独立二进制 `arxiv2md`：`arxiv:ID` / `arxiv.org/abs/...` / `arxiv.org/pdf/...` → 干净 Markdown（HTML 优先，TeX 兜底）
- Chrome 扩展 `extension/`：在 arXiv 论文页右侧栏注入「在 PaperAgent 中打开」按钮

### 进程模型

- `main.go`：解析 `-version`、加载 config、检测 API key、建目录、`daemonize()`、起托盘
- `main_unix.go`（非 Windows）：默认调用 `setsid` 把自己 daemonize 成后台进程；`PAPER_FOREGROUND=1` 跳过（dev 模式）
- `main_windows.go`：空操作（托盘管理生命周期，不需要 fork）
- HTTP 端口：`PAPER_ADDR=:8686` 可改；启动时自动扫描 8686～8785 找可用端口
- 浏览器自动打开：默认开；`PAPER_NO_BROWSER=1` 关闭

## Testing

```bash
# 全部测试（部分会打真实 API）
go test ./... -v

# 单元测试（不打 API），CI 也只跑这批
go test ./internal/config/ ./internal/session/ ./internal/chat/ \
        ./internal/prompt/ ./internal/urlparse/ ./internal/export/ \
        ./internal/database/ ./internal/recommend/ -v

# Lint
go vet ./...
golangci-lint run --config .golangci.yaml ./...   # v2，与 .github/workflows/lint.yml 同配置
```

测试覆盖：config + crypto（含 AES 解密旧 key 兼容）、session、chat（fakeLLM 驱动 Engine+Sink 测试）、prompt、urlparse、export、database（含 SQL 池钩子）、recommend（feed / scoring / algorithm 单测 + e2e）、feishu（latex2unicode / 卡片尺寸 / 卡片模板）、api 客户端。

`internal/recommend/e2e_test.go` 是端到端冒烟：通过 `PAPER_RECOMMEND_RSS_FILE` 环境变量把 RSS XML 替换成本地文件，跑完整推荐管线，不打网络。

## Release

通过 GitHub Actions（`.github/workflows/release.yml`，`workflow_dispatch`）发布。**不要** 手动 `git tag` / `gh release create`。

CI（`.github/workflows/ci.yml` + `.github/workflows/lint.yml`）会在 PR 上跑 `npm ci && npm run build`、`go vet ./...`、单元测试、`go build`、golangci-lint v2。

### 步骤

1. **检查自上次 release 之后的 commits**

   ```bash
   git log v{last_tag}..HEAD --format="%h %s" --reverse
   ```

2. **检查实际 diff**（注意 rebase 后的 commit —— 看日期而非 hash）

   ```bash
   git diff --stat v{last_tag}..HEAD
   ```

3. **触发 release workflow**

   ```bash
   gh workflow run release.yml -f version=v1.2.0 -f release_notes='你的 release notes markdown'
   ```

   推荐**总是传** `version` 明确指定版本，避免 auto-bump 受脏 tag 影响。
   确认没有脏 tag 也可以只用 `bump`：`-f bump=patch`。
   `release_notes` 传空字符串时 action 会自动从 commits 生成。

   > ⚠️ 绝对不要手动 `gh release create` 或 `git tag` + `git push`，会留下脏 tag 干扰 auto-bump。

4. **等 action 完成**（https://github.com/happyTonakai/PaperAgent/actions）

   流水线：`prepare`（算版本号 + 编译前端，Node 堆 4GB）→ `build`（三平台：macOS arm64 / Windows amd64 / Linux amd64，macOS ad-hoc 签名）→ `release`（打 tag + 创建 Release + 上传二进制）。

## Architecture

**Tech stack**：Go 1.25.8、React 19 + TypeScript + Vite 6 + Tailwind 4、YAML 配置、JSON 持久化、SQLite（`modernc.org/sqlite`，纯 Go 零 CGO）、`fyne.io/systray`、`larksuite/oapi-sdk-go/v3`、KaTeX / highlight.js / react-markdown。

**核心设计原则（Q&A 系统）**：论文全文始终在 LLM 上下文（L1），多轮 Q&A 用锚点 + 动态截断：上下文窗口从 `TruncationAnchor` 开始累计 token，超过 `max_input_tokens`（默认 30000）后硬截断到 `min_recent_rounds`（默认 2）。截断后前缀稳定，KV cache 友好。初始摘要**不进入**后续对话上下文。`/btw` 旁听消息（`SkipContext: true`）从上下文窗口排除。

**双系统架构**：

```
            ┌──────────────────┐
            │   Web UI (React) │
            └────────┬─────────┘
                     │ HTTP / SSE
            ┌────────▼─────────┐
            │   HTTP Server    │  internal/server/
            │  (embed.FS SPA)  │  + handlers_recommend.go
            └────┬──────┬──────┘
                 │      │
   ┌─────────────┘      └──────────────┐
   ▼                                  ▼
┌──────────────┐               ┌──────────────┐
│  Q&A System  │               │  Recommend   │
│  session/    │               │  recommend/  │
│  api/        │               │  database/   │
│  chat/       │               │  scheduler/  │
│  prompt/     │               │              │
│  urlparse/   │               │              │
└──────┬───────┘               └──────┬───────┘
       │                              │
       └──────────┬───────────────────┘
                  ▼
          ┌──────────────┐
          │  config/     │
          │  export/     │
          │  feishu/     │
          │  systray/    │
          └──────────────┘
```

### Q&A 二阶段状态机

- **INIT**：`system.txt` + 论文全文 + `heavy.txt` 任务提示 → API → SSE 流式输出 Markdown 摘要；title 从 arXiv HTML 解析
- **CHAT**：`chat.Engine` 构建消息（`system.txt` + 论文全文 + `light.txt` + 动态上下文）→ LLM 可调用工具（`fetch_arxiv` 获取另一篇论文做交叉对比、`get_references` 查看参考文献）→ 流式回复经 `sseSink` / `cardSink` 输出

### 每日推荐管线（每天触发一次）

1. **收集反馈**：`CollectYesterdayFeedback()` —— 汇集推荐系统（`articles.status`，含点赞/点踩/已读/PDF点击）和 Q&A 系统（`chat_papers.updated_at` + rating）的反馈信号
2. **更新偏好**：`UpdatePreferences()` 用 LLM 把反馈合成新的 `preferences.md`
3. **拉 RSS**：`FetchArxivRSS(categories, 100)` 从配置的 arXiv category 拉新论文（按 arXiv ID 去重，过滤 `replace` 公告）
4. **LLM 打分**：`ScoreArticlesBatch()` 按偏好 + 摘要批量打分
5. **选 top N**：`MarkDailyRecommendations()` 用混合策略：`daily_papers × (1 - diversity_ratio)` 走 top score，其余走随机探索；每条打上 `recommendation_type = 'score'|'random'`
6. **拉社区票数**：`FetchVotesForArticles()` 并行查 HuggingFace / AlphaXiv（仅展示，不入排序）
7. **翻译**（可选）：独立 translation API 翻成中文写回 `translated_title` / `translated_abstract`
8. **推飞书**：`Server.RunPush(force)` —— 推送唯一入口，先判断节假日（见下），查 `pushed_at IS NULL` 的积压批次，调 `feishu.PushDailyRecommend()` 推 `daily_recommend_chat_id`，推完后批量写 `pushed_at = now`

`Scheduler` 每分钟醒一次，看是否到 `scheduled_time`（HH:MM）且今天没跑过。完成后通过 `onComplete` 回调触发翻译 + `RunPush(force=false)`。

**节假日跳过 / 积压合并推送**（`internal/holiday/` + `server.RunPush`）：

- `articles.pushed_at` (schema v5) 追踪推送状态：`NULL` = 待推，有值 = 已推
- 节假日判断在 `holiday.Checker.IsHoliday()`，三步走：
  1. `Provider` chain（现为 timor.tech 一个，按序可拓展到 3 个）+ in-memory cache（24h，按 date 切分）
  2. chain 全失败 → `IsWeekendHoliday()` (周六周日 = 假) fallback，打日志
  3. 仍 fail → 当工作日，**不阻塞推送**
- `Server.RunPush(force)` 流程（推送唯一入口，scheduler.onComplete / Web UI / 飞书 `/push` 三者共享）:
  1. force=false → `IsHoliday(today)`，节假日 → return 0（**不查 DB**），log `[push] today is holiday, skipping`
  2. 查 DB 拿 `pushed_at IS NULL AND recommend_date IS NOT NULL` 的全部（按 `recommend_date ASC, batch_order ASC` 排序——积压优先于今天）
  3. 调 `feishu.PushDailyRecommend()` + `MarkArticlesPushed(ids, ts)`
- 飞书端 `/push` 命令：用户主动调，绕过节假日判断走 force=true 路径。流程同 `POST /api/recommend/push-to-feishu`
- `SchedulerStatus` 增 `pending_push_count` + `last_push_at`，UI 展示「积压 N 篇 · 上次推送 ...」
- `ManualTrigger()` (= `POST /api/recommend/trigger`) 始终 force=true，走完整 pipeline + 强制推

### 模块布局（`internal/`）

| Package | 职责 |
|---|---|
| `config/` | `$XDG_CONFIG_HOME/paperagent/config.yaml` 加载（默认 `~/.config/paperagent/`）、env var override、AES-256-GCM 加密 API key、路径 helper（`ConfigDir` / `PapersDir` / `PromptsDir` / `ConfigExists`）。**`Config` 是 `sync.RWMutex` 保护的**，server 端 handler 持锁读。 |
| `api/` | OpenAI 兼容 HTTP 客户端。`Chat()` 同步 / `ChatStream()` 通过 goroutine 返回 `<-chan StreamChunk`。SSE chunk 解析 + prompt/completion/cached token 提取。提供 `FetchArxivTool()` / `GetReferencesTool()` 供 LLM 调用。 |
| `session/` | `Paper`（含 `SessionID` UUID、`Pinned` / `Rating` / `SkipContext` / `TruncationAnchor` / `References`）和 `Message` 模型（含 `ToolCalls` / `ToolCallID` 字段支持 tool call 序列化）。`Manager`（mutex 保护）做 CRUD + 持久化到 `~/.config/paperagent/papers/{uuid}.json`。`ExtractReferences()` 从 MD / TeX 抽出参考文献。`EstimateTokens(text) = len(text) / 4`。加载时从 `source_url` 回填 `arxiv_id`，从 `messages[0]` 回填 `Content` / `InitialSummary`。同步元数据到 `chat_papers` 表（Q&A 偏好信号源）。 |
| `chat/` | **新增**。共享 Q&A 引擎，Web SSE / 飞书统一使用。`Engine` 负责消息构造、LLM 流式循环（含 tool-call follow-up）、消息持久化。`Sink` 接口（`OnChunk` / `OnToolCall` / `OnDone` / `OnError`）适配不同传输。`BuildChatTools()` 统一注册 `fetch_arxiv` + `get_references` 工具。`BuildMessages()` 统一消息构造。`ResolveToolCall()` 统一工具分派（live-chat 和 retry 路径共用）。 |
| `prompt/` | `//go:embed` 模板：`system.txt` / `heavy.txt` / `light.txt` / `summarize.txt` / `scoring.txt` / `update-prefs.txt`。`Get*()` 检查用户覆写文件（`system.txt` 锁住，不允许覆写）。结构 `[system, user(论文), user(任务)]` 优化 prompt cache 命中。 |
| `urlparse/` | arXiv URL/ID 归一化。`FetchURL(ctx)` / `FetchArxivAsMarkdown(ctx)` / `FetchArxivAsMarkdownFromTeX(ctx)` / `FetchArxivTitleCtx(ctx)` / `FetchArxivAbstractCtx(ctx)` 均接受 `context.Context` 支持调用方取消（浏览器关 tab → arxiv2text 子进程 SIGKILL）。HTML 优先 → TeX 源 → PDF。`LoadFile()` 读本地文件（`~` 展开）。 |
| `export/` | `ExportToObsidian()` 写带 YAML frontmatter 的 Markdown 到 Obsidian vault。`ExportSummary()` 导出纯摘要。模板在 `~/.config/paperagent/prompts/export.md`。 |
| `database/` | **新增**。SQLite 池（`modernc.org/sqlite`，纯 Go）。`zenflow.db` 与 JSON session 存储**分开**。`articles` 表（推荐系统，schema v1）和 `chat_papers` 表（Q&A 元数据，schema v2），schema v3 / v4 加翻译字段、推荐类型、投票细分字段。`SetDB(nil)` 测试钩子；`OpenTestDB()` 内存 DB。WAL 模式，SQLite 单写者所以 `SetMaxOpenConns(1)`。 |
| `recommend/` | **新增**。`feed.go`（arXiv RSS 拉取 + 解析 + 去重 + 过滤 replace 公告）、`scoring.go`（LLM 批量打分 + JSON 响应解析）、`preferences.go`（preferences.md 读写 + LLM 偏好更新 + 反馈汇集）、`votes.go`（HuggingFace + AlphaXiv 并行拉票数）、`algorithm.go`（`GenerateDailyRecommendations` + `FetchAndRecommend` 主入口）。`e2e_test.go` 走 `PAPER_RECOMMEND_RSS_FILE` 环境变量替换 RSS 源。 |
| `scheduler/` | **新增**。每分钟醒一次的 cron 风格调度器。`scheduled_time`（HH:MM）+ `lastRunDate` 防止一天跑两次。`Status()` / `ManualTrigger()` / `UpdateConfig()` 供 API 调用。`SetOnComplete()` 回调在跑完后触发（server 用来翻译 + 推飞书）。 |
| `server/` | **新增**（从 main.go 抽出）。`Server` struct 持 config / 三套 API client（Q&A / scoring / translation）。`handlers.go`（Q&A、config、prompts、logs、active paper、feishu status）使用 `chat.Engine` + `sseSink` 驱动 Q&A 流式回复。`handlers_recommend.go`（recommend CRUD、scheduler、preferences、stats、push）。`sse.go` 流式响应。`sse_sink.go` 实现 `chat.Sink` 写 SSE `chunk`/`tool_call`/`done`/`error` 事件。`logbuffer.go` 内存日志环形缓冲。`//go:embed frontend-dist` 把前端 SPA 嵌进二进制，`/` 路由兜底返回 `index.html`。 |
| `systray/` | **新增**。`fyne.io/systray` 封装。菜单项：版本 / 关于 / 退出；左键直接开浏览器。SIGINT/SIGTERM 优雅退出。 |
| `feishu/` | 飞书机器人。`larksuite/oapi-sdk-go/v3` WebSocket 事件分发。Slash 命令：`/new` / `/list` / `/search` / `/summary` / `/fetch` / `/chat` / `/btw` / `/rate` / `/pin` / `/help`。`PushDailyRecommend()` 推推荐卡片（带交互按钮：点赞/点踩/已读/进入对话），通过 Card Patch 流式更新。`card_sink.go` 实现 `chat.Sink` 驱动聊天流式卡片（自动拆分多卡、LaTeX→Unicode）。`card.go` 卡片模板，`latex2unicode.go` 公式转 Unicode（避免飞书渲染 LaTeX 出错）。聊天命令使用 `chat.Engine` + `cardSink` 取代旧有内联流式。Hot-reload：配置改了调 `Reload()` 重建连接。 |

### 数据流（双系统）

**Q&A**：
1. 用户给论文 → `urlparse` 抓内容（arXiv HTML / TeX / PDF，所有函数接受 `context.Context` 支持取消）
2. `session.NewPaper()` 建 paper 对象（UUID）
3. INIT：`SYSTEM_PROMPT + 论文 + HEAVY_PROMPT` → 流式摘要
4. CHAT：`chat.Engine` 构造消息（`SYSTEM_PROMPT + 论文 + LIGHT_PROMPT + 动态上下文`）→ LLM 可调用工具（`fetch_arxiv` 跨论文对比、`get_references` 查看参考文献）→ `chat.Engine` 执行工具 handler 并持久化 tool-call/tool-result 消息 → 流式回复经 `sseSink` / `cardSink` 输出
5. Web SSE 走 `sseSink`（`server/sse_sink.go`），飞书走 `cardSink`（`feishu/card_sink.go`），两者共享 `chat.Engine`
6. 工具调用记录（`ToolCalls` / `ToolCallID`）持久化到 paper JSON，后续轮次可重放避免重复调用
7. 持久化为 JSON `~/.config/paperagent/papers/{uuid}.json`（content / initial_summary 在写出时被清空，从 messages[0] 回填）
8. 元数据同步到 SQLite `chat_papers` 表（作为偏好信号源）

**每日推荐**：
1. Scheduler 到点 → `recommend.FetchAndRecommend()`
2. 收集昨日反馈（推荐 status 含点赞/点踩/已读/PDF点击 + Q&A rating）→ LLM 更新 preferences.md
3. 拉 arXiv RSS → 过滤 `replace` 公告 → 按 arXiv ID 去重入 SQLite
4. LLM 按偏好 + 摘要批量打分 → 回写 `articles.score`
5. 混合策略选 top N（score + diversity 随机）→ 打 `recommend_date` / `batch_order` / `recommendation_type`
6. 拉 HF / AlphaXiv 票数（仅展示）
7. 翻译（如果配了 translation API）→ 写 `translated_title` / `translated_abstract`
8. 推飞书卡片到 `daily_recommend_chat_id`

### Entry point

`main.go` → `config.Load()` → 警告式 config 错误（env var 未解析、API key 缺失）→ `daemonize()` → `systray.Run()` 启动系统托盘；托盘 `onReady` 中启动 HTTP server（`server.New`）→ 启动飞书 bot（如果 `feishu.enabled`）→ 浏览器自动打开。SIGINT/SIGTERM 走托盘 `Quit` 路径。

`server.New()` 内部：建 logbuffer → 注册路由 → 启动 scheduler（如果 `arxiv_categories` 非空）→ scheduler 完成后通过 `onComplete` 回调翻译 + 推飞书 → 一键迁移 `~/.config/paperagent/papers/*.json` 到 `chat_papers` 表（首次启动）。

## Token estimation

`len(text) / 4` —— 轻量、无外部依赖。`EstimateTokens(text)` 是公开 helper。Q&A 动态截断算法：每轮 API 返回的 `prompt_tokens + completion_tokens` 超过 `max_input_tokens` 时，调用 `Paper.SetAnchorFromTokens(round, ...)` 把 `TruncationAnchor` 设为 `round - min_recent_rounds + 1`（floor 1）。`RecentContextMessages()` 从 anchor 算起返回 messages。`TruncateContextMessages()` 是 standalone 变体（retry-chat 用，不动 paper 状态）。

关键配置（`UI` section）：
- `min_recent_rounds`（默认 2）：截断后最少保留的轮数
- `max_input_tokens`（默认 30000）：触发截断的 token 预算

## Configuration

三层优先级：**环境变量** > `$XDG_CONFIG_HOME/paperagent/config.yaml`（默认 `~/.config/paperagent/config.yaml`） > 内置默认值。

### 环境变量

| 变量 | 用途 |
|---|---|
| `OPENAI_API_KEY` | Q&A 系统主 API key（仅默认填入 config 模板，实际读 config） |
| `OPENAI_BASE_URL` / `OPENAI_MODEL_NAME` | 启动时填入默认 config |
| `PAPER_ADDR` | HTTP 监听地址（默认 `:8686`） |
| `PAPER_NO_BROWSER=1` | 禁用自动打开浏览器（dev 模式） |
| `PAPER_FOREGROUND=1` | 跳过 daemonize（dev 模式） |
| `PAPER_RECOMMEND_RSS_FILE` | e2e 测试专用：把 RSS 替换成本地 XML 文件 |

config.yaml 里所有 `${VAR}` 引用都会走 `os.ExpandEnv` 展开；未解析的引用会在启动时打 warning（不致命，UI 引导用户修）。

### 完整 config schema

```yaml
api:
  base_url: "https://api.openai.com/v1"
  api_key: "${OPENAI_API_KEY}"        # 自动 AES-256-GCM 加密后写盘
  default_model: "gpt-4o"
  scoring:                              # 可选：scoring API（推荐打分）
    base_url: "..."
    api_key: "..."
    model: "gpt-4o-mini"
  translation:                          # 可选：翻译 API（推荐翻译）
    base_url: "..."
    api_key: "..."
    model: "gpt-4o-mini"

arxiv_categories: ["cs.AI", "cs.CL"]   # 推荐系统的 arXiv 类别

recommend:
  daily_papers: 20
  scoring_batch_size: 10
  diversity_ratio: 0.3                  # 0-1：随机探索占比
  scheduled_time: "08:00"               # HH:MM
  push_to_feishu: true                  # 推飞书开关

feishu:
  enabled: true
  app_id: "cli_xxxxx"
  app_secret: "xxxxx"
  daily_recommend_chat_id: "oc_xxxxx"  # 每日推荐推送目标群

obsidian:
  vault_path: "~/Documents/Obsidian/MyVault"
  export_folder: "Papers"

ui:
  min_recent_rounds: 2
  max_input_tokens: 30000
```

### API key 加密

`config.Save()` 写盘前自动 AES-256-GCM 加密 `api_key`（前缀 `!!aes:`）。密钥在 `~/.config/paperagent/.key`（hex-encoded 32 字节，权限 0600），首次启动自动生成。**旧版本 bug**（直接用 hex 字符串的前 32 字节作 key）已修，新代码 hex-decode 后用；`loadLegacyKey()` 兜底兼容老 key 文件。

### 自定义提示词

`~/.config/paperagent/prompts/` 下放 `{name}.txt` 覆写内嵌默认值。**`system.txt` 锁住**不允覆写（影响太大）。可覆写：`heavy` / `light` / `summarize` / `scoring` / `update-prefs`。

### 飞书机器人

- `feishu.enabled: true` + `app_id` + `app_secret` 启用
- WebSocket 模式（不需要公网回调）
- 需要权限：`im:message` + `im:message:send_as_bot` + `card.action.trigger`
- 改动通过 hot-reload 立即生效（`feishu.Bot.Reload()` 重建连接）
- 飞书机器人会发到自己的 chat 或被邀请进的群；`daily_recommend_chat_id` 是推荐推送的固定目标群

### 推荐系统

- 跑通最小配置：填 `arxiv_categories` + `api.scoring`（如果用不同 endpoint）
- 偏好从空开始：第一天推送会用 LLM 按（空）偏好批量打分；用户给反馈（点赞/点踩/已读/评分）后第二天起 LLM 持续更新 `preferences.md`
- `preferences.md` 在 `~/.config/paperagent/preferences.md`，可手动编辑
- `diversity_ratio` 是探索/利用权衡：0 = 纯 score，1 = 纯随机
- rating=0 视为「未评分」，**不当作负面信号**
- `mark_read=3`（批量已阅）**不参与偏好更新**（用户会无脑点已阅，会污染信号）
