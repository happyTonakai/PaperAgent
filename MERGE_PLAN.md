# PaperAgent + ZenFlow 合并计划

## 背景

两个项目都是面向学术论文的 AI 工具：

| 项目 | 框架 | 当前能力 |
|------|------|----------|
| PaperAgent (当前) | Go + HTTP Server + React SPA | 单篇论文 URL/粘贴 → 摘要 → 多轮问答 |
| ZenFlow (待吸收) | Rust + Tauri v2 + React SPA | arXiv RSS 订阅 → LLM评分 → 每日推荐 → 对话 |

合并后 PaperAgent 同时拥有**论文问答**和**论文推荐**两套系统。

---

## 架构决策

| 决策项 | 决定 | 原因 |
|--------|------|------|
| 主语言 | Go (保持 PaperAgent) | ZenFlow 的 Rust 代码翻译为 Go |
| 桌面框架 | HTTP Server + 系统托盘 (保持 PaperAgent) | 不引入 Tauri |
| 推荐数据存储 | SQLite (`~/.config/paperagent/zenflow.db`) | 大量文章需要 SQL 查询 |
| 问答数据存储 | JSON 文件 (`~/.config/paperagent/papers/*.json`) | 保持不变，与推荐数据隔离 |
| 推荐 API | 纯 LLM 评分排序，无混合推荐 | 社区投票数据质量不可靠 |
| 社区投票 | 推荐生成时按需拉取，不参与排序 | HuggingFace 限流严重，新论文评分为 0 |
| 多 API 配置 | 三路独立：评分 / 问答 / 翻译，均 OpenAI 兼容 | 各任务可用不同模型 |
| Keychain | 保持加密 YAML 存储 | ZenFlow 的 Keychain 实现有问题 |
| 前端框架 | React 19 + TypeScript (保持 PaperAgent) | ZenFlow 也是 React，组件可移植 |

---

## 数据模型

### SQLite Schema (`internal/database/schema.go`)

```sql
articles 表（核心）:
  id TEXT PK              -- arXiv ID
  title TEXT NOT NULL      -- 论文标题
  link TEXT NOT NULL       -- 原文链接
  abstract TEXT            -- 摘要
  status INT DEFAULT 0     -- 0:unread, 1:clicked, 2:liked, -1:disliked, 3:mark_read
  score REAL DEFAULT 0.0   -- LLM 兴趣分 (0~1)
  author TEXT              -- 作者列表
  category TEXT            -- arXiv 分类
  hf_upvotes INT           -- HuggingFace Papers 点赞数
  ax_net_votes INT         -- AlphaXiv 净投票数（API 只返回 net votes）
  votes_updated_at TEXT    -- 投票数据最后更新时间
  comment TEXT             -- 用户评论
  recommend_date TEXT      -- 推荐日期 (YYYY-MM-DD)
  batch_order INT          -- 推荐批次内排序
  created_at TEXT          -- 创建时间 (自动)
```

**说明**：
- **无 `source` 字段**：所有文章来自 arXiv，无需标识来源
- **无 `settings` 表**：所有配置存储在 YAML 文件 (`~/.config/paperagent/config.yaml`) 中，不重复存 SQLite
- **无 `ax_downvotes`**：AlphaXiv API 只返回净投票数（upvotes - downvotes 的差值），无法获取反对数

**参考**: ZenFlow `db/schema.rs` 的 articles 表设计。

---

## 模块划分

### 1. `internal/database/` — SQLite 数据层 (新增)

| 文件 | 职责 | 参考 ZenFlow |
|------|------|-------------|
| `pool.go` | 连接池管理 (sync.Once 单例)、迁移执行 | `db/pool.rs` |
| `schema.go` | SQL Schema 定义 (v1 迁移) | `db/schema.rs` |
| `operations.go` | 全部 CRUD 操作 | `db/operations.rs` |

