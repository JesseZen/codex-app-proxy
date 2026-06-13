import { readFileSync } from "node:fs";
import fs from "node:fs/promises";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import process from "node:process";
import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";

loadDotEnv();

const LISTEN_HOST = "127.0.0.1";
const LISTEN_PORT = Number(process.env.PORT || "8787");
const ACTIVE_PROVIDER = process.env.ACTIVE_PROVIDER || "";
const UPSTREAM_BASE_URL = process.env.BASE_URL;
const UPSTREAM_API_KEY = process.env.API_KEY || "";
const CODEX_CONFIG_PATH = expandHome(process.env.CODEX_CONFIG_PATH || "~/.codex/config.toml");
const LOCAL_BASE_URL = `http://${LISTEN_HOST}:${LISTEN_PORT}`;
const LOG_EVERY_REQUEST = process.env.LOG_EVERY_REQUEST === "1";
const LOG_FILTERED_REQUESTS = process.env.LOG_FILTERED_REQUESTS !== "0";

const configPatchState = {
  patched: false,
  restored: false,
  providerName: null,
  entries: [],
};

if (!UPSTREAM_BASE_URL) {
  console.error("Missing BASE_URL");
  process.exit(1);
}

function loadDotEnv() {
  const initialEnvKeys = new Set(Object.keys(process.env));
  const baseEnv = parseEnvFile(path.resolve(process.cwd(), ".env"));
  const providerName = normalizeProviderName(process.env.ACTIVE_PROVIDER || baseEnv.ACTIVE_PROVIDER || "");
  const providerEnv = providerName
    ? parseEnvFile(path.resolve(process.cwd(), `.env.${providerName}`))
    : {};
  const mergedEnv = {
    ...baseEnv,
    ...providerEnv,
  };

  for (const [key, value] of Object.entries(mergedEnv)) {
    if (!key || initialEnvKeys.has(key)) {
      continue;
    }

    process.env[key] = value;
  }
}

function parseEnvFile(filePath) {
  let envText;

  try {
    envText = requireText(filePath);
  } catch {
    return {};
  }

  const parsed = {};

  for (const line of envText.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) {
      continue;
    }

    const separatorIndex = trimmed.indexOf("=");
    if (separatorIndex === -1) {
      continue;
    }

    const key = trimmed.slice(0, separatorIndex).trim();
    if (!key) {
      continue;
    }

    let value = trimmed.slice(separatorIndex + 1).trim();
    if (
      (value.startsWith("\"") && value.endsWith("\"")) ||
      (value.startsWith("'") && value.endsWith("'"))
    ) {
      value = value.slice(1, -1);
    }

    parsed[key] = value;
  }

  return parsed;
}

function normalizeProviderName(providerName) {
  const normalized = providerName.trim();

  if (!normalized) {
    return "";
  }

  if (!/^[A-Za-z0-9._-]+$/.test(normalized)) {
    throw new Error(
      `Invalid ACTIVE_PROVIDER "${providerName}". Only letters, numbers, ".", "_" and "-" are allowed.`,
    );
  }

  return normalized;
}

function requireText(filePath) {
  return readFileSync(filePath, "utf8");
}

function expandHome(filePath) {
  if (!filePath.startsWith("~/")) {
    return filePath;
  }

  return path.join(os.homedir(), filePath.slice(2));
}

function joinUrl(baseUrl, requestUrl) {
  const upstream = new URL(baseUrl);
  const incoming = new URL(requestUrl, "http://127.0.0.1");
  const joinedPath = `${upstream.pathname.replace(/\/$/, "")}${incoming.pathname}`;

  upstream.pathname = joinedPath || "/";
  upstream.search = incoming.search;
  return upstream;
}

function detectModelProvider(configText) {
  const providerMatch = configText.match(/^model_provider\s*=\s*"([^"]+)"\s*$/m);
  return providerMatch?.[1] || null;
}

function locateProviderSection(configText, providerName) {
  const lines = configText.split("\n");
  const sectionHeader = `[model_providers.${providerName}]`;
  const sectionStart = lines.findIndex((line) => line.trim() === sectionHeader);

  if (sectionStart === -1) {
    throw new Error(`Provider section not found: ${sectionHeader}`);
  }

  let sectionEnd = lines.length;
  for (let index = sectionStart + 1; index < lines.length; index += 1) {
    const trimmed = lines[index].trim();
    if (trimmed.startsWith("[") && trimmed.endsWith("]")) {
      sectionEnd = index;
      break;
    }
  }

  return {
    lines,
    sectionStart,
    sectionEnd,
  };
}

