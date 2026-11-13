import assert from "node:assert/strict";
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { test } from "node:test";
import {
  createCliActionAdmission,
  cliActionRequiresGitHubQuota,
  detectCliRateLimit,
} from "../../dashboard/cli-action-quota.mjs";

const helper = `
  const readline = require("node:readline");
  let reservation = 0;
  readline.createInterface({input:process.stdin}).on("line", line => {
    const request = JSON.parse(line);
    process.stdout.write(JSON.stringify({
      id: request.id, capacity: request.maxConcurrency, reason: "available",
      ...(request.operation === "admit" ? {reservationId: String(++reservation)} : {}),
      unexpectedRawRecord: "must-not-leak"
    }) + "\\n");
  });
`;

test("quota bridge uses one persistent helper and serializes reservations", async (t) => {
  const admission = createCliActionAdmission({
    helperExecutable: process.execPath, helperArguments: ["-e", helper], maxConcurrency: 3,
  });
  t.after(() => admission.close());
  const [first, second] = await Promise.all([admission.admit(), admission.admit()]);
  assert.deepEqual(first, { capacity: 3, reason: "available", reservationId: "1" });
  assert.deepEqual(second, { capacity: 3, reason: "available", reservationId: "2" });
  assert.deepEqual(await admission.complete(first.reservationId), { capacity: 3, reason: "available" });
  assert.deepEqual(await admission.status(), { capacity: 3, reason: "available" });
});

test("queue admission adapter exposes exactly-once reservation lifecycle", async (t) => {
  const script = `
    const readline = require("node:readline");
    let reservations = 0;
    readline.createInterface({input:process.stdin}).on("line", line => {
      const request = JSON.parse(line);
      if (request.operation === "admit") reservations++;
      if (request.operation === "complete" || request.operation === "release") reservations--;
      console.log(JSON.stringify({
        id:request.id,capacity:4,reason:"available",
        ...(request.operation === "admit" ? {reservationId:String(reservations)} : {})
      }));
    });
  `;
  const admission = createCliActionAdmission({
    helperExecutable: process.execPath, helperArguments: ["-e", script],
  });
  t.after(() => admission.close());
  assert.equal((await admission.admission({ command: "gh aw update", running: 4 })).concurrency, 0);
  const first = await admission.admission({ id: 1, command: "gh aw update", running: 0 });
  assert.equal(first.concurrency, 4);
  assert.equal(first.reservationId, "1");
  await first.complete({ started: false });
  await first.release();
  await first.complete();
  const second = await admission.admission({ id: 2, command: "gh aw update", running: 0 });
  assert.equal(second.reservationId, "1");
  await second.complete({ rateLimited: true });
  await second.release();
  const third = await admission.admission({ id: 3, command: "gh aw update", running: 0 });
  assert.equal(third.reservationId, "1");
  await third.release();
});

test("queue output hook detects split secondary limits without retaining raw output", async (t) => {
  const script = `
    require("node:readline").createInterface({input:process.stdin}).on("line", line => {
      const request = JSON.parse(line);
      const valid = request.operation !== "complete"
        || (request.rateLimited === true && request.retryAfterSeconds === 120);
      console.log(JSON.stringify({
        id:request.id,capacity:valid ? 2 : 0,reason:valid ? "available" : "unknown",
        ...(request.operation === "admit" ? {reservationId:"reserved"} : {})
      }));
    });
  `;
  const admission = createCliActionAdmission({
    helperExecutable: process.execPath, helperArguments: ["-e", script],
  });
  t.after(() => admission.close());
  const decision = await admission.admission({ command: "gh aw update" });
  decision.onOutput({ stream: "stderr", data: "Sensitive output: secondary rate lim" });
  decision.onOutput({ stream: "stderr", data: "it\nRetry-Af" });
  decision.onOutput({ stream: "stderr", data: "ter: 120\n" });
  const reconciled = await decision.complete({ started: true, result: { ok: false } });
  assert.equal(reconciled.capacity, 2);
  assert.equal(reconciled.reason, "available");
  assert.ok(!JSON.stringify(decision).includes("Sensitive output"));
});

