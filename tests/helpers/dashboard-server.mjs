import { execFile, spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join, relative, resolve, sep } from "node:path";
import { promisify } from "node:util";
import { bundleDashboardFiles } from "../../dashboard/report/bundle-dashboards.mjs";
import { prepareDashboardPostgres } from "./dashboard-snapshot.mjs";

const execute = promisify(execFile);
const root = resolve(import.meta.dirname, "../..");

/**
 * Real Go/PostgreSQL fixture; no Node serving, execution, or query callbacks.
 * @param {{
 *   workingDirectory?: string, siteRoot?: string, repository?: string, caoUrl?: string,
 *   prepareSnapshot: (destination: string) => Promise<unknown>, operatorConfig?: string,
 *   cliActions?: object[],
 * }} options
 */
export async function startDashboardServer({
  workingDirectory = root, siteRoot = join(root, "dashboard/site/dist"), repository, caoUrl,
  prepareSnapshot, operatorConfig, cliActions,
}) {
  const directory = await mkdtemp(join(workingDirectory, ".cao-server-fixture-"));
  const site = join(directory, "site");
  const source = join(directory, "source");
  const executable = process.env.CAO_DASHBOARD_EXECUTABLE ?? join(root, ".tmp/cao-dashboard");
  const token = randomBytes(24).toString("hex");
  const headers = { Authorization: `Bearer ${token}` };
  let postgres, child, exited, closing, failure;
  const close = () => {
    closing ??= (async () => {
      if (child) {
        child.stdin.end();
        const timer = setTimeout(() => child.kill("SIGKILL"), 10_000);
        try { await exited; } finally { clearTimeout(timer); }
      }
      await postgres?.close();
      await rm(directory, { recursive: true, force: true });
    })();
    return closing;
  };
  try {
    await cp(siteRoot, site, { recursive: true, filter: (path) =>
      !relative(siteRoot, path).split(sep).some((part) =>
        ["node_modules", "dist", ".tmp", "test", "test-results", "scripts"].includes(part)) });
    await bundleDashboardFiles(join(site, "dashboard.json"), [], join(siteRoot, "dashboard.json"));
    if (repository || cliActions) {
      const path = join(site, "dashboard.json");
      const document = JSON.parse(await readFile(path, "utf8"));
      if (repository) document.dashboard.repository = repository;
      if (cliActions) document.dashboard["cli-actions"] = cliActions;
      await writeFile(path, JSON.stringify(document));
    }
    if (caoUrl) {
      const path = join(site, "index.html");
      await writeFile(path, (await readFile(path, "utf8")).replace("</head>",
        `<meta name="cao-url" content="${caoUrl}"></head>`));
    }
    await mkdir(source);
    await prepareSnapshot(source);
    postgres = await prepareDashboardPostgres();
    const env = {
      ...Object.fromEntries(Object.entries(process.env)
        .filter(([name]) => !/^(?:CAO_|DASHBOARD_|GH_|GITHUB_|OTEL_)/.test(name))),
      CAO_POSTGRES_URL: postgres.postgresURL, CAO_OPERATIONAL_NAMESPACE: postgres.namespace,
      OTEL_SDK_DISABLED: "true",
    };
    const queryArgs = ["--database-queries", join(root, "dashboard/site/src/data/queries/database.json")];
    child = spawn(executable, [
      "serve", "--site", site, "--source", source, "--listen", "127.0.0.1:0",
      "--access-token", token, "--exit-on-stdin-close", "--namespace", postgres.namespace,
      ...queryArgs,
      "--dashboard-queries", join(root, "dashboard/site/src/agent/queries.generated.json"),
      ...(operatorConfig ? ["--operator-config", operatorConfig] : []),
    ], { env, stdio: ["pipe", "pipe", "pipe"] });
    child.stdin.on("error", (error) => { failure = error; });
    exited = new Promise((accept) => child.once("close", accept));
    const url = await new Promise((accept, reject) => {
      const timer = setTimeout(() => reject(new Error("Go dashboard fixture startup timed out.")), 30_000);
      let output = "";
      const inspect = (chunk) => {
        output = (output + chunk).slice(-16_384);
        const match = /serving local dashboard at (http:\/\/127\.0\.0\.1:\d+)/.exec(output);
        if (match) { clearTimeout(timer); accept(match[1]); }
      };
      child.stdout.on("data", inspect);
      child.stderr.on("data", inspect);
      child.once("error", (error) => { clearTimeout(timer); reject(error); });
      void exited.then(() => {
        clearTimeout(timer);
        reject(failure ?? new Error("Go dashboard fixture exited before readiness."));
      });
    });
    const ready = await fetch(`${url}/api/v1/health`, { headers });
    await ready.body?.cancel();
    if (!ready.ok) throw new Error("Go dashboard fixture readiness failed.");
    return {
      url, headers, close,
      async ingestSnapshot() {
        await execute(executable, ["ingest", "--source", source, "--namespace", postgres.namespace, ...queryArgs],
          { env, timeout: 120_000, maxBuffer: 1 << 20 });
      },
    };
  } catch (error) {
    await close();
    throw error;
  }
}
