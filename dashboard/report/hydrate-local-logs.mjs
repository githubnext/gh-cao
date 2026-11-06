import { adaptCachedGhAwJsonl, adaptGhAwLogs } from "../site/src/data/adapters/gh-aw-logs.js";
import { normalize } from "../site/src/data/normalize/index.js";
import { parseUnifiedSessionDocument } from "../site/src/data/adapters/unified-session.js";

export function hydrate(input) {
  const envelopes = input.jsonl.trim().split('\n').filter(Boolean).map((line) => JSON.parse(line));
  const legacy = [];
  for (const artifacts of input.artifacts ?? []) {
    const session = artifacts.files.find((file) => /(^|\/)aw_session\.jsonl$/.test(file.path));
    if (session) {
      const parent = envelopes.find((envelope) => envelope.kind === 'run'
        && String(envelope.run.run_id) === String(artifacts.run.githubRunId));
      if (!parent) throw new TypeError('Unified session has no selected parent run');
      parent.run.cao_unified_session = {
        version: 1, path: session.path,
        ...parseUnifiedSessionDocument(session.content, session.path, {
          runId: artifacts.run.githubRunId, attempt: artifacts.run.attempt,
        }),
      };
    }
    legacy.push({ unified: Boolean(session), observations: adaptGhAwLogs(artifacts).observations });
  }
  let { observations } = adaptCachedGhAwJsonl(envelopes.map((envelope) => JSON.stringify(envelope)).join('\n'));
  for (const artifact of legacy) {
    const additions = artifact.unified
      ? artifact.observations.filter((observation) =>
        ['experiment', 'experiment-assignment', 'grader', 'grader-observation', 'eval', 'eval-observation', 'issue'].includes(observation.kind)
        || observation.kind === 'audit' && ['grader.result', 'eval.result', 'safe_output.result'].includes(observation.data.type))
      : artifact.observations;
    if (!artifact.unified) {
      const facets = new Set(additions.filter((observation) => ['tool', 'domain'].includes(observation.kind))
        .map((observation) => `${observation.data.runId}:${observation.kind}`));
      observations = observations.filter((observation) => !facets.has(`${observation.data.runId}:${observation.kind}`));
    }
    observations.push(...additions);
  }
  return JSON.stringify(normalize(observations));
}
