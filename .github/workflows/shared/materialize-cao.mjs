import { chmodSync, cpSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { gunzipSync } from 'node:zlib';
import { createHash } from 'node:crypto';

const repository = 'githubnext/gh-cao';
const rootPackageNames = [`${repository}/control-plane`, repository];
const runtimeWaitArray = new Int32Array(new SharedArrayBuffer(4));
const rootResources = [
  'activity',
  'dashboard',
  '.github/actions/setup-cao-runtime',
  '.github/actions/setup-gh-aw',
  '.github/cao/instructions.md',
  '.github/workflows/shared/activity-cache.md',
  '.github/workflows/shared/control.md',
  '.github/workflows/shared/operation.md',
  '.github/workflows/shared/review-bundle.md',
];
const retiredLauncherDigests = new Set([
  'c55be5e923b46a247eeee6d8c2f0926883f469e9577b4ac4b99d7171972e4c4d',
  'f186ab9735fb4d2a44333a81eac9ed18638ae2c4304af37ab351cc39ecf983c3'
]);

function removeRetiredLauncher(repositoryRoot) {
  const filename = path.join(repositoryRoot, 'cao.sh');
  const entry = lstatSync(filename, { throwIfNoEntry: false });
  if (!entry?.isFile()) return;
  const digest = createHash('sha256').update(readFileSync(filename)).digest('hex');
  if (retiredLauncherDigests.has(digest)) rmSync(filename);
}

function validateCampaign(campaign) {
  if (!/^(?:root|activity|dashboard|[a-z0-9-]+)$/.test(campaign)) {
    throw new Error(`Invalid CAO campaign name: ${campaign}`);
  }
}

function packageName(record) {
  if (typeof record.package === 'string' && record.package.trim()) return record.package.trim();
  if (typeof record.campaign === 'string' && record.campaign.trim()) return record.campaign.trim();
  if (typeof record.source === 'string') return record.source.split('@')[0].trim();
  return '';
}

function installedRecords(root) {
  const records = [];
  for (const directory of ['packages', 'campaigns']) {
    const recordDirectory = path.join(root, '.github', 'aw', directory);
    let entries;
    try {
      entries = readdirSync(recordDirectory, { withFileTypes: true });
    } catch (error) {
      if (error?.code === 'ENOENT') continue;
      throw error;
    }
    for (const entry of entries) {
      if (!entry.isFile() || !entry.name.endsWith('.json')) continue;
      const record = JSON.parse(readFileSync(path.join(recordDirectory, entry.name), 'utf8'));
      const name = packageName(record);
      if (name === repository || name.startsWith(`${repository}/`)) records.push({ name, record });
    }
  }
  return records;
}

function resolvedRevision(record) {
  const revision = typeof record.resolvedCommit === 'string' ? record.resolvedCommit.trim() : '';
  if (!/^[0-9a-f]{40}$/i.test(revision)) {
    throw new Error(`CAO package record must contain a full resolvedCommit SHA: ${revision || '(missing)'}`);
  }
  return revision.toLowerCase();
}

function exactRecordFor(records, campaign) {
  if (campaign === 'root') {
    return rootPackageNames
      .map((name) => records.find((candidate) => candidate.name === name)?.record)
      .find(Boolean);
  }
  return records.find((candidate) => candidate.name === `${repository}/${campaign}`)?.record;
}

function recordFor(records, campaign) {
  const name = campaign === 'root' ? repository : `${repository}/${campaign}`;
  const exact = exactRecordFor(records, campaign);
  if (exact) return exact;
  if (campaign === 'activity' || campaign === 'dashboard') {
    const root = exactRecordFor(records, 'root');
    if (root) return root;
  }
  throw new Error(`No installed CAO package record found for ${name}`);
}

export function installedCaoRevision(campaign = 'root', repositoryRoot = process.cwd()) {
  validateCampaign(campaign);
  return resolvedRevision(recordFor(installedRecords(repositoryRoot), campaign));
}

async function downloadArchive(revision, destination) {
  const response = await fetch(`https://codeload.github.com/${repository}/tar.gz/${encodeURIComponent(revision)}`);
  if (!response.ok) throw new Error(`Unable to download CAO ${revision}: HTTP ${response.status}`);
  writeFileSync(destination, Buffer.from(await response.arrayBuffer()));
}

function archiveRoot(directory) {
  const entries = readdirSync(directory, { withFileTypes: true }).filter((entry) => entry.isDirectory());
  if (entries.length !== 1) throw new Error('CAO archive did not contain exactly one repository root');
  return path.join(directory, entries[0].name);
}

function tarString(buffer, start, length) {
  return buffer.subarray(start, start + length).toString('utf8').replace(/\0.*$/, '');
}

function tarSize(buffer) {
  const value = tarString(buffer, 124, 12).trim();
  if (!/^[0-7]*$/.test(value)) throw new Error('CAO archive has an invalid entry size');
  return Number.parseInt(value || '0', 8);
}

function tarDestination(directory, entry) {
  entry = entry.replace(/\/+$/, '');
  if (!entry || entry.startsWith('/') || entry.split('/').some((part) => part === '' || part === '.' || part === '..')) {
    throw new Error(`CAO archive has an unsafe entry path: ${JSON.stringify(entry)}`);
  }
  const destination = path.resolve(directory, ...entry.split('/'));
  if (!destination.startsWith(`${path.resolve(directory)}${path.sep}`)) {
    throw new Error(`CAO archive has an unsafe entry path: ${JSON.stringify(entry)}`);
  }
  return destination;
}

function paxAttributes(data) {
  const attributes = {};
  let offset = 0;
  while (offset < data.length) {
    const separator = data.indexOf(0x20, offset);
    const length = Number.parseInt(data.subarray(offset, separator).toString('utf8'), 10);
    if (!Number.isSafeInteger(length) || length <= 0 || offset + length > data.length) {
      throw new Error('CAO archive has an invalid PAX header');
    }
    const record = data.subarray(separator + 1, offset + length - 1).toString('utf8');
    const equals = record.indexOf('=');
    if (equals > 0) attributes[record.slice(0, equals)] = record.slice(equals + 1);
    offset += length;
  }
  return attributes;
}

export function extractCaoArchive(archive, directory, native = process.platform === 'win32') {
  if (!native) {
    const tar = spawnSync('tar', ['-xzf', archive, '-C', directory], { encoding: 'utf8' });
    if (tar.error || tar.status !== 0) {
      throw new Error((tar.stderr || tar.error?.message || 'tar failed').trim());
    }
    return;
  }

  const data = gunzipSync(readFileSync(archive));
  let offset = 0;
  let nextPath;
  while (offset + 512 <= data.length) {
    const header = data.subarray(offset, offset + 512);
    if (header.every((byte) => byte === 0)) break;
    const size = tarSize(header);
    const payloadStart = offset + 512;
    const payloadEnd = payloadStart + size;
    if (payloadEnd > data.length) throw new Error('CAO archive ended before an entry was complete');
    const type = String.fromCharCode(header[156] || 0);
    const payload = data.subarray(payloadStart, payloadEnd);
    if (type === 'x' || type === 'g') {
      const attributes = paxAttributes(payload);
      if (attributes.path) nextPath = attributes.path;
    } else if (type === 'L') {
      nextPath = payload.toString('utf8').replace(/\0.*$/, '');
    } else {
      const entry = nextPath || [tarString(header, 345, 155), tarString(header, 0, 100)].filter(Boolean).join('/');
      nextPath = undefined;
      const destination = tarDestination(directory, entry);
      if (type === '5') {
        mkdirSync(destination, { recursive: true });
      } else if (type === '\0' || type === '0') {
        const encodedMode = tarString(header, 100, 8).trim();
        if (!/^[0-7]*$/.test(encodedMode)) throw new Error('CAO archive has an invalid file mode');
        const mode = 0o644 | (Number.parseInt(encodedMode || '0', 8) & 0o111);
        mkdirSync(path.dirname(destination), { recursive: true });
        writeFileSync(destination, payload, { mode });
        chmodSync(destination, mode);
      } else {
        throw new Error(`CAO archive has unsupported entry type: ${type}`);
      }
    }
    offset = payloadStart + Math.ceil(size / 512) * 512;
  }
}

function copyResource(sourceRoot, repositoryRoot, resource) {
  const source = path.join(sourceRoot, ...resource.split('/'));
  const destination = path.join(repositoryRoot, ...resource.split('/'));
  rmSync(destination, { force: true, recursive: true });
  cpSync(source, destination, { force: true, recursive: true });
}

function copyTrackedResource(sourceRoot, repositoryRoot, resource) {
  const listing = spawnSync('git', ['-C', sourceRoot, 'ls-files', '-z', '--', resource], {
    encoding: 'utf8',
  });
  if (listing.error || listing.status !== 0) {
    throw new Error(
      `Unable to list tracked CAO source files for ${resource}: `
      + `${(listing.stderr || listing.error?.message || `exit ${listing.status}`).trim()}`,
    );
  }
  const files = listing.stdout.split('\0').filter(Boolean);
  if (files.length === 0) {
    throw new Error(`CAO source resource has no tracked files: ${resource}`);
  }
  rmSync(path.join(repositoryRoot, ...resource.split('/')), { force: true, recursive: true });
  for (const file of files) {
    const source = path.join(sourceRoot, ...file.split('/'));
    const destination = path.join(repositoryRoot, ...file.split('/'));
    mkdirSync(path.dirname(destination), { recursive: true });
    cpSync(source, destination, { force: true });
  }
}

function materializeCampaignIntelligence(campaign, repositoryRoot) {
  const source = path.join(repositoryRoot, campaign, 'intelligence.json');
  const destination = path.join(repositoryRoot, '.github', 'cao', 'intelligence', `${campaign}.json`);
  rmSync(destination, { force: true });
  if (!existsSync(source)) return;
  mkdirSync(path.dirname(destination), { recursive: true });
  cpSync(source, destination, { force: true });
}

function validateResources(sourceRoot, resources) {
  for (const resource of resources) {
    const source = path.join(sourceRoot, ...resource.split('/'));
    try {
      statSync(source);
    } catch (error) {
      if (error?.code === 'ENOENT') {
        throw new Error(`CAO source revision is missing required resource: ${resource}`);
      }
      throw error;
    }
  }
}

export function packCaoRuntime(sourceRoot, destination, { tracked = false, prepare } = {}) {
  const manifestPath = path.join(sourceRoot, 'runtime', 'package.json');
  validateResources(sourceRoot, ['runtime/package.json']);
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
  if (!Array.isArray(manifest.files) || manifest.files.some((file) => (
    typeof file !== 'string' || file.split('/').some((part) => part === '.' || part === '..')
    || !rootResources.some((resource) => (
      file === resource || file.startsWith(`${resource}/`)
    ))
  ))) {
    throw new Error('CAO runtime package must declare files within its owned runtime destinations');
  }
  validateResources(sourceRoot, [...rootResources, ...manifest.files]);
  const staging = path.join(destination, 'source');
  const unpacked = path.join(destination, 'unpacked');
  mkdirSync(staging, { recursive: true });
  mkdirSync(unpacked, { recursive: true });
  for (const resource of rootResources) {
    if (tracked) copyTrackedResource(sourceRoot, staging, resource);
    else copyResource(sourceRoot, staging, resource);
  }
  cpSync(manifestPath, path.join(staging, 'package.json'));
  const npmArguments = [
    'pack', '--ignore-scripts', '--json', '--pack-destination', destination,
    '--cache', path.join(destination, 'cache'),
  ];
  if (process.platform === 'win32') {
    npmArguments.unshift(process.env.npm_execpath ?? path.join(path.dirname(process.execPath), 'node_modules', 'npm', 'bin', 'npm-cli.js'));
  }
  const pack = (cwd) => {
    const result = spawnSync(process.platform === 'win32' ? process.execPath : 'npm', npmArguments, {
      cwd, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024,
    });
    if (result.error || result.status !== 0) {
      throw new Error(`Unable to pack CAO runtime: ${(result.stderr || result.error?.message || `exit ${result.status}`).trim()}`);
    }
    const [packed] = JSON.parse(result.stdout);
    if (!packed?.filename || path.basename(packed.filename) !== packed.filename) {
      throw new Error('npm pack did not produce a valid CAO runtime archive');
    }
    return packed;
  };
  let packed = pack(staging);
  extractCaoArchive(path.join(destination, packed.filename), unpacked);
  const packedRoot = archiveRoot(unpacked);
  validateResources(packedRoot, [...rootResources, ...manifest.files]);
  if (prepare) {
    prepare(packedRoot);
    packed = pack(packedRoot);
  }
  return {
    sourceRoot: packedRoot,
    archive: path.join(destination, packed.filename),
    files: packed.files.map(({ path }) => path),
  };
}

function runtimeFiles(directory, prefix = '') {
  return readdirSync(path.join(directory, prefix), { withFileTypes: true }).flatMap((entry) => {
    const file = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (entry.isDirectory()) return runtimeFiles(directory, file);
    if (!entry.isFile()) throw new Error(`CAO runtime contains a non-regular file: ${file}`);
    return [file];
  }).sort();
}

export function sealCaoRuntime(sourceRoot, revision) {
  if (!/^[0-9a-f]{40}$/.test(revision)) throw new Error('CAO runtime requires a full lowercase revision SHA');
  const files = runtimeFiles(sourceRoot).filter((file) => file !== 'package.json' && file !== 'cao-runtime.json');
  const metadata = {
    schemaVersion: 1, repository, revision,
    files: Object.fromEntries(files.map((file) => [
      file, createHash('sha256').update(readFileSync(path.join(sourceRoot, file))).digest('hex'),
    ])),
  };
  writeFileSync(path.join(sourceRoot, 'cao-runtime.json'), `${JSON.stringify(metadata, null, 2)}\n`);
  const manifestPath = path.join(sourceRoot, 'package.json');
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
  manifest.files = [...manifest.files, 'cao-runtime.json'];
  writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
}

export function validateCaoRuntimeArtifact(sourceRoot, revision) {
  validateResources(sourceRoot, ['cao-runtime.json', ...rootResources]);
  const metadata = JSON.parse(readFileSync(path.join(sourceRoot, 'cao-runtime.json'), 'utf8'));
  if (metadata.schemaVersion !== 1 || metadata.repository !== repository || metadata.revision !== revision) {
    throw new Error(`CAO runtime artifact provenance does not match ${repository}@${revision}`);
  }
  if (!metadata.files || typeof metadata.files !== 'object' || Array.isArray(metadata.files)) {
    throw new Error('CAO runtime artifact has no valid file inventory');
  }
  const files = runtimeFiles(sourceRoot).filter((file) => file !== 'package.json' && file !== 'cao-runtime.json');
  if (JSON.stringify(files) !== JSON.stringify(Object.keys(metadata.files).sort())) {
    throw new Error('CAO runtime artifact file inventory does not match its contents');
  }
  for (const file of files) {
    if (file !== 'cao.sh' && !rootResources.some((resource) => file === resource || file.startsWith(`${resource}/`))) {
      throw new Error(`CAO runtime artifact contains an unowned path: ${file}`);
    }
    const digest = createHash('sha256').update(readFileSync(path.join(sourceRoot, file))).digest('hex');
    if (metadata.files[file] !== digest) throw new Error(`CAO runtime artifact checksum mismatch: ${file}`);
  }
  for (const bundle of ['activity', 'dashboard']) verifyCaoRuntime(bundle, sourceRoot);
}

function gh(arguments_) {
  const result = spawnSync('gh', arguments_, {
    encoding: 'utf8', maxBuffer: 16 * 1024 * 1024, timeout: 120_000,
  });
  if (result.error || result.status !== 0) {
    throw new Error(`Unable to acquire or verify prebuilt CAO runtime: ${(result.stderr || result.error?.message || `gh exited ${result.status}`).trim()}`);
  }
  return result.stdout;
}

function githubApi(endpoint) {
  return JSON.parse(gh(['api', '--hostname', 'github.com', '--paginate', '--slurp', endpoint]));
}

export function downloadCaoRuntimeArtifact(record, directory, {
  attempts = 60,
  intervalMs = 5_000,
  wait = (milliseconds) => Atomics.wait(runtimeWaitArray, 0, 0, milliseconds),
} = {}) {
  const revision = resolvedRevision(record);
  const name = `cao-runtime-${revision}`;
  mkdirSync(directory, { recursive: true });
  const ref = typeof record.source === 'string' ? record.source.split('@')[1] : '';
  if (/^v\d+\.\d+\.\d+$/.test(ref ?? '')) {
    gh(['release', 'download', ref, '--repo', `github.com/${repository}`, '--pattern', `${name}.tgz`, '--dir', directory]);
  } else {
    let run;
    for (let attempt = 0; attempt <= attempts; attempt += 1) {
      const runs = githubApi(`repos/${repository}/actions/workflows/cao-runtime.yml/runs?head_sha=${revision}&per_page=100`)
        .flatMap((page) => page.workflow_runs)
        .filter((candidate) => candidate.head_sha === revision
          && ['push', 'release'].includes(candidate.event)
          && Number.isSafeInteger(candidate.id));
      for (const candidate of runs.filter((item) => item.conclusion === 'success')) {
        const artifacts = githubApi(`repos/${repository}/actions/runs/${candidate.id}/artifacts?per_page=100`)
          .flatMap((page) => page.artifacts);
        if (artifacts.some((artifact) => artifact.name === name && artifact.expired === false)) {
          run = candidate;
          break;
        }
      }
      if (run) break;
      const active = runs.some((candidate) =>
        ['queued', 'in_progress', 'pending', 'requested', 'waiting'].includes(candidate.status));
      if (!active) {
        const failed = runs.find((candidate) =>
          candidate.status === 'completed' && candidate.conclusion && candidate.conclusion !== 'success');
        if (failed) {
          throw new Error(`The CAO runtime build for ${revision} completed with ${failed.conclusion}. Use a revision with a successful runtime build.`);
        }
        throw new Error(`No successful, unexpired prebuilt CAO runtime is available for ${revision}. Wait for the CAO runtime build or select a published release; source/npm fallback is disabled.`);
      }
      if (attempt === attempts) {
        throw new Error(`The CAO runtime build for ${revision} is still running after ${Math.ceil((attempts * intervalMs) / 60_000)} minutes. Rerun the installer after the build completes.`);
      }
      wait(intervalMs);
    }
    gh(['run', 'download', String(run.id), '--repo', `github.com/${repository}`, '--name', name, '--dir', directory]);
  }
  const archive = path.join(directory, `${name}.tgz`);
  validateResources(directory, [`${name}.tgz`]);
  gh([
    'attestation', 'verify', archive, '--hostname', 'github.com', '--repo', repository,
    '--signer-workflow', `${repository}/.github/workflows/cao-runtime.yml`,
    '--source-digest', revision, '--signer-digest', revision, '--deny-self-hosted-runners',
  ]);
  return archive;
}

export function materializeCaoFromSource(campaign, sourceRoot, repositoryRoot) {
  validateCampaign(campaign);
  const resources = campaign === 'root' ? rootResources : [campaign];
  validateResources(sourceRoot, resources);
  if (campaign === 'root' || campaign === 'activity' || campaign === 'dashboard') {
    const temporaryDirectory = mkdtempSync(path.join(tmpdir(), 'cao-pack-'));
    try {
      const packed = packCaoRuntime(sourceRoot, temporaryDirectory, { tracked: true });
      for (const resource of resources) copyResource(packed.sourceRoot, repositoryRoot, resource);
    } finally {
      rmSync(temporaryDirectory, { force: true, recursive: true });
    }
  } else {
    for (const resource of resources) copyTrackedResource(sourceRoot, repositoryRoot, resource);
  }
  if (campaign === 'root') removeRetiredLauncher(repositoryRoot);
  else materializeCampaignIntelligence(campaign, repositoryRoot);
  return resources;
}

export function planCaoMaterialization(records, campaign) {
  validateCampaign(campaign);
  if (campaign !== 'root') {
    const record = recordFor(records, campaign);
    return [{
      campaign,
      record,
      revision: resolvedRevision(record),
      resources: [campaign],
    }];
  }

  const focused = ['activity', 'dashboard']
    .map((focusedCampaign) => ({
      campaign: focusedCampaign,
      record: exactRecordFor(records, focusedCampaign),
      resources: [focusedCampaign],
    }))
    .filter(({ record }) => record);
  const overriddenResources = new Set(focused.flatMap(({ resources }) => resources));
  return [{
    campaign: 'root',
    record: recordFor(records, 'root'),
    resources: rootResources.filter((resource) => !overriddenResources.has(resource)),
  }, ...focused].map((plan) => ({ ...plan, revision: resolvedRevision(plan.record) }));
}

export async function materializeCao(campaign = 'root', repositoryRoot = process.cwd()) {
  const plans = planCaoMaterialization(installedRecords(repositoryRoot), campaign);
  const temporaryDirectory = mkdtempSync(path.join(tmpdir(), 'cao-materialize-'));
  try {
    const sourceRoots = new Map();
    for (const [index, plan] of plans.entries()) {
      let sourceRoot = sourceRoots.get(plan.revision);
      if (!sourceRoot) {
        const extractionDirectory = path.join(temporaryDirectory, String(index));
        mkdirSync(extractionDirectory);
        const runtime = ['root', 'activity', 'dashboard'].includes(plan.campaign);
        const archive = runtime
          ? downloadCaoRuntimeArtifact(plan.record, path.join(temporaryDirectory, `download-${index}`))
          : path.join(temporaryDirectory, `${index}.tar.gz`);
        if (!runtime) await downloadArchive(plan.revision, archive);
        try {
          extractCaoArchive(archive, extractionDirectory, runtime || process.platform === 'win32');
        } catch (error) {
          throw new Error(`Unable to extract CAO ${plan.revision}: ${error.message}`, { cause: error });
        }
        sourceRoot = archiveRoot(extractionDirectory);
        if (runtime) validateCaoRuntimeArtifact(sourceRoot, plan.revision);
        sourceRoots.set(plan.revision, sourceRoot);
      }
      plan.sourceRoot = sourceRoot;
      validateResources(sourceRoot, plan.resources);
    }
    for (const plan of plans) {
      for (const resource of plan.resources) copyResource(plan.sourceRoot, repositoryRoot, resource);
    }
    if (campaign === 'root') removeRetiredLauncher(repositoryRoot);
    else materializeCampaignIntelligence(campaign, repositoryRoot);
    return {
      campaign,
      revision: plans[0].revision,
      revisions: Object.fromEntries(plans.map((plan) => [plan.campaign, plan.revision])),
      resources: plans.flatMap((plan) => plan.resources),
    };
  } finally {
    rmSync(temporaryDirectory, { force: true, recursive: true });
  }
}

export function verifyCaoRuntime(bundle, repositoryRoot = process.cwd()) {
  const required = {
    activity: [
      'activity/cao.mjs',
      'activity/workflow-values.mjs',
      'activity/workflow-value-history.mjs',
      'activity/workflow-value-status.mjs',
      'activity/canonical-query.mjs',
      'activity/local-values.mjs',
      'dashboard/site/src/data/model/operational-value-evidence.js',
      'dashboard/site/src/data/model/workflow-value-collection.js',
      'activity/cli/activity-stats.mjs',
      'activity/cli/cache.mjs',
      'activity/cli/dashboard.mjs',
      'activity/cli/download.mjs',
      'activity/cli/files.mjs',
      'activity/cli/ingestion.mjs',
      'activity/cli/issue-status.mjs',
      'activity/cli/jsonl-identity.mjs',
      'activity/cli/jsonl.mjs',
      'activity/cli/options.mjs',
      'activity/cli/payloads.mjs',
      'activity/cli/queries.mjs',
      'activity/cli/run.mjs',
      'activity/cli-usage.mjs',
      'activity/normalized-phase.mjs',
      'activity/commands/index.mjs',
      'activity/control-settings.mjs',
      'activity/repository-visibility.mjs',
      'activity/recycle-snapshot.mjs',
      'activity/collect-logs.sh',
      'activity/collect-workflow-logs.mjs',
      'activity/session-evidence.mjs',
      'activity/marketplace.mjs',
      'activity/problem-clustering.mjs',
      'activity/repository-memory.mjs',
    ],
    dashboard: ['dashboard/site/package.json', 'dashboard/report/aic-usage.mjs'],
  }[bundle];
  if (!required) throw new Error(`Unknown CAO runtime bundle: ${bundle}`);
  const missing = required.filter((file) => {
    try {
      readFileSync(path.join(repositoryRoot, ...file.split('/')));
      return false;
    } catch (error) {
      if (error?.code === 'ENOENT') return true;
      throw error;
    }
  });
  if (missing.length > 0) {
    throw new Error(`CAO ${bundle} runtime is incomplete at its canonical source paths: ${missing.join(', ')}`);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const [command, argument = 'root', sourceRoot] = process.argv.slice(2);
    if (command === 'materialize') {
      const result = await materializeCao(argument);
      console.log(`Materialized CAO ${result.campaign} ${result.revision}: ${result.resources.join(', ')}`);
    } else if (command === 'materialize-source') {
      if (!sourceRoot) throw new Error('materialize-source requires a source directory');
      const resources = materializeCaoFromSource(argument, path.resolve(sourceRoot), process.cwd());
      console.log(`Materialized CAO ${argument} from ${path.resolve(sourceRoot)}: ${resources.join(', ')}`);
    } else if (command === 'verify') {
      verifyCaoRuntime(argument);
    } else {
      throw new Error('Usage: materialize-cao.mjs <materialize CAMPAIGN|materialize-source CAMPAIGN SOURCE|verify BUNDLE>');
    }
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
