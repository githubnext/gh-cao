import test from "node:test";
import assert from "node:assert/strict";
import { parseMapping, mappingStatus } from "../../repository-ownership/mapping.mjs";

const entry = `version: 1
repositories:
  - repository: example/repo
    owners:
      - kind: team
        id: github:team:example/maintainers
      - kind: person
        id: github:user:alice
      - kind: service
        id: service:example/deployer
    reviewed_at: "2026-01-01T00:00:00Z"
    review_due_at: "2026-02-01T00:00:00Z"
    exemption:
      reason: pending review
      expires_at: "2026-01-15T00:00:00Z"
`;

test("valid mapping classifies exact time boundaries without authorizing exemptions", () => {
  const mapping = parseMapping(entry);
  assert.deepEqual(mappingStatus(mapping, "EXAMPLE/REPO", "2025-12-31T23:59:59Z"), { status: "not-yet-reviewed", exemption: "not-yet-effective" });
  assert.deepEqual(mappingStatus(mapping, "example/repo", "2026-01-01T00:00:00Z"), { status: "current", exemption: "declared-current" });
  assert.deepEqual(mappingStatus(mapping, "example/repo", "2026-01-15T00:00:00Z"), { status: "current", exemption: "expired" });
  assert.deepEqual(mappingStatus(mapping, "example/repo", "2026-02-01T00:00:00Z"), { status: "stale", exemption: "expired" });
  assert.deepEqual(mappingStatus(mapping, "example/other", "2026-02-01T00:00:00Z"), { status: "unmapped", exemption: "none" });
});

test("malformed YAML, aliases, duplicate keys and unknown fields fail closed", () => {
  for (const source of [
    "version: 1\nversion: 1\nrepositories: []\n",
    "version: 1\nrepositories: &items []\nother: *items\n",
    `${entry}    unexpected: true\n`,
    "version: 2\nrepositories: []\n",
    "version: 1\nrepositories: nope\n",
    "x".repeat(262145),
  ]) assert.throws(() => parseMapping(source));
});

test("rejects malformed or duplicate identities, invalid dates and duplicate repositories", () => {
  for (const source of [
    entry.replace("github:user:alice", "github:team:example/alice"),
    entry.replace("github:user:alice", "github:user:"),
    entry.replace("2026-02-01T00:00:00Z", "2026-02-30T00:00:00Z"),
    entry.replace("2026-02-01T00:00:00Z", "2026-01-01T00:00:00Z"),
    entry.replace("2026-01-15T00:00:00Z", "2026-01-01T00:00:00Z"),
    entry.replace("pending review", " "),
    entry.replace("github:user:alice", "github:team:EXAMPLE/MAINTAINERS").replace("kind: person", "kind: team"),
    `${entry}  - repository: EXAMPLE/REPO\n    owners: []\n    reviewed_at: "2026-01-01T00:00:00Z"\n    review_due_at: "2026-02-01T00:00:00Z"\n`,
  ]) assert.throws(() => parseMapping(source));
});

test("unmapped does not mean unowned and caller time must be valid", () => {
  const mapping = parseMapping("version: 1\nrepositories: []\n");
  assert.equal(mappingStatus(mapping, "example/repo", "2026-01-01T00:00:00Z").status, "unmapped");
  assert.throws(() => mappingStatus(mapping, "example/repo", "now"));
  assert.throws(() => mappingStatus({ repositories: [] }, "example/repo", "2026-01-01T00:00:00Z"));
  assert.throws(() => mapping.repositories.push({ repository: "example/repo" }));
});
