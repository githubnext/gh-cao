import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { createSqliteIndexedDB } from "../../dashboard/site/src/data/storage/sqlite-indexeddb.js";
import { upsertCanonicalBatch } from "../../dashboard/site/src/data/storage/indexeddb.js";
import { normalize } from "../../dashboard/site/src/data/normalize/index.js";
import { startCanvasServer as startPreparedCanvasServer } from "../../dashboard/canvas-server.mjs";

/**
 * @param {NonNullable<Parameters<typeof startPreparedCanvasServer>[0]> & {
 *   prepareSnapshot?: (destination: string, repository?: string, ghExecutable?: string, workingDirectory?: string) => Promise<unknown>
 * }} options
 */
export async function startCanvasServer({ prepareSnapshot, ...options } = {}) {
  if (!prepareSnapshot) return startPreparedCanvasServer(options);
  const directory = await mkdtemp(join(options.workingDirectory ?? process.cwd(), ".cao-fixture-"));
  try {
    const prepared = await prepareSnapshot(directory, options.repository, options.ghExecutable, options.workingDirectory);
    if (prepared && typeof prepared === "object" && "repository" in prepared && typeof prepared.repository === "string") {
      options.repository ??= prepared.repository;
    }
    const preview = await startPreparedCanvasServer({ ...options, snapshotDirectory: directory });
    return {
      ...preview,
      async close() {
        try { await preview.close(); }
        finally { await rm(directory, { recursive: true, force: true }); }
      },
    };
  } catch (error) {
    await rm(directory, { recursive: true, force: true });
    throw error;
  }
}

// Artifact fixtures use canonical records, not the removed logical-source HTTP transport.
export async function writeCanvasSnapshot(destination, records = {}) {
  await mkdir(destination, { recursive: true });
  const snapshot = join(destination, "gh-aw-logs.sqlite");
  await upsertCanonicalBatch(createSqliteIndexedDB(snapshot), {
    ...normalize([]), ...records,
  });
  const hash = createHash("sha256").update(await readFile(snapshot)).digest("hex");
  await writeFile(join(destination, "payload-hashes.json"), JSON.stringify({ "gh-aw-logs.sqlite": hash }));
  return snapshot;
}
