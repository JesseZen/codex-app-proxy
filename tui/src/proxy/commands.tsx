import type { TuiPluginApi } from "@agent-inn/plugin/tui"
import { DialogSettings } from "./dialog-settings"
import { DialogLogs } from "./dialog-logs"
import { DialogTopology } from "./dialog-topology"
import { DialogUpstream } from "./dialog-upstream"
import { DialogWorkerPicker } from "./dialog-worker-picker"
import { DialogWorkers } from "./dialog-workers"
import { DialogLaunch } from "./dialog-launch"

export function registerProxyCommands(api: TuiPluginApi) {
  return api.keymap.registerLayer({
    commands: [
      {
        namespace: "palette",
        name: "proxy.upstreams",
        title: "Manage upstreams",
        category: "Proxy",
        slashName: "upstreams",
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
        name: "proxy.settings",
        title: "View proxy settings",
        category: "Proxy",
        slashName: "settings",
        slashAliases: ["config"],
        run() {
          api.ui.dialog.replace(() => <DialogSettings />)
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
      {
        namespace: "palette",
        name: "proxy.topology",
        title: "View topology",
        category: "Proxy",
        slashName: "topology",
        run() {
          api.ui.dialog.replace(() => <DialogTopology />)
        },
      },
    ],
    bindings: [],
  })
}
