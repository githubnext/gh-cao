/**
 * Diagnostic driver that ingests synthetic enterprise demo data and records a
 * data-worker heap timeline alongside the worker's own debug log output.
 *
 * It exists to attribute data-worker memory growth to a stage: the heap peak is
 * printed with a timeline, and every canonical collection read is summarized so
 * a whole-database read can be told apart from a page-scoped one. Use it when
 * `npm run test:memory:dashboard-overview` reports a budget regression.
 *
 * Usage: node tests/e2e/dashboard-worker-memory-trace.mjs [--eager] [--debug=data:*]
 */
import { createServer } from "node:net";
import { chromium } from "playwright";
import { startDashboardPagesPreview } from "../helpers/dashboard-pages-preview.mjs";
const eager = process.argv.includes("--eager");
const debugPattern = process.argv.find((value) => value.startsWith("--debug="))?.slice(8) ?? "data:*";
const heapMb = Number(process.env.TRACE_HEAP_MB ?? 2048);
const megabyte = 1024 * 1024;
const mb = (bytes) => `${(bytes / megabyte).toFixed(1)}MB`;

const freePort = () => new Promise((resolve) => {
  const probe = createServer();
  probe.listen(0, "127.0.0.1", () => {
    const { port } = probe.address();
    probe.close(() => resolve(port));
  });
});

