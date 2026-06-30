import type { WorkerSummary, RedactedUpstream } from "../backend"

export type TopologyNode = {
  id: string
  kind: "upstream" | "worker"
  label: string
  width: number
  height: number
  data: WorkerSummary | RedactedUpstream
}

export type TopologyGroup = {
  upstream: TopologyNode
  workers: TopologyNode[]
  width: number
}

export type TopologyLayout = {
  groups: TopologyGroup[]
  orphans: TopologyNode[]
  rows: number
}

const NODE_HEIGHT = 3
const NODE_PAD = 2
const COL_GAP = 2
const GROUP_GAP = 4

export const TOPOLOGY_GROUP_GAP = GROUP_GAP
export const TOPOLOGY_COL_GAP = COL_GAP
export const TOPOLOGY_NODE_HEIGHT = NODE_HEIGHT
export const TOPOLOGY_EDGE_ROWS = 1

function nodeWidth(label: string): number {
  return label.length + NODE_PAD + 2
}

function makeNode(kind: "upstream" | "worker", label: string, data: WorkerSummary | RedactedUpstream): TopologyNode {
  return {
    id: `${kind}:${label}`,
    kind,
    label,
    width: nodeWidth(label),
    height: NODE_HEIGHT,
    data,
  }
}

type Group = {
  upstream: RedactedUpstream
  workers: WorkerSummary[]
}

function groupWorkers(workers: WorkerSummary[]): Group[] {
  const map = new Map<string, Group>()
  for (const worker of workers) {
    const name = worker.upstream.name
    let group = map.get(name)
    if (!group) {
      group = { upstream: worker.upstream, workers: [] }
      map.set(name, group)
    }
    group.workers.push(worker)
  }
  return [...map.values()].sort((a, b) => a.upstream.name.localeCompare(b.upstream.name))
}

function orphanUpstreams(upstreams: RedactedUpstream[], groups: Group[]): RedactedUpstream[] {
  const used = new Set(groups.map((g) => g.upstream.name))
  return upstreams.filter((u) => !used.has(u.name))
}

export function computeLayout(workers: WorkerSummary[], upstreams: RedactedUpstream[]): TopologyLayout {
  if (workers.length === 0 && upstreams.length === 0) {
    return { groups: [], orphans: [], rows: 0 }
  }

  const rawGroups = groupWorkers(workers)
  const orphans = orphanUpstreams(upstreams, rawGroups)
  const groups: TopologyGroup[] = rawGroups.map((group) => {
    const upstreamNode = makeNode("upstream", group.upstream.name, group.upstream)
    const workerNodes = group.workers.map((w) => makeNode("worker", w.name, w))
    const workersTotal = workerNodes.reduce((sum, w) => sum + w.width, 0) + COL_GAP * (workerNodes.length - 1)
    return {
      upstream: upstreamNode,
      workers: workerNodes,
      width: Math.max(upstreamNode.width, workersTotal),
    }
  })

  const orphanNodes = orphans.map((u) => makeNode("upstream", u.name, u))
  const rows = workers.length > 0 ? NODE_HEIGHT * 2 + 1 : NODE_HEIGHT
  return { groups, orphans: orphanNodes, rows }
}
