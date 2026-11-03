import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import path from "node:path";
import { digest } from "./github-evidence.mjs";

const promisedBlobFetchBatchSize = 50;

export function collectGitInventories(proof, prior, directory, eligible, execute = execFileSync) {
  const inventories = new Map(prior?.inventories ?? []);
  const counts = new Map();
  for (const files of inventories.values()) for (const file of files) {
    const previous = counts.get(file.blob);
    const value = { lines: file.lines, bytes: file.bytes };
    if (previous) assert.deepEqual(previous, value, "Conflicting preserved blob measurements");
    counts.set(file.blob, value);
  }
  const git = (args, options = {}) => execute("git", ["--git-dir", path.join(directory, "git"), ...args],
    { encoding: "utf8", timeout: 240_000, maxBuffer: 128 * 1024 * 1024, ...options });
  git(["init", "--bare", "--quiet"]);
  git(["remote", "add", "origin", `https://github.com/${proof.repository}.git`]);
  git(["config", "remote.origin.promisor", "true"]);
  git(["config", "remote.origin.partialclonefilter", "blob:none"]);
  const revisions = new Set([proof.adoption.baselineCommit, proof.adoption.commit,
    ...proof.snapshots.map(item => item.commit),
    ...proof.issues.flatMap(issue => issue.run.validation ? [issue.run.validation.headSha] : []),
    ...proof.history.map(item => item.commit),
    ...proof.pulls.filter(item => item.mergeSha).flatMap(item => [item.headSha, item.mergeSha])]);
  const knownPulls = new Map(prior?.proof.pulls.map(pull => [pull.id, pull]) ?? []);
  const needParents = proof.pulls.filter(pull => pull.mergeSha && Date.parse(pull.mergedAt) < Date.parse(proof.until));
  const fetch = values => {
    const unique = [...new Set(values)];
    for (let offset = 0; offset < unique.length; offset += 100) {
      git(["fetch", "--quiet", "--depth=1", "--filter=blob:none", "--no-tags", "origin", ...unique.slice(offset, offset + 100)]);
    }
  };
  fetch([...revisions].filter(commit => !inventories.has(commit)));
  for (const pull of needParents) {
    const known = knownPulls.get(pull.id);
    if (known?.mergeSha === pull.mergeSha && known.preMergeSha) pull.preMergeSha = known.preMergeSha;
    else {
      if (inventories.has(pull.mergeSha)) fetch([pull.mergeSha]);
      const parent = git(["cat-file", "-p", pull.mergeSha]).match(/^parent ([a-f0-9]{40})$/m)?.[1];
      assert.ok(parent, "Accepted revision lacks an actual first parent");
      pull.preMergeSha = parent;
    }
    revisions.add(pull.preMergeSha);
  }
  fetch([...revisions].filter(commit => !inventories.has(commit)));
  const newInventories = new Map();
  for (const commit of revisions) {
    if (inventories.has(commit)) continue;
    const files = git(["ls-tree", "-r", "-z", commit, "--", "pkg"]).split("\0").filter(Boolean).flatMap(entry => {
      const split = entry.indexOf("\t");
      const [mode, type, blob] = entry.slice(0, split).split(" ");
      const file = entry.slice(split + 1);
      return type === "blob" && ["100644", "100755"].includes(mode) && eligible(file) ? [{ path: file, blob }] : [];
    });
    assert.equal(new Set(files.map(file => file.path)).size, files.length);
    newInventories.set(commit, files);
  }
  const missing = [...new Set([...newInventories.values()].flatMap(files => files.map(file => file.blob)))]
    .filter(blob => !counts.has(blob));
  assert.ok(counts.size + missing.length <= 50_000, "Native blob measurement budget exceeded");
  for (let offset = 0; offset < missing.length; offset += promisedBlobFetchBatchSize) {
    const batch = missing.slice(offset, offset + promisedBlobFetchBatchSize);
    git(["fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin", "--stdin"], { input: `${batch.join("\n")}\n` });
    const bytes = git(["cat-file", "--batch"], { input: `${batch.join("\n")}\n`, encoding: null });
    let cursor = 0;
    for (const blob of batch) {
      const end = bytes.indexOf(10, cursor);
      assert.ok(end > cursor, "Malformed Git blob header");
      const [actual, type, sizeText] = bytes.subarray(cursor, end).toString().split(" ");
      const size = Number(sizeText);
      assert.ok(actual === blob && type === "blob" && Number.isSafeInteger(size) && size >= 0, "Unexpected Git blob identity");
      const content = bytes.subarray(end + 1, end + 1 + size);
      assert.equal(createHash("sha1").update(`blob ${content.length}\0`).update(content).digest("hex"), blob, "Native Git blob digest mismatch");
      counts.set(blob, { lines: content.reduce((sum, byte) => sum + Number(byte === 10), 0), bytes: content.length });
      cursor = end + 1 + size + 1;
    }
    assert.equal(cursor, bytes.length, "Unexpected trailing Git blob content");
  }
  for (const [commit, files] of newInventories) {
    inventories.set(commit, files.map(file => ({ ...file, ...counts.get(file.blob) })));
  }
  return {
    inventories: new Map([...inventories].map(([commit, files]) => [commit,
      { commit, files, inventoryDigest: digest(files) }])),
    uniqueBlobs: new Set([...inventories.values()].flatMap(files => files.map(file => file.blob))).size,
    newInventories: newInventories.size, newBlobs: missing.length,
  };
}
