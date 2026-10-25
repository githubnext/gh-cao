import assert from "node:assert/strict";
import test from "node:test";
import { normalizedParityRows } from "../e2e/dashboard-query-parity.mjs";

test("query parity compares sample composition across backend-specific frame identities", () => {
  const row = { summary: "copilot / auto", "sampled-runs": 4 };
  const normalize = (rows) => normalizedParityRows({}, rows, true);
  const indexedDB = [{ ...row, "statistical-frame-id": `sha256:${"a".repeat(64)}` }];
  const postgres = [{ ...row, "statistical-frame-id": `sha256:${"b".repeat(64)}` }];
  assert.deepEqual(normalize(indexedDB), normalize(postgres));
  assert.notDeepEqual(normalize(indexedDB), normalize([{ ...postgres[0], "sampled-runs": 3 }]));
  assert.notDeepEqual(normalize(indexedDB), normalize([row]));
  assert.notDeepEqual(normalize(indexedDB), normalize([]));
  assert.equal(indexedDB[0]["statistical-frame-id"], `sha256:${"a".repeat(64)}`);
});

test("query parity rejects malformed statistical frame identities", () => {
  for (const id of ["", "sha256:bad", null, 42]) {
    assert.throws(() => normalizedParityRows({}, [{
      "statistical-frame-id": id, "sampled-runs": 4
    }], true), /valid frame identity/);
  }
});
