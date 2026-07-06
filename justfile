# PaperAgent 项目 justfile
# 列出所有 recipe: just --list

bin := "paperagent"
gocache := "/tmp/gocache-" + env_var_or_default("USER", "paperagent")

# ---- 默认 ----
default:
    @just --list

# ---- 开发 ----

# 开发模式：同时启动后端（Go）和前端（Vite HMR）
# 前后端日志合并到一个终端，Go=青色，Vite=绿色
# Ctrl+C 同时退出两个服务。浏览器访问 http://localhost:5173
dev: install build-go
    PAPER_FOREGROUND=1 PAPER_NO_BROWSER=1 npx concurrently -n Go,Vite -c cyan,green \
        "GOCACHE={{gocache}} ./{{bin}}" \
        "cd frontend && npm run dev -- --open"

# ---- 构建 ----

# 安装前端依赖
install:
    cd frontend && npm install

# 构建前端（输出到 internal/server/frontend-dist/）
build-frontend: install
    cd frontend && npm run build

# 完整构建（前端 + Go，产出单二进制）
build: build-frontend
    GOCACHE={{gocache}} go build -o {{bin}} .

# 仅构建 Go（跳过前端）
build-go:
    GOCACHE={{gocache}} go build -o {{bin}} .

# ---- 生产 ----

# 生产模式：单二进制（内含前端），浏览器访问 http://localhost:8686
serve: build
    ./{{bin}}

# ---- arxiv2md ----

# 编译 standalone arxiv2md 二进制
arxiv2md:
	GOCACHE={{gocache}} go build -o arxiv2md ./cmd/arxiv2md/

# ---- 代码质量 ----

# Go 代码检查
vet:
    GOCACHE={{gocache}} go vet ./...

# Go 静态分析（golangci-lint v2，需要本机先装好：brew install golangci-lint 或
# go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest）。
# 先 config verify 再 run：CI 的 golangci-lint-action 默认会跑 config verify，
# 本地也对齐、提前发现 schema 非法问题。
lint:
    golangci-lint config verify --config .golangci.yaml
    golangci-lint run --config .golangci.yaml ./...

# 前端类型检查
typecheck:
    cd frontend && npx tsc --noEmit

# ---- 清理 ----

# 清理构建产物
clean:
    rm -f {{bin}}
    rm -rf internal/server/frontend-dist

# 彻底清理（含 node_modules）
clean-all: clean
    rm -rf frontend/node_modules