function updateProviderFieldInConfig(configText, providerName, fieldName, nextValue) {
  const { lines, sectionStart, sectionEnd } = locateProviderSection(configText, providerName);
  const escapedFieldName = fieldName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

  for (let index = sectionStart + 1; index < sectionEnd; index += 1) {
    const match = lines[index].match(new RegExp(`^(\\s*)${escapedFieldName}\\s*=\\s*"([^"]*)"\\s*$`));
    if (!match) {
      continue;
    }

    lines[index] = `${match[1]}${fieldName} = "${nextValue}"`;
    return {
      updatedText: lines.join("\n"),
      previousExists: true,
      previousValue: match[2],
    };
  }

  lines.splice(sectionEnd, 0, `${fieldName} = "${nextValue}"`);
  return {
    updatedText: lines.join("\n"),
    previousExists: false,
    previousValue: null,
  };
}

function removeProviderFieldInConfig(configText, providerName, fieldName) {
  const { lines, sectionStart, sectionEnd } = locateProviderSection(configText, providerName);
  const escapedFieldName = fieldName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

  for (let index = sectionStart + 1; index < sectionEnd; index += 1) {
    if (!new RegExp(`^(\\s*)${escapedFieldName}\\s*=\\s*"([^"]*)"\\s*$`).test(lines[index])) {
      continue;
    }

    lines.splice(index, 1);
    return {
      updatedText: lines.join("\n"),
    };
  }

  return {
    updatedText: lines.join("\n"),
  };
}

function getStartupConfigPatchPlan() {
  const plan = [];

  if (UPSTREAM_BASE_URL) {
    plan.push({
      action: "set",
      fieldName: "base_url",
      nextValue: LOCAL_BASE_URL,
      reason: "route Codex through the local proxy",
    });
  }

  if (UPSTREAM_API_KEY) {
    plan.push({
      action: "set",
      fieldName: "experimental_bearer_token",
      nextValue: UPSTREAM_API_KEY,
      reason: "override the upstream bearer token",
    });
  }

  return plan;
}

async function patchCodexConfig(entries) {
  if (entries.length === 0) {
    return {
      providerName: null,
      appliedEntries: [],
    };
  }

  const originalText = await fs.readFile(CODEX_CONFIG_PATH, "utf8");
  const providerName = detectModelProvider(originalText);

  if (!providerName) {
    throw new Error(`model_provider not found in ${CODEX_CONFIG_PATH}`);
  }

  let nextText = originalText;
  const appliedEntries = [];

  for (const entry of entries) {
    const result =
      entry.action === "delete"
        ? removeProviderFieldInConfig(nextText, providerName, entry.fieldName)
        : updateProviderFieldInConfig(nextText, providerName, entry.fieldName, entry.nextValue);
    nextText = result.updatedText;

    if (entry.action !== "delete") {
      appliedEntries.push({
        ...entry,
        previousExists: result.previousExists,
        previousValue: result.previousValue,
      });
    }
  }

  if (nextText !== originalText) {
    await fs.writeFile(CODEX_CONFIG_PATH, nextText, "utf8");
  }

  return {
    providerName,
    appliedEntries,
  };
}

async function applyStartupConfigPatch() {
  const patchPlan = getStartupConfigPatchPlan();

  if (patchPlan.length === 0) {
    console.log(`[proxy] no config patch entries from environment`);
    return;
  }

  const { providerName, appliedEntries } = await patchCodexConfig(patchPlan);
  configPatchState.patched = true;
  configPatchState.providerName = providerName;
  configPatchState.entries = appliedEntries;

  for (const entry of appliedEntries) {
    console.log(
      `[proxy] patched ${CODEX_CONFIG_PATH} (${providerName}.${entry.fieldName}: ${entry.previousExists ? entry.previousValue : "<unset>"} -> ${entry.nextValue})`,
    );
  }
}

async function restoreCodexConfigPatch() {
  if (!configPatchState.patched || configPatchState.restored) {
    return;
  }

  const restoreEntries = configPatchState.entries
    .slice()
    .reverse()
    .map((entry) =>
      entry.previousExists
        ? {
            action: "set",
            fieldName: entry.fieldName,
            nextValue: entry.previousValue,
            reason: `restore ${entry.fieldName}`,
          }
        : {
            action: "delete",
            fieldName: entry.fieldName,
            reason: `remove ${entry.fieldName}`,
          },
    );

  if (restoreEntries.length > 0) {
    await patchCodexConfig(restoreEntries);
  }

  configPatchState.restored = true;
  for (const entry of configPatchState.entries) {
    console.log(
      `[proxy] restored ${CODEX_CONFIG_PATH} (${configPatchState.providerName}.${entry.fieldName} -> ${entry.previousExists ? entry.previousValue : "<unset>"})`,
    );
  }
}

