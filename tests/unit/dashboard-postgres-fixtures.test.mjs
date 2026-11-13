import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import test from "node:test";
import { promisify } from "node:util";
import { prepareDashboardPostgres, prepareDashboardPostgresTemplate } from "../helpers/dashboard-snapshot.mjs";

const execute = promisify(execFile);
const sql = async (endpoint, statement) => {
  const target = new URL(endpoint);
  const schema = target.searchParams.get("search_path");
  target.searchParams.delete("search_path");
  return (await execute("psql", ["-X", "--dbname", target.href,
    "-v", "ON_ERROR_STOP=1", "-At", "-c", statement], {
    env: { ...process.env, PGOPTIONS: `-c search_path=${schema}` },
  })).stdout.trim();
};

test("dashboard template clones preserve production stores and isolate fixture mutations", async (t) => {
  const template = await prepareDashboardPostgresTemplate();
  t.after(() => template.close());
  const first = await prepareDashboardPostgres({ template: template.postgresURL });
  t.after(() => first.close());
  const second = await prepareDashboardPostgres({ template: template.postgresURL });
  t.after(() => second.close());
  assert.notEqual(new URL(first.postgresURL).pathname, new URL(second.postgresURL).pathname);
  assert.equal(first.namespace, second.namespace);
  for (const fixture of [first, second]) {
    assert.equal(await sql(fixture.postgresURL,
      "SELECT namespace FROM cao_operational_namespaces"), "dashboard-test");
    assert.equal(await sql(fixture.postgresURL, "SELECT count(*) FROM repositories"), "0");
  }
  await sql(first.postgresURL, "CREATE TABLE fixture_marker (value text); INSERT INTO fixture_marker VALUES ('first')");
  assert.equal(await sql(first.postgresURL, "SELECT value FROM fixture_marker"), "first");
  assert.equal(await sql(second.postgresURL, "SELECT to_regclass('fixture_marker') IS NULL"), "t");
  await first.close();
  await first.close();
  assert.equal(await sql(second.postgresURL, "SELECT count(*) FROM repositories"), "0");
  const third = await prepareDashboardPostgres({ template: template.postgresURL });
  t.after(() => third.close());
  assert.equal(await sql(third.postgresURL, "SELECT to_regclass('fixture_marker') IS NULL"), "t");
});

test("dashboard fixtures reject a template on another server before creating a database", async () => {
  await assert.rejects(prepareDashboardPostgres({
    endpoint: "postgresql://postgres@127.0.0.1:5432/tests",
    template: "postgresql://postgres@127.0.0.1:5433/dashboard_template_" + "a".repeat(20)
      + "?search_path=dashboard_test_" + "b".repeat(20),
  }), /configured PostgreSQL test server/);
});
