import { expect, test } from "bun:test"
import { createTerminalActivateCommand, createTerminalOpenCommand } from "../src/proxy/terminal-opener"

test("default opener resolves to Terminal.app on macOS", () => {
  const command = createTerminalOpenCommand({
    platform: "darwin",
    opener: "default",
    command: "codex-proxy launch --worker 1234",
  })

  expect(command).toEqual([
    "osascript",
    "-e",
    'tell application "Terminal" to do script "codex-proxy launch --worker 1234"',
  ])
})

test("iterm2 opener resolves on macOS", () => {
  const command = createTerminalOpenCommand({
    platform: "darwin",
    opener: "iterm2",
    command: "tmux -L cap attach-session -t cap-host",
  })

  expect(command).toEqual([
    "osascript",
    "-e",
    `tell application "iTerm2"
activate
set newWindow to (create window with default profile)
tell current session of current tab of newWindow
write text "tmux -L cap attach-session -t cap-host"
end tell
end tell`,
  ])
})

test("default opener activates Terminal.app on macOS", () => {
  const command = createTerminalActivateCommand({
    platform: "darwin",
    opener: "default",
  })

  expect(command).toEqual(["osascript", "-e", 'tell application "Terminal" to activate'])
})

test("iterm2 opener activates existing iTerm2 window on macOS", () => {
  const command = createTerminalActivateCommand({
    platform: "darwin",
    opener: "iterm2",
  })

  expect(command).toEqual(["osascript", "-e", 'tell application "iTerm2" to activate'])
})

test("default opener resolves to x-terminal-emulator on linux", () => {
  const command = createTerminalOpenCommand({
    platform: "linux",
    opener: "default",
    command: "codex-proxy launch --worker 1234",
  })

  expect(command).toEqual(["x-terminal-emulator", "-e", "codex-proxy launch --worker 1234"])
})

test("default opener does not provide activate command on linux", () => {
  const command = createTerminalActivateCommand({
    platform: "linux",
    opener: "default",
  })

  expect(command).toBeUndefined()
})