function installShutdownHooks(server) {
  let shuttingDown = false;

  const shutdown = async (signal, exitCode = 0) => {
    if (shuttingDown) {
      return;
    }

    shuttingDown = true;
    console.log(`[proxy] shutting down${signal ? ` (${signal})` : ""}`);

    try {
      server.close();
      await restoreCodexConfigPatch();
    } catch (error) {
      console.error("[proxy] failed during shutdown", error);
      exitCode = 1;
    } finally {
      process.exit(exitCode);
    }
  };

  process.on("SIGINT", () => {
    shutdown("SIGINT");
  });

  process.on("SIGTERM", () => {
    shutdown("SIGTERM");
  });

  process.on("uncaughtException", (error) => {
    console.error("[proxy] uncaught exception", error);
    shutdown("uncaughtException", 1);
  });

  process.on("unhandledRejection", (error) => {
    console.error("[proxy] unhandled rejection", error);
    shutdown("unhandledRejection", 1);
  });
}

function isImageGenerationTool(tool) {
  if (!tool) {
    return false;
  }

  if (typeof tool === "string") {
    return tool === "image_generation";
  }

  return tool.type === "image_generation" || tool.name === "image_generation";
}

function sanitizeToolChoice(toolChoice) {
  if (!toolChoice) {
    return toolChoice;
  }

  if (toolChoice === "image_generation") {
    return "auto";
  }

  if (typeof toolChoice === "object") {
    const type = toolChoice.type;
    const name = toolChoice.name;
    const nestedName = toolChoice.tool?.name;
    const nestedType = toolChoice.tool?.type;

    if (
      type === "image_generation" ||
      name === "image_generation" ||
      nestedName === "image_generation" ||
      nestedType === "image_generation"
    ) {
      return "auto";
    }
  }

  return toolChoice;
}

function sanitizeJsonBody(body) {
  if (!body || typeof body !== "object") {
    return {
      body,
      changed: false,
      removedCount: 0,
    };
  }

  const next = Array.isArray(body) ? [...body] : { ...body };
  let changed = false;
  let removedCount = 0;

  if (Array.isArray(next.tools)) {
    const originalCount = next.tools.length;
    next.tools = next.tools.filter((tool) => !isImageGenerationTool(tool));
    removedCount = originalCount - next.tools.length;
    changed = changed || removedCount > 0;
  }

  if ("tool_choice" in next) {
    const sanitizedToolChoice = sanitizeToolChoice(next.tool_choice);
    changed = changed || sanitizedToolChoice !== next.tool_choice;
    next.tool_choice = sanitizedToolChoice;
  }

  return {
    body: next,
    changed,
    removedCount,
  };
}

function getHeaderValue(headerValue) {
  if (Array.isArray(headerValue)) {
    return headerValue[0] || "";
  }

  return headerValue || "";
}

function methodMayHaveRequestBody(method) {
  return method !== "GET" && method !== "HEAD";
}

function requestHasBody(reqHeaders) {
  const transferEncoding = getHeaderValue(reqHeaders["transfer-encoding"]);
  if (transferEncoding) {
    return true;
  }

  const contentLength = Number(getHeaderValue(reqHeaders["content-length"]));
  return Number.isFinite(contentLength) && contentLength > 0;
}

function isJsonContentType(contentType) {
  const normalized = contentType.toLowerCase();
  return normalized.includes("application/json") || normalized.includes("+json");
}

function hasUnsupportedContentEncoding(contentEncoding) {
  if (!contentEncoding) {
    return false;
  }

  return contentEncoding
    .split(",")
    .map((value) => value.trim().toLowerCase())
    .some((value) => value && value !== "identity");
}

function copyHeaders(
  reqHeaders,
  {
    bodyBufferLength,
    preserveContentEncoding = false,
    preserveContentLength = false,
  } = {},
) {
  const headers = new Headers();

  for (const [key, value] of Object.entries(reqHeaders)) {
    if (value == null) {
      continue;
    }

    const lowerKey = key.toLowerCase();
    if (
      lowerKey === "host" ||
      lowerKey === "connection" ||
      lowerKey === "transfer-encoding" ||
      (lowerKey === "content-length" && !preserveContentLength) ||
      (lowerKey === "content-encoding" && !preserveContentEncoding)
    ) {
      continue;
    }

    if (Array.isArray(value)) {
      for (const item of value) {
        headers.append(key, item);
      }
      continue;
    }

    headers.set(key, value);
  }

  if (UPSTREAM_API_KEY) {
    headers.set("authorization", `Bearer ${UPSTREAM_API_KEY}`);
  }

  if (typeof bodyBufferLength === "number") {
    headers.set("content-length", String(bodyBufferLength));
  }

  return headers;
}

async function readRequestBody(req) {
  const chunks = [];

  for await (const chunk of req) {
    chunks.push(chunk);
  }

  return Buffer.concat(chunks);
}

