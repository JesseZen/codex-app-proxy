import { mock } from "bun:test"
import { createTestRenderer } from "@opentui/core/testing"
import type { TuiPluginApi } from "@agent-inn/plugin/tui"
import { Effect } from "effect"
import { Global } from "@agent-inn/core/global"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { tmpdir } from "./fixture/fixture"
import { createTuiResolvedConfig } from "./fixture/tui-runtime"
import { createEventSource, createFetch, directory, json } from "./fixture/tui-sdk"
import { registerProxyCommands } from "../src/proxy/commands"
import {
  toAinnUpstreams,
  type ProxyConfigStatus,
  type ProxySettings,
  type PluginDefinition,
  type RedactedUpstream,
  type WorkerSummary,
} from "../src/proxy/backend"

export async function wait(fn: () => boolean | Promise<boolean>, timeout = 2000) {
  const start = Date.now()
  while (!(await fn())) {
    if (Date.now() - start > timeout) throw new Error("timed out waiting for condition")
    await Bun.sleep(10)
  }
}

function frameLines(frame: string) {
  return frame
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}

function createProxyHarness() {
  const providers = new Map<string, RedactedUpstream>([
    [
      "openai",
      {
        name: "openai",
        base_url: "https://api.openai.com/v1",
        has_api_key: true,
      },
    ],
    [
      "anthropic",
      {
        name: "anthropic",
        base_url: "https://api.anthropic.com/v1",
        has_api_key: true,
      },
    ],
  ])

  const workers = new Map<number, WorkerSummary>([
    [
      6767,
      {
        name: "app",
        port: 6767,
        role: "app",
        upstream: providers.get("openai")!,
        status: "running",
        snapshot_generation: 3,
        log_level: "simple",
        modules: {
          model_override: { enabled: false, params: { model: "gpt-old" } },
          api_translate: { enabled: true, params: { api_format: "chat_completions" } },
        },
        hooks: {
          config_patch: { enabled: true, params: { config_path: "~/.codex/config.toml", state_dir: "~/.ainn" } },
        },
        hook_statuses: {
          config_patch: { state: "active", detail: { provider_name: "test" } },
        },
      },
    ],
    [
      11199,
      {
        name: "cli-openrouter",
        port: 11199,
        role: "cli",
        upstream: providers.get("openai")!,
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
      },
    ],
  ])

  const logs = new Map<number, string[]>([[6767, ["booted", "serving :6767"]]])
  const config: {
    status: ProxyConfigStatus
    settings: ProxySettings
    plugins: Record<string, PluginDefinition>
  } = {
    status: {
      generation: 4,
      dirty: true,
      last_save_error: "",
    },
    settings: {
      state_dir: "~/.ainn",
      log_dir: "~/.ainn/logs",
      launch: { default_mode: "hosted-terminal" },
      terminal: {
        host: "tmux",
        opener: "default",
        tmux: {
          socket_name: "ainn",
          host_session: "ainn-host",
        },
      },
    },
    plugins: {
      api_translate: { kind: "request_middleware", source: "builtin" },
      model_override: { kind: "request_middleware", source: "builtin" },
      request_log: { kind: "request_middleware", source: "builtin" },
      config_patch: { kind: "lifecycle_hook", source: "builtin" },
    },
  }
  const calls = {
    patchWorker: [] as Array<{ port: number; upstream?: string; log_level?: string }>,
    patchModule: [] as Array<{ port: number; module: string; body: Record<string, unknown> }>,
    patchUpstream: [] as Array<{ name: string; body: { base_url?: string; api_key?: string; api_format?: string } }>,
    patchSettings: [] as Array<Partial<ProxySettings>>,
    deleteWorker: [] as number[],
    deleteUpstream: [] as string[],
    restartWorker: [] as number[],
    stopWorker: [] as number[],
    saveConfig: 0,
    getLogs: 0,
    testUpstream: [] as string[],
    testAllUpstreams: 0,
  }

  const fetch = createFetch((url) => {
    if (url.pathname === "/config/providers")
      return json({
        providers: toAinnUpstreams([...providers.values()]),
        default: Object.fromEntries([...providers.keys()].map((name) => [name, `${name}-proxy`])),
      })
    if (url.pathname === "/provider")
      return json({
        all: toAinnUpstreams([...providers.values()]),
        default: Object.fromEntries([...providers.keys()].map((name) => [name, `${name}-proxy`])),
        connected: [...providers.keys()],
      })
    if (url.pathname === "/agent")
      return json([
        {
          name: "build",
          mode: "primary",
          hidden: false,
          permission: [],
          model: { providerID: "openai", modelID: "openai-proxy" },
          options: {},
        },
      ])
    if (url.pathname === "/api/workers")
      return json({
        workers: [...workers.values()],
      })
    if (url.pathname === "/api/workers/6767" && url.search === "")
      return json(workers.get(6767)!)
    if (url.pathname === "/api/upstreams")
      return json({
        upstreams: Object.fromEntries(providers.entries()),
      })
    if (url.pathname === "/api/config" && url.search === "") {
      if (url.href.includes("&__method=PUT")) return undefined
      return json({
        config: { plugins: config.plugins },
        status: config.status,
      })
    }
    if (url.pathname === "/api/config" && url.searchParams.get("__method") === "PUT") {
      return undefined
    }
    if (url.pathname === "/api/config")
      return json({
        config: { plugins: config.plugins },
        status: config.status,
      })
    if (url.pathname === "/api/settings") {
      return json({
        settings: config.settings,
        status: config.status,
      })
    }
    if (url.pathname === "/api/workers/6767/logs") {
      calls.getLogs += 1
      return json({ lines: logs.get(6767) ?? [] })
    }
    if (url.pathname === "/api/workers/6767" && url.searchParams.get("__method") === "PATCH") {
      return undefined
    }
    if (url.pathname === "/api/workers/6767/modules/model_override" && url.searchParams.get("__method") === "PATCH") {
      return undefined
    }
    return undefined
  })

  const override = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = input instanceof Request ? input : undefined
    const url = new URL(request ? request.url : String(input))
    const method = (init?.method ?? request?.method ?? "GET").toUpperCase()

    if (url.pathname === "/api/workers/6767" && method === "PATCH") {
      const body = JSON.parse(String(init?.body ?? "null")) as { upstream: string; log_level?: string }
      calls.patchWorker.push({ port: 6767, upstream: body.upstream, log_level: body.log_level })
      const nextUpstream = providers.get(body.upstream)
      if (nextUpstream) {
        workers.set(6767, {
          ...workers.get(6767)!,
          upstream: nextUpstream,
        })
      }
      if (body.log_level) {
        workers.set(6767, { ...workers.get(6767)!, log_level: body.log_level })
      }
      return json(workers.get(6767)!)
    }

    if (url.pathname === "/api/workers/6767/modules/model_override" && method === "PATCH") {
      const body = JSON.parse(String(init?.body ?? "null")) as { enabled: boolean; params?: { model?: string } }
      calls.patchModule.push({ port: 6767, module: "model_override", body })
      workers.set(6767, {
        ...workers.get(6767)!,
        modules: {
          ...workers.get(6767)!.modules,
          model_override: body,
        },
      })
      return json({
        worker: "app",
        port: 6767,
        module: {
          name: "model_override",
          enabled: body.enabled,
          params: body.params,
        },
      })
    }

    if (url.pathname === "/api/workers/6767/restart" && method === "POST") {
      calls.restartWorker.push(6767)
      workers.set(6767, { ...workers.get(6767)!, status: "running" })
      return json({ worker: "app", status: "running" })
    }

    if (url.pathname === "/api/workers/6767" && method === "DELETE") {
      calls.stopWorker.push(6767)
      workers.set(6767, { ...workers.get(6767)!, status: "stopped" })
      return json({ worker: "app", status: "stopped" })
    }

    if (url.pathname === "/api/workers/6767/config" && method === "DELETE") {
      calls.deleteWorker.push(6767)
      workers.delete(6767)
      return json({ worker: "app" })
    }

    if (url.pathname.startsWith("/api/upstreams/") && method === "PATCH") {
      const name = url.pathname.slice("/api/upstreams/".length)
      const body = JSON.parse(String(init?.body ?? "null")) as { base_url?: string; api_key?: string; api_format?: string }
      calls.patchUpstream.push({ name, body })
      providers.set(name, {
        name,
        base_url: body.base_url ?? providers.get(name)?.base_url ?? "",
        api_format: body.api_format ?? providers.get(name)?.api_format,
        has_api_key: body.api_key !== undefined ? Boolean(body.api_key) : providers.get(name)?.has_api_key ?? false,
      })
      for (const [port, worker] of workers.entries()) {
        if (worker.upstream.name !== name) continue
        workers.set(port, {
          ...worker,
          upstream: providers.get(name)!,
        })
      }
      return json(providers.get(name)!)
    }

    if (url.pathname.startsWith("/api/upstreams/") && method === "DELETE") {
      const name = url.pathname.slice("/api/upstreams/".length)
      calls.deleteUpstream.push(name)
      providers.delete(name)
      return json({ upstream: name })
    }

    if (url.pathname === "/api/upstreams/test" && method === "POST") {
      calls.testAllUpstreams += 1
      return json({
        results: [...providers.values()].map((p) => ({
          upstream: p.name,
          ok: true,
          status_code: 200,
          latency_ms: 120,
        })),
      })
    }

    if (url.pathname.startsWith("/api/upstreams/") && url.pathname.endsWith("/test") && method === "POST") {
      const name = url.pathname.slice("/api/upstreams/".length, -"/test".length)
      calls.testUpstream.push(name)
      return json({ upstream: name, ok: true, status_code: 200, latency_ms: 120 })
    }

    if (url.pathname === "/api/config" && method === "PUT") {
      calls.saveConfig += 1
      config.status = { ...config.status, dirty: false }
      return json({ status: config.status })
    }

    if (url.pathname === "/api/settings" && method === "PATCH") {
      const body = JSON.parse(String(init?.body ?? "null")) as Partial<ProxySettings>
      calls.patchSettings.push(body)
      config.settings = {
        ...config.settings,
        ...body,
        launch: { ...config.settings.launch, ...body.launch },
        terminal: {
          ...config.settings.terminal,
          ...body.terminal,
          tmux: { ...config.settings.terminal.tmux, ...body.terminal?.tmux },
        },
      }
      config.status = { ...config.status, dirty: false, generation: config.status.generation + 1 }
      return json({ settings: config.settings, status: config.status })
    }

    if (url.pathname === "/api/events") {
      return new Response("", {
        headers: { "content-type": "text/event-stream" },
      })
    }

    return fetch.fetch(input, init)
  }) as typeof fetch.fetch

  return { calls, fetch: override }
}

