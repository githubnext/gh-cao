import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import test from "node:test";
import { promisify } from "node:util";
import {
  buildDashboardSite,
  embedDashboardVersion,
  embedDashboardVersions,
  filterExperimentalDashboardViews,
  loadDashboardControlSettings,
} from "../../dashboard/site/scripts/build.mjs";
import { validateDashboardAgentArtifacts } from "../../dashboard/site/scripts/llms.mjs";
import { loadDashboardSource } from "../../dashboard/report/bundle-dashboards.mjs";
import { agentCatalog } from "../../dashboard/site/src/agent/catalog.js";

const execFileAsync = promisify(execFile);

async function builtSiteSha(destination) {
  const index = await readFile(new URL("index.html", destination), "utf8");
  return index.match(/script\.src = '\.\/src\/main\.js\?sha=([a-f0-9]{64})'/)?.[1];
}

test("dashboard site filters experimental views unless explicitly enabled", () => {
  const document = {
    "language-version": "0.1.0",
    dashboard: {
      pages: [
        { id: "stable" },
        { id: "preview" },
        { id: "preview-tab", experimental: true, route: { "navigation-page": "preview" } },
      ],
      navigation: [
        { label: "Main", pages: ["stable"] },
        { label: "Preview", experimental: true, pages: ["preview"] },
      ],
      callouts: [
        { id: "stable-callout", "navigation-page": "stable" },
        { id: "preview-callout", "navigation-page": "preview" },
        { id: "preview-tab-callout", "navigation-page": "preview-tab" },
      ],
    },
  };

  const filtered = filterExperimentalDashboardViews(document);
  assert.deepEqual(filtered.dashboard.pages, [{ id: "stable" }]);
  assert.deepEqual(filtered.dashboard.navigation, [{ label: "Main", pages: ["stable"] }]);
  assert.deepEqual(filtered.dashboard.callouts, [{ id: "stable-callout", "navigation-page": "stable" }]);
  assert.equal(filterExperimentalDashboardViews(document, true), document);
});

test("dashboard site embeds a validated commit SHA", () => {
  const html = '<meta name="dashboard-version" content="development">';
  const commitSha = "0123456789abcdef0123456789abcdef01234567";
  assert.equal(
    embedDashboardVersion(html, commitSha),
    `<meta name="dashboard-version" content="${commitSha}">`,
  );
  assert.throws(
    () => embedDashboardVersion(html, "not-a-commit"),
    /dashboard commit SHA must be a 40-character lowercase hexadecimal string/,
  );
});

test("dashboard site embeds validated CAO and control-policy gh-aw versions", () => {
  const html = '<meta name="cao-version" content=""><meta name="gh-aw-version" content="">';
  assert.equal(
    embedDashboardVersions(html, { caoVersion: "1.2.3", ghAwVersion: "v0.91.1" }),
    '<meta name="cao-version" content="1.2.3"><meta name="gh-aw-version" content="v0.91.1">',
  );
  assert.equal(embedDashboardVersions(html, {}), html);
  assert.throws(
    () => embedDashboardVersions(html, { ghAwVersion: '"><script>alert(1)</script>' }),
    /gh-aw-version must be a semantic version/,
  );
});

test("dashboard build uses GitHub Actions repository and server URL for the chrome and version links", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-actions-links-"));
  try {
    await buildDashboardSite({
      destination: path.join(root, "output"),
      controlSettings: { campaigns: {} },
      commitSha: "0123456789abcdef0123456789abcdef01234567",
      githubRepository: "octo-org/operations",
      githubServerUrl: "https://github.example.com",
    });
    const dashboard = JSON.parse(await readFile(path.join(root, "output", "dashboard.json"), "utf8"));
    assert.equal(dashboard.dashboard.repository, "octo-org/operations");
    assert.equal(dashboard.dashboard["github-url-base"], "https://github.example.com");
    const index = await readFile(path.join(root, "output", "index.html"), "utf8");
    const controlPolicy = JSON.parse(await readFile(path.resolve(".github/workflows/cao.json"), "utf8"));
    assert.ok(index.includes('<meta name="cao-version" content="0.0.0-main.0123456789ab">'));
    assert.ok(index.includes(`<meta name="gh-aw-version" content="${controlPolicy["gh-aw-version"]}">`));
    const { document } = await loadDashboardSource(path.resolve("dashboard/site/dashboard.json"));
    const queries = JSON.parse(await readFile(path.join(root, "output", "src/agent/queries.generated.json"), "utf8"));
    const catalog = JSON.parse(await readFile(path.join(root, "output", "src/agent/catalog.generated.json"), "utf8"));
    assert.deepEqual(queries, document.dashboard.queries);
    assert.ok(queries.some(({ name }) => name === "overview-workflow-health-summary"));
    assert.equal(catalog.pages.find((page) => page.id === "friction")?.experimental, false);
    assert.ok(dashboard.dashboard.navigation.some(
      (section) => section.label === "Data" && section.pages.includes("friction"),
    ));
    assert.deepEqual(catalog, agentCatalog({
      ...document,
      dashboard: {
        ...filterExperimentalDashboardViews(document).dashboard,
        repository: "octo-org/operations",
        "github-url-base": "https://github.example.com",
      },
    }));
  } finally {
    await rm(root, { force: true, recursive: true });
  }
});

