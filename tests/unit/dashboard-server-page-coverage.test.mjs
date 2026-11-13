import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { pageCapabilityAvailable } from "../../dashboard/site/src/view-availability.js";
import { serverDashboardPageExclusionReason } from "../e2e/dashboard-server-page-coverage.mjs";

test("server coverage excludes command pages only while they declare the local CLI capability", () => {
  const { pages } = JSON.parse(readFileSync(
    new URL("../../dashboard/site/dashboard-fragments/commands.json", import.meta.url), "utf8",
  ));
  assert.deepEqual(pages.map((page) => page.id), ["commands", "command"]);
  const previous = globalThis.__CLI_QUEUE__;
  try {
    for (const page of pages) {
      globalThis.__CLI_QUEUE__ = false;
      assert.equal(pageCapabilityAvailable(page), false);
      assert.match(serverDashboardPageExclusionReason(page), /local CLI queue/);
      globalThis.__CLI_QUEUE__ = true;
      assert.equal(pageCapabilityAvailable(page), true);

      const ungated = { ...page };
      delete ungated.requires;
      assert.equal(serverDashboardPageExclusionReason(ungated), undefined);
      assert.equal(pageCapabilityAvailable(ungated), true);
    }
  } finally {
    if (previous === undefined) delete globalThis.__CLI_QUEUE__;
    else globalThis.__CLI_QUEUE__ = previous;
  }
});

test("ordinary and parameterized pages remain covered regardless of their names or empty evidence", () => {
  for (const page of [
    { id: "overview" },
    { id: "commands" },
    { id: "command", route: { "hash-query-parameter": "command" } },
    { id: "workflow", route: { "availability-view": "workflow-details" } },
    { id: "campaigns", requires: { source: "campaign-inventory" } },
  ]) {
    assert.equal(serverDashboardPageExclusionReason(page), undefined);
  }
});

test("server coverage excludes Governance only while it declares the admin capability", () => {
  const { pages: [page] } = JSON.parse(readFileSync(
    new URL("../../dashboard/site/dashboard-fragments/governance.json", import.meta.url), "utf8",
  ));
  assert.match(serverDashboardPageExclusionReason(page), /authorized hosted administrator/);
  const ungated = { ...page };
  delete ungated.requires;
  assert.equal(serverDashboardPageExclusionReason(ungated), undefined);
});

test("new or malformed capability requirements fail closed instead of silently skipping coverage", () => {
  for (const requires of [null, {}, [], "cli-queue", { source: "" }, { capability: "future-feature", source: "campaigns" }]) {
    assert.throws(
      () => serverDashboardPageExclusionReason({ id: "future-page", requires }),
      /Unknown server dashboard page requirement: future-page/,
    );
  }
});
