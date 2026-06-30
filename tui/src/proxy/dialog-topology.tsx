import { TextAttributes, type RGBA } from "@opentui/core"
import { createEffect, createMemo, createSignal, For, Show } from "solid-js"
import { useTheme, type Theme } from "../context/theme"
import { EscHint, useDialog } from "../ui/dialog"
import { useSync } from "../context/sync"
import { useSDK } from "../context/sdk"
import { useToast } from "../ui/toast"
import { DialogWorkerStatus } from "./dialog-worker-status"
import { DialogUpstreamEditor } from "./dialog-upstream"
import { computeLayout, TOPOLOGY_COL_GAP, TOPOLOGY_GROUP_GAP, type TopologyGroup, type TopologyNode } from "./topology/layout"
import { computeGroupEdges } from "./topology/edges"
import { isValidDrop, toDropPair, dropLabel } from "./topology/drag"
import type { WorkerSummary, RedactedUpstream } from "./backend"

export function DialogTopology() {
  const sync = useSync()
  const dialog = useDialog()
  const sdk = useSDK()
  const toast = useToast()
  const { theme } = useTheme()
  const [hovered, setHovered] = createSignal<string | null>(null)
  const [dragSource, setDragSource] = createSignal<TopologyNode | null>(null)
  const [dragEnded, setDragEnded] = createSignal(false)

  const layout = createMemo(() => computeLayout(sync.data.workers, sync.data.upstreams))
  const hasData = createMemo(() => layout().groups.length > 0 || layout().orphans.length > 0)

  createEffect(() => {
    if (hasData()) dialog.setSize("xlarge")
  })

  function handleClick(node: TopologyNode) {
    if (node.kind === "worker") {
      dialog.push(() => <DialogWorkerStatus worker={node.data as WorkerSummary} management />)
      return
    }
    const upstream = node.data as RedactedUpstream
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
  }

  async function handleDrop(source: TopologyNode, target: TopologyNode) {
    if (!isValidDrop(source, target)) return
    const { worker, upstream } = toDropPair(source, target)
    const workerData = worker.data as WorkerSummary
    const upstreamData = upstream.data as RedactedUpstream
    if (workerData.upstream.name === upstreamData.name) return
    try {
      await sdk.client.patchWorker(workerData.port, { upstream: upstreamData.name })
      await sync.bootstrap({ fatal: false })
      toast.show({ message: `Switched ${workerData.name} → ${upstreamData.name}`, variant: "success" })
    } catch (err) {
      toast.error(err)
    }
  }

  return (
    <box flexDirection="column">
      <box flexDirection="row" justifyContent="space-between">
        <text fg={theme.text} attributes={TextAttributes.BOLD}>
          Topology
        </text>
        <EscHint dialog={dialog} />
      </box>
      <Show
        when={hasData()}
        fallback={
          <box justifyContent="center" alignItems="center">
            <text fg={theme.textMuted}>No workers or upstreams configured</text>
          </box>
        }
      >
        <box flexDirection="row" gap={TOPOLOGY_GROUP_GAP}>
          <For each={layout().groups}>
            {(group) => (
              <box flexDirection="column" width={group.width} alignItems="center">
                <NodeBox
                  node={group.upstream}
                  hovered={hovered()}
                  dragSource={dragSource()}
                  dragEnded={dragEnded()}
                  setHovered={setHovered}
                  setDragSource={setDragSource}
                  setDragEnded={setDragEnded}
                  onClick={handleClick}
                  onDrop={handleDrop}
                  theme={theme}
                />
                <EdgeRow group={group} hoveredId={hovered()} theme={theme} />
                <box flexDirection="row" gap={TOPOLOGY_COL_GAP}>
                  <For each={group.workers}>
                    {(node) => (
                      <NodeBox
                        node={node}
                        hovered={hovered()}
                        dragSource={dragSource()}
                        dragEnded={dragEnded()}
                        setHovered={setHovered}
                        setDragSource={setDragSource}
                        setDragEnded={setDragEnded}
                        onClick={handleClick}
                        onDrop={handleDrop}
                        theme={theme}
                      />
                    )}
                  </For>
                </box>
              </box>
            )}
          </For>
          <For each={layout().orphans}>
            {(node) => (
              <NodeBox
                node={node}
                hovered={hovered()}
                dragSource={dragSource()}
                dragEnded={dragEnded()}
                setHovered={setHovered}
                setDragSource={setDragSource}
                setDragEnded={setDragEnded}
                onClick={handleClick}
                onDrop={handleDrop}
                theme={theme}
              />
            )}
          </For>
        </box>
        <DragHint source={dragSource()} hovered={hovered()} layout={layout()} theme={theme} />
      </Show>
    </box>
  )
}

