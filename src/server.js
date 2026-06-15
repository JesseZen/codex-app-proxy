import { randomUUID } from "node:crypto";
import { createGunzip, createInflate } from "node:zlib";
import { decompress as zstdDecompress } from "fzstd";
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
const API_FORMAT = process.env.API_FORMAT || "";
const MODEL_NAME_OVERRIDE = process.env.MODEL_NAME || "";

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

  // Skip patching experimental_bearer_token — the proxy injects the
  // upstream API key on every outgoing request regardless, so there
  // is no need to write it into the Codex config file.

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

// ---------------------------------------------------------------------------
// Chat Completions ↔ Responses API adaptation
// ---------------------------------------------------------------------------

/**
 * Convert Responses API content format to Chat Completions format.
 *
 * Responses API uses: [{type: "output_text", text: "..."}, {type: "input_text", text: "..."}]
 * Chat Completions uses: "string" or [{type: "text", text: "..."}, {type: "image_url", image_url: {...}}]
 *
 * For assistant messages, prefer plain string when content is all text.
 * For user messages, keep the array format but convert types.
 */
function convertResponsesContentToChat(content, role) {
  if (content == null) {
    return content;
  }

  if (typeof content === "string") {
    return content;
  }

  if (!Array.isArray(content)) {
    return content;
  }

  const parts = content.map((part) => {
    if (typeof part === "string") {
      return { type: "text", text: part };
    }
    // output_text / input_text → text
    if (part.type === "output_text" || part.type === "input_text") {
      return { type: "text", text: part.text || "" };
    }
    // text → text (already correct)
    if (part.type === "text") {
      return { type: "text", text: part.text || "" };
    }
    // input_image → image_url
    if (part.type === "input_image") {
      return {
        type: "image_url",
        image_url: { url: part.image_url || part.url || "" },
      };
    }
    // refusal → skip for Chat Completions
    if (part.type === "refusal") {
      return null;
    }
    return part;
  }).filter(Boolean);

  // For assistant messages, if all parts are text, flatten to a plain string
  if (role === "assistant" && parts.every((p) => p.type === "text")) {
    return parts.map((p) => p.text).join("");
  }

  return parts;
}

const CHAT_COMPLETIONS_API_FORMAT = "chat_completions";

function isChatCompletionsMode() {
  return API_FORMAT === CHAT_COMPLETIONS_API_FORMAT;
}

/**
 * Translate a Responses API request body to Chat Completions format.
 *
 * Key mappings:
 *   input (string | array) → messages (array of {role, content})
 *   instructions           → system message prepended to messages
 *   tools[].type=function  → tools[].type=function (same shape, compatible)
 *   tool_choice            → tool_choice (mostly compatible)
 *   stream                 → stream (passthrough)
 *   model                  → model
 */
function translateResponsesRequestToChatCompletions(body) {
  if (!body || typeof body !== "object") {
    return { body, changed: false };
  }

  const messages = [];

  // Convert instructions → system message
  if (body.instructions && typeof body.instructions === "string") {
    messages.push({ role: "system", content: body.instructions });
  }

  // Convert input → messages
  if (typeof body.input === "string") {
    messages.push({ role: "user", content: body.input });
  } else if (Array.isArray(body.input)) {
    for (const item of body.input) {
      if (!item || typeof item !== "object") {
        continue;
      }

      // Handle Responses API input items
      if (item.type === "function_call") {
        messages.push({
          role: "assistant",
          content: null,
          tool_calls: [
            {
              id: item.call_id || item.id || randomUUID(),
              type: "function",
              function: {
                name: item.name,
                arguments: item.arguments || "{}",
              },
            },
          ],
        });
      } else if (item.type === "function_call_output") {
        messages.push({
          role: "tool",
          tool_call_id: item.call_id || "",
          content: item.output || "",
        });
      } else if (item.role === "user" || item.role === "assistant" || item.role === "system" || item.role === "developer") {
        // Message-style input items
        const role = item.role === "developer" ? "system" : item.role;
        const content = convertResponsesContentToChat(item.content, role);
        messages.push({ role, content });
      }
    }
  }

  // Convert tools — Responses API function tools are already compatible
  let tools = body.tools;
  if (Array.isArray(tools)) {
    tools = tools
      .filter((tool) => {
        // Keep function tools; drop Responses API-specific types that Chat Completions doesn't support
        return tool.type === "function";
      })
      .map((tool) => ({
        type: "function",
        function: {
          name: tool.name,
          description: tool.description || "",
          parameters: tool.parameters || { type: "object", properties: {} },
        },
      }));
  }

  // Convert tool_choice — mostly compatible
  let toolChoice = body.tool_choice;
  if (typeof toolChoice === "object" && toolChoice !== null) {
    if (toolChoice.type === "function" && toolChoice.name) {
      toolChoice = { type: "function", function: { name: toolChoice.name } };
    }
  }

  const chatBody = {
    model: MODEL_NAME_OVERRIDE || body.model,
    messages,
    stream: body.stream !== false, // default to true for Codex App compatibility
  };

  if (tools && tools.length > 0) {
    chatBody.tools = tools;
  }
  if (toolChoice !== undefined) {
    chatBody.tool_choice = toolChoice;
  }
  if (body.temperature !== undefined) {
    chatBody.temperature = body.temperature;
  }
  if (body.top_p !== undefined) {
    chatBody.top_p = body.top_p;
  }
  if (body.max_output_tokens !== undefined) {
    chatBody.max_tokens = body.max_output_tokens;
  }
  if (body.metadata?.user_id) {
    chatBody.user = body.metadata.user_id;
  }

  return { body: chatBody, changed: true };
}

