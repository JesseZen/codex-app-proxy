import { TextareaRenderable, TextAttributes } from "@opentui/core"
import { useTheme } from "../context/theme"
import { useDialog, type DialogContext } from "./dialog"
import { Show, createEffect, createResource, createSignal, Index, onMount, type JSX } from "solid-js"
import { Spinner } from "../component/spinner"
import { useTuiConfig } from "../config"
import { useBindings, useCommandShortcut } from "../keymap"
import { getDirectoryCompletions } from "../util/directory-completion"

export type DialogPromptProps = {
  title: string
  description?: () => JSX.Element
  placeholder?: string
  value?: string
  selectAll?: boolean
  busy?: boolean
  busyText?: string
  directoryCompletion?: {
    basePath: string
    maxResults?: number
  }
  onConfirm?: (value: string) => void
  onCancel?: () => void
  onInputChange?: (value: string) => void
}

export function DialogPrompt(props: DialogPromptProps) {
  const dialog = useDialog()
  const { theme } = useTheme()
  const tuiConfig = useTuiConfig()
  const submitShortcut = useCommandShortcut("dialog.prompt.submit")
  const [textareaTarget, setTextareaTarget] = createSignal<TextareaRenderable>()
  let textarea: TextareaRenderable

  const [query, setQuery] = createSignal(props.value ?? "")
  const [selected, setSelected] = createSignal(0)
  const [suggestions] = createResource(
    () => props.directoryCompletion ? { query: query(), basePath: props.directoryCompletion.basePath, maxResults: props.directoryCompletion.maxResults } : undefined,
    (input) => input ? getDirectoryCompletions(input) : Promise.resolve([]),
    { initialValue: [] },
  )

  function confirm() {
    if (props.busy) return
    props.onConfirm?.(textarea.plainText)
  }

  useBindings(() => ({
    target: textareaTarget,
    enabled: textareaTarget() !== undefined && !props.busy,
    // Dialog form semantics must win over the global managed textarea input layer.
    priority: 1,
    commands: [
      {
        name: "dialog.prompt.submit",
        title: "Submit dialog prompt",
        category: "Dialog",
        run: confirm,
      },
      ...(props.directoryCompletion
        ? [
            {
              name: "dialog.select.prev",
              title: "Previous dialog item",
              category: "Dialog",
              run() {
                if (suggestions().length) setSelected((value) => value > 0 ? value - 1 : suggestions().length - 1)
              },
            },
            {
              name: "dialog.select.next",
              title: "Next dialog item",
              category: "Dialog",
              run() {
                if (suggestions().length) setSelected((value) => value + 1 < suggestions().length ? value + 1 : 0)
              },
            },
            {
              name: "prompt.autocomplete.complete",
              title: "Complete dialog directory",
              category: "Dialog",
              run() {
                const next = suggestions()[selected()] ?? suggestions()[0]
                if (!next) return
                textarea.setText(next.value)
                textarea.gotoLineEnd()
                setQuery(next.value)
              },
            },
          ]
        : []),
    ],
    bindings: [
      ...tuiConfig.keybinds.gather("dialog.prompt", ["dialog.prompt.submit"]),
      ...(props.directoryCompletion
        ? [
            ...tuiConfig.keybinds.gather("dialog.select", ["dialog.select.prev", "dialog.select.next"]),
            ...tuiConfig.keybinds.gather("prompt.autocomplete", ["prompt.autocomplete.complete"]),
          ]
        : []),
    ],
  }))

  onMount(() => {
    dialog.setSize("medium")
    setTimeout(() => {
      if (!textarea || textarea.isDestroyed) return
      if (props.busy) return
      textarea.focus()
      if (props.selectAll) {
        textarea.selectAll()
        return
      }
      textarea.gotoLineEnd()
    }, 1)
  })

  createEffect(() => {
    if (!textarea || textarea.isDestroyed) return
    const traits = props.busy
      ? {
          suspend: true,
          status: "BUSY",
        }
      : {}
    textarea.traits = traits
    if (props.busy) {
      textarea.blur()
      return
    }
    textarea.focus()
  })

  createEffect(() => {
    suggestions()
    setSelected(0)
  })

  return (
    <box paddingLeft={2} paddingRight={2} gap={1}>
      <box flexDirection="row" justifyContent="space-between">
        <text attributes={TextAttributes.BOLD} fg={theme.text}>
          {props.title}
        </text>
        <text fg={theme.textMuted} onMouseUp={() => dialog.pop()}>
          esc
        </text>
      </box>
      <box gap={1}>
        {props.description}
        <textarea
          height={3}
          ref={(val: TextareaRenderable) => {
            textarea = val
            setTextareaTarget(val)
          }}
          initialValue={props.value}
          placeholder={props.placeholder ?? "Enter text"}
          placeholderColor={theme.textMuted}
          textColor={props.busy ? theme.textMuted : theme.text}
          focusedTextColor={props.busy ? theme.textMuted : theme.text}
          cursorColor={props.busy ? theme.backgroundElement : theme.text}
          onContentChange={() => {
            setQuery(textarea.plainText)
            props.onInputChange?.(textarea.plainText)
          }}
        />
        <Show when={props.busy}>
          <Spinner color={theme.textMuted}>{props.busyText ?? "Working..."}</Spinner>
        </Show>
        <Show when={props.directoryCompletion}>
          <box flexDirection="column">
            <Index each={suggestions()} fallback={<text fg={theme.textMuted}>No matching directories</text>}>
              {(item, index) => (
                <box backgroundColor={index === selected() ? theme.primary : undefined}>
                  <text fg={index === selected() ? theme.backgroundPanel : theme.text}>{item().display}</text>
                </box>
              )}
            </Index>
          </box>
        </Show>
      </box>
      <box paddingBottom={1} gap={1} flexDirection="row">
        <Show when={!props.busy} fallback={<text fg={theme.textMuted}>processing...</text>}>
          <Show when={submitShortcut()}>
            <text fg={theme.text}>
              {submitShortcut()} <span style={{ fg: theme.textMuted }}>submit</span>
            </text>
          </Show>
        </Show>
      </box>
    </box>
  )
}

DialogPrompt.show = (dialog: DialogContext, title: string, options?: Omit<DialogPromptProps, "title">) => {
  return new Promise<string | null>((resolve) => {
    dialog.push(
      () => (
        <DialogPrompt
          title={title}
          {...options}
          onConfirm={(value) => {
            resolve(value)
            dialog.pop()
          }}
          onCancel={() => resolve(null)}
        />
      ),
      () => resolve(null),
    )
  })
}
