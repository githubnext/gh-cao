import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import https from "node:https";
import { join } from "node:path";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const execute = promisify(execFile);
const root = fileURLToPath(new URL("../../", import.meta.url));

test("kind runs maintenance and the real hosted HTTPS application without fabricating evidence", {
  skip: process.env.CAO_KUBERNETES_INTEGRATION !== "1",
  timeout: 420_000,
}, async (t) => {
  const cluster = process.env.CAO_LOCAL_KIND_CLUSTER;
  assert.match(cluster ?? "", /^[a-z0-9]([-a-z0-9]{0,30}[a-z0-9])?$/, "Select an explicit disposable kind cluster");
  const kubectl = (...args) => execute("kubectl", ["--context", `kind-${cluster}`, ...args], {
    cwd: root, timeout: 30_000,
  });
  for (const namespace of ["cao-local", "cao-local-system"]) {
    const { stdout } = await kubectl("get", "namespace", namespace, "-o", "json");
    assert.equal(JSON.parse(stdout).metadata.labels["app.kubernetes.io/part-of"], "cao-local");
  }
  const existing = await kubectl("-n", "cao-local", "get", "deployment", "cao-web", "--ignore-not-found", "-o", "name");
  assert.equal(existing.stdout.trim(), "", "Refusing to replace an existing web deployment");

  const { stdout: job } = await kubectl("-n", "cao-local-system", "create", "job",
    "--from=cronjob/cao-canonical-maintenance", `cao-maintenance-test-${Date.now()}`, "-o", "name");
  t.after(() => kubectl("-n", "cao-local-system", "delete", job.trim(), "--wait=false"));
  let completed = false;
  for (let attempt = 0; attempt < 120; attempt++) {
    const { stdout } = await kubectl("-n", "cao-local-system", "get", job.trim(), "-o", "json");
    const conditions = JSON.parse(stdout).status?.conditions ?? [];
    assert.ok(!conditions.some(({ type, status }) => type === "Failed" && status === "True"), "Maintenance Job failed");
    if (conditions.some(({ type, status }) => type === "Complete" && status === "True")) {
      completed = true;
      break;
    }
    await delay(1000);
  }
  assert.ok(completed, "Maintenance Job timed out");

  const directory = await mkdtemp(join(root, ".tmp/kubernetes-smoke-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const oauth = join(directory, "oauth.env");
  await writeFile(oauth, [
    "CAO_GITHUB_CLIENT_ID=kubernetes-test-only",
    "CAO_GITHUB_CLIENT_SECRET=kubernetes-test-only",
    "CAO_GITHUB_ALLOWED_USERS=kubernetes-test-only",
    "CAO_GITHUB_ADMIN_USERS=kubernetes-test-only",
    "",
  ].join("\n"), { mode: 0o600 });

  // With no canonical snapshot, serve must time out its rollout, not claim readiness.
  t.after(() => kubectl("-n", "cao-local", "delete", "deployment/cao-web", "service/cao-web",
    "--ignore-not-found", "--wait=false"));
  await assert.rejects(execute("bash", ["server/kubernetes/local.sh", "serve", oauth], {
    cwd: root, env: process.env, timeout: 240_000,
  }), (error) => {
    assert.equal(error.code, 1, error.stderr);
    assert.match(error.stderr, /timed out waiting for the condition/);
    return true;
  });

  const forwarding = spawn("kubectl", ["--context", `kind-${cluster}`, "-n", "cao-local",
    "port-forward", "--address", "127.0.0.1", "deployment/cao-web", ":8443", "--pod-running-timeout=120s"], {
    cwd: root, stdio: ["ignore", "pipe", "pipe"],
  });
  let forwardLog = "";
  forwarding.stdout.on("data", (chunk) => { forwardLog += chunk; });
  forwarding.stderr.on("data", (chunk) => { forwardLog += chunk; });
  const closed = new Promise((resolve, reject) => {
    forwarding.once("error", reject);
    forwarding.once("close", resolve);
  });
  t.after(async () => {
    if (forwarding.exitCode === null && forwarding.signalCode === null) forwarding.kill();
    await closed;
  });
  let port;
  for (let attempt = 0; attempt < 120; attempt++) {
    port = /Forwarding from 127\.0\.0\.1:(\d+) -> 8443/.exec(forwardLog)?.[1];
    if (port) break;
    assert.equal(forwarding.exitCode, null, forwardLog);
    await delay(1000);
  }
  assert.ok(port, `Port-forward did not start: ${forwardLog}`);
  const ca = await readFile(join(root, ".tmp/cao-kubernetes-local/ca.crt"));
  const get = (path) => new Promise((resolve, reject) => {
    const request = https.get({
      hostname: "localhost", family: 4, port, path, ca,
      headers: { Host: "localhost:8443" }, timeout: 10_000,
    }, (response) => {
      let body = "";
      response.setEncoding("utf8");
      response.on("data", (chunk) => { body += chunk; });
      response.on("end", () => resolve({ status: response.statusCode, headers: response.headers, body }));
      response.on("error", reject);
    });
    request.on("timeout", () => request.destroy(new Error("HTTPS request timed out")));
    request.on("error", reject);
  });

  const health = await get("/api/health");
  assert.equal(health.status, 200, health.body);
  assert.equal(JSON.parse(health.body).status, "healthy");
  const readiness = await get("/api/readiness");
  assert.equal(readiness.status, 503, readiness.body);
  const unavailable = JSON.parse(readiness.body);
  assert.equal(unavailable.ready, false);
  assert.equal(unavailable.operational.connected, true);
  assert.equal(unavailable.data.available, false);
  assert.equal(unavailable.data.rebuildRequired, true);
  const login = await get("/auth/login");
  assert.equal(login.status, 302, login.body);
  const redirect = new URL(login.headers.location);
  assert.equal(redirect.origin, "https://github.com");
  assert.equal(redirect.pathname, "/login/oauth/authorize");
  assert.equal(redirect.searchParams.get("redirect_uri"), "https://localhost:8443/auth/callback");
  assert.ok(login.headers["set-cookie"].some((cookie) => /cao_oauth_state=/.test(cookie) && /Secure/.test(cookie) && /HttpOnly/.test(cookie)));
  const denied = await get("/api/auth/session");
  assert.equal(denied.status, 401, denied.body);
  const { stdout } = await kubectl("-n", "cao-local", "get", "pods", "-l", "app=cao-web", "-o", "json");
  const pods = JSON.parse(stdout).items;
  assert.equal(pods.length, 1);
  assert.equal(pods[0].status.phase, "Running");
  assert.equal(pods[0].status.containerStatuses[0].ready, false);
  assert.equal(pods[0].status.containerStatuses[0].restartCount, 0);
});
