import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { test } from "node:test";

const pluginEntry = import.meta.resolve("starlight-llms-txt");
const pluginRequire = createRequire(pluginEntry);
const matcherPath = pluginRequire.resolve("micromatch");
const matcher = pluginRequire("micromatch");

test("the docs plugin's matcher replacement implements its entire micromatch API usage", async () => {
  const source = await readFile(new URL("./generator.ts", pluginEntry), "utf8");
  const implementation = source.replace(/^import micromatch from ['"]micromatch['"];$/m, "");
  const uses = [...implementation.matchAll(/\bmicromatch\b/g)];
  assert.equal(uses.length, 4, "review matcher compatibility when the plugin changes");
  assert.equal([...implementation.matchAll(/\bmicromatch\.isMatch\(/g)].length, uses.length);
  const metadata = pluginRequire("micromatch/package.json");
  assert.equal(metadata.name, "picomatch");
  assert.equal(metadata.dependencies, undefined);

  for (const [id, patterns, expected] of [
    ["index", "index", true],
    ["setup-quickstarts", ["index", "setup-quickstarts"], true],
    ["architecture", ["index", "setup-quickstarts"], false],
    ["guides/2026/nested", "guides/**", true],
    ["operational-value/reports/weekly", "operational-value/reports/**", true],
    ["dashboard-overview-components", "dashboard-overview-*", true],
    ["dashboard", "dashboard*", true],
    ["setup-one-target", "setup-one-*", true],
    ["architecture-at-a-glance", "architecture", false],
    ["guide", "{guide,reference}", true],
    ["reference", "{guide,reference}", true],
    ["guide", "!guides/**", true],
    ["guides/post", "!guides/**", false],
  ]) {
    assert.equal(matcher.isMatch(id, patterns), expected, `${id}: ${JSON.stringify(patterns)}`);
  }
});

test("deep brace patterns below the vulnerable parser's character limit do not exhaust the stack", () => {
  const script = `
    const assert = require("node:assert/strict");
    const matcher = require(${JSON.stringify(matcherPath)});
    for (const depth of [2000, 4000, 4990]) {
      const pattern = "{".repeat(depth) + "a,b" + "}".repeat(depth);
      assert.ok(pattern.length < 10000);
      for (const patterns of [pattern, [pattern], ["guides/**", pattern]]) {
        assert.equal(typeof matcher.isMatch("a", patterns), "boolean");
      }
    }
    assert.equal(matcher.braces, undefined);
    assert.equal(matcher.braceExpand, undefined);
  `;
  execFileSync(process.execPath, ["--stack-size=512", "-e", script], {
    timeout: 10_000,
    stdio: "pipe",
  });
});

test("the root dependency graph no longer installs the vulnerable braces walkers", async () => {
  const lock = JSON.parse(await readFile(new URL("../../package-lock.json", import.meta.url), "utf8"));
  assert.ok(Object.keys(lock.packages).every((name) => !/(?:^|\/)node_modules\/braces$/.test(name)));
  assert.throws(() => pluginRequire.resolve("braces"), { code: "MODULE_NOT_FOUND" });
});
