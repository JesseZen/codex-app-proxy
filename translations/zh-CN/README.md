# codex-app-proxy

**[English](../../README.md)** | 中文

Codex App 的本地代理管理器。一个二进制文件即可启动管理器 + 工作进程 + TUI。

## 架构

```
Codex App / CLI
      │
      ▼
┌──────────┐
│  Worker  │  ← 监听本地端口，转发请求到上游
│  (proxy) │  ← 过滤 image_generation、Chat Completions 翻译等
└──────────┘
      │
      ▼
┌──────────┐
│ Upstream │  ← 上游 API 服务（OpenAI、OpenRouter、Groq 等）
└──────────┘

┌──────────┐
│ Manager  │  ← 管理 Worker 生命周期，提供 HTTP API + SSE 事件流
│          │  ← TUI 通过 API 与 Manager 通信
└──────────┘
      │
      ▼
┌──────────┐
│   TUI    │  ← OpenTUI (SolidJS) 终端界面
│(OpenTUI) │  ← 对话式交互，输入 / 触发命令
└──────────┘
```

### 核心概念

| 概念 | 描述 |
|------|------|
| **Manager** | 中心管理器 — 启动/停止 Worker，提供 HTTP API，TUI 连接至它 |
| **Worker** | 监听某个端口的本地代理进程，将请求转发到指定的上游服务 |
| **Upstream** | 上游 API 服务配置（base_url, api_key, api_format） |
| **Module** | Worker 功能模块（见下方 [模块](#模块) 表） |

每个 Worker 绑定一个 Upstream。你可以同时在不同的端口上运行多个指向不同 Upstream 的 Worker。

### 模块

| 模块 | 描述 |
|------|------|
| `config_patch` | 自动修改 `~/.codex/config.toml`，将 Codex 指向该 Worker |
| `image_filter` | 过滤 `image_generation` 工具调用 |
| `api_translate` | Chat Completions ↔ Responses API 翻译 |
| `model_override` | 通过 `params.model` 覆盖请求中的 `model` 字段 |
| `request_log` | 将请求方法 + 路径记录到 stderr |
| `debug_sse` | 将 SSE 分块统计信息记录到 stderr |

## 构建与运行

### 前置条件

- Go 1.26+
- Bun 1.2+（用于 TUI）

### 构建

```bash

# 安装 TUI 依赖项
bun install

# 构建 Go 二进制文件
go build -o codex-proxy .

```

### 配置

```bash
mkdir -p ${HOME}/.codex-proxy

cp config.example.yaml ${HOME}/.codex-proxy/config.yaml
# 编辑 ${HOME}/.codex-proxy/config.yaml 以设置 workers 和 upstreams
```

### 运行

```bash
./codex-proxy
```

这条命令会启动 Manager → 启动所有 Worker → 启动 TUI。

### 开发模式（前后端分离）

```bash
# 终端 1：仅后端（默认 manager-port 为 9090）
./codex-proxy --config config.yaml --manager-port 9090 &

# 终端 2：带热重载的 TUI
bun install  # 从项目根目录安装依赖（首次需要）
cd tui && CODEX_PROXY_URL=http://localhost:9090 bun run dev
```

## TUI 操作

启动后，你会看到一个空白屏幕，底部有一个输入栏。输入 `/` 即可打开带模糊搜索的命令选择器。

### 命令列表

| 命令 | 别名 | 描述 |
|------|------|------|
| `/help` | | 显示所有命令 |
| `/status` | | 查看 Worker 状态详情 |
| `/config` | | 查看配置状态（generation、dirty、保存到磁盘） |
| `/workers` | | 管理 Worker（创建、编辑 log_level） |
| `/upstream` | | 管理 Upstream（创建、编辑 base_url/api_key/api_format） |
| `/modules` | | 管理 Worker 模块（按 Worker 启用/禁用） |
| `/logs` | | 查看 Worker 日志 |
| `/launch` | | 通过 cli 角色 Worker 启动 Codex CLI |
| `/exit` | `/quit` `/q` | 退出 |

### 键盘快捷键

| 按键 | 操作 |
|------|------|
| `Ctrl+C` | 清除输入；按两次退出 |
| `Shift+Enter` | 输入中换行 |
| `↑` `↓` | 列表导航 |
| `Enter` | 确认选择 |
| `Esc` | 取消/返回 |

## 配置文件格式

```yaml
# Log directory
defaults:
  log_dir: ~/.codex-proxy/logs

# Worker definitions
workers:
  codex-app:              # Worker name
    port: 6767            # Local listen port
    upstream: joycode     # Bound Upstream
    role: cli             # "cli" (default) or "app"
    log_level: simple     # "simple" or "detail"
    modules:
      config_patch:       # Auto-modify ~/.codex/config.toml
        enabled: true
        config_path: ~/.codex/config.toml
      image_filter:       # Filter image_generation tool
        enabled: true
      api_translate:      # Chat Completions ↔ Responses API translation
        enabled: true

# Upstream definitions
upstreams:
  joycode:
    base_url: https://api.joycode.dev/v1
    api_key: sk-...                   # Plain key in config is supported
    api_format: chat_completions       # Requires Chat Completions translation

  openrouter:
    base_url: https://openrouter.ai/api/v1
    api_key: sk-...
    api_format: chat_completions

  openai:
    base_url: https://api.openai.com/v1
    api_key: sk-...                    # Plain key is supported
    # <UPSTREAM_NAME>_API_KEY env var wins over config if set (e.g. OPENAI_API_KEY)
    # No api_format = native Responses API passthrough
```

将 `api_format` 留空或不设置 = 原生透传，不进行翻译。

`role` 默认为 `"cli"`；`role: app` 的 Worker 不会出现在 `/launch` 选择器中。`log_level` 默认为 `"simple"`。

### API Key 解析

对于名为 `<NAME>` 的每个 upstream，会先检查环境变量 `<NAME>_API_KEY`（例如 `JOYCODE_API_KEY`、`OPENAI_API_KEY`、`OPENROUTER_API_KEY`）。如果该环境变量已设置且非空，它将覆盖配置文件中的 `api_key`。

## 测试

```bash
# Go 后端
go test ./...

# TUI
cd tui && bun test --timeout 30000

# 类型检查
cd tui && bun run typecheck
```

## 子命令

```bash
./codex-proxy version           # 显示版本
./codex-proxy worker ...        # Worker 进程（由 Manager 自动启动，无需手动运行）
./codex-proxy launch --worker <port> [--profile <name>] [--cd <dir>] [--add-dir <dir>] [--model <model>]
                                # 启动连接到 Worker 的 Codex CLI
```

## 待办事项

- [ ] hosted-terminal: 使用 `tmux` 或类似多路复用器作为外部终端主机；CAP 处理 `create` / `list` / `attach` / `switch`
- [ ] embedded-terminal: 在 CAP 内部内置 PTY 会话，支持直接会话切换

计划顺序：先实现 `hosted-terminal`，然后是 `embedded-terminal`。

## 许可证

本项目基于 MIT 许可证授权 — 详情请参阅 [LICENSE](../../LICENSE) 文件。

## 致谢

本项目是 [anomalyco](https://github.com/anomalyco) 开发的 [opencode](https://github.com/anomalyco/opencode) 的定制分支，在 [MIT 许可证](https://github.com/anomalyco/opencode/blob/main/LICENSE) 下使用。

原始 opencode 源代码已被修改，以作为 Codex App 的本地代理管理器。

---

<!-- CO-OP TRANSLATOR DISCLAIMER START -->
**免责声明**：
本文件由 AI 翻译服务 [Co-op Translator](https://github.com/Azure/co-op-translator) 翻译完成。尽管我们力求准确，但请注意，自动翻译可能包含错误或不准确之处。原始语言版文件应视为权威来源。对于重要信息，建议使用专业人工翻译。我们对因使用本翻译而产生的任何误解或误释不承担责任。
<!-- CO-OP TRANSLATOR DISCLAIMER END -->