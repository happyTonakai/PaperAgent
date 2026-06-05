# PaperAgent 📄

**给你的论文装上 AI 大脑 —— 粘贴 arXiv 链接，深度解析，多轮追问。**

PaperAgent 是一个 AI 论文阅读助手。给它一篇论文（arXiv 链接），AI 生成详实、可复现级别的深度解析，随后进入多轮问答模式。所有对话持久化到本地，随时可以恢复。

**默认启动为 Web UI 模式**（浏览器自动打开）。

## Why

市面上已有不少 AI 能帮你「读」论文 —— Gemini、Claude 都有超长上下文，丢一篇 PDF 进去也能总结。但实际用下来有几个痛点：

**痛点一：PDF 解析本身就是个坑。**

直接从 PDF 提取文本，公式乱码、表格错位、双栏混排 —— 你花在修解析结果上的时间比读论文还多。我们的做法是：你只需要给一个 **arXiv 链接**，我们直接从 HTML/TeX 源抓取内容，自动转成干净的 Markdown，并**去除 References 等无关章节**。省去了解析 PDF 的一切烦恼。

**痛点二：超长上下文 ≠ 超准。**

Gemini 有 1M token 上下文，听起来很美好。但论文全文本身就有几千到上万 token，再加上反复多轮问答，上下文窗口会迅速膨胀。几十轮下来，模型要在一片汪洋中找答案，准确性断崖式下降，对话历史中的噪声逐渐「淹没」论文细节，漂移和幻觉随之而来。

**我们的解法：只保留论文原文 + 最近 N 轮问答。**

观察一个朴素的规律：你对一篇论文的追问往往是「发散式」的 —— 这轮在问实验设计，下轮跳到数学推导，再下轮可能回头看结论。**多轮之前的问题和回答，对当前提问几乎没有参考价值。**

所以我们只把两样东西放进上下文：

1. **论文全文（始终保留）** —— AI 回答的锚点，永不丢失。
2. **最近 N 轮问答（默认 5 轮）** —— 维持对话连贯性，同时不让历史变成噪声。

初始生成的深度摘要则**不进入**后续对话上下文，避免占用宝贵的窗口。

这样每轮问答的上下文窗口都保持干净、聚焦，模型始终「记得」论文本身，而不被聊天记录带偏。

## 安装

