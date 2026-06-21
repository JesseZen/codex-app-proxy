import { createCodexProxyClient } from "@codex-proxy/sdk/v2"
import type { GlobalEvent } from "@codex-proxy/sdk/v2"
import { Flag } from "@codex-proxy/core/flag/flag"
import { createSimpleContext } from "./helper"
import { batch, onCleanup, onMount } from "solid-js"
import type { ProxyConfigResponse, ProxyConfigStatus, RedactedProvider, WorkerDetail, WorkerSummary } from "../proxy/backend"

export type EventSource = {
  subscribe: (handler: (event: GlobalEvent) => void) => Promise<() => void>
}

export type { ProxyConfigStatus, RedactedProvider, WorkerDetail, WorkerSummary }

export const { use: useSDK, provider: SDKProvider } = createSimpleContext({
  name: "SDK",
  init: (props: {
    url: string
    directory?: string
    fetch?: typeof fetch
    headers?: RequestInit["headers"]
    events?: EventSource
  }) => {
    const abort = new AbortController()
    let sse: AbortController | undefined

    function createSDK() {
      const client = createCodexProxyClient({
        baseUrl: props.url,
        signal: abort.signal,
        directory: props.directory,
        fetch: props.fetch,
        headers: props.headers,
      })

      async function request<T>(pathname: string, init?: RequestInit): Promise<T> {
        const response = await (props.fetch ?? globalThis.fetch)(new URL(pathname, props.url), init)
        const body = (await response.json().catch(() => undefined)) as { error?: string; message?: string } | undefined
        if (!response.ok) throw new Error(body?.error ?? body?.message ?? `${response.status} ${response.statusText}`)
        return body as T
      }

      return Object.assign(client, {
        async listWorkers() {
          return request<{ workers: WorkerSummary[] }>("/api/workers").then((result) => result.workers)
        },
        async getWorker(port: number) {
          return request<WorkerDetail>(`/api/workers/${port}`)
        },
        async createWorker(input: { name: string; port: number; provider: string }) {
          return request<WorkerSummary>("/api/workers", {
            method: "POST",
            headers: { "content-type": "application/json" },
            body: JSON.stringify(input),
          })
        },
        async patchWorker(
          port: number,
          patch: Partial<{
            port: number
            provider: string
            modules: WorkerDetail["modules"]
            log_level: string
          }>,
        ) {
          const current = await this.getWorker(port)
          return request<WorkerSummary>(`/api/workers/${port}`, {
            method: "PATCH",
            headers: { "content-type": "application/json" },
            body: JSON.stringify({
              port: patch.port ?? current.port,
              provider: patch.provider ?? current.provider.name,
              modules: patch.modules ?? current.modules ?? {},
              log_level: patch.log_level ?? current.log_level,
            }),
          })
        },
        async restartWorker(port: number) {
          return request<{ worker: string; status: string }>(`/api/workers/${port}/restart`, {
            method: "POST",
          })
        },
        async stopWorker(port: number) {
          return request<{ worker: string; status: string }>(`/api/workers/${port}`, {
            method: "DELETE",
          })
        },
        async toggleModule(port: number, moduleName: string) {
          return request<{ worker: string; module: string; enabled: boolean }>(`/api/workers/${port}/modules/${moduleName}/toggle`, {
            method: "POST",
          })
        },
        async getLogs(port: number) {
          return request<{ lines: string[] }>(`/api/workers/${port}/logs`).then((result) => result.lines)
        },
        logsUrl(port: number) {
          return new URL(`/api/workers/${port}/stream`, props.url).toString()
        },
        async getProviders() {
          return request<{ providers: Record<string, RedactedProvider> }>("/api/providers").then((result) =>
            Object.values(result.providers ?? {}),
          )
        },
        async patchProvider(name: string, profile: { base_url?: string; api_key?: string; api_format?: string }) {
          return request(`/api/providers/${name}`, {
            method: "PATCH",
            headers: { "content-type": "application/json" },
            body: JSON.stringify(profile),
          })
        },
        async getConfig() {
          return request<ProxyConfigResponse>("/api/config")
        },
        async saveConfig() {
          return request<{ status: ProxyConfigStatus }>("/api/config", { method: "PUT" })
        },
        async subscribeManagerEvents(handler: (event: { type: string; payload: Record<string, unknown> }) => void) {
          const ctrl = new AbortController()
          const response = await (props.fetch ?? globalThis.fetch)(new URL("/api/events", props.url), {
            signal: ctrl.signal,
            headers: { Accept: "text/event-stream" },
          })
          if (!response.ok || !response.body) throw new Error(`failed to subscribe manager events: ${response.status}`)
          ;(async () => {
            const reader = response.body!.getReader()
            const decoder = new TextDecoder()
            let buffer = ""
            let eventType = ""
            let eventData = ""
            while (true) {
              const { done, value } = await reader.read()
              if (done) break
              buffer += decoder.decode(value, { stream: true })
              const chunks = buffer.split("\n\n")
              buffer = chunks.pop() ?? ""
              for (const chunk of chunks) {
                eventType = ""
                eventData = ""
                for (const line of chunk.split("\n")) {
                  if (line.startsWith("event: ")) eventType = line.slice(7)
                  if (line.startsWith("data: ")) eventData += line.slice(6)
                }
                if (!eventType) continue
                handler({
                  type: eventType,
                  payload: eventData ? ((JSON.parse(eventData) as Record<string, unknown>) ?? {}) : {},
                })
              }
            }
          })().catch(() => {})
          return () => ctrl.abort()
        },
      })
    }

    let sdk = createSDK()

    const handlers = new Set<(event: GlobalEvent) => void>()
    const emitter = {
      emit(_type: "event", event: GlobalEvent) {
        for (const handler of handlers) handler(event)
      },
      on(_type: "event", handler: (event: GlobalEvent) => void) {
        handlers.add(handler)
        return () => {
          handlers.delete(handler)
        }
      },
    }

    let queue: GlobalEvent[] = []
    let timer: Timer | undefined
    let last = 0
    const retryDelay = 1000
    const maxRetryDelay = 30000

    const flush = () => {
      if (queue.length === 0) return
      const events = queue
      queue = []
      timer = undefined
      last = Date.now()
      // Batch all event emissions so all store updates result in a single render
      batch(() => {
        for (const event of events) {
          emitter.emit("event", event)
        }
      })
    }

    const handleEvent = (event: GlobalEvent) => {
      queue.push(event)
      const elapsed = Date.now() - last

      if (timer) return
      // If we just flushed recently (within 16ms), batch this with future events
      // Otherwise, process immediately to avoid latency
      if (elapsed < 16) {
        timer = setTimeout(flush, 16)
        return
      }
      flush()
    }

    function startSSE() {
      sse?.abort()
      const ctrl = new AbortController()
      sse = ctrl
      ;(async () => {
        let attempt = 0
        while (true) {
          if (abort.signal.aborted || ctrl.signal.aborted) break

          const events = await sdk.global.event({
            signal: ctrl.signal,
            sseMaxRetryAttempts: 0,
          })

          if (Flag.CODEX_PROXY_EXPERIMENTAL_WORKSPACES) {
            // Start syncing workspaces, it's important to do this after
            // we've started listening to events
            await sdk.sync.start().catch(() => {})
          }

          for await (const event of events.stream) {
            if (ctrl.signal.aborted) break
            handleEvent(event)
          }

          if (timer) clearTimeout(timer)
          if (queue.length > 0) flush()
          attempt += 1
          if (abort.signal.aborted || ctrl.signal.aborted) break

          // Exponential backoff
          const backoff = Math.min(retryDelay * 2 ** (attempt - 1), maxRetryDelay)
          await new Promise((resolve) => setTimeout(resolve, backoff))
        }
      })().catch(() => {})
    }

    onMount(async () => {
      if (props.events) {
        const unsub = await props.events.subscribe(handleEvent)
        onCleanup(unsub)

        if (Flag.CODEX_PROXY_EXPERIMENTAL_WORKSPACES) {
          // Start syncing workspaces, it's important to do this after
          // we've started listening to events
          await sdk.sync.start().catch(() => {})
        }
      } else {
        startSSE()
      }
    })

    onCleanup(() => {
      abort.abort()
      sse?.abort()
      if (timer) clearTimeout(timer)
      handlers.clear()
    })

    return {
      get client() {
        return sdk
      },
      directory: props.directory,
      event: emitter,
      fetch: props.fetch ?? fetch,
      url: props.url,
    }
  },
})