async function workerTargets(port) {
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  return targets.filter((target) =>
    target.type === "worker" && target.url.split(/[?#]/, 1)[0].endsWith("/data-worker.js"));
}

function cdpClient(endpoint) {
  const socket = new WebSocket(endpoint);
  const pending = new Map();
  const listeners = [];
  let nextId = 1;
  socket.addEventListener("message", (event) => {
    const message = JSON.parse(event.data);
    if (message.id !== undefined) {
      const settle = pending.get(message.id);
      if (!settle) return;
      pending.delete(message.id);
      if (message.error) settle.reject(new Error(message.error.message));
      else settle.resolve(message.result);
      return;
    }
    for (const listener of listeners) listener(message);
  });
  socket.addEventListener("close", () => {
    for (const [id, settle] of pending) {
      pending.delete(id);
      settle.reject(new Error("worker target closed"));
    }
  });
  return {
    socket,
    ready: new Promise((resolve, reject) => {
      socket.addEventListener("open", () => resolve(), { once: true });
      socket.addEventListener("error", () => reject(new Error("cdp connect failed")), { once: true });
    }),
    on: (listener) => listeners.push(listener),
    send(method, params = {}) {
      const id = nextId;
      nextId += 1;
      return new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
        socket.send(JSON.stringify({ id, method, params }));
      });
    },
  };
}

console.log("Synthetic enterprise demo: browser-worker diagnostics, not operational freshness or completeness evidence.");
const preview = await startDashboardPagesPreview();
const port = await freePort();
const browser = await chromium.launch({
  args: [
    "--no-sandbox",
    "--disable-dev-shm-usage",
    `--remote-debugging-port=${port}`,
    `--js-flags=--max-old-space-size=${heapMb}`,
  ],
});
const page = await browser.newPage();
await page.addInitScript(() => {
  window.__refresh = [];
  document.addEventListener("dashboard-data", (event) => {
    if (event.detail?.kind === "refresh") window.__refresh.push(event.detail.status);
  });
});
page.on("console", (message) => {
  if (message.text().includes("[cao:")) console.log(`[page] ${message.text()}`);
});
page.on("pageerror", (error) => console.log(`[page-error] ${error.message}`));

const parameters = new URLSearchParams({ debug: debugPattern });
if (eager) parameters.set("debug-eager-ingest", "1");
const url = `${preview.url}/?${parameters}#page-overview`;
console.log(`Loading ${url}`);
const startedAt = Date.now();
await page.goto(url, { waitUntil: "domcontentloaded" });

const samples = [];
const collectionReads = [];
let peak = 0;
let attachments = 0;
let terminated = false;
let client = null;

function instrument(active, targetUrl) {
  attachments += 1;
  console.log(`[attach ${((Date.now() - startedAt) / 1000).toFixed(1)}s] #${attachments} ${targetUrl.slice(-60)}`);
  active.send("Runtime.enable").catch(() => {});
  active.on((message) => {
    if (message.method === "Runtime.consoleAPICalled") {
      const text = (message.params.args ?? [])
        .map((argument) => {
          if (argument.value !== undefined) return String(argument.value);
          const properties = argument.preview?.properties;
          if (properties) {
            return `{${properties.map(({ name, value }) => `${name}=${value}`).join(", ")}}`;
          }
          return argument.description ?? argument.type;
        })
        .join(" ");
      const elapsed = (Date.now() - startedAt) / 1000;
      const read = /multi-store collection read \{storeCount=(\d+).*?durationMs=([\d.]+)/.exec(text);
      if (read) {
        collectionReads.push({
          elapsed,
          storeCount: Number(read[1]),
          durationMs: Number(read[2]),
          stores: /stores=([^,}]*)/.exec(text)?.[1] ?? "",
        });
      }
      console.log(`[worker ${elapsed.toFixed(1)}s] ${text}`);
    }
    if (message.method === "Runtime.exceptionThrown") {
      console.log(`[worker-exception] ${message.params.exceptionDetails.text}`);
    }
  });
  active.socket.addEventListener("close", () => {
    console.log(`[worker ${((Date.now() - startedAt) / 1000).toFixed(1)}s] TARGET CLOSED `
      + `(peak so far ${mb(peak)})`);
    if (client === active) client = null;
  });
}

/** Keeps a probe attached to whichever data worker is currently alive. */
async function superviseAttachment() {
  while (!terminated) {
    if (!client) {
      const [target] = await workerTargets(port).catch(() => []);
      if (target) {
        const candidate = cdpClient(target.webSocketDebuggerUrl);
        await candidate.ready.catch(() => null);
        if (candidate.socket.readyState === WebSocket.OPEN) {
          client = candidate;
          instrument(candidate, target.url);
        }
      }
    }
    await new Promise((ready) => setTimeout(ready, 100));
  }
}
superviseAttachment();

const sampler = setInterval(async () => {
  if (!client) return;
  const usage = await client.send("Runtime.getHeapUsage").catch(() => null);
  if (!usage) return;
  const elapsed = (Date.now() - startedAt) / 1000;
  samples.push({ elapsed, used: usage.usedSize, total: usage.totalSize });
  if (usage.usedSize > peak) {
    peak = usage.usedSize;
    console.log(`[heap ${elapsed.toFixed(1)}s] new peak used=${mb(usage.usedSize)} total=${mb(usage.totalSize)}`);
  }
}, 250);

const finished = await page
  .waitForFunction(() => window.__refresh?.includes("completed"), null, { timeout: 900_000 })
  .then(() => "ingestion-completed")
  .catch((error) => `ingestion-failed: ${error.message.split("\n")[0]}`);
terminated = true;
clearInterval(sampler);
console.log(`Worker attachments: ${attachments}`);

console.log(`\nOutcome: ${finished}`);
console.log(`Peak data-worker heap: ${mb(peak)} across ${samples.length} samples`);
for (const sample of samples.filter((_, index) => index % 8 === 0)) {
  console.log(`  ${sample.elapsed.toFixed(1)}s used=${mb(sample.used)} total=${mb(sample.total)}`);
}
const state = await page.evaluate(async () => {
  const databases = await indexedDB.databases();
  const descriptor = databases.find(({ name }) => name?.startsWith("gh-aw-cao-dashboard-data"));
  if (!descriptor?.name) return { counts: {} };
  const database = await new Promise((resolve) => {
    const request = indexedDB.open(descriptor.name, descriptor.version);
    request.onsuccess = () => resolve(request.result);
  });
  const names = [...database.objectStoreNames];
  const counts = Object.fromEntries(await Promise.all(names.map((name) => new Promise((resolve) => {
    const request = database.transaction(name, "readonly").objectStore(name).count();
    request.onsuccess = () => resolve([name, request.result]);
  }))));
  database.close();
  return { counts };
}).catch((error) => ({ error: error.message }));
console.log(`Canonical database: ${JSON.stringify(state)}`);

// A read that touches every entity store materializes the whole database in
// the worker, which is the dominant source of ingestion heap pressure.
const storeCount = Object.keys(state.counts ?? {}).filter((name) => name !== "transactions").length;
console.log(`\nCanonical collection reads (${collectionReads.length}):`);
for (const read of collectionReads) {
  const scope = storeCount > 0 && read.storeCount >= storeCount ? "WHOLE-DATABASE" : "scoped";
  console.log(`  ${read.elapsed.toFixed(1)}s ${scope} storeCount=${read.storeCount} `
    + `durationMs=${read.durationMs.toFixed(0)} stores=${read.stores}`);
}

await browser.close();
await preview.close();
process.exit(0);
