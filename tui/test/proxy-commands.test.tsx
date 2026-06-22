import { expect, mock, test } from "bun:test"
import { createTestRenderer } from "@opentui/core/testing"
import type { TuiPluginApi } from "@codex-proxy/plugin/tui"
import { Effect } from "effect"
import { Global } from "@codex-proxy/core/global"
import { mkdir } from "node:fs/promises"
import path from "node:path"
import { tmpdir } from "./fixture/fixture"
import { createTuiResolvedConfig } from "./fixture/tui-runtime"
import { createEventSource, createFetch, directory, json } from "./fixture/tui-sdk"
import { registerProxyCommands } from "../src/proxy/commands"
import { toCodexProxyUpstreams, type ProxyConfigStatus, type RedactedUpstream, type WorkerSummary } from "../src/proxy/backend"

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
        upstream: providers.get("openai")!,
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
    patchWorker: [] as Array<{ port: number; upstream?: string; log_level?: string }>,
    patchModule: [] as Array<{ port: number; module: string; body: Record<string, unknown> }>,
    patchUpstream: [] as Array<{ name: string; body: { base_url?: string; api_key?: string; api_format?: string } }>,
    saveConfig: 0,
    getLogs: 0,
  }

  const fetch = createFetch(async (url) => {
    if (url.pathname === "/config/providers")
      return json({
        providers: toCodexProxyUpstreams([...providers.values()]),
        default: Object.fromEntries([...providers.keys()].map((name) => [name, `${name}-proxy`])),
      })
    if (url.pathname === "/provider")
      return json({
        all: toCodexProxyUpstreams([...providers.values()]),
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
  const tmp = await tmpdir()
  const home = tmp.path
  const app = "codex-proxy"
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

test("proxy status switch upstream action updates worker provider and reflects the change", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.status")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await app.render()
    expect(app.frame()).toContain("Switch Upstream")

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

test("proxy modules editor patches module params through field flow", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.modules")
    await app.render()
    expect(app.frame()).toContain("Worker Modules")

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Modules: app")
    })

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Module: app")
    })

    app.api.keymap.dispatchCommand("dialog.select.next")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Model: model_override")
    })

    await app.mockInput.typeText("gpt-live")
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(() => app.calls.patchModule.length === 1)

    expect(app.calls.patchModule).toEqual([
      {
        port: 6767,
        module: "model_override",
        body: {
          enabled: false,
          params: { model: "gpt-live" },
        },
      },
    ])

    expect(app.frame()).toContain("Worker Modules")
    expect(app.frame()).not.toContain("Modules: app")

    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      return !app.frame().includes("Worker Modules")
    })
    expect(app.frame()).not.toContain("Worker Modules")
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
    await wait(() => app.calls.patchUpstream.length === 1)

    expect(app.calls.patchUpstream).toEqual([
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
    await app.render()
    await wait(async () => {
      await app.render()
      return app.frame().includes("https://api.groq.com/openai/v1")
    })
    app.mockInput.pressEnter()
    await wait(() => app.calls.patchUpstream.length === 1)

    expect(app.calls.patchUpstream).toEqual([
      {
        name: "groq",
        body: { base_url: "https://api.groq.com/openai/v1" },
      },
    ])
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream editor shows empty api_format as dash and persists edits", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.upstream")
    await app.render()

    app.api.keymap.dispatchCommand("dialog.select.next")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Upstream: openai")
    })

    await app.render()
    const frame = app.frame()
    expect(frame).toContain("API Format")
    expect(frame).not.toContain("chat_completions")

    app.api.keymap.dispatchCommand("dialog.select.end")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await app.render()
    await app.mockInput.typeText("responses")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(() => app.calls.patchUpstream.length === 1)

    expect(app.calls.patchUpstream).toEqual([
      { name: "openai", body: { api_format: "responses" } },
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

test("proxy workers editor patches log_level field", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.workers")
    await app.render()
    expect(app.frame()).toContain("Manage Workers")
    expect(app.frame()).toContain("Create New Worker")

    app.api.keymap.dispatchCommand("dialog.select.next")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await app.render()
    expect(app.frame()).toContain("Edit Worker: app")

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await app.render()
    expect(app.frame()).toContain("Log Level: app")

    app.api.keymap.dispatchCommand("dialog.select.next")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.patchWorker.some((c) => c.log_level === "detail"))
    await app.render()

    expect(app.calls.patchWorker).toEqual([{ port: 6767, upstream: "openai", log_level: "detail" }])
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream editor ESC returns to upstream list when stack depth > 1", async () => {
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
    expect(app.frame()).toContain("esc back")

    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      return app.frame().includes("Manage Upstreams") && !app.frame().includes("Edit Upstream: openai")
    })
    expect(app.frame()).toContain("Manage Upstreams")
    expect(app.frame()).not.toContain("Edit Upstream: openai")
  } finally {
    await app.cleanup()
  }
})

test("proxy upstream editor ESC closes dialog when stack depth is 1", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.upstream")
    await app.render()
    expect(app.frame()).toContain("Manage Upstreams")
    expect(app.frame()).not.toContain("esc back")

    app.mockInput.pressEscape()
    await wait(async () => {
      await app.render()
      return !app.frame().includes("Manage Upstreams")
    })
    expect(app.frame()).not.toContain("Manage Upstreams")
  } finally {
    await app.cleanup()
  }
})
