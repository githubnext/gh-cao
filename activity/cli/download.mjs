import { createHash } from 'node:crypto';
import { createReadStream, createWriteStream } from 'node:fs';
import { readFile, mkdir, mkdtemp, rename, rm, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { setTimeout as delay } from 'node:timers/promises';
import { DEFAULT_OUTPUT_DIRECTORY } from '../cli-usage.mjs';
import { isMapping, hashFileContents } from './files.mjs';

function deployedDataUrl(value) {
  if (!value?.trim()) {
    throw new Error('An explicit Activity snapshot manifest URL is required: pass --url or set DASHBOARD_DATA_URL to a payload-hashes.json URL. Alternatively, download the cao-activity-index artifact from a selected CAO Activity run with gh run download RUN_ID --repo OWNER/REPO --name cao-activity-index --dir .cao, then query the local cache with --database FILE.');
  }
  const url = new URL(value);
  if (!['http:', 'https:'].includes(url.protocol)) {
    throw new Error('Dashboard data URL must use HTTP or HTTPS.');
  }
  if (url.username || url.password) {
    throw new Error('Dashboard data URL must not contain credentials.');
  }
  return url;
}

class TransientDownloadError extends Error {}

function isTransientTransportError(error) {
  const transientCodes = new Set([
    'EAI_AGAIN',
    'ECONNREFUSED',
    'ECONNRESET',
    'ENETUNREACH',
    'EPIPE',
    'ETIMEDOUT',
    'UND_ERR_SOCKET'
  ]);
  return error instanceof TypeError
    || error?.name === 'AbortError'
    || error?.name === 'TimeoutError'
    || transientCodes.has(error?.code)
    || (error?.cause && isTransientTransportError(error.cause));
}

async function downloadFile(url, destination, { allowEmpty = false, signal } = {}) {
  let response;
  try {
    response = await fetch(url, {
      headers: { accept: 'application/x-ndjson, application/json, text/plain' },
      redirect: 'follow',
      signal: signal
        ? AbortSignal.any([signal, AbortSignal.timeout(120_000)])
        : AbortSignal.timeout(120_000)
    });
  } catch (error) {
    throw new TransientDownloadError(`Unable to download ${url}: transport failure`, { cause: error });
  }
  if (!response.ok) throw new Error(`Unable to download ${url}: HTTP ${response.status}`);
  if (!response.body) throw new Error(`Unable to download ${url}: response body is empty`);
  try {
    await pipeline(Readable.fromWeb(response.body), createWriteStream(destination, { flags: 'wx' }));
  } catch (error) {
    if (isTransientTransportError(error)) {
      throw new TransientDownloadError(`Unable to download ${url}: interrupted response`, { cause: error });
    }
    throw error;
  }
  const size = (await stat(destination)).size;
  if (!allowEmpty && size === 0) {
    throw new Error(`Unable to download ${url}: response body is empty`);
  }
  return size;
}

async function replaceFile(source, destination) {
  await rm(destination, { force: true });
  await rename(source, destination);
}

export async function downloadDeployedDashboardData({
  url = process.env.DASHBOARD_DATA_URL,
  output = DEFAULT_OUTPUT_DIRECTORY, manifestSha256 = process.env.DASHBOARD_MANIFEST_SHA256
} = {}) {
  const manifestUrl = deployedDataUrl(url);
  if (!manifestUrl.pathname.endsWith('/payload-hashes.json')) {
    throw new Error('Dashboard data URL must identify payload-hashes.json.');
  }
  if (manifestSha256 && !/^[a-f0-9]{64}$/i.test(manifestSha256)) {
    throw new Error('Dashboard manifest SHA-256 must contain exactly 64 hexadecimal characters.');
  }
  const databaseUrl = new URL('gh-aw-logs.sqlite', manifestUrl);
  const inventoryUrl = new URL('inventory-sources.json', manifestUrl);
  const outputDirectory = path.resolve(output);
  await mkdir(outputDirectory, { recursive: true });
  const manifestPath = path.join(outputDirectory, 'payload-hashes.json');
  const databasePath = path.join(outputDirectory, 'gh-aw-logs.sqlite');
  const inventoryPath = path.join(outputDirectory, 'inventory-sources.json');
  const isChecksumMismatch = (error) => error instanceof Error
    && /^Activity (?:SQLite|shard) checksum mismatch: /.test(error.message);
  const isTransientDownloadFailure = (error) => error instanceof TransientDownloadError
    || (error instanceof Error && /: HTTP (?:408|425|429|5\d\d)$/.test(error.message));

  const downloadAttempt = async () => {
    const temporaryDirectory = await mkdtemp(path.join(outputDirectory, '.deployed-dashboard-'));
    const temporaryManifest = path.join(temporaryDirectory, 'payload-hashes.json');
    const temporaryPayloads = path.join(temporaryDirectory, 'payloads');
    const temporaryDatabase = path.join(temporaryDirectory, 'gh-aw-logs.sqlite');
    const temporaryInventory = path.join(temporaryDirectory, 'inventory-sources.json');
    const abortController = new AbortController();
    try {
      await downloadFile(manifestUrl, temporaryManifest, { signal: abortController.signal });
      if (manifestSha256 && await hashFileContents(temporaryManifest) !== manifestSha256.toLowerCase()) {
        throw new Error('Activity snapshot manifest checksum mismatch: payload-hashes.json');
      }
      const hashes = JSON.parse(await readFile(temporaryManifest, 'utf8'));
      if (!isMapping(hashes)) {
        throw new Error('Activity snapshot manifest must contain a JSON object.');
      }
      if (hashes.synthetic === true) {
        throw new Error('Synthetic Pages demo data cannot be used as an operational Activity snapshot.');
      }
      const validDigest = (digest) => /^[a-f0-9]{64}$/i.test(String(digest));
      const expectedDatabaseDigest = hashes['gh-aw-logs.sqlite'];
      if (!validDigest(expectedDatabaseDigest)) {
        throw new Error('Activity snapshot manifest contains no valid SQLite checksum.');
      }
      const downloads = [
        downloadFile(databaseUrl, temporaryDatabase, { signal: abortController.signal }),
        downloadFile(inventoryUrl, temporaryInventory, { signal: abortController.signal })
      ];
      await Promise.all(downloads).catch(async (error) => {
        abortController.abort();
        await Promise.allSettled(downloads);
        throw error;
      });
      const inventorySources = JSON.parse(await readFile(temporaryInventory, 'utf8'));
      if (!isMapping(inventorySources)) {
        throw new Error('Deployed inventory sources must contain a JSON object.');
      }
      const databaseDigest = await hashFileContents(temporaryDatabase);
      if (databaseDigest !== expectedDatabaseDigest.toLowerCase()) {
        throw new Error('Activity SQLite checksum mismatch: gh-aw-logs.sqlite');
      }
      const runEntries = Object.entries(hashes)
        .filter(([name, digest]) => /^gh-aw-logs-runs\/[^/]+\.jsonl$/.test(name)
          && validDigest(digest))
        .sort(([left], [right]) => left.localeCompare(right));
      const recordEntries = Object.entries(hashes)
        .filter(([name, digest]) => /^gh-aw-logs-records\/[^/]+\.jsonl$/.test(name)
          && validDigest(digest))
        .sort(([left], [right]) => left.localeCompare(right));
      const rawEntries = Object.entries(hashes)
        .filter(([name, digest]) => /^gh-aw-logs-shards\/[^/]+\.jsonl$/.test(name)
          && validDigest(digest))
        .sort(([left], [right]) => left.localeCompare(right));
      const payloadEntries = runEntries.length > 0 ? [...runEntries, ...recordEntries] : rawEntries;
      if (payloadEntries.length === 0) throw new Error('Activity shard manifest contains no valid JSONL shards.');
      await mkdir(temporaryPayloads);
      for (const [name, expectedDigest] of payloadEntries) {
        const destination = path.join(temporaryPayloads, name);
        await mkdir(path.dirname(destination), { recursive: true });
        const size = await downloadFile(new URL(name, manifestUrl), destination, { allowEmpty: true });
        const hash = createHash('sha256');
        for await (const chunk of createReadStream(destination)) hash.update(chunk);
        if (hash.digest('hex') !== expectedDigest.toLowerCase()) {
          throw new Error(`Activity shard checksum mismatch: ${name}`);
        }
        if (size === 0) {
          delete hashes[name];
          await rm(destination);
        }
      }
      await writeFile(temporaryManifest, `${JSON.stringify(hashes, null, 2)}\n`);
      const payloadDirectories = [...new Set(payloadEntries.map(([name]) => name.split('/')[0]))];
      for (const directory of payloadDirectories) {
        const destination = path.join(outputDirectory, directory);
        await rm(destination, { recursive: true, force: true });
        await rename(path.join(temporaryPayloads, directory), destination);
      }
      await replaceFile(temporaryManifest, manifestPath);
      await replaceFile(temporaryDatabase, databasePath);
      await replaceFile(temporaryInventory, inventoryPath);
      return {
        manifestUrl: manifestUrl.href,
        databaseUrl: databaseUrl.href,
        inventoryUrl: inventoryUrl.href,
        manifest: manifestPath,
        payloadDirectories: payloadDirectories.map((directory) => path.join(outputDirectory, directory)),
        database: databasePath,
        inventory: inventoryPath
      };
    } finally {
      abortController.abort();
      await rm(temporaryDirectory, { recursive: true, force: true });
    }
  };

  let lastError;
  for (let attempt = 1; attempt <= 3; attempt += 1) {
    try {
      return await downloadAttempt();
    } catch (error) {
      lastError = error;
      if ((!isChecksumMismatch(error) && !isTransientDownloadFailure(error)) || attempt === 3) throw error;
      if (isTransientDownloadFailure(error)) await delay(attempt * 1_000);
    }
  }
  throw lastError;
}
