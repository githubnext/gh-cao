import assert from "node:assert/strict";
import { test } from "node:test";
import { createDashboardExtension } from "../../com.github.copilot/extensions/cao-dashboard/dashboard-extension.mjs";

test("dashboard extension exposes offline query tools without canvases or credentials", async () => {
  const { config } = createDashboardExtension();
  assert.equal(config.canvases, undefined);
  assert.equal(config.requestedEnvironmentVariables, undefined);
  assert.deepEqual(Object.keys(config), ["tools", "hooks"]);
  const query = config.tools.find(({ name }) => name === "cao_dashboard_execute_query");
  const output = JSON.parse(await query.handler({
    queries: [{ name: "failed", from: "runs", filter: { predicates: [{ field: "conclusion", equals: "failure" }] } }],
    sources: { runs: [{ conclusion: "failure" }, { conclusion: "success" }] },
  }));
  assert.deepEqual(output.failed.rows, [{ conclusion: "failure" }]);
  const specification = config.tools.find(({ name }) => name === "cao_dashboard_read_data_specification");
  const spec = JSON.parse(await specification.handler({ startLine: 1, endLine: 5 }));
  assert.equal(spec.startLine, 1);
  assert.equal(spec.endLine, 5);
  assert.match((await config.hooks.onSessionStart({})).additionalContext, /cao_dashboard_execute_query/);
});
