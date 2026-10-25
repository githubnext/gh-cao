import { lstat, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const readJson = async (file) => JSON.parse(await readFile(file, 'utf8'));
const scope = (settings, field, fallbackField = field) => {
  const entries = settings[field] === undefined ? settings[fallbackField] : settings[field];
  if (!Array.isArray(entries) || entries.some((entry) => typeof entry !== 'string')) {
    throw new Error(`Recycle-only requires a valid ${field} snapshot`);
  }
  return JSON.stringify([...new Set(entries.map((entry) => entry.toLowerCase()))].sort());
};

export async function verifyRecycleSnapshot(directory, currentSettingsPath) {
  const [retained, current, inventory] = await Promise.all([
    readJson(path.join(directory, 'control-settings.json')),
    readJson(currentSettingsPath),
    readJson(path.join(directory, 'inventory-sources.json'))
  ]);
  if (current.policy_resolution?.status !== 'available') {
    throw new Error('Recycle-only requires an available current control policy');
  }
  for (const field of ['allowed_owners', 'allowed_repositories']) {
    if (scope(retained, field) !== scope(current, field)) {
      throw new Error('Recycle-only cannot reuse a snapshot with a different repository scope; run normal collection');
    }
  }
  if (scope(retained, 'activity_repositories', 'allowed_repositories')
      !== scope(current, 'activity_repositories', 'allowed_repositories')
      || Array.isArray(retained.activity_repositories) !== Array.isArray(current.activity_repositories)
      || (retained.activity_public_only === true) !== (current.activity_public_only === true)) {
    throw new Error('Recycle-only cannot reuse a snapshot with a different Activity collection scope; run normal collection');
  }
  for (const name of ['repositories', 'workflows', 'campaigns']) {
    if (!Array.isArray(inventory[name]?.rows)) throw new Error(`Recycle-only requires cached ${name} inventory`);
  }
  const shards = path.join(directory, 'gh-aw-logs-shards');
  const info = await lstat(shards);
  if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Recycle-only requires a non-symlink raw shard directory');
  const files = (await readdir(shards)).filter((name) => name.endsWith('.jsonl'));
  if (files.length === 0) throw new Error('Recycle-only requires restored raw evidence; no cache will be published');
  return { command: 'verify-recycle-snapshot', shards: files.length };
}

export async function resetRecycleProjection(directory, { repository = process.env.GITHUB_REPOSITORY } = {}) {
  for (const suffix of ['', '-wal', '-shm', '-journal']) {
    await rm(path.join(directory, `gh-aw-logs.sqlite${suffix}`), { force: true });
  }
  for (const phase of ['runs', 'records', 'normalized']) {
    await rm(path.join(directory, `gh-aw-logs-${phase}`), { recursive: true, force: true });
  }
  await rm(path.join(directory, 'payload-hashes.json'), { force: true });
  const shards = path.join(directory, 'gh-aw-logs-shards');
  if (!(await readdir(shards)).some((name) => name.endsWith('.jsonl'))) {
    if (typeof repository !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/.test(repository)) {
      throw new Error('Recycle-only requires GITHUB_REPOSITORY to represent an empty retained snapshot');
    }
    await writeFile(path.join(shards, 'empty.jsonl'), `${JSON.stringify({
      schema_version: 2, kind: 'workflow_runs', request: { repository }, payload: []
    })}\n`, { flag: 'wx' });
  }
  const manifest = path.join(directory, 'memory/manifest.json');
  try {
    const memory = await readJson(manifest);
    if (memory?.version !== 1 || !Array.isArray(memory.campaigns)) throw new Error('Invalid cached repository-memory manifest');
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    await mkdir(path.dirname(manifest), { recursive: true });
    await writeFile(manifest, `${JSON.stringify({ version: 1, generatedAt: new Date().toISOString(), campaigns: [] })}\n`, { flag: 'wx' });
  }
  return { command: 'reset-recycle-projection', rebuiltFromRetainedEvidence: true };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [command, directory, settings] = process.argv.slice(2);
  if (!directory || !['verify', 'reset'].includes(command) || command === 'verify' && !settings) {
    throw new Error('Usage: recycle-snapshot.mjs verify DIRECTORY SETTINGS | reset DIRECTORY');
  }
  console.log(await (command === 'verify'
    ? verifyRecycleSnapshot(directory, settings)
    : resetRecycleProjection(directory)));
}
