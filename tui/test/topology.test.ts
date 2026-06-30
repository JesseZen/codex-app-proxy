import { expect, test } from "bun:test"
import { computeLayout } from "../src/proxy/topology/layout"
import { computeGroupEdges } from "../src/proxy/topology/edges"
import { isValidDrop, toDropPair, dropLabel } from "../src/proxy/topology/drag"
import type { WorkerSummary, RedactedUpstream } from "../src/proxy/backend"

function makeUpstream(name: string, hasKey = true): RedactedUpstream {
  return { name, base_url: `https://${name}.example.com/v1`, has_api_key: hasKey }
}

function makeWorker(name: string, upstream: RedactedUpstream, status = "running"): WorkerSummary {
  return { name, port: 10000, upstream, status, snapshot_generation: 1, log_level: "simple" }
}

function sortCells(cells: Array<{ x: number; y: number; char: string }>) {
  return [...cells].sort((a, b) => a.y - b.y || a.x - b.x)
}

function findGroup(layout: ReturnType<typeof computeLayout>, upstreamName: string) {
  return layout.groups.find((g) => g.upstream.label === upstreamName)!
}

test("computeLayout returns empty for no workers and no upstreams", () => {
  expect(computeLayout([], [])).toEqual({ groups: [], orphans: [], rows: 0 })
})

test("computeLayout places upstream above single worker", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const layout = computeLayout([worker], [upstream])

  expect(layout.groups).toHaveLength(1)
  const group = layout.groups[0]
  expect(group.upstream).toEqual({
    id: "upstream:openai",
    kind: "upstream",
    label: "openai",
    width: 10,
    height: 3,
    data: upstream,
  })
  expect(group.workers).toEqual([
    {
      id: "worker:app",
      kind: "worker",
      label: "app",
      width: 7,
      height: 3,
      data: worker,
    },
  ])
  // group width = max(upstream width 10, worker width 7) = 10
  expect(group.width).toBe(10)
  expect(layout.orphans).toEqual([])
})

test("computeLayout sets group width to fit multiple workers", () => {
  const upstream = makeUpstream("ab")
  const w1 = makeWorker("app", upstream)
  const w2 = makeWorker("cli-openrouter", upstream)
  const layout = computeLayout([w1, w2], [upstream])

  const group = layout.groups[0]
  // workers total = 7 + 18 + 2 (COL_GAP) = 27; upstream width = 6
  expect(group.width).toBe(27)
})

test("computeLayout places multiple upstream groups side by side", () => {
  const up1 = makeUpstream("aaa")
  const up2 = makeUpstream("zzz")
  const w1 = makeWorker("w1", up1)
  const w2 = makeWorker("w2", up2)
  const layout = computeLayout([w1, w2], [up1, up2])

  expect(layout.groups).toHaveLength(2)
  expect(layout.groups[0].upstream.label).toBe("aaa")
  expect(layout.groups[1].upstream.label).toBe("zzz")
})

test("computeLayout shows orphan upstreams without workers", () => {
  const usedUp = makeUpstream("openai")
  const orphanUp = makeUpstream("orphan")
  const worker = makeWorker("app", usedUp)
  const layout = computeLayout([worker], [usedUp, orphanUp])

  expect(layout.orphans).toEqual([
    {
      id: "upstream:orphan",
      kind: "upstream",
      label: "orphan",
      width: 10,
      height: 3,
      data: orphanUp,
    },
  ])
})

test("computeLayout handles worker whose upstream is not in upstreams list", () => {
  const embeddedUp = makeUpstream("embedded")
  const worker = makeWorker("app", embeddedUp)
  const layout = computeLayout([worker], [])

  expect(layout.groups).toHaveLength(1)
  expect(layout.groups[0].upstream.label).toBe("embedded")
})

test("computeLayout is deterministic for same input", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const a = computeLayout([worker], [upstream])
  const b = computeLayout([worker], [upstream])
  expect(a).toEqual(b)
})

test("computeGroupEdges connects same-column worker with vertical line", () => {
  const upstream = makeUpstream("ab")
  const worker = makeWorker("ab", upstream)
  const layout = computeLayout([worker], [upstream])
  const group = findGroup(layout, "ab")
  // group width = 6, upstream center = 3, worker center = 3 → vertical line
  const edges = computeGroupEdges(group)
  expect(sortCells(edges.cells)).toEqual([{ x: 3, y: 0, char: "│" }])
})

