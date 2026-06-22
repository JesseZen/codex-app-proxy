import type { Agent, Config, Model, Path, Project, Provider } from "@codex-proxy/sdk/v2"
import type { EventSource } from "../context/sdk"

export type RedactedUpstream = {
  name: string
  base_url: string
  has_api_key: boolean
  api_format?: string
}

export type ModuleConfig = {
  enabled: boolean
  params?: Record<string, unknown>
}

export type WorkerSummary = {
  name: string
  port: number
  role?: string
  upstream: RedactedUpstream
  status: string
  snapshot_generation: number
  log_level: string
  modules?: Record<string, ModuleConfig>
}

export type WorkerDetail = WorkerSummary & {
  config_patch_state?: string
  config_patch_detail?: Record<string, string>
}

export type ProxyConfigStatus = {
  generation: number
  dirty: boolean
  last_save_error?: string
}

export type ProxyConfigResponse = {
  config: Config
  status: ProxyConfigStatus
}

function json(value: unknown, init?: ResponseInit) {
  return new Response(JSON.stringify(value), {
    ...init,
    headers: {
      "content-type": "application/json",
      ...(init?.headers ?? {}),
    },
  })
}

async function readJSON<T>(response: Response): Promise<T> {
  if (response.ok) return response.json() as Promise<T>
  const body = (await response.json().catch(() => undefined)) as { error?: string; message?: string } | undefined
  throw new Error(body?.error ?? body?.message ?? `${response.status} ${response.statusText}`)
}

async function fetchManager<T>(baseUrl: string, pathname: string, init?: RequestInit): Promise<T> {
  const response = await globalThis.fetch(new URL(pathname, baseUrl), init)
  return readJSON<T>(response)
}

function createModel(providerID: string): Model {
  const modelID = `${providerID}-proxy`
  return {
    id: modelID,
    providerID,
    api: {
      id: modelID,
      url: "",
      npm: "",
    },
    name: `${providerID} Proxy`,
    capabilities: {
      temperature: false,
      reasoning: false,
      attachment: false,
      toolcall: false,
      input: {
        text: true,
        audio: false,
        image: false,
        video: false,
        pdf: false,
      },
      output: {
        text: true,
        audio: false,
        image: false,
        video: false,
        pdf: false,
      },
      interleaved: false,
    },
    cost: {
      input: 0,
      output: 0,
      cache: {
        read: 0,
        write: 0,
      },
    },
    limit: {
      context: 128_000,
      output: 8_192,
    },
    status: "active",
    options: {},
    headers: {},
    release_date: "2026-01-01",
  }
}

export function toCodexProxyUpstreams(upstreams: RedactedUpstream[]): Provider[] {
  return upstreams.map((upstream) => {
    const model = createModel(upstream.name)
    return {
      id: upstream.name,
      name: upstream.name,
      source: "config",
      env: [],
      key: "",
      options: {
        base_url: upstream.base_url,
        api_format: upstream.api_format,
        has_api_key: upstream.has_api_key,
      },
      models: {
        [model.id]: model,
      },
    }
  })
}

function defaultModels(providers: Provider[]) {
  return Object.fromEntries(providers.map((provider) => [provider.id, Object.keys(provider.models)[0] ?? ""]))
}

function createPath(directory: string): Path {
  return {
    home: process.env.HOME ?? "",
    state: "",
    config: "",
    worktree: directory,
    directory,
  }
}

function createProject(directory: string): Project {
  const now = Date.now()
  return {
    id: "codex-proxy",
    name: "codex-proxy",
    worktree: directory,
    vcs: "git",
    time: {
      created: now,
      updated: now,
    },
    sandboxes: [],
  }
}

function createAgent(providers: Provider[]): Agent[] {
  const first = providers[0]
  const modelID = first ? Object.keys(first.models)[0] : undefined
  return [
    {
      name: "build",
      description: "Proxy manager",
      mode: "primary",
      hidden: false,
      permission: [],
      model:
        first && modelID
          ? {
              providerID: first.id,
              modelID,
            }
          : undefined,
      options: {},
    },
  ]
}

