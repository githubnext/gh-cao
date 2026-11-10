import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import { promisify } from "node:util";

const execute = promisify(execFile);
const helper = resolve("scripts/azure-local/platform.py");
const platform = (...args) => execute("python3", [helper, ...args], { timeout: 10_000 });

for (const [system, architecture, asset] of [
  ["Linux", "x86_64", "linux-x64"],
  ["Linux", "aarch64", "linux-arm64"],
  ["Darwin", "x86_64", "osx-x64"],
  ["Darwin", "arm64", "osx-arm64"],
]) {
  test(`Azure Core Tools selects pinned ${asset} and rejects corrupt downloads`, async () => {
    const dir = await mkdtemp(join(tmpdir(), "azure-platform-"));
    try {
      const script = join(dir, "scripts/azure-local/azure-local.sh");
      const tools = join(dir, "bin");
      const trace = join(dir, "download");
      await mkdir(dirname(script), { recursive: true });
      await mkdir(tools);
      await copyFile(resolve("scripts/azure-local/azure-local.sh"), script);
      await copyFile(resolve("scripts/container-engine.sh"), join(dir, "scripts/container-engine.sh"));
      await copyFile(helper, join(dirname(script), "platform.py"));
      await writeFile(join(tools, "uname"),
        `#!/bin/bash\nif [[ "$1" == -s ]]; then echo ${system}; else echo ${architecture}; fi\n`,
        { mode: 0o700 });
      await writeFile(join(tools, "curl"),
        '#!/bin/bash\nprintf "%s\\n" "$2" > "$DOWNLOAD_TRACE"\nprintf corrupt > "$4"\n',
        { mode: 0o700 });
      await assert.rejects(execute("/bin/bash", [script, "install-core-tools"], {
        env: { ...process.env, PATH: `${tools}:${process.env.PATH}`, DOWNLOAD_TRACE: trace },
      }), /checksum verification failed/);
      assert.equal((await readFile(trace, "utf8")).trim(),
        `https://github.com/Azure/azure-functions-core-tools/releases/download/4.15.1/Azure.Functions.Cli.${asset}.4.15.1.zip`);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });
}

test("Azure platform verifies streamed checksums and serializes installers", async () => {
  const dir = await mkdtemp(join(tmpdir(), "azure-lock-"));
  try {
    const archive = join(dir, "archive");
    const content = Buffer.alloc((1 << 20) + 1, 123);
    await writeFile(archive, content);
    await platform("verify", archive, createHash("sha256").update(content).digest("hex"));
    await assert.rejects(platform("verify", archive, "0".repeat(64)));
    const trace = join(dir, "trace");
    await Promise.all(["a", "b", "c"].map((id) =>
      platform("lock", join(dir, "lock"), process.execPath, "-e", `
        const {appendFileSync} = require("node:fs");
        appendFileSync(process.argv[1], process.argv[2] + "\\n");
        setTimeout(() => appendFileSync(process.argv[1], process.argv[2] + "\\n"), 30);
      `, trace, id)));
    const lines = (await readFile(trace, "utf8")).trim().split("\n");
    assert.equal(lines.length, 6);
    for (let index = 0; index < lines.length; index += 2) {
      assert.equal(lines[index], lines[index + 1], "the installer lock spans the whole subprocess");
    }
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("Azure host tracks native executable and start identity, including orphaned handlers", async () => {
  const dir = await mkdtemp(join(tmpdir(), "azure-process-"));
  let host;
  let handler;
  const handlerFile = join(dir, "handler");
  try {
    const { stdout } = await platform("launch", dir, join(dir, "host.log"), process.execPath, "-e", `
      const {spawn} = require("node:child_process");
      const {writeFileSync} = require("node:fs");
      const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], {stdio: "ignore"});
      writeFileSync(process.argv[1], String(child.pid));
      setInterval(() => {}, 1000);
    `, handlerFile);
    host = stdout.trim();
    const start = (await platform("identity", host)).stdout.trim();
    assert.ok(start);
    await platform("matches", host, start, process.execPath);
    await assert.rejects(platform("matches", host, `${start}-stale`, process.execPath));
    await assert.rejects(platform("matches", host, start, "/bin/bash"));
    await platform("group", host, process.execPath);
    await assert.rejects(platform("group", host, "/bin/bash"));
    for (let attempt = 0; attempt < 100; attempt++) {
      try { handler = (await readFile(handlerFile, "utf8")).trim(); break; }
      catch (error) {
        if (error.code !== "ENOENT") throw error;
        await new Promise((done) => setTimeout(done, 20));
      }
    }
    assert.ok(handler);
    process.kill(Number(host), "SIGTERM");
    for (let attempt = 0; attempt < 100; attempt++) {
      if (!(await platform("identity", host).catch(() => undefined))) break;
      await new Promise((done) => setTimeout(done, 20));
    }
    await assert.rejects(platform("matches", host, start, process.execPath));
    await platform("group", host, process.execPath);
    await assert.rejects(platform("group", "2147483647", process.execPath));
  } finally {
    for (const pid of [handler, host]) {
      if (!pid) continue;
      try { process.kill(Number(pid), "SIGKILL"); }
      catch (error) { if (error.code !== "ESRCH") throw error; }
    }
    await rm(dir, { recursive: true, force: true });
  }
});