test("computeGroupEdges creates branch when worker is off-center", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const layout = computeLayout([worker], [upstream])
  const group = findGroup(layout, "openai")
  // group width = 10, upstream center = 5, worker start = 1, worker center = 4 → branch
  const edges = computeGroupEdges(group)
  expect(sortCells(edges.cells)).toEqual([
    { x: 4, y: 0, char: "┌" },
    { x: 5, y: 0, char: "┘" },
  ])
})

test("computeGroupEdges merges shared upstream branch with T-junction", () => {
  const upstream = makeUpstream("openai")
  const w1 = makeWorker("app", upstream)
  const w2 = makeWorker("cli-long-name", upstream)
  const layout = computeLayout([w1, w2], [upstream])
  const group = findGroup(layout, "openai")

  const edges = computeGroupEdges(group)
  const cellMap = new Map(edges.cells.map((c) => [c.x, c.char]))

  // group width = max(10, 7+17+2) = 26
  // upstream center = 13
  // worker centers: w1 (app, width 7) at start 0, center 3; w2 at start 9, center 17
  // T-junction at upstream center: up + left + right = ┴
  expect(cellMap.get(13)).toBe("┴")
  // w1 corner at x=3: down + right = ┌
  expect(cellMap.get(3)).toBe("┌")
  // w2 corner at x=17: down + left = ┐
  expect(cellMap.get(17)).toBe("┐")
  // between w1 and upstream center: ─
  for (let x = 4; x < 13; x++) {
    expect(cellMap.get(x)).toBe("─")
  }
  // between upstream center and w2: ─
  for (let x = 14; x < 17; x++) {
    expect(cellMap.get(x)).toBe("─")
  }
})

test("computeGroupEdges returns empty for group with no workers", () => {
  const orphan = makeUpstream("orphan")
  const layout = computeLayout([], [orphan])
  // orphans don't have groups; we test with a synthetic group instead
  const syntheticGroup = {
    upstream: { id: "upstream:orphan", kind: "upstream" as const, label: "orphan", width: 10, height: 3, data: orphan },
    workers: [],
    width: 10,
  }
  expect(computeGroupEdges(syntheticGroup).cells).toEqual([])
})

test("computeGroupEdges is deterministic for same input", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const layout = computeLayout([worker], [upstream])
  const group = findGroup(layout, "openai")
  const a = computeGroupEdges(group)
  const b = computeGroupEdges(group)
  expect(a).toEqual(b)
})

test("isValidDrop accepts worker↔upstream, rejects same kind or same node", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const layout = computeLayout([worker], [upstream])
  const upstreamNode = layout.groups[0].upstream
  const workerNode = layout.groups[0].workers[0]

  expect(isValidDrop(workerNode, upstreamNode)).toBe(true)
  expect(isValidDrop(upstreamNode, workerNode)).toBe(true)
  expect(isValidDrop(workerNode, workerNode)).toBe(false)
  expect(isValidDrop(upstreamNode, upstreamNode)).toBe(false)
})

test("toDropPair identifies worker and upstream roles regardless of source order", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const layout = computeLayout([worker], [upstream])
  const upstreamNode = layout.groups[0].upstream
  const workerNode = layout.groups[0].workers[0]

  const fromWorker = toDropPair(workerNode, upstreamNode)
  expect(fromWorker.worker).toBe(workerNode)
  expect(fromWorker.upstream).toBe(upstreamNode)

  const fromUpstream = toDropPair(upstreamNode, workerNode)
  expect(fromUpstream.worker).toBe(workerNode)
  expect(fromUpstream.upstream).toBe(upstreamNode)
})

test("dropLabel formats with target or placeholder question mark", () => {
  const upstream = makeUpstream("openai")
  const worker = makeWorker("app", upstream)
  const layout = computeLayout([worker], [upstream])
  const upstreamNode = layout.groups[0].upstream
  const workerNode = layout.groups[0].workers[0]

  expect(dropLabel(workerNode, upstreamNode)).toBe("app → openai")
  expect(dropLabel(workerNode, null)).toBe("app → ?")
})