/**
 * Build the initial response object that is referenced by multiple SSE events.
 */
function buildInitialResponse(requestBody) {
  return {
    id: `resp_${randomUUID().replace(/-/g, "").slice(0, 24)}`,
    object: "response",
    created_at: Math.floor(Date.now() / 1000),
    status: "in_progress",
    model: MODEL_NAME_OVERRIDE || requestBody?.model || "unknown",
    instructions: requestBody?.instructions || null,
    output: [],
    parallel_tool_calls: true,
    tool_choice: requestBody?.tool_choice || "auto",
    tools: requestBody?.tools || [],
    metadata: requestBody?.metadata || {},
  };
}

/**
 * Format an SSE event line.
 */
function formatSSE(event, data) {
  return `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;
}

/**
 * Create a Transform stream that converts Chat Completions SSE chunks
 * into Responses API SSE events in real time.
 */
function createChatToResponsesTransform(requestBody) {
  const responseObj = buildInitialResponse(requestBody);
  let seq = 0;
  let outputIndex = 0;
  let currentMessageId = `msg_${randomUUID().replace(/-/g, "").slice(0, 24)}`;
  let currentFunctionCallId = "";
  let currentFunctionCallName = "";
  let currentFunctionCallArguments = "";
  let textContent = "";
  let reasoningContent = "";
  let currentReasoningId = "";
  let started = false;
  let currentOutputItemType = null; // "message", "reasoning", or "function_call"

  const textEncoder = new TextEncoder();

  function nextSeq() {
    return ++seq;
  }

  function emitEvents(events) {
    return events.map((e) => textEncoder.encode(formatSSE(e.event, e.data)));
  }

  let debugFirstChunk = true;
  let pendingSseText = "";

  function emitCompletedResponse(controller) {
    const finalEvents = [];

    if (started) {
      // Close current output item if still open
      if (currentOutputItemType === "reasoning") {
        // Close reasoning output item
        finalEvents.push({
          event: "response.reasoning_text.done",
          data: {
            type: "response.reasoning_text.done",
            output_index: outputIndex,
            content_index: 0,
            text: reasoningContent,
            item_id: currentReasoningId,
            sequence_number: nextSeq(),
          },
        });

        responseObj.output.push({
          type: "reasoning",
          id: currentReasoningId,
          summary: [],
          content: [{ type: "reasoning_text", text: reasoningContent }],
          status: "completed",
        });

        finalEvents.push({
          event: "response.output_item.done",
          data: {
            type: "response.output_item.done",
            output_index: outputIndex,
            item: responseObj.output[responseObj.output.length - 1],
            sequence_number: nextSeq(),
          },
        });
      } else if (currentOutputItemType === "message") {
        // output_text.done
        finalEvents.push({
          event: "response.output_text.done",
          data: {
            type: "response.output_text.done",
            output_index: outputIndex,
            content_index: 0,
            text: textContent,
            item_id: currentMessageId,
            sequence_number: nextSeq(),
          },
        });

        // content_part.done
        finalEvents.push({
          event: "response.content_part.done",
          data: {
            type: "response.content_part.done",
            output_index: outputIndex,
            content_index: 0,
            part: { type: "output_text", text: textContent, annotations: [] },
            item_id: currentMessageId,
            sequence_number: nextSeq(),
          },
        });

        // output_item.done (message)
        responseObj.output.push({
          type: "message",
          id: currentMessageId,
          role: "assistant",
          content: [{ type: "output_text", text: textContent, annotations: [] }],
          status: "completed",
        });

        finalEvents.push({
          event: "response.output_item.done",
          data: {
            type: "response.output_item.done",
            output_index: outputIndex,
            item: responseObj.output[responseObj.output.length - 1],
            sequence_number: nextSeq(),
          },
        });
      } else if (currentOutputItemType === "function_call") {
        // function_call_arguments.done
        finalEvents.push({
          event: "response.function_call_arguments.done",
          data: {
            type: "response.function_call_arguments.done",
            output_index: outputIndex,
            item_id: currentFunctionCallId,
            name: currentFunctionCallName,
            arguments: currentFunctionCallArguments,
            sequence_number: nextSeq(),
          },
        });

        // output_item.done (function_call)
        responseObj.output.push({
          type: "function_call",
          id: currentFunctionCallId,
          call_id: currentFunctionCallId,
          name: currentFunctionCallName,
          arguments: currentFunctionCallArguments,
          status: "completed",
        });

        finalEvents.push({
          event: "response.output_item.done",
          data: {
            type: "response.output_item.done",
            output_index: outputIndex,
            item: responseObj.output[responseObj.output.length - 1],
            sequence_number: nextSeq(),
          },
        });
      }
    }

    // response.completed
    responseObj.status = "completed";
    responseObj.completed_at = Math.floor(Date.now() / 1000);

    finalEvents.push({
      event: "response.completed",
      data: {
        type: "response.completed",
        response: responseObj,
        sequence_number: nextSeq(),
      },
    });

    for (const encoded of emitEvents(finalEvents)) {
      controller.enqueue(encoded);
    }
  }

  function processSseFrame(frame, controller) {
    const dataLines = [];

    for (const line of frame.split(/\r?\n/)) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith(":")) {
        continue;
      }

      if (trimmed.startsWith("data:")) {
        dataLines.push(trimmed.slice(5).trimStart());
      }
    }

    if (dataLines.length === 0) {
      return;
    }

    const jsonStr = dataLines.join("\n").trim();
    if (!jsonStr || jsonStr === "[DONE]") {
      // Stream is complete — emit final events and response.completed
      emitCompletedResponse(controller);
      return;
    }

    let parsed;
    try {
      parsed = JSON.parse(jsonStr);
    } catch {
      return;
    }

    if (!parsed.choices || !Array.isArray(parsed.choices) || parsed.choices.length === 0) {
      return;
    }

    const choice = parsed.choices[0];
    const delta = choice.delta || {};
    const finishReason = choice.finish_reason;

    if (process.env.DEBUG_SSE === "1") {
      const hasContent = delta.content != null && delta.content !== "";
      const hasReasoning = delta.reasoning_content != null && delta.reasoning_content !== "";
      const hasToolCalls = !!(delta.tool_calls && delta.tool_calls.length > 0);
      console.log(`[proxy] delta: content=${hasContent ? JSON.stringify(delta.content).slice(0,50) : "none"} reasoning=${hasReasoning ? JSON.stringify(delta.reasoning_content).slice(0,50) : "none"} tool_calls=${hasToolCalls} finish=${finishReason || "none"}`);
    }

    const events = [];

    // Emit initial events on first chunk
    if (!started) {
      started = true;

      events.push({
        event: "response.created",
        data: {
          type: "response.created",
          response: { ...responseObj, status: "created" },
          sequence_number: nextSeq(),
        },
      });

      responseObj.status = "in_progress";

      events.push({
        event: "response.in_progress",
        data: {
          type: "response.in_progress",
          response: responseObj,
          sequence_number: nextSeq(),
        },
      });
    }

    // Handle tool calls
    if (delta.tool_calls && Array.isArray(delta.tool_calls)) {
      for (const tc of delta.tool_calls) {
        if (tc.function) {
          if (tc.function.name) {
            // New function call starting
            if (currentOutputItemType === "function_call") {
              // Close previous function call
              events.push({
                event: "response.function_call_arguments.done",
                data: {
                  type: "response.function_call_arguments.done",
                  output_index: outputIndex,
                  item_id: currentFunctionCallId,
                  name: currentFunctionCallName,
                  arguments: currentFunctionCallArguments,
                  sequence_number: nextSeq(),
                },
              });

              responseObj.output.push({
                type: "function_call",
                id: currentFunctionCallId,
                call_id: currentFunctionCallId,
                name: currentFunctionCallName,
                arguments: currentFunctionCallArguments,
                status: "completed",
              });

              events.push({
                event: "response.output_item.done",
                data: {
                  type: "response.output_item.done",
                  output_index: outputIndex,
                  item: responseObj.output[responseObj.output.length - 1],
                  sequence_number: nextSeq(),
                },
              });

              outputIndex++;
              currentFunctionCallArguments = "";
            }

            currentFunctionCallId = tc.id || randomUUID();
            currentFunctionCallName = tc.function.name;
            currentFunctionCallArguments = "";
            currentOutputItemType = "function_call";

            // output_item.added (function_call)
            events.push({
              event: "response.output_item.added",
              data: {
                type: "response.output_item.added",
                output_index: outputIndex,
                item: {
                  type: "function_call",
                  id: currentFunctionCallId,
                  call_id: currentFunctionCallId,
                  name: currentFunctionCallName,
                  arguments: "",
                  status: "in_progress",
                },
                sequence_number: nextSeq(),
              },
            });
          }

          if (tc.function.arguments) {
            currentFunctionCallArguments += tc.function.arguments;

            events.push({
              event: "response.function_call_arguments.delta",
              data: {
                type: "response.function_call_arguments.delta",
                output_index: outputIndex,
                item_id: currentFunctionCallId,
                delta: tc.function.arguments,
                sequence_number: nextSeq(),
              },
            });
          }
        }
      }
    }

    // Handle reasoning_content — convert to Responses API reasoning output item
    // (Codex-style collapsible thinking section)
    if (delta.reasoning_content != null && delta.reasoning_content !== "") {
      if (currentOutputItemType !== "reasoning") {
        // Close previous output item if any
        if (currentOutputItemType === "function_call") {
          events.push({
            event: "response.function_call_arguments.done",
            data: {
              type: "response.function_call_arguments.done",
              output_index: outputIndex,
              item_id: currentFunctionCallId,
              name: currentFunctionCallName,
              arguments: currentFunctionCallArguments,
              sequence_number: nextSeq(),
            },
          });

          responseObj.output.push({
            type: "function_call",
            id: currentFunctionCallId,
            call_id: currentFunctionCallId,
            name: currentFunctionCallName,
            arguments: currentFunctionCallArguments,
            status: "completed",
          });

          events.push({
            event: "response.output_item.done",
            data: {
              type: "response.output_item.done",
              output_index: outputIndex,
              item: responseObj.output[responseObj.output.length - 1],
              sequence_number: nextSeq(),
            },
          });

          outputIndex++;
          currentFunctionCallArguments = "";
        } else if (currentOutputItemType === "message") {
          // Close previous message
          events.push({
            event: "response.output_text.done",
            data: {
              type: "response.output_text.done",
              output_index: outputIndex,
              content_index: 0,
              text: textContent,
              item_id: currentMessageId,
              sequence_number: nextSeq(),
            },
          });

          events.push({
            event: "response.content_part.done",
            data: {
              type: "response.content_part.done",
              output_index: outputIndex,
              content_index: 0,
              part: { type: "output_text", text: textContent, annotations: [] },
              item_id: currentMessageId,
              sequence_number: nextSeq(),
            },
          });

          responseObj.output.push({
            type: "message",
            id: currentMessageId,
            role: "assistant",
            content: [{ type: "output_text", text: textContent, annotations: [] }],
            status: "completed",
          });

          events.push({
            event: "response.output_item.done",
            data: {
              type: "response.output_item.done",
              output_index: outputIndex,
              item: responseObj.output[responseObj.output.length - 1],
              sequence_number: nextSeq(),
            },
          });

          outputIndex++;
        }

        // Start a new reasoning output item
        currentOutputItemType = "reasoning";
        currentReasoningId = `rs_${randomUUID().replace(/-/g, "").slice(0, 24)}`;
        reasoningContent = "";

        events.push({
          event: "response.output_item.added",
          data: {
            type: "response.output_item.added",
            output_index: outputIndex,
            item: {
              type: "reasoning",
              id: currentReasoningId,
              summary: [],
              content: [],
              status: "in_progress",
            },
            sequence_number: nextSeq(),
          },
        });
      }

      reasoningContent += delta.reasoning_content;

      events.push({
        event: "response.reasoning_text.delta",
        data: {
          type: "response.reasoning_text.delta",
          output_index: outputIndex,
          content_index: 0,
          delta: delta.reasoning_content,
          item_id: currentReasoningId,
          sequence_number: nextSeq(),
        },
      });
    }

    // Handle text content
    if (delta.content != null && delta.content !== "") {
      if (currentOutputItemType !== "message") {
        // Close previous reasoning if any
        if (currentOutputItemType === "reasoning") {
          events.push({
            event: "response.reasoning_text.done",
            data: {
              type: "response.reasoning_text.done",
              output_index: outputIndex,
              content_index: 0,
              text: reasoningContent,
              item_id: currentReasoningId,
              sequence_number: nextSeq(),
            },
          });

          responseObj.output.push({
            type: "reasoning",
            id: currentReasoningId,
            summary: [],
            content: [{ type: "reasoning_text", text: reasoningContent }],
            status: "completed",
          });

          events.push({
            event: "response.output_item.done",
            data: {
              type: "response.output_item.done",
              output_index: outputIndex,
              item: responseObj.output[responseObj.output.length - 1],
              sequence_number: nextSeq(),
            },
          });

          outputIndex++;
        }

        // Close previous function call if any
        if (currentOutputItemType === "function_call") {
          events.push({
            event: "response.function_call_arguments.done",
            data: {
              type: "response.function_call_arguments.done",
              output_index: outputIndex,
              item_id: currentFunctionCallId,
              name: currentFunctionCallName,
              arguments: currentFunctionCallArguments,
              sequence_number: nextSeq(),
            },
          });

          responseObj.output.push({
            type: "function_call",
            id: currentFunctionCallId,
            call_id: currentFunctionCallId,
            name: currentFunctionCallName,
            arguments: currentFunctionCallArguments,
            status: "completed",
          });

          events.push({
            event: "response.output_item.done",
            data: {
              type: "response.output_item.done",
              output_index: outputIndex,
              item: responseObj.output[responseObj.output.length - 1],
              sequence_number: nextSeq(),
            },
          });

          outputIndex++;
          currentFunctionCallArguments = "";
        }

        // Start a new message output item
        currentOutputItemType = "message";
        currentMessageId = `msg_${randomUUID().replace(/-/g, "").slice(0, 24)}`;
        textContent = "";

        events.push({
          event: "response.output_item.added",
          data: {
            type: "response.output_item.added",
            output_index: outputIndex,
            item: {
              type: "message",
              id: currentMessageId,
              role: "assistant",
              content: [],
              status: "in_progress",
            },
            sequence_number: nextSeq(),
          },
        });

        events.push({
          event: "response.content_part.added",
          data: {
            type: "response.content_part.added",
            output_index: outputIndex,
            content_index: 0,
            part: { type: "output_text", text: "", annotations: [] },
            item_id: currentMessageId,
            sequence_number: nextSeq(),
          },
        });
      }

      textContent += delta.content;

      events.push({
        event: "response.output_text.delta",
        data: {
          type: "response.output_text.delta",
          output_index: outputIndex,
          content_index: 0,
          delta: delta.content,
          item_id: currentMessageId,
          sequence_number: nextSeq(),
        },
      });
    }

    // Handle finish_reason from Chat Completions — but don't close items yet,
    // we'll close them on [DONE] to ensure we have the complete content.
    // We do, however, want to emit any pending close events if the stream
    // ends abruptly (no [DONE]) — that's handled by the flush logic.
    void finishReason;

    if (process.env.DEBUG_SSE === "1" && events.length > 0) {
      for (const e of events) {
        console.log(`[proxy] emit: ${e.event}`);
      }
    }

    for (const encoded of emitEvents(events)) {
      controller.enqueue(encoded);
    }
  }

  return new TransformStream({
    transform(chunk, controller) {
      const text = typeof chunk === "string" ? chunk : new TextDecoder().decode(chunk);

      // Debug: log first chunk from upstream to diagnose format issues
      if (debugFirstChunk) {
        debugFirstChunk = false;
        const preview = text.slice(0, 500);
        console.log(`[proxy] upstream SSE first chunk (${text.length} bytes): ${preview}`);
      }

      pendingSseText += text;
      const frames = pendingSseText.split(/\r?\n\r?\n/);
      pendingSseText = frames.pop() || "";

      for (const frame of frames) {
        processSseFrame(frame, controller);
      }
    },

    flush(controller) {
      if (pendingSseText.trim()) {
        processSseFrame(pendingSseText, controller);
        pendingSseText = "";
      }

      // If the stream ended without [DONE], we still need to close things out
      if (started && responseObj.status !== "completed") {
        const events = [];

        if (currentOutputItemType === "reasoning") {
          events.push({
            event: "response.reasoning_text.done",
            data: {
              type: "response.reasoning_text.done",
              output_index: outputIndex,
              content_index: 0,
              text: reasoningContent,
              item_id: currentReasoningId,
              sequence_number: nextSeq(),
            },
          });

          responseObj.output.push({
            type: "reasoning",
            id: currentReasoningId,
            summary: [],
            content: [{ type: "reasoning_text", text: reasoningContent }],
            status: "completed",
          });

          events.push({
            event: "response.output_item.done",
            data: {
              type: "response.output_item.done",
              output_index: outputIndex,
              item: responseObj.output[responseObj.output.length - 1],
              sequence_number: nextSeq(),
            },
          });
        } else if (currentOutputItemType === "message" && textContent) {
          events.push({
            event: "response.output_text.done",
            data: {
              type: "response.output_text.done",
              output_index: outputIndex,
              content_index: 0,
              text: textContent,
              item_id: currentMessageId,
              sequence_number: nextSeq(),
            },
          });

          events.push({
            event: "response.content_part.done",
            data: {
              type: "response.content_part.done",
              output_index: outputIndex,
              content_index: 0,
              part: { type: "output_text", text: textContent, annotations: [] },
              item_id: currentMessageId,
              sequence_number: nextSeq(),
            },
          });

          responseObj.output.push({
            type: "message",
            id: currentMessageId,
            role: "assistant",
            content: [{ type: "output_text", text: textContent, annotations: [] }],
            status: "completed",
          });

          events.push({
            event: "response.output_item.done",
            data: {
              type: "response.output_item.done",
              output_index: outputIndex,
              item: responseObj.output[responseObj.output.length - 1],
              sequence_number: nextSeq(),
            },
          });
        } else if (currentOutputItemType === "function_call") {
          events.push({
            event: "response.function_call_arguments.done",
            data: {
              type: "response.function_call_arguments.done",
              output_index: outputIndex,
              item_id: currentFunctionCallId,
              name: currentFunctionCallName,
              arguments: currentFunctionCallArguments,
              sequence_number: nextSeq(),
            },
          });

          responseObj.output.push({
            type: "function_call",
            id: currentFunctionCallId,
            call_id: currentFunctionCallId,
            name: currentFunctionCallName,
            arguments: currentFunctionCallArguments,
            status: "completed",
          });

          events.push({
            event: "response.output_item.done",
            data: {
              type: "response.output_item.done",
              output_index: outputIndex,
              item: responseObj.output[responseObj.output.length - 1],
              sequence_number: nextSeq(),
            },
          });
        }

        responseObj.status = "completed";
        responseObj.completed_at = Math.floor(Date.now() / 1000);

        events.push({
          event: "response.completed",
          data: {
            type: "response.completed",
            response: responseObj,
            sequence_number: nextSeq(),
          },
        });

        const textEncoder = new TextEncoder();
        for (const e of events) {
          controller.enqueue(textEncoder.encode(formatSSE(e.event, e.data)));
        }
      }
    },
  });
}

/**
 * Write the adapted response: pipe upstream Chat Completions SSE through
 * the Chat→Responses transform, then stream the result to the client.
 * Also handles non-streaming Chat Completions JSON responses.
 */
async function writeAdaptedResponse(res, upstreamResponse, requestBody) {
  const upstreamContentType = upstreamResponse.headers.get("content-type") || "";

  // If upstream returned non-streaming JSON (content-type: application/json),
  // convert it directly to Responses API SSE events
  if (upstreamContentType.includes("application/json")) {
    console.log(`[proxy] upstream returned non-streaming JSON, converting to Responses API events`);
    const chatBody = await upstreamResponse.json();
    const sseText = convertChatCompletionJsonToResponsesSSE(chatBody, requestBody);
    res.writeHead(200, {
      "content-type": "text/event-stream; charset=utf-8",
      "cache-control": "no-cache",
      connection: "keep-alive",
    });
    res.end(sseText);
    return;
  }

  res.writeHead(200, {
    "content-type": "text/event-stream; charset=utf-8",
    "cache-control": "no-cache",
    connection: "keep-alive",
  });

  if (!upstreamResponse.body) {
    // No body — emit a minimal completed response
    const responseObj = buildInitialResponse(requestBody);
    responseObj.status = "completed";
    responseObj.completed_at = Math.floor(Date.now() / 1000);
    res.end(
      formatSSE("response.created", { type: "response.created", response: { ...responseObj, status: "created" }, sequence_number: 1 })
        + formatSSE("response.in_progress", { type: "response.in_progress", response: responseObj, sequence_number: 2 })
        + formatSSE("response.completed", { type: "response.completed", response: responseObj, sequence_number: 3 }),
    );
    return;
  }

  const transform = createChatToResponsesTransform(requestBody);
  const readable = upstreamResponse.body.pipeThrough(transform);

  // Read chunks from the transform and write+flush each one immediately
  // for true streaming (pipeline buffers too aggressively)
  const reader = readable.getReader();
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }
      res.write(value);
      // Flush immediately so the client sees each SSE event as it arrives
      if (typeof res.flush === "function") {
        res.flush();
      } else if (typeof res.flushHeaders === "function" && !res.headersSent) {
        // noop — headers already sent for SSE
      }
    }
  } finally {
    reader.releaseLock();
    res.end();
  }
}

/**
 * Convert a non-streaming Chat Completions JSON response into
 * a complete Responses API SSE event sequence.
 */
function convertChatCompletionJsonToResponsesSSE(chatBody, requestBody) {
  const responseObj = buildInitialResponse(requestBody);
  let seq = 0;
  const nextSeq = () => ++seq;
  const events = [];

  events.push(formatSSE("response.created", {
    type: "response.created",
    response: { ...responseObj, status: "created" },
    sequence_number: nextSeq(),
  }));

  responseObj.status = "in_progress";

  events.push(formatSSE("response.in_progress", {
    type: "response.in_progress",
    response: responseObj,
    sequence_number: nextSeq(),
  }));

  const choice = chatBody.choices?.[0];
  if (!choice) {
    // No choices — complete with empty output
    responseObj.status = "completed";
    responseObj.completed_at = Math.floor(Date.now() / 1000);
    events.push(formatSSE("response.completed", {
      type: "response.completed",
      response: responseObj,
      sequence_number: nextSeq(),
    }));
    return events.join("");
  }

  let outputIndex = 0;
  const message = choice.message || {};

  // Handle tool calls
  if (message.tool_calls && message.tool_calls.length > 0) {
    for (const tc of message.tool_calls) {
      const callId = tc.id || randomUUID();
      const funcName = tc.function?.name || "";
      const funcArgs = tc.function?.arguments || "{}";

      events.push(formatSSE("response.output_item.added", {
        type: "response.output_item.added",
        output_index: outputIndex,
        item: { type: "function_call", id: callId, call_id: callId, name: funcName, arguments: "", status: "in_progress" },
        sequence_number: nextSeq(),
      }));

      events.push(formatSSE("response.function_call_arguments.delta", {
        type: "response.function_call_arguments.delta",
        output_index: outputIndex,
        item_id: callId,
        delta: funcArgs,
        sequence_number: nextSeq(),
      }));

      events.push(formatSSE("response.function_call_arguments.done", {
        type: "response.function_call_arguments.done",
        output_index: outputIndex,
        item_id: callId,
        name: funcName,
        arguments: funcArgs,
        sequence_number: nextSeq(),
      }));

      const outputItem = { type: "function_call", id: callId, call_id: callId, name: funcName, arguments: funcArgs, status: "completed" };
      responseObj.output.push(outputItem);

      events.push(formatSSE("response.output_item.done", {
        type: "response.output_item.done",
        output_index: outputIndex,
        item: outputItem,
        sequence_number: nextSeq(),
      }));

      outputIndex++;
    }
  }

  // Handle reasoning_content (thinking tokens)
  if (message.reasoning_content != null && message.reasoning_content !== "") {
    const rsId = `rs_${randomUUID().replace(/-/g, "").slice(0, 24)}`;
    const reasoningText = typeof message.reasoning_content === "string" ? message.reasoning_content : String(message.reasoning_content);

    events.push(formatSSE("response.output_item.added", {
      type: "response.output_item.added",
      output_index: outputIndex,
      item: { type: "reasoning", id: rsId, summary: [], content: [], status: "in_progress" },
      sequence_number: nextSeq(),
    }));

    events.push(formatSSE("response.reasoning_text.delta", {
      type: "response.reasoning_text.delta",
      output_index: outputIndex,
      content_index: 0,
      delta: reasoningText,
      item_id: rsId,
      sequence_number: nextSeq(),
    }));

    events.push(formatSSE("response.reasoning_text.done", {
      type: "response.reasoning_text.done",
      output_index: outputIndex,
      content_index: 0,
      text: reasoningText,
      item_id: rsId,
      sequence_number: nextSeq(),
    }));

    const rsOutputItem = { type: "reasoning", id: rsId, summary: [], content: [{ type: "reasoning_text", text: reasoningText }], status: "completed" };
    responseObj.output.push(rsOutputItem);

    events.push(formatSSE("response.output_item.done", {
      type: "response.output_item.done",
      output_index: outputIndex,
      item: rsOutputItem,
      sequence_number: nextSeq(),
    }));

    outputIndex++;
  }

  // Handle text content
  if (message.content != null && message.content !== "") {
    const msgId = `msg_${randomUUID().replace(/-/g, "").slice(0, 24)}`;
    const text = typeof message.content === "string" ? message.content : String(message.content);

    events.push(formatSSE("response.output_item.added", {
      type: "response.output_item.added",
      output_index: outputIndex,
      item: { type: "message", id: msgId, role: "assistant", content: [], status: "in_progress" },
      sequence_number: nextSeq(),
    }));

    events.push(formatSSE("response.content_part.added", {
      type: "response.content_part.added",
      output_index: outputIndex,
      content_index: 0,
      part: { type: "output_text", text: "", annotations: [] },
      item_id: msgId,
      sequence_number: nextSeq(),
    }));

    events.push(formatSSE("response.output_text.delta", {
      type: "response.output_text.delta",
      output_index: outputIndex,
      content_index: 0,
      delta: text,
      item_id: msgId,
      sequence_number: nextSeq(),
    }));

    events.push(formatSSE("response.output_text.done", {
      type: "response.output_text.done",
      output_index: outputIndex,
      content_index: 0,
      text,
      item_id: msgId,
      sequence_number: nextSeq(),
    }));

    events.push(formatSSE("response.content_part.done", {
      type: "response.content_part.done",
      output_index: outputIndex,
      content_index: 0,
      part: { type: "output_text", text, annotations: [] },
      item_id: msgId,
      sequence_number: nextSeq(),
    }));

    const msgOutputItem = { type: "message", id: msgId, role: "assistant", content: [{ type: "output_text", text, annotations: [] }], status: "completed" };
    responseObj.output.push(msgOutputItem);

    events.push(formatSSE("response.output_item.done", {
      type: "response.output_item.done",
      output_index: outputIndex,
      item: msgOutputItem,
      sequence_number: nextSeq(),
    }));
  }

  responseObj.status = "completed";
  responseObj.completed_at = Math.floor(Date.now() / 1000);

  events.push(formatSSE("response.completed", {
    type: "response.completed",
    response: responseObj,
    sequence_number: nextSeq(),
  }));

  return events.join("");
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

function decompressBuffer(buffer, contentEncoding) {
  const encodings = contentEncoding
    .split(",")
    .map((e) => e.trim().toLowerCase())
    .filter(Boolean)
    .reverse(); // decompress in reverse order (outermost first)

  let result = Promise.resolve(buffer);

  for (const encoding of encodings) {
    if (encoding === "gzip" || encoding === "x-gzip") {
      result = result.then((buf) => gunzipBuffer(buf));
    } else if (encoding === "deflate" || encoding === "x-deflate") {
      result = result.then((buf) => inflateBuffer(buf));
    } else if (encoding === "zstd") {
      result = result.then((buf) => zstdDecompressBuffer(buf));
    } else if (encoding === "identity") {
      // no-op
    } else {
      return Promise.reject(new Error(`Unsupported content-encoding: ${encoding}`));
    }
  }

  return result;
}

function gunzipBuffer(buffer) {
  return new Promise((resolve, reject) => {
    const gunzip = createGunzip();
    const chunks = [];
    gunzip.on("data", (chunk) => chunks.push(chunk));
    gunzip.on("end", () => resolve(Buffer.concat(chunks)));
    gunzip.on("error", reject);
    gunzip.end(buffer);
  });
}

function inflateBuffer(buffer) {
  return new Promise((resolve, reject) => {
    const inflate = createInflate();
    const chunks = [];
    inflate.on("data", (chunk) => chunks.push(chunk));
    inflate.on("end", () => resolve(Buffer.concat(chunks)));
    inflate.on("error", reject);
    inflate.end(buffer);
  });
}

function zstdDecompressBuffer(buffer) {
  const decompressed = zstdDecompress(new Uint8Array(buffer));
  return Buffer.from(decompressed);
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
  const chatMode = isChatCompletionsMode();
  let requestUrl = req.url || "/";
  const method = req.method || "GET";
  const originalUrl = req.url || "/";
  const isResponsesApiRequest = chatMode && (originalUrl === "/v1/responses" || originalUrl === "/responses");

  // In chat_completions mode, rewrite /v1/responses → /v1/chat/completions
  if (isResponsesApiRequest) {
    requestUrl = originalUrl.replace(/\/v1\/responses$/, "/v1/chat/completions").replace(/\/responses$/, "/chat/completions");
  }

  const upstreamUrl = joinUrl(UPSTREAM_BASE_URL, requestUrl);
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
  let originalRequestBody = null;

  try {
    if (methodMayHaveRequestBody(method) && requestHasBody(req.headers)) {
      const contentType = getHeaderValue(req.headers["content-type"]);
      const contentEncoding = getHeaderValue(req.headers["content-encoding"]);

      if (isJsonContentType(contentType)) {
        // Read the raw body, decompressing gzip/deflate if needed
        let rawBody;
        if (hasUnsupportedContentEncoding(contentEncoding)) {
          // Codex with name = "OpenAI" sends gzip-encoded JSON.
          // Decompress so we can parse, filter, and forward as uncompressed JSON.
          console.log(`[proxy] decompressing ${contentEncoding}-encoded JSON request body`);
          const compressed = await readRequestBody(req);
          rawBody = await decompressBuffer(compressed, contentEncoding);
        } else {
          rawBody = await readRequestBody(req);
        }

        if (rawBody.length > 0) {
          const parsed = JSON.parse(rawBody.toString("utf8"));

          // Save original request body for the adaptation layer
          if (isResponsesApiRequest) {
            originalRequestBody = parsed;
          }

          const sanitized = sanitizeJsonBody(parsed);
          removedCount = sanitized.removedCount;

          let finalBody = sanitized.changed ? sanitized.body : parsed;

          // In chat_completions mode, translate Responses API body → Chat Completions body
          if (isResponsesApiRequest) {
            const translated = translateResponsesRequestToChatCompletions(finalBody);
            finalBody = translated.body;
            console.log(`[proxy] translated request body keys: ${Object.keys(finalBody).join(", ")}`);
          }

          upstreamRequestBody = Buffer.from(JSON.stringify(finalBody));
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

    if (isResponsesApiRequest) {
      headers.set("accept", "text/event-stream");
      headers.set("accept-encoding", "identity");
      headers.set("cache-control", "no-cache");
    }

    if (shouldLogRequest(removedCount) || isResponsesApiRequest) {
      const adaptTag = isResponsesApiRequest ? " [adapt→chat]" : "";
      if (removedCount > 0) {
        console.log(`[proxy] filtered ${removedCount} image_generation tool(s): ${method} ${originalUrl} → ${requestUrl}${adaptTag}`);
      } else {
        console.log(`[proxy] ${method} ${originalUrl} → ${requestUrl}${adaptTag}`);
      }
    }

    const upstreamResponse = await fetch(upstreamUrl, {
      method,
      headers,
      body: upstreamRequestBody,
      duplex: useStreamingRequestBody ? "half" : undefined,
      redirect: "manual",
      signal: abortController.signal,
    });

    // In chat_completions mode, translate Chat Completions SSE → Responses API SSE
    // Only adapt responses for requests that were originally to /v1/responses
    if (isResponsesApiRequest && upstreamResponse.ok) {
      const upstreamContentType = upstreamResponse.headers.get("content-type") || "";
      console.log(`[proxy] adapting response: upstream ${upstreamResponse.status} ${upstreamContentType}`);
      await writeAdaptedResponse(res, upstreamResponse, originalRequestBody);
    } else if (isResponsesApiRequest && !upstreamResponse.ok) {
      console.log(`[proxy] upstream returned ${upstreamResponse.status}, passing through without adaptation`);
      await writeResponse(res, upstreamResponse);
    } else {
      await writeResponse(res, upstreamResponse);
    }
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
  if (isChatCompletionsMode()) {
    console.log(`API format adaptation: Chat Completions → Responses API`);
  }
  console.log(`Proxying to ${UPSTREAM_BASE_URL}`);
});
