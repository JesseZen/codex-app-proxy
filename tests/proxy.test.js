import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import process from "node:process";
import test from "node:test";
import assert from "node:assert/strict";
import { once } from "node:events";
import { spawn } from "node:child_process";

async function createTempConfig(dirPath) {
  const configPath = path.join(dirPath, "config.toml");
  await writeFile(
    configPath,
    [
      'model_provider = "test"',
      "",
      "[model_providers.test]",
      'base_url = "https://example.com/v1"',
      'experimental_bearer_token = "orig-token"',
      "",
    ].join("\n"),
    "utf8",
  );
  return configPath;
}

async function startMockUpstream() {
  const requests = [];
  const server = http.createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) {
      chunks.push(chunk);
    }

    const bodyBuffer = Buffer.concat(chunks);
    requests.push({
      method: req.method,
      url: req.url,
      headers: req.headers,
      bodyText: bodyBuffer.toString("utf8"),
      bodyBuffer,
    });

    if (req.url === "/stream") {
      res.writeHead(200, { "content-type": "text/plain; charset=utf-8" });
      res.write("part-1:");
      setTimeout(() => {
        res.end("part-2");
      }, 20);
      return;
    }

    res.writeHead(200, { "content-type": "application/json; charset=utf-8" });
    res.end(
      JSON.stringify({
        ok: true,
        method: req.method,
        url: req.url,
        bodyLength: bodyBuffer.length,
      }),
    );
  });

  server.listen(0, "127.0.0.1");
  await once(server, "listening");

  return {
    requests,
    server,
    url: `http://127.0.0.1:${server.address().port}`,
    async close() {
      server.close();
      await once(server, "close");
    },
  };
}

async function waitForServerReady(child, port) {
  const readyText = `Listening on http://127.0.0.1:${port}`;
  const startupTimeoutMs = 10_000;
  const startTime = Date.now();

  while (Date.now() - startTime < startupTimeoutMs) {
    if (child.exitCode != null) {
      throw new Error(`proxy exited early with code ${child.exitCode}`);
    }

    const output = `${child.stdoutText}${child.stderrText}`;
    if (output.includes(readyText)) {
      return;
    }

    await new Promise((resolve) => {
      setTimeout(resolve, 50);
    });
  }

  throw new Error(`proxy did not start within ${startupTimeoutMs}ms\n${child.stdoutText}\n${child.stderrText}`);
}

async function startProxy({ baseUrl, configPath, port, extraEnv = {} }) {
  const child = spawn(process.execPath, ["src/server.js"], {
    cwd: process.cwd(),
    env: {
      ...process.env,
      PORT: String(port),
      BASE_URL: baseUrl,
      API_KEY: "test-upstream-key",
      CODEX_CONFIG_PATH: configPath,
      ACTIVE_PROVIDER: "",
      API_FORMAT: "",
      MODEL_NAME: "",
      LOG_EVERY_REQUEST: "0",
      ...extraEnv,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });

  child.stdout.setEncoding("utf8");
  child.stderr.setEncoding("utf8");
  child.stdoutText = "";
  child.stderrText = "";
  child.stdout.on("data", (chunk) => {
    child.stdoutText += chunk;
  });
  child.stderr.on("data", (chunk) => {
    child.stderrText += chunk;
  });

  await waitForServerReady(child, port);

  return {
    child,
    async stop() {
      if (child.exitCode != null) {
        return;
      }

      child.kill("SIGTERM");
      await once(child, "exit");
    },
  };
}

test("filters image_generation for JSON requests and rewrites tool_choice", async (t) => {
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-test-"));
  const upstream = await startMockUpstream();
  const configPath = await createTempConfig(tempDir);
  const proxyPort = 21001;
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
  });

  t.after(async () => {
    await proxy.stop();
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  const response = await fetch(`http://127.0.0.1:${proxyPort}/v1/responses`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
    },
    body: JSON.stringify({
      tools: [
        { type: "image_generation" },
        { type: "function", name: "keep_me" },
      ],
      tool_choice: "image_generation",
      input: "hello",
    }),
  });

  assert.equal(response.status, 200);
  assert.equal(upstream.requests.length, 1);

  const forwarded = JSON.parse(upstream.requests[0].bodyText);
  assert.deepEqual(forwarded.tools, [{ type: "function", name: "keep_me" }]);
  assert.equal(forwarded.tool_choice, "auto");
  assert.equal(forwarded.input, "hello");
  assert.equal(upstream.requests[0].headers.authorization, "Bearer test-upstream-key");
});