function NodeBox(props: {
  node: TopologyNode
  hovered: string | null
  dragSource: TopologyNode | null
  dragEnded: boolean
  setHovered: (id: string | null) => void
  setDragSource: (node: TopologyNode | null) => void
  setDragEnded: (ended: boolean) => void
  onClick: (node: TopologyNode) => void
  onDrop: (source: TopologyNode, target: TopologyNode) => void
  theme: Theme
}) {
  const isHovered = () => props.hovered === props.node.id
  return (
    <box
      width={props.node.width}
      height={props.node.height}
      border={true}
      borderColor={nodeColor(props.node, isHovered(), props.dragSource, props.theme)}
      backgroundColor={props.theme.backgroundPanel}
      justifyContent="center"
      alignItems="center"
      onMouseOver={() => props.setHovered(props.node.id)}
      onMouseOut={() => props.setHovered(null)}
      onMouseDown={() => props.setDragSource(props.node)}
      onMouseDragEnd={() => { props.setDragEnded(true); queueMicrotask(() => props.setDragSource(null)) }}
      onMouseDrop={() => { const source = props.dragSource; if (source && source.id !== props.node.id) props.onDrop(source, props.node); props.setDragEnded(true) }}
      onMouseUp={() => {
        if (props.dragEnded) {
          props.setDragEnded(false)
          return
        }
        props.setDragSource(null)
        props.onClick(props.node)
      }}
    >
      <text fg={props.theme.text} selectable={false}>{props.node.label}</text>
    </box>
  )
}

function DragHint(props: {
  source: TopologyNode | null
  hovered: string | null
  layout: ReturnType<typeof computeLayout>
  theme: Theme
}) {
  const target = createMemo(() => {
    const s = props.source
    if (!s) return null
    const all: TopologyNode[] = [
      ...props.layout.groups.flatMap((g) => [g.upstream, ...g.workers]),
      ...props.layout.orphans,
    ]
    return all.find((n) => n.id === props.hovered && n.id !== s.id) ?? null
  })
  return (
    <Show when={props.source}>
      {(src) => (
        <box height={1} marginTop={1}>
          <text fg={props.theme.borderActive}>Drag: {dropLabel(src(), target())}</text>
        </box>
      )}
    </Show>
  )
}

function EdgeRow(props: { group: TopologyGroup; hoveredId: string | null; theme: Theme }) {
  const edges = createMemo(() => computeGroupEdges(props.group))
  const isHighlighted = () => props.hoveredId === props.group.upstream.id || props.group.workers.some((w) => w.id === props.hoveredId)
  const line = createMemo(() => {
    const cells = edges().cells
    const maxX = cells.reduce((max, c) => Math.max(max, c.x), -1)
    const map = new Map(cells.map((c) => [c.x, c.char]))
    let s = ""
    for (let x = 0; x <= maxX; x++) {
      s += map.get(x) ?? " "
    }
    return s
  })

  return (
    <box height={1} width={props.group.width}>
      <text fg={isHighlighted() ? props.theme.borderActive : props.theme.textMuted}>{line()}</text>
    </box>
  )
}

function nodeColor(node: TopologyNode, hovered: boolean, dragSource: TopologyNode | null, theme: Theme): RGBA {
  const src = dragSource
  if (src && src.id === node.id) return theme.borderActive
  if (src && src.kind !== node.kind && hovered) return theme.success
  if (src && src.kind === node.kind && hovered) return theme.error
  if (hovered) return theme.borderActive
  if (node.kind === "worker") {
    const status = (node.data as WorkerSummary).status
    if (status === "running") return theme.success
    if (status === "failed") return theme.error
    return theme.textMuted
  }
  const upstream = node.data as RedactedUpstream
  return upstream.has_api_key ? theme.success : theme.warning
}
