import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { chmod, mkdir, mkdtemp, readFile, readdir, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { startLocalDashboardPreview } from "../../com.github.copilot/extensions/cao-dashboard/local-preview.mjs";

async function fixture(t, mode = "ready") {
  const workspace = await realpath(await mkdtemp(join(tmpdir(), "cao-cli-canvas-")));
  t.after(() => rm(workspace, { recursive: true, force: true }));
  const ghExecutable = join(workspace, "gh");
  await writeFile(ghExecutable, `#!${process.execPath}
const { createServer } = require("node:http");
const { writeFileSync } = require("node:fs");
writeFileSync("invocation.json", JSON.stringify({ args: process.argv.slice(2), cwd: process.cwd() }));
const mode = ${JSON.stringify(mode)};
if (mode === "exit") {
  process.stderr.write("private diagnostics must not leak\\n");
  process.exit(3);
}
const server = createServer((request, response) => {
  response.statusCode = mode === "unhealthy" ? 503 : 200;
  response.end("native dashboard");
});
server.listen(0, "127.0.0.1", () => {
  const url = "http://127.0.0.1:" + server.address().port + "/?access_token=${"a".repeat(43)}#page-overview";
  if (mode === "ready" || mode === "unhealthy") process.stdout.write(JSON.stringify({ url }) + "\\n");
  else if (mode === "remote") process.stdout.write(JSON.stringify({ url: "https://example.com/?access_token=${"a".repeat(43)}#page-overview" }) + "\\n");
  else if (mode === "invalid") process.stdout.write("not json\\n");
});
process.stdin.resume();
process.stdin.on("end", () => {
  writeFileSync("closed", "stdin closed");
  server.close(() => process.exit(0));
});
`);
  await chmod(ghExecutable, 0o755);
  return { workspace, ghExecutable };
}

test("canvas runs the installed CLI in the workspace without snapshot or staged assets", {
  skip: process.platform === "win32",
}, async (t) => {
  const { workspace, ghExecutable } = await fixture(t);
  await mkdir(join(workspace, "dashboard"));
  await writeFile(join(workspace, "dashboard", "canvas-server.mjs"), 'throw new Error("Untrusted workspace code");');
  const preview = await startLocalDashboardPreview({
    workingDirectory: workspace, repository: "acme/control", ghExecutable,
  });
  t.after(() => preview.close());
  assert.equal(await (await fetch(preview.url)).text(), "native dashboard");
  assert.deepEqual(JSON.parse(await readFile(join(workspace, "invocation.json"), "utf8")), {
    cwd: workspace,
    args: ["cao", "dashboard", "--json", "--exit-on-stdin-close",
      "--listen", "127.0.0.1:0", "--repo", "acme/control"],
  });
  assert.equal((await readdir(workspace)).some((name) => name.startsWith(".cao-dashboard-preview-")), false);
  await Promise.all([preview.close(), preview.close()]);
  assert.equal(await readFile(join(workspace, "closed"), "utf8"), "stdin closed");
  await assert.rejects(fetch(preview.url));
});

test("canvas leaves default repository resolution to gh cao dashboard", {
  skip: process.platform === "win32",
}, async (t) => {
  const { workspace, ghExecutable } = await fixture(t);
  const preview = await startLocalDashboardPreview({ workingDirectory: workspace, ghExecutable });
  t.after(() => preview.close());
  const { args } = JSON.parse(await readFile(join(workspace, "invocation.json"), "utf8"));
  assert.equal(args.includes("--repo"), false);
});

for (const [mode, message] of [
  ["exit", /Install or update the gh-cao extension/],
  ["invalid", /invalid readiness message/],
  ["remote", /invalid loopback URL/],
  ["unhealthy", /readiness check/],
  ["timeout", /Timed out starting gh cao dashboard/],
]) test(`canvas cleans up CLI startup failure: ${mode}`, { skip: process.platform === "win32" }, async (t) => {
  const { workspace, ghExecutable } = await fixture(t, mode);
  await assert.rejects(startLocalDashboardPreview({
    workingDirectory: workspace, ghExecutable, startupTimeout: mode === "timeout" ? 500 : 5_000,
  }), message);
  if (mode !== "exit") assert.equal(await readFile(join(workspace, "closed"), "utf8"), "stdin closed");
});

test("canvas reports a missing CLI executable", async (t) => {
  const workspace = await realpath(await mkdtemp(join(tmpdir(), "cao-no-cli-")));
  t.after(() => rm(workspace, { recursive: true, force: true }));
  await assert.rejects(startLocalDashboardPreview({
    workingDirectory: workspace, ghExecutable: join(workspace, "missing-gh"),
  }), /ENOENT/);
});

test("canvas rejects repository arguments before starting the CLI", async () => {
  for (const repository of ["--help", "acme/../other", "acme/repo\n--repo=other/repo"]) {
    await assert.rejects(startLocalDashboardPreview({
      workingDirectory: process.cwd(), repository,
    }), /exact OWNER\/REPO/);
  }
});

test("extension SIGTERM closes the native dashboard before exiting", {
  skip: process.platform === "win32", timeout: 15_000,
}, async (t) => {
  const { workspace } = await fixture(t);
  const sdkUrl = import.meta.resolve("@github/copilot-sdk/extension");
  const shim = `export { CanvasError, createCanvas } from ${JSON.stringify(sdkUrl)};
export async function joinSession(config) {
  const preview = await config.canvases[0].open({
    sessionId: "test", extensionId: "test:cao", canvasId: "cao-dashboard",
    instanceId: "preview", session: { workingDirectory: process.cwd() }
  });
  process.stdout.write(JSON.stringify(preview) + "\\n");
}`;
  const shimUrl = `data:text/javascript,${encodeURIComponent(shim)}`;
  const preload = `import { registerHooks } from "node:module";
registerHooks({ resolve(specifier, context, next) {
  return specifier === "@github/copilot-sdk/extension"
    ? { url: ${JSON.stringify(shimUrl)}, shortCircuit: true }
    : next(specifier, context);
} });`;
  const child = spawn(process.execPath, [
    "--import", `data:text/javascript,${encodeURIComponent(preload)}`,
    fileURLToPath(new URL("../../com.github.copilot/extensions/cao-dashboard/extension.mjs", import.meta.url)),
  ], {
    cwd: workspace, env: { ...process.env, PATH: `${workspace}:${process.env.PATH}` },
    stdio: ["ignore", "pipe", "pipe"],
  });
  const lines = createInterface({ input: child.stdout });
  let stderr = "";
  child.stderr.on("data", (chunk) => { stderr += chunk; });
  const exit = once(child, "exit");
  try {
    const [line] = await Promise.race([
      once(lines, "line"),
      exit.then(() => { throw new Error(`Extension exited before opening: ${stderr}`); }),
    ]);
    const { url } = JSON.parse(line);
    assert.equal((await fetch(url)).status, 200);
    child.kill("SIGTERM");
    const [code, signal] = await exit;
    assert.equal(code, 0, stderr);
    assert.equal(signal, null);
    assert.equal(await readFile(join(workspace, "closed"), "utf8"), "stdin closed");
    await assert.rejects(fetch(url));
  } finally {
    lines.close();
    if (child.exitCode === null && child.signalCode === null) {
      child.kill("SIGTERM");
      await exit;
    }
  }
});
