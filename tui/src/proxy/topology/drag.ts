import type { TopologyNode } from "./layout"

export type DropPair = { worker: TopologyNode; upstream: TopologyNode }

export function isValidDrop(source: TopologyNode, target: TopologyNode): boolean {
  return source.kind !== target.kind && source.id !== target.id
}

export function toDropPair(source: TopologyNode, target: TopologyNode): DropPair {
  if (source.kind === "worker") return { worker: source, upstream: target }
  return { worker: target, upstream: source }
}

export function dropLabel(source: TopologyNode, target: TopologyNode | null): string {
  return target ? `${source.label} → ${target.label}` : `${source.label} → ?`
}
