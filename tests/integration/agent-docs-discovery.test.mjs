import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { test } from "node:test";

const root = path.resolve(import.meta.dirname, "../..");
const dist = path.join(root, "dist");
const publicBase = new URL("https://githubnext.github.io/gh-cao/");

function hrefForRel(html, rel, type) {
  const tags = html.match(/<link\b[^>]*>/g) ?? [];
  const tag = tags.find((candidate) =>
    candidate.includes(`rel="${rel}"`) && (!type || candidate.includes(`type="${type}"`))
  );
  return tag?.match(/\bhref="([^"]+)"/)?.[1];
}

function markdownLinks(markdown) {
  return [...markdown.matchAll(/\[[^\]]+\]\(([^)\s]+)\)/g)].map((match) => match[1]);
}

function fileForPublicUrl(url) {
  const parsed = new URL(url, publicBase);
  assert.equal(parsed.origin, publicBase.origin);
  assert.ok(parsed.pathname.startsWith(publicBase.pathname));
  const relative = parsed.pathname.slice(publicBase.pathname.length);
  return path.join(dist, relative || "index.html");
}

test("documentation links expose complete static social cards for Teams", async () => {
  function meta(html, key) {
    const tags = (html.match(/<meta\b[^>]*>/g) ?? [])
      .filter((tag) => tag.includes(`property="${key}"`) || tag.includes(`name="${key}"`));
    assert.equal(tags.length, 1, `expected exactly one ${key} tag`);
    return tags[0].match(/\bcontent="([^"]+)"/)?.[1];
  }

  for (const route of ["", "architecture/", "catalog/"]) {
    const html = await readFile(path.join(dist, route, "index.html"), "utf8");
    const imageUrl = meta(html, "og:image");
    assert.equal(imageUrl, new URL("social-card.png", publicBase).href);
    assert.equal(meta(html, "twitter:image"), imageUrl);
    assert.equal(meta(html, "twitter:card"), "summary_large_image");
    assert.equal(meta(html, "og:image:type"), "image/png");
    assert.ok(meta(html, "og:image:alt"));
    assert.equal(meta(html, "twitter:image:alt"), meta(html, "og:image:alt"));
    assert.ok(meta(html, "og:title"));
    assert.ok(meta(html, "og:description"));
    assert.equal(meta(html, "og:url"), new URL(route, publicBase).href);

    const image = await readFile(fileForPublicUrl(imageUrl));
    assert.equal(image.subarray(0, 8).toString("hex"), "89504e470d0a1a0a");
    assert.equal(image.readUInt32BE(16), Number(meta(html, "og:image:width")));
    assert.equal(image.readUInt32BE(20), Number(meta(html, "og:image:height")));
    assert.equal(image.readUInt32BE(16), 1200);
    assert.equal(image.readUInt32BE(20), 630);
  }

  const landingHtml = await readFile(path.join(dist, "index.html"), "utf8");
  assert.deepEqual(landingHtml.match(/<title>[^<]*<\/title>/g), [
    "<title>Central Agentic Ops (CAO)</title>",
  ]);
  assert.equal(meta(landingHtml, "og:title"), "Central Agentic Ops (CAO)");
  assert.equal(meta(landingHtml, "og:type"), "website");
  const articleHtml = await readFile(path.join(dist, "architecture/index.html"), "utf8");
  assert.equal(meta(articleHtml, "og:type"), "article");
  assert.match(articleHtml, /<title>Architecture \| Central Agentic Ops<\/title>/);
});