从 [Releases](https://github.com/happyTonakai/PaperAgent/releases) 下载对应平台二进制：

```bash
# macOS (Apple Silicon)
curl -L -o paperagent https://github.com/happyTonakai/PaperAgent/releases/latest/download/paperagent_darwin_arm64
chmod +x paperagent
xattr -cr paperagent                     # macOS Gatekeeper 隔离移除
sudo mv paperagent /usr/local/bin/

# Linux (amd64)
curl -L -o paperagent https://github.com/happyTonakai/PaperAgent/releases/latest/download/paperagent_linux_amd64
chmod +x paperagent
sudo mv paperagent /usr/local/bin/

# Windows (amd64)
# 下载 paperagent_windows_amd64.exe，双击运行即可
```

二进制内嵌前端静态资源（React SPA），运行时无需安装 Node.js。

### 🌐 Chrome 扩展（可选）

[![Chrome Web Store](https://img.shields.io/chrome-web-store/v/ojkppdajbpnhppadnnfpaabakmolcbkf?label=Chrome%20%E6%89%A9%E5%B1%95)](https://chromewebstore.google.com/detail/paperagent/ojkppdajbpnhppadnnfpaabakmolcbkf)

> 📦 配套扩展，非独立客户端。实际仍需在本地运行 PaperAgent 服务端。

安装 [PaperAgent Chrome 扩展](https://chromewebstore.google.com/detail/paperagent/ojkppdajbpnhppadnnfpaabakmolcbkf) 后，在 arXiv 论文页面（`arxiv.org/abs/*`）的右侧栏 **View PDF** / **TeX Source** 下方会增加一个「在 PaperAgent 中打开」按钮。

点击按钮会：
1. 自动探测本地 PaperAgent 服务端口（默认 8686～8785）
2. 复用已有 PaperAgent 标签页（或打开新标签页），自动加载该论文并开始流式生成摘要

> 如服务端口非默认（通过 `PAPER_ADDR` 指定），可在扩展选项页中配置自定义端口。

### 自动恢复

如果论文内容因异常崩溃而丢失，打开论文或重新生成摘要时会自动从原始 arXiv URL 重新抓取内容并恢复，无需额外操作。

### 📐 arxiv2md 独立工具

PaperAgent 内置的 arXiv→Markdown 转换引擎也可以独立使用。两种路径：

- **HTML 优先**：从 arXiv HTML 提取内容，MathML 转 `$...$` 行内公式，表格保持 Markdown 对齐格式
- **TeX 备选**：下载 e-print tar.gz，自动展开 `\input`，`\begin{tabular}` 转 Markdown 表格

```bash
# 编译
just arxiv2md

# 使用：arXiv URL → 干净 Markdown
./arxiv2md https://arxiv.org/abs/2503.12345 > paper.md
```

## 配置

支持两种配置方式，按需选用即可：

### 1. 直接编辑配置文件

`~/.config/paperagent/config.yaml` 是主配置文件：

```yaml
api:
  base_url: "https://api.openai.com/v1"
  api_key: "${OPENAI_API_KEY}"
  default_model: "gpt-4o"
obsidian:
  vault_path: "~/Documents/Obsidian/MyVault"
  export_folder: "Papers"
ui:
  max_recent_rounds: 5
feishu:
  enabled: true
  app_id: "cli_xxxxx"
  app_secret: "xxxxx"
```

支持 `${ENV_VAR}` 语法引用环境变量，避免明文写入敏感信息：

```bash
export OPENAI_API_KEY="sk-..."
export OPENAI_BASE_URL="https://api.openai.com/v1"
```

然后在 `config.yaml` 中用 `api_key: "${OPENAI_API_KEY}"` 引用即可。

自定义 Prompt 也属于文件配置的一部分，在 `~/.config/paperagent/prompts/` 下放置同名文件即可覆盖内置模板：

- `heavy.txt` — 初始深度摘要的 system prompt
- `light.txt` — 问答阶段的 system prompt
- `summarize.txt` — 对话元总结的 system prompt

### 2. Web UI 设置页面

程序运行后打开浏览器，点击左下角设置图标，可以在 Web 界面中直接修改 API 配置（API Key、Base URL、模型等），修改即时生效。

### 飞书 Bot（可选）

在配置中启用飞书后，可在飞书群聊/私聊中使用斜杠命令操作：

| 命令 | 功能 |
|---|---|
| `/new <url>` | 创建论文总结（流式卡片实时更新） |
| `/list` | 论文列表（分页卡片 + 搜索高亮 + 翻页导航） |
| `/search <关键词>` | 按标题搜索论文，结果格式同 `/list` |
| `/summary` | 拉取当前论文初始总结 |
| `/fetch [n]` | 拉取最近 n 轮问答（默认 2） |
| `/btw <问题>` | 提问但不记入上下文 |
| `/help` | 帮助信息 |

直接发消息即可对当前论文多轮 Q&A。配置保存后自动热加载，无需重启。

**飞书开放平台配置要求**：
- 开启机器人能力
- 权限：`im:message`、`im:message:send_as_bot`
- 事件订阅：`im.message.receive_v1`、`card.action.trigger`

## 使用

### 启动

```bash
# 启动 Web UI（默认）：浏览器自动打开，端口被占用时自动 +1
paperagent

# 开发模式：Go 后端 + Vite HMR 前端，浏览器访问 http://localhost:5173
just dev

# 也可指定监听地址
PAPER_ADDR=":9000" paperagent
```

### Web UI 操作

| 操作 | 方式 |
|---|---|
| 新建论文 | 点击左侧"+"按钮，输入 arXiv URL |
| 快捷新建 | 直接粘贴 arXiv 链接到底部输入框，按 Enter 自动创建 |
| 搜索论文 | 点击🔍按钮或按 `Cmd+F`，实时过滤论文列表 |
| 选择论文 | 点击左侧论文列表 |
| 提问 | 底部输入框输入问题，Enter 发送 |
| 换行 | Shift+Enter |
| 命令 | 输入 `/` 触发命令补全 |
| `/export` | 导出当前论文到 Obsidian |
| `/config` | 打开设置（主题切换） |
| `/btw <问题>` | 提问但不记入上下文 |
| 复制原文 | 鼠标悬浮在 AI 回复上，点击 📋 按钮复制原始 Markdown |
| `/help` | 显示帮助 |

> **自动恢复**：关闭 PaperAgent 后重新打开，自动恢复到最后阅读的论文。

> **注意**：只支持通过 arXiv 链接加载论文。

## 架构

```
┌────────────────────────────────────────────────────┐
│                  浏览器 (React SPA)                  │
│  PaperList │ ChatView │ InputBox │ NewPaperDialog  │
│  ───────────────────────────────────────────────── │
│  Zustand (状态管理) │ TanStack Query (数据请求)    │
│  react-markdown │ KaTeX │ rehype-highlight        │
└──────────────────┬─────────────────────────────────┘
                   │ HTTP (JSON / SSE)
┌──────────────────▼─────────────────────────────────┐
│              Go HTTP 服务器 (:8686)                  │
│  ┌─────────────┐ ┌──────────┐ ┌──────────────────┐ │
│  │ REST API    │ │ SSE      │ │ 静态资源 (embed) │ │
│  │ /api/papers │ │ /chat    │ │ React SPA        │ │
│  │ /api/config │ │ /retry   │ │ 内嵌在二进制中   │ │
│  └──────┬──────┘ └──────────┘ └──────────────────┘ │
└─────────┼──────────────────────────────────────────┘
          │
┌─────────▼──────────────────────────────────────────┐
│             内部模块                                 │
│  ┌──────────┐ ┌──────────┐ ┌────────────────────┐ │
│  │ session  │ │ api      │ │ prompt             │ │
│  │ 持久化   │ │ 流式调用 │ │ 模板 (//go:embed)  │ │
│  │ JSON文件 │ │ OpenAI   │ │ + 用户覆盖         │ │
│  └──────────┘ └──────────┘ └────────────────────┘ │
│  ┌──────────┐ ┌──────────┐ ┌────────────────────┐ │
│  │ urlparse │ │ export   │ │ config             │ │
│  │ 加载论文 │ │ Obsidian │ │ 三层叠加配置       │ │
│  └──────────┘ └──────────┘ └────────────────────┘ │
└────────────────────────────────────────────────────┘
```

### 核心设计：两阶段状态机

**INIT 阶段**：论文全文 + `heavy.txt` prompt → 流式生成深度 Markdown 摘要。同时异步用轻量模型提取论文标题。

**CHAT 阶段**：每次提问 = 论文全文 + `light.txt` prompt + 最近 N 轮问答（默认 5 轮）。回复流式渲染。每轮异步生成一句话摘要（用于列表导航）。

**BTW 模式**：使用 `/btw <问题>` 提问时，该轮问答虽然正常显示，但 `skip_context` 标记为 `true`，不会进入后续对话的上下文。适合问一些辅助性的问题（如术语解释、公式推导），不会干扰主线的上下文连贯性。

**关键原则**：论文全文始终在 L1 上下文中。初始摘要和 BTW 轮次不在 Chat 阶段重复发送。只有最近 N 轮非 BTW 问答在 L2 上下文中。这保证了即使对话进行到第 50 轮，AI 依然准确地"记得"论文内容，而不是被聊天记录淹没。

### API 端点

| 端点 | 方法 | 说明 |
|---|---|---|
| `/api/papers` | POST | 新建论文（URL -> 抓取 -> INIT 摘要流式输出 via SSE） |
| `/api/papers` | GET | 论文列表 |
| `/api/papers/{id}` | GET | 获取论文详情 + 对话历史 |
| `/api/papers/{id}` | DELETE | 删除论文 |
| `/api/papers/{id}/chat` | POST | 提问（SSE 流式回复） |
| `/api/papers/{id}/retry-summary` | POST | 重新生成/续写摘要（SSE） |
| `/api/papers/{id}/chat/{n}/retry` | POST | 重新生成第 n 轮回答（SSE） |
| `/api/papers/{id}/export` | POST | 导出到 Obsidian |
| `/api/papers/{id}/rounds/{n}` | DELETE | 删除指定轮次 |
| `/api/config` | GET | 查看配置 |
| `/api/config` | POST | 更新配置 |

所有流式端点使用 Server-Sent Events (SSE)，事件类型：`created` / `chunk` / `done` / `error` / `title`。

### 数据持久化

每篇论文以 JSON 文件保存在 `~/.config/paperagent/papers/{uuid}.json`，包含完整论文内容、摘要、对话历史。支持 UUID 会话 ID 和旧版数字 ID 向后兼容。

## 开发

```bash
# 完整构建（前端 + Go）
just build

# 开发模式（热更新）
just dev

# 仅构建 Go
just build-go

# arxiv2md 独立工具
just arxiv2md

# 代码检查
just vet
just typecheck   # 前端 TypeScript 检查

# 测试
go test ./... -v

# 清理
just clean
```

开发模式下，前端通过 Vite 代理请求到 Go 后端，实现前后端独立热更新。

> **提示**：开发模式自动设置 `PAPER_NO_BROWSER=1`（禁止自动打开浏览器）和 `PAPER_FOREGROUND=1`（禁止后台运行）。手动启动时也可通过这两个环境变量控制行为。

## 技术栈

| 层 | 技术 |
|---|---|
| 前端 UI | React 19, TypeScript, Tailwind CSS 4 |
| 状态管理 | Zustand 5 |
| 数据请求 | TanStack React Query 5 |
| Markdown 渲染 | react-markdown + remark-math + rehype-katex + rehype-highlight |
| 图标 | Lucide React |
| 后端 | Go 1.25+ (net/http, embed) |
| SSE 流式 | Server-Sent Events |
| LLM API | OpenAI 兼容接口 |
| 持久化 | JSON 文件 (~/.config/paperagent/papers/) |
| 飞书 | larksuite/oapi-sdk-go v3, WebSocket + 交互卡片 |


## 许可

MIT
