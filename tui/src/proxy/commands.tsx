import type { TuiPluginApi } from "@codex-proxy/plugin/tui"
import { DialogConfig } from "./dialog-config"
import { DialogLogs } from "./dialog-logs"
import { DialogModules } from "./dialog-modules"
import { DialogStatus } from "./dialog-status"
import { showNewWorkerDialog } from "./dialog-new-worker"
import { DialogUpstream } from "./dialog-upstream"
import { DialogWorkerPicker } from "./dialog-worker-picker"
import { DialogWorkers } from "./dialog-workers"
import { DialogLaunch } from "./dialog-launch"

type WorkerClient = {
  createWorker(input: { name: string; port: number; upstream: string }): Promise<unknown>
}

type DialogLike = {
  clear(): void
  replace(input: any, onClose?: () => void): void
  push(input: any, onClose?: () => void): void
}

type ToastLike = {
  show(input: { title?: string; message: string; variant?: "info" | "success" | "warning" | "error"; duration?: number }): void
  error(error: unknown): void
}

export function registerProxyCommands(api: TuiPluginApi) {
  return api.keymap.registerLayer({
    commands: [
      {
        namespace: "palette",
        name: "proxy.upstream",
        title: "Manage upstreams",
        category: "Proxy",
        slashName: "upstream",
        run() {
          api.ui.dialog.replace(() => <DialogUpstream />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.workers",
        title: "Manage workers",
        category: "Proxy",
        slashName: "workers",
        run() {
          api.ui.dialog.replace(() => <DialogWorkers />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.status",
        title: "Show worker status",
        category: "Proxy",
        slashName: "status",
        run() {
          api.ui.dialog.replace(() => <DialogStatus />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.logs",
        title: "View worker logs",
        category: "Proxy",
        slashName: "logs",
        async run() {
          api.ui.dialog.replace(() => (
            <DialogWorkerPicker
              title="Worker Logs"
              placeholder="Search workers..."
              onSelect={async (worker) => {
                const initialLines = await (api.client as unknown as { getLogs(port: number): Promise<string[]> }).getLogs(
                  worker.port,
                )
                api.ui.dialog.push(() => <DialogLogs worker={worker} initialLines={initialLines} />)
              }}
            />
          ))
        },
      },
      {
        namespace: "palette",
        name: "proxy.config",
        title: "View proxy config status",
        category: "Proxy",
        slashName: "config",
        run() {
          api.ui.dialog.replace(() => <DialogConfig />)
        },
      },
      {
        namespace: "palette",
        name: "proxy.modules",
        title: "Manage worker modules",
        category: "Proxy",
        slashName: "modules",
        run() {
          api.ui.dialog.replace(() => (
            <DialogWorkerPicker
              title="Worker Modules"
              placeholder="Search workers..."
              onSelect={(worker) => {
                api.ui.dialog.replace(() => <DialogModules worker={worker} />)
              }}
            />
          ))
        },
      },
      {
        namespace: "palette",
        name: "proxy.new",
        title: "Create worker",
        category: "Proxy",
        slashName: "new-worker",
        run() {
          const toast: ToastLike = {
            show(input) {
              api.ui.toast(input)
            },
            error(error) {
              api.ui.toast({
                title: "Error",
                message: error instanceof Error ? error.message : String(error),
                variant: "error",
              })
            },
          }
          void showNewWorkerDialog(
            api.ui.dialog as DialogLike as never,
            api.client as unknown as WorkerClient as never,
            toast as never,
          )
        },
      },
      {
        namespace: "palette",
        name: "proxy.launch",
        title: "Launch Codex CLI",
        category: "Proxy",
        slashName: "launch",
        run() {
          api.ui.dialog.replace(() => <DialogLaunch />)
        },
      },
    ],
    bindings: [],
  })
}
