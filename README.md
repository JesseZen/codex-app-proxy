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
| **Module** | Worker feature module (see [Modules](#modules) below) |

Each Worker is bound to one Upstream. You can run multiple Workers pointing to different Upstreams on different ports simultaneously.

### Modules

| Module | Description |
|--------|-------------|
| `config_patch` | Auto-modify `~/.codex/config.toml` to point Codex at the Worker |
| `image_filter` | Filter `image_generation` tool calls |
| `api_translate` | Chat Completions ↔ Responses API translation |
| `model_override` | Override the `model` field in requests via `params.model` |
| `request_log` | Log request method + path to stderr |
| `debug_sse` | Log SSE chunk statistics to stderr |

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
# Edit ${HOME}/.codex-proxy/config.yaml to set workers and upstreams
```

### Run

```bash
./codex-proxy
```

This single command starts the Manager → starts all Workers → starts the TUI.

### Development Mode (Frontend/Backend Separated)

```bash
# Terminal 1: Backend only (default manager-port is 9090)
./codex-proxy --config config.yaml --manager-port 9090 &

# Terminal 2: TUI with hot reload
bun install  # Install dependencies from project root (required first time)
cd tui && CODEX_PROXY_URL=http://localhost:9090 bun run dev
```

## TUI Operations

After launching, you'll see an empty screen with an input bar at the bottom. Type `/` to open the command selector with fuzzy search.

### Command List

| Command | Alias | Description |
|---------|-------|-------------|
| `/help` | | Show all commands |
| `/status` | | View worker status details |
| `/config` | | View config status (generation, dirty, save to disk) |
| `/workers` | | Manage workers (create, edit log_level) |
| `/upstream` | | Manage upstreams (create, edit base_url/api_key/api_format) |
| `/modules` | | Manage worker modules (enable/disable per worker) |
| `/logs` | | View Worker logs |
| `/launch` | | Launch Codex CLI through a cli-role worker |
| `/exit` | `/quit` `/q` | Exit |

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

Leaving `api_format` empty or unset = native passthrough, no translation.

`role` defaults to `"cli"`; workers with `role: app` are filtered out of the `/launch` picker. `log_level` defaults to `"simple"`;

### API Key Resolution

For each upstream named `<NAME>`, the environment variable `<NAME>_API_KEY` is checked first (e.g. `JOYCODE_API_KEY`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY`). If the env var is set and non-empty, it overrides the `api_key` in the config file.

## Testing

```bash
# Go backend
go test ./...

# TUI
cd tui && bun test --timeout 30000

# Type checking
cd tui && bun run typecheck
```

## Subcommands

```bash
./codex-proxy version           # Show version
./codex-proxy worker ...        # Worker process (auto-started by Manager, no need to run manually)
./codex-proxy launch --worker <port> [--profile <name>] [--cd <dir>] [--add-dir <dir>] [--model <model>]
                                # Launch Codex CLI connected to a worker
```

## TODO

- [ ] hosted-terminal: one external terminal host using `tmux` or a similar multiplexer; CAP handles `create` / `list` / `attach` / `switch`
- [ ] embedded-terminal: built-in PTY sessions inside CAP with direct session switching

Planned order: `hosted-terminal` first, then `embedded-terminal`.

## License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.

## Attribution

This project is a customized fork of [opencode](https://github.com/anomalyco/opencode) by [anomalyco](https://github.com/anomalyco), used under the [MIT License](https://github.com/anomalyco/opencode/blob/main/LICENSE).

The original opencode source code has been modified to serve as a local proxy manager for Codex App.
