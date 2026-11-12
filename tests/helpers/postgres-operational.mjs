import { execFileSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { existsSync } from "node:fs";
import path from "node:path";

export function operationalPostgres(t) {
  const endpoint = process.env.POSTGRES_URL;
  if (!endpoint) { t.skip("POSTGRES_URL is required for real PostgreSQL integration"); return; }
  const binary = path.resolve(process.env.DASHBOARD_SERVER_BINARY ?? ".tmp/cao-dashboard");
  if (!existsSync(binary)) throw new Error("Build .tmp/cao-dashboard before running PostgreSQL integration.");
  const url = new URL(endpoint);
  if (!["127.0.0.1", "[::1]"].includes(url.hostname)) throw new Error("Select an explicitly disposable numeric-loopback PostgreSQL instance.");
  const schema = "cao_node_" + randomBytes(10).toString("hex");
  const namespace = "node-test";
  const psql = (statement) => execFileSync("psql", ["-X", "--dbname", endpoint, "-v", "ON_ERROR_STOP=1", "-c", statement], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  psql("CREATE SCHEMA " + schema);
  t.after(() => psql("DROP SCHEMA " + schema + " CASCADE"));
  url.searchParams.set("search_path", schema);
  const dsn = url.href;
  execFileSync(binary, ["database", "bootstrap", "--store", "operational", "--namespace", namespace], {
    env: { ...process.env, CAO_DATABASE_ADMIN_URL: dsn, OTEL_SDK_DISABLED: "true" },
    stdio: ["ignore", "pipe", "pipe"],
  });
  const previous = {};
  for (const [name, value] of Object.entries({
    CAO_POSTGRES_URL: dsn, CAO_OPERATIONAL_NAMESPACE: namespace,
    CAO_DASHBOARD_EXECUTABLE: binary,
  })) {
    previous[name] = process.env[name];
    process.env[name] = value;
  }
  t.after(() => {
    for (const [name, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[name];
      else process.env[name] = value;
    }
  });
  return { binary, namespace, dsn };
}