操作清单：
- `SaveArticle` / `SaveArticles` — 插入文章 (INSERT OR IGNORE)
- `GetArticles` — 按 status/limit/offset 查询
- `GetRecommendedArticles` — 按 score DESC 取 top N (纯LLM评分)
- `GetUnscoredArticles` — 取 score=0 未读文章 (待评分)
- `UpdateArticleScore` / `UpdateArticleScores` — 更新评分
- `UpdateArticleStatus` / `UpdateArticleComment` — 更新状态/评论
- `UpdateArticleVotes` — 更新社区投票
- `GetArticlesNeedingVotes` — 获取需要拉取投票的文章
- `MarkDailyRecommendations` — 标记每日推荐批次
- `GetArticlesByRecommendDate` / `GetRecommendationDates` — 按日期查询
- `GetStats` — 统计各状态文章数量
- `ArticleExists` / `GetExistingIDs` — 去重检查
- `ClearAllData` — 清空 (用于重新初始化)

**注意**：没有 settings CRUD — 所有配置走 YAML。

---

### 2. `internal/recommend/` — 推荐引擎 (新增)

| 文件 | 职责 | 参考 ZenFlow |
|------|------|-------------|
| `feed.go` | arXiv RSS 按分类订阅抓取 | `feed/fetcher.rs` |
| `scoring.go` | LLM 批量评分 (偏好 + 论文列表 → JSON 分数数组) | `llm/scoring.rs` + `algorithm/score.rs` |
| `preferences.go` | 偏好文件 CRUD (`~/.config/paperagent/preferences.md`) | `llm/preferences.rs` |
| `votes.go` | 社区投票 API (HuggingFace + AlphaXiv) | `votes.rs` |
| `algorithm.go` | 推荐生成 (评分取 top N + 社区投票辅助) | `algorithm/mod.rs` + `algorithm/score.rs` 的部分逻辑 |

#### 2a. `feed.go` — arXiv RSS 订阅

参考 ZenFlow: `ZenFlow/src-tauri/src-tauri/src/feed/fetcher.rs`

功能：
- `FetchArxivRSS(categories []string, maxPerCategory int) ([]NewArticle, error)`
  - 对每个分类调用 arXiv RSS endpoint: `http://export.arxiv.org/rss/{category}`
  - 解析 Atom/RSS XML
  - 提取 id (arxiv ID), title, link, abstract, author, category
  - 去重 (通过 `GetExistingIDs` 跳过已有文章)
  - 返回待插入的文章列表
- 单次调用，不做定时循环 (定时在 scheduler 中处理)

#### 2b. `scoring.go` — LLM 批量评分

参考 ZenFlow: `ZenFlow/src-tauri/src-tauri/src/llm/scoring.rs` + `ZenFlow/src-tauri/src-tauri/src/algorithm/score.rs`

**函数签名**:
```go
// ScoreArticlesBatch 对一批文章进行 LLM 评分
// 参数:
//   - client: LLM 客户端 (评分专用)
//   - preferences: 偏好文件内容
//   - articles: 待评分文章列表 (需要 id + title + abstract)
//   - onProgress: 可选进度回调 (completedBatches, totalBatches)
// 返回: map[articleID]score
func ScoreArticlesBatch(client *api.Client, preferences string, articles []ArticleInfo, batchSize int, onProgress func(int, int)) (map[string]float64, error)
```

**Prompt 结构**:
```
system: "你是一个学术论文推荐系统的评分助手。根据用户的兴趣偏好，为论文打分。
评分规则：
- 分数范围 0.0 到 1.0
- 1.0 表示与用户兴趣完全匹配
- 0.0 表示与用户兴趣完全不相关
请严格按照以下 JSON 数组格式返回：[{\"id\": \"...\", \"score\": 0.85}, ...]"

user: "## 用户兴趣偏好\n{preferences.md}\n\n## 待评分论文\nID: {id1}\n标题: {title1}\n摘要: {abstract1}\n---\nID: {id2}\n..."
```

**批量策略**：每批 `batchSize` 篇（默认 10），循环调用 LLM，每批结果合并。
**错误处理**：某批失败则跳过，继续处理下一批。

#### 2c. `preferences.go` — 偏好文件管理

参考 ZenFlow: `ZenFlow/src-tauri/src-tauri/src/llm/preferences.rs`

