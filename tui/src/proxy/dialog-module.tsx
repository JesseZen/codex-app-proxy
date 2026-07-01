import { createMemo, createSignal } from "solid-js"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useDialog, EscHint } from "../ui/dialog"
import { useSDK, type WorkerDetail, type WorkerSummary } from "../context/sdk"
import { useSync } from "../context/sync"
import { useToast } from "../ui/toast"

type ModuleKey = "enabled" | "model" | "api_format" | "config_path" | "state_dir"

type ModuleField = {
  key: ModuleKey
  title: string
  placeholder: string
}

const MODULE_FIELDS: Record<string, ModuleField[]> = {
  model_override: [{ key: "model", title: "Model", placeholder: "gpt-4o" }],
  api_translate: [{ key: "api_format", title: "API Format", placeholder: "responses or chat_completions" }],
  config_patch: [
    { key: "config_path", title: "Config Path", placeholder: "~/.codex/config.toml" },
    { key: "state_dir", title: "State Dir", placeholder: "~/.ainn" },
  ],
}

export function DialogModulePicker(props: { worker: WorkerSummary }) {
  const sdk = useSDK()
  const sync = useSync()
  const dialog = useDialog()
  const options = createMemo<DialogSelectOption<string>[]>(() => {
    const plugins = sync.data.manager_config.plugins ?? {}
    const configuredRequestModules = Object.entries(props.worker.modules ?? {}).filter(
      ([name]) => plugins[name]?.kind === "request_middleware",
    )
    const configuredRequestNames = new Set(configuredRequestModules.map(([name]) => name))
    const availableRequestModules = Object.entries(plugins)
      .filter(([name, definition]) => definition.kind === "request_middleware" && !configuredRequestNames.has(name))
      .map(([name]) => [name, { enabled: false }] as const)
    const hooks = [
      ...Object.entries(props.worker.hooks ?? {}).filter(([name]) => plugins[name]?.kind === "lifecycle_hook"),
      ...Object.entries(plugins)
        .filter(([name, definition]) => definition.kind === "lifecycle_hook" && props.worker.hooks?.[name] === undefined)
        .map(([name]) => [name, { enabled: false }] as const),
    ]
    return [
      ...configuredRequestModules.map(([name, cfg]) => ({
        title: `${cfg.enabled ? "✓" : "○"} ${name}`,
        value: name,
        description: describeModule(name, cfg.params ?? {}),
        category: "Request Middleware",
        onSelect: async () => {
          const worker = await sdk.client.getWorker(props.worker.port)
          dialog.push(() => <DialogModuleEditor worker={worker} moduleName={name} />)
        },
      })),
      ...availableRequestModules.map(([name, cfg]) => ({
        title: `${cfg.enabled ? "✓" : "○"} ${name}`,
        value: name,
        description: describeModule(name, cfg.params ?? {}),
        category: "Request Middleware",
        onSelect: async () => {
          const worker = await sdk.client.getWorker(props.worker.port)
          dialog.push(() => <DialogModuleEditor worker={worker} moduleName={name} />)
        },
      })),
      ...hooks.map(([name, cfg]) => ({
        title: `${cfg.enabled ? "✓" : "○"} ${name}`,
        value: name,
        description: describeModule(name, cfg.params ?? {}),
        category: "Lifecycle Hooks",
        onSelect: async () => {
          const worker = await sdk.client.getWorker(props.worker.port)
          dialog.push(() => <DialogModuleEditor worker={worker} moduleName={name} />)
        },
      })),
    ]
  })

  return (
    <DialogSelect
      title={`Modules & Hooks: ${props.worker.name} (:${props.worker.port})`}
      options={options()}
      placeholder="Search modules..."
      footer={<EscHint dialog={dialog} />}
    />
  )
}

function DialogModuleEditor(props: { worker: WorkerDetail; moduleName: string }) {
  const dialog = useDialog()
  const sdk = useSDK()
  const sync = useSync()
  const toast = useToast()
  const [draft, setDraft] = createSignal(props.worker.modules?.[props.moduleName] ?? props.worker.hooks?.[props.moduleName] ?? { enabled: false })

  const options = createMemo<DialogSelectOption<ModuleKey>[]>(() => {
    const cfg = draft()
    const fields = MODULE_FIELDS[props.moduleName] ?? []
    return [
      {
        title: cfg.enabled ? "Disable" : "Enable",
        value: "enabled",
        description: cfg.enabled ? "enabled" : "disabled",
        category: "Actions",
        onSelect: async () => {
          await saveModule({ enabled: !cfg.enabled, params: cfg.params })
        },
      },
      ...fields.map((field) => ({
        title: field.title,
        value: field.key,
        description: describeField(cfg.params ?? {}, field.key),
        category: "Fields",
        onSelect: async () => {
          const next = await DialogPrompt.show(dialog, `${field.title}: ${props.moduleName}`, {
            placeholder: field.placeholder,
          })
          if (next === null) return
          const params = { ...(cfg.params ?? {}), [field.key]: next }
          await saveModule({ enabled: cfg.enabled, params })
        },
      })),
    ]
  })

  async function saveModule(next: { enabled: boolean; params?: Record<string, unknown> }) {
    try {
      const result = await sdk.client.patchModule(props.worker.port, props.moduleName, next)
      setDraft({
        enabled: result.module.enabled,
        params: result.module.params,
      })
      await sync.bootstrap({ fatal: false })
      toast.show({ message: `Saved ${props.moduleName}`, variant: "success" })
      dialog.pop()
      dialog.pop()
    } catch (err) {
      toast.error(err)
    }
  }

  return (
    <DialogSelect
      title={`Edit Module: ${props.worker.name}`}
      options={options()}
      placeholder="Select a field..."
      footer={<EscHint dialog={dialog} />}
    />
  )
}

function describeModule(name: string, params: Record<string, unknown>) {
  if (name === "model_override") return String(params.model ?? "—")
  if (name === "api_translate") return String(params.api_format ?? "—")
  if (name === "config_patch") return [String(params.config_path ?? "—"), String(params.state_dir ?? "—")].join(" • ")
  return "—"
}

function describeField(params: Record<string, unknown>, key: string) {
  const value = params[key]
  return value === undefined || value === "" ? "—" : String(value)
}
