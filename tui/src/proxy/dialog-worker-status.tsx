import { TextAttributes } from "@opentui/core"
import { For, Show, createMemo } from "solid-js"
import { useTheme } from "../context/theme"
import { useDialog } from "../ui/dialog"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import type { WorkerSummary } from "../context/sdk"
import { DialogUpstreamPicker } from "./dialog-upstream-picker"
import { DialogLogs } from "./dialog-logs"
import { DialogModulePicker } from "./dialog-module"

export function DialogWorkerStatus(props: { worker: WorkerSummary }) {
  const { theme } = useTheme()
  const dialog = useDialog()
  const modules = createMemo(() => Object.entries(props.worker.modules ?? {}))
  const actions = createMemo<DialogSelectOption<string>[]>(() => [
    {
      title: "Switch Upstream",
      value: "switch",
      description: props.worker.upstream.name,
      category: "Actions",
      onSelect: () => dialog.replace(() => <DialogUpstreamPicker worker={props.worker} />),
    },
    {
      title: "View Logs",
      value: "logs",
      description: `:${props.worker.port}`,
      category: "Actions",
      onSelect: () => dialog.replace(() => <DialogLogs worker={props.worker} />),
    },
    {
      title: "Manage Modules",
      value: "modules",
      description: `${modules().length}`,
      category: "Actions",
      onSelect: () => dialog.replace(() => <DialogModulePicker worker={props.worker} />),
    },
  ])

  return (
    <DialogSelect
      title={`${props.worker.name} (:${props.worker.port})`}
      options={actions()}
      placeholder="Worker actions..."
      footer={
        <box flexDirection="column" gap={1}>
          <text fg={theme.textMuted}>status: {props.worker.status}</text>
          <text fg={theme.textMuted}>upstream: {props.worker.upstream.name}</text>
          <text fg={theme.textMuted}>snapshot: {props.worker.snapshot_generation}</text>
          <text fg={theme.textMuted}>log level: {props.worker.log_level}</text>
          <Show when={modules().length > 0} fallback={<text fg={theme.textMuted}>modules: none</text>}>
            <box flexDirection="column">
              <text fg={theme.text} attributes={TextAttributes.BOLD}>
                modules
              </text>
              <For each={modules()}>
                {([name, config]) => <text fg={theme.textMuted}>{config.enabled ? "✓" : "○"} {name}</text>}
              </For>
            </box>
          </Show>
        </box>
      }
    />
  )
}