export async function mountProxyApp() {
  const tmp = await tmpdir()
  const home = tmp.path
  const app = "ainn"
  const data = path.join(home, ".local", "share", app)
  const cache = path.join(home, ".cache", app)
  const state = path.join(home, ".local", "state", app)
  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))
  await mkdir(state, { recursive: true })
  await Bun.write(path.join(state, "kv.json"), "{}")
  const events = createEventSource()
  const proxy = createProxyHarness()
  let api!: TuiPluginApi
  let started!: () => void
  const ready = new Promise<void>((resolve) => {
    started = resolve
  })

  const { run } = await import("../src/app")
  const task = Effect.runPromise(
    run({
      url: "http://test",
      directory,
      config: createTuiResolvedConfig({ plugin_enabled: {} }),
      fetch: proxy.fetch,
      events: events.source,
      args: {},
      pluginHost: {
        async start(input) {
          api = input.api
          registerProxyCommands(api)
          started()
        },
        async dispose() {},
      },
    }).pipe(
      Effect.provide(
        Global.layerWith({
          home,
          data,
          cache,
          config: path.join(home, ".config", app),
          state,
          tmp: path.join(home, "tmp", app),
          bin: path.join(cache, "bin"),
          log: path.join(data, "log"),
          repos: path.join(data, "repos"),
        }),
      ),
    ),
  )

  await ready
  await setup.renderOnce()
  await setup.renderOnce()

  return {
    api,
    calls: proxy.calls,
    setup,
    frame() {
      return setup.captureCharFrame()
    },
    lines() {
      return frameLines(setup.captureCharFrame())
    },
    mockInput: setup.mockInput,
    async render() {
      await setup.renderOnce()
    },
    async cleanup() {
      setup.renderer.destroy()
      await task
      mock.restore()
      await tmp[Symbol.asyncDispose]()
    },
  }
}

export type ProxyApp = Awaited<ReturnType<typeof mountProxyApp>>

export async function runCommand(app: ProxyApp, command: string) {
  app.api.keymap.dispatchCommand(command)
  await app.render()
}

export async function openWorkerDetail(app: ProxyApp) {
  await runCommand(app, "proxy.workers")
  await runCommand(app, "dialog.select.next")
  await runCommand(app, "dialog.select.submit")
}

export async function openUpstreamManager(app: ProxyApp) {
  await runCommand(app, "proxy.upstreams")
}

export async function openUpstreamEditor(app: ProxyApp, name: string) {
  await openUpstreamManager(app)
  await runCommand(app, "dialog.select.next")
  await runCommand(app, "dialog.select.next")
  await runCommand(app, "dialog.select.submit")
  await wait(async () => {
    await app.render()
    return app.frame().includes(`Edit Upstream: ${name}`)
  })
}