test("preserves non-JSON request bodies for methods other than POST", async (t) => {
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-test-"));
  const upstream = await startMockUpstream();
  const configPath = await createTempConfig(tempDir);
  const proxyPort = 21002;
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
  });

  t.after(async () => {
    await proxy.stop();
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  const payload = "hello-streaming-body";
  const response = await fetch(`http://127.0.0.1:${proxyPort}/upload`, {
    method: "PATCH",
    headers: {
      "content-type": "text/plain",
    },
    body: payload,
    duplex: "half",
  });

  assert.equal(response.status, 200);
  assert.equal(upstream.requests.length, 1);
  assert.equal(upstream.requests[0].method, "PATCH");
  assert.equal(upstream.requests[0].bodyText, payload);
});

test("rejects encoded JSON bodies that cannot be safely filtered", async (t) => {
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-test-"));
  const upstream = await startMockUpstream();
  const configPath = await createTempConfig(tempDir);
  const proxyPort = 21003;
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
  });

  t.after(async () => {
    await proxy.stop();
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  const response = await fetch(`http://127.0.0.1:${proxyPort}/v1/responses`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "content-encoding": "gzip",
    },
    body: '{"tools":["image_generation"]}',
  });

  assert.equal(response.status, 415);
  assert.equal(upstream.requests.length, 0);

  const body = await response.json();
  assert.equal(body.error.type, "unsupported_content_encoding");
});

test("streams upstream responses back to the client", async (t) => {
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-test-"));
  const upstream = await startMockUpstream();
  const configPath = await createTempConfig(tempDir);
  const proxyPort = 21004;
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
  });

  t.after(async () => {
    await proxy.stop();
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  const response = await fetch(`http://127.0.0.1:${proxyPort}/stream`);
  assert.equal(response.status, 200);
  assert.equal(await response.text(), "part-1:part-2");
});

test("restores config on shutdown", async (t) => {
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-test-"));
  const upstream = await startMockUpstream();
  const configPath = await createTempConfig(tempDir);
  const proxyPort = 21005;
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
  });

  t.after(async () => {
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  const patchedText = await readFile(configPath, "utf8");
  assert.match(patchedText, new RegExp(`base_url = "http://127\\.0\\.0\\.1:${proxyPort}"`));
  assert.match(patchedText, /experimental_bearer_token = "test-upstream-key"/);

  await proxy.stop();

  const restoredText = await readFile(configPath, "utf8");
  assert.match(restoredText, /base_url = "https:\/\/example\.com\/v1"/);
  assert.match(restoredText, /experimental_bearer_token = "orig-token"/);
});

// ---------------------------------------------------------------------------
// Chat Completions ↔ Responses API adaptation tests
// ---------------------------------------------------------------------------

/**
 * Create a mock upstream that speaks Chat Completions SSE format.
 */
