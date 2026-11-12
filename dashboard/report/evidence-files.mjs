import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { readFile, readdir, mkdir, rename, rm, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { readLines } from './vendor/gh-aw/jsonl_helpers.cjs';

export { jsonlRecords } from './jsonl.mjs';

export function isMapping(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

export function commandFailureMessage(result, fallback) {
  return (result.stderr || result.stdout || '').trim() || result.error?.message || fallback;
}

export async function writeJsonAtomically(filePath, document) {
  const absolutePath = path.resolve(filePath);
  await mkdir(path.dirname(absolutePath), { recursive: true });
  const temporaryPath = `${absolutePath}.tmp-${process.pid}-${Date.now()}`;
  try {
    await writeFile(temporaryPath, `${JSON.stringify(document, null, 2)}\n`, { flag: 'wx' });
    await rename(temporaryPath, absolutePath);
  } finally {
    await rm(temporaryPath, { force: true });
  }
}

export async function jsonlFiles(root) {
  const files = [];
  const pending = [root];
  while (pending.length > 0) {
    const directory = pending.pop();
    const entries = await readdir(directory, { withFileTypes: true });
    for (const entry of entries) {
      const candidate = path.join(directory, entry.name);
      if (entry.isDirectory()) pending.push(candidate);
      else if (entry.isFile() && entry.name.endsWith('.jsonl')) {
        files.push({
          path: path.relative(root, candidate).split(path.sep).join('/'),
          content: await readFile(candidate, 'utf8')
        });
      }
    }
  }
  return files.sort((left, right) => left.path.localeCompare(right.path));
}

export async function* jsonlLines(paths) {
  for (const filePath of paths) {
    for (const line of readLines(filePath)) {
      if (line) yield line;
    }
  }
}

export async function hashFileContents(filePath) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(filePath)) hash.update(chunk);
  return hash.digest('hex');
}

export async function totalFileBytes(paths) {
  return (await Promise.all(paths.map(async (filePath) => (await stat(filePath)).size)))
    .reduce((sum, size) => sum + size, 0);
}
