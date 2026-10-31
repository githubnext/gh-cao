import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { controlSettings, parsePolicy, PolicyError } from "../../.github/workflows/shared/policy.mjs";

const schema = JSON.parse(readFileSync(new URL("../../.github/workflows/shared/cao.schema.json", import.meta.url), "utf8"));
const brokerSchema = schema.$defs.tokenBroker;
const workflow = {
  "repository-id": "123",
  "owner-id": "456",
  "workflow-ref": "owner/repo/.github/workflows/name.lock.yml@refs/heads/main",
  "workflow-sha": "0123456789abcdef0123456789abcdef01234567",
};

function document(broker) {
  return {
    version: 1,
    "gh-aw-version": "v1.0.1",
    "control-plane": { "token-broker": broker },
  };
}

function broker(workflows = [workflow]) {
  return { "copilot-requests": { workflows } };
}

// Evaluate the small schema vocabulary used by the broker, without another dependency.
function matchesSchema(value, definition) {
  switch (definition.type) {
    case "object":
      return value !== null && typeof value === "object" && !Array.isArray(value)
        && definition.required.every(key => Object.hasOwn(value, key))
        && Object.entries(value).every(([key, child]) =>
          Object.hasOwn(definition.properties, key)
          && matchesSchema(child, definition.properties[key]));
    case "array":
      return Array.isArray(value) && value.length <= definition.maxItems
        && value.every(child => matchesSchema(child, definition.items))
        && (!definition.uniqueItems || value.every((child, index) =>
          !value.slice(0, index).some(previous =>
            Object.keys(child).every(key => child[key] === previous[key]))));
    case "string":
      return typeof value === "string" && new RegExp(definition.pattern).test(value);
    default:
      throw new Error(`unexpected schema type ${definition.type}`);
  }
}

function accepts(value) {
  assert.equal(matchesSchema(value, brokerSchema), true, JSON.stringify(value));
  const input = document(value);
  assert.deepEqual(parsePolicy(JSON.stringify(input)), input);
}

function rejects(value) {
  assert.equal(matchesSchema(value, brokerSchema), false, JSON.stringify(value));
  assert.throws(() => parsePolicy(JSON.stringify(document(value))), PolicyError);
}

test("token broker schema is opt-in and closed at every mapping level", () => {
  assert.equal(schema.$defs.controlPlane.properties["token-broker"].$ref, "#/$defs/tokenBroker");
  assert.equal(schema.$defs.controlPlane.required?.includes("token-broker") ?? false, false);
  assert.equal(brokerSchema.additionalProperties, false);
  assert.deepEqual(brokerSchema.required, ["copilot-requests"]);
  const requests = brokerSchema.properties["copilot-requests"];
  assert.equal(requests.additionalProperties, false);
  assert.deepEqual(requests.required, ["workflows"]);
  assert.equal(requests.properties.workflows.maxItems, 128);
  assert.equal(requests.properties.workflows.uniqueItems, true);
  const entry = requests.properties.workflows.items;
  assert.equal(entry.additionalProperties, false);
  assert.deepEqual(entry.required, Object.keys(workflow));
  assert.deepEqual(Object.keys(entry.properties), Object.keys(workflow));
});

test("omitted and empty token broker authorizations remain disabled without changing rollout settings", () => {
  const missing = { version: 1, "gh-aw-version": "v1.0.1", "control-plane": {} };
  assert.deepEqual(parsePolicy(JSON.stringify(missing)), missing);
  accepts(broker([]));
  const populated = document(broker());
  populated["control-plane"].scope = { "allowed-repositories": ["different/control"] };
  assert.deepEqual(parsePolicy(JSON.stringify(populated)), populated);
  const baseline = structuredClone(populated);
  delete baseline["control-plane"]["token-broker"];
  assert.deepEqual(controlSettings(populated, "different/control"), controlSettings(baseline, "different/control"));
  const checkedIn = parsePolicy(readFileSync(new URL("../../.github/workflows/cao.json", import.meta.url), "utf8"));
  assert.deepEqual(checkedIn["control-plane"]["token-broker"], broker([]));
});

test("token broker accepts exact branch/tag workflow pins and preserves all fields", () => {
  accepts(broker());
  accepts(broker([{
    ...workflow,
    "repository-id": "12345678901234567890",
    "owner-id": "1",
    "workflow-ref": "some-owner/some.repo/.github/workflows/build_test.yaml@refs/tags/releases/v1.2-rc_1",
  }]));
  accepts(broker(Array.from({ length: 128 }, (_, index) => ({
    ...workflow, "repository-id": String(index + 1),
  }))));
  accepts(broker([workflow, { ...workflow, "workflow-sha": "f".repeat(40) }]));
});