功能：
- `PreferencesPath()` → `~/.config/paperagent/preferences.md`
- `ReadPreferences() (string, error)` — 读取偏好文件
- `WritePreferences(content string) error` — 写入偏好文件
- `UpdatePreferences(client *api.Client, currentPrefs string, feedbacks []FeedbackArticle) (string, error)` — LLM 更新偏好

**偏好更新 Prompt**:
```
system: "你是一个用户偏好分析助手。根据用户对学术论文的反馈行为，更新和完善用户的兴趣偏好描述。..."

user: "## 当前用户偏好\n{currentPrefs}\n\n## 新的用户反馈\n- [点赞-推荐系统] {title}\n  摘要: {abstract}\n  用户评论: {comment}\n- [评分9-问答系统] {title}\n..."
```

**触发时机**：每日推荐生成时触发一次。

#### 2d. `votes.go` — 社区投票

参考 ZenFlow: `ZenFlow/src-tauri/src-tauri/src/votes.rs`

功能：
- `FetchHuggingFaceVotes(arxivID string) (int, error)` — GET `https://huggingface.co/api/papers/{arxiv_id}` → upvotes
- `FetchAlphaXivVotes(arxivID string) (int, error)` — GET `https://api.alphaxiv.org/papers/v3/{arxiv_id}/metrics` → publicTotalVotes
- `FetchVotesForArticles(ids []string) (map[string]VoteData, error)` — 并行批量拉取（限速每秒 2 请求）

**注意**：
- 仅在推荐生成时对选中的 20~30 篇文章调用
- HuggingFace 限流严重，需要做好错误处理
- 投票数据只用于 UI 展示，不参与推荐排序

#### 2e. `algorithm.go` — 推荐生成

参考 ZenFlow: `ZenFlow/src-tauri/src-tauri/src/algorithm/score.rs` 简化版

**简化为纯 LLM 评分排序**：

```go
// GenerateRecommendations 生成每日推荐
// 流程：
// 1. 获取所有未评分文章 (GetUnscoredArticles)
// 2. 读取偏好文件 (ReadPreferences)
// 3. LLM 分批评分 (ScoreArticlesBatch)
// 4. 批量写入评分 (UpdateArticleScores)
// 5. 按日期标记推荐 (MarkDailyRecommendations, 取 top dailyPapers 篇)
// 6. 对标记的文章拉取社区投票 (FetchVotesForArticles)
// 7. 写入投票数据 (UpdateArticleVotes)
// 8. 返回推荐列表
func GenerateRecommendations(client *api.Client, dailyPapers, batchSize int) ([]Article, error)
```

---

### 3. `internal/api/` — 多 API 客户端扩展 (改造)

参考 ZenFlow: `ZenFlow/src-tauri/src-tauri/src/llm/client.rs`

当前：单一 `Client`（一个 base_url + api_key + model）。

改造后：

```go
// internal/api/client.go — 保留公共 HTTP 基础
type Client struct {
    BaseURL string
    APIKey  string
    Model   string
    http    *http.Client
}

func New(baseURL, apiKey, model string) *Client

// ChatStream 流式调用 (已有)
func (c *Client) ChatStream(model string, messages []ChatMessage, tools []Tool) <-chan StreamChunk

// Chat 非流式调用 (新增, 评分用)
func (c *Client) Chat(model string, messages []ChatMessage, tools []Tool) (string, int, int, int, error)
```

新增 `Chat` 方法以支持非流式评分调用。评分模块通过 `NewScoringClient()` 获取一个配置了评分 API 参数的 `Client` 实例。

---

### 4. `internal/scheduler/` — 后台定时任务 (新增)

参考 ZenFlow: `ZenFlow/src-tauri/src-tauri/src/scheduler.rs`

```go
// internal/scheduler/scheduler.go

// Scheduler 管理后台定时任务
// 任务：
//   1. 每日 RSS 抓取 (每天 10:00 触发一次)
//   2. 抓取完成后自动触发评分 + 推荐生成 (如开启 auto_refresh)
type Scheduler struct {
    cfg    *config.Config
    api    *api.Client // 评分专用 client
    stopCh chan struct{}
}

func New(cfg *config.Config, scoringClient *api.Client) *Scheduler
func (s *Scheduler) Start()
func (s *Scheduler) Stop()
```

