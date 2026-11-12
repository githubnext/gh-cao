import assert from "node:assert/strict";
import test from "node:test";
import {
  buildInventoryDashboardSources,
  mergeInventoryDashboardSources,
} from "../../dashboard/report/inventory-sources.mjs";
import { queryDashboardSourceObservations } from "../../dashboard/site/src/data/queries/ingestion.js";
import { adaptGhAwLogs } from "../../dashboard/site/src/data/adapters/gh-aw-logs.js";
import { normalize } from "../../dashboard/site/src/data/normalize/index.js";

function workflow(id, path, overrides = {}) {
  return {
    id,
    name: `Workflow ${id}`,
    path,
    state: "active",
    html_url: `https://github.com/acme/app/actions/workflows/${id}`,
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-16T00:00:00Z",
    ...overrides,
  };
}

test("direct operations remain workflow evidence without synthetic campaigns", () => {
  const sources = buildInventoryDashboardSources({
    repository: "acme/control", generatedAt: "2026-10-21T00:00:00Z",
    inventory: { workflows: [{ id: "audit", name: "Audit", sourcePath: ".github/workflows/audit.md",
      compiled: true, role: "standalone" }], bundles: [] },
    controlSettings: { campaigns: {}, operations: { audit: { enabled: false, mode: "review" } } },
    discoveredRepositories: [],
  });
  assert.equal(sources.campaigns.rows.length, 0);
  const row = sources.workflows.rows[0];
  assert.equal(row["workflow-role"], "standalone");
  assert.equal(row["admission-status"], "blocked");
  assert.equal(row["admission-reason"], "operation-disabled");
});







test("publishes normalized marketplace packages and registry-level health", () => {
  const packageRow = {
    id: "official:acme/packages/demo@abc",
    "registry-id": "official",
    "registry-name": "Official",
    "registry-precedence": 0,
    name: "Demo",
    repository: "acme/packages",
    path: "demo",
    ref: "main",
    "resolved-commit": "a".repeat(40),
    version: "main",
    readme: "# Demo\n\nMarketplace package documentation.\n",
    "readme-path": "demo/README.md",
    source: `acme/packages/demo@${"a".repeat(40)}`,
    "add-command": `gh cao add acme/packages/demo@${"a".repeat(40)}`,
  };
  const sources = buildInventoryDashboardSources({
    repository: "acme/control",
    generatedAt: "2026-09-26T00:00:00Z",
    inventory: { workflows: [], bundles: [] },
    controlSettings: {},
    marketplace: {
      packages: [packageRow],
      diagnostics: [
        { "registry-id": "official", status: "available", packages: 1 },
        { "registry-id": "private", status: "unavailable", message: "permission denied" },
      ],
    },
  });

  assert.deepEqual(sources["marketplace-packages"].rows, [packageRow]);
  assert.equal(sources["marketplace-packages"].metadata.availability, "available");
  assert.equal(sources["marketplace-packages"].metadata.completeness, "partial");
  assert.deepEqual(
    sources["marketplace-registries"].rows.map((row) => row["registry-id"]),
    ["official", "private"],
  );
});

test("merges control registry metadata without replacing campaign ownership", () => {
  const generatedAt = "2026-09-17T00:00:00Z";
  const sources = buildInventoryDashboardSources({
    repository: "acme/control",
    generatedAt,
    inventory: {
      workflows: [{
        id: "worker",
        name: "Declared Worker",
        role: "worker",
        sourcePath: ".github/workflows/worker.md",
        compiled: true,
        maxAiCredits: 25,
      }],
      bundles: [{
        id: "operations",
        controlCampaign: "operations",
        name: "Operations",
        workflow: ".github/workflows/orchestrator.md",
        compiled: true,
        missingWorkers: [],
        workers: [{
          id: "worker",
          sourcePath: ".github/workflows/worker.md",
          compiled: true,
          maxAiCredits: 25,
        }],
      }],
    },
    controlSettings: {
      allowed_repositories: ["acme/control"],
      campaigns: {
        operations: {
          mode: "review",
          worker_policies: { worker: { enabled: true } },
        },
      },
      policy_resolution: { status: "available" },
    },
    discoveredRepositories: [{ full_name: "acme/control", visibility: "private" }],
    workflowRegistries: [{
      repository: "acme/control",
      expected: 1,
      observed: 1,
      pages: 1,
      state: "complete",
      failure: null,
      workflows: [{
        repository: "acme/control",
        id: 501,
        name: "Registered Worker",
        path: ".github/workflows/worker.lock.yml",
        state: "disabled_inactivity",
        htmlUrl: "https://github.com/acme/control/actions/workflows/501",
        createdAt: null,
        updatedAt: null,
      }],
    }],
  });

  assert.equal(sources.workflows.rows.length, 1);
  assert.deepEqual(sources.workflows.rows[0], {
    organization: "acme",
    repository: "control",
    campaign: "operations",
    "campaign-name": "Operations",
    "campaign-icon": "goal",
    "campaign-aic-allowance": 25,
    "campaign-worker-count": 1,
    "campaign-inventory-warnings": 0,
    "max-ai-credits": 25,
    "inventory-ready": true,
    "admission-status": "authorized",
    "admission-reason": "authorized",
    workflow: ".github/workflows/worker.md",
    "workflow-name": "Registered Worker",
    "workflow-role": "worker",
    "workflow-source-kind": "local",
    "workflow-source": "",
    "workflow-source-version": "unknown",
    "workflow-source-current-version": "unknown",
    "workflow-source-update-state": "unknown",
    "workflow-active": "false",
    "gh-aw-version": "unknown",
    "gh-aw-current-version": "unknown",
    "gh-aw-update-state": "unknown",
    "rollout-mode": "review",
    "observed-at": generatedAt,
    "workflow-registry-state": "disabled_inactivity",
    "workflow-id": "501",
    "workflow-link": {
      relation: "workflow",
      href: "https://github.com/acme/control/actions/workflows/501",
      label: "View Registered Worker",
    },
  });
});

