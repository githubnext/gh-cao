import assert from "node:assert/strict";
import test from "node:test";
import { AuthorityError } from "../../.github/workflows/shared/authority.mjs";
import { parsePolicy } from "../../.github/workflows/shared/policy.mjs";
import {
  multiTargetInput, operationBinding, resolveMultiTargetWorker, validateOperationHandoff,
} from "../../.github/workflows/shared/operations.mjs";

const environment = {
  GITHUB_REPOSITORY: "acme/control",
  GITHUB_WORKFLOW_SHA: "1".repeat(40),
  GITHUB_WORKFLOW_REF: "acme/control/.github/workflows/platform-review.lock.yml@refs/heads/main",
  GITHUB_RUN_ID: "77",
  GITHUB_RUN_ATTEMPT: "1",
  GITHUB_SERVER_URL: "https://github.com",
  CAO_ENVELOPE_VERSION: "1",
  CAO_TARGET_REPOSITORIES: '["acme/one","acme/two"]',
  CAO_REQUESTED_MODE: "review",
  CAO_CORRELATION_ID: "12-3",
  CAO_CENTRAL_REPOSITORY: "acme/control",
  CAO_CONTROL_PLANE_RUN_URL: "https://github.com/acme/control/actions/runs/12",
};
const identity = { campaign: "platform", worker: "review" };

function policy(change = () => {}) {
  const value = {
    version: 1,
    "gh-aw-version": "v0.93.1",
    "control-plane": {
      scope: { "allowed-owners": ["acme", "other"], "allowed-repositories": ["acme/one", "acme/two", "acme/three", "other/one"] },
      campaigns: {
        platform: {
          mode: "live",
          "max-repositories": 3,
          targets: { "acme/one": { mode: "live" } },
          workers: { review: { workflow: "platform-review", "max-mode": "live", "max-targets-per-run": 2 } },
        },
      },
    },
  };
  change(value["control-plane"]);
  return parsePolicy(JSON.stringify(value));
}

function resolve(env = {}, document = policy()) {
  const merged = { ...environment, ...env };
  return resolveMultiTargetWorker(document, multiTargetInput(merged), operationBinding(merged), identity);
}

test("multi-target worker admits an explicit review-only target set bound to the exact policy revision", () => {
  const result = resolve();
  assert.equal(result.authorized, true);
  assert.equal(result.control_role, "multi-target-worker");
  assert.equal(result.safe_output_mode, "review");
  assert.equal(result.max_repositories, 2);
  assert.deepEqual(result.authority_envelope.grants, [
    { target: "acme/one", mode: "review", destination: "acme/control" },
    { target: "acme/two", mode: "review", destination: "acme/control" },
  ]);
  assert.equal(result.authority_envelope.operation, "platform/review");
  assert.equal(result.authority_envelope.delegation, false);
  assert.deepEqual(result.authority_envelope.binding, operationBinding(environment));

  const routed = resolve({ CAO_REQUESTED_SAFE_OUTPUT_REPOSITORY: "acme/reviews", CAO_REQUESTED_MAX_REPOSITORIES: "2" });
  assert.deepEqual(routed.authority_envelope.grants.map(grant => grant.destination), ["acme/reviews", "acme/reviews"]);
  const single = resolve({ CAO_TARGET_REPOSITORIES: '["acme/three"]', CAO_REQUESTED_MODE: "" });
  assert.deepEqual(single.authority_envelope.grants.map(grant => grant.target), ["acme/three"]);
});

test("multi-target worker bound is the stricter worker opt-in and campaign repository limit", () => {
  const narrowed = policy(control => { control.campaigns.platform.workers.review["max-targets-per-run"] = 5; });
  assert.equal(resolve({ CAO_TARGET_REPOSITORIES: '["acme/one","acme/two","acme/three"]' }, narrowed).max_repositories, 3);
  assert.throws(
    () => resolve({ CAO_TARGET_REPOSITORIES: '["acme/one","acme/two","acme/three"]' }),
    { message: "target_repos count exceeds the admitted repository limit" },
  );
  const defaults = policy(control => { delete control.campaigns.platform["max-repositories"]; });
  assert.throws(() => resolve({}, defaults), /target_repos count exceeds/);
  assert.equal(resolve({ CAO_REQUESTED_MAX_REPOSITORIES: "1", CAO_TARGET_REPOSITORIES: '["acme/one"]' }).max_repositories, 1);
  assert.throws(() => resolve({ CAO_REQUESTED_MAX_REPOSITORIES: "1" }), /target_repos count exceeds/);
  assert.throws(() => resolve({ CAO_REQUESTED_MAX_REPOSITORIES: "3" }), /max_repositories exceeds checked-in policy/);
  assert.throws(() => resolve({ CAO_REQUESTED_MAX_REPOSITORIES: "two" }), /max_repositories must be an integer/);
});

test("multi-target worker denials follow campaign and worker policy without widening", () => {
  for (const [name, change, reason] of [
    ["absent control plane", null, "control-plane-absent"],
    ["undeclared campaign", control => { control.campaigns = { other: { workers: {} } }; }, "campaign-undeclared"],
    ["disabled campaign", control => { control.campaigns.platform.enabled = false; }, "campaign-disabled"],
    ["disabled worker", control => { control.campaigns.platform.workers.review.enabled = false; }, "worker-disabled"],
    ["single-target worker", control => { delete control.campaigns.platform.workers.review["max-targets-per-run"]; }, "worker-multi-target-undeclared"],
  ]) {
    const document = change ? policy(change) : { version: 1 };
    const result = resolve({}, document);
    assert.equal(result.authorized, false, name);
    assert.equal(result.reason, reason, name);
    assert.equal("authority_envelope" in result, false, name);
  }
  const unknown = policy(control => { control.campaigns.platform.workers = { other: { workflow: "platform-review", "max-targets-per-run": 2 } }; });
  assert.throws(() => resolve({}, unknown), /unknown worker: platform\/review/);
  assert.throws(
    () => resolve({ GITHUB_WORKFLOW_REF: "acme/control/.github/workflows/platform-other.lock.yml@refs/heads/main" }),
    /multi-target worker identity must match GITHUB_WORKFLOW_REF/,
  );
});

