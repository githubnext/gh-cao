import assert from "node:assert/strict";
import { existsSync, lstatSync, readdirSync, readFileSync, realpathSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

const root = new URL("../../", import.meta.url).pathname;
const skillPath = join(root, "skills", "central-agentic-ops", "SKILL.md");
const skill = readFileSync(skillPath, "utf8");
const read = (path) => readFileSync(join(root, path), "utf8");

const taskGuides = {
  setup: ".github/cao/instructions/setup.md",
  operations: ".github/cao/instructions/operations.md",
  "gh cao": ".github/cao/instructions/gh-cao.md",
  "gh aw": ".github/cao/instructions/gh-aw.md",
  gh: ".github/cao/instructions/gh.md",
  "dashboard analysis": ".github/cao/instructions/dashboard-analysis.md",
  diagnostics: ".github/cao/instructions/diagnostics.md",
};

test("central-agentic-ops is a discoverable, packaged, compact skill", () => {
  assert.match(skill, /^---\nname: central-agentic-ops\ndescription: "[^"\n]+"\n/);
  assert.ok(Buffer.byteLength(skill) <= 6 * 1024, "router must stay lightweight");

  const link = join(root, ".github", "skills", "central-agentic-ops");
  assert.equal(lstatSync(link).isSymbolicLink(), true);
  assert.equal(realpathSync(join(link, "SKILL.md")), realpathSync(skillPath));

  const manifest = read("aw.yml");
  assert.match(manifest, /^\s+- skills\/central-agentic-ops$/m);
  assert.match(read("docs/agent-plugin.md"), /central-agentic-ops/);
});

test("central-agentic-ops resolves guides outside a checkout for gh skill installs", () => {
  const frontmatter = skill.slice(0, skill.indexOf("\n---", 4));
  assert.match(frontmatter, /^license: MIT$/m);
  assert.match(frontmatter, /^compatibility: "[^"\n]+"$/m);
  assert.ok(skill.includes("https://raw.githubusercontent.com/githubnext/gh-cao/REF/PATH"));
  assert.match(skill, /metadata\.github-ref/);
  assert.doesNotMatch(skill, /Former skill|setup-cao|debug-cao/);
});

test("task guides do not collide with control-repository steering", () => {
  const steering = new Set();
  for (const name of readdirSync(join(root, ".github", "workflows")).filter((file) => file.endsWith(".md"))) {
    for (const [, operation] of read(`.github/workflows/${name}`).matchAll(/runtime-import\? \.github\/cao\/([^/\s}]+)\.md/g)) {
      steering.add(operation);
    }
  }
  assert.ok(steering.size > 0);
  for (const guide of Object.values(taskGuides)) {
    assert.match(guide, /^\.github\/cao\/instructions\//);
  }
  assert.equal(steering.has("instructions"), false);
});

test("central-agentic-ops loads shared instructions first, then one task guide", () => {
  const shared = skill.indexOf("`.github/cao/instructions.md`");
  const table = skill.indexOf("| Task | Guide |");
  assert.ok(shared > 0 && shared < table, "shared instructions precede task routing");
  assert.match(skill, /read only its guide/);

  for (const [task, guide] of Object.entries(taskGuides)) {
    assert.ok(skill.includes(`\`${guide}\``), `${task} route must be listed`);
    assert.ok(existsSync(join(root, guide)), `${task} guide must exist`);
  }
  for (const [, guide] of skill.matchAll(/`(\.github\/cao\/[^`\s]+\.md)`/g)) {
    assert.ok(existsSync(join(root, guide)), `routed guide must exist: ${guide}`);
  }
});

test("task guides travel with the control-repository runtime", () => {
  for (const file of [
    "scripts/build-cao-runtime.mjs",
    "runtime/package.json",
    "internal/cli/native_management_runtime.go",
    "internal/cli/setup_runtime.go",
  ]) {
    assert.match(read(file), /"\.github\/cao\/instructions"/, `${file} must ship task guides`);
  }
});

test("dashboard analysis teaches query evidence over the UI", () => {
  const guide = read(taskGuides["dashboard analysis"]);
  assert.match(guide, /## Query the evidence, not the UI/);
  assert.match(guide, /gh cao query-info/);
  assert.match(guide, /cao_dashboard_execute_query/);
  assert.match(skill, /named dashboard queries[\s\S]*not the rendered UI/);
});

test("router and new guides preserve CAO authority boundaries", () => {
  assert.match(skill, /`review` is the default/);
  assert.match(skill, /read-only; writes use declared safe outputs only/);
  assert.match(skill, /Fail closed/);
  assert.match(skill, /Never print or persist tokens/);
  for (const guide of [taskGuides.operations, taskGuides["gh aw"], taskGuides.gh]) {
    assert.match(read(guide), /safe output|read-only|review/i, `${guide} must state its boundary`);
  }
  assert.match(read(taskGuides["gh aw"]), /lock\.yml/);
  assert.match(read(taskGuides["gh aw"]), /--schedule-seed githubnext\/gh-cao/);
});
