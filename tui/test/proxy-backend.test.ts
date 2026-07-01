import { expect, test } from "bun:test"
import type { WorkerDetail } from "../src/proxy/backend"

test("worker detail keeps request modules and lifecycle hooks separate", () => {
  const worker: WorkerDetail = {
    name: "app",
    port: 6767,
    upstream: { name: "openai", base_url: "https://api.openai.com/v1", has_api_key: true },
    status: "running",
    snapshot_generation: 3,
    log_level: "simple",
    modules: {
      api_translate: { enabled: true, params: { api_format: "chat_completions" } },
    },
    hooks: {
      config_patch: { enabled: true, params: { config_path: "~/.codex/config.toml", state_dir: "~/.ainn" } },
    },
    hook_statuses: {
      config_patch: { state: "active", detail: { provider_name: "test" } },
    },
  }

  expect(worker.modules).toEqual({
    api_translate: { enabled: true, params: { api_format: "chat_completions" } },
  })
  expect(worker.hooks).toEqual({
    config_patch: { enabled: true, params: { config_path: "~/.codex/config.toml", state_dir: "~/.ainn" } },
  })
  expect(worker.hook_statuses).toEqual({
    config_patch: { state: "active", detail: { provider_name: "test" } },
  })
})
