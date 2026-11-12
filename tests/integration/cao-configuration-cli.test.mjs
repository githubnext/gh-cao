import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

test("installed CAO validation preserves control-repository checkout semantics", async () => {
  const content = await readFile(path.resolve(".github", "workflows", "cao-validate.yml"), "utf8");
  assert.match(content, /ref: \$\{\{ github\.workflow_sha \}\}/);
  assert.doesNotMatch(content, /repository: githubnext\/gh-cao/);
  assert.match(content, /uses: \.\/\.github\/actions\/setup-gh-aw/);
});
