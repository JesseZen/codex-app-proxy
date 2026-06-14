# codex-app-proxy

给 Codex App 用的本地代理：

`Codex App -> http://127.0.0.1:8787 -> 你的中转站 API`

它主要做几件事：

- 把请求转发到 `BASE_URL`
- 过滤 JSON 请求里 `tools` 中的 `image_generation`
- 如果 `tool_choice` 明确指定了 `image_generation`，改成 `auto`
- 支持 Chat Completions API 到 Responses API 的自动转译（`API_FORMAT=chat_completions`）
- 支持用 `ACTIVE_PROVIDER` 切换 `.env.<provider>` 配置
- 启动时把 `~/.codex/config.toml` 里当前 `model_provider` 的 `base_url` 改成本地代理地址
- 退出时把 `base_url` 改回去
- 可以打包成一个本地 macOS app，方便直接启动

## 启动

需要 Node 20+。Node 18/22/25 也可以。

```bash
cp .env.example .env
npm start
```

程序启动时会自动读取当前目录下的 `.env`，不需要手动 `export`。

## 多服务商切换

比较省事的方式是这样分：

- `.env` 放公共配置和当前选中的 `ACTIVE_PROVIDER`
- `.env.<provider>` 放每个服务商自己的 `BASE_URL` / `API_KEY`

示例：

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

启动时会先看 `.env` 里的 `ACTIVE_PROVIDER`，再加载对应的 `.env.<provider>`。

如果只是切换 provider，改这一项就够了：

```bash
ACTIVE_PROVIDER=openai
```

加载优先级如下：

1. shell 里显式传入的环境变量
2. `.env.<ACTIVE_PROVIDER>`
3. `.env`

所以命令行里临时传的值优先级最高。

## 命令行快捷启动

除了手改 `.env`，也可以直接按 provider 启动：

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

第二个例子会去读：

```bash
.env.myrelay
```

也可以完全不用 `.env`，直接传：

```bash
PORT=8787 \
BASE_URL=https://your-relay.example.com \
npm start
```

如果你的中转站要求固定 key，再额外设置：

```bash
API_KEY=sk-xxx
```

代理会用 `Bearer <API_KEY>` 覆盖客户端传来的 `Authorization`。

如果同时设置了 `ACTIVE_PROVIDER` 和 `.env.<provider>`，`API_KEY` / `BASE_URL` 会优先取 provider 文件里的值。只有你在 shell 里再次显式传入，才会覆盖它们。

## Chat Completions 适配

如果你的上游只支持 OpenAI Chat Completions API（`/v1/chat/completions`），不支持 Responses API（`/v1/responses`），Codex App 会报错：

```
stream disconnected before completion: stream closed before response.completed
```

在 `.env.<provider>` 里加一行就行：

```bash
API_FORMAT=chat_completions
```

代理会自动做这些转译：

- 请求路径：`/v1/responses` → `/v1/chat/completions`
- 请求体：`input` → `messages`，`instructions` → `system` 消息，`tools` 格式转换
- 响应 SSE：Chat Completions 的 `data:` + `[DONE]` → Responses API 的 `event: response.xxx` + `response.completed`
- Tool call：Chat Completions 的 `delta.tool_calls` → Responses API 的 `function_call` 事件

适用场景举例：

| Provider | 需要 `API_FORMAT=chat_completions`？ |
|----------|-------------------------------------|
| OpenAI | ❌ 原生支持 Responses API |
| OpenRouter | ❌ 原生支持 |
| Groq | ✅ 只支持 Chat Completions |
| JoyCode | ✅ 只支持 Chat Completions |
| 其他 Chat-only 中转 | ✅ |

原生支持 Responses API 的 provider 不需要设这个，代理会直接透明转发。

## Codex 配置

代理会自动修改 `~/.codex/config.toml` 里当前 `model_provider` 对应 section 的 `base_url`。

如果你的上游地址本身带前缀，比如 `/v1`，那就把 `BASE_URL` 也写成带前缀的完整地址：

```bash
BASE_URL=https://your-relay.example.com/v1
```

代理会保留原始请求路径和 query string，再拼到这个 base URL 后面。

默认配置文件路径是 `~/.codex/config.toml`。如果你想指定别的路径，可以设置：

```bash
CODEX_CONFIG_PATH=/path/to/config.toml
```

现在这套做法会改两项配置：

- 启动时把 `base_url` 注入成 `http://127.0.0.1:PORT`
- 如果设置了 `API_KEY`，也会把 `experimental_bearer_token` 改成同一个值

正常退出时，这两项都会恢复。

如果进程被强制杀掉，比如 `kill -9`，退出钩子来不及执行，`base_url` 和 `experimental_bearer_token` 都可能残留在 `config.toml` 里。这不是远端泄露，但算本机残留风险。

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

当前这个 app 的行为很直接：

- 打开 app，会弹出一个 Terminal 窗口
- Terminal 会 `cd` 到项目目录，然后执行 `/opt/homebrew/bin/node src/server.js`
- 关掉这个 Terminal 窗口，就等于停止代理

日志文件在：

```text
~/Library/Logs/codex-app-proxy.log
```

## 测试

仓库里带了最小回归测试，直接跑：

```bash
npm test
```

覆盖点包括：

- `image_generation` 会从 JSON body 的 `tools` 里被过滤掉
- `tool_choice` 指向 `image_generation` 时会被改成 `auto`
- `PATCH` / `PUT` 这类带 body 的非 `POST` 请求不会丢 body
- 压缩过的 JSON body 因为没法安全过滤，会返回 `415`
- 上游流式响应可以正常回传
- Chat Completions 模式下请求体和路径会正确转译
- Chat Completions SSE 流会转译成 Responses API 格式，包含 `response.completed`
- Tool call 会正确转译（请求和响应双向）
- 非 `/v1/responses` 路径不受 Chat Completions 模式影响
- `image_generation` 过滤在 Chat Completions 模式下依然生效

## mock 压测

压测不会打真实上游 API。流程是：

- 本地起一个 mock upstream
- 本地起当前 proxy
- 先测直连 mock upstream
- 再测通过 proxy 转发到同一个 mock upstream

直接跑：

```bash
npm run bench
```

默认输出：

- `direct-upstream` 和 `via-proxy` 两组结果
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
