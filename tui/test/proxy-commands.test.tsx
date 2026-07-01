import { expect, test } from "bun:test"
import { TextareaRenderable } from "@opentui/core"
import { resolveSlashCommand } from "../src/keymap"
import { mountProxyApp, openWorkerDetail, runCommand, wait } from "./proxy-commands.fixture"

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
    app.api.keymap.dispatchCommand("proxy.settings")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.select.end")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.saveConfig === 1)
    await app.render()

    app.api.keymap.dispatchCommand("proxy.settings")
    await app.render()
    expect(app.frame().includes("Save Config to Disk")).toBe(false)
  } finally {
    await app.cleanup()
  }
})

test("proxy settings editor patches settings through manager API", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.settings")
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes("Settings") && frame.includes("State Dir") && frame.includes("~/.ainn")
    })

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.setup.renderer.currentFocusedEditor instanceof TextareaRenderable
    })
    const editor = app.setup.renderer.currentFocusedEditor
    if (!(editor instanceof TextareaRenderable)) throw new Error("expected focused settings prompt")
    editor.selectAll()
    await app.mockInput.typeText("/tmp/ainn-state")
    await app.render()
    app.api.keymap.dispatchCommand("dialog.prompt.submit")
    await wait(async () => {
      await app.render()
      return app.calls.patchSettings.length === 1
    })

    expect(app.calls.patchSettings).toEqual([{ state_dir: "/tmp/ainn-state" }])
  } finally {
    await app.cleanup()
  }
})

test("proxy settings command is registered and config command is removed", async () => {
  const app = await mountProxyApp()

  try {
    const commands = app.api.keymap.getCommandEntries({
      namespace: "palette",
      visibility: "registered",
    })
    const names = commands.map((entry) => entry.command.name)
    expect(names.includes("proxy.settings")).toBe(true)

    expect(resolveSlashCommand(app.api.keymap, "/settings")).toBe("proxy.settings")
    expect(resolveSlashCommand(app.api.keymap, "/config")).toBe("proxy.settings")
  } finally {
    await app.cleanup()
  }
})

test("proxy worker status commands are folded into workers", async () => {
  const app = await mountProxyApp()

  try {
    const commands = app.api.keymap.getCommandEntries({
      namespace: "palette",
      visibility: "registered",
    })
    expect(commands.map((entry) => entry.command.name).includes("proxy.status")).toBe(false)
    expect(commands.map((entry) => entry.command.name).includes("proxy.modules")).toBe(false)

    await openWorkerDetail(app)

    expect(app.frame()).toContain("Switch Upstream")
    expect(app.frame()).toContain("View Logs")
    expect(app.frame()).toContain("Manage Modules")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers detail exposes worker status and scoped actions", async () => {
  const app = await mountProxyApp()

  try {
    await runCommand(app, "proxy.workers")
    expect(app.frame()).toContain("Manage Workers")
    expect(app.frame()).toContain("Create New Worker")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("app (:6767)")
    expect(app.frame()).toContain("status: running")
    expect(app.frame()).toContain("upstream: openai")
    expect(app.frame()).toContain("log level: simple")
    expect(app.frame()).toContain("modules")
    expect(app.frame()).toContain("config_patch: active")
    expect(app.frame()).toContain("Log Level")
    expect(app.frame()).toContain("Switch Upstream")
    expect(app.frame()).toContain("Manage Modules")
    expect(app.frame()).toContain("View Logs")
    expect(app.frame()).toContain("Restart Worker")
    expect(app.frame()).toContain("Stop Worker")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers module action patches module through module API", async () => {
  const app = await mountProxyApp()

  try {
    await openWorkerDetail(app)
    expect(app.frame()).toContain("Manage Modules")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Modules & Hooks: app")
    })

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Edit Module: app")
    })

    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.patchModule.length === 1)

    expect(app.calls.patchModule).toEqual([
      {
        port: 6767,
        module: "model_override",
        body: {
          enabled: true,
          params: { model: "gpt-old" },
        },
      },
    ])
    expect(app.calls.patchWorker).toEqual([])

    expect(app.frame()).not.toContain("Modules & Hooks: app")

    app.mockInput.pressEscape()
    await app.render()
    expect(app.frame()).not.toContain("Saved model_override")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers module view shows lifecycle hooks separately", async () => {
  const app = await mountProxyApp()

  try {
    await openWorkerDetail(app)
    expect(app.frame()).toContain("Manage Modules")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Modules & Hooks: app")
    })

    expect(app.frame()).toContain("Request Middleware")
    expect(app.frame()).toContain("Lifecycle Hooks")
    expect(app.frame()).toContain("request_log")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    expect(app.frame()).toContain("config_patch")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers detail controls worker lifecycle", async () => {
  const app = await mountProxyApp()

	try {
		await openWorkerDetail(app)
		await runCommand(app, "dialog.select.end")
		await runCommand(app, "dialog.select.prev")
		await runCommand(app, "dialog.select.prev")
		app.api.keymap.dispatchCommand("dialog.select.submit")
		await wait(() => app.calls.restartWorker.length === 1)
		expect(app.calls.restartWorker).toEqual([6767])

		await openWorkerDetail(app)
		await runCommand(app, "dialog.select.end")
		await runCommand(app, "dialog.select.prev")
		app.api.keymap.dispatchCommand("dialog.select.submit")
		await wait(() => app.calls.stopWorker.length === 1)
    expect(app.calls.stopWorker).toEqual([6767])
  } finally {
    await app.cleanup()
  }
})

