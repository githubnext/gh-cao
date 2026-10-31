import assert from "node:assert/strict";
import test from "node:test";
import { dashboardBenchmarkQueries } from "../helpers/dashboard-benchmark-queries.mjs";
import { authoritativeDashboard } from "../helpers/authoritative-dashboard.mjs";

test("benchmarks bind declared parameters using custom and built-in form defaults", () => {
  const dashboard = {
    pages: [
      { kind: "custom", form: { fields: [{ id: "filter", default: "" }] } },
      { kind: "built-in", definition: { form: { fields: [{ id: "minimum", default: 10 }] } } },
    ],
    queries: [{
      name: "filtered",
      from: "runs",
      parameters: [{ name: "filter", type: "string" }, { name: "minimum", type: "number" }],
      compute: [{
        as: "included",
        function: "equals-any",
        args: [{ parameter: "filter" }, { value: "" }],
      }, {
        as: "floor",
        function: "difference",
        args: [{ parameter: "minimum" }, { value: 1 }],
      }],
    }],
  };
  const original = structuredClone(dashboard);
  const [query] = dashboardBenchmarkQueries(dashboard);
  assert.deepEqual(query.compute.map(({ args }) => args[0]), [{ value: "" }, { value: 10 }]);
  assert.deepEqual(dashboard, original);
});

test("benchmarks fail closed on missing or conflicting form defaults", () => {
  assert.throws(() => dashboardBenchmarkQueries({
    queries: [{
      name: "missing",
      from: "runs",
      parameters: [{ name: "filter", type: "string" }],
      compute: [{ as: "value", function: "concat", args: [{ parameter: "filter" }] }],
    }],
  }), /requires form parameter "filter"/);
  assert.throws(() => dashboardBenchmarkQueries({
    queries: [],
    pages: ["one", "two"].map((defaultValue) => ({
      kind: "custom", form: { fields: [{ id: "filter", default: defaultValue }] },
    })),
  }), /conflicting form defaults/);
});

test("every deployed benchmark query has resolved operands", () => {
  const queries = dashboardBenchmarkQueries(authoritativeDashboard.dashboard);
  assert.equal(queries.length, authoritativeDashboard.dashboard.queries.length);
  for (const query of queries) {
    for (const computed of query.compute ?? []) {
      for (const operand of computed.args) {
        assert.equal(Object.hasOwn(operand, "parameter"), false, query.name);
      }
    }
  }
  const routing = queries.find(({ name }) => name === "routing-filter-candidates");
  assert.ok(routing);
  assert.deepEqual(routing.compute.find(({ as }) => as === "all-workflows").args[0], { value: "" });
  const database = queries.find(({ name }) => name === "simulator-database-inputs");
  assert.deepEqual(database.compute.find(({ as }) => as === "runs-daily").args[0], { value: 1000 });
  const company = queries.find(({ name }) => name === "simulator-pr-inputs");
  assert.deepEqual(company.compute.find(({ as }) => as === "prs-per-repo").args[1], { value: 10000 });
});
