import { ScrollBoxRenderable, TextAttributes } from "@opentui/core"
import { useSDK } from "../context/sdk"
import { EscHint, useDialog } from "../ui/dialog"
import { useTheme } from "../context/theme"
import { useTerminalDimensions } from "@opentui/solid"
import { createSignal, onMount, onCleanup, For, Show, batch } from "solid-js"
import type { WorkerSummary } from "../context/sdk"

export function DialogLogs(props: { worker: WorkerSummary; initialLines?: string[] }) {
  const sdk = useSDK()
  const dialog = useDialog()
  const { theme } = useTheme()
  const dimensions = useTerminalDimensions()
  const [lines, setLines] = createSignal<string[]>(props.initialLines ?? [])
  const [loading, setLoading] = createSignal(true)
  const [connected, setConnected] = createSignal(false)

  onMount(async () => {
    dialog.setSize("large")
    try {
      if (!props.initialLines) {
        const existing = await sdk.client.getLogs(props.worker.port)
        setLines(existing)
      }
    } catch {
      // may have no logs yet
    } finally {
      setLoading(false)
    }

    // Connect to SSE log stream
    const url = sdk.client.logsUrl(props.worker.port)
    const controller = new AbortController()
    setConnected(true)

    sdk.fetch(url, { signal: controller.signal, headers: { Accept: "text/event-stream" } })
      .then(async (res) => {
        if (!res.ok || !res.body) return
        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ""
        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })
          const splitLines = buffer.split("\n")
          buffer = splitLines.pop() ?? ""
          const incoming: string[] = []
          for (const line of splitLines) {
            if (line.startsWith("data: ")) {
              const payload = JSON.parse(line.slice(6)) as { line?: string }
              if (typeof payload.line !== "string") continue
              incoming.push(payload.line)
            }
          }
          if (incoming.length > 0) {
            const take = Math.min(incoming.length, 500)
            batch(() => {
              setLines((prev) => [...prev.slice(-(500 - take)), ...incoming.slice(-take)])
            })
          }
        }
      })
      .catch(() => {})
      .finally(() => setConnected(false))

    onCleanup(() => controller.abort())
  })

  const height = () => Math.min(dimensions().height - 8, 30)

  return (
    <box paddingBottom={1} flexDirection="column" gap={1}>
      <box paddingLeft={2} flexDirection="row" justifyContent="space-between">
        <box flexDirection="row" gap={1}>
          <text fg={theme.text} attributes={TextAttributes.BOLD}>
            Logs: {props.worker.name} (:{props.worker.port})
          </text>
          <text fg={theme.textMuted}>{connected() ? "● live" : "○ disconnected"}</text>
        </box>
        <EscHint dialog={dialog} />
      </box>
      <scrollbox height={height()} paddingLeft={1} paddingRight={1}>
        <For each={lines()}>
          {(line) => <text fg={theme.textMuted}>{line}</text>}
        </For>
        <Show when={!loading() && lines().length === 0}>
          <text fg={theme.textMuted}>No logs yet</text>
        </Show>
      </scrollbox>
    </box>
  )
}
