import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { bundleDashboardFiles, loadDashboardSource } from "../../dashboard/report/bundle-dashboards.mjs";

function document(id, navigationLabel) {
  return {
    "language-version": "0.1.0",
    dashboard: {
      id,
      title: id,
      pages: [{ id, kind: "custom", views: [{ id: `${id}-view`, data: { source: "runs" }, mark: "metric", encoding: { value: { field: "run", aggregate: "count" } } }] }],
      navigation: [{ label: navigationLabel, pages: [id] }],
    },
  };
}

test("bundles canonical campaign dashboards into one deterministic document", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-bundle-"));
  const output = path.join(root, "dashboard.json");
  const additions = path.join(root, "dashboards");
  await mkdir(additions);
  await writeFile(output, `${JSON.stringify(document("overview", "Explore"))}\n`);
  await writeFile(path.join(additions, "zeta.json"), `${JSON.stringify(document("zeta", "Campaign operations"))}\n`);
  await writeFile(path.join(additions, "alpha.json"), `${JSON.stringify(document("alpha", "Campaign operations"))}\n`);

  try {
    await bundleDashboardFiles(output, [path.join(additions, "zeta.json"), path.join(additions, "alpha.json")]);
    const bundled = JSON.parse(await readFile(output, "utf8"));
    assert.deepEqual(bundled.dashboard.pages.map(({ id }) => id), ["overview", "alpha", "zeta"]);
    assert.deepEqual(bundled.dashboard.navigation, [
      { label: "Explore", pages: ["overview"] },
      { label: "Campaign operations", pages: ["alpha", "zeta"] },
    ]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("composes feature fragments with multiple related queries and views before bundling", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-fragments-"));
  const output = path.join(root, "dashboard.json");
  const additions = path.join(root, "dashboards");
  const features = path.join(root, "features");
  await mkdir(additions);
  await mkdir(features);
  await writeFile(output, `${JSON.stringify({
    "language-version": "0.1.0",
    fragments: ["features/repositories.json"],
    dashboard: {
      id: "overview",
      title: "Overview",
      pages: [{ id: "overview", kind: "custom", views: [] }],
      navigation: [{ label: "Explore", pages: ["overview"] }],
    },
  })}\n`);
  await writeFile(path.join(features, "repositories.json"), `${JSON.stringify({
    queries: [
      { name: "repository-summary", from: "repositories" },
      { name: "repository-runs", from: "runs" },
    ],
    views: [
      { id: "repository-health", data: { source: "repository-summary" }, mark: "metric" },
      { id: "repository-history", data: { source: "repository-runs" }, mark: "table" },
    ],
    pages: [
      { id: "repositories", kind: "custom", views: ["repository-health", "repository-history"] },
      { id: "repository-runs", kind: "custom", views: ["repository-history"] },
    ],
    navigation: [{ label: "Explore", pages: ["repositories", "repository-runs"] }],
  })}\n`);

  try {
    await bundleDashboardFiles(output, []);
    const bundled = JSON.parse(await readFile(output, "utf8"));
    assert.equal(bundled.fragments, undefined);
    assert.deepEqual(bundled.dashboard.queries.map(({ name }) => name), [
      "repository-summary",
      "repository-runs",
    ]);
    assert.deepEqual(bundled.dashboard.views.map(({ id }) => id), [
      "repository-health",
      "repository-history",
    ]);
    assert.deepEqual(bundled.dashboard.pages.map(({ id }) => id), [
      "overview",
      "repositories",
      "repository-runs",
    ]);
    assert.deepEqual(bundled.dashboard.navigation, [{
      label: "Explore",
      pages: ["overview", "repositories", "repository-runs"],
    }]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("rejects fragment paths that escape through traversal or symlinks", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-fragment-paths-"));
  const source = path.join(root, "source");
  const outside = path.join(root, "outside");
  await mkdir(source);
  await mkdir(outside);
  await writeFile(path.join(outside, "fragment.json"), JSON.stringify({ pages: [{ id: "outside" }] }));

  try {
    await writeFile(path.join(source, "dashboard.json"), JSON.stringify({
      "language-version": "0.1.0",
      fragments: ["../outside/fragment.json"],
      dashboard: { id: "test", title: "Test" },
    }));
    await assert.rejects(
      loadDashboardSource(path.join(source, "dashboard.json")),
      /fragment path escapes its source directory/,
    );

    await symlink(path.join(outside, "fragment.json"), path.join(source, "linked.json"));
    await writeFile(path.join(source, "dashboard.json"), JSON.stringify({
      "language-version": "0.1.0",
      fragments: ["linked.json"],
      dashboard: { id: "test", title: "Test" },
    }));
    await assert.rejects(
      loadDashboardSource(path.join(source, "dashboard.json")),
      /fragment path escapes its source directory/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
