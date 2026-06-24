import { spawn } from "node:child_process"
import { platform } from "node:os"
import { createTerminalActivateCommand, createTerminalOpenCommand } from "./terminal-opener"

export type LaunchMode = "external-window" | "hosted-terminal"

export type ProxyLaunchOptions = {
  executable?: string
  workerPort: number
  configDir?: string
  profile?: string
  workspace?: string
  addDirs?: string[]
  model?: string
  mode?: LaunchMode
  sessionID?: string
  sessionLabel?: string
  opener?: string
  tmuxSocketName?: string
  tmuxHostSession?: string
}

function shellQuote(value: string) {
  if (value === "") return "''"
  return "'" + value.replace(/'/g, `'\\''`) + "'"
}

export function createProxyLaunchCommand(opts: ProxyLaunchOptions) {
  const cmd = [opts.executable || "codex-proxy", "launch", "--worker", String(opts.workerPort)]
  if (opts.profile) {
    cmd.push("--profile", opts.profile)
  }
  if (opts.configDir) {
    cmd.push("--config-dir", opts.configDir)
  }
  if (opts.workspace) {
    cmd.push("--cd", opts.workspace)
  }
  for (const dir of opts.addDirs ?? []) {
    if (dir) cmd.push("--add-dir", dir)
  }
  if (opts.model) {
    cmd.push("--model", opts.model)
  }
  if (opts.mode === "hosted-terminal") {
    cmd.push("--mode", "hosted-terminal")
    if (opts.sessionLabel) cmd.push("--session-label", opts.sessionLabel)
    if (opts.sessionID) cmd.push("--session-id", opts.sessionID)
  }
  return cmd
}

export function renderProxyLaunchCommand(cmd: string[]) {
  return cmd.map(shellQuote).join(" ")
}

function runProcess(cmd: string, args: string[]): Promise<{ code: number; stdout: string; stderr: string }> {
  return new Promise((resolve) => {
    const child = spawn(cmd, args, { stdio: ["ignore", "pipe", "pipe"] })
    let stdout = ""
    let stderr = ""
    child.stdout?.on("data", (data: Buffer) => { stdout += data.toString() })
    child.stderr?.on("data", (data: Buffer) => { stderr += data.toString() })
    child.on("error", () => resolve({ code: 1, stdout, stderr: stderr || `failed to spawn ${cmd}` }))
    child.on("exit", (code) => resolve({ code: code ?? 1, stdout, stderr }))
  })
}

async function hasTmuxClient(socketName: string, hostSession: string) {
  const result = await runProcess("tmux", ["-L", socketName, "list-clients", "-t", hostSession])
  if (result.code !== 0) return false
  return result.stdout.trim() !== ""
}

export async function launchProxySession(opts: ProxyLaunchOptions) {
  if (opts.mode === "hosted-terminal") {
    return launchHostedTerminal(opts)
  }
  return launchExternalWindow(opts)
}

async function launchExternalWindow(opts: ProxyLaunchOptions) {
  const executable = opts.executable || "codex-proxy"
  const terminalCommand = renderProxyLaunchCommand(createProxyLaunchCommand(opts))
  const args = ["launch", "--worker", String(opts.workerPort)]
  if (opts.profile) {
    args.push("--profile", opts.profile)
  }
  if (opts.configDir) {
    args.push("--config-dir", opts.configDir)
  }
  if (opts.workspace) {
    args.push("--cd", opts.workspace)
  }
  for (const dir of opts.addDirs ?? []) {
    if (dir) args.push("--add-dir", dir)
  }
  if (opts.model) {
    args.push("--model", opts.model)
  }
  const openCommand = createTerminalOpenCommand({
    platform: platform(),
    opener: opts.opener || "default",
    command: terminalCommand,
  })
  const child = spawn(openCommand[0], openCommand.slice(1), {
    detached: true,
    stdio: "ignore",
  })
  child.unref()
  return true
}

// launchHostedTerminal ensures the tmux window is set up (non-interactive) and
// then attaches only when no client is already attached. This keeps a single
// Terminal.app window for all hosted sessions: the first launch opens it, later
// launches just switch the tmux window and focus the existing terminal.
async function launchHostedTerminal(opts: ProxyLaunchOptions) {
  const executable = opts.executable || "codex-proxy"
  const tmuxSocket = opts.tmuxSocketName || "cap"
  const tmuxHostSession = opts.tmuxHostSession || "cap-host"

  // Phase 1: set up tmux host + window without attaching.
  const setupArgs = ["launch", "--worker", String(opts.workerPort), "--mode", "hosted-terminal", "--no-attach"]
  if (opts.profile) {
    setupArgs.push("--profile", opts.profile)
  }
  if (opts.configDir) {
    setupArgs.push("--config-dir", opts.configDir)
  }
  if (opts.sessionLabel) {
    setupArgs.push("--session-label", opts.sessionLabel)
  }
  if (opts.sessionID) {
    setupArgs.push("--session-id", opts.sessionID)
  }
  if (opts.workspace) {
    setupArgs.push("--cd", opts.workspace)
  }
  for (const dir of opts.addDirs ?? []) {
    if (dir) setupArgs.push("--add-dir", dir)
  }
  if (opts.model) {
    setupArgs.push("--model", opts.model)
  }
  const setup = await runProcess(executable, setupArgs)
  if (setup.code !== 0) {
    throw new Error(setup.stderr || `codex-proxy launch exited with code ${setup.code}`)
  }

  if (await hasTmuxClient(tmuxSocket, tmuxHostSession)) {
    const activateCommand = createTerminalActivateCommand({
      platform: platform(),
      opener: opts.opener || "default",
    })
    if (!activateCommand) return true
    const child = spawn(activateCommand[0], activateCommand.slice(1), {
      detached: true,
      stdio: "ignore",
    })
    child.unref()
    return true
  }

  const attachCommand = `tmux -L ${tmuxSocket} attach-session -t ${tmuxHostSession}`
  const openCommand = createTerminalOpenCommand({
    platform: platform(),
    opener: opts.opener || "default",
    command: attachCommand,
  })
  const child = spawn(openCommand[0], openCommand.slice(1), {
    detached: true,
    stdio: "ignore",
  })
  child.unref()
  return true
}
