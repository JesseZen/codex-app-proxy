import type {
  Message,
  Agent,
  Provider,
  Session,
  Part,
  Config,
  Todo,
  Command,
  PermissionRequest,
  QuestionRequest,
  LspStatus,
  McpStatus,
  McpResource,
  FormatterStatus,
  SessionStatus,
  ProviderListResponse,
  ProviderAuthMethod,
  VcsInfo,
  SnapshotFileDiff,
  ConsoleState,
} from "@agent-inn/sdk/v2"
import type { ProxyConfig, ProxyConfigStatus, RedactedUpstream, UpstreamProbeResult, WorkerSummary } from "./sdk"
import { produce, reconcile } from "solid-js/store"
import { batch } from "solid-js"

export type SyncStore = {
  status: "loading" | "partial" | "complete"
  provider: Provider[]
  provider_default: Record<string, string>
  provider_next: ProviderListResponse
  console_state: ConsoleState
  capabilities: {
    experimentalBackgroundSubagents: boolean
  }
  provider_auth: Record<string, ProviderAuthMethod[]>
  agent: Agent[]
  command: Command[]
  permission: {
    [sessionID: string]: PermissionRequest[]
  }
  question: {
    [sessionID: string]: QuestionRequest[]
  }
  config: Config
  session: Session[]
  session_status: {
    [sessionID: string]: SessionStatus
  }
  session_diff: {
    [sessionID: string]: SnapshotFileDiff[]
  }
  todo: {
    [sessionID: string]: Todo[]
  }
  message: {
    [sessionID: string]: Message[]
  }
  part: {
    [messageID: string]: Part[]
  }
  lsp: LspStatus[]
  mcp: {
    [key: string]: McpStatus
  }
  mcp_resource: {
    [key: string]: McpResource
  }
  formatter: FormatterStatus[]
  vcs: VcsInfo | undefined
  workers: WorkerSummary[]
  upstreams: RedactedUpstream[]
  upstreamProbes: Record<string, UpstreamProbeResult>
  manager_config: ProxyConfig
  config_status: ProxyConfigStatus | undefined
  error?: string
}

export function search<T>(items: T[], target: string, key: (item: T) => string) {
  let left = 0
  let right = items.length - 1
  while (left <= right) {
    const middle = Math.floor((left + right) / 2)
    const value = key(items[middle])
    if (value === target) return { found: true, index: middle }
    if (value < target) left = middle + 1
    else right = middle - 1
  }
  return { found: false, index: left }
}

type SyncEventHandlerDeps = {
  store: SyncStore
  setStore: (path: any[], ...args: any[]) => void
  sdk: any
  project: any
  touchMessage: (sessionID: string, messageID: string) => void
  touchPart: (sessionID: string, partID: string) => void
  onServerDisposed: () => void
}