test("token broker rejects missing fields, unknown capabilities, and malformed mappings", () => {
  for (const value of [
    null, [], "disabled", {}, { "copilot-requests": null },
    { "copilot-requests": [] }, { "copilot-requests": {} },
    { "copilot-requests": { workflows: null } },
    { "copilot-requests": { workflows: {} } },
    { ...broker(), "copilot-tasks": {} },
    { ...broker(), enabled: true },
    { "copilot-requests": { workflows: [], token: "forbidden" } },
    broker([null]), broker([[]]), broker(["workflow"]),
    broker([{ ...workflow, mode: "live" }]),
    broker([{ ...workflow, "copilot-tasks": true }]),
  ]) rejects(value);
  for (const key of Object.keys(workflow)) {
    const incomplete = { ...workflow };
    delete incomplete[key];
    rejects(broker([incomplete]));
  }
});

test("token broker enforces canonical positive decimal IDs and lowercase full SHAs", () => {
  for (const key of ["repository-id", "owner-id"]) {
    for (const value of [0, 123, null, "", "0", "01", "-1", "+1", "1.0", "1e2", "1 ", " 1", "1\n", "1\r", "1".repeat(21), "${{ github.repository_id }}"]) {
      rejects(broker([{ ...workflow, [key]: value }]));
    }
  }
  for (const value of [null, 123, "", "a".repeat(39), "a".repeat(41), "A".repeat(40), "g".repeat(40), `${"a".repeat(40)}\n`, "${{ github.sha }}"]) {
    rejects(broker([{ ...workflow, "workflow-sha": value }]));
  }
});

test("token broker rejects ambiguous, unsafe, or unpinned workflow references", () => {
  const prefix = "owner/repo/.github/workflows/name.lock.yml@";
  for (const value of [
    null, 123, "", "*", "owner/repo/.github/workflows/name.lock.yml",
    `${prefix}main`, `${prefix}${"a".repeat(40)}`, `${prefix}refs/pull/1/merge`,
    `${prefix}refs/heads/`, `${prefix}refs/tags/`, `${prefix}refs/heads/*`,
    `${prefix}refs/heads/../main`, `${prefix}refs/heads/feature/../../main`,
    `${prefix}refs/heads/.hidden`, `${prefix}refs/heads/feature/.hidden`,
    `${prefix}refs/heads/main.`, `${prefix}refs/heads/main.lock`,
    `${prefix}refs/heads/main.lock/other`, `${prefix}refs/heads/a..b`,
    `${prefix}refs/heads/feature//main`, `${prefix}refs/heads/main/`,
    `${prefix}refs/heads/main\n`, `${prefix}refs/heads/main\r`,
    `${prefix}refs/heads/main\u0000`, `${prefix}refs/heads/main\u007f`,
    `${prefix}refs/heads/main\u0085`, `${prefix}refs/heads/main\u2028`,
    `${prefix}refs/heads/a b`, `${prefix}refs/heads/a?b`,
    `${prefix}refs/heads/a[b`, `${prefix}refs/heads/a~b`,
    `${prefix}refs/heads/a^b`, `${prefix}refs/heads/a:b`,
    `${prefix}refs/heads/a\\b`, `${prefix}refs/heads/a@{b`,
    `${prefix}refs/heads/\${{ github.ref }}`,
    "owner/*/.github/workflows/name.yml@refs/heads/main",
    "owner/../.github/workflows/name.yml@refs/heads/main",
    "owner/repo/.github/workflows/../name.yml@refs/heads/main",
    "owner/repo/.github/workflows/nested/name.yml@refs/heads/main",
    "owner/repo/.github/workflows/name.md@refs/heads/main",
    "owner/repo/.github/workflows/name.yml@refs/heads/main@refs/tags/v1",
    "https://github.com/owner/repo/.github/workflows/name.yml@refs/heads/main",
  ]) rejects(broker([{ ...workflow, "workflow-ref": value }]));
});

test("token broker bounds the allowlist and rejects duplicate objects regardless of key order", () => {
  rejects(broker(Array.from({ length: 129 }, (_, index) => ({
    ...workflow, "repository-id": String(index + 1),
  }))));
  rejects(broker([workflow, { ...workflow }]));
  rejects(broker([workflow, Object.fromEntries(Object.entries(workflow).reverse())]));
  const duplicateKey = JSON.stringify(document(broker())).replace('"repository-id":"123"', '"repository-id":"123","repository-id":"123"');
  assert.throws(() => parsePolicy(duplicateKey), /duplicate mapping key/);
});
