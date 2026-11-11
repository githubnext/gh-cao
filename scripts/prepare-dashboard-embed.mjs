import { cp, mkdir, readFile, rm } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const source = resolve(process.argv[2] ?? "dashboard/site/dist");
const resources = fileURLToPath(new URL("../server/internal/dashboardassets/resources/", import.meta.url));

for (const name of [
  "index.html",
  "dashboard.json",
  "src/agent/queries.generated.json",
  "src/agent/catalog.generated.json",
  "src/agent/mcp-contract.json",
  "src/data/queries/database.json",
]) {
  const content = await readFile(resolve(source, name), "utf8");
  if (!content.trim()) throw new Error(`Empty dashboard resource: ${name}`);
  if (name.endsWith(".json")) JSON.parse(content);
}
await mkdir(resources, { recursive: true });
await rm(resolve(resources, "site"), { recursive: true, force: true });
await cp(source, resolve(resources, "site"), { recursive: true });