test("dashboard build uses an explicit packaged CAO version rather than the source revision", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-package-version-"));
  try {
    await buildDashboardSite({
      destination: path.join(root, "output"),
      controlSettings: { campaigns: {} },
      commitSha: "0123456789abcdef0123456789abcdef01234567",
      caoVersion: "v1.2.3",
    });
    const index = await readFile(path.join(root, "output", "index.html"), "utf8");
    assert.ok(index.includes('<meta name="cao-version" content="v1.2.3">'));
  } finally {
    await rm(root, { force: true, recursive: true });
  }
});

test("dashboard build accepts resolved settings and extracts composed policy settings", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-control-settings-"));
  try {
    const resolvedSettings = path.join(root, "control-settings.json");
    await writeFile(resolvedSettings, '{"web":{"experimental":true}}\n');
    assert.deepEqual(await loadDashboardControlSettings(resolvedSettings), {
      web: { experimental: true },
    });

    const baseSettings = path.join(root, "cao.json");
    await writeFile(baseSettings, await readFile(path.resolve(".github/workflows/cao.json"), "utf8"));
    const profileSettings = path.join(root, "cao.test.json");
    await writeFile(profileSettings, JSON.stringify({
      extends: "cao.json",
      "control-plane": { web: { host: {
        target: { module: "container", name: "test" },
        redis: { module: "generic" },
      } } },
    }));
    const composedSettings = await loadDashboardControlSettings(profileSettings);
    assert.equal(composedSettings.web.experimental, true);
    assert.equal(composedSettings.web.host.target.name, "test");
    assert.ok(composedSettings.campaigns);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("dashboard site ignores the legacy flat dashboards directory", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-site-layout-"));
  const repositoryRoot = path.join(root, "repository");
  const destination = path.join(root, "output");

  try {
    await mkdir(path.join(repositoryRoot, "dashboards"), { recursive: true });
    await writeFile(path.join(repositoryRoot, "dashboards", "legacy.json"), "not valid dashboard JSON");
    await buildDashboardSite({ destination, repositoryRoot, controlSettings: { campaigns: {} } });
    assert.ok(JSON.parse(await readFile(path.join(destination, "dashboard.json"), "utf8")).dashboard);
  } finally {
    await rm(root, { force: true, recursive: true });
  }
});

test("docs dashboard installs renderer assets without experimental campaign pages", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-site-build-"));
  const destination = pathToFileURL(`${root}/cao/`);
  const controlSettings = {
    web: { experimental: true, favicon: "https://example.com/dashboard.svg" },
    campaigns: { "uk-ai-advisory": {}, dependabot: {} },
  };

  try {
    await buildDashboardSite({ destination, controlSettings });
    const dashboard = JSON.parse(await readFile(new URL("dashboard.json", destination), "utf8"));
    const pageIds = dashboard.dashboard.pages.map(({ id }) => id);
    assert.ok(!pageIds.includes("uk-ai-advisory-dashboard"));
    assert.ok(!pageIds.includes("dependabot-dashboard"));
    assert.ok(!pageIds.includes("ambient-context-dashboard"));
    assert.match(
      await readFile(new URL("index.html", destination), "utf8"),
      /<link rel="icon" href="https:\/\/example\.com\/dashboard\.svg">/,
    );
    const builtIndex = await readFile(new URL("index.html", destination), "utf8");
    assert.match(builtIndex, /<link rel="alternate" type="text\/plain" href="\.\/llms\.txt"/);
    assert.match(builtIndex, /<a href="\.\/llms\.txt">Agent access guide<\/a>/);
    const llms = await readFile(new URL("llms.txt", destination), "utf8");
    for (const section of [
      "## Choose the cheapest access path",
      "## Skills",
      "## Published Activity artifacts",
      "## Targeted queries with MCP",
      "## Bulk analysis with CAO CLI and SQLite",
      "## Schema and freshness",
    ]) {
      assert.match(llms, new RegExp(section));
    }
    assert.match(llms, /raw\.githubusercontent\.com\/githubnext\/gh-cao\/main\/skills\/debug-cao\/SKILL\.md/);
    assert.match(llms, /\[Agent summary\]\(\.\/agent-summary\.json\)/);
    const mainHash = builtIndex.match(/script\.src = '\.\/src\/main\.js\?sha=([a-f0-9]{64})'/)?.[1];
    assert.ok(mainHash, "entry module includes the site content SHA");
    assert.ok(builtIndex.includes(`rel="modulepreload" href="./src/main.js?sha=${mainHash}"`));
    assert.ok(builtIndex.includes(`rel="stylesheet" href="./styles.css?sha=${mainHash}"`));
    const stylesheet = await readFile(new URL("styles.css", destination), "utf8");
    assert.ok(stylesheet.includes(".sidebar") && stylesheet.includes(".dashboard-notifications"),
      "compiled stylesheet preserves the dashboard and notification styles");
    assert.match(
      builtIndex,
      new RegExp(`script\\.src = './src/main\\.js\\?sha=${mainHash}'`),
    );
    assert.match(
      await readFile(new URL("src/main.js", destination), "utf8"),
      /sourceMappingURL=main\.js\.map/,
    );
    assert.match(
      await readFile(new URL("src/data-worker.js", destination), "utf8"),
      /sourceMappingURL=data-worker\.js\.map/,
    );
    const mainSourceMap = JSON.parse(await readFile(new URL("src/main.js.map", destination), "utf8"));
    const workerSourceMap = JSON.parse(await readFile(new URL("src/data-worker.js.map", destination), "utf8"));
    assert.ok(!mainSourceMap.sources.some((source) => source.endsWith("/src/dashboard-app.js")));
    const chunkFiles = (await readdir(new URL("src/", destination)))
      .filter((file) => file.startsWith("chunk-") && file.endsWith(".js"));
    assert.ok(chunkFiles.length > 0, "dynamic imports emit separate JavaScript chunks");
    const chunkMaps = await Promise.all(chunkFiles.map(async (file) => (
      JSON.parse(await readFile(new URL(`src/${file}.map`, destination), "utf8"))
    )));
    assert.ok(chunkMaps.some((map) => map.sources.some((source) => source.endsWith("/src/dashboard-app.js"))));
    const preloadedChunks = [...builtIndex.matchAll(/<link rel="modulepreload" href="\.\/src\/([^"]+)" fetchpriority="low">/g)]
      .map((match) => match[1]).filter((file) => file.startsWith("chunk-"));
    assert.ok(preloadedChunks.length > 0 && preloadedChunks.length <= 6,
      "startup preloads stay bounded and leave bandwidth for the render-blocking stylesheet");
    assert.ok(preloadedChunks.some((file) => chunkMaps[chunkFiles.indexOf(file)].sources
      .some((source) => source.endsWith("/src/dashboard-app.js"))), "application startup is preloaded");
    for (const file of preloadedChunks) {
      assert.ok(chunkFiles.includes(file), `preloaded chunk exists: ${file}`);
      const map = chunkMaps[chunkFiles.indexOf(file)];
      assert.ok(!map.sources.some((source) => /\/(campaign-route-view|workflow-route-page)\.js$/.test(source)),
        "route modules remain deferred");
      assert.ok(!map.sources.some((source) => /\/(dashboard-fixtures|diagnostics|configuration-view|campaign-memory|measure-history|outcome-detail|outcome-detail-sections)\.js$/.test(source)),
        "optional and fixture modules remain deferred");
      assert.ok(!map.sources.some((source) => source.endsWith("/webmcp/runtime.js")),
        "WebMCP loads only when the browser supports it");
      assert.ok(!/^chunk-(reset-dashboard-control|browser-support)-/.test(file),
        "recovery and unsupported-browser entry points remain deferred");
    }
    const serviceWorker = await readFile(new URL("service-worker.js", destination), "utf8");
    for (const file of chunkFiles) {
      assert.ok(serviceWorker.includes(`src/${file}`), `precache includes ${file}`);
      assert.ok(!serviceWorker.includes(`src/${file}.map`), "precache excludes source maps");
    }
    assert.ok(workerSourceMap.sources.some((source) => source.endsWith("/src/data-worker.js")));
    assert.ok(workerSourceMap.sources.some((source) => source.endsWith("/src/data/storage/indexeddb.js")),
      "worker dependencies are bundled without main-thread chunk discovery");
    assert.equal(mainSourceMap.sources.length, mainSourceMap.sourcesContent.length);
    assert.equal(workerSourceMap.sources.length, workerSourceMap.sourcesContent.length);
    await assert.rejects(readFile(new URL("src/presenter.js", destination), "utf8"), { code: "ENOENT" });
    const completeSprite = await readFile(new URL("src/octicons.svg", destination), "utf8");
    assert.ok(completeSprite.includes("octicon-feed-trophy"));
    assert.ok(serviceWorker.includes("src/octicons.svg"), "the complete icon set remains available offline");
    assert.match(
      (await Promise.all(chunkFiles.map((file) => readFile(new URL(`src/${file}`, destination), "utf8")))).join("\n"),
      /octicon-question/,
      "bundled JavaScript includes Octicon glyphs",
    );
    await assert.rejects(readFile(new URL("uk-ai-advisory-dashboard/index.html", destination), "utf8"), { code: "ENOENT" });
    await assert.rejects(readFile(new URL("dependabot-dashboard/index.html", destination), "utf8"), { code: "ENOENT" });
    assert.match(
      await readFile(new URL("service-worker.js", destination), "utf8"),
      /periodicsync/,
    );
    const webManifest = JSON.parse(await readFile(new URL("manifest.webmanifest", destination), "utf8"));
    assert.equal(webManifest.scope, "./");
    assert.equal(webManifest.display, "standalone");
    assert.equal(webManifest.icons.length, 3);
    assert.match(
      await readFile(new URL("service-worker.js", destination), "utf8"),
      new RegExp(`const VERSION = '${mainHash}';`),
    );

    await assert.rejects(
      readFile(new URL("README.md", destination), "utf8"),
      (error) => error?.code === "ENOENT",
      "build copied a dashboard development-only source",
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("dashboard cache hashes are stable and change with assembled site content", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-site-cache-hash-"));
  const firstDestination = pathToFileURL(`${root}/first/`);
  const secondDestination = pathToFileURL(`${root}/second/`);
  const changedDestination = pathToFileURL(`${root}/changed/`);
  const controlSettings = { web: { favicon: "https://example.com/dashboard.svg" } };

  try {
    await buildDashboardSite({ destination: firstDestination, controlSettings });
    await buildDashboardSite({ destination: secondDestination, controlSettings });
    await buildDashboardSite({
      destination: changedDestination,
      controlSettings: { web: { favicon: "https://example.com/changed.svg" } },
    });

    const firstSha = await builtSiteSha(firstDestination);
    const secondSha = await builtSiteSha(secondDestination);
    const changedSha = await builtSiteSha(changedDestination);
    assert.ok(firstSha, "first build includes a cache hash");
    assert.equal(secondSha, firstSha, "identical content produces the same cache hash");
    assert.notEqual(changedSha, firstSha, "changed content produces a different cache hash");
    assert.match(await readFile(new URL("src/main.js", changedDestination), "utf8"), /sourceMappingURL=main\.js\.map/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("dashboard agent guide describes and validates the assembled public artifacts", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-agent-guide-"));
  const activityDataPath = path.join(root, "activity");
  const destination = path.join(root, "cao");
  const artifacts = {
    "agent-summary.json": '{"schemaVersion":1,"generatedAt":"2026-09-27T00:00:00Z"}\n',
    "inventory-sources.json": "{}\n",
    "payload-hashes.json": "{}\n",
    "gh-aw-logs.sqlite": "sqlite fixture",
  };

  try {
    await mkdir(activityDataPath);
    for (const [name, content] of Object.entries(artifacts)) {
      await writeFile(path.join(activityDataPath, name), content);
    }
    await buildDashboardSite({ destination, activityDataPath, controlSettings: { campaigns: {} } });
    for (const [name, content] of Object.entries(artifacts)) {
      await writeFile(path.join(destination, name), content);
    }
    await execFileAsync(process.execPath, [
      path.resolve("dashboard/site/scripts/llms.mjs"),
      "generate",
      path.join(destination, "llms.txt"),
      destination,
    ]);
    await execFileAsync(process.execPath, [
      path.resolve("dashboard/site/scripts/llms.mjs"),
      "validate",
      destination,
    ], { cwd: root });
    await validateDashboardAgentArtifacts({
      sitePath: destination,
    });
    const llms = await readFile(path.join(destination, "llms.txt"), "utf8");
    assert.match(llms, /Agent summary.*57 bytes/);
    assert.doesNotMatch(llms, /(?:token|secret|password|private[-_ ]key)\s*[:=]\s*\S+/i);
    await writeFile(path.join(destination, "llms.txt"), llms.replace(
      "https://raw.githubusercontent.com/githubnext/gh-cao/main/skills/setup-cao/SKILL.md", "missing-skill-url",
    ));
    await assert.rejects(validateDashboardAgentArtifacts({ sitePath: destination }), /missing raw skill URL: setup-cao/);
    await writeFile(path.join(destination, "llms.txt"), llms);

    await assert.rejects(
      execFileAsync(process.execPath, [
        path.resolve("dashboard/site/scripts/llms.mjs"),
        "validate",
        path.join(root, "missing"),
      ]),
      (error) => error?.code !== 0,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
