import { createMemo, createSignal } from "solid-js"
import { DialogConfirm } from "../ui/dialog-confirm"
import { DialogPrompt } from "../ui/dialog-prompt"
import { DialogSelect, type DialogSelectOption } from "../ui/dialog-select"
import { useDialog } from "../ui/dialog"
import { useSDK } from "../context/sdk"
import { useSync } from "../context/sync"
import { useTheme } from "../context/theme"
import { useToast } from "../ui/toast"

type UpstreamOption = { type: "create" } | { type: "edit"; name: string }
type FieldKey = "base_url" | "api_key" | "api_format"

type Draft = {
  base_url: string
  api_key: string
  api_format: string
  has_api_key: boolean
}

type Field = {
  key: FieldKey
  title: string
  placeholder: string
  hidden?: boolean
}

const FIELDS: Field[] = [
  { key: "base_url", title: "Base URL", placeholder: "https://example.com/v1" },
  { key: "api_key", title: "API Key", placeholder: "sk-...", hidden: true },
  { key: "api_format", title: "API Format", placeholder: "responses or chat_completions" },
]

export function DialogUpstream() {
  const sync = useSync()
  const dialog = useDialog()
  const toast = useToast()

  const options = createMemo<DialogSelectOption<UpstreamOption>[]>(() => [
    { title: "Create New Upstream", value: { type: "create" }, description: "Add a relay endpoint", category: "Actions" },
    ...sync.data.upstreams.map((upstream) => ({
      title: upstream.name,
      value: { type: "edit" as const, name: upstream.name },
      description: `${upstream.base_url}${upstream.has_api_key ? "" : " (no key)"}`,
      category: "Configured upstreams",
    })),
  ])

  return (
    <DialogSelect
      title="Manage Upstreams"
      options={options()}
      placeholder="Search upstreams..."
      onSelect={async (opt) => {
        if (opt.value.type === "create") {
          const name = await DialogPrompt.show(dialog, "New Upstream Name", { placeholder: "e.g. groq" })
          if (name === null) return
          const upstreamName = name.trim()
          if (!upstreamName || upstreamName.includes("/")) {
            toast.show({ message: "Invalid upstream name", variant: "error" })
            dialog.clear()
            return
          }
          dialog.push(() => <DialogUpstreamEditor name={upstreamName} draft={{ base_url: "", api_key: "", api_format: "chat_completions", has_api_key: false }} mode="created" />)
          return
        }

        const upstream = sync.data.upstreams.find((item) => item.name === opt.value.name)
        if (!upstream) return
        dialog.push(() => (
          <DialogUpstreamEditor
            name={upstream.name}
            draft={{
              base_url: upstream.base_url,
              api_key: "",
              api_format: upstream.api_format ?? "",
              has_api_key: upstream.has_api_key,
            }}
            mode="saved"
          />
        ))
      }}
    />
  )
}

function DialogUpstreamEditor(props: { name: string; draft: Draft; mode: "created" | "saved" }) {
  const sync = useSync()
  const sdk = useSDK()
  const dialog = useDialog()
  const toast = useToast()
  const [draft, setDraft] = createSignal(props.draft)

  const options = createMemo<DialogSelectOption<FieldKey>[]>(() =>
    FIELDS.map((field) => ({
      title: field.title,
      value: field.key,
      description: describe(field, draft()),
      category: "Fields",
      onSelect: async () => {
        const patch = await editField(dialog, field, draft())
        if (!patch) return
        const updated = { ...draft(), ...patch, has_api_key: patch.api_key === undefined ? draft().has_api_key : patch.api_key !== "" }
        setDraft(updated)
        await sdk.client.patchUpstream(props.name, patch)
        await sync.bootstrap({ fatal: false })
        toast.show({ message: `${props.mode === "created" ? "Created" : "Saved"} upstream ${props.name}`, variant: "success" })
      },
    })),
  )

  return <DialogSelect title={`Edit Upstream: ${props.name}`} options={options()} placeholder="Select a field..." footer={<EscHint dialog={dialog} />} />
}

function EscHint(props: { dialog: ReturnType<typeof useDialog> }) {
  const { theme } = useTheme()
  const label = () => (props.dialog.stack.length > 1 ? "back" : "close")
  return (
    <text fg={theme.textMuted}>
      esc <span style={{ fg: theme.text }}>{label()}</span>
    </text>
  )
}

function describe(field: Field, draft: Draft) {
  if (field.hidden) return draft.has_api_key ? "******" : "none"
  return draft[field.key] || "—"
}

async function editField(dialog: ReturnType<typeof useDialog>, field: Field, draft: Draft) {
  if (field.hidden) {
    let dirty = false
    let value = draft.api_key
    const result = await DialogPrompt.show(dialog, `${field.title}: ${draft.base_url || "upstream"}`, {
      value: draft.has_api_key ? "******" : "",
      placeholder: field.placeholder,
      onInputChange(next) {
        value = next
        dirty = true
      },
    })
    if (result === null) {
      if (!dirty) return
      const save = await DialogConfirm.show(dialog, "Save API Key", "Save the edited API key?")
      if (save !== true) return
    }
    if (!dirty) return
    return { api_key: value === "******" ? "" : value }
  }

  const result = await DialogPrompt.show(dialog, `${field.title}: ${draft.base_url || "upstream"}`, {
    value: draft[field.key],
    placeholder: field.placeholder,
  })
  if (result === null) return
  return { [field.key]: result } as Partial<Draft>
}
