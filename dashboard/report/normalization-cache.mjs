import { lstat, readdir, rm, stat } from 'node:fs/promises';
import path from 'node:path';
export const DEFAULT_NORMALIZATION_CACHE_BYTES = 256 * 1024 * 1024;
export async function pruneNormalizationCaches(directories, {
  retainedNames, maxBytes = DEFAULT_NORMALIZATION_CACHE_BYTES
} = {}) {
  if (!Number.isSafeInteger(maxBytes) || maxBytes < 0) throw new RangeError('Normalization cache byte limit must be a non-negative integer');
  const files = [];
  let removedBytes = 0;
  for (const directory of directories) {
    let entries;
    try {
      if ((await lstat(directory)).isSymbolicLink()) throw new Error('Normalization cache must not be a symlink');
      entries = await readdir(directory, { withFileTypes: true });
    } catch (error) {
      if (error.code === 'ENOENT') continue;
      throw error;
    }
    for (const entry of entries) {
      if (!entry.isFile() || !/\.(jsonl|tmp)$/.test(entry.name)) continue;
      const file = path.join(directory, entry.name);
      const info = await stat(file);
      if (entry.name.endsWith('.tmp') || retainedNames && !retainedNames.has(entry.name)) {
        await rm(file);
        removedBytes += info.size;
      } else {
        files.push({ file, bytes: info.size, modified: info.mtimeMs });
      }
    }
  }
  let retainedBytes = files.reduce((total, file) => total + file.bytes, 0);
  for (const file of files.sort((a, b) => a.modified - b.modified || a.file.localeCompare(b.file))) {
    if (retainedBytes <= maxBytes) break;
    await rm(file.file);
    retainedBytes -= file.bytes;
    removedBytes += file.bytes;
  }
  return { retainedBytes, removedBytes, maxBytes };
}