**简化**：只保留每天一次的 RSS 抓取。去掉 ZenFlow 的每小时检查和每 12h 投票同步。

---

### 5. `internal/config/` — 配置扩展 (改造)

参考 ZenFlow: `ZenFlow/src-tauri/src-tauri/src/settings.rs`

```yaml
# 新增字段
api:
  base_url: https://api.siliconflow.cn/v1    # 保留（问答用，向后兼容）
  api_key: ${PAPER_API_KEY}
  default_model: Qwen/Qwen2.5-7B-Instruct
  scoring:                                     # 新增：推荐评分 API
    base_url: https://api.siliconflow.cn/v1
    api_key: ${SCORING_API_KEY}
    model: Qwen/Qwen2.5-7B-Instruct
  translation:                                 # 新增：翻译 API（可选）
    base_url: https://api.siliconflow.cn/v1
    api_key: ${TRANSLATION_API_KEY}
    model: Qwen/Qwen2.5-7B-Instruct

recommend:                                     # 新增
  daily_papers: 20                             # 每日推荐数量
  scoring_batch_size: 10                       # 评分批次大小
  auto_refresh: true                           # RSS 抓取后自动生成推荐

arxiv_categories:                              # 新增
  - cs.LG
  - cs.CV
  - cs.AI
```

`Config` struct 新增字段：
```go
type APIConfig struct {
    BaseURL      string          `yaml:"base_url"`
    APIKey       string          `yaml:"api_key"`
    DefaultModel string          `yaml:"default_model"`
    Scoring      *APISubConfig   `yaml:"scoring,omitempty"`
    Translation  *APISubConfig   `yaml:"translation,omitempty"`
}

type APISubConfig struct {
    BaseURL string `yaml:"base_url"`
    APIKey  string `yaml:"api_key"`
    Model   string `yaml:"model"`
}

type RecommendConfig struct {
    DailyPapers      int      `yaml:"daily_papers"`
    ScoringBatchSize int      `yaml:"scoring_batch_size"`
    AutoRefresh      bool     `yaml:"auto_refresh"`
}

type Config struct {
    API            APIConfig        `yaml:"api"`
    Recommend      RecommendConfig  `yaml:"recommend"`
    ArxivCategories []string        `yaml:"arxiv_categories"`
    Obsidian       ObsidianConfig   `yaml:"obsidian"`
    UI             UIConfig         `yaml:"ui"`
    Feishu         FeishuConfig     `yaml:"feishu"`
}
```

---

### 6. `internal/server/handlers.go` — 新增路由 (扩展)

新增 API 路由：

```
GET    /api/recommend/config              → 获取推荐系统配置
PUT    /api/recommend/config              → 更新推荐系统配置
GET    /api/recommend/preferences         → 获取偏好文件内容
POST   /api/recommend/preferences         → 手动触发偏好更新
PUT    /api/recommend/preferences         → 手动编辑偏好文件
POST   /api/recommend/fetch               → 手动触发 RSS 抓取
POST   /api/recommend/generate            → 手动触发推荐生成
GET    /api/recommend/articles            → 文章列表 (?status=&limit=&offset=)
GET    /api/recommend/articles/recommended → 获取今日推荐
GET    /api/recommend/dates               → 有推荐的日期列表
GET    /api/recommend/dates/:date         → 某日推荐详情
PUT    /api/recommend/articles/:id/status  → 更新文章状态
PUT    /api/recommend/articles/:id/comment → 更新文章评论
POST   /api/recommend/votes               → 手动触发投票更新
GET    /api/recommend/stats               → 统计数据
```

---

### 7. 前端改造

#### 7a. 页面结构