async function startChatCompletionsMockUpstream({ textChunks = [], toolCalls = [], splitSseFrames = false } = {}) {
  const requests = [];
  const server = http.createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) {
      chunks.push(chunk);
    }

    const bodyBuffer = Buffer.concat(chunks);
    requests.push({
      method: req.method,
      url: req.url,
      headers: req.headers,
      bodyText: bodyBuffer.toString("utf8"),
      bodyBuffer,
    });

    // Respond with Chat Completions SSE stream
    res.writeHead(200, {
      "content-type": "text/event-stream; charset=utf-8",
      "cache-control": "no-cache",
      connection: "keep-alive",
    });

    const id = "chatcmpl-test-123";
    const writeSseFrame = (frame) => {
      if (!splitSseFrames || frame.length < 2) {
        res.write(frame);
        return;
      }

      const splitAt = Math.floor(frame.length / 2);
      res.write(frame.slice(0, splitAt));
      res.write(frame.slice(splitAt));
    };

    // Emit text chunks
    for (const text of textChunks) {
      writeSseFrame(
        `data: ${JSON.stringify({
          id,
          object: "chat.completion.chunk",
          model: "gpt-4o",
          choices: [{ index: 0, delta: { content: text }, finish_reason: null }],
        })}\n\n`,
      );
    }

    // Emit tool call chunks
    for (let i = 0; i < toolCalls.length; i++) {
      const tc = toolCalls[i];
      // First chunk: name
      writeSseFrame(
        `data: ${JSON.stringify({
          id,
          object: "chat.completion.chunk",
          model: "gpt-4o",
          choices: [{ index: 0, delta: { tool_calls: [{ index: i, id: tc.id || `call_${i}`, type: "function", function: { name: tc.name, arguments: "" } }] }, finish_reason: null }],
        })}\n\n`,
      );

      // Argument chunks
      if (tc.argumentChunks) {
        for (const argChunk of tc.argumentChunks) {
          writeSseFrame(
            `data: ${JSON.stringify({
              id,
              object: "chat.completion.chunk",
              model: "gpt-4o",
              choices: [{ index: 0, delta: { tool_calls: [{ index: i, function: { arguments: argChunk } }] }, finish_reason: null }],
            })}\n\n`,
          );
        }
      }
    }

    // Final chunk with finish_reason
    writeSseFrame(
      `data: ${JSON.stringify({
        id,
        object: "chat.completion.chunk",
        model: "gpt-4o",
        choices: [{ index: 0, delta: {}, finish_reason: toolCalls.length > 0 ? "tool_calls" : "stop" }],
      })}\n\n`,
    );

    writeSseFrame("data: [DONE]\n\n");
    res.end();
  });

  server.listen(0, "127.0.0.1");
  await once(server, "listening");

  return {
    requests,
    server,
    url: `http://127.0.0.1:${server.address().port}`,
    async close() {
      server.close();
      await once(server, "close");
    },
  };
}

/**
 * Parse an SSE text into a list of { event, data } objects.
 */
function parseSSE(text) {
  const events = [];
  const chunks = text.split("\n\n").filter(Boolean);

  for (const chunk of chunks) {
    let event = "";
    let data = "";
    for (const line of chunk.split("\n")) {
      if (line.startsWith("event: ")) {
        event = line.slice(7);
      } else if (line.startsWith("data: ")) {
        data = line.slice(6);
      }
    }
    if (event && data) {
      events.push({ event, data: JSON.parse(data) });
    }
  }

  return events;
}

test("chat_completions mode: translates request and response for simple text", async (t) => {
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-test-"));
  const upstream = await startChatCompletionsMockUpstream({ textChunks: ["Hello", " world"] });
  const configPath = await createTempConfig(tempDir);
  const proxyPort = 21010;
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
    extraEnv: { API_FORMAT: "chat_completions" },
  });

  t.after(async () => {
    await proxy.stop();
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  const response = await fetch(`http://127.0.0.1:${proxyPort}/v1/responses`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      model: "gpt-4o",
      input: "Say hello",
      instructions: "Be friendly",
      stream: true,
    }),
  });

  assert.equal(response.status, 200);

  // Verify request was forwarded to /v1/chat/completions
  assert.equal(upstream.requests.length, 1);
  assert.equal(upstream.requests[0].url, "/v1/chat/completions");

  // Verify request body was translated
  const forwardedBody = JSON.parse(upstream.requests[0].bodyText);
  assert.equal(forwardedBody.model, "gpt-4o");
  assert.equal(forwardedBody.stream, true);
  assert.deepEqual(forwardedBody.messages, [
    { role: "system", content: "Be friendly" },
    { role: "user", content: "Say hello" },
  ]);
  assert.equal(upstream.requests[0].headers.accept, "text/event-stream");
  assert.equal(upstream.requests[0].headers["accept-encoding"], "identity");
  assert.equal(upstream.requests[0].headers["cache-control"], "no-cache");

  // Verify response SSE events
  const responseText = await response.text();
  const events = parseSSE(responseText);

  const eventTypes = events.map((e) => e.event);
  assert.ok(eventTypes.includes("response.created"), "should include response.created");
  assert.ok(eventTypes.includes("response.in_progress"), "should include response.in_progress");
  assert.ok(eventTypes.includes("response.output_item.added"), "should include response.output_item.added");
  assert.ok(eventTypes.includes("response.content_part.added"), "should include response.content_part.added");
  assert.ok(eventTypes.includes("response.output_text.delta"), "should include response.output_text.delta");
  assert.ok(eventTypes.includes("response.output_text.done"), "should include response.output_text.done");
  assert.ok(eventTypes.includes("response.content_part.done"), "should include response.content_part.done");
  assert.ok(eventTypes.includes("response.output_item.done"), "should include response.output_item.done");
  assert.ok(eventTypes.includes("response.completed"), "should include response.completed");

  // Verify text deltas
  const textDeltas = events.filter((e) => e.event === "response.output_text.delta");
  assert.equal(textDeltas.length, 2);
  assert.equal(textDeltas[0].data.delta, "Hello");
  assert.equal(textDeltas[1].data.delta, " world");

  // Verify response.completed
  const completed = events.find((e) => e.event === "response.completed");
  assert.equal(completed.data.response.status, "completed");
  assert.equal(completed.data.response.output.length, 1);
  assert.equal(completed.data.response.output[0].type, "message");
  assert.equal(completed.data.response.output[0].content[0].text, "Hello world");
});

