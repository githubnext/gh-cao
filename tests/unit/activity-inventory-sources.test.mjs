import assert from "node:assert/strict";
import test from "node:test";
import {
  buildInventoryDashboardSources,
  discoverLatestGhAwVersion,
  discoverLatestCampaignCommits,
  discoverRepositories,
  discoverWorkflowRegistries,
  discoverWorkflowVersions,
  mergeInventoryDashboardSources,
} from "../../activity/inventory-sources.mjs";
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

test("merges independently authenticated inventory fragments without losing health", () => {
  const metadata = {
    "source-id": "central-agentic-ops-repositories",
    "source-kind": "github",
    "as-of": "2026-09-29T00:00:00Z",
    "retrieved-at": "2026-09-29T00:00:00Z",
    completeness: "complete",
    freshness: "fresh",
    availability: "available",
    "coverage-expected": 1,
    "coverage-observed": 1,
  };
  const fragment = (owner) => ({
    repositories: {
      rows: [{ organization: owner, repository: "service" }],
      metadata,
    },
    workflows: {
      rows: [{ organization: owner, repository: "service", workflow: ".github/workflows/agent.md" }],
      metadata,
    },
    campaigns: { rows: [{ campaign: "catalog" }], metadata },
    "marketplace-packages": { rows: [], metadata },
    "marketplace-registries": { rows: [], metadata },
    "configuration-policy": { rows: [{ version: 1 }], metadata },
  });

  test("owner-wide discovery accepts an App token scoped to a different control-repository owner", async () => {
    const repositories = await discoverRepositories({
      policy_document: { "control-plane": { inventory: { "max-scan-repositories": 10 } } },
      allowed_repositories: [],
      allowed_owners: ["octo"],
    }, {
      token: "enterprise-installation-token",
      controlRepository: "acme/control",
      credentialOwner: "octo",
      fetchImplementation: async (url) => {
        assert.match(String(url), /orgs\/octo\/repos\?per_page=100&type=all&page=1$/);
        return new Response(JSON.stringify([{ full_name: "octo/service", visibility: "private" }]));
      },
    });

    assert.deepEqual(repositories, [{ full_name: "octo/service", visibility: "private" }]);
  });

  const merged = mergeInventoryDashboardSources([fragment("acme"), fragment("octo")]);
  assert.deepEqual(
    merged.repositories.rows.map(({ organization }) => organization),
    ["acme", "octo"],
  );
  assert.equal(merged.repositories.metadata["coverage-expected"], 2);
  assert.equal(merged.workflows.rows.length, 2);
  assert.equal(merged.campaigns.rows.length, 1);
});