```
App.tsx 改为 Tab 切换:
  ├── Tab 1: "论文对话" (Chat) — 现有 PaperAgent UI
  │   ├── <PaperList> (sidebar) | <ChatView> + <InputBox> (main)
  │   └── <NewPaperDialog> / <SettingsDialog> / <LogDialog>
  │
  └── Tab 2: "每日推荐" (Recommend) — 新增
      ├── <StatsBar> (统计栏: 未读/喜欢/点击)
      ├── <Toolbar> (操作行: 抓取文章/生成推荐/刷新/设置)
      ├── <FilterTabs> (筛选: 未读/喜欢/全部)
      ├── <ArticleList> (文章卡片列表)
      │   └── <ArticleCard> * N
      │       ├── 标题 + 摘要 (含 LaTeX 渲染)
      │       ├── 状态标签 + 评分 + 投票
      │       ├── 操作按钮: 👍 👎 → 💬 📋 🤖
      │       └── 评论输入框
      └── <WelcomeWizard> (首次初始化向导, 4步)

交互: 点击 ArticleCard 的 🤖 按钮 → 自动切换到 Tab 1 (Chat)
      → 创建新 PaperSession → 进入 INIT 阶段 → 流式摘要 → 可对话
```

#### 7b. 可复用组件 (从 ZenFlow 移植)

| 组件 | ZenFlow 源路径 | 说明 |
|------|---------------|------|
| `ArticleCard.tsx` | `src-tauri/src/components/ArticleCard.tsx` | 文章卡片，含 KaTeX、交互按钮、评论、投票显示 |
| `ArticleList.tsx` | `src-tauri/src/components/ArticleList.tsx` | 文章列表 |
| `DateGroupedArticleList.tsx` | `src-tauri/src/components/DateGroupedArticleList.tsx` | 按日期分组的推荐列表 (可选) |
| `StatsBar` | 内嵌在 `src-tauri/src/App.tsx` | 统计栏 (提取为独立组件) |
| `Toolbar` | 内嵌在 `src-tauri/src/App.tsx` | 操作按钮行 (提取为独立组件) |
| `WelcomeWizard.tsx` | `src-tauri/src/components/WelcomeWizard.tsx` | 初始化向导 |
| `SettingsModal` | `src-tauri/src/components/SettingsModal.tsx` | 设置弹窗 (合并到现有 SettingsDialog) |

#### 7c. 需要改动的内容

- **API 调用方式**：ZenFlow 用 Tauri IPC (`invoke<T>`)，PaperAgent 用 HTTP fetch。所有组件中的 `invoke()` 调用需要替换为 `fetch()`。
- **类型定义**：ZenFlow 的 `types/article.ts` 中的 `Article`、`Stats`、`AppSettings` 等 type 需要移植到 PaperAgent 的 `types/index.ts`。
- **Hooks**：需要新增 `useArticles.ts` (基于 HTTP fetch 的数据获取钩子)。
- **样式**：ZenFlow 的 CSS 文件 (ArticleCard.css 等) 需要适配 PaperAgent 的 CSS 体系。

#### 7d. 前端新增/修改文件

```
frontend/src/
├── App.tsx                          # 改造：添加 Tab 切换
├── types/index.ts                   # 扩展：添加 Article, Stats, AppSettings 等类型
├── stores/appStore.ts               # 扩展：添加推荐系统相关状态
├── hooks/
│   ├── usePapers.ts                 # 已有
│   ├── useSSE.ts                    # 已有
│   └── useArticles.ts              # 新增：推荐系统的文章 CRUD
├── components/
│   ├── ArticleCard.tsx              # 新增：文章卡片
│   ├── ArticleList.tsx              # 新增：文章列表
│   ├── WelcomeWizard.tsx            # 新增：初始化向导
│   ├── SettingsDialog.tsx           # 改造：合并推荐系统设置项
│   ├── RecommendTab.tsx             # 新增：推荐系统主页面
│   └── ...                          # 其余组件保持不变
```

---

## 实施顺序

