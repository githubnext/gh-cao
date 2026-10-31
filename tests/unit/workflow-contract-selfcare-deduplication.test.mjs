import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { root, workflow, workflowsDirectory } from "./workflow-contract.helpers.mjs";

const descriptor = JSON.parse(readFileSync(join(root, "self-care/cao.json"), "utf8"));
const workers = readdirSync(workflowsDirectory)
  .filter((name) => name.startsWith("self-care-") && name.endsWith(".md"))
  .map((name) => name.slice(0, -3));

test("SelfCare deduplication covers every registered and in-tree worker", () => {
  assert.deepEqual([...workers].sort(), Object.values(descriptor.workers).sort());
});

for (const id of workers) {
  test(`${id} skips before agent activation while its own PR is open`, () => {
    const config = parse(/^---\n([\s\S]*?)\n---/.exec(workflow(`${id}.md`))[1]);
    const query = `is:pr is:open "gh-aw-workflow-id: ${id}" in:body`;
    assert.equal(config.imports[0].with.role, "worker");
    assert.equal(config.on["skip-if-match"], query);
    assert.equal(config["tracker-id"], id);

    const compiled = parse(workflow(`${id}.lock.yml`));
    const preActivation = compiled.jobs.pre_activation;
    const check = preActivation.steps.find((step) => step.id === "check_skip_if_match");
    assert.ok(check, "compiled workflow must execute the open-PR check");
    assert.equal(check.env.GH_AW_SKIP_QUERY, query);
    assert.equal(check.env.GH_AW_SKIP_MAX_MATCHES, "1");
    assert.match(check.with.script, /check_skip_if_match\.cjs/);
    assert.match(preActivation.outputs.activated, /steps\.check_skip_if_match\.outputs\.skip_check_ok == 'true'/);
    assert.equal(compiled.jobs.activation.needs, "pre_activation");
    assert.match(compiled.jobs.activation.if, /needs\.pre_activation\.outputs\.activated == 'true'/);
    assert.ok(compiled.jobs.agent.needs.includes("activation"));
  });
}