test("helper unavailable, invalid, timed out, or closed pauses without raw errors", async (t) => {
  for (const options of [
    { helperExecutable: "/does-not-exist-quota-helper" },
    { helperExecutable: process.execPath, helperArguments: ["-e", "console.log('secret raw error')"] },
    { helperExecutable: process.execPath, helperArguments: ["-e", "setInterval(()=>{},1000)"], requestTimeoutMs: 50 },
    { helperExecutable: process.execPath, helperArguments: ["-e", `
      process.stdin.once("data", () => console.log(JSON.stringify({id:1,capacity:999,reason:"available"})));
    `] },
  ]) {
    const admission = createCliActionAdmission(options);
    t.after(() => admission.close());
    const result = await admission.admit();
    assert.equal(result.capacity, 0);
    assert.equal(result.reason, "unknown");
    if (options.helperExecutable === "/does-not-exist-quota-helper") {
      assert.match(result.message, /Install cao-cli-quota on PATH or set CAO_CLI_QUOTA_EXECUTABLE/);
    }
    assert.ok(Number.isFinite(Date.parse(result.retryAt)));
    admission.close();
    assert.equal((await admission.admit()).capacity, 0);
  }
});

test("portable install without Go sources uses the prebuilt quota helper on PATH", async (t) => {
  const directory = await mkdtemp(join(process.cwd(), ".cao-cli-quota-test-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const dashboard = join(directory, "dashboard");
  await mkdir(join(dashboard, "site", "src"), { recursive: true });
  await Promise.all([
    "cli-action-quota.mjs", "cli-actions.mjs", "cli-terminal-process.mjs", "cli-terminal-child.mjs",
    "environment-defaults.mjs", "site/src/environment-defaults.js", "site/src/debug.js",
  ].map((name) =>
    copyFile(new URL(`../../dashboard/${name}`, import.meta.url), join(dashboard, name))));
  const executable = join(directory, "cao-cli-quota");
  await writeFile(executable, `#!${process.execPath}
    require("node:readline").createInterface({input:process.stdin}).on("line", line => {
      const request = JSON.parse(line);
      console.log(JSON.stringify({
        id:request.id,capacity:2,reason:"available",reservationId:"prebuilt"
      }));
    });
  `, { mode: 0o700 });
  const { createCliActionAdmission: createInstalledAdmission } =
    await import(pathToFileURL(join(dashboard, "cli-action-quota.mjs")).href);
  const admission = createInstalledAdmission({
    workingDirectory: directory, env: { ...process.env, PATH: directory },
  });
  t.after(() => admission.close());
  assert.deepEqual(await admission.admit({ command: "gh aw update" }), {
    capacity: 2, reason: "available", reservationId: "prebuilt",
  });
});

test("helper inherits gh environment and requested working directory", async (t) => {
  const script = `
    process.stdin.once("data", data => {
      const request = JSON.parse(data);
      const valid = process.env.GH_HOST === "example.test"
        && process.env.GH_PATH === "custom-gh"
        && process.env.CAO_CLI_QUOTA_WORKING_DIRECTORY === process.cwd();
      console.log(JSON.stringify({id:request.id,capacity: valid ? 2 : 0,reason:valid ? "available" : "unknown"}));
    });
  `;
  const admission = createCliActionAdmission({
    helperExecutable: process.execPath, helperArguments: ["-e", script],
    workingDirectory: process.cwd(), ghExecutable: "custom-gh",
    env: { ...process.env, GH_HOST: "example.test" },
  });
  t.after(() => admission.close());
  assert.equal((await admission.status()).capacity, 2);
});

test("quota helper starts lazily and close cancels an outstanding decision", async () => {
  const neverStarted = createCliActionAdmission({ helperExecutable: "/not-a-quota-helper" });
  neverStarted.close();
  assert.equal((await neverStarted.admit()).reason, "unknown");
  const admission = createCliActionAdmission({
    helperExecutable: process.execPath,
    helperArguments: ["-e", "setInterval(()=>{},1000)"],
  });
  const waiting = admission.admit();
  await new Promise((resolve) => setImmediate(resolve));
  admission.close();
  assert.equal((await waiting).capacity, 0);
});

test("rate limit indicator parsing does not expose raw command output", () => {
  assert.deepEqual(detectCliRateLimit("GitHub: secondary rate limit\nRetry-After: 123\nsecret"), {
    rateLimited: true, retryAfterSeconds: 123,
  });
  assert.deepEqual(detectCliRateLimit("HTTP 429: Too Many Requests"), {
    rateLimited: true, retryAfterSeconds: 0,
  });
  assert.deepEqual(detectCliRateLimit("HTTP 403: Resource not accessible"), {
    rateLimited: false, retryAfterSeconds: 0,
  });
});

test("quota configuration rejects invalid thresholds", () => {
  for (const options of [{ maxConcurrency: 0 }, { estimatedCost: -1 }, { minimumRemaining: 1.5 }]) {
    assert.throws(() => createCliActionAdmission(options), /positive integer/);
  }
});

test("candidate command bypasses quota only for known offline operations", async (t) => {
  const admission = createCliActionAdmission({ helperExecutable: "/not-a-quota-helper" });
  t.after(() => admission.close());
  for (const command of ["gh aw compile", "gh aw compile --strict --no-check-update", "gh aw --help"]) {
    assert.equal(cliActionRequiresGitHubQuota({ command }), false);
    const decision = await admission.admit({ command, actionId: "irrelevant" });
    assert.equal(decision.capacity, 4);
    assert.match(decision.reservationId, /^local:/);
    assert.equal((await admission.complete(decision.reservationId)).capacity, 4);
    assert.deepEqual(await admission.admission({ command }), { concurrency: 4, reason: "available" });
  }
  for (const command of [
    "gh aw update", "gh cao update", "gh workflow run activity",
    "gh agent-task create --from-file -", "gh aw compile --repo owner/repository",
    "gh aw compile -Rowner/repository", "gh aw compile --check-update",
  ]) {
    assert.equal(cliActionRequiresGitHubQuota({ command, actionId: "compile" }), true);
  }
  assert.equal(cliActionRequiresGitHubQuota({ actionId: "compile" }), true);
  assert.equal((await admission.admit({ command: "gh aw update" })).capacity, 0);
  // A broken GitHub helper must not pause unrelated offline work.
  assert.equal((await admission.admit({ command: "gh aw compile" })).capacity, 4);
});

test("only exact workflow dispatch commands can narrow required quota resources", async (t) => {
  const script = `
    require("node:readline").createInterface({input:process.stdin}).on("line", line => {
      const request = JSON.parse(line);
      console.log(JSON.stringify({
        id:request.id,capacity:4,reason:"available",reservationId:request.resources.join(",")
      }));
    });
  `;
  const admission = createCliActionAdmission({
    helperExecutable: process.execPath, helperArguments: ["-e", script],
  });
  t.after(() => admission.close());
  assert.equal((await admission.admit({ command: "gh workflow run activity" })).reservationId, "core");
  for (const command of ["gh aw update", "gh cao update", "gh agent-task create --from-file -"]) {
    assert.equal((await admission.admit({ command, resources: ["core"] })).reservationId, "core,graphql");
  }
});

test("default Go bridge delegates only credential lookup to the selected gh executable", async (t) => {
  const directory = await mkdtemp(join(process.cwd(), ".cao-cli-quota-test-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const executable = join(directory, "gh-fixture");
  const callsPath = join(directory, "calls.json");
  await writeFile(join(directory, "hosts.yml"), "");
  await writeFile(executable, `#!${process.execPath}
    const fs = require("node:fs");
    const args = process.argv.slice(2);
    if (process.cwd() !== ${JSON.stringify(directory)}
      || process.env.GH_HOST !== "quota.example.test"
      || process.env.GH_CONFIG_DIR !== ${JSON.stringify(directory)}) process.exit(1);
    fs.writeFileSync(${JSON.stringify(callsPath)}, JSON.stringify(args));
    process.exit(1);
  `, { mode: 0o700 });
  const admission = createCliActionAdmission({
    workingDirectory: directory,
    ghExecutable: executable,
    env: {
      ...process.env, GH_HOST: "quota.example.test", GH_CONFIG_DIR: directory,
      GH_TOKEN: "", GITHUB_TOKEN: "", GH_ENTERPRISE_TOKEN: "", GITHUB_ENTERPRISE_TOKEN: "",
    },
  });
  t.after(() => admission.close());
  const decision = await admission.admit();
  assert.equal(decision.capacity, 0);
  assert.equal(decision.reason, "unknown");
  assert.deepEqual(JSON.parse(await readFile(callsPath, "utf8")),
    ["auth", "token", "--secure-storage", "--hostname", "quota.example.test"]);
});
