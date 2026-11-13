import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { createHash, generateKeyPairSync } from "node:crypto";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { parse } from "yaml";
import { nativeInputs, readImages, renderManifest, verifyAppScope } from "../../server/kubernetes/deployment.mjs";
import { reviewedBareInventory } from "../../server/kubernetes/review-inventory.mjs";

const script = fileURLToPath(new URL("../../server/kubernetes/local.sh", import.meta.url));
const root = fileURLToPath(new URL("../../", import.meta.url));
const assets = join(root, "server/kubernetes");
const hash = (content) => createHash("sha256").update(content).digest("hex");
const key = generateKeyPairSync("rsa", { modulusLength: 2048 }).privateKey.export({ type: "pkcs8", format: "pem" });

function reviewedInput() {
  const policy = JSON.parse(readFileSync(join(assets, "policy.json"), "utf8"));
  policy["control-plane"].campaigns = {};
  policy["control-plane"].scope = { "allowed-owners": ["octo"], "allowed-repositories": ["octo/ops", "octo/target"] };
  policy["control-plane"].activity = { repositories: ["octo/ops", "octo/target"] };
  const raw = JSON.stringify(policy);
  const source = reviewedBareInventory(raw, "octo/ops", "a".repeat(40));
  const configuration = [
    "CAO_COLLECT_APP_ID=123",
    "CAO_COLLECT_CONTROL_REPOSITORY=octo/ops",
    `CAO_COLLECT_NATIVE_INVENTORY_DIGEST=${hash(source)}`,
    "CAO_COLLECT_INVENTORY_LIMIT=2",
    "",
  ].join("\n");
  return { raw, source, configuration };
}

test("bare inventory is source-bound to policy validated by the production resolver", () => {
  const policy = JSON.parse(readFileSync(new URL("../../server/kubernetes/policy.json", import.meta.url), "utf8"));
  policy["control-plane"].campaigns = {};
  const raw = JSON.stringify(policy);
  const content = reviewedBareInventory(raw, "octo/ops", "a".repeat(40));
  const inventory = JSON.parse(content);
  assert.equal(inventory.sourceSHA, "a".repeat(40));
  assert.equal(inventory.sources["configuration-policy"].rows[0].raw, raw);
  assert.deepEqual(inventory.sources.campaigns.rows, []);
  policy["control-plane"].campaigns = { security: {} };
  assert.throws(() => reviewedBareInventory(JSON.stringify(policy), "octo/ops", "a".repeat(40)));
  assert.throws(() => reviewedBareInventory(raw, "octo/ops", "main"), /immutable commit/);
  assert.throws(() => reviewedBareInventory('{"version":1}', "octo/ops", "a".repeat(40)));
});