test("preserves an unknown registry state instead of inferring active from compilation", () => {
  const sources = buildInventoryDashboardSources({
    repository: "acme/control",
    generatedAt: "2026-09-17T00:00:00Z",
    inventory: {
      workflows: [{
        id: "worker",
        name: "Declared Worker",
        sourcePath: ".github/workflows/worker.md",
        compiled: true,
      }],
      bundles: [],
    },
    controlSettings: {},
    discoveredRepositories: [{ full_name: "acme/control", visibility: "private" }],
    workflowRegistries: [{
      repository: "acme/control",
      expected: 1,
      observed: 1,
      pages: 1,
      state: "complete",
      failure: null,
      workflows: [{
        repository: "acme/control",
        id: 501,
        name: "Registered Worker",
        path: ".github/workflows/worker.lock.yml",
        state: "unknown",
        htmlUrl: "https://github.com/acme/control/actions/workflows/501",
        createdAt: null,
        updatedAt: null,
      }],
    }],
  });

  assert.equal(sources.workflows.rows[0]["workflow-active"], "unknown");
});

test("does not infer active state when registry evidence is unavailable", () => {
  const sources = buildInventoryDashboardSources({
    repository: "acme/control",
    generatedAt: "2026-09-17T00:00:00Z",
    inventory: {
      workflows: [{
        id: "worker",
        name: "Declared Worker",
        sourcePath: ".github/workflows/worker.md",
        compiled: true,
      }],
      bundles: [],
    },
    controlSettings: {},
    discoveredRepositories: [{ full_name: "acme/control", visibility: "private" }],
    workflowRegistries: [{
      repository: "acme/control",
      expected: null,
      observed: 0,
      pages: 0,
      state: "unavailable",
      failure: {
        repository: "acme/control",
        state: "unavailable",
        "failure-class": "permission",
        status: 403,
        reason: "Unable to discover workflows for acme/control: 403",
      },
      workflows: [],
    }],
  });

  assert.equal(sources.workflows.rows[0]["workflow-active"], "unknown");
});

test("remote registry and run-derived workflows share one canonical identity", () => {
  const generatedAt = "2026-09-17T00:00:00Z";
  const sources = buildInventoryDashboardSources({
    repository: "acme/control",
    generatedAt,
    inventory: { workflows: [], bundles: [] },
    controlSettings: {},
    discoveredRepositories: [{ full_name: "acme/app", visibility: "public" }],
    workflowRegistries: [{
      repository: "acme/app",
      expected: 1,
      observed: 1,
      pages: 1,
      state: "complete",
      failure: null,
      workflows: [{
        repository: "acme/app",
        id: 101,
        name: "CI",
        path: ".github/workflows/ci.yml",
        state: "active",
        htmlUrl: "https://github.com/acme/app/actions/workflows/101",
        createdAt: null,
        updatedAt: null,
      }],
    }],
  });
  const inventoryObservations = queryDashboardSourceObservations(sources).observations;
  const runObservations = adaptGhAwLogs({
    observedAt: generatedAt,
    repository: { githubId: 1, owner: "acme", name: "app", visibility: "public" },
    workflow: { githubId: 101, name: "CI run", path: ".github/workflows/ci.yml", state: "unknown" },
    run: {
      githubRunId: 9001,
      attempt: 1,
      event: "push",
      status: "completed",
      conclusion: "success",
    },
    files: [],
  }).observations;
  const canonical = normalize([...runObservations, ...inventoryObservations], {
    sourcePrecedence: { "gh-aw-logs": 1, "dashboard-sources": 2 },
  });

  assert.equal(canonical.workflows.length, 1);
  assert.equal(canonical.workflows[0].name, "CI");
  assert.equal(canonical.workflows[0].path, ".github/workflows/ci.yml");
  assert.equal(canonical.workflows[0].state, "active");
  assert.equal(canonical.workflows[0].githubId, "101");
  assert.equal(canonical.workflows[0].registryState, "active");
  assert.equal(canonical.runs[0].workflowId, canonical.workflows[0].id);
});
