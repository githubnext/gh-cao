import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { root, workflow } from "./workflow-contract.helpers.mjs";

test("retired deployment checks have no executable workflow or test entry point", () => {
  for (const path of [
    ".github/workflows/cao-mcp-remote.yml",
    ".github/workflows/self-care-hosted-health.md",
    ".github/workflows/self-care-hosted-health.lock.yml",
    "tests/e2e/cao-mcp-remote.mjs",
    "tests/integration/dashboard-hosted-backfill.test.mjs",
  ]) {
    assert.equal(existsSync(join(root, path)), false, `${path} must remain retired`);
  }
});

test("SelfCare dispatch stays live-only without the retired health worker", () => {
  const source = workflow("self-care.md");
  const manifest = readFileSync(join(root, "self-care/aw.yml"), "utf8");
  const campaign = JSON.parse(readFileSync(join(root, "self-care/cao.json"), "utf8"));

  assert.match(source, /only when its precomputed candidate mode is `live`/);
  assert.match(source, /Never promote a `review` candidate to `live`/);
  assert.match(source, /Never dispatch any worker in review mode or for another repository/);
  assert.doesNotMatch(source, /hosted-health/);
  assert.doesNotMatch(manifest, /hosted-health/);
  assert.equal(campaign.workers["hosted-health"], undefined);
  assert.equal(Object.keys(campaign.workers).length, 21);
});