function fixture(t, options = {}) {
  mkdirSync(join(root, ".tmp"), { recursive: true });
  const directory = mkdtempSync(join(root, ".tmp/kubernetes-test-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const localAssets = join(directory, "server/kubernetes");
  const state = join(directory, ".tmp/cao-kubernetes-local");
  const bin = join(directory, "bin");
  for (const path of [localAssets, state, bin]) mkdirSync(path, { recursive: true });
  for (const file of ["local.sh", "deployment.mjs", "web.yaml", "collector.yaml", "inventory.yaml", "postgres.yaml", "bootstrap.yaml", "maintenance.yaml"]) {
    copyFileSync(join(assets, file), join(localAssets, file));
  }
  symlinkSync(join(assets, "review-inventory.mjs"), join(localAssets, "review-inventory.mjs"));
  const input = reviewedInput();
  const policy = join(localAssets, "policy.json");
  writeFileSync(policy, input.raw);
  const images = {
    RUNTIME_IMAGE: `cao-dashboard:sha256-${"a".repeat(64)}`,
    MAINTENANCE_IMAGE: `cao-db-maintenance:sha256-${"b".repeat(64)}`,
    POLICY_SHA256: hash(input.raw),
  };
  writeFileSync(join(state, "images.env"), Object.entries(images).map(([name, value]) => `${name}=${value}`).join("\n"));
  for (const file of ["bootstrap.env", "database.env", "postgres.env", "owner.env", "runtime.env", "web-keys.env",
    "web.crt", "web.key", "postgres.crt", "postgres.key", "ca.crt"]) writeFileSync(join(state, file), "test-only");
  writeFileSync(join(directory, "inventory.json"), input.source);
  writeFileSync(join(directory, "collector.env"), input.configuration);
  writeFileSync(join(directory, "app.pem"), key, { mode: 0o600 });
  const log = join(directory, "calls");
  writeFileSync(log, "");
  const cluster = join(directory, "cluster.json");
  writeFileSync(cluster, "{}");
  writeFileSync(join(bin, "kubectl"), `#!${process.execPath}
const fs = require("node:fs");
const args = process.argv.slice(2);
const entry = { args };
const state = JSON.parse(fs.readFileSync(process.env.CLUSTER, "utf8"));
const get = args.includes("get");
const file = args[args.indexOf("-f") + 1];
if ((args.includes("apply") || args.includes("create")) && args.includes("-f")) {
  entry.document = JSON.parse(fs.readFileSync(file === "-" ? 0 : file, "utf8"));
  for (const document of entry.document.items ?? [entry.document]) {
    if (document.kind === "Deployment") {
      state[document.metadata.name] = document;
      fs.writeFileSync(process.env.CLUSTER, JSON.stringify(state));
    }
  }
}
fs.appendFileSync(process.env.CALL_LOG, JSON.stringify(entry) + "\\n");
if (get && args.includes("namespace")) {
  const name = args[args.indexOf("namespace") + 1];
  if (name !== process.env.MISSING_NAMESPACE) process.stdout.write(args.some(a => a.includes("metadata.labels"))
    ? name + "|" + (name === process.env.FOREIGN_NAMESPACE ? "another-application" : "cao-local") : "namespace/" + name);
} else if (get && args.includes("deployment")) {
  const name = args[args.indexOf("deployment") + 1];
  const document = state[name];
  const query = args.join(" ");
  if (query.includes("cao-local-profile")) process.stdout.write(document?.metadata.annotations["cao-local-profile"] ?? "");
  else if (query.includes("cao-local-native-config")) process.stdout.write(document?.metadata.annotations["cao-local-native-config"] ?? "");
  else if (query.includes("cao-local-policy-sha256")) process.stdout.write(document?.spec.template.metadata.annotations["cao-local-policy-sha256"] ?? "");
  else if (query.includes("CAO_COLLECT_NATIVE")) process.stdout.write(document?.spec.template.spec.containers[0].env.find(e => e.name === "CAO_COLLECT_NATIVE")?.value ?? "");
  else if (query.includes("spec.replicas")) process.stdout.write(String(document?.spec.replicas ?? 0));
  else process.stdout.write("deployment/" + name);
} else if (get && args.includes("jobs")) process.stdout.write(process.env.SNAPSHOT_JOB ?? "");
else if (get && args.includes("configmap")) process.stdout.write(process.env.INVENTORY_DIGEST);
else if (get && args.some(a => a.startsWith("job.batch/"))) process.stdout.write(process.env.FAIL_PIN ? "Failed" : "Complete");
else if (args.includes("--dry-run=client")) process.stdout.write('{"apiVersion":"v1","kind":"ConfigMap"}');
else if (args.includes("create") && args.includes("-f")) process.stdout.write("job.batch/cao-test");
`, { mode: 0o700 });
  writeFileSync(join(bin, "docker"), `#!${process.execPath}
const image = process.argv[4];
process.stdout.write("sha256:" + image.split(":sha256-")[1] + " " + process.env.POLICY_DIGEST);
`, { mode: 0o700 });
  writeFileSync(join(bin, "node"), `#!${process.execPath}
const { spawnSync } = require("node:child_process");
if (process.argv[3] === "verify-native") {
  if (process.env.FAIL_SCOPE) process.exit(1);
  console.log("cao-native-" + "e".repeat(24));
} else {
  const result = spawnSync(process.execPath, process.argv.slice(2), { stdio: "inherit" });
  process.exit(result.status ?? 1);
}
`, { mode: 0o700 });
  const oauth = join(directory, "oauth.env");
  writeFileSync(oauth, [
    "CAO_GITHUB_CLIENT_ID=test-only-id",
    "CAO_GITHUB_CLIENT_SECRET=test-only-secret",
    "CAO_GITHUB_ALLOWED_USERS=test-user",
    "CAO_GITHUB_ADMIN_USERS=test-user",
    "",
  ].join("\n"), { mode: 0o600 });
  return {
    oauth,
    log,
    state,
    images,
    policy,
    directory,
    calls: () => readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line))
      .flatMap((call) => call.document?.kind === "List" ? call.document.items.map((document) => ({ ...call, document })) : [call]),
    run: (command = "serve", env = {}) => spawnSync("bash", [join(localAssets, "local.sh"), command,
      ...(command === "native" ? [join(directory, "inventory.json"), join(directory, "collector.env"), join(directory, "app.pem")] : [oauth])], {
      encoding: "utf8",
      env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, CALL_LOG: log, CLUSTER: cluster,
        CAO_LOCAL_POLICY: policy, POLICY_DIGEST: images.POLICY_SHA256, INVENTORY_DIGEST: hash(input.source),
        FOREIGN_NAMESPACE: options.foreign ?? "cao-local", MISSING_NAMESPACE: options.missing ?? "", ...env },
    }),
  };
}