test("chat_completions mode: handles SSE frames split across network chunks", async (t) => {
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-test-"));
  const upstream = await startChatCompletionsMockUpstream({
    textChunks: ["Split", " frame"],
    splitSseFrames: true,
  });
  const configPath = await createTempConfig(tempDir);
  const proxyPort = 21015;
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
    extraEnv: { API_FORMAT: "chat_completions" },
  });

  t.after(async () => {
    await proxy.stop();
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  const response = await fetch(`http://127.0.0.1:${proxyPort}/v1/responses`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      model: "gpt-4o",
      input: "Say hello",
      stream: true,
    }),
  });

  assert.equal(response.status, 200);

  const events = parseSSE(await response.text());
  const textDeltas = events.filter((e) => e.event === "response.output_text.delta");
  assert.equal(textDeltas.length, 2);
  assert.equal(textDeltas[0].data.delta, "Split");
  assert.equal(textDeltas[1].data.delta, " frame");

  const completed = events.find((e) => e.event === "response.completed");
  assert.equal(completed.data.response.output[0].content[0].text, "Split frame");
});

test("chat_completions mode: translates tool calls in response", async (t) => {
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-test-"));
  const upstream = await startChatCompletionsMockUpstream({
    toolCalls: [
      {
        id: "call_abc123",
        name: "get_weather",
        argumentChunks: ['{"loc', 'ation":"NYC"}'],
      },
    ],
  });
  const configPath = await createTempConfig(tempDir);
  const proxyPort = 21011;
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
    extraEnv: { API_FORMAT: "chat_completions" },
  });

  t.after(async () => {
    await proxy.stop();
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  const response = await fetch(`http://127.0.0.1:${proxyPort}/v1/responses`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      model: "gpt-4o",
      input: "What's the weather?",
      stream: true,
      tools: [{ type: "function", name: "get_weather", parameters: { type: "object", properties: { location: { type: "string" } } } }],
    }),
  });

  assert.equal(response.status, 200);

  // Verify request body had tools translated
  const forwardedBody = JSON.parse(upstream.requests[0].bodyText);
  assert.equal(forwardedBody.tools[0].type, "function");
  assert.equal(forwardedBody.tools[0].function.name, "get_weather");

  // Verify response SSE events
  const responseText = await response.text();
  const events = parseSSE(responseText);

  const eventTypes = events.map((e) => e.event);
  assert.ok(eventTypes.includes("response.output_item.added"), "should include output_item.added");
  assert.ok(eventTypes.includes("response.function_call_arguments.delta"), "should include function_call_arguments.delta");
  assert.ok(eventTypes.includes("response.function_call_arguments.done"), "should include function_call_arguments.done");
  assert.ok(eventTypes.includes("response.output_item.done"), "should include output_item.done");
  assert.ok(eventTypes.includes("response.completed"), "should include response.completed");

  // Verify function call details
  const argsDeltas = events.filter((e) => e.event === "response.function_call_arguments.delta");
  assert.equal(argsDeltas.length, 2);
  assert.equal(argsDeltas[0].data.delta, '{"loc');
  assert.equal(argsDeltas[1].data.delta, 'ation":"NYC"}');

  // Verify function_call_arguments.done
  const argsDone = events.find((e) => e.event === "response.function_call_arguments.done");
  assert.equal(argsDone.data.arguments, '{"location":"NYC"}');
  assert.equal(argsDone.data.name, "get_weather");

  // Verify output in response.completed
  const completed = events.find((e) => e.event === "response.completed");
  assert.equal(completed.data.response.output.length, 1);
  assert.equal(completed.data.response.output[0].type, "function_call");
  assert.equal(completed.data.response.output[0].name, "get_weather");
  assert.equal(completed.data.response.output[0].arguments, '{"location":"NYC"}');
});

