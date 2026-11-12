import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { jsonlLines } from '../../dashboard/report/evidence-files.mjs';
import { jsonlRecords, parseJsonlRecords, readJsonlRecords } from '../../dashboard/report/jsonl.mjs';

async function fixture(t, content) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'cao-jsonl-reader-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const file = path.join(directory, 'records.jsonl');
  await writeFile(file, content);
  return file;
}

test('vendored JSONL reader and dependencies match the pinned upstream manifest', async () => {
  const directory = new URL('../../dashboard/report/vendor/gh-aw/', import.meta.url);
  const source = JSON.parse(await readFile(new URL('source.json', directory), 'utf8'));
  assert.equal(source.repository, 'github/gh-aw');
  assert.match(source.revision, /^[0-9a-f]{40}$/);
  for (const [file, digest] of Object.entries(source.files)) {
    assert.equal(createHash('sha256').update(await readFile(new URL(file, directory))).digest('hex'), digest, file);
  }
  assert.match(await readFile(new URL('LICENSE', directory), 'utf8'), /MIT License/);
});

test('text and file readers preserve JSON values, raw records and physical line numbers', async (t) => {
  const content = '\n \t\r\n  {"id":1} \r\nnull\nfalse\r\n0\n"text"\n[]\n{}';
  const file = await fixture(t, content);
  const expected = [1, null, false, 0, 'text', [], {}];
  for (const events of [parseJsonlRecords(content), readJsonlRecords(file)]) {
    const records = [...events];
    assert.deepEqual(records.map(({ value }) => value?.id ?? value), expected);
    assert.deepEqual(records.map(({ lineNumber }) => lineNumber), [3, 4, 5, 6, 7, 8, 9]);
    assert.equal(records[0].rawLine, '  {"id":1} \r\n');
    assert.equal(records.at(-1).rawLine, '{}');
  }
});

test('file framing handles split UTF-8 characters, CRLF and multi-chunk records', async (t) => {
  const first = JSON.stringify({ text: `${'x'.repeat(65525)}\u{1f680}` });
  const second = JSON.stringify({ text: '\u00e9'.repeat(200000) });
  const file = await fixture(t, `${first}\r\n${second}`);
  assert.deepEqual([...readJsonlRecords(file)].map(({ value }) => value), [
    JSON.parse(first), JSON.parse(second),
  ]);
  assert.deepEqual(await Array.fromAsync(jsonlLines([file])), [first, second]);
});

test('malformed and truncated records fail with sanitized file and line diagnostics', async (t) => {
  for (const invalid of ['{"secret":"private-evidence",}', '{"secret":"private-evidence"']) {
    const content = `{}\n \t\n${invalid}`;
    const file = await fixture(t, content);
    for (const records of [parseJsonlRecords(content, file), readJsonlRecords(file)]) {
      assert.throws(() => [...records], (error) => {
        assert.ok(error instanceof SyntaxError);
        assert.ok(error.message.startsWith(`${file}:3:`));
        assert.match(error.message, /Invalid JSONL record/);
        assert.doesNotMatch(error.stack, /private-evidence/);
        assert.equal(error.cause, undefined);
        return true;
      });
    }
  }
});

test('file reads are lazy and propagate I/O errors', async (t) => {
  const file = await fixture(t, '{}');
  await rm(file);
  const records = readJsonlRecords(file);
  assert.throws(() => records.next(), { code: 'ENOENT' });
  await assert.rejects(Array.fromAsync(jsonlLines([file])), { code: 'ENOENT' });
});

test('file descriptors close on completion, early return and parse errors', async (t) => {
  const file = await fixture(t, '{}\n{"unfinished":');
  const close = t.mock.method(fs, 'closeSync');
  const first = readJsonlRecords(file);
  assert.equal(first.next().value.lineNumber, 1);
  assert.equal(close.mock.callCount(), 0);
  first.return();
  assert.equal(close.mock.callCount(), 1);
  assert.throws(() => [...readJsonlRecords(file)], SyntaxError);
  assert.equal(close.mock.callCount(), 2);
  await writeFile(file, '{}\n');
  assert.equal([...readJsonlRecords(file)].length, 1);
  assert.equal(close.mock.callCount(), 3);
  for await (const line of jsonlLines([file])) {
    assert.equal(line, '{}');
    break;
  }
  assert.equal(close.mock.callCount(), 4);
});

test('multiple shard files retain caller order and restart physical line numbers', async (t) => {
  const first = await fixture(t, '\n{"id":1}\n');
  const second = await fixture(t, '{"id":2}');
  assert.deepEqual([...jsonlRecords([second, first])].map(({ value, lineNumber }) => [value.id, lineNumber]), [
    [2, 1], [1, 2],
  ]);
});