for (const [name, env, pattern] of [
  ["missing envelope version", { CAO_ENVELOPE_VERSION: "" }, /^target_repos requires envelope_version 1$/],
  ["unsupported envelope version", { CAO_ENVELOPE_VERSION: "2" }, /^target_repos requires envelope_version 1$/],
  ["mixed target inputs", { CAO_TARGET_REPOSITORY: "acme/one" }, /^target_repos must not be combined with target_repo$/],
  ["missing target set", { CAO_TARGET_REPOSITORIES: "" }, /^target_repos is required/],
  ["oversized target set", { CAO_TARGET_REPOSITORIES: ` ${" ".repeat(65536)}` }, /^target_repos exceeds 64 KiB$/],
  ["malformed JSON", { CAO_TARGET_REPOSITORIES: "[acme/one" }, /^target_repos must be a non-empty JSON array/],
  ["comma-separated string", { CAO_TARGET_REPOSITORIES: '"acme/one,acme/two"' }, /^target_repos must be a non-empty JSON array/],
  ["empty target set", { CAO_TARGET_REPOSITORIES: "[]" }, /^target_repos must be a non-empty JSON array/],
  ["non-string target", { CAO_TARGET_REPOSITORIES: '["acme/one",{"target":"acme/two"}]' }, /^target_repos\[1\] must use owner\/repository form$/],
  ["tampered target", { CAO_TARGET_REPOSITORIES: '["acme/one","acme/two;$(id)"]' }, /^target_repos\[1\] must use owner\/repository form$/],
  ["duplicate target", { CAO_TARGET_REPOSITORIES: '["acme/one","ACME/One"]' }, /^target_repos\[1\] duplicates an earlier target$/],
  ["multiple owners", { CAO_TARGET_REPOSITORIES: '["acme/one","other/one"]' }, /^target_repos must share one owner/],
  ["owner outside scope", { CAO_TARGET_REPOSITORIES: '["outside/one"]' }, /^target_repos\[0\] owner is outside control-plane\.scope\.allowed-owners$/],
  ["repository outside allowlist", { CAO_TARGET_REPOSITORIES: '["acme/one","acme/secret"]' }, /^target_repos\[1\] is not allowed$/],
  ["foreign central repository", { CAO_CENTRAL_REPOSITORY: "acme/other" }, /^central_repo must identify/],
  ["missing correlation", { CAO_CORRELATION_ID: "" }, /^correlation_id must identify/],
  ["mismatched run URL", { CAO_CONTROL_PLANE_RUN_URL: "https://github.com/acme/control/actions/runs/13" }, /^control_plane_run_url must match/],
  ["live mode", { CAO_REQUESTED_MODE: "live" }, /^safe_output_mode must be review for multi-target workers$/],
  ["debug mode", { CAO_REQUESTED_MODE: "debug" }, /^safe_output_mode must be review for multi-target workers$/],
  ["malformed destination", { CAO_REQUESTED_SAFE_OUTPUT_REPOSITORY: "acme" }, /^safe_output_repo must use owner\/repository form$/],
  ["destination owner outside scope", { CAO_REQUESTED_SAFE_OUTPUT_REPOSITORY: "outside/reviews" }, /^safe_output_repo owner is outside/],
  ["rollout narrowing", { CAO_REQUESTED_ROLLOUT_PERCENT: "50" }, /^rollout_percent is not supported for multi-target workers$/],
]) {
  test(`multi-target worker rejects ${name} before model execution`, () => {
    assert.throws(() => resolve(env), error => {
      assert.ok(error instanceof AuthorityError);
      assert.match(error.message, pattern);
      for (const value of Object.values(env)) {
        if (value.length > 4) assert.equal(error.message.includes(value), false, "rejections must not echo untrusted input");
      }
      return true;
    });
  });
}

test("multi-target handoff rejects a different revision, run, or target set", () => {
  const admitted = resolve();
  const binding = operationBinding(environment);
  validateOperationHandoff(admitted, resolve(), binding);
  assert.throws(() => validateOperationHandoff(admitted, resolve({ GITHUB_WORKFLOW_SHA: "2".repeat(40) }), binding), /handoff exceeds parent authority/);
  assert.throws(() => validateOperationHandoff(admitted, resolve({ GITHUB_RUN_ATTEMPT: "2" }), operationBinding({ ...environment, GITHUB_RUN_ATTEMPT: "2" })), /envelope binding mismatch: runAttempt/);
  assert.throws(() => validateOperationHandoff(admitted, resolve({ CAO_TARGET_REPOSITORIES: '["acme/one","acme/three"]' }), binding), /handoff widens a scoped grant/);
  assert.throws(
    () => validateOperationHandoff(admitted, resolve({ CAO_TARGET_REPOSITORIES: '["acme/one"]' }), binding),
    /handoff widens a scoped grant/,
  );
  const narrowed = resolve({ CAO_TARGET_REPOSITORIES: '["acme/one"]', CAO_REQUESTED_MAX_REPOSITORIES: "1" });
  assert.throws(
    () => validateOperationHandoff(narrowed, resolve({ CAO_TARGET_REPOSITORIES: '["acme/one"]' }), binding),
    /operation handoff does not match current request and exact-revision policy/,
  );
});
