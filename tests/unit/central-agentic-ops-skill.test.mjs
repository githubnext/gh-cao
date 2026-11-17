import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, lstatSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
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

  assert.equal(lstatSync(join(root, "skills", "central-agentic-ops")).isSymbolicLink(), false);
  // skills/ is the only copy: gh skill discovers skills/*, and gh aw collides on a second copy.
  for (const copy of [[".github", "skills"], [".agents", "skills"]]) {
    assert.equal(existsSync(join(root, ...copy, "central-agentic-ops")), false, copy.join("/"));
  }
  assert.match(read("AGENTS.md"), /\[`central-agentic-ops` skill\]\(skills\/central-agentic-ops\/SKILL\.md\)/);

  const manifest = read("aw.yml");
  assert.match(manifest, /^\s+- skills\/central-agentic-ops$/m);
  assert.match(read("docs/agent-plugin.md"), /central-agentic-ops/);
});

test("central-agentic-ops resolves guides outside a checkout for gh skill installs", () => {
  const frontmatter = skill.slice(0, skill.indexOf("\n---", 4));
  assert.match(frontmatter, /^license: MIT$/m);
  assert.match(frontmatter, /^compatibility: "[^"\n]+"$/m);
  assert.match(skill, /^\s*curl -fsSL "https:\/\/raw\.githubusercontent\.com\/githubnext\/gh-cao\/REF\/PATH"$/m);
  assert.match(skill, /metadata\.github-ref/);
  assert.doesNotMatch(skill, /Former skill|\b(?:setup-cao|add-cao-campaign|create-cao-campaign|debug-cao|analyze-cao)\b|\bcao-cli\b(?!\.md)/);
});

const ghSkillAvailable = spawnSync("gh", ["skill", "--help"], { stdio: "ignore" }).status === 0;

test("gh skill validates and installs central-agentic-ops from the current commit", {
  skip: !ghSkillAvailable && !process.env.CI && "gh skill is unavailable",
}, () => {
  const temp = mkdtempSync(join(tmpdir(), "cao-gh-skill-"));
  try {
    const source = join(temp, "source");
    const installed = join(temp, "installed");
    mkdirSync(source);
    execFileSync("tar", ["-x", "-C", source], { input: execFileSync("git", ["archive", "HEAD", "skills"], { cwd: root }) });
    const env = { ...process.env, GH_CONFIG_DIR: join(temp, "gh"), GH_PROMPT_DISABLED: "1", GH_TOKEN: "", GITHUB_TOKEN: "" };
    const gh = (...args) => {
      const result = spawnSync("gh", ["skill", ...args], { env, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], timeout: 60_000 });
      assert.equal(result.status, 0, `gh skill ${args.join(" ")}\n${result.stdout}${result.stderr}`);
      return `${result.stdout}${result.stderr}`;
    };

    const published = gh("publish", "--dry-run", source);
    assert.doesNotMatch(published, /^error\b/im);
    assert.doesNotMatch(published, /^\S+\s+central-agentic-ops\b/m, "central-agentic-ops must validate without warnings");

    gh("install", source, "central-agentic-ops", "--from-local", "--dir", installed);
    const installedDir = join(installed, "central-agentic-ops");
    assert.deepEqual(readdirSync(installedDir), readdirSync(join(source, "skills", "central-agentic-ops")));
    const installedSkill = readFileSync(join(installedDir, "SKILL.md"), "utf8");
    assert.match(installedSkill, /^name: central-agentic-ops$/m);
    assert.match(installedSkill, /^\s+local-path: .+central-agentic-ops$/m);
    const body = (text) => text.slice(text.indexOf("\n---\n", 4) + 5).trim();
    assert.equal(body(installedSkill), body(readFileSync(join(source, "skills", "central-agentic-ops", "SKILL.md"), "utf8")));

    // A standalone install fetches guides by raw URL at its ref, so each must exist at this commit.
    const tracked = new Set(execFileSync("git", ["ls-tree", "-r", "--name-only", "HEAD", ".github/cao"], { cwd: root, encoding: "utf8" }).split("\n"));
    const guides = [...installedSkill.matchAll(/`(\.github\/cao\/[^`\s]+\.md)`/g)].map(([, path]) => path);
    assert.ok(guides.length >= Object.keys(taskGuides).length);
    for (const guide of guides) assert.ok(tracked.has(guide), `${guide} is not committed at HEAD`);
  } finally {
    rmSync(temp, { recursive: true, force: true });
  }
});

test("former CAO skills are removed without legacy aliases", () => {
  for (const former of ["setup-cao", "add-cao-campaign", "create-cao-campaign", "debug-cao", "analyze-cao", "cao-cli"]) {
    assert.equal(existsSync(join(root, "skills", former)), false, former);
    assert.equal(existsSync(join(root, ".github", "skills", former)), false, former);
  }
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
