# codex-app-proxy

**English** | [中文](./README.md)

一个面向 Codex App 的本地代理管理器。单个二进制文件启动 Manager + Workers + TUI。

## 架构

```
Codex App / CLI
      │
      ▼
┌──────────┐
│  Worker  │  ← Listens on a local port, forwards requests to upstream
│  (proxy) │  ← Filters image_generation, Chat Completions translation, etc.
└──────────┘
      │
      ▼
┌──────────┐
│ Upstream │  ← Upstream API service (OpenAI, OpenRouter, Groq, etc.)
└──────────┘

┌──────────┐
│ Manager  │  ← Manages Worker lifecycle, exposes HTTP API + SSE event stream
│          │  ← TUI communicates with Manager via API
└──────────┘
      │
      ▼
┌──────────┐
│   TUI    │  ← OpenTUI (SolidJS) terminal interface
│(OpenTUI) │  ← Conversational interaction, type / to trigger commands
└──────────┘
```

### 核心概念

| 概念 | 描述 |
|---------|-------------|
| **Manager** | 中心管理器 — 启动/停止 Workers，提供 HTTP API，TUI 连接到它 |
| **Worker** | 监听某个端口的本地代理进程，将请求转发到指定的 Upstream |
| **Upstream** | 上游 API 服务配置（base_url，api_key，api_format） |
| **Module** | Worker 功能模块：`config_patch`（自动修改 Codex 配置），`image_filter`（过滤图像生成），`api_translate`（聊天补全翻译） |

每个 Worker 绑定到一个 Upstream。你可以同时运行多个 Worker，分别指向不同端口上的不同 Upstream。

## 构建与运行

### 先决条件

- Go 1.26+
- Bun 1.2+（用于 TUI）

### 构建

```bash

# 安装TUI依赖项
bun install

# 构建Go二进制文件
go build -o codex-proxy .

```

### 配置

```bash
mkdir -p ${HOME}/.codex-proxy

cp config.example.yaml ${HOME}/.codex-proxy/config.yaml
# 编辑 ${HOME}/.codex-proxy/config.yaml 以设置工作进程和上游服务
```

### 运行

```bash
./codex-proxy
```

这个单一命令将启动 Manager → 启动所有 Workers → 启动 TUI。

### 开发模式（前后端分离）

```bash
# 终端1：仅后端
./codex-proxy --config config.yaml --manager-port 8080 &

# 终端2：TUI，支持热重载
bun install  # 从项目根目录安装依赖（首次运行时必需）
cd tui && CODEX_PROXY_URL=http://localhost:8080 bun run dev
```

## TUI 操作

启动后，你会看到一个空白屏幕，底部有一个输入栏。输入 `/` 会打开带有模糊搜索的命令选择器。

### 命令列表

| 命令 | 别名 | 描述 |
|---------|-------|-------------|
| `/help` | | 显示所有命令 |
| `/status` | | 查看 Workers、Upstreams 和配置状态 |
| `/config` | `/settings` | 修改配置（选择类别 → 对象 → 字段 → 更改值） |
| `/new` | | 创建一个新的 Worker |
| `/restart` | | 重启一个 Worker |
| `/stop` | | 停止一个 Worker |
| `/logs` | | 查看 Worker 日志 |
| `/stream` | | 切换 SSE 事件流面板 |
| `/clear` | | 清屏 |
| `/exit` | `/quit` `:q` `:wq` | 退出 |

### 键盘快捷键

| 按键 | 操作 |
|-----|--------|
| `Ctrl+C` | 清除输入；按两次退出 |
| `Shift+Enter` | 在输入中换行 |
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
    base_url: https://openapi.com/v1
    api_key: sk-...                    # Plain key is supported
    # OPENAI_API_KEY wins over config if exported
    # No api_format = native Responses API passthrough
```

将 `api_format` 留空或不设置 = 原生直通，不进行翻译。

## 测试

```bash
# Go后端
go test ./...

# TUI
cd tui && bun test

# 类型检查
cd tui && bun run typecheck
```

## 其他子命令

```bash
./codex-proxy version           # 显示版本
./codex-proxy worker ...        # 工作进程（由管理器自动启动，无需手动运行）
```

## 许可证

本项目依据 MIT 许可证授权 — 详情请参见 [LICENSE](../../LICENSE) 文件。

## 归属

本项目是 [anomalyco](https://github.com/anomalyco) 的 [opencode](https://github.com/anomalyco/opencode) 的一个定制化分支，依据 [MIT 许可证](https://github.com/anomalyco/opencode/blob/main/LICENSE) 使用。

原始的 opencode 源代码已被修改，以作为 Codex App 的本地代理管理器。

---

<!-- CO-OP TRANSLATOR DISCLAIMER START -->
**免责声明**：
本文件由 AI 翻译服务 [Co-op Translator](https://github.com/Azure/co-op-translator) 翻译完成。尽管我们力求准确，但请注意，自动翻译可能包含错误或不准确之处。原始语言版文件应视为权威来源。对于重要信息，建议使用专业人工翻译。我们对因使用本翻译而产生的任何误解或误释不承担责任。
<!-- CO-OP TRANSLATOR DISCLAIMER END -->