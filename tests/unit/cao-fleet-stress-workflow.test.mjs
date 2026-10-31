import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const workflow = readFileSync(new URL("../../.github/workflows/cao-fleet-stress.yml", import.meta.url), "utf8");
const pkg = JSON.parse(readFileSync(new URL("../../package.json", import.meta.url), "utf8"));

test("daily fleet stress uses only the local simulator with no secrets or inference", () => {
  assert.match(workflow, /workflow_call:/);
  const daily = readFileSync(".github/workflows/ci-daily.yml", "utf8");
  assert.match(daily, /schedule:\n\s+- cron: "\d+ \d+ \* \* \*"/);
  assert.match(daily, /uses: \.\/\.github\/workflows\/cao-fleet-stress\.yml/);
  assert.match(workflow, /workflow_dispatch:/);
  assert.match(workflow, /persist-credentials: false/);
  assert.match(workflow, /npm run test:stress:cao/);
  assert.match(workflow, /timeout-minutes: 65/);
  assert.match(workflow, /if: always\(\)/);
  assert.match(workflow, /actions\/upload-artifact@[a-f0-9]{40}/);
  assert.doesNotMatch(workflow, /secrets\.|id-token:|pull_request_target|gh aw run|copilot|OPENAI_API_KEY/);
  assert.equal(pkg.scripts["test:stress:cao"],
    "CAO_FLEET_STRESS=1 go -C server test ./internal/webhookgateway -run '^TestGitHubSimulatorFleetStress$' -count=1 -timeout=60m -v");
});
