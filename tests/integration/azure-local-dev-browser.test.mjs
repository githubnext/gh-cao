import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { get } from "node:http";
import { join } from "node:path";
import test from "node:test";
import { chromium } from "../../dashboard/site/node_modules/playwright/index.mjs";

if (!process.argv[2]) throw new Error("explicit GitHub development runtime JSON is required");
const runtime = JSON.parse(await readFile(process.argv[2], "utf8"));
assert.equal(runtime.profile, "github");
const origin = new URL(runtime.baseUrl);
assert.equal(origin.protocol, "http:");
assert.equal(origin.hostname, "localhost");
assert.equal(origin.username + origin.password + origin.search + origin.hash, "");

test("real Functions OAuth preserves Secure browser cookies and validates callback state", async () => {
  const browser = await chromium.launch({
    headless: true,
    args: ["--no-proxy-server"],
    ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } : {}),
  });
  try {
    const context = await browser.newContext();
    let state;
    await context.route(`${runtime.baseUrl}/auth/login`, async (route) => {
      // Fetch independently so only the browser can accept the state cookie.
      const response = await new Promise((resolve, reject) => {
        get(route.request().url(), { headers: route.request().headers() }, resolve).on("error", reject);
      });
      response.resume();
      assert.equal(response.statusCode, 302);
      const target = new URL(response.headers.location);
      assert.equal(target.origin, "https://github.com");
      assert.equal(target.pathname, "/login/oauth/authorize");
      assert.equal(target.searchParams.get("redirect_uri"), `${runtime.baseUrl}/auth/callback`);
      state = target.searchParams.get("state");
      // Preserve the actual cookie headers, but do not follow the GitHub redirect.
      const headers = Object.fromEntries(Object.entries(response.headers)
        .map(([name, value]) => [name, Array.isArray(value) ? value.join("\n") : value]));
      delete headers["content-length"];
      await route.fulfill({
        status: 200,
        headers,
        body: "<title>Intercepted GitHub authorization</title>",
      });
    });
    const page = await context.newPage();
    await page.goto(`${runtime.baseUrl}/auth/login`);
    assert.ok(state);
    const cookies = () => context.cookies(`${runtime.baseUrl}/auth/callback`);
    const hasState = async () => (await cookies()).some((cookie) => cookie.name === "cao_oauth_state");
    const cookie = (await cookies()).find((cookie) => cookie.name === "cao_oauth_state");
    assert.ok(cookie, "browser must preserve the state cookie on HTTP localhost");
    assert.equal(cookie.secure, true);
    assert.equal(cookie.httpOnly, true);
    assert.equal(cookie.sameSite, "Lax");
    const invalid = await page.goto(`${runtime.baseUrl}/auth/callback?state=wrong&error=access_denied`);
    assert.equal(invalid.status(), 400);
    assert.ok(await hasState(), "invalid state must not consume the real login cookie");
    const denied = await page.goto(`${runtime.baseUrl}/auth/callback?state=${encodeURIComponent(state)}&error=access_denied`);
    assert.equal(denied.status(), 400);
    assert.equal(await hasState(), false, "valid callback state is consumed before provider denial is handled");
    assert.equal((await context.request.get(`${runtime.baseUrl}/api/repositories`)).status(), 401);
  } finally {
    await browser.close();
  }
});

test("real development host binds only numeric loopback and does not log configured secrets", async () => {
  const settings = JSON.parse(await readFile(join(runtime.applicationDirectory, "local.settings.json"), "utf8")).Values;
  for (const name of await readdir(join(runtime.stateDirectory, "logs"))) {
    const text = await readFile(join(runtime.stateDirectory, "logs", name), "utf8");
    for (const secret of [
      settings.CAO_GITHUB_CLIENT_SECRET,
      settings.CAO_SESSION_SECRET,
      new URL(settings.CAO_POSTGRES_URL).password,
    ]) {
      assert.ok(secret && !text.includes(secret), `configured secrets must not appear in ${name}`);
    }
  }
  const port = Number(origin.port).toString(16).toUpperCase().padStart(4, "0");
  const listeners = (await readFile("/proc/net/tcp", "utf8")).split("\n")
    .map((line) => line.trim().split(/\s+/))
    .filter((fields) => fields[1]?.endsWith(`:${port}`) && fields[3] === "0A");
  assert.ok(listeners.length > 0);
  assert.ok(listeners.every((fields) => fields[1] === `0100007F:${port}`), "Functions must bind only 127.0.0.1");
});
