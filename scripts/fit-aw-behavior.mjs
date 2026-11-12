import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { writeFile } from 'node:fs/promises';
import { parseArgs } from 'node:util';
import { pathToFileURL } from 'node:url';
import { adaptCachedGhAwJsonlStream } from '../dashboard/site/src/data/adapters/gh-aw-logs.js';
import { normalize } from '../dashboard/site/src/data/normalize/index.js';
import { behaviorEvidence, fitBehaviorModel } from '../activity/empirical-behavior.mjs';

export async function main(args = process.argv.slice(2)) {
  const { values } = parseArgs({
    args,
    options: {
      input: { type: 'string' }, repo: { type: 'string' },
      since: { type: 'string' }, until: { type: 'string' },
      output: { type: 'string' }, help: { type: 'boolean' },
    },
  });
  if (values.help) {
    console.log(`Usage: node scripts/fit-aw-behavior.mjs --input PRIVATE_CACHE.jsonl --repo OWNER/REPO --since ISO --until ISO --output PRIVATE_MODEL.json

Acquire real evidence with gh aw logs --repo OWNER/REPO --artifacts usage --audit
--cached-jsonl PRIVATE_CACHE.jsonl --output PRIVATE_DIRECTORY, using explicit date,
run, API, timeout and storage bounds. Or use a retained authenticated Actions
artifact's raw cached JSONL. Never fit public synthetic Pages evidence.

The window is [since, until), based on run creation. The production gh-aw adapter,
normalizer and Dashboard Language engine select canonical evidence. Weights are
workflow-conditional, unsmoothed and descriptive. Missing observations stay unknown.
Joint cells support coarse resampling, not causal inference. Safe item counts are
not issue/PR batch sizes. Closure is not acceptance. Quiet-day rates require a
collection census and are deliberately unavailable.

Keep inputs and fitted operational models outside committed/public demo assets.
The output contains aggregate identifiers and weights, not raw runs or payloads.`);
    return;
  }
  for (const name of ['input', 'repo', 'since', 'until', 'output']) {
    if (!values[name]) throw new TypeError(`Missing --${name}`);
  }
  const hash = createHash('sha256');
  async function* hashedInput() {
    for await (const chunk of createReadStream(values.input)) {
      hash.update(chunk);
      yield chunk;
    }
  }
  const adapted = await adaptCachedGhAwJsonlStream(hashedInput());
  const scope = {
    repository: values.repo,
    since: values.since,
    until: values.until,
    inputSha256: hash.digest('hex'),
    selection: 'operator-selected cached JSONL; acquisition authenticity/completeness not attested by estimator',
  };
  const batch = normalize(adapted.observations);
  const evidence = behaviorEvidence(batch, scope);
  const model = fitBehaviorModel(evidence, scope);
  model.collection = {
    interpretation: 'input-wide adapter counters, including excluded runs; only model.runs is the fitted denominator',
    rawRuns: adapted.rawRuns, enrichedRuns: adapted.agenticRuns,
    duplicateEnrichedObservations: adapted.duplicateAgenticRunObservations,
    observedWindow: {
      first: evidence.runs[0].createdAt,
      last: evidence.runs.at(-1).createdAt,
    },
  };
  await writeFile(values.output, `${JSON.stringify(model, null, 2)}\n`, { flag: 'wx', mode: 0o600 });
  console.log(JSON.stringify({ output: values.output, runs: model.runs, workflows: model.workflows.length }));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
