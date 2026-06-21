import { expect, mock, test } from "bun:test"
import { createTestRenderer } from "@opentui/core/testing"
import type { TuiPluginApi } from "@codex-proxy/plugin/tui"
import { Effect } from "effect"
import { Global } from "@codex-proxy/core/global"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { createTuiResolvedConfig } from "./fixture/tui-runtime"
import { createEventSource, createFetch, directory, json } from "./fixture/tui-sdk"
import { registerProxyCommands } from "../src/proxy/commands"
import { toCodexProxyProviders, type ProxyConfigStatus, type RedactedProvider, type WorkerSummary } from "../src/proxy/backend"

async function wait(fn: () => boolean | Promise<boolean>, timeout = 2000) {
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
  const providers = new Map<string, RedactedProvider>([
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
        provider: providers.get("openai")!,
        status: "running",
        snapshot_generation: 3,
        log_level: "simple",
        modules: {
          api_translate: { enabled: true },
          request_log: { enabled: false },
        },
      },
    ],
    [
      11199,
      {
        name: "cli-openrouter",
        port: 11199,
        role: "cli",
        provider: providers.get("openai")!,
        status: "running",
        snapshot_generation: 1,
        log_level: "simple",
      },
    ],
  ])

  const logs = new Map<number, string[]>([[6767, ["booted", "serving :6767"]]])
  const config = {
    status: {
      generation: 4,
      dirty: true,
      last_save_error: "",
    } satisfies ProxyConfigStatus,
  }
  const calls = {
    patchWorker: [] as Array<{ port: number; provider: string }>,
    patchProvider: [] as Array<{ name: string; body: { base_url?: string; api_key?: string; api_format?: string } }>,
    saveConfig: 0,
    getLogs: 0,
  }

  const fetch = createFetch(async (url) => {
    if (url.pathname === "/config/providers")
      return json({
        providers: toCodexProxyProviders([...providers.values()]),
        default: Object.fromEntries([...providers.keys()].map((name) => [name, `${name}-proxy`])),
      })
    if (url.pathname === "/provider")
      return json({
        all: toCodexProxyProviders([...providers.values()]),
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
    if (url.pathname === "/api/providers")
      return json({
        providers: Object.fromEntries(providers.entries()),
      })
    if (url.pathname === "/api/config" && url.search === "") {
      if (url.href.includes("&__method=PUT")) return undefined
      return json({
        config: {},
        status: config.status,
      })
    }
    if (url.pathname === "/api/config" && url.searchParams.get("__method") === "PUT") {
      return undefined
    }
    if (url.pathname === "/api/config")
      return json({
        config: {},
        status: config.status,
      })
    if (url.pathname === "/api/workers/6767/logs") {
      calls.getLogs += 1
      return json({ lines: logs.get(6767) ?? [] })
    }
    if (url.pathname === "/api/workers/6767" && url.searchParams.get("__method") === "PATCH") {
      return undefined
    }
    return undefined
  })

  const override = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = input instanceof Request ? input : undefined
    const url = new URL(request ? request.url : String(input))
    const method = (init?.method ?? request?.method ?? "GET").toUpperCase()

    if (url.pathname === "/api/workers/6767" && method === "PATCH") {
      const body = JSON.parse(String(init?.body ?? "null")) as { provider: string }
      calls.patchWorker.push({ port: 6767, provider: body.provider })
      const nextProvider = providers.get(body.provider)
      if (nextProvider) {
        workers.set(6767, {
          ...workers.get(6767)!,
          provider: nextProvider,
        })
      }
      return json(workers.get(6767)!)
    }

    if (url.pathname.startsWith("/api/providers/") && method === "PATCH") {
      const name = url.pathname.slice("/api/providers/".length)
      const body = JSON.parse(String(init?.body ?? "null")) as { base_url?: string; api_key?: string; api_format?: string }
      calls.patchProvider.push({ name, body })
      providers.set(name, {
        name,
        base_url: body.base_url ?? providers.get(name)?.base_url ?? "",
        api_format: body.api_format ?? providers.get(name)?.api_format,
        has_api_key: body.api_key !== undefined ? Boolean(body.api_key) : providers.get(name)?.has_api_key ?? false,
      })
      for (const [port, worker] of workers.entries()) {
        if (worker.provider.name !== name) continue
        workers.set(port, {
          ...worker,
          provider: providers.get(name)!,
        })
      }
      return json(providers.get(name)!)
    }

    if (url.pathname === "/api/config" && method === "PUT") {
      calls.saveConfig += 1
      config.status = { ...config.status, dirty: false }
      return json({ status: config.status })
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

async function mountProxyApp() {
  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))
  const state = path.join("/Users/jesse/.local/state/codex-proxy")
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
    }).pipe(Effect.provide(Global.defaultLayer)),
  )

  await ready
  await setup.renderOnce()
  await setup.renderOnce()

  return {
    api,
    calls: proxy.calls,
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
    },
  }
}

