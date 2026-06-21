# codex-app-proxy

**English** | [中文](translations/zh-CN/README.md)

A local proxy manager for Codex App. A single binary launches Manager + Workers + TUI.

## Architecture

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

### Core Concepts

| Concept | Description |
|---------|-------------|
| **Manager** | Central manager — starts/stops Workers, provides HTTP API, TUI connects to it |
| **Worker** | A local proxy process listening on a port, forwarding requests to a specified Upstream |
| **Upstream** | Upstream API service config (base_url, api_key, api_format) |
| **Module** | Worker feature modules: `config_patch` (auto-modify Codex config), `image_filter` (filter image generation), `api_translate` (Chat Completions translation) |

Each Worker is bound to one Upstream. You can run multiple Workers pointing to different Upstreams on different ports simultaneously.

## Build & Run

### Prerequisites

- Go 1.26+
- Bun 1.2+ (for TUI)

### Build

```bash

# Install TUI dependencies
bun install

# Build Go binary
go build -o codex-proxy .

```

### Configuration

```bash
mkdir -p ${HOME}/.codex-proxy

cp config.example.yaml ${HOME}/.codex-proxy/config.yaml
# Edit ${HOME}/.codex-proxy/config.yaml to set workers and providers
```

### Run

```bash
./codex-proxy
```

This single command starts the Manager → starts all Workers → starts the TUI.

### Development Mode (Frontend/Backend Separated)

```bash
# Terminal 1: Backend only
./codex-proxy --config config.yaml --manager-port 8080 &

# Terminal 2: TUI with hot reload
bun install  # Install dependencies from project root (required first time)
cd tui && CODEX_PROXY_URL=http://localhost:8080 bun run dev
```

## TUI Operations

After launching, you'll see an empty screen with an input bar at the bottom. Type `/` to open the command selector with fuzzy search.

### Command List

| Command | Alias | Description |
|---------|-------|-------------|
| `/help` | | Show all commands |
| `/status` | | View Workers, Upstreams, and config status |
| `/config` | `/settings` | Modify config (select category → object → field → change value) |
| `/new` | | Create a new Worker |
| `/switch` | | Switch a Worker's Upstream |
| `/restart` | | Restart a Worker |
| `/stop` | | Stop a Worker |
| `/logs` | | View Worker logs |
| `/stream` | | Toggle SSE event stream panel |
| `/clear` | | Clear screen |
| `/exit` | `/quit` `:q` `:wq` | Exit |

### Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `Ctrl+C` | Clear input; press twice to exit |
| `Shift+Enter` | New line in input |
| `↑` `↓` | List navigation |
| `Enter` | Confirm selection |
| `Esc` | Cancel/Go back |

## Configuration File Format

```yaml
# Log directory
defaults:
  log_dir: ~/.codex-proxy/logs

# Worker definitions
workers:
  codex-app:              # Worker name
    port: 6767            # Local listen port
    provider: joycode     # Bound Upstream
    modules:
      config_patch:       # Auto-modify ~/.codex/config.toml
        enabled: true
        config_path: ~/.codex/config.toml
      image_filter:       # Filter image_generation tool
        enabled: true
      api_translate:      # Chat Completions ↔ Responses API translation
        enabled: true

# Upstream definitions
providers:
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

Leaving `api_format` empty or unset = native passthrough, no translation.

## Testing

```bash
# Go backend
go test ./...

# TUI
cd tui && bun test

# Type checking
cd tui && bun run typecheck
```

## Other Subcommands

```bash
./codex-proxy version           # Show version
./codex-proxy worker ...        # Worker process (auto-started by Manager, no need to run manually)
```

## License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.

## Attribution

This project is a customized fork of [opencode](https://github.com/anomalyco/opencode) by [anomalyco](https://github.com/anomalyco), used under the [MIT License](https://github.com/anomalyco/opencode/blob/main/LICENSE).

The original opencode source code has been modified to serve as a local proxy manager for Codex App.
