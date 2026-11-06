import { createDebug, diagnosticErrorName } from '../../debug.js';
import { parseUnifiedSessionDocument } from './unified-session.js';

const debug = createDebug('grader-evidence');

/** @param {{ path: string, content: string }} file */
function document(file) {
  let value;
  try {
    value = JSON.parse(file.content);
  } catch (error) {
    if (!(error instanceof SyntaxError)) throw error;
    debug({ event: 'parse', status: 'invalid-json', error: diagnosticErrorName(error) });
    throw new TypeError('Artifact must contain valid grader JSON');
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)
      || ![1, 2].includes(value.version)) {
    throw new TypeError('Unsupported grader artifact document');
  }
  return value;
}

/**
 * Project already-downloaded results through the unified-session allowlist.
 * The caller verifies the enclosing artifact's repository, run and attempt.
 * Session results win; the downstream artifact may add custom results only.
 * @param {{ path: string, content: string }[]} files
 * @param {{ runId: string, attempt: number }} context
 * @param {{ results: Record<string, unknown>[] }} [session]
 * @returns {{ results: Record<string, unknown>[] } | undefined}
 */
export function parseGraderArtifacts(files, context, session) {
  const candidates = files.filter((file) => /(^|\/)grader_results\.json$/.test(file.path));
  const usage = candidates.filter((file) =>
    /(^|\/)(?:[^/]+-)?usage\/graders\/grader_results\.json$/.test(file.path));
  const separate = candidates.filter((file) => !usage.includes(file));
  if (separate.length > 1 || (!session && !separate.length && usage.length > 1)) {
    throw new TypeError('Multiple grader result artifacts found for one run');
  }
  const file = separate[0] ?? (session ? undefined : usage[0]);
  if (!file) return session;
  const output = document(file);
  if (!Array.isArray(output.results) || (output.run !== undefined
      && (!output.run || typeof output.run !== 'object' || Array.isArray(output.run)
        || (output.run.id !== undefined && output.run.id !== '' && String(output.run.id) !== context.runId)
        || (output.run.attempt !== undefined && Number(output.run.attempt) !== context.attempt)))) {
    throw new TypeError('Grader artifact run identity or results are invalid');
  }
  const ids = new Set();
  for (const result of output.results) {
    if (!result || typeof result !== 'object' || Array.isArray(result)
        || typeof result.id !== 'string' || !result.id.trim() || ids.has(result.id)
        || (result.value !== null && (typeof result.value !== 'number' || !Number.isFinite(result.value)))) {
      throw new TypeError('Grader artifact must contain unique identities and finite or null values');
    }
    ids.add(result.id);
  }
  const manifestPath = file.path.replace(/grader_results\.json$/, 'grader_manifest.json');
  const manifests = files.filter((candidate) => candidate.path === manifestPath);
  if (manifests.length > 1) throw new TypeError('Multiple grader manifests found for one artifact');
  const manifest = manifests[0] ? document(manifests[0]) : undefined;
  if (manifest && !Array.isArray(manifest.graders)) throw new TypeError('Invalid grader manifest');
  const events = [
    { type: 'session.format', data: { version: 1 } },
    ...(manifest ? [{ type: 'grader.manifest', data: manifest }] : []),
    { type: 'grader.result', data: {
      ...output,
      ...(output.run ? { run: { ...output.run, id: context.runId } } : {})
    } }
  ];
  const projected = parseUnifiedSessionDocument(
    events.map((event) => JSON.stringify(event)).join('\n'), file.path, context
  );
  const graders = /** @type {{ results: Record<string, unknown>[] }} */ (projected.graders);
  for (const result of graders.results) result.sessionPayloadRef = file.path;
  const retained = new Set(session?.results.map((result) => result.id));
  const additional = session
    ? graders.results.filter((result) =>
      ['inline', 'operational-value'].includes(String(result.source)) && !retained.has(result.id))
    : graders.results;
  debug({ event: 'project', results: additional.length, sessionResults: retained.size });
  return { results: [...(session?.results ?? []), ...additional] };
}