test("local deployment refuses foreign namespaces without making cluster writes", (t) => {
  const f = fixture(t);
  const result = f.run();
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Refusing foreign namespace: cao-local/);
  const calls = f.calls().map(({ args }) => args.join(" "));
  assert.equal(calls.length, 2);
  for (const call of calls) {
    assert.match(call, /^--context docker-desktop get namespace cao-local/);
    assert.doesNotMatch(call, /create|apply|delete|patch/);
  }
});

test("database preflight checks the second namespace before creating a missing first namespace", (t) => {
  const f = fixture(t, { missing: "cao-local-system" });
  const result = f.run("database");
  assert.notEqual(result.status, 0, result.stderr);
  assert.match(result.stderr, /Refusing foreign namespace/);
  assert.equal(f.calls().length, 2);
  assert.ok(f.calls().every(({ args }) => args.includes("get")));
});

test("native scope rejection makes no cluster writes", (t) => {
  const f = fixture(t, { foreign: "" });
  const result = f.run("native", { FAIL_SCOPE: "1" });
  assert.notEqual(result.status, 0);
  assert.ok(f.calls().every(({ args }) => args.includes("get")));
});

test("pending snapshot Job blocks native activation before its active counter appears", (t) => {
  const f = fixture(t, { foreign: "" });
  const result = f.run("native", { SNAPSHOT_JOB: "cao-ingest-pending:" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /nonterminal snapshot/);
  assert.ok(f.calls().every(({ args }) => args.includes("get")));
});

test("failed inventory pin cannot select native mode, mount its key or start its collector", (t) => {
  const f = fixture(t, { foreign: "" });
  const result = f.run("native", { FAIL_PIN: "1" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /failed; inspect the retained Job/);
  assert.ok(!f.calls().some(({ args }) => args.includes("secret")));
  assert.ok(!f.calls().some(({ document }) => document?.kind === "Deployment"));
  const served = f.run();
  assert.equal(served.status, 0, served.stderr);
  const web = f.calls().findLast(({ document }) => document?.kind === "Deployment" && document.metadata.name === "cao-web").document;
  assert.equal(web.metadata.annotations["cao-local-profile"], "snapshot");
});

test("native activation and subsequent serve apply a complete identical native profile", (t) => {
  const f = fixture(t, { foreign: "" });
  const activated = f.run("native");
  assert.equal(activated.status, 0, activated.stderr);
  const served = f.run();
  assert.equal(served.status, 0, served.stderr);
  const calls = f.calls();
  const web = calls.filter(({ document }) => document?.kind === "Deployment" && document.metadata.name === "cao-web").map(({ document }) => document);
  assert.equal(web.length, 2);
  assert.deepEqual(web[0], web[1]);
  assert.equal(web[0].metadata.annotations["cao-local-profile"], "native");
  const container = web[0].spec.template.spec.containers[0];
  assert.equal(container.image, f.images.RUNTIME_IMAGE);
  assert.deepEqual(Object.fromEntries(container.env.filter(({ name }) => name.startsWith("CAO_COLLECT") || name === "CAO_EXTERNAL_INGESTION").map(({ name, value }) => [name, value])), {
    CAO_EXTERNAL_INGESTION: "false", CAO_COLLECT_NATIVE: "true", CAO_COLLECT_ADMIT_ONLY: "true",
  });
  assert.ok(!calls.some(({ args }) => args.includes("set")));
  const pin = calls.findIndex(({ document }) => document?.metadata?.generateName === "cao-inventory-");
  const keyMount = calls.findIndex(({ args }) => args.includes("secret"));
  const activation = calls.findIndex(({ document }) => document?.metadata?.name === "cao-web");
  assert.ok(pin < keyMount && keyMount < activation);
  writeFileSync(f.policy, readFileSync(f.policy, "utf8") + " ");
  const changedPolicy = f.run();
  assert.notEqual(changedPolicy.status, 0);
  assert.match(changedPolicy.stderr, /policy digest mismatch/);
});

test("image lock rejects a different policy and rendering changes the actual workload image", () => {
  const policy = reviewedInput().raw;
  const lock = `RUNTIME_IMAGE=cao-dashboard:sha256-${"a".repeat(64)}\nMAINTENANCE_IMAGE=cao-db-maintenance:sha256-${"b".repeat(64)}\nPOLICY_SHA256=${hash(policy)}\n`;
  assert.throws(() => readImages(lock, policy + " "), /policy digest mismatch/);
  const images = readImages(lock, policy);
  const content = readFileSync(join(assets, "web.yaml"), "utf8");
  const first = renderManifest(content, images, "snapshot").items.find(({ kind }) => kind === "Deployment");
  const second = renderManifest(content, { ...images, RUNTIME_IMAGE: `cao-dashboard:sha256-${"c".repeat(64)}` }, "snapshot").items.find(({ kind }) => kind === "Deployment");
  assert.notEqual(first.spec.template.spec.containers[0].image, second.spec.template.spec.containers[0].image);
  assert.equal(first.spec.template.metadata.annotations["cao-local-policy-sha256"], hash(policy));
});

test("rendering preserves Kubernetes octal secret modes and does not tie PostgreSQL restarts to application policy", () => {
  const content = readFileSync(join(assets, "postgres.yaml"), "utf8");
  const first = renderManifest(content, { POLICY_SHA256: "a".repeat(64) }, "snapshot")
    .items.find(({ kind }) => kind === "StatefulSet");
  const second = renderManifest(content, { POLICY_SHA256: "b".repeat(64) }, "snapshot")
    .items.find(({ kind }) => kind === "StatefulSet");
  assert.equal(first.spec.template.spec.volumes.find(({ name }) => name === "tls").secret.defaultMode, 0o440);
  assert.deepEqual(first, second);
  const collector = renderManifest(readFileSync(join(assets, "collector.yaml"), "utf8"),
    { RUNTIME_IMAGE: "runtime", POLICY_SHA256: "a".repeat(64) }, "native", `cao-native-${"e".repeat(24)}`);
  assert.equal(collector.spec.template.spec.volumes.find(({ name }) => name === "app-key").secret.defaultMode, 0o440);
});

test("collector restart clears a previous backfill marker and does not start collection after failed backfill", (t) => {
  const directory = mkdtempSync(join(root, ".tmp/kubernetes-marker-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const marker = join(directory, "ready");
  const log = join(directory, "calls");
  const executable = join(directory, "collector");
  writeFileSync(executable, '#!/bin/sh\nprintf "%s\\n" "$1" >> "$TEST_LOG"\nif test "$1" = backfill; then exit 1; fi\n', { mode: 0o700 });
  writeFileSync(marker, "");
  const manifest = parse(readFileSync(join(assets, "collector.yaml"), "utf8"));
  const command = manifest.spec.template.spec.containers[0].args[0]
    .replaceAll("/tmp/backfill-ready", marker).replaceAll("/app/cao-dashboard", executable);
  const result = spawnSync("/bin/sh", ["-ec", command], { env: { ...process.env, TEST_LOG: log } });
  assert.notEqual(result.status, 0);
  const probe = manifest.spec.template.spec.containers[0].readinessProbe.exec.command[2].replaceAll("/tmp/backfill-ready", marker);
  assert.notEqual(spawnSync("/bin/sh", ["-ec", probe]).status, 0);
  assert.equal(readFileSync(log, "utf8"), "backfill\n");
});

test("native preflight rejects source/policy and repository-limit mismatches", () => {
  const input = reviewedInput();
  assert.equal(nativeInputs(input.source, input.configuration, input.raw, key).repositories.length, 2);
  assert.throws(() => nativeInputs(input.source, input.configuration, input.raw + " ", key), /does not match/);
  assert.throws(() => nativeInputs(input.source, input.configuration.replace("LIMIT=2", "LIMIT=3"), input.raw, key), /matching explicit/);
});

test("mixed-case GitHub identity is canonicalized without rewriting reviewed policy bytes", () => {
  const raw = reviewedInput().raw.replaceAll("octo/ops", "Octo/Ops").replaceAll("octo/target", "Octo/Target");
  const source = reviewedBareInventory(raw, "Octo/Ops", "a".repeat(40));
  const configuration = reviewedInput().configuration.replace(hash(reviewedInput().source), hash(source));
  const inventory = JSON.parse(source);
  assert.equal(inventory.sourceRepository, "octo/ops");
  assert.equal(inventory.controlRepository, "octo/ops");
  assert.equal(inventory.sources["configuration-policy"].rows[0].raw, raw);
  assert.deepEqual(nativeInputs(source, configuration, raw, key).repositories, ["octo/ops", "octo/target"]);
  assert.throws(() => nativeInputs(source, configuration.replace("octo/ops", "Octo/Ops"), raw, key), /canonical lowercase/);
});

test("App preflight follows empty pages, ignores aggregate count and revokes verification tokens", async () => {
  const calls = [];
  const response = (body, next) => new Response(JSON.stringify(body), { headers: next ? { link: `<https://api.github.com${next}>; rel="next"` } : {} });
  const request = async (url, options) => {
    calls.push({ url, options });
    const path = new URL(url).pathname;
    if (path === "/app/installations") return response([{ id: 123 }]);
    if (path.endsWith("/access_tokens")) return response({ token: "test-only-verification-token" });
    if (options.method === "DELETE") return new Response(null, { status: 204 });
    return url.includes("page=2")
      ? response({ total_count: 6, repositories: [{ full_name: "Octo/Ops" }, { full_name: "Octo/Target" }] })
      : response({ total_count: 6, repositories: [] }, "/installation/repositories?per_page=100&page=2");
  };
  await verifyAppScope("123", key, ["octo/ops", "octo/target"], request);
  assert.equal(calls.length, 5);
  assert.deepEqual(JSON.parse(calls[1].options.body), { permissions: { metadata: "read" } });
  assert.equal(calls.at(-1).options.method, "DELETE");
  await assert.rejects(verifyAppScope("123", key, ["octo/ops"], request), /unreviewed repository/);
  assert.equal(calls.at(-1).options.method, "DELETE");
});

test("App preflight rejects foreign pagination links and fails closed on token cleanup errors", async () => {
  let revoked = false;
  const request = async (url, options) => {
    assert.equal(new URL(url).origin, "https://api.github.com");
    if (new URL(url).pathname === "/app/installations") return Response.json([{ id: 123 }]);
    if (url.endsWith("/access_tokens")) return Response.json({ token: "test-only-verification-token" });
    if (options.method === "DELETE") {
      revoked = true;
      return new Response(null, { status: 204 });
    }
    return new Response('{"repositories":[]}', { headers: { link: '<https://example.invalid/installation/repositories>; rel="next"' } });
  };
  await assert.rejects(verifyAppScope("123", key, ["octo/ops"], request), /pagination destination/);
  assert.equal(revoked, true);
  await assert.rejects(verifyAppScope("123", key, ["octo/ops"], async (url, options) => {
    if (options.method === "DELETE") return new Response(null, { status: 403 });
    if (new URL(url).pathname === "/installation/repositories") return Response.json({ repositories: [{ full_name: "octo/ops" }] });
    return request(url, options);
  }), /HTTP 403/);
});

test("OAuth input cannot inject local identity, database authority or shell code", (t) => {
  const f = fixture(t);
  const original = readFileSync(f.oauth, "utf8");
  for (const extra of [
    "CAO_GITHUB_LOCAL_USER=test-user",
    "CAO_DATABASE_ADMIN_URL=postgresql://test-only",
    "CAO_GITHUB_CLIENT_SECRET=duplicate",
    "CAO_GITHUB_CLIENT_ID=REPLACE_WITH_YOUR_OAUTH_CLIENT_ID",
    "UNSUPPORTED=$(touch should-not-exist)",
  ]) {
    writeFileSync(f.oauth, `${original}${extra}\n`);
    const result = f.run();
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /OAuth file must contain exactly/);
    assert.equal(readFileSync(f.log, "utf8"), "");
  }
});

test("operator shell passes syntax and ShellCheck when available", () => {
  execFileSync("bash", ["-n", script]);
  const probe = spawnSync("shellcheck", ["--version"]);
  if (!probe.error) execFileSync("shellcheck", [script]);
});
