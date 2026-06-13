# codex-app-proxy

本地代理，用法是：

`Codex App -> http://127.0.0.1:8787 -> 你的中转站 API`

功能：

- 转发所有请求到 `BASE_URL`
- 对所有 `POST` JSON 请求过滤 `tools` 里的 `image_generation`
- 如果 `tool_choice` 明确指定了 `image_generation`，自动改成 `auto`
- 支持通过 `ACTIVE_PROVIDER` 自动切换 `.env.<provider>` 配置
- 启动时自动把 `~/.codex/config.toml` 当前 `model_provider` 对应的 `base_url` 改成 `http://127.0.0.1:PORT`
- 退出时自动把 `base_url` 恢复成 `BASE_URL`
- 可以编译成一个 macOS app，Spotlight 里直接搜 `Codex App Proxy` 启动

## 启动

Node 20+ 即可，Node 18/22/25 也可以。

```bash
cp .env.example .env
npm start
```

程序启动时会自动读取当前目录的 `.env`，不需要手动 `export`。

## 多服务商切换

推荐做法是：

- `.env` 放公共配置和当前选中的 `ACTIVE_PROVIDER`
- `.env.<provider>` 放每个服务商自己的 `BASE_URL` / `API_KEY`

例如：

`.env`

```bash
PORT=8787
CODEX_CONFIG_PATH=~/.codex/config.toml
ACTIVE_PROVIDER=openrouter
```

`.env.openrouter`

```bash
BASE_URL=https://openrouter.ai/api/v1
API_KEY=sk-or-xxx
```

`.env.openai`

```bash
BASE_URL=https://api.openai.com/v1
API_KEY=sk-xxx
```

这样 app 启动时会按 `.env` 里的 `ACTIVE_PROVIDER` 自动选中对应服务商。

切换时只需要改：

```bash
ACTIVE_PROVIDER=openai
```

启动加载顺序是：

1. shell 里显式传入的环境变量
2. `.env.<ACTIVE_PROVIDER>`
3. `.env`

也就是说，命令行临时传入的值优先级最高。

## 命令行快捷启动

除了改 `.env` 里的 `ACTIVE_PROVIDER`，也可以直接按 provider 启动：

```bash
npm run start:openai
npm run start:openrouter
npm run start:groq
```

如果你想临时用任意名字，也可以：

```bash
npm run start:provider -- openai
npm run start:provider -- myrelay
```

上面第二个例子会去读取：

```bash
.env.myrelay
```

也可以不使用 `.env`，直接：

```bash
PORT=8787 \
BASE_URL=https://your-relay.example.com \
npm start
```

启动后不需要再手动改 `~/.codex/config.toml`。

如果你的中转站要求固定 key，可以额外设置：

```bash
API_KEY=sk-xxx
```

设置后，代理会用 `Bearer <API_KEY>` 覆盖客户端传来的 `Authorization`。
这个值通常可以直接对应你 `~/.codex/config.toml` 里的 `experimental_bearer_token`。

如果同时设置了 `ACTIVE_PROVIDER` 和 `.env.<provider>`，那么这里的 `API_KEY` / `BASE_URL` 会优先取 provider 文件里的值；除非你在 shell 里又显式传了一次。

## Codex 配置

代理会自动修改 `~/.codex/config.toml` 里当前 `model_provider` 对应 section 的 `base_url`。

如果你原来上游地址是带前缀的，例如 `/v1`，那就把 `BASE_URL` 也写成带前缀的版本，例如：

```bash
BASE_URL=https://your-relay.example.com/v1
```

代理会保留请求路径和 query string，并拼到这个 base URL 后面。

默认配置文件路径是 `~/.codex/config.toml`，如果你想指定别的路径，可以设置：

```bash
CODEX_CONFIG_PATH=/path/to/config.toml
```

说明：

- 启动时会把 `base_url` 注入成 `http://127.0.0.1:PORT`
- 正常退出（如 `Ctrl+C`）时会恢复成 `BASE_URL`
- 如果进程被强制杀掉（例如 `kill -9`），来不及执行退出钩子，`base_url` 可能不会自动恢复

## macOS App

可以把代理编译成一个本地 app：

```bash
npm run build:app
```

生成位置：

```text
~/Applications/Codex App Proxy.app
```

之后可以直接用 Spotlight 搜 `Codex App Proxy` 启动。

现在这个 app 采用更直接的方式：

- 打开 app 时，会弹出一个 Terminal 窗口
- Terminal 会 `cd` 到项目目录并直接运行 `/opt/homebrew/bin/node src/server.js`
- 关闭那个 Terminal 窗口，就等于停止代理

日志文件：

```text
~/Library/Logs/codex-app-proxy.log
```

## 测试

现在仓库里带了一个最小回归测试集，直接跑：

```bash
npm test
```

覆盖点包括：

- `image_generation` 会从 JSON body 的 `tools` 里被过滤掉
- `tool_choice` 指向 `image_generation` 时会被改成 `auto`
- `PATCH` / `PUT` 这类非 `POST` 但带 body 的请求不会再丢 body
- 压缩过的 JSON body 因为无法安全过滤，会返回 `415`
- 上游流式响应可以正常回传

## Mock 压测

压测不会打真实上游 API，而是：

- 本地起一个 mock upstream
- 本地起当前 proxy
- 先测直连 mock upstream
- 再测通过 proxy 转发到同一个 mock upstream

直接跑：

```bash
npm run bench
```

默认会输出：

- 两组结果：`direct-upstream` 和 `via-proxy`
- 每组的总耗时、RPS、`p50/p95/p99/max` 延迟
- proxy 相对直连的额外开销

常用调参方式：

```bash
BENCH_REQUESTS=1000 \
BENCH_CONCURRENCY=100 \
BENCH_BODY_BYTES=65536 \
npm run bench
```

可用环境变量：

- `BENCH_REQUESTS`：总请求数，默认 `400`
- `BENCH_CONCURRENCY`：并发数，默认 `40`
- `BENCH_BODY_BYTES`：请求 JSON 里 `input` 的填充大小，默认 `32768`
- `BENCH_UPSTREAM_DELAY_MS`：mock upstream 每次响应前额外 sleep 多久，默认 `0`
- `BENCH_RESPONSE_BYTES`：mock upstream 返回体大小，默认 `256`
- `BENCH_PROXY_PORT`：proxy 压测时监听端口，默认 `21100`