async function writeResponse(res, upstreamResponse) {
  const responseHeaders = Object.fromEntries(upstreamResponse.headers.entries());

  // fetch() auto-decodes body streams, so these headers are no longer accurate
  delete responseHeaders["transfer-encoding"];
  delete responseHeaders["content-encoding"];
  delete responseHeaders["content-length"];

  res.writeHead(upstreamResponse.status, responseHeaders);

  if (!upstreamResponse.body) {
    res.end();
    return;
  }

  await pipeline(Readable.fromWeb(upstreamResponse.body), res);
}

function writeJsonError(res, statusCode, message, type = "proxy_error") {
  if (res.headersSent || res.writableEnded) {
    return;
  }

  res.writeHead(statusCode, { "content-type": "application/json; charset=utf-8" });
  res.end(
    JSON.stringify({
      error: {
        message,
        type,
      },
    }),
  );
}

function shouldLogRequest(removedCount) {
  return (removedCount > 0 && LOG_FILTERED_REQUESTS) || LOG_EVERY_REQUEST;
}

async function handleRequest(req, res) {
  const upstreamUrl = joinUrl(UPSTREAM_BASE_URL, req.url || "/");
  const method = req.method || "GET";
  const abortController = new AbortController();
  const onRequestAborted = () => {
    abortController.abort();
  };
  const onResponseClosed = () => {
    if (!res.writableFinished) {
      abortController.abort();
    }
  };

  req.on("aborted", onRequestAborted);
  res.on("close", onResponseClosed);

  let upstreamRequestBody;
  let upstreamBodyBufferLength;
  let preserveContentEncoding = false;
  let preserveContentLength = false;
  let useStreamingRequestBody = false;
  let removedCount = 0;

  try {
    if (methodMayHaveRequestBody(method) && requestHasBody(req.headers)) {
      const contentType = getHeaderValue(req.headers["content-type"]);
      const contentEncoding = getHeaderValue(req.headers["content-encoding"]);

      if (isJsonContentType(contentType)) {
        if (hasUnsupportedContentEncoding(contentEncoding)) {
          writeJsonError(
            res,
            415,
            "Cannot safely filter image_generation from encoded JSON request bodies.",
            "unsupported_content_encoding",
          );
          return;
        }

        const rawBody = await readRequestBody(req);

        if (rawBody.length > 0) {
          const parsed = JSON.parse(rawBody.toString("utf8"));
          const sanitized = sanitizeJsonBody(parsed);
          removedCount = sanitized.removedCount;
          if (sanitized.changed) {
            upstreamRequestBody = Buffer.from(JSON.stringify(sanitized.body));
          } else {
            upstreamRequestBody = rawBody;
          }
        } else {
          upstreamRequestBody = rawBody;
        }

        upstreamBodyBufferLength = upstreamRequestBody.length;
      } else {
        upstreamRequestBody = Readable.toWeb(req);
        preserveContentEncoding = hasUnsupportedContentEncoding(contentEncoding);
        preserveContentLength = true;
        useStreamingRequestBody = true;
      }
    }

    const headers = copyHeaders(req.headers, {
      bodyBufferLength: upstreamBodyBufferLength,
      preserveContentEncoding,
      preserveContentLength,
    });
    const upstreamResponse = await fetch(upstreamUrl, {
      method,
      headers,
      body: upstreamRequestBody,
      duplex: useStreamingRequestBody ? "half" : undefined,
      redirect: "manual",
      signal: abortController.signal,
    });

    if (shouldLogRequest(removedCount)) {
      if (removedCount > 0) {
        console.log(`[proxy] filtered ${removedCount} image_generation tool(s): ${method} ${req.url}`);
      } else {
        console.log(`[proxy] ${method} ${req.url}`);
      }
    }

    await writeResponse(res, upstreamResponse);
  } catch (error) {
    if (abortController.signal.aborted) {
      return;
    }

    console.error("[proxy] request failed", error);
    writeJsonError(
      res,
      502,
      error instanceof Error ? error.message : "Proxy request failed",
    );
  } finally {
    req.off("aborted", onRequestAborted);
    res.off("close", onResponseClosed);
  }
}

const server = http.createServer((req, res) => {
  handleRequest(req, res);
});

installShutdownHooks(server);

try {
  await applyStartupConfigPatch();
} catch (error) {
  console.error("[proxy] failed to patch Codex config", error);
  process.exit(1);
}

server.listen(LISTEN_PORT, LISTEN_HOST, () => {
  console.log(`Listening on ${LOCAL_BASE_URL}`);
  if (ACTIVE_PROVIDER) {
    console.log(`Using provider profile ${ACTIVE_PROVIDER}`);
  }
  console.log(`Proxying to ${UPSTREAM_BASE_URL}`);
});
