function escapeAppleScript(value: string) {
  return value.replace(/\\/g, "\\\\").replace(/"/g, '\\"')
}

type TerminalOpenCommandInput = {
  platform: NodeJS.Platform
  opener: string
  command: string
}

type TerminalActivateCommandInput = {
  platform: NodeJS.Platform
  opener: string
}

export function createTerminalOpenCommand(input: TerminalOpenCommandInput) {
  const opener = input.opener || "default"
  if (input.platform === "darwin") {
    if (opener === "iterm2") {
      return [
        "osascript",
        "-e",
        `tell application "iTerm2"
activate
set newWindow to (create window with default profile)
tell current session of current tab of newWindow
write text "${escapeAppleScript(input.command)}"
end tell
end tell`,
      ]
    }
    return [
      "osascript",
      "-e",
      `tell application "Terminal" to do script "${escapeAppleScript(input.command)}"`,
    ]
  }

  switch (opener) {
    case "kitty":
      return ["kitty", input.command]
    case "wezterm":
      return ["wezterm", "start", "--always-new-process", "--", "sh", "-lc", input.command]
    default:
      return ["x-terminal-emulator", "-e", input.command]
  }
}

export function createTerminalActivateCommand(input: TerminalActivateCommandInput) {
  const opener = input.opener || "default"
  if (input.platform !== "darwin") return undefined
  if (opener === "iterm2") {
    return ["osascript", "-e", 'tell application "iTerm2" to activate']
  }
  return ["osascript", "-e", 'tell application "Terminal" to activate']
}
