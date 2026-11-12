import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';

const repository = 'github/gh-aw';
const revision = '69065d46a566dc498776be6e948e15d5a52729c7';
const sourceDirectory = 'actions/setup/js';
const files = [
  'jsonl_helpers.cjs',
  'json_schema_validation.cjs',
  'mcp_scripts_validation.cjs',
  'memory_schema_contract.cjs',
];
const destination = path.resolve(import.meta.dirname, '../dashboard/report/vendor/gh-aw');
const sources = [...files.map((name) => `${sourceDirectory}/${name}`), 'LICENSE'];
const downloaded = sources.map((source) => {
  const response = JSON.parse(execFileSync('gh', [
    'api', `repos/${repository}/contents/${source}?ref=${revision}`,
  ], { encoding: 'utf8' }));
  if (response.encoding !== 'base64' || typeof response.content !== 'string') {
    throw new Error(`Unable to download ${source} at ${revision}`);
  }
  return [path.basename(source), Buffer.from(response.content, 'base64')];
});

mkdirSync(destination, { recursive: true });
const hashes = {};
for (const [name, content] of downloaded) {
  writeFileSync(path.join(destination, name), content);
  hashes[name] = createHash('sha256').update(content).digest('hex');
}
writeFileSync(path.join(destination, 'source.json'), `${JSON.stringify({
  repository, revision, sourceDirectory, files: hashes,
}, null, 2)}\n`);