export function createSyncEventHandler(deps: SyncEventHandlerDeps) {
  const { store, setStore, sdk, project, touchMessage, touchPart, onServerDisposed } = deps
  return (event: any, ctx: { workspace: string }) => {
    switch (event.type) {
      case "server.instance.disposed":
        onServerDisposed()
        break
      case "permission.replied": {
        const requests = store.permission[event.properties.sessionID]
        if (!requests) break
        const match = search(requests, event.properties.requestID, (r: any) => r.id)
        if (!match.found) break
        setStore(
          "permission",
          event.properties.sessionID,
          produce((draft: any) => {
            draft.splice(match.index, 1)
          }),
        )
        break
      }

      case "permission.asked": {
        const request = event.properties
        const requests = store.permission[request.sessionID]
        if (!requests) {
          setStore("permission", request.sessionID, [request])
          break
        }
        const match = search(requests, request.id, (r: any) => r.id)
        if (match.found) {
          setStore("permission", request.sessionID, match.index, reconcile(request))
          break
        }
        setStore(
          "permission",
          request.sessionID,
          produce((draft: any) => {
            draft.splice(match.index, 0, request)
          }),
        )
        break
      }

      case "question.replied":
      case "question.rejected": {
        const requests = store.question[event.properties.sessionID]
        if (!requests) break
        const match = search(requests, event.properties.requestID, (r: any) => r.id)
        if (!match.found) break
        setStore(
          "question",
          event.properties.sessionID,
          produce((draft: any) => {
            draft.splice(match.index, 1)
          }),
        )
        break
      }

      case "question.asked": {
        const request = event.properties
        const requests = store.question[request.sessionID]
        if (!requests) {
          setStore("question", request.sessionID, [request])
          break
        }
        const match = search(requests, request.id, (r: any) => r.id)
        if (match.found) {
          setStore("question", request.sessionID, match.index, reconcile(request))
          break
        }
        setStore(
          "question",
          request.sessionID,
          produce((draft: any) => {
            draft.splice(match.index, 0, request)
          }),
        )
        break
      }

      case "todo.updated":
        setStore("todo", event.properties.sessionID, event.properties.todos)
        break

      case "session.diff":
        setStore("session_diff", event.properties.sessionID, event.properties.diff)
        break

      case "session.deleted": {
        const result = search(store.session, event.properties.info.id, (s: any) => s.id)
        if (result.found) {
          setStore(
            "session",
            produce((draft: any) => {
              draft.splice(result.index, 1)
            }),
          )
        }
        break
      }
      case "session.updated": {
        const result = search(store.session, event.properties.info.id, (s: any) => s.id)
        if (result.found) {
          setStore("session", result.index, reconcile(event.properties.info))
          break
        }
        setStore(
          "session",
          produce((draft: any) => {
            draft.splice(result.index, 0, event.properties.info)
          }),
        )
        break
      }

      case "session.next.moved": {
        const result = search(store.session, event.properties.sessionID, (s: any) => s.id)
        if (!result.found) break
        setStore(
          "session",
          result.index,
          produce((session: any) => {
            session.directory = event.properties.location.directory
            session.path = event.properties.subdirectory
            session.workspaceID = event.properties.location.workspaceID
            session.time.updated = event.properties.timestamp
          }),
        )
        break
      }

      case "session.status": {
        setStore("session_status", event.properties.sessionID, event.properties.status)
        break
      }

      case "message.updated": {
        touchMessage(event.properties.info.sessionID, event.properties.info.id)
        const messages = store.message[event.properties.info.sessionID]
        if (!messages) {
          setStore("message", event.properties.info.sessionID, [event.properties.info])
          break
        }
        const result = search(messages, event.properties.info.id, (m: any) => m.id)
        if (result.found) {
          setStore("message", event.properties.info.sessionID, result.index, reconcile(event.properties.info))
          break
        }
        setStore(
          "message",
          event.properties.info.sessionID,
          produce((draft: any) => {
            draft.splice(result.index, 0, event.properties.info)
          }),
        )
        const updated = store.message[event.properties.info.sessionID]
        if (updated.length > 100) {
          const oldest = updated[0]
          batch(() => {
            setStore(
              "message",
              event.properties.info.sessionID,
              produce((draft: any) => {
                draft.shift()
              }),
            )
            setStore(
              "part",
              produce((draft: any) => {
                delete draft[oldest.id]
              }),
            )
          })
        }
        break
      }
      case "message.removed": {
        touchMessage(event.properties.sessionID, event.properties.messageID)
        const messages = store.message[event.properties.sessionID]
        const result = search(messages, event.properties.messageID, (m: any) => m.id)
        if (result.found) {
          setStore(
            "message",
            event.properties.sessionID,
            produce((draft: any) => {
              draft.splice(result.index, 1)
            }),
          )
        }
        break
      }
      case "message.part.updated": {
        touchPart(event.properties.part.sessionID, event.properties.part.id)
        const parts = store.part[event.properties.part.messageID]
        if (!parts) {
          setStore("part", event.properties.part.messageID, [event.properties.part])
          break
        }
        const result = search(parts, event.properties.part.id, (p: any) => p.id)
        if (result.found) {
          setStore("part", event.properties.part.messageID, result.index, reconcile(event.properties.part))
          break
        }
        setStore(
          "part",
          event.properties.part.messageID,
          produce((draft: any) => {
            draft.splice(result.index, 0, event.properties.part)
          }),
        )
        break
      }

      case "message.part.delta": {
        const parts = store.part[event.properties.messageID]
        if (!parts) break
        const result = search(parts, event.properties.partID, (p: any) => p.id)
        if (!result.found) break
        touchPart(event.properties.sessionID, event.properties.partID)
        setStore(
          "part",
          event.properties.messageID,
          produce((draft: any) => {
            const part = draft[result.index]
            const field = event.properties.field as keyof typeof part
            const existing = part[field] as string | undefined
            ;(part[field] as string) = (existing ?? "") + event.properties.delta
          }),
        )
        break
      }

      case "message.part.removed": {
        touchPart(event.properties.sessionID, event.properties.partID)
        const parts = store.part[event.properties.messageID]
        const result = search(parts, event.properties.partID, (p: any) => p.id)
        if (result.found) {
          setStore(
            "part",
            event.properties.messageID,
            produce((draft: any) => {
              draft.splice(result.index, 1)
            }),
          )
        }
        break
      }

      case "lsp.updated": {
        const workspace = project.workspace.current()
        void sdk.client.lsp.status({ workspace }).then((x: any) => setStore("lsp", x.data ?? []))
        break
      }

      case "vcs.branch.updated": {
        if (ctx.workspace === project.workspace.current()) {
          setStore("vcs", { branch: event.properties.branch })
        }
        break
      }
    }
  }
}
