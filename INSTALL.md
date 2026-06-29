# PaperAgent 安装指南（AI Agent 专用）

> 本文档面向**具备 shell 执行能力**的 AI Coding Agent（Claude Code、Cursor、Aider 等）。请按顺序逐节执行，每节都附有验证步骤，不要跳步。

## 前置假设

在开始之前，请确认你（Agent）具备以下能力：

1. **shell 执行** —— 能运行 `bash` / `zsh` 命令
2. **文件读写** —— 能写本地文件（配置、临时脚本）
3. **网络访问** —— 能 `curl` GitHub Releases
4. **可询问用户** —— 能停下来问用户拿 API key / 飞书凭证等敏感信息

**不要**：把任何 API key / secret 写入对话日志、写入 git 仓库、写到临时文件后未清理。所有敏感输入都应直接写入 `~/.config/paperagent/config.yaml`。

---

## 1. 安装二进制

运行下面这个一行命令即可。脚本自动处理：OS/arch 检测、从 GitHub Releases 下载最新版本、macOS Gatekeeper 隔离属性、Linux 缺失库检测、PATH 警告。

```bash
curl -sSfL https://raw.githubusercontent.com/happyTonakai/PaperAgent/main/install.sh | sh
```

可选参数：

| 场景 | 命令 |
|---|---|
| 指定版本 | `... \| VERSION=v1.2.0 sh` |
| 自定义安装路径 | `... \| INSTALL_DIR=/usr/local/bin sh`（全局安装需要 sudo） |

**Agent 不要主动跑 sudo** —— 让用户自己输密码。