test("discovers public workflow registry metadata and disabled state", async () => {
  const registries = await discoverWorkflowRegistries([{ full_name: "acme/app" }], {
    token: "test-token",
    fetchImplementation: async (url) => {
      assert.match(String(url), /repos\/acme\/app\/actions\/workflows\?per_page=100&page=1$/);
      return new Response(JSON.stringify({
        total_count: 2,
        workflows: [
          workflow(101, ".github/workflows/ci.yml", { name: "CI" }),
          workflow(102, ".github/workflows/release.yml", {
            name: "Release",
            state: "disabled_manually",
          }),
        ],
      }));
    },
  });

  test("selects the latest stable gh-aw release instead of a prerelease", async () => {
    const version = await discoverLatestGhAwVersion({
      token: "test-token",
      fetchImplementation: async (url) => {
        assert.match(String(url), /repos\/github\/gh-aw\/releases\?per_page=100&page=1$/);
        return new Response(JSON.stringify([
          { tag_name: "v0.91.0-rc.1", prerelease: false, draft: false },
          { tag_name: "v0.90.2", prerelease: false, draft: false },
        ]));
      },
    });

    assert.equal(version, "v0.90.2");
  });

  test("retains successful campaign and workflow source revisions when another repository lookup fails", async () => {
    const currentRevision = "a".repeat(40);
    const workflowRevision = "b".repeat(40);
    const resolution = await discoverLatestCampaignCommits({
      campaigns: [
        { campaign: "acme/catalog/operations" },
        { campaign: "acme/broken/operations" },
      ],
      workflows: [{
        sourcePath: ".github/workflows/review.md",
        source: `acme/workflows/review@${"c".repeat(40)}`,
      }],
    }, {
      token: "test-token",
      fetchImplementation: async (url) => {
        const request = String(url);
        if (request.endsWith("/repos/acme/broken")) return new Response("", { status: 503 });
        if (request.endsWith("/repos/acme/catalog")) {
          return new Response(JSON.stringify({ default_branch: "main" }));
        }
        if (request.endsWith("/repos/acme/catalog/commits/main")) {
          return new Response(JSON.stringify({ sha: currentRevision }));
        }
        if (request.endsWith("/repos/acme/workflows")) {
          return new Response(JSON.stringify({ default_branch: "main" }));
        }
        if (request.endsWith("/repos/acme/workflows/commits/main")) {
          return new Response(JSON.stringify({ sha: workflowRevision }));
        }
        throw new Error(`Unexpected request: ${request}`);
      },
    });

    assert.deepEqual(resolution.commits, {
      "acme/catalog": currentRevision,
      "acme/workflows": workflowRevision,
    });
    assert.equal(resolution.expected, 3);
    assert.equal(resolution.observed, 2);
    assert.equal(resolution.failures[0].repository, "acme/broken");
  });

  test("parses remote compiler metadata while retaining missing workflow files and partial repositories", async () => {
    const registries = [
      {
        repository: "acme/app",
        expected: 2,
        observed: 2,
        pages: 1,
        state: "complete",
        failure: null,
        workflows: [
          workflow(101, ".github/workflows/agent.lock.yml"),
          workflow(102, ".github/workflows/missing.lock.yml"),
        ],
      },
      {
        repository: "acme/broken",
        expected: 1,
        observed: 1,
        pages: 1,
        state: "complete",
        failure: null,
        workflows: [
          { ...workflow(201, ".github/workflows/agent.lock.yml"), repository: "acme/broken" },
        ],
      },
    ];
    const enriched = await discoverWorkflowVersions(registries, {
      token: "test-token",
      fetchImplementation: async (url) => {
        const request = String(url);
        if (request.includes("/repos/acme/app/contents/.github/workflows/agent.lock.yml")) {
          return new Response(JSON.stringify({
            encoding: "base64",
            content: Buffer.from('# gh-aw-metadata: {"compiler_version":"v0.89.15"}\n').toString("base64"),
          }));
        }
        if (request.includes("/repos/acme/app/contents/.github/workflows/missing.lock.yml")) {
          return new Response("", { status: 404 });
        }
        if (request.includes("/repos/acme/broken/contents/")) {
          return new Response("", { status: 503 });
        }
        throw new Error(`Unexpected request: ${request}`);
      },
    });

    assert.equal(enriched[0].workflows[0].ghAwVersion, "v0.89.15");
    assert.equal(enriched[0].workflows[1].ghAwVersion, undefined);
    assert.equal(enriched[0].versionFailures[0].workflow, ".github/workflows/missing.lock.yml");
    assert.equal(enriched[1].workflows.length, 1);
    assert.equal(enriched[1].versionFailures[0].repository, "acme/broken");
  });

  test("emits campaign and workflow version update state for control and enrolled repositories", () => {
    const installedCampaignRevision = "1".repeat(40);
    const latestCampaignRevision = "2".repeat(40);
    const installedWorkflowRevision = "3".repeat(40);
    const latestWorkflowRevision = "4".repeat(40);
    const generatedAt = "2026-09-17T00:00:00Z";
    const intelligenceDeclaration = {
      contractVersion: "1.0.0",
      campaign: "operations",
      fields: {
        intendedOutcome: {
          statement: "Reduce unresolved operational work.",
        },
      },
    };
    const sources = buildInventoryDashboardSources({
      repository: "acme/control",
      generatedAt,
      inventory: {
        campaigns: [{
          id: "operations",
          name: "Operations",
          campaign: "operations",
          source: `acme/catalog/operations@${installedCampaignRevision}`,
          resolvedCommit: installedCampaignRevision,
          intelligenceDeclaration,
        }],
        workflows: [{
          id: "control-agent",
          name: "Control agent",
          sourcePath: ".github/workflows/control-agent.md",
          source: `acme/workflows/control-agent@${installedWorkflowRevision}`,
          version: installedWorkflowRevision,
          compiled: true,
          ghAwVersion: "v0.88.0",
        }],
        bundles: [],
      },
      controlSettings: {
        campaigns: {
          operations: { enabled: true, mode: "review" },
        },
      },
      discoveredRepositories: [{ full_name: "acme/app", visibility: "public" }],
      workflowRegistries: [{
        repository: "acme/app",
        expected: 1,
        observed: 1,
        pages: 1,
        state: "complete",
        failure: null,
        versionFailures: [],
        workflows: [{
          repository: "acme/app",
          id: 101,
          name: "Agent",
          path: ".github/workflows/agent.lock.yml",
          state: "active",
          htmlUrl: "https://github.com/acme/app/actions/workflows/101",
          createdAt: null,
          updatedAt: null,
          ghAwVersion: "v0.89.0",
        }],
      }],
      latestCampaignResolution: {
        commits: {
          "acme/catalog": latestCampaignRevision,
          "acme/workflows": latestWorkflowRevision,
        },
        failures: [],
        expected: 1,
        observed: 1,
      },
      latestGhAwVersion: "v0.89.0",
    });

    assert.deepEqual({
      installed: sources.campaigns.rows[0]["campaign-version"],
      latest: sources.campaigns.rows[0]["campaign-current-version"],
      state: sources.campaigns.rows[0]["campaign-update-state"],
    }, {
      installed: installedCampaignRevision.slice(0, 12),
      latest: latestCampaignRevision.slice(0, 12),
      state: "update-available",
    });
    assert.equal(sources.campaigns.rows[0]["campaign-source"], "acme/catalog/operations");
    assert.deepEqual(
      sources.campaigns.rows[0]["campaign-intelligence-declaration"],
      intelligenceDeclaration,
    );
    const canonical = normalize(
      queryDashboardSourceObservations(sources).observations,
    );
    assert.deepEqual(
      canonical.campaigns[0].intelligenceDeclaration,
      intelligenceDeclaration,
    );
    assert.equal(canonical.campaigns[0].sourceCoordinate, "acme/catalog/operations");
    const localWorkflow = sources.workflows.rows.find(
      (row) => row.workflow === ".github/workflows/control-agent.md",
    );
    assert.deepEqual({
      kind: localWorkflow["workflow-source-kind"],
      source: localWorkflow["workflow-source"],
      installed: localWorkflow["workflow-source-version"],
      latest: localWorkflow["workflow-source-current-version"],
      state: localWorkflow["workflow-source-update-state"],
    }, {
      kind: "source-managed",
      source: "acme/workflows/control-agent",
      installed: installedWorkflowRevision.slice(0, 12),
      latest: latestWorkflowRevision.slice(0, 12),
      state: "update-available",
    });
    const canonicalLocalWorkflow = canonical.workflows.find(
      (row) => row.path === ".github/workflows/control-agent.md",
    );
    assert.deepEqual({
      kind: canonicalLocalWorkflow.sourceKind,
      source: canonicalLocalWorkflow.sourceCoordinate,
      installed: canonicalLocalWorkflow.sourceVersion,
      latest: canonicalLocalWorkflow.sourceCurrentVersion,
      state: canonicalLocalWorkflow.sourceUpdateState,
    }, {
      kind: "source-managed",
      source: "acme/workflows/control-agent",
      installed: installedWorkflowRevision.slice(0, 12),
      latest: latestWorkflowRevision.slice(0, 12),
      state: "update-available",
    });
    assert.deepEqual(sources.workflows.rows.map((row) => ({
      repository: `${row.organization}/${row.repository}`,
      workflow: row.workflow,
      installed: row["gh-aw-version"],
      latest: row["gh-aw-current-version"],
      state: row["gh-aw-update-state"],
    })), [
      {
        repository: "acme/app",
        workflow: ".github/workflows/agent.md",
        installed: "v0.89.0",
        latest: "v0.89.0",
        state: "up-to-date",
      },
      {
        repository: "acme/control",
        workflow: ".github/workflows/control-agent.md",
        installed: "v0.88.0",
        latest: "v0.89.0",
        state: "update-available",
      },
    ]);
  });
  const sources = buildInventoryDashboardSources({
    repository: "acme/control",
    generatedAt: "2026-09-17T00:00:00Z",
    inventory: { workflows: [], bundles: [] },
    controlSettings: {},
    discoveredRepositories: [{ full_name: "acme/app", visibility: "public" }],
    workflowRegistries: registries,
  });

  assert.equal(sources.workflows.metadata.completeness, "complete");
  assert.equal(sources.workflows.metadata["coverage-expected"], 1);
  assert.equal(sources.workflows.metadata["coverage-observed"], 1);
  assert.deepEqual(sources.workflows.rows.map((row) => ({
    repository: `${row.organization}/${row.repository}`,
    name: row["workflow-name"],
    path: row.workflow,
    active: row["workflow-active"],
    role: row["workflow-role"],
    id: row["workflow-id"],
    href: row["workflow-link"]?.href,
  })), [
    {
      repository: "acme/app",
      name: "CI",
      path: ".github/workflows/ci.yml",
      active: "true",
      role: "standalone",
      id: "101",
      href: "https://github.com/acme/app/actions/workflows/101",
    },
    {
      repository: "acme/app",
      name: "Release",
      path: ".github/workflows/release.yml",
      active: "false",
      role: "standalone",
      id: "102",
      href: "https://github.com/acme/app/actions/workflows/102",
    },
  ]);
});

