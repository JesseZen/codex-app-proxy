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
