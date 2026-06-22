import { expect, mock, test } from "bun:test"
import { createTestRenderer } from "@opentui/core/testing"
import type { TuiPluginApi } from "@codex-proxy/plugin/tui"
import { Effect } from "effect"
import { Global } from "@codex-proxy/core/global"
import { createTuiResolvedConfig } from "./fixture/tui-runtime"
import { createEventSource, createFetch, directory, json } from "./fixture/tui-sdk"
import { registerProxyCommands } from "../src/proxy/commands"
import { DialogPrompt } from "../src/ui/dialog-prompt"

async function wait(fn: () => boolean | Promise<boolean>, timeout = 2000) {
  const start = Date.now()
  while (!(await fn())) {
    if (Date.now() - start > timeout) throw new Error("timed out waiting for condition")
    await Bun.sleep(10)
  }
}

test("launch dialog enables directory completion with current project directory", async () => {
  const promptCalls: any[] = []
  const originalShow = DialogPrompt.show
  DialogPrompt.show = async (_dialog: unknown, _title: string, options: any) => {
    promptCalls.push(options)
    return null
  }

  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))

  const events = createEventSource()
  const calls = createFetch((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [
          {
            name: "test-cli",
            port: 1234,
            role: "cli",
            upstream: { name: "test", base_url: "", has_api_key: false },
            status: "running",
            snapshot_generation: 0,
            log_level: "info",
          },
        ],
      })
    return undefined
  })

  let api!: TuiPluginApi
  let started!: () => void
  const ready = new Promise<void>((resolve) => {
    started = resolve
  })

  try {
    const { run } = await import("../src/app")
    const task = Effect.runPromise(
      run({
        url: "http://test",
        directory,
        config: createTuiResolvedConfig({ plugin_enabled: {} }),
        fetch: calls.fetch,
        events: events.source,
        args: {},
        pluginHost: {
          async start(input) {
            api = input.api
            registerProxyCommands(input.api)
            started()
          },
          async dispose() {},
        },
      }).pipe(Effect.provide(Global.defaultLayer)),
    )

    await ready
    await setup.renderOnce()
    await setup.renderOnce()

    api.keymap.dispatchCommand("proxy.launch")
    await wait(async () => {
      await setup.renderOnce()
      return setup.captureCharFrame().includes("test-cli")
    })

    api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => promptCalls.length === 1)

    setup.renderer.destroy()
    await task

    expect(promptCalls).toEqual([
      expect.objectContaining({
        directoryCompletion: {
          basePath: directory,
        },
      }),
    ])
  } finally {
    DialogPrompt.show = originalShow
    if (!setup.renderer.isDestroyed) setup.renderer.destroy()
    mock.restore()
  }
})

test("launch directory prompt ESC returns to worker picker", async () => {
  const setup = await createTestRenderer({ width: 80, height: 24, useThread: false, kittyKeyboard: true })
  const core = await import("@opentui/core")
  mock.module("@opentui/core", () => ({ ...core, createCliRenderer: async () => setup.renderer }))

  const events = createEventSource()
  const calls = createFetch((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [
          {
            name: "test-cli",
            port: 1234,
            role: "cli",
            upstream: { name: "test", base_url: "", has_api_key: false },
            status: "running",
            snapshot_generation: 0,
            log_level: "info",
          },
        ],
      })
    return undefined
  })

  let api!: TuiPluginApi
  let started!: () => void
  const ready = new Promise<void>((resolve) => {
    started = resolve
  })

  try {
    const { run } = await import("../src/app")
    const task = Effect.runPromise(
      run({
        url: "http://test",
        directory,
        config: createTuiResolvedConfig({ plugin_enabled: {} }),
        fetch: calls.fetch,
        events: events.source,
        args: {},
        pluginHost: {
          async start(input) {
            api = input.api
            registerProxyCommands(input.api)
            started()
          },
          async dispose() {},
        },
      }).pipe(Effect.provide(Global.defaultLayer)),
    )

    await ready
    await setup.renderOnce()
    await setup.renderOnce()

    api.keymap.dispatchCommand("proxy.launch")
    await wait(async () => {
      await setup.renderOnce()
      return setup.captureCharFrame().includes("test-cli")
    })

    api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await setup.renderOnce()
      const frame = setup.captureCharFrame()
      return frame.includes("Launch Codex") && frame.includes(directory)
    })

    setup.mockInput.pressEscape()
    await wait(async () => {
      await setup.renderOnce()
      const frame = setup.captureCharFrame()
      return frame.includes("Launch Codex CLI") && frame.includes("test-cli")
    })

    setup.renderer.destroy()
    await task
  } finally {
    if (!setup.renderer.isDestroyed) setup.renderer.destroy()
    mock.restore()
  }
})