test("an external agent can traverse static discovery and resource metadata", async () => {
  const landingHtml = await readFile(path.join(dist, "index.html"), "utf8");
  const scopedHref = hrefForRel(landingHtml, "describedby");
  assert.ok(scopedHref, "landing HTML must advertise scoped llms.txt");
  const indexHref = hrefForRel(landingHtml, "index", "application/json");
  assert.ok(indexHref, "landing HTML must advertise the machine-readable resource index");
  const index = JSON.parse(await readFile(fileForPublicUrl(indexHref), "utf8"));
  const dashboardSummary = index.resources.find((entry) => entry.id === "dashboard");
  assert.deepEqual(dashboardSummary.interfaces, ["cli", "cli-mcp", "web-mcp"]);

  const scoped = await readFile(fileForPublicUrl(scopedHref), "utf8");
  const links = markdownLinks(scoped);
  const resourceHtmlUrl = links.find((link) => link.endsWith("/architecture/"));
  assert.ok(resourceHtmlUrl, "scoped llms.txt must expose an important resource route");

  const resourceHtml = await readFile(
    path.join(fileForPublicUrl(resourceHtmlUrl), "index.html"),
    "utf8",
  );
  const alternateHref = hrefForRel(resourceHtml, "alternate", "application/json");
  assert.ok(alternateHref, "resource HTML must advertise JSON metadata");

  const resource = JSON.parse(await readFile(fileForPublicUrl(alternateHref), "utf8"));
  const related = resource.links.find((link) => link.rel === "related");
  assert.ok(related, "resource metadata must expose a related resource");
  const relatedResource = JSON.parse(await readFile(fileForPublicUrl(related.href), "utf8"));

  assert.equal(resource.provenance.repository, "githubnext/gh-cao");
  assert.match(resource.provenance.commit, /^[0-9a-f]{40}$/);
  assert.equal(resource.provenance.commit, process.env.PUBLIC_PAGES_BUILD_COMMIT
    || execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim());
  assert.ok(Number.isFinite(Date.parse(resource.freshness.generatedAt)));
  assert.ok(Number.isFinite(Date.parse(resource.freshness.sourceCommittedAt)));
  assert.equal(resource.source.path, "docs/architecture.md");
  assert.match(resource.integrity.sourceDigest, /^[0-9a-f]{64}$/);
  assert.equal(typeof relatedResource.id, "string");
  assert.equal(typeof relatedResource.provenance.source, "string");
  assert.ok(Number.isFinite(Date.parse(relatedResource.freshness.generatedAt)));

  const dashboard = JSON.parse(await readFile(path.join(dist, "dashboard", "index.json"), "utf8"));
  assert.equal(dashboard.interfaces.cli[0].subcommand, "pages");
  assert.deepEqual(dashboard.interfaces.cli[0].arguments, {
    positional: ["overview"],
    options: { "--json": true },
  });
  assert.deepEqual(
    dashboard.interfaces.mcp.map((binding) => [binding.transport, binding.capability]),
    [["cli", "cao_catalog"], ["web", "cao_overview"]],
  );
  assert.equal(dashboard.recommendedInterface.default, "web-mcp");

  const catalog = JSON.parse(await readFile(path.join(dist, "agent-analysis", "index.json"), "utf8"));
  assert.deepEqual(
    catalog.interfaces.mcp.map((binding) => binding.arguments),
    [{ kind: "pages" }, { kind: "queries" }],
  );
});

