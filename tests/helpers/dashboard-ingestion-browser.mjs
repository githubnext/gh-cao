import { chromium, expect } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { createInterface } from "node:readline";

const address = new URL(process.env.CAO_STRESS_URL);
if (address.protocol !== "http:" || address.hostname !== "127.0.0.1") {
  throw new Error("Browser ingestion stress requires numeric-loopback HTTP.");
}
const reportDirectory = process.env.CAO_STRESS_REPORT_DIR;
const expectedRuns = Number(process.env.CAO_STRESS_RUNS);
const samples = [];
const revisions = new Set();
const errors = [];
const sockets = [];
const pending = new Set();
const requestStarts = new Map();
let browser;
let status = "incomplete";

try {
  await mkdir(reportDirectory, { recursive: true });
  browser = await chromium.launch({ args: ["--disable-background-networking"] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: "block" });
  await context.route("**/*", (route) => {
    if (new URL(route.request().url()).origin !== address.origin) {
      errors.push("Browser attempted a non-local request");
      return route.abort("blockedbyclient");
    }
    return route.continue();
  });
  const page = await context.newPage();
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  page.on("websocket", (socket) => sockets.push(new URL(socket.url()).pathname));
  page.on("request", (request) => {
    if (/^\/api\/v1\/(?:query|refresh)$/.test(new URL(request.url()).pathname)) {
      requestStarts.set(request, performance.now());
    }
  });
  page.on("requestfailed", (request) => {
    if (requestStarts.has(request)) errors.push(`API request failed: ${new URL(request.url()).pathname}`);
  });
  page.on("response", (response) => {
    const request = response.request();
    if (!requestStarts.has(request)) return;
    const operation = (async () => {
      const payload = await response.json();
      const durationMs = performance.now() - requestStarts.get(request);
      samples.push({ path: new URL(response.url()).pathname, durationMs, status: response.status() });
      if (!response.ok()) errors.push(`API returned ${response.status()}`);
      if (Number.isSafeInteger(payload.revision)) revisions.add(payload.revision);
    })().catch((error) => errors.push(error.message));
    pending.add(operation);
    void operation.finally(() => pending.delete(operation));
  });
  await page.goto(address.href, { waitUntil: "domcontentloaded" });
  await expect(page.locator('meta[name="dashboard-data-backend"]')).toHaveAttribute("content", "server-http");
  await expect(page.locator('meta[name="dashboard-storage-backend"]')).toHaveAttribute("content", "postgres");
  const indicator = page.locator(".dashboard-current-status > button");
  await expect(indicator).toBeVisible({ timeout: 30_000 });
  await indicator.focus();
  const tooltip = page.locator("#dashboard-current-status-tooltip");
  await expect(tooltip).toBeVisible();
  await expect(tooltip).toContainText("Runs received this pass0");
  const initialURL = page.url();
  await writeFile(join(process.env.CAO_STRESS_WORKSPACE, "browser-ready"), "");
  const input = createInterface({ input: process.stdin });
  for await (const line of input) {
    if (line !== "complete") throw new Error("Unknown stress browser command.");
    await expect(tooltip).toContainText(`Runs ingested${expectedRuns}`, { timeout: 30_000 });
    await expect(tooltip).toContainText("168");
    expect(page.url()).toBe(initialURL);
    expect(sockets).toEqual(["/api/v1/events/ws"]);
    await Promise.all(pending);
    expect(revisions.size).toBeGreaterThanOrEqual(3);
    expect(samples.length).toBeGreaterThan(5);
    expect(Math.max(...samples.map(({ durationMs }) => durationMs))).toBeLessThan(15_000);
    expect(errors).toEqual([]);
    await page.screenshot({ path: join(reportDirectory, "desktop.png"), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.locator(".mobile-ingestion-indicator")).toBeVisible();
    await page.screenshot({ path: join(reportDirectory, "mobile.png"), fullPage: true });
    status = "passed";
    break;
  }
  if (status !== "passed") throw new Error("Ingestion ended before browser assertions completed.");
  input.close();
} catch (error) {
  const message = String(error?.stack ?? error).replaceAll(address.searchParams.get("access_token") ?? "", "[redacted]");
  errors.push(message);
  console.error(message);
  process.exitCode = 1;
} finally {
  await browser?.close();
  const sorted = samples.map(({ durationMs }) => durationMs).sort((a, b) => a - b);
  await writeFile(join(reportDirectory, "report.json"), JSON.stringify({
    status, synthetic: true, upstreamRuns: 1600, expectedRuns, revisions: [...revisions],
    sockets, queryCount: samples.length, maxLatencyMs: sorted.at(-1),
    p95LatencyMs: sorted[Math.max(0, Math.ceil(sorted.length * 0.95) - 1)], samples, errors,
  }, null, 2));
}
