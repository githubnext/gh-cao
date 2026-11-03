import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { validateAgentPlugin } from "../../scripts/validate-agent-plugin.mjs";

const root = new URL("../../", import.meta.url);

test("Agent Plugins manifest exposes portable skills and Copilot namespace", async () => {
  const manifest = JSON.parse(await readFile(new URL("plugin.json", root), "utf8"));

  assert.equal(
    manifest.$schema,
    "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  );
  assert.equal(manifest.name, "central-agentic-ops");
  assert.match(manifest.description, /analyze activity data/);
  assert.match(manifest.description, /local CAO dashboard previews/);
  assert.deepEqual(manifest.extensions, { "com.github.copilot": {} });
  for (const skillName of [
    "setup-cao",
    "debug-cao",
    "add-cao-campaign",
    "create-cao-campaign",
    "analyze-cao",
    "cao-cli",
  ]) {
    const skill = await readFile(
      new URL(`skills/${skillName}/SKILL.md`, root),
      "utf8",
    );
    assert.match(skill, new RegExp(`^---\\nname: ${skillName}\\n`));
  }
});

test("add-cao-campaign requires discovery, consent, and review-safe installation", async () => {
  const skill = await readFile(
    new URL("skills/add-cao-campaign/SKILL.md", root),
    "utf8",
  );

  assert.match(skill, /same commit/);
  assert.match(skill, /Exclude campaigns with `private: true`/);
  assert.match(skill, /no more than three installable campaigns/);
  assert.match(skill, /explicit approval/);
  assert.match(skill, /gh cao add githubnext\/gh-cao\/<campaign-slug>@<catalog-commit>/);
  assert.match(skill, /must remain in review/);
  assert.match(skill, /did not broaden or change/);
  assert.match(skill, /Treat all installed campaign package sources as immutable/);
  assert.match(skill, /\.github\/cao\/<campaign-slug>\.md/);
});

test("Copilot extension uses the current Canvas provider contract", async () => {
  const extensionRoot = new URL(
    "com.github.copilot/extensions/cao-dashboard/",
    root,
  );
  const metadata = JSON.parse(
    await readFile(new URL("copilot-extension.json", extensionRoot), "utf8"),
  );
  const [entry, source] = await Promise.all([
    readFile(new URL("extension.mjs", extensionRoot), "utf8"),
    readFile(new URL("dashboard-extension.mjs", extensionRoot), "utf8"),
  ]);

  assert.deepEqual(metadata, { name: "cao-dashboard", version: 1 });
  assert.match(entry, /@github\/copilot-sdk\/extension/);
  assert.match(entry, /joinSession\(config\)/);
  assert.match(source, /canvases:/);
  assert.match(source, /createCanvas\(\{/);
  assert.match(source, /context\.session\?\.workingDirectory/);
  assert.match(source, /startLocalDashboardPreview/);
  assert.match(source, /requestedEnvironmentVariables: \["GH_TOKEN", "GITHUB_TOKEN"\]/);
  assert.match(source, /executeDashboardCommand/);
  assert.match(source, /cao_dashboard_execute_query/);
  assert.match(source, /cao_dashboard_read_data_specification/);
  assert.match(source, /onSessionStart:/);
  assert.match(source, /onSessionEnd: async/);
  assert.match(source, /if \(reason === "user_exit"\) await closePreviews\(\)/);
  assert.match(source, /onClose:/);
  assert.match(source, /additionalProperties: false/);
  assert.doesNotMatch(source, /github\.io|https:/);
  assert.match(entry, /process\.once\("SIGTERM", shutdown\)/);
  assert.doesNotMatch(entry, /process\.once\("exit"/);
  assert.match(entry, /approveCommand: approveDashboardCommand/);
  assert.doesNotMatch(entry, /session\.ui\.confirm|session\.ui\.elicitation/);
});

test("the complete plugin passes portable metadata, link, and bundled-resource validation", async () => {
  assert.deepEqual(await validateAgentPlugin(new URL("../../", import.meta.url)), []);
});

test("portable skill validation rejects client-specific top-level fields and broken references", async () => {
  const directory = await mkdtemp(join(tmpdir(), "cao-plugin-validation-"));
  try {
    await writeFile(join(directory, "plugin.json"), JSON.stringify({
      $schema: "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
      name: "example",
    }));
    const skill = join(directory, "skills", "example");
    await mkdir(skill, { recursive: true });
    await writeFile(join(skill, "SKILL.md"), [
      "---", "name: example", "description: Example skill", "argument-hint: invalid", "---",
      "[Missing](references/missing.md)",
    ].join("\n"));
    const errors = await validateAgentPlugin(directory);
    assert.ok(errors.some((error) => error.includes("unsupported skill field argument-hint")));
    assert.ok(errors.some((error) => error.includes("references/missing.md: missing package file")));
    await writeFile(join(skill, "SKILL.md"), [
      "---", "name: example", "description: Example skill", "metadata:", "  argument-hint: valid", "---",
    ].join("\n"));
    const fixedErrors = await validateAgentPlugin(directory);
    assert.ok(!fixedErrors.some((error) => error.includes("unsupported skill field")));
    assert.ok(fixedErrors.some((error) => error.includes("dashboard/canvas-server.mjs: missing package file")));
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test("plugin validation rejects package paths outside the installed root", async () => {
  const directory = await mkdtemp(join(tmpdir(), "cao-plugin-path-validation-"));
  try {
    const plugin = join(directory, "plugin");
    await mkdir(plugin);
    const manifest = join(directory, "outside.json");
    await writeFile(manifest, "{}");
    await symlink(manifest, join(plugin, "plugin.json"));
    const errors = await validateAgentPlugin(plugin);
    assert.ok(errors.some((error) => error.includes("plugin.json: package path resolves outside the plugin root")));
  } finally { await rm(directory, { recursive: true, force: true }); }
});