test("paginates workflow registries until total_count is observed", async () => {
  const requestedPages = [];
  const registries = await discoverWorkflowRegistries([{ full_name: "acme/app" }], {
    token: "test-token",
    fetchImplementation: async (url) => {
      const page = Number(new URL(String(url)).searchParams.get("page"));
      requestedPages.push(page);
      const workflows = page === 1
        ? Array.from({ length: 100 }, (_, index) => workflow(index + 1, `.github/workflows/job-${index + 1}.yml`))
        : [workflow(101, ".github/workflows/job-101.yml")];
      return new Response(JSON.stringify({ total_count: 101, workflows }));
    },
  });

  assert.deepEqual(requestedPages, [1, 2]);
  assert.equal(registries[0].state, "complete");
  assert.equal(registries[0].pages, 2);
  assert.equal(registries[0].workflows.length, 101);
});

test("excludes deleted workflows without degrading registry completeness", async () => {
  const registries = await discoverWorkflowRegistries([{ full_name: "acme/app" }], {
    token: "test-token",
    fetchImplementation: async () => new Response(JSON.stringify({
      total_count: 1,
      workflows: [workflow(101, ".github/workflows/deleted.yml", { state: "deleted" })],
    })),
  });

  assert.equal(registries[0].state, "complete");
  assert.equal(registries[0].observed, 1);
  assert.deepEqual(registries[0].workflows, []);
});

