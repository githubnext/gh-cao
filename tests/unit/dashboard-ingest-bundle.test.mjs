import { execFileSync } from "node:child_process";
import test from "node:test";

test("native ingestion embeds the current production gh-aw adapter and canonical normalizer", () => {
  execFileSync(process.execPath, ["scripts/generate-dashboard-ingest.mjs", "--check"], {
    cwd: new URL("../..", import.meta.url),
    timeout: 30_000,
    stdio: "pipe",
  });
});
