import { expect, test } from "bun:test"
import { mkdir, mkdtemp, writeFile } from "node:fs/promises"
import os from "node:os"
import path from "node:path"
import { getDirectoryCompletions } from "../../src/util/directory-completion"

async function makeTree() {
  const root = await mkdtemp(path.join(os.tmpdir(), "cap-dir-complete-"))
  await mkdir(path.join(root, "alpha"), { recursive: true })
  await mkdir(path.join(root, "alpine"), { recursive: true })
  await mkdir(path.join(root, "nested", "child"), { recursive: true })
  await writeFile(path.join(root, "alpha.txt"), "file")
  return root
}

test("returns only directories and preserves relative prefixes", async () => {
  const root = await makeTree()

  const result = await getDirectoryCompletions({
    query: "a",
    basePath: root,
  })

  expect(result).toEqual([
    { value: "alpha/", display: "alpha/" },
    { value: "alpine/", display: "alpine/" },
  ])
})

test("scans the next level when query already ends with slash", async () => {
  const root = await makeTree()

  const result = await getDirectoryCompletions({
    query: "nested/",
    basePath: root,
  })

  expect(result).toEqual([{ value: "nested/child/", display: "nested/child/" }])
})

test("supports dot-relative queries", async () => {
  const root = await makeTree()

  const result = await getDirectoryCompletions({
    query: "./a",
    basePath: root,
  })

  expect(result).toEqual([
    { value: "./alpha/", display: "./alpha/" },
    { value: "./alpine/", display: "./alpine/" },
  ])
})