test("chat_completions mode: translates function_call in input", async (t) => {
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-test-"));
  const upstream = await startChatCompletionsMockUpstream({ textChunks: ["The weather is sunny"] });
  const configPath = await createTempConfig(tempDir);
  const proxyPort = 21012;
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
    extraEnv: { API_FORMAT: "chat_completions" },
  });

  t.after(async () => {
    await proxy.stop();
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  const response = await fetch(`http://127.0.0.1:${proxyPort}/v1/responses`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      model: "gpt-4o",
      stream: true,
      input: [
        { role: "user", content: "What's the weather?" },
        { type: "function_call", id: "fc_123", call_id: "call_abc", name: "get_weather", arguments: '{"location":"NYC"}' },
        { type: "function_call_output", call_id: "call_abc", output: '{"temp":72}' },
      ],
    }),
  });

  assert.equal(response.status, 200);

  // Verify the request was translated correctly
  const forwardedBody = JSON.parse(upstream.requests[0].bodyText);
  assert.equal(forwardedBody.messages.length, 3);
  assert.equal(forwardedBody.messages[0].role, "user");
  assert.equal(forwardedBody.messages[1].role, "assistant");
  assert.equal(forwardedBody.messages[1].tool_calls[0].function.name, "get_weather");
  assert.equal(forwardedBody.messages[2].role, "tool");
  assert.equal(forwardedBody.messages[2].tool_call_id, "call_abc");
});

test("chat_completions mode: non-Responses-API paths are not affected", async (t) => {
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-test-"));
  const upstream = await startMockUpstream();
  const configPath = await createTempConfig(tempDir);
  const proxyPort = 21013;
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
    extraEnv: { API_FORMAT: "chat_completions" },
  });

  t.after(async () => {
    await proxy.stop();
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  // Request to a non-/v1/responses path should not be rewritten
  const response = await fetch(`http://127.0.0.1:${proxyPort}/v1/models`, {
    headers: { "content-type": "application/json" },
  });

  assert.equal(response.status, 200);
  assert.equal(upstream.requests[0].url, "/v1/models");

  const body = await response.json();
  assert.equal(body.ok, true);
});

test("chat_completions mode: direct Chat Completions POST is passed through", async (t) => {
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-test-"));
  const upstream = await startMockUpstream();
  const configPath = await createTempConfig(tempDir);
  const proxyPort = 21016;
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
    extraEnv: { API_FORMAT: "chat_completions" },
  });

  t.after(async () => {
    await proxy.stop();
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  const chatBody = {
    model: "gpt-4o",
    messages: [{ role: "user", content: "hello" }],
    stream: false,
  };

  const response = await fetch(`http://127.0.0.1:${proxyPort}/v1/chat/completions`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(chatBody),
  });

  assert.equal(response.status, 200);
  assert.equal(upstream.requests[0].url, "/v1/chat/completions");
  assert.deepEqual(JSON.parse(upstream.requests[0].bodyText), chatBody);
});

test("chat_completions mode: image_generation tools are still filtered", async (t) => {
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-test-"));
  const upstream = await startChatCompletionsMockUpstream({ textChunks: ["Hi"] });
  const configPath = await createTempConfig(tempDir);
  const proxyPort = 21014;
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
    extraEnv: { API_FORMAT: "chat_completions" },
  });

  t.after(async () => {
    await proxy.stop();
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  });

  const response = await fetch(`http://127.0.0.1:${proxyPort}/v1/responses`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      model: "gpt-4o",
      input: "hello",
      tools: [
        { type: "image_generation" },
        { type: "function", name: "keep_me", parameters: { type: "object", properties: {} } },
      ],
      stream: true,
    }),
  });

  assert.equal(response.status, 200);

  // image_generation should be filtered, only function tool kept
  const forwardedBody = JSON.parse(upstream.requests[0].bodyText);
  assert.equal(forwardedBody.tools.length, 1);
  assert.equal(forwardedBody.tools[0].function.name, "keep_me");
});
