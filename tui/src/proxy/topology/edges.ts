import type { TopologyGroup } from "./layout"

export type EdgeCell = { x: number; y: number; char: string }

export type GroupEdges = {
  cells: EdgeCell[]
}

/**
 * Compute edge cells for a single group. Coordinate system is local to the group:
 * - x=0 is the left edge of the group
 * - y=0 is the single edge row between upstream (above) and workers (below)
 *
 * Upstream is centered horizontally within the group; each worker is centered within
 * its slot. We draw a branch from upstream center down to each worker center.
 */
export function computeGroupEdges(group: TopologyGroup): GroupEdges {
  const upstreamCenter = Math.floor(group.width / 2)
  const workerCenters = computeWorkerCenters(group)

  const cells = new Map<string, { x: number; y: number; up: boolean; down: boolean; left: boolean; right: boolean }>()

  function mark(x: number, dir: { up?: boolean; down?: boolean; left?: boolean; right?: boolean }) {
    const k = `${x},0`
    const existing = cells.get(k) ?? { x, y: 0, up: false, down: false, left: false, right: false }
    if (dir.up) existing.up = true
    if (dir.down) existing.down = true
    if (dir.left) existing.left = true
    if (dir.right) existing.right = true
    cells.set(k, existing)
  }

  for (const wx of workerCenters) {
    if (wx === upstreamCenter) {
      mark(wx, { up: true, down: true })
      continue
    }
    const upstreamDir = wx > upstreamCenter ? "right" : "left"
    const workerDir = wx > upstreamCenter ? "left" : "right"
    mark(upstreamCenter, { up: true, [upstreamDir]: true })
    mark(wx, { down: true, [workerDir]: true })
    const minX = Math.min(upstreamCenter, wx)
    const maxX = Math.max(upstreamCenter, wx)
    for (let x = minX + 1; x < maxX; x++) {
      mark(x, { left: true, right: true })
    }
  }

  return { cells: [...cells.values()].map((c) => ({ x: c.x, y: c.y, char: dirToChar(c) })) }
}

function computeWorkerCenters(group: TopologyGroup): number[] {
  const workersTotal = group.workers.reduce((sum, w) => sum + w.width, 0) + (group.workers.length - 1) * 2
  const startX = Math.floor((group.width - workersTotal) / 2)
  const centers: number[] = []
  let cursor = startX
  for (const w of group.workers) {
    centers.push(cursor + Math.floor(w.width / 2))
    cursor += w.width + 2
  }
  return centers
}

function dirToChar(dir: { up: boolean; down: boolean; left: boolean; right: boolean }): string {
  const { up: u, down: d, left: l, right: r } = dir
  if (u && d && l && r) return "┼"
  if (u && d && l) return "┤"
  if (u && d && r) return "├"
  if (u && l && r) return "┴"
  if (d && l && r) return "┬"
  if (u && d) return "│"
  if (u && l) return "┘"
  if (u && r) return "└"
  if (d && l) return "┐"
  if (d && r) return "┌"
  if (l && r) return "─"
  if (u || d) return "│"
  if (l || r) return "─"
  return " "
}