**Windows 用户**：本脚本不支持 Windows（mingw/msys 环境会被拒绝）。请去 [Releases 页面](https://github.com/happyTonakai/PaperAgent/releases/latest) 手动下载 `paperagent_windows_amd64.exe`。

**验证**：

```bash
command -v paperagent && paperagent -version
```

如果 `command -v paperagent` 找不到，说明 `$HOME/.local/bin` 不在 PATH 里，install.sh 已经提示过，把对应 export 加进 `~/.zshrc` / `~/.bashrc` 后 `source` 一下即可。

如果脚本里检测到 Linux 缺失库或下载失败，会在 stderr 给出明确错误信息和排查指引。

---

## 2. 模块 A — Q&A 系统（可跳过）

### 2.1 询问是否启用

用 `AskUserQuestion` 询问：

> **是否启用 Q&A 系统？**
>
> - 启用：继续 2.2 节索取配置
> - 跳过：跳到第 3 节

**记录状态**：维护内部 `ENABLED_MODULES` 集合。选启用 → 加入 `"qa"`；选跳过 → 不加入。

### 2.2 索取配置（仅当启用）

向用户索取以下信息（**用 `AskUserQuestion` 或类似机制**，不要 echo 完整 key 到日志）：

| 字段 | 说明 | 示例 |
|---|---|---|
| API base URL | OpenAI 兼容接口 | `https://api.openai.com/v1` |
| API key 形式 | 二选一 —— **推荐用 `${VAR}` 引用形式**（跟 README 一致，写盘安全、不依赖用户 Shell）；也可直接填明文（PaperAgent **启动时**检测到明文会主动用 AES-256-GCM 加密写回磁盘） | `${OPENAI_API_KEY}` 或 `sk-...` |
| 环境变量名（仅当用引用形式时填） | 用户在 Shell 里 export 用的变量名 | `OPENAI_API_KEY` |
| 默认模型 | 任意兼容模型 | `gpt-4o`、`deepseek-chat` |
| Token 上限（可选，回车跳过用默认 30000） | 触发截断的 token 预算 | `30000` |
| 保留轮数（可选，回车跳过用默认 2） | 截断后最少保留的轮数 | `2` |

**记录配置**：把用户给的 5 个值暂存到内部变量（如 `$QA_BASE_URL` / `$QA_API_KEY` / `$QA_MODEL` / `$QA_MAX_TOKENS` / `$QA_MIN_ROUNDS`），**第 5 节统一写盘**。

---

## 3. 模块 B — 每日推荐（可跳过）

### 3.1 询问是否启用

用 `AskUserQuestion` 询问：

> **是否启用每日推荐？**
>
> - 启用：继续 3.2 节
> - 跳过：跳到第 4 节
>
> 启用后请提供：
>
> - 想订阅的 arXiv 分类（如 `cs.AI, cs.CL`）
> - 推荐时间（默认 `08:00`）
> - 每天推荐数量（默认 20）
> 

**记录状态**：选启用 → 把 `"recommend"` 加入 `ENABLED_MODULES`；选跳过 → 不加入。

### 3.2 索取配置（仅当启用）

| 字段 | 说明 | 默认 |
|---|---|---|
| arXiv 分类 | 逗号分隔 | 必填 |
| 推荐时间 | `HH:MM` | `08:00` |
| 每天推荐数量 | 整数 | `20` |
| 探索比例 `diversity_ratio` | 0-1 | `0.3` |
| 翻译推荐论文 | 是否用主 API 翻译标题/摘要（推荐 tab 里的复选框） | 不勾选 = 不翻译 |

**记录配置**：暂存到 `$REC_CATEGORIES` / `$REC_TIME` / `$REC_DAILY` / `$REC_DIVERSITY` / `$REC_TRANSLATE`。<br>排除关键词在 §3.3 收集偏好时一起提取，暂存到 `$REC_EXCLUDED_KEYWORDS`。

### 3.3 收集研究偏好

用 `AskUserQuestion`（长文本输入）询问：

> **说说你的研究方向。**
>
> 随便聊聊你关注什么领域、方法、技术，以及对什么不感兴趣。不限格式。
> 例如：
> ```
> 我做 LLM 推理优化和 KV cache 压缩，对 MoE 和 speculative decoding 也感兴趣。
> 不太想看联邦学习和区块链相关的论文。
> ```

用户输入后，用 `AskUserQuestion`（是/否）确认：

> **已记录你的描述：**
> ```
> {用户输入}
> ```
> **以上描述是否准确？**
> - 是 → 继续
> - 否 → 回到上一步重新输入

确认后，做两件事：

1. **写入 `preferences.md`** — 把用户输入写成 Markdown 暂存到 `$PREFERENCES_CONTENT`：

   ```markdown
   ## 感兴趣的主题
   - {用户输入}

   ## 备注
   - 安装时首次配置
   ```

2. **提取排除关键词** — 从用户输入中人工挑出方向性技术词（用户说不感兴趣的部分），**关键词必须用英文**，暂存到 `$REC_EXCLUDED_KEYWORDS`（逗号分隔，可选）。<br>例如用户说「不太想看联邦学习和区块链」，则 `$REC_EXCLUDED_KEYWORDS="federated learning, blockchain"`。

> 如果用户跳过（空输入），则 `$PREFERENCES_CONTENT` 和 `$REC_EXCLUDED_KEYWORDS` 都留空。系统使用空偏好启动（推荐退化为按时间倒序选未读论文）。

---

## 4. 模块 C — 飞书推送（可跳过）

### 4.1 询问是否启用

用 `AskUserQuestion` 询问：

> **是否启用飞书机器人 / 每日推荐推送？**
>
> - 启用：继续 4.2 节
> - 跳过：跳到第 5 节
>
> 启用前请准备好：
>
> - 飞书应用 `App ID`（格式 `cli_xxxxx`）
> - 飞书应用 `App Secret`
> - 每日推荐推送目标会话 `chat_id`（格式 `oc_xxxxx`）
>
> 还需要在飞书开放平台给应用开通权限：`im:message`、`im:message:send_as_bot`、`card.action.trigger`。

**记录状态**：选启用 → 把 `"feishu"` 加入 `ENABLED_MODULES`；选跳过 → 不加入。

### 4.2 索取配置（仅当启用）

| 字段 | 说明 | 示例 |
|---|---|---|
| App ID | 飞书应用标识 | `cli_xxxxx` |
| App Secret | **直接填真实 secret**，跟 API key 一样会加密写盘 | `xxxxx` |
| 每日推荐群 ID | 推送目标群 | `oc_xxxxx` |

**记录配置**：暂存到 `$FEISHU_APP_ID` / `$FEISHU_APP_SECRET` / `$FEISHU_CHAT_ID`。

---

## 5. 写配置

### 5.1 备份现有配置

```bash
CONFIG_DIR="$HOME/.config/paperagent"
mkdir -p "$CONFIG_DIR"
chmod 700 "$CONFIG_DIR"

[ -f "$CONFIG_DIR/config.yaml" ] && cp "$CONFIG_DIR/config.yaml" "$CONFIG_DIR/config.yaml.bak.$(date +%s)"
```

### 5.2 合并写入 config.yaml

**只写入 `ENABLED_MODULES` 里的模块对应的段**。用 heredoc 写入整个文件（如果之前没备份过，需要先读出现有内容合并；如果备份过，直接覆盖）。

**通用骨架**（永远写入）：

```yaml
ui:
  min_recent_rounds: 2
  max_input_tokens: 30000
```

**Q&A 段**（仅当 `qa` ∈ `ENABLED_MODULES`）：

```yaml
api:
  base_url: "<QA_BASE_URL>"
  api_key: "${<QA_API_KEY_VAR>}"   # 推荐形式；用户传明文则替换为 <QA_API_KEY>
  default_model: "<QA_MODEL>"
```

**推荐段**（仅当 `recommend` ∈ `ENABLED_MODULES`）：

```yaml
arxiv_categories:
  - "cs.AI"
  - "cs.CL"
  # ... 用户给的分类

recommend:
  daily_papers: 20
  scoring_batch_size: 10
  diversity_ratio: 0.3
  scheduled_time: "08:00"
  excluded_keywords:
    # 关键词必须用英文
    # 拼接时：如果 $REC_EXCLUDED_KEYWORDS 非空，每行 - kw；否则留空或注释
  push_to_feishu: <true|false>   # 如果 feishu 也启用则 true
  enable_translation: <true|false>  # 是否翻译推荐论文标题/摘要
```

**飞书段**（仅当 `feishu` ∈ `ENABLED_MODULES`）：

```yaml
feishu:
  enabled: true
  app_id: "<FEISHU_APP_ID>"
  app_secret: "<FEISHU_APP_SECRET>"
  daily_recommend_chat_id: "<FEISHU_CHAT_ID>"
```

**写入命令**（把上面的所有段按 `ENABLED_MODULES` 拼接成一个 heredoc 写盘，**用 `<<EOF` 不要用 `<<'EOF'`**，确保 shell 变量能展开）：

```bash
cat > "$CONFIG_DIR/config.yaml" <<EOF
<拼接后的完整 yaml，变量已展开>
EOF
chmod 600 "$CONFIG_DIR/config.yaml"
```

### 5.3 写入研究偏好（仅当 `recommend` ∈ `ENABLED_MODULES` 且用户填了偏好）

如果 `$PREFERENCES_CONTENT` 非空，写入 `preferences.md`：

```bash
if [ -n "$PREFERENCES_CONTENT" ]; then
  cat > "$CONFIG_DIR/preferences.md" <<EOF
$PREFERENCES_CONTENT
EOF
  echo "→ 研究偏好已写入 $CONFIG_DIR/preferences.md"
fi
```

**验证**：

```bash
ls -la "$CONFIG_DIR/config.yaml"   # 权限应为 -rw------
cat "$CONFIG_DIR/config.yaml"      # 人工 spot-check，确认 api_key / app_secret 都在
[ -f "$CONFIG_DIR/preferences.md" ] && echo "preferences.md exists" || echo "preferences.md not created (user skipped)"
```

---

## 6. 启动 + 健康检查

**端口检查**（如果 8686~8785 都被占用，需要设 `PAPER_ADDR` 改端口，或停掉占用进程）：

```bash
for p in 8686 8785; do
  (echo > /dev/tcp/127.0.0.1/$p) 2>/dev/null && echo "port $p=OCCUPIED" || echo "port $p=FREE"
done
```

```bash
# 日志按天轮转写入 $CONFIG_DIR/logs/paperagent-YYYY-MM-DD.log，启动时自动清理 7 天前的旧文件
# stderr 仍重定向到 paperagent.out，以备 panic 诊断
nohup paperagent > "$CONFIG_DIR/paperagent.out" 2>&1 &
echo $! > "$CONFIG_DIR/paperagent.pid"

sleep 3

# 健康检查
curl -sf http://localhost:8686/api/config | head -c 200 && echo " → OK"
```

**预期**：返回 JSON 包含 `api.base_url`、`api.default_model` 等字段。

**如果失败**：

| 现象 | 排查 |
|---|---|
| `connection refused` | 端口未起 → 看 `$CONFIG_DIR/logs/paperagent-$(date +%F).log` |
| 返回 401/403 | API key 无效 → 修 §5.2 后重启 |
| `unresolved env vars` | config.yaml 里有未展开的 `${VAR}` → 提示用户在 Shell 里 export 对应的 env var 后重启服务 |
| panic in log | 贴给用户看 |

---

## 7. 验证各模块

**只验证 `ENABLED_MODULES` 里的模块**。跳过的模块不做对应验证。

### 7.1 Q&A 验证（仅当启用）

打开 Web UI（`http://localhost:8686`）粘一个 arXiv 链接，确认能流式生成摘要。

或者用 API 自检：

```bash
curl -sf -X POST http://localhost:8686/api/papers \
  -H "Content-Type: application/json" \
  -d '{"url":"https://arxiv.org/abs/1706.03762"}' | head -c 300
```

### 7.2 推荐验证（仅当启用）

立即触发一次完整推荐流水线：

```bash
curl -sf -X POST http://localhost:8686/api/recommend/trigger | head -c 300
```

**预期**：返回 JSON 含 `articles_fetched`、`recommendations_generated` 等字段。

**如果失败**：

| 现象 | 排查 |
|---|---|
| `RSS fetch failed` | 网络问题 → 提示用户检查防火墙 |

| `no categories configured` | `arxiv_categories` 为空 → 修 §5.2 |

### 7.3 飞书验证（仅当启用）

```bash
tail -50 "$CONFIG_DIR/logs/paperagent-$(date +%F).log" | grep -i "feishu\|lark\|websocket" || echo "NO_FEISHU_LOG"
```

**预期**：日志出现 `feishu bot started` 或 `lark websocket connected`。

可在飞书目标群里发 `/help`，机器人应返回命令列表。

**如果失败**：

| 现象 | 排查 |
|---|---|
| `websocket connect failed` | 网络 / 防火墙 |
| `invalid app_id` | 修 §5.2 的 `feishu.app_id` |
| `permission denied` | 没开通 `im:message` 等权限 → 提示去开放平台加 |
| 群里发命令无反应 | bot 没被邀请进群 → 提示用户在群里 `@` 一下 bot |

---

## 8. 全跳过守卫（必看）

进入本节前检查 `ENABLED_MODULES`。如果它是**空集**（三个都没启用），**停下来用 `AskUserQuestion` 警告用户**：

> ⚠️ **Q&A、每日推荐、飞书推送三项你全部跳过了 —— 装了个寂寞。**
>
> 当前的 PaperAgent 只是一个空壳二进制：没有 AI 能力、没有推荐生成、没有消息推送。运行后打开 Web UI 会看到「请先在设置页配置 API」的提示，且永远不会有推荐出现。
>
> 你想怎么办？
>
> - **补装一个或多个模块**：告诉我装哪几个，我回到 §2-§4 重新问
> - **继续这样装**：我完成当前安装，你之后想用时再补配置
> - **中止安装**：清理已下载的二进制，什么都不留

**用户必须明确选「继续这样装」**才能进入 §9 完成报告；选「补装」则回到对应模块重新执行；选「中止」则跳到 §10 卸载清理。

---

## 9. 完成报告

向用户汇总：

```
✅ PaperAgent 已就绪
   - 二进制：~/.local/bin/paperagent（默认；如用户用 INSTALL_DIR 自定义则填实际路径）
   - 配置：~/.config/paperagent/config.yaml（权限 600）
   - PID 文件：~/.config/paperagent/paperagent.pid
   - 日志：~/.config/paperagent/logs/paperagent-YYYY-MM-DD.log（按天轮转，保留 7 天）
   - Web UI：http://localhost:8686

模块状态：
   [✓/✗] Q&A 系统      — 模型 <MODEL>（如启用）
   [✓/✗] 每日推荐       — 分类 [...], 时间 HH:MM
   [✓/✗] 飞书推送       — 群 <CHAT_ID>

下一步建议：
   1. （如启用 Q&A）打开 http://localhost:8686 粘一个 arXiv 链接试试
   2. （如启用推荐）等明天 HH:MM 查看第一批推荐
   3. （如启用飞书）在群里发 /help 验证机器人
```

---

## 10. 卸载（如用户后续要求）

```bash
# 停进程
PID=$(cat "$CONFIG_DIR/paperagent.pid" 2>/dev/null)
[ -n "$PID" ] && kill "$PID" 2>/dev/null

# 删二进制：优先从 PATH 找（默认 ~/.local/bin）；找不到时问用户当初装到了哪里
PAPERAGENT_BIN="$(command -v paperagent 2>/dev/null || true)"
if [ -z "$PAPERAGENT_BIN" ]; then
  echo "⚠️  在 PATH 里找不到 paperagent。如果用户当初用了自定义 INSTALL_DIR（如 /opt/bin）且不在 PATH 里，停下来用 AskUserQuestion 问装到了哪里。" >&2
else
  rm -f "$PAPERAGENT_BIN"
fi

# 询问用户：是否删除全部数据（论文、配置、SQLite、加密 API key）
# 如果是：
rm -rf "$CONFIG_DIR"
```

---

## 故障排查速查

| 症状 | 第一时间看 |
|---|---|
| 启动失败 / panic | `~/.config/paperagent/logs/paperagent-$(date +%F).log` 及 `~/.config/paperagent/paperagent.out` |
| Web UI 打不开 | `curl -v http://localhost:8686/` |
| Q&A 无响应 | `GET /api/config` 看 api 字段 |
| 推荐没出来 | `GET /api/recommend/scheduler-status` 看 `last_run` / `last_error` / `next_run` |
| 飞书无响应 | 启动日志搜 `feishu` |
| API key 泄漏 | 立即到 OpenAI 控制台 revoke，然后改配置后重启 |

---

## 给 Agent 的备注

- **不要 echo API key / app_secret 到任何命令输出**，所有写入文件用 heredoc 或 stdin 重定向
- **每节执行完跑一次验证命令**，失败了**先诊断再继续**，不要假装成功
- **修改 config.yaml 用 heredoc 整体重写**（不是增量编辑，因为重写时 §5 已经合并了所有模块）
- **macOS Gatekeeper**（`xattr -cr`）和 **Linux 库依赖**（`ldd`）由 `install.sh` 自动处理，agent 无需再跑
- **API key 优先推荐 `${OPENAI_API_KEY}` 引用形式**，跟 README 主文档一致；写盘后磁盘上是引用形式（不加密），但启动时 PaperAgent 用 `os.ExpandEnv` 展开 → 内存中是明文。明文直接填也是合法的，**首次启动时** PaperAgent 会自动用 AES-256-GCM 加密成 `!!aes:...` 写回磁盘（不依赖后续任何 Save() 调用）。
