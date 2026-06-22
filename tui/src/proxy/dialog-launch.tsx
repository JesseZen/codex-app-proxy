import { createMemo } from "solid-js"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useSync } from "../context/sync"
import { useDialog } from "../ui/dialog"
import { DialogPrompt } from "../ui/dialog-prompt"
import { useClipboard } from "../context/clipboard"
import { DialogAlert } from "../ui/dialog-alert"
import { useProject } from "../context/project"
import { createProxyLaunchCommand, launchProxySession, renderProxyLaunchCommand } from "./launch"

export function DialogLaunch() {
  const sync = useSync()
  const project = useProject()
  const dialog = useDialog()
  const clipboard = useClipboard()

  const options = createMemo<DialogSelectOption<string>[]>(() =>
    sync.data.workers
      .filter((worker) => worker.role === "cli")
      .map((worker) => ({
        title: worker.name,
        value: worker.name,
        description: `:${worker.port} • ${worker.upstream.name}`,
        category: worker.status === "running" ? "Running cli workers" : "Stopped cli workers",
      })),
  )

  async function launch(workerName: string) {
    const worker = sync.data.workers.find((item) => item.name === workerName)
    if (!worker) return
    const basePath = project.instance.directory() || sync.path.directory
    const workspace = await DialogPrompt.show(dialog, "Launch Codex", {
      placeholder: "Workspace directory",
      description: () => <text>Launch Codex in this workspace.</text>,
      value: basePath,
      directoryCompletion: basePath
        ? {
            basePath,
          }
        : undefined,
    })
    if (workspace === null) return
    dialog.clear()
    const command = createProxyLaunchCommand({
      workerPort: worker.port,
      profile: worker.name,
      workspace: workspace || undefined,
    })
    const rendered = renderProxyLaunchCommand(command)
    await clipboard.write?.(rendered).catch(() => undefined)
    const launched = await launchProxySession({
      executable: import.meta.env?.CODEX_PROXY_EXECUTABLE || undefined,
      workerPort: worker.port,
      profile: worker.name,
      workspace: workspace || undefined,
    })
    if (!launched) {
      await DialogAlert.show(dialog, "Launch Command", rendered)
      return
    }
    await DialogAlert.show(dialog, "Launch", "Opened a new Codex session.")
  }

  return (
    <DialogSelect
      title="Launch Codex CLI"
      options={options()}
      placeholder="Search cli workers..."
      onSelect={(option) => {
        void launch(option.value)
      }}
    />
  )
}
