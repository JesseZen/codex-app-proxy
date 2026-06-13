import { mkdtemp, rm, writeFile } from "node:fs/promises";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import process from "node:process";
import { once } from "node:events";
import { spawn } from "node:child_process";
import { performance } from "node:perf_hooks";

function parseNumber(value, fallback) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function percentile(values, ratio) {
  if (values.length === 0) {
    return 0;
  }

  const sorted = [...values].sort((left, right) => left - right);
  const index = Math.min(sorted.length - 1, Math.floor(sorted.length * ratio));
  return sorted[index];
}

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

async function startMockUpstream({ responseDelayMs, responseBytes }) {
  const payload = "x".repeat(responseBytes);
  let requestCount = 0;

  const server = http.createServer(async (req, res) => {
    for await (const _chunk of req) {
      // consume the request body to simulate a normal upstream
    }

    requestCount += 1;

    if (responseDelayMs > 0) {
      await new Promise((resolve) => {
        setTimeout(resolve, responseDelayMs);
      });
    }

    res.writeHead(200, { "content-type": "application/json; charset=utf-8" });
    res.end(JSON.stringify({ ok: true, requestCount, payload }));
  });

  server.listen(0, "127.0.0.1");
  await once(server, "listening");

  return {
    server,
    url: `http://127.0.0.1:${server.address().port}`,
    getRequestCount() {
      return requestCount;
    },
    async close() {
      server.close();
      await once(server, "close");
    },
  };
}

async function waitForProxyReady(child, port) {
  const readyText = `Listening on http://127.0.0.1:${port}`;
  const timeoutMs = 10_000;
  const startedAt = Date.now();

  while (Date.now() - startedAt < timeoutMs) {
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

  throw new Error(`proxy did not start within ${timeoutMs}ms\n${child.stdoutText}\n${child.stderrText}`);
}

async function startProxy({ baseUrl, configPath, port }) {
  const child = spawn(process.execPath, ["src/server.js"], {
    cwd: process.cwd(),
    env: {
      ...process.env,
      PORT: String(port),
      BASE_URL: baseUrl,
      API_KEY: "bench-upstream-key",
      CODEX_CONFIG_PATH: configPath,
      ACTIVE_PROVIDER: "",
      LOG_EVERY_REQUEST: "0",
      LOG_FILTERED_REQUESTS: "0",
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

  await waitForProxyReady(child, port);

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

async function runLoad({ targetUrl, requests, concurrency, requestBody }) {
  const latencies = [];
  let okCount = 0;
  let errorCount = 0;
  let nextIndex = 0;
  const startedAt = performance.now();

  async function worker() {
    while (true) {
      const currentIndex = nextIndex;
      nextIndex += 1;

      if (currentIndex >= requests) {
        return;
      }

      const requestStartedAt = performance.now();
      try {
        const response = await fetch(targetUrl, {
          method: "POST",
          headers: {
            "content-type": "application/json",
          },
          body: requestBody,
        });
        await response.arrayBuffer();
        latencies.push(performance.now() - requestStartedAt);
        if (response.ok) {
          okCount += 1;
        } else {
          errorCount += 1;
        }
      } catch {
        errorCount += 1;
      }
    }
  }

  await Promise.all(Array.from({ length: concurrency }, () => worker()));
  const durationMs = performance.now() - startedAt;

  return {
    durationMs,
    errorCount,
    okCount,
    requestsPerSecond: requests / (durationMs / 1000),
    latencyMs: {
      p50: percentile(latencies, 0.5),
      p95: percentile(latencies, 0.95),
      p99: percentile(latencies, 0.99),
      max: percentile(latencies, 1),
    },
  };
}

function formatMetrics(label, metrics) {
  return {
    label,
    okCount: metrics.okCount,
    errorCount: metrics.errorCount,
    durationMs: Number(metrics.durationMs.toFixed(1)),
    requestsPerSecond: Number(metrics.requestsPerSecond.toFixed(1)),
    p50Ms: Number(metrics.latencyMs.p50.toFixed(1)),
    p95Ms: Number(metrics.latencyMs.p95.toFixed(1)),
    p99Ms: Number(metrics.latencyMs.p99.toFixed(1)),
    maxMs: Number(metrics.latencyMs.max.toFixed(1)),
  };
}

async function main() {
  const requests = parseNumber(process.env.BENCH_REQUESTS, 400);
  const concurrency = parseNumber(process.env.BENCH_CONCURRENCY, 40);
  const bodyBytes = parseNumber(process.env.BENCH_BODY_BYTES, 32_768);
  const responseDelayMs = parseNumber(process.env.BENCH_UPSTREAM_DELAY_MS, 0);
  const responseBytes = parseNumber(process.env.BENCH_RESPONSE_BYTES, 256);
  const proxyPort = parseNumber(process.env.BENCH_PROXY_PORT, 21100);

  const filler = "x".repeat(Math.max(0, bodyBytes));
  const requestBody = JSON.stringify({
    model: "gpt-test",
    input: filler,
    tools: [
      { type: "image_generation" },
      { type: "function", name: "keep_me" },
    ],
    tool_choice: "image_generation",
  });

  const tempDir = await mkdtemp(path.join(os.tmpdir(), "codex-proxy-bench-"));
  const configPath = await createTempConfig(tempDir);
  const upstream = await startMockUpstream({ responseDelayMs, responseBytes });
  const proxy = await startProxy({
    baseUrl: upstream.url,
    configPath,
    port: proxyPort,
  });

  try {
    console.log(
      JSON.stringify(
        {
          config: {
            requests,
            concurrency,
            requestBodyBytes: Buffer.byteLength(requestBody),
            upstreamDelayMs: responseDelayMs,
            responseBytes,
          },
        },
        null,
        2,
      ),
    );

    const direct = await runLoad({
      targetUrl: `${upstream.url}/v1/responses`,
      requests,
      concurrency,
      requestBody,
    });
    const viaProxy = await runLoad({
      targetUrl: `http://127.0.0.1:${proxyPort}/v1/responses`,
      requests,
      concurrency,
      requestBody,
    });

    console.log(
      JSON.stringify(
        {
          results: [
            formatMetrics("direct-upstream", direct),
            formatMetrics("via-proxy", viaProxy),
          ],
          proxyOverhead: {
            rpsDropPercent: Number(
              (((direct.requestsPerSecond - viaProxy.requestsPerSecond) / direct.requestsPerSecond) * 100).toFixed(1),
            ),
            p95IncreaseMs: Number((viaProxy.latencyMs.p95 - direct.latencyMs.p95).toFixed(1)),
          },
          upstreamRequestCount: upstream.getRequestCount(),
        },
        null,
        2,
      ),
    );
  } finally {
    await proxy.stop();
    await upstream.close();
    await rm(tempDir, { recursive: true, force: true });
  }
}

await main();