test("proxy workers detail deletes worker config after confirmation", async () => {
  const app = await mountProxyApp()

  try {
    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.end")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(async () => {
      await app.render()
      return app.frame().includes("Delete worker")
    })

    app.api.keymap.dispatchCommand("worker.delete")
    app.mockInput.pressEnter()
    await wait(() => app.calls.deleteWorker.length === 1)
    await app.render()

    expect(app.calls.deleteWorker).toEqual([6767])
    await runCommand(app, "proxy.workers")
    expect(app.frame()).not.toContain("app")
  } finally {
    await app.cleanup()
  }
})

test("proxy workers detail view logs action opens worker logs", async () => {
  const app = await mountProxyApp()

  try {
    await openWorkerDetail(app)
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.next")
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

test("proxy topology command is registered and opens topology dialog", async () => {
  const app = await mountProxyApp()

  try {
    const commands = app.api.keymap.getCommandEntries({
      namespace: "palette",
      visibility: "registered",
    })
    expect(commands.map((entry) => entry.command.name).includes("proxy.topology")).toBe(true)

    app.api.keymap.dispatchCommand("proxy.topology")
    await app.render()
    expect(app.frame()).toContain("Topology")
  } finally {
    await app.cleanup()
  }
})

test("topology dialog click on worker navigates to worker status", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.topology")
    await app.render()
    await app.render()

    await app.setup.mockMouse.click(5, 13)
    await app.render()
    await app.render()

    const frame = app.frame()
    expect(frame).toContain("app (:6767)")
    expect(frame).toContain("Worker actions")
  } finally {
    await app.cleanup()
  }
})

test("topology dialog click on upstream navigates to upstream editor", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.topology")
    await app.render()
    await app.render()

    await app.setup.mockMouse.click(13, 9)
    await app.render()
    await app.render()

    const frame = app.frame()
    expect(frame).toContain("Edit Upstream")
    expect(frame).toContain("openai")
  } finally {
    await app.cleanup()
  }
})

test("topology dialog drag worker to upstream calls patchWorker", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.topology")
    await app.render()
    await app.render()

    await app.setup.mockMouse.drag(5, 13, 38, 9)
    await app.render()
    await app.render()

    expect(app.calls.patchWorker).toContainEqual({ port: 6767, upstream: "anthropic", log_level: "simple" })
  } finally {
    await app.cleanup()
  }
})

test("topology dialog drag upstream to worker calls patchWorker", async () => {
  const app = await mountProxyApp()

  try {
    app.api.keymap.dispatchCommand("proxy.topology")
    await app.render()
    await app.render()

    await app.setup.mockMouse.drag(38, 9, 5, 13)
    await app.render()
    await app.render()

    expect(app.calls.patchWorker).toContainEqual({ port: 6767, upstream: "anthropic", log_level: "simple" })
  } finally {
    await app.cleanup()
  }
})

test("proxy workers editor patches log_level field", async () => {
  const app = await mountProxyApp()

  try {
    await runCommand(app, "proxy.workers")
    expect(app.frame()).toContain("Manage Workers")
    expect(app.frame()).toContain("Create New Worker")

    await runCommand(app, "dialog.select.next")
    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("app (:6767)")

    await runCommand(app, "dialog.select.submit")
    expect(app.frame()).toContain("Log Level: app")

    await runCommand(app, "dialog.select.next")
    app.api.keymap.dispatchCommand("dialog.select.submit")
    await wait(() => app.calls.patchWorker.some((c) => c.log_level === "detail"))
    await app.render()

    expect(app.calls.patchWorker).toEqual([{ port: 6767, upstream: "openai", log_level: "detail" }])
  } finally {
    await app.cleanup()
  }
})