test("consolidated guides retain section links and expose their canonical sources", async () => {
  const routes = {
    "architecture-at-a-glance": ["what-is-cao", "agentic-workflows-operated-across-repositories", "how-it-works", "a-simple-example", "get-started"],
    architecture: ["objectives", "authority-and-admission", "execution-boundaries", "detailed-references"],
    authentication: [
      "configure-your-profile", "public-only-monitoring-quickstart", "reuse-approved-apps",
      "automated-app-setup", "multiple-organizations-in-one-enterprise",
      "fine-grained-pat-fallback", "fine-grained-pat-fallback-1",
      "validate-before-activation", "validation", "credential-reference",
      "api-capacity", "rotation-and-incidents", "rotation-and-revocation",
    ],
    "control-plane-authentication": [
      "choose-a-profile", "reuse-approved-apps", "configure-private-github-apps",
      "enterprise-owned-apps", "configure-a-fine-grained-token", "validate-before-activation",
    ],
    "setup-one-organization": ["install", "configure-the-apps", "validate-and-commit"],
    "setup-multiple-organizations": ["install", "create-and-install-the-apps", "configure-the-control-repository", "validate-and-commit"],
    "setup-fine-grained-pat": ["install", "configure-the-tokens", "validate-and-commit"],
    "deployment-actions": ["choosing-a-credential-profile", "deploying-the-dashboard", "keeping-the-site-up-to-date", "publishing-from-an-existing-pages-workflow"],
    deployment: ["about-the-shared-data-flow", "what-every-option-guarantees"],
    "execution-and-safety": ["standard-control-envelope", "invariants", "failure-posture"],
    "rollout-and-routing": ["promotion-at-a-glance", "live-authority-check"],
    dashboard: ["know-the-boundary", "design-queries-in-a-copilot-canvas"],
    "dashboard-data-ingestion": ["canvas-administrative-actions", "hosted-server-data-pipeline", "choose-a-local-serving-mode"],
    "agent-plugin": ["install-the-plugin", "open-a-copilot-canvas", "design-queries-in-a-copilot-canvas", "run-approved-commands", "compiler-version-evidence"],
    "cao-cli": ["github-cli-extension", "guided-runtime-preparation", "control-plane-installer"],
  };
  const pages = new Map();
  async function readPage(url) {
    const file = path.join(fileForPublicUrl(url), "index.html");
    if (!pages.has(file)) pages.set(file, await readFile(file, "utf8"));
    return pages.get(file);
  }
  const resourceIndex = JSON.parse(await readFile(path.join(dist, "agent/resources.json"), "utf8"));

  for (const [route, anchors] of Object.entries(routes)) {
    const url = new URL(`${route}/`, publicBase);
    const html = await readPage(url);
    for (const anchor of anchors) {
      assert.ok(html.includes(`id="${anchor}"`), `${route} must retain #${anchor}`);
    }
    const summary = resourceIndex.resources.find((entry) => entry.id === route);
    assert.ok(summary, `${route} must be discoverable, not hidden by a redirect`);
    const resource = JSON.parse(await readFile(path.join(dist, route, "index.json"), "utf8"));
    assert.equal(resource.source.path, `docs/${route}.md`);

    const content = html.match(/<main\b[^>]*>([\s\S]*?)<\/main>/)?.[1];
    assert.ok(content, `${route} must render an article`);
    for (const [, href] of content.matchAll(/<a\b[^>]*\bhref="([^"]+)"/g)) {
      const target = new URL(href, url);
      if (target.origin !== publicBase.origin || !target.pathname.startsWith(publicBase.pathname)
          || target.pathname.startsWith(`${publicBase.pathname}cao/`)) continue;
      if (path.extname(target.pathname)) continue;
      const targetHtml = await readPage(target);
      if (target.hash) {
        const anchor = decodeURIComponent(target.hash.slice(1));
        assert.ok(targetHtml.includes(`id="${anchor}"`), `${route} links to missing ${href}`);
      }
    }
  }

  const navigationHtml = await readPage(new URL("dashboard/", publicBase));
  for (const route of ["control-plane-authentication", "agent-plugin"]) {
    assert.ok(navigationHtml.includes(`href="/gh-cao/${route}/"`), `${route} must be in navigation`);
  }
});

test("removed guides redirect to canonical sections without duplicate agent resources", async () => {
  const resourceIndex = JSON.parse(await readFile(path.join(dist, "agent/resources.json"), "utf8"));
  for (const [route, destination] of [
    ["how-cao-works", "architecture-at-a-glance/#how-it-works"],
    ["deployment-actions-github-app", "control-plane-authentication/#configure-private-github-apps"],
    ["deployment-actions-pat", "control-plane-authentication/#configure-a-fine-grained-token"],
  ]) {
    const html = await readFile(path.join(dist, route, "index.html"), "utf8");
    const target = new URL(destination, publicBase);
    assert.equal(hrefForRel(html, "canonical"), target.href, route);
    assert.ok(html.includes(`content="0;url=${target.pathname}${target.hash}"`), `${route} must redirect without JavaScript`);
    const targetHtml = await readFile(path.join(fileForPublicUrl(target), "index.html"), "utf8");
    assert.ok(targetHtml.includes(`id="${target.hash.slice(1)}"`), `${route} must resolve to a real section`);
    assert.ok(!resourceIndex.resources.some((entry) => entry.id === route), `${route} must not duplicate its replacement`);
    const canonicalRoute = destination.split("/")[0];
    assert.ok(resourceIndex.resources.some((entry) => entry.id === canonicalRoute), `${canonicalRoute} must remain discoverable`);
  }
});

test("specifications use the generated outline instead of a duplicate manual TOC", async () => {
  for (const route of ["dashboard-language-specification", "operational-observability-visualization-specification"]) {
    const html = await readFile(path.join(dist, route, "index.html"), "utf8");
    assert.ok(html.includes('id="table-of-contents"'), `${route} retains its legacy anchor`);
    assert.doesNotMatch(html, /<h[1-6]\b[^>]*\bid="table-of-contents"/);
    assert.ok(html.includes('href="#1-introduction"'), `${route} must have a generated outline`);
    assert.ok(html.includes('id="1-introduction"'), `${route} must retain specification sections`);
  }
});
