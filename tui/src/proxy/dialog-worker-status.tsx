import { TextAttributes } from "@opentui/core"
import { For, Show, createMemo } from "solid-js"
import { useTheme } from "../context/theme"
import { useDialog } from "../ui/dialog"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSDK, type WorkerSummary } from "../context/sdk"
import { useSync } from "../context/sync"
import { useToast } from "../ui/toast"
import { DialogUpstreamPicker } from "./dialog-upstream-picker"
import { DialogLogs } from "./dialog-logs"
import { DialogModulePicker } from "./dialog-module"
import { DialogConfirm } from "../ui/dialog-confirm"

const LOG_LEVELS = ["simple", "detail"] as const
type LogLevel = (typeof LOG_LEVELS)[number]

export function DialogWorkerStatus(props: { worker: WorkerSummary; management?: boolean }) {
  const { theme } = useTheme()
  const dialog = useDialog()
  const sdk = useSDK()
  const sync = useSync()
  const toast = useToast()
  const modules = createMemo(() => Object.entries(props.worker.modules ?? {}))
  const hooks = createMemo(() => Object.entries(props.worker.hooks ?? {}))
  const hookStatusSummary = createMemo(() =>
    hooks()
      .map(([name]) => {
        const state = props.worker.hook_statuses?.[name]?.state
        return state ? `${name}: ${state}` : ""
      })
      .filter(Boolean)
      .join(" • "),
  )

  const logLevelAction: DialogSelectOption<string> = {
    title: "Log Level",
    value: "log_level",
    description: props.worker.log_level || "—",
    onSelect: async () => {
      const next = await new Promise<LogLevel | null>((resolve) => {
        dialog.replace(
          () => (
            <DialogSelect
              title={`Log Level: ${props.worker.name}`}
              options={LOG_LEVELS.map((level) => ({
                title: level,
                value: level,
                category: level === props.worker.log_level ? "Current" : "Options",
              }))}
              placeholder="Select log level..."
              current={props.worker.log_level}
              onSelect={(opt) => resolve(opt.value as LogLevel)}
            />
          ),
          () => resolve(null),
        )
      })
      if (!next || next === props.worker.log_level) {
        dialog.replace(() => <DialogWorkerStatus worker={props.worker} management />)
        return
      }
      try {
        await sdk.client.patchWorker(props.worker.port, { log_level: next })
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `Saved ${props.worker.name} log_level: ${next}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
      dialog.clear()
    },
  }

  const switchAction: DialogSelectOption<string> = {
      title: "Switch Upstream",
      value: "switch",
      description: props.worker.upstream.name,
      onSelect: () => dialog.replace(() => <DialogUpstreamPicker worker={props.worker} />),
  }

  const logsAction: DialogSelectOption<string> = {
    title: "View Logs",
    value: "logs",
    description: `:${props.worker.port}`,
    onSelect: () => dialog.replace(() => <DialogLogs worker={props.worker} />),
  }

  const modulesAction: DialogSelectOption<string> = {
      title: "Manage Modules",
      value: "modules",
      description: `${modules().length} req • ${hooks().length} hook`,
      onSelect: () => dialog.replace(() => <DialogModulePicker worker={props.worker} />),
  }

  const restartAction: DialogSelectOption<string> = {
    title: "Restart Worker",
    value: "restart",
    description: `:${props.worker.port}`,
    onSelect: async () => {
      try {
        await sdk.client.restartWorker(props.worker.port)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `Restarted ${props.worker.name}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
      dialog.clear()
    },
  }

  const stopAction: DialogSelectOption<string> = {
    title: "Stop Worker",
    value: "stop",
    description: `:${props.worker.port}`,
    onSelect: async () => {
      try {
        await sdk.client.stopWorker(props.worker.port)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `Stopped ${props.worker.name}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
      dialog.clear()
    },
  }

  const deleteAction: DialogSelectOption<string> = {
    title: "Delete Worker",
    value: "delete",
    description: `:${props.worker.port}`,
    onSelect: async () => {
      const confirmed = await DialogConfirm.show(
        dialog,
        "Delete worker",
        `Delete ${props.worker.name}? This will remove the worker config and stop the process.`,
      )
      if (!confirmed) {
        dialog.clear()
        return
      }
      try {
        await sdk.client.deleteWorker(props.worker.port)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `Deleted ${props.worker.name}`, variant: "success" })
      } catch (err) {
        toast.error(err)
      }
      dialog.clear()
    },
  }

  const actions = createMemo<DialogSelectOption<string>[]>(() =>
    props.management
      ? [logLevelAction, switchAction, modulesAction, logsAction, restartAction, stopAction, deleteAction]
      : [switchAction, logsAction, modulesAction],
  )

  return (
    <DialogSelect
      title={`${props.worker.name} (:${props.worker.port})`}
      options={actions()}
      placeholder="Worker actions..."
      footer={
        <box flexDirection="column" gap={1}>
          <text fg={theme.textMuted}>status: {props.worker.status}{hookStatusSummary() ? ` • ${hookStatusSummary()}` : ""}</text>
          <text fg={theme.textMuted}>upstream: {props.worker.upstream.name}</text>
          <text fg={theme.textMuted}>log level: {props.worker.log_level} • modules: {modules().length} req • {hooks().length} hook</text>
          <text fg={theme.textMuted}>snapshot: {props.worker.snapshot_generation}</text>
          <Show when={modules().length > 0} fallback={<text fg={theme.textMuted}>modules: none</text>}>
            <box flexDirection="column">
              <text fg={theme.text} attributes={TextAttributes.BOLD}>
                request middleware
              </text>
              <For each={modules()}>
                {([name, config]) => <text fg={theme.textMuted}>{config.enabled ? "✓" : "○"} {name}</text>}
              </For>
            </box>
          </Show>
          <Show when={hooks().length > 0} fallback={<text fg={theme.textMuted}>lifecycle hooks: none</text>}>
            <box flexDirection="column">
              <text fg={theme.text} attributes={TextAttributes.BOLD}>
                lifecycle hooks
              </text>
              <For each={hooks()}>
                {([name, config]) => {
                  const state = props.worker.hook_statuses?.[name]?.state
                  return <text fg={theme.textMuted}>{config.enabled ? "✓" : "○"} {name}{state ? `: ${state}` : ""}</text>
                }}
              </For>
            </box>
          </Show>
        </box>
      }
    />
  )
}
