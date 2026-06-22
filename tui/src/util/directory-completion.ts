import { readdir } from "node:fs/promises"
import os from "node:os"
import path from "node:path"

export type DirectoryCompletionItem = {
  value: string
  display: string
}

const cache = new Map<string, string[]>()

export async function getDirectoryCompletions(input: {
  query: string
  basePath: string
  maxResults?: number
}): Promise<DirectoryCompletionItem[]> {
  const query = input.query.trim()
  const maxResults = input.maxResults ?? 10
  const expanded = query === "~" || query.startsWith("~/") ? path.join(os.homedir(), query.slice(2)) : query
  const resolved = query ? path.resolve(input.basePath, expanded) : input.basePath
  const scanDirectory = query.endsWith(path.sep) || query.endsWith("/") ? resolved : path.dirname(resolved)
  const prefix = query.endsWith(path.sep) || query.endsWith("/") ? "" : path.basename(query || "")

  let directories = cache.get(scanDirectory)
  if (!directories) {
    try {
      const entries = await readdir(scanDirectory, { withFileTypes: true })
      directories = entries.filter((entry) => entry.isDirectory()).map((entry) => entry.name).sort((left, right) => left.localeCompare(right))
      cache.set(scanDirectory, directories)
    } catch {
      return []
    }
  }

  const lowerPrefix = prefix.toLowerCase()
  const base = query.endsWith(path.sep) || query.endsWith("/") ? query : query.slice(0, Math.max(0, query.length - prefix.length))

  return directories
    .filter((name) => name.toLowerCase().startsWith(lowerPrefix))
    .slice(0, maxResults)
    .map((name) => {
      const value = `${base}${name}/`
      return { value, display: value }
    })
}