test("includes the control repository when target discovery omits it", async () => {
  const requestedRepositories = [];
  const registries = await discoverWorkflowRegistries([{ full_name: "acme/app" }], {
    token: "test-token",
    controlRepository: "acme/control",
    fetchImplementation: async (url) => {
      const repository = String(url).match(/repos\/([^/]+\/[^/]+)\/actions/)?.[1];
      assert.ok(repository);
      requestedRepositories.push(repository);
      return new Response(JSON.stringify({ total_count: 0, workflows: [] }));
    },
  });

  assert.deepEqual(requestedRepositories, ["acme/app", "acme/control"]);
  assert.deepEqual(registries.map((registry) => registry.repository), ["acme/app", "acme/control"]);
});

test("represents inaccessible repositories and workflow registries as partial evidence", async () => {
  const discoveredRepositories = await discoverRepositories({
    allowed_repositories: ["acme/app", "acme/private"],
    policy_document: { "control-plane": { inventory: { "max-scan-repositories": 10 } } },
  }, {
    token: "test-token",
    fetchImplementation: async (url) => String(url).endsWith("/repos/acme/private")
      ? new Response("", { status: 404 })
      : new Response(JSON.stringify({ full_name: "acme/app", visibility: "public" })),
  });
  const registries = await discoverWorkflowRegistries(discoveredRepositories, {
    token: "test-token",
    fetchImplementation: async (url) => String(url).includes("/repos/acme/private/")
      ? new Response("", { status: 404 })
      : new Response(JSON.stringify({
          total_count: 1,
          workflows: [workflow(101, ".github/workflows/ci.yml", { name: "CI" })],
        })),
  });
  const sources = buildInventoryDashboardSources({
    repository: "acme/control",
    generatedAt: "2026-09-17T00:00:00Z",
    inventory: { workflows: [], bundles: [] },
    controlSettings: {},
    discoveredRepositories,
    workflowRegistries: registries,
  });

  assert.equal(sources.repositories.metadata.availability, "available");
  assert.equal(sources.repositories.metadata.completeness, "partial");
  assert.equal(sources.repositories.metadata["coverage-expected"], 2);
  assert.equal(sources.repositories.metadata["coverage-observed"], 1);
  assert.equal(sources.repositories.metadata["repository-failures"][0].repository, "acme/private");
  assert.equal(sources.workflows.metadata.availability, "available");
  assert.equal(sources.workflows.metadata.completeness, "partial");
  assert.equal(sources.workflows.metadata["coverage-expected"], 2);
  assert.equal(sources.workflows.metadata["coverage-observed"], 1);
  assert.equal(sources.workflows.metadata["repository-failures"][0].repository, "acme/private");
  assert.deepEqual(sources.workflows.rows.map((row) => `${row.organization}/${row.repository}`), ["acme/app"]);
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
