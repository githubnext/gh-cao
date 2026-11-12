import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { glob } from "node:fs/promises";
import test from "node:test";
import { parse } from "yaml";

const campaignDetail = readFileSync(
  new URL("../../docs/pages/catalog/[slug].astro", import.meta.url),
  "utf8",
);
const campaignReadmeContent = readFileSync(
  new URL("../../docs/components/CampaignReadmeContent.astro", import.meta.url),
  "utf8",
);

test("catalog discovers canonical campaigns without the obsolete experimental bundle", async () => {
  const catalog = readFileSync(new URL("../../docs/lib/catalog.ts", import.meta.url), "utf8");
  assert.match(catalog, /import\.meta\.glob<string>\("\.\.\/\.\.\/\*\/aw\.\{yml,yaml\}"/);
  assert.match(catalog, /import\.meta\.glob<CampaignReadme>\("\.\.\/\.\.\/\*\/README\.md"/);
  assert.doesNotMatch(catalog, /\.experimental\//);

  const root = new URL("../../", import.meta.url);
  const optimization = [];
  for await (const path of glob("*/aw.{yml,yaml}", { cwd: root })) {
    const manifest = parse(readFileSync(new URL(path, root), "utf8"));
    if (manifest.name === "Optimization") optimization.push(path);
  }
  assert.deepEqual(optimization, ["optimization/aw.yml"]);
  const manifest = parse(readFileSync(new URL(optimization[0], root), "utf8"));
  for (const include of manifest.includes) {
    const path = include.startsWith(".github/") ? include : `optimization/${include}`;
    assert.ok(existsSync(new URL(path, root)), `${path} must remain installable`);
  }
});

test("campaign detail keeps the embedded README title out of the page heading outline", () => {
  assert.match(campaignDetail, /<CampaignReadmeContent>\s*<ReadmeContent \/>\s*<\/CampaignReadmeContent>/);
  assert.match(campaignReadmeContent, /content\.replace\(\/<h1\\b\[\^>\]\*>\[\\s\\S\]\*\?<\\\/h1>\/, ""\)/);
  assert.doesNotMatch(campaignDetail, /\.campaign-readme :global\(h1\)/);
});

test("campaign detail installs the latest release without a version placeholder", () => {
  assert.match(
    campaignDetail,
    /const installCommand = `gh aw add githubnext\/gh-cao\/\$\{entry\.slug\}`;/,
  );
  assert.doesNotMatch(campaignDetail, /catalog-release/);
});

test("campaign detail code examples scroll without widening the page", () => {
  assert.match(
    campaignDetail,
    /\.campaign-guide :global\(pre\),\s*\.install-campaign pre \{[\s\S]*?max-width: 100%;[\s\S]*?overflow-x: auto;/,
  );
});