test("proxy switch updates worker provider and status detail reflects the change", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.switch")
    await app.render()
    expect(app.frame()).toContain("Switch Worker Upstream")

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await app.render()
    expect(app.frame()).toContain("Switch Upstream: app")

    app.api.keymap.dispatchCommand("dialog.select.next")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.patchWorker.length === 1)
    await app.render()

    app.api.keymap.dispatchCommand("proxy.status")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await app.render()

    expect(app.frame()).toContain("upstream: anthropic")
  } finally {
    await app.cleanup()
  }
})

test("proxy logs opens worker logs dialog with initial log lines", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.logs")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.getLogs === 1)
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Logs: app (:6767)") && frame.includes("booted")
    })

    expect(app.frame()).toContain("Logs: app (:6767)")
    expect(app.frame()).toContain("booted")
  } finally {
    await app.cleanup()
  }
})

test("proxy config save clears dirty state on reopen", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.config")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.saveConfig === 1)
    await app.render()

    app.api.keymap.dispatchCommand("proxy.config")
    await app.render()
    expect(app.frame().includes("Save Config to Disk")).toBe(false)
  } finally {
    await app.cleanup()
  }
})

test("proxy status detail exposes worker follow-up actions", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.status")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await app.render()

    expect(app.frame()).toContain("Switch Upstream")
    expect(app.frame()).toContain("View Logs")
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream registers an upstream command", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.upstream")
    await app.render()
    expect(app.frame()).toContain("Manage Upstreams")
    expect(app.frame()).toContain("Create New Upstream")
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream selection opens field list and saves provider", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.upstream")
    await app.render()
    expect(app.frame()).toContain("Manage Upstreams")

    app.api.keymap.dispatchCommand("dialog.select.next")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Upstream: openai")
    })

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Base URL: https://api.openai.com/v1")
    })
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(() => app.calls.patchProvider.length === 1)

    expect(app.calls.patchProvider).toEqual([
      {
        name: "openai",
        body: { base_url: "https://api.openai.com/v1" },
      },
    ])
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream creates a new upstream", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.upstream")
    await app.render()
    expect(app.frame()).toContain("Create New Upstream")

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("New Upstream Name")
    })

    await app.mockInput.typeText("groq")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Upstream: groq")
    })

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Base URL: upstream")
    })
    await app.mockInput.typeText("https://api.groq.com/openai/v1")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(() => app.calls.patchProvider.length === 1)

    expect(app.calls.patchProvider).toEqual([
      {
        name: "groq",
        body: { base_url: "https://api.groq.com/openai/v1" },
      },
    ])
  } finally {
    await app.cleanup()
  }
})

test("proxy status detail view logs action opens worker logs", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.status")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await app.render()

    app.api.keymap.dispatchCommand("dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.getLogs === 1)
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Logs: app (:6767)") && frame.includes("booted")
    })

    expect(app.frame()).toContain("Logs: app (:6767)")
    expect(app.frame()).toContain("booted")
  } finally {
    await app.cleanup()
  }
})

test("proxy launch registers a launch command", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.launch")
    await app.render()
    expect(app.frame()).toContain("Launch Codex CLI")
  } finally {
    await app.cleanup()
  }
})
