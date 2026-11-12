import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createWriteStream } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { finished } from "node:stream/promises";
import test from "node:test";

test("canonical normalization streams one million cached JSONL rows within a 32 MB JavaScript heap", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "cao-jsonl-load-"));
  const input = path.join(directory, "activity-shard.jsonl");
  const output = createWriteStream(input);
  const row = `${JSON.stringify({
    schema_version: 2,
    kind: "run",
    run: {
      run_id: 1,
      run_attempt: 1,
      organization: "githubnext",
      repository: "gh-cao",
      workflow_name: "Stress",
      workflow_path: ".github/workflows/stress.md",
      status: "completed",
      created_at: "2026-01-01T00:00:00Z",
    },
  })}\n`;
  const chunk = row.repeat(100);
  for (let index = 0; index < 10_000; index += 1) {
    if (!output.write(chunk)) await new Promise((resolve) => output.once("drain", resolve));
  }
  output.end();
  await finished(output);

  try {
    const reader = spawnSync(process.execPath, [
      "--max-old-space-size=32", "--input-type=module", "-e",
      `import { readJsonlRecords } from './dashboard/report/jsonl.mjs';
       let records = 0;
       let checksum = 0;
       for (const { value } of readJsonlRecords(process.argv[1])) {
         records++;
         checksum += value.run.run_id;
       }
       console.log(JSON.stringify({ records, checksum }));`,
      input,
    ], { cwd: process.cwd(), encoding: "utf8", timeout: 15_000 });
    assert.equal(reader.signal, null, reader.stderr);
    assert.equal(reader.status, 0, reader.stderr);
    assert.deepEqual(JSON.parse(reader.stdout), { records: 1_000_000, checksum: 1_000_000 });

    const result = spawnSync(process.execPath, [
      "--max-old-space-size=32", "--input-type=module", "-e",
      `import { normalizeSnapshotPayloads } from './dashboard/report/normalized-payloads.mjs';
       import { readJsonlRecords } from './dashboard/report/jsonl.mjs';
       import path from 'node:path';
       const directory = process.argv[1];
       const hashes = await normalizeSnapshotPayloads({
         shardDirectory: directory,
         runsDirectory: path.join(directory, 'gh-aw-logs-runs'),
         recordsDirectory: path.join(directory, 'gh-aw-logs-records')
       });
       const counts = {};
       for (const name of Object.keys(hashes).filter(name => name.startsWith('gh-aw-logs-runs/'))) {
         for (const { value } of readJsonlRecords(path.join(directory, name))) {
           if (value.kind === 'record') counts[value.collection] = (counts[value.collection] ?? 0) + 1;
         }
       }
       console.log(JSON.stringify(counts));`,
      directory,
    ], {
      cwd: process.cwd(),
      encoding: "utf8",
      timeout: 30_000,
    });

    assert.equal(result.signal, null, result.stderr);
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout), { repositories: 1, workflows: 1, runs: 1 });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
