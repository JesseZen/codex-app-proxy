import { createSignal, onCleanup, type Setter } from "solid-js"
import { createStore, unwrap } from "solid-js/store"
import { createSimpleContext } from "./helper"
import { Flock } from "@codex-proxy/core/util/flock"
import { Global } from "@codex-proxy/core/global"
import { readJson, writeJsonAtomic } from "../util/persistence"
import { useTuiPaths } from "./runtime"
import path from "path"

export const { use: useKV, provider: KVProvider } = createSimpleContext({
  name: "KV",
  init: () => {
    const paths = useTuiPaths()
    void Global.Path.state
    const file = path.join(paths.state, "kv.json")
    const lock = `tui-kv:${file}`
    const [ready, setReady] = createSignal(false)
    const [store, setStore] = createStore<Record<string, any>>()

    Flock.withLock(lock, () => readJson<Record<string, unknown>>(file))
      .then((x) => {
        setStore(x)
      })
      .catch((error) => {
        console.error("Failed to read KV state", { error })
      })
      .finally(() => {
        setReady(true)
      })

    // Queue same-process writes so rapid updates persist in order.
    let write = Promise.resolve()
    let flushTimer: ReturnType<typeof setTimeout> | undefined
    const FLUSH_DEBOUNCE_MS = 200

    function scheduleFlush() {
      if (flushTimer) return
      flushTimer = setTimeout(flush, FLUSH_DEBOUNCE_MS)
    }

    function flush() {
      flushTimer = undefined
      const snapshot = structuredClone(unwrap(store))
      write = write
        .then(() => Flock.withLock(lock, () => writeJsonAtomic(file, snapshot)))
        .catch((error) => {
          console.error("Failed to write KV state", { error })
        })
    }

    onCleanup(() => {
      if (flushTimer) {
        clearTimeout(flushTimer)
        flushTimer = undefined
        flush()
      }
    })

    const result = {
      get ready() {
        return ready()
      },
      get store() {
        return store
      },
      signal<T>(name: string, defaultValue: T) {
        if (store[name] === undefined) setStore(name, defaultValue)
        return [
          function () {
            return result.get(name)
          },
          function setter(next: Setter<T>) {
            result.set(name, next)
          },
        ] as const
      },
      get(key: string, defaultValue?: any) {
        return store[key] ?? defaultValue
      },
      set(key: string, value: any) {
        setStore(key, value)
        scheduleFlush()
      },
    }
    return result
  },
})
