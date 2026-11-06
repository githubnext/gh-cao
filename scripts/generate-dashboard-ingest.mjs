import { build } from "esbuild";
import { gunzipSync, gzipSync } from "node:zlib";
import { readFile, writeFile } from "node:fs/promises";

const { outputFiles } = await build({
  entryPoints: [new URL("../dashboard/report/hydrate-local-logs.mjs", import.meta.url).pathname],
  bundle: true,
  format: "iife",
  globalName: "CaoIngest",
  target: "es2017",
  minify: true,
  write: false,
});
const artifact = new URL("../internal/dashboardingest/normalize.generated.js.gz", import.meta.url);
const generated = gzipSync(outputFiles[0].contents);
if (process.argv.includes("--check")) {
  if (!Buffer.from(outputFiles[0].contents).equals(gunzipSync(await readFile(artifact)))) {
    throw new Error("Embedded log normalizer is stale; run npm run generate:dashboard-ingest.");
  }
} else {
  await writeFile(artifact, generated);
}
