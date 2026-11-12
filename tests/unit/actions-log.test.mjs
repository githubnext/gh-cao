import assert from "node:assert/strict";
import test from "node:test";
import { actionsLog } from "../../.github/workflows/shared/actions-log.mjs";

test("Actions log macros format messages and workflow commands", () => {
  const output = [];
  const originalLog = console.log;
  console.log = (line) => output.push(line);
  try {
    actionsLog.info`Collected ${3} records`;
    actionsLog.warning`partial%coverage
retry`;
    actionsLog.group`Build ${"dashboard"}`;
    actionsLog.endGroup();
  } finally {
    console.log = originalLog;
  }

  assert.deepEqual(output, [
    "Collected 3 records",
    "::warning::partial%25coverage%0Aretry",
    "::group::Build dashboard",
    "::endgroup::",
  ]);
});