function createLocation(directory: string) {
  return {
    directory,
    project: {
      id: "codex-proxy",
      directory,
    },
  }
}

export function emptyEventSource(): EventSource {
  return {
    subscribe: async () => () => {},
  }
}

export function createProxyFetch(input: { baseUrl: string; directory: string }) {
  return async function proxyFetch(requestInfo: RequestInfo | URL, init?: RequestInit) {
    const request = requestInfo instanceof Request ? requestInfo : undefined
    const url = new URL(request ? request.url : String(requestInfo))
    const method = (init?.method ?? request?.method ?? "GET").toUpperCase()
    const upstreams = toCodexProxyUpstreams(
      await fetchManager<{ upstreams: Record<string, RedactedUpstream> }>(input.baseUrl, "/api/upstreams").then((result) =>
        Object.values(result.upstreams ?? {}),
      ),
    )
    const providerDefault = defaultModels(upstreams)
    const location = createLocation(input.directory)

    if (url.pathname === "/path" && method === "GET") return json(createPath(input.directory))
    if (url.pathname === "/project/current" && method === "GET") return json(createProject(input.directory))
    if (url.pathname === "/project/codex-proxy/directories" && method === "GET") {
      return json([{ directory: input.directory }])
    }
    if (url.pathname === "/experimental/workspace" && method === "GET") return json([])
    if (url.pathname === "/experimental/workspace/status" && method === "GET") return json([])
    if (url.pathname === "/config/providers" && method === "GET") {
      return json({ providers: upstreams, default: providerDefault })
    }
    if (url.pathname === "/provider" && method === "GET") {
      return json({ all: upstreams, default: providerDefault, connected: upstreams.map((upstream) => upstream.id) })
    }
    if (url.pathname === "/command" && method === "GET") return json([])
    if (url.pathname === "/config" && method === "GET") {
      const config = await fetchManager<ProxyConfigResponse>(input.baseUrl, "/api/config")
      return json(config.config)
    }
    if (url.pathname === "/experimental/capabilities" && method === "GET") {
      return json({ backgroundSubagents: false })
    }
    if (url.pathname === "/experimental/console" && method === "GET") {
      return json({ consoleManagedProviders: [], switchableOrgCount: 0 })
    }
    if (url.pathname === "/agent" && method === "GET") return json(createAgent(upstreams))
    if (url.pathname === "/lsp" && method === "GET") return json([])
    if (url.pathname === "/mcp" && method === "GET") return json({})
    if (url.pathname === "/experimental/resource" && method === "GET") return json({})
    if (url.pathname === "/formatter" && method === "GET") return json([])
    if (url.pathname === "/session/status" && method === "GET") return json({})
    if (url.pathname === "/provider/auth" && method === "GET") return json({})
    if (url.pathname === "/session" && method === "GET") return json([])
    if (url.pathname === "/vcs" && method === "GET") return json({ branch: "main" })

    if (url.pathname === "/api/location" && method === "GET") return json(location)
    if (url.pathname === "/api/agent" && method === "GET") return json({ location, data: [] })
    if (url.pathname === "/api/model" && method === "GET") return json({ location, data: [] })
    if (url.pathname === "/api/provider" && method === "GET") return json({ location, data: [] })
    if (url.pathname === "/api/integration" && method === "GET") return json({ location, data: [] })
    if (url.pathname === "/api/reference" && method === "GET") return json({ location, data: [] })
    if (url.pathname === "/api/command" && method === "GET") return json({ location, data: [] })
    if (url.pathname === "/api/skill" && method === "GET") return json({ location, data: [] })

    if (url.pathname.startsWith("/session") && method !== "GET") {
      return json({ message: "Proxy mode does not support chat sessions yet." }, { status: 501 })
    }

    return request ? globalThis.fetch(request) : globalThis.fetch(url, init)
  }
}
