import { afterEach, expect, mock, test } from "bun:test"
import { Global } from "@codex-proxy/core/global"
import { homedir } from "node:os"
import path from "node:path"
import { createProxyLaunchCommand, renderProxyLaunchCommand } from "../src/proxy/launch"
import { defaultWorker, directory, json, mountHostedTerminalApp, wait } from "./proxy-hosted-terminal.fixture"

afterEach(() => {
  mock.restore()
})

test("Global.Path.config defaults to ~/.codex-proxy", () => {
  expect(Global.Path.config).toBe(path.join(homedir(), ".codex-proxy"))
})

test("createProxyLaunchCommand omits --mode for external-window", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli", mode: "external-window" })
  expect(cmd).toEqual(["codex-proxy", "launch", "--worker", "1234", "--profile", "cli"])
})

test("createProxyLaunchCommand includes --mode hosted-terminal when selected", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli", mode: "hosted-terminal" })
  expect(cmd).toEqual(["codex-proxy", "launch", "--worker", "1234", "--profile", "cli", "--mode", "hosted-terminal"])
})

test("createProxyLaunchCommand includes --config-dir for hosted terminal launches", () => {
  const cmd = createProxyLaunchCommand({
    workerPort: 1234,
    profile: "cli",
    mode: "hosted-terminal",
    configDir: "/tmp/codex-config",
  })
  expect(cmd).toEqual([
    "codex-proxy",
    "launch",
    "--worker",
    "1234",
    "--profile",
    "cli",
    "--config-dir",
    "/tmp/codex-config",
    "--mode",
    "hosted-terminal",
  ])
})

test("createProxyLaunchCommand omits --mode by default", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli" })
  expect(cmd).toEqual(["codex-proxy", "launch", "--worker", "1234", "--profile", "cli"])
})

test("renderProxyLaunchCommand quotes hosted-terminal mode", () => {
  const cmd = createProxyLaunchCommand({ workerPort: 1234, profile: "cli", mode: "hosted-terminal" })
  const rendered = renderProxyLaunchCommand(cmd)
  expect(rendered).toContain("'--mode' 'hosted-terminal'")
})

test("launchHostedTerminal reuses existing macOS terminal window when tmux already has a client", async () => {
  const spawns: Array<{ cmd: string; args: string[] }> = []

  mock.module("node:os", () => ({
    platform: () => "darwin",
  }))
  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      let onStdoutData: ((chunk: Buffer) => void) | undefined
      const child = {
        stdout: {
          on(event: string, handler: (data: Buffer) => void) {
            if (event === "data") onStdoutData = handler
          },
        },
        stderr: { on() {} },
        on(event: string, handler: (code?: number) => void) {
          if (event === "exit") {
            queueMicrotask(() => {
              if (cmd === "tmux" && args[2] === "list-clients") onStdoutData?.(Buffer.from("/dev/ttys001: cap-host\n"))
              handler(0)
            })
          }
          return child
        },
        unref() {},
      }
      return child
    },
  }))

  const launchModule = await import(`../src/proxy/launch?reuse-existing-client=${Date.now()}`)
  const launched = await launchModule.launchProxySession({
    executable: "codex-proxy",
    workerPort: 1234,
    profile: "cli",
    configDir: "/tmp/codex-config",
    mode: "hosted-terminal",
    sessionID: "hs_1",
    opener: "default",
    tmuxSocketName: "cap",
    tmuxHostSession: "cap-host",
  })

  expect(launched).toBe(true)
  expect(spawns).toEqual([
    {
      cmd: "codex-proxy",
      args: ["launch", "--worker", "1234", "--mode", "hosted-terminal", "--no-attach", "--profile", "cli", "--config-dir", "/tmp/codex-config", "--session-id", "hs_1"],
    },
    {
      cmd: "tmux",
      args: ["-L", "cap", "list-clients", "-t", "cap-host"],
    },
    {
      cmd: "osascript",
      args: ["-e", 'tell application "Terminal" to activate'],
    },
  ])
})

test("launch dialog prompts for mode before worker selection", async () => {
  const app = await mountHostedTerminalApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    return undefined
  })

  try {
    await app.openLaunchDialog()
    const frame = app.setup.captureCharFrame()
    expect(frame.includes("Launch Codex CLI")).toBe(true)
    expect(frame.includes("External window")).toBe(true)
    expect(frame.includes("Hosted terminal")).toBe(true)
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("launch dialog opens hosted terminal session menu", async () => {
  const app = await mountHostedTerminalApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions")
      return json({
        sessions: [
          {
            session_id: "hs_1",
            session_label: "solve problem A",
            worker_name: "test-cli",
            worker_port: 1234,
            status: "active",
            created_at: "2026-06-23T00:00:00Z",
            last_opened_at: "2026-06-23T00:00:00Z",
          },
        ],
      })
    return undefined
  })

  try {
    await app.openHostedTerminalPicker()
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("Create new session") && frame.includes("solve problem A")
    })
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})

test("stale hosted session cannot be opened", async () => {
  const app = await mountHostedTerminalApp((url) => {
    if (url.pathname === "/api/workers")
      return json({
        workers: [defaultWorker],
      })
    if (url.pathname === "/api/hosted-sessions")
      return json({
        sessions: [
          {
            session_id: "hs_1",
            session_label: "solve problem A",
            worker_name: "test-cli",
            worker_port: 1234,
            created_at: "2026-06-23T00:00:00Z",
            last_opened_at: "2026-06-23T00:00:00Z",
            status: "stale",
          },
        ],
      })
    return undefined
  })

  try {
    await app.openHostedTerminalPicker()
    await wait(async () => {
      await app.setup.renderOnce()
      const frame = app.setup.captureCharFrame()
      return frame.includes("Hosted Terminal") && frame.includes("solve problem A")
    })
  } finally {
    if (!app.setup.renderer.isDestroyed) app.setup.renderer.destroy()
    await app.cleanup()
  }
})