```
阶段一：SQLite 数据层  ← 当前正在进行
  └── internal/database/ (pool.go + schema.go + operations.go)

阶段二：配置扩展
  └── internal/config/config.go 扩展 (多 API + 推荐参数)
  └── config.yaml 默认值更新

阶段三：推荐引擎核心
  ├── internal/recommend/feed.go         (RSS 抓取)
  ├── internal/recommend/preferences.go   (偏好文件管理)
  ├── internal/recommend/scoring.go       (LLM 批量评分)
  ├── internal/recommend/votes.go         (社区投票 API)
  └── internal/recommend/algorithm.go     (推荐生成流程编排)

阶段四：API 客户端 + 路由 + Scheduler
  ├── internal/api/client.go 扩展 (Chat 非流式方法)
  ├── internal/scheduler/scheduler.go     (每日 RSS 定时器)
  └── internal/server/handlers.go 扩展 (推荐路由)

阶段五：前端改造
  ├── 类型扩展
  ├── useArticles hook
  ├── 组件移植 (ArticleCard, ArticleList, RecommendTab, WelcomeWizard)
  ├── 设置合并 (SettingsDialog 扩展)
  └── App.tsx Tab 改造

阶段六：端到端集成测试
  ├── RSS 抓取 → 评分 → 推荐 → 展示
  ├── 推荐文章 → 跳转对话 → 问答
  └── 数据一致性验证
```

---

## 偏好更新 (待讨论)

当前状态：**跳过，等待后续讨论**

核心待确认问题：
1. 输入源：推荐系统 (点赞/点踩/评论) + 问答系统 (评分 rating)
2. 触发时机：每日推荐生成时触发一次 (每日汇总)
3. 时间窗口：最近一天的反馈（非 7 天或 30 天，只汇总前一天）
4. 增量策略：全量重写 (直接覆盖 preferences.md)
5. 用户可手动编辑偏好文件

> **备忘 — 偏好输入源的范围**：
> 每日偏好更新汇总的输入源应包含两部分：
> 1. **推荐系统反馈**：昨日通过 RSS 抓取 + 推荐给用户的论文，用户做出的点赞/点踩/评论操作
> 2. **问答系统新增**：用户主动通过问答系统提交的 arXiv 链接对应的论文（`~/.config/paperagent/papers/*.json` 中有 `arxiv_id` 的条目）
>    这些论文也是用户的兴趣信号，需要纳入当天的偏好汇总流程
> 具体实现方案等待后续讨论。


---

## 附录：关键 ZenFlow 源文件索引

| 功能 | 源文件路径 (相对于 ZenFlow repo) |
|------|----------------------------------|
| SQLite 连接池 | `src-tauri/src-tauri/src/db/pool.rs` |
| SQL Schema | `src-tauri/src-tauri/src/db/schema.rs` |
| DB CRUD | `src-tauri/src-tauri/src/db/operations.rs` |
| LLM 客户端 | `src-tauri/src-tauri/src/llm/client.rs` |
| LLM 评分 | `src-tauri/src-tauri/src/llm/scoring.rs` |
| LLM 偏好更新 | `src-tauri/src-tauri/src/llm/preferences.rs` |
| LLM 对话 | `src-tauri/src-tauri/src/llm/chat.rs` |
| 评分引擎 + 偏好更新触发 | `src-tauri/src-tauri/src/algorithm/score.rs` |
| RSS 抓取 | `src-tauri/src-tauri/src/feed/fetcher.rs` |
| 社区投票 | `src-tauri/src-tauri/src/votes.rs` |
| 定时器 | `src-tauri/src-tauri/src/scheduler.rs` |
| 设置管理 | `src-tauri/src-tauri/src/settings.rs` |
| Tauri Commands (前端API) | `src-tauri/src-tauri/src/commands/article.rs` |
| 前端: App 入口 | `src-tauri/src/App.tsx` |
| 前端: 文章卡片 | `src-tauri/src/components/ArticleCard.tsx` |
| 前端: 文章列表 | `src-tauri/src/components/ArticleList.tsx` |
| 前端: 设置弹窗 | `src-tauri/src/components/SettingsModal.tsx` |
| 前端: 初始化向导 | `src-tauri/src/components/WelcomeWizard.tsx` |
| 前端: Hooks | `src-tauri/src/hooks/useArticles.ts` |
| 前端: 类型定义 | `src-tauri/src/types/article.ts` |
