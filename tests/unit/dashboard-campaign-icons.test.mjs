import assert from "node:assert/strict";
import { access, glob, readFile } from "node:fs/promises";
import test from "node:test";
import { parse } from "yaml";

import { parsePackageManifest } from "../../activity/marketplace.mjs";

const root = new URL("../../", import.meta.url);

test("every catalog package declares a supported marketplace Octicon", async () => {
  const iconNames = new Set(JSON.parse(await readFile(
    new URL("dashboard/site/src/octicon-names.json", root), "utf8",
  )));
  let packageCount = 0;

  for await (const manifestPath of glob(["aw.yml", "*/aw.yml"], { cwd: root })) {
    const source = await readFile(new URL(manifestPath, root), "utf8");
    const manifest = parse(source);
    assert.equal(typeof manifest.icon, "string", `${manifestPath} must declare a marketplace icon`);
    const iconName = manifest.icon.match(/^:([a-z0-9]+(?:-[a-z0-9]+)*):$/)?.[1];
    assert.ok(iconName, `${manifestPath} must use gh-aw's colon-wrapped Octicon syntax`);
    assert.ok(iconNames.has(iconName), `${manifestPath} must use a supported Octicon`);

    const resolved = parsePackageManifest(source, {
      registryId: "official",
      registryName: "Official CAO catalog",
      precedence: 0,
      repository: "githubnext/gh-cao",
      path: manifestPath,
      ref: "main",
      resolvedCommit: "a".repeat(40),
    });
    assert.equal(resolved.icon, iconName, `${manifestPath} must normalize its marketplace Octicon name`);
    packageCount += 1;
  }

  assert.ok(packageCount > 0, "catalog package manifests must be discovered");
});

test("configured campaign icons are used by campaign dashboard menu entries when present", async () => {
  const policy = JSON.parse(await readFile(new URL(".github/workflows/cao.json", root), "utf8"));
  const campaigns = policy["control-plane"].campaigns;

  for (const [campaignName, campaignPolicy] of Object.entries(campaigns)) {
    if (["activity", "dashboard"].includes(campaignName)) continue;
    assert.equal(typeof campaignPolicy.icon, "string", `${campaignName} must configure an Octicon`);
    const manifest = parse(await readFile(new URL(`${campaignName}/aw.yml`, root), "utf8"));
    assert.equal(
      manifest.icon,
      `:${campaignPolicy.icon}:`,
      `${campaignName} marketplace icon must match its configured campaign icon`,
    );
    const dashboardUrl = new URL(`${campaignName}/dashboard.json`, root);
    const exists = await access(dashboardUrl).then(() => true, (error) => {
      if (error?.code === "ENOENT") return false;
      throw error;
    });
    if (!exists) continue;
    const campaignDashboard = JSON.parse(await readFile(dashboardUrl, "utf8"));
    assert.equal(
      campaignDashboard.dashboard.pages[0]?.icon,
      campaignPolicy.icon,
      `${campaignName} dashboard menu must use its configured Octicon`,
    );
  }
});
