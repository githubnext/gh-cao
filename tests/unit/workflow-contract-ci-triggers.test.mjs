import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join, posix } from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { parse } from "yaml";
import { root, workflow } from "./workflow-contract.helpers.mjs";

const mainOnlyWorkflows = [
  "actions.yml",
  "azure-local-integration.yml",
  "dashboard-deployed-integration.yml",
  "dashboard-query-parity.yml",
  "dashboard-views.yml",
  "mcp-parity.yml",
];
const fastWorkflows = [
  "action-lint.yml",
  "actions-runtime.yml",
  "cao-validate.yml",
  "cgo.yml",
  "cid.yml",
  "csh.yml",
  "install-windows.yml",
  "svg-contrast-check.yml",
  "workflow-contracts.yml",
];

function matchesPaths(file, patterns) {
  let matched = false;
  for (const pattern of patterns) {
    const excluded = pattern.startsWith("!");
    if (posix.matchesGlob(file, excluded ? pattern.slice(1) : pattern)) {
      matched = !excluded;
    }
  }
  return matched;
}

function runsForFile(name, event, file) {
  const triggers = parse(workflow(name)).on;
  if (!Object.hasOwn(triggers, event)) return false;
  const trigger = triggers[event];
  if (trigger?.paths) return matchesPaths(file, trigger.paths);
  if (trigger?.["paths-ignore"]) {
    return !trigger["paths-ignore"].some((pattern) => posix.matchesGlob(file, pattern));
  }
  return true;
}

test("slow integration workflows run on relevant main pushes, not pull requests", () => {
  for (const name of mainOnlyWorkflows) {
    const triggers = parse(workflow(name)).on;
    assert.equal(triggers.pull_request, undefined, name);
    assert.deepEqual(triggers.push.branches, ["main"], name);
    assert.ok(triggers.push.paths.length > 0, name);
    assert.ok(runsForFile(name, "push", `.github/workflows/${name}`), name);
    assert.equal(runsForFile(name, "push", "docs/glossary.md"), false, name);
    assert.equal(runsForFile(name, "push", "server/README.md"), false, name);
    assert.ok(Object.hasOwn(triggers, "workflow_dispatch"), name);
  }
  for (const name of ["actions.yml", "dashboard-views.yml"]) {
    assert.ok(Object.hasOwn(parse(workflow(name)).on, "workflow_call"), name);
  }
  assert.ok(parse(workflow("dashboard-deployed-integration.yml")).on.schedule.length > 0);
});

test("MCP parity keeps service-backed Go integration tests out of its contract step", () => {
  const job = parse(workflow("mcp-parity.yml")).jobs.parity;
  const steps = job.steps;
  const contract = steps.find((step) => step.name === "Run contract tests");
  const parity = steps.find((step) => step.name === "Compare MCP contracts and behavior");
  assert.match(contract.run, /env -u REDIS_URL -u CAO_POSTGRES_URL go -C server test \.\/internal\/server \.\/cmd\/cao-dashboard/);
  assert.equal(parity.run, "npm run test:e2e:dashboard-query-parity");
  assert.ok(job.env.REDIS_URL);
  assert.ok(job.env.CAO_POSTGRES_URL);
});

test("service integration harnesses provision canonical storage before starting runtime processes", () => {
  const parity = readFileSync(join(root, "tests/e2e/dashboard-query-parity.mjs"), "utf8");
  assert.match(parity, /const namespace = `query-parity-\$\{process\.pid\}`/);
  assert.match(parity, /execFileSync\(binary, \[\s*"database", "bootstrap", "--store", "canonical", "--namespace", namespace,/);
  assert.match(parity, /env: \{ \.\.\.process\.env, CAO_DATABASE_ADMIN_URL: process\.env\.CAO_POSTGRES_URL \}/);
  assert.match(parity, /"--redis-namespace", namespace/);
  assert.ok(parity.indexOf('execFileSync(binary, [') < parity.indexOf('const child = spawn(binary, ['));

  const azure = readFileSync(join(root, "scripts/azure-local/azure-local.sh"), "utf8");
  assert.match(azure, /CAO_DATABASE_ADMIN_URL="\$CAO_POSTGRES_URL" run_logged "canonical database bootstrap" database-bootstrap\.log \\\n\s+"\$APP_DIR\/cao-dashboard" database bootstrap --store canonical --namespace "\$namespace"/);
  assert.ok(azure.indexOf('"$APP_DIR/cao-dashboard" database bootstrap') < azure.indexOf('"$APP_DIR/cao-dashboard" ingest'));
  assert.doesNotMatch(azure, /export CAO_DATABASE_ADMIN_URL/);
});

test("Go dashboard integration bootstraps both stores in the runtime namespace", () => {
  const job = parse(workflow("cgo.yml")).jobs["redis-dashboard-integration"];
  const steps = job.steps;
  const bootstrap = steps.find((step) => step.name === "Bootstrap dashboard integration storage");
  const runtime = steps.find((step) => step.name === "Verify deployed shards populate dashboard views");
  assert.ok(job.env.CAO_OPERATIONAL_NAMESPACE);
  assert.deepEqual(bootstrap.env, { CAO_DATABASE_ADMIN_URL: "${{ env.CAO_POSTGRES_URL }}" });
  for (const store of ["canonical", "operational"]) {
    assert.ok(bootstrap.run.includes(
      `.tmp/cao-dashboard database bootstrap --store ${store} --namespace "$CAO_OPERATIONAL_NAMESPACE"`,
    ));
  }
  assert.ok(steps.indexOf(bootstrap) > steps.findIndex((step) => step.name === "Build dashboard and server"));
  assert.ok(steps.indexOf(bootstrap) < steps.indexOf(runtime));
  assert.match(runtime.env.DASHBOARD_SERVER_COMMAND, /--redis-namespace "\$CAO_OPERATIONAL_NAMESPACE"/);
  assert.equal(job.env.CAO_DATABASE_ADMIN_URL, undefined);
  assert.equal(runtime.env.CAO_DATABASE_ADMIN_URL, undefined);
});

test("Go dashboard integration shards complete test coverage across isolated services", () => {
  const job = parse(workflow("cgo.yml")).jobs["redis-dashboard-integration"];
  assert.deepEqual(job.strategy.matrix, { shard: [0, 1, 2, 3, 4, 5, 6, 7] });
  assert.equal(job.strategy["fail-fast"], false);
  assert.ok(job.services.postgres);
  assert.ok(job.services.redis);
  const tests = job.steps.find((step) => step.name === "Go tests with Redis and Postgres");
  assert.deepEqual(tests.env, {
    TEST_SHARD: "${{ matrix.shard }}",
    TEST_SHARD_COUNT: "${{ strategy.job-total }}",
  });
  assert.match(tests.run, /set -euo pipefail/);
  assert.match(tests.run, /test -n "\$pattern"/);
  assert.match(tests.run, /go -C server test -p 1 -run "\^\(\$pattern\)\$" \.\/\.\.\./);
  assert.doesNotMatch(tests.run, /-timeout|-short|-skip/);
  assert.equal(tests["continue-on-error"], undefined);
  assert.equal(tests.if, undefined);
  for (const name of [
    "Build dashboard and server", "Bootstrap dashboard integration storage",
    "Install Chromium", "Verify deployed shards populate dashboard views",
  ]) {
    assert.equal(job.steps.find((step) => step.name === name).if, "matrix.shard == 0", name);
  }
  assert.equal(job.steps.find((step) => step.name === "Upload integration traces").if,
    "always() && matrix.shard == 0");

  const names = [
    "TestOne", "TestTwo", "ExampleQuery", "FuzzParser", "TestThree", "TestFour",
    "TestFive", "TestSix", "TestSeven", "TestEight",
  ];
  const output = [...names.slice(0, 2), "ok example/first", ...names, "ok example/second"].join("\n");
  const selected = new Map(names.map((name) => [name, 0]));
  const mock = [
    "go() {",
    '  if [[ "$*" == *" -list "* ]]; then printf "%s\\n" "$MOCK_LIST";',
    '  else printf "%s\\n" "$7"; fi',
    "}",
  ].join("\n");
  for (const shard of job.strategy.matrix.shard) {
    const result = spawnSync("bash", ["-c", `${mock}\n${tests.run}`], {
      encoding: "utf8",
      env: {
        ...process.env, MOCK_LIST: output, TEST_SHARD: String(shard),
        TEST_SHARD_COUNT: String(job.strategy.matrix.shard.length),
      },
    });
    assert.equal(result.status, 0, result.stderr);
    const pattern = new RegExp(result.stdout.trim());
    for (const name of names) {
      if (pattern.test(name)) selected.set(name, selected.get(name) + 1);
      assert.equal(pattern.test(`${name}Suffix`), false);
    }
  }
  assert.deepEqual([...selected.values()], names.map(() => 1));
  const empty = spawnSync("bash", ["-c", `${mock}\n${tests.run}`], {
    encoding: "utf8",
    env: {
      ...process.env, MOCK_LIST: "ok example/empty", TEST_SHARD: "0",
      TEST_SHARD_COUNT: String(job.strategy.matrix.shard.length),
    },
  });
  assert.notEqual(empty.status, 0);
  const failed = spawnSync("bash", ["-c", `go() { return 23; }\n${tests.run}`], {
    encoding: "utf8",
    env: {
      ...process.env, TEST_SHARD: "0",
      TEST_SHARD_COUNT: String(job.strategy.matrix.shard.length),
    },
  });
  assert.equal(failed.status, 23);
});

test("Go extension quality checks cover root-module sources and lint configuration", () => {
  for (const event of ["pull_request", "push"]) {
    for (const file of [
      ".github/workflows/go-extension.yml",
      ".github/workflows/cao.json",
      ".github/actions/setup-gh-aw/action.yml",
      ".golangci.yml",
      "cmd/gh-cao/main.go",
      "internal/cli/doctor.go",
      "internal/cli/doctor_test.go",
      "internal/cli/doctor_auth_test.go",
      "internal/ghcli/gh.go",
      "internal/cli/native_queries.go",
      "dashboard/catalog.go",
      "dashboard/site/src/data/queries/database.json",
      "dashboard/site/src/data/queries/declarative.js",
      "dashboard/site/src/data/storage/sqlite-indexeddb.js",
      "dashboard/site/src/semantic-view-prompt.js",
      "go.mod",
      "go.sum",
      "package.json",
      "package-lock.json",
      "aw.yml",
      "server/cmd/cao-dashboard/azure_functions.go",
      "server/internal/server/azure.go",
      "server/internal/server/server.go",
      "server/internal/simulator/setup_integration_test.go",
      "server/internal/simulator/setup_hosted_cli_integration_test.go",
      "server/internal/simulator/setup_azure_integration_test.go",
      "server/internal/simulator/bootstrap_integration_test.go",
      "server/azure/main.bicep",
      "server/azure/main.json",
      "server/Dockerfile",
      "server/go.mod",
      "server/go.sum",
      "tests/unit/workflow-contract-ci-triggers.test.mjs",
    ]) {
      assert.ok(runsForFile("go-extension.yml", event, file), `${event}: ${file}`);
    }
  }

  const { jobs, defaults } = parse(workflow("go-extension.yml"));
  const job = jobs["extension-quality"];
  const steps = job.steps;
  const go = steps.find((step) => step.uses?.startsWith("actions/setup-go@"));
  const install = steps.find((step) => step.name === "Install golangci-lint");
  const format = steps.find((step) => step.name === "Check formatting");
  const lint = steps.find((step) => step.name === "Lint");
  const serverSteps = parse(workflow("cgo.yml")).jobs["server-quality"].steps;
  assert.equal(job.if, undefined);
  assert.equal(defaults?.run?.["working-directory"] ?? ".", ".");
  assert.equal(job.defaults?.run?.["working-directory"] ?? ".", ".");
  assert.equal(go.with["go-version-file"], "go.mod");
  assert.equal(install.run, serverSteps.find((step) => step.name === "Install golangci-lint").run);
  assert.ok(steps.indexOf(go) < steps.indexOf(install));
  assert.ok(steps.indexOf(install) < steps.indexOf(format));
  assert.ok(steps.indexOf(install) < steps.indexOf(lint));
  assert.equal(format["working-directory"] ?? ".", ".");
  assert.equal(lint["working-directory"] ?? ".", ".");
  assert.equal(format.shell, "bash");
  assert.equal(format.run, [
    'golangci-lint fmt --diff ./cmd/... ./internal/... ./dashboard | tee "$RUNNER_TEMP/go-extension-fmt.diff"',
    'test ! -s "$RUNNER_TEMP/go-extension-fmt.diff"',
    "",
  ].join("\n"));
  assert.equal(lint.run, "golangci-lint run ./cmd/... ./internal/... ./dashboard");
  const standalone = steps.find((step) => step.name === "Check standalone execution without Node.js");
  assert.match(standalone.run, /PATH="" "\$binary" pages --json/);
  assert.match(standalone.run, /PATH="" "\$binary" query --database explicit.sqlite --collection runs/);
  const parity = steps.find((step) => step.name === "Install JavaScript parity-oracle dependencies");
  const check = steps.find((step) => step.run === "make check");
  assert.equal(parity.run, "npm ci --ignore-scripts --no-audit --no-fund");
  assert.ok(steps.indexOf(parity) < steps.indexOf(check));
});

test("Go extension keeps authenticated doctor integration separate from unit and smoke tests", () => {
  const { jobs, permissions } = parse(workflow("go-extension.yml"));
  const quality = jobs["extension-quality"];
  assert.ok(quality.steps.some((step) => step.run === "make check"));
  assert.equal(quality.steps.some((step) => step.uses === "./.github/actions/setup-gh-aw"), false);
  assert.equal(quality.env?.GH_TOKEN, undefined);
  const smoke = quality.steps.find((step) => step.name === "Smoke test extension");
  assert.equal(smoke.env, undefined);
  assert.ok(smoke.run.split("\n").includes("./gh-cao doctor --help"));
  assert.equal(smoke.run.split("\n").includes("./gh-cao doctor"), false);

  const job = jobs["extension-authtests"];
  const steps = job.steps;
  const go = steps.find((step) => step.uses?.startsWith("actions/setup-go@"));
  const node = steps.find((step) => step.uses?.startsWith("actions/setup-node@"));
  const install = steps.find((step) => step.uses === "./.github/actions/setup-gh-aw");
  const auth = steps.find((step) => step.name === "Run authenticated doctor integration tests");
  assert.ok(install, "doctor requires the policy-pinned gh-aw installation");
  assert.equal(go.with["go-version-file"], "go.mod");
  assert.ok(steps.indexOf(node) < steps.indexOf(install));
  assert.ok(steps.indexOf(install) < steps.indexOf(auth));
  assert.deepEqual(auth.env, { GH_TOKEN: "${{ github.token }}" });
  assert.deepEqual(permissions, { contents: "read" });
  assert.equal(job.permissions, undefined);
  assert.equal(job.env?.GH_TOKEN, undefined);
  assert.equal(job.if, "github.event_name != 'pull_request'");
  assert.equal(job.needs, undefined);
  assert.equal(quality.needs, undefined);
  assert.equal(install.if, undefined);
  assert.equal(install["continue-on-error"], undefined);
  assert.equal(auth.if, undefined);
  assert.equal(auth["continue-on-error"], undefined);
  assert.equal(auth.run, "make authtests");
});

test("Go extension setup simulator runs only on main pushes, independently without live credentials", () => {
  const source = workflow("go-extension.yml");
  const { jobs, permissions, on: triggers } = parse(source);
  const job = jobs["extension-setup-simulator"];
  const steps = job.steps;
  const checkout = steps.find((step) => step.uses?.startsWith("actions/checkout@"));
  const go = steps.find((step) => step.uses?.startsWith("actions/setup-go@"));
  const install = steps.find((step) => step.uses === "./.github/actions/setup-gh-aw");
  const run = steps.find((step) => step.run === "npm run test:integration:setup");
  const azure = steps.find((step) => step.name === "Run Azure setup with official Azurite and local control-plane simulation");
  const azurePrerequisites = steps.find((step) => step.name === "Prepare offline Azure simulation prerequisites");
  assert.ok(azure);
  assert.deepEqual(azure.run.trim().split("\n"), [
    "npm run test:integration:setup-azure",
    "npm run test:integration:azure-install-journey",
  ]);
  assert.deepEqual(permissions, { contents: "read" });
  assert.equal(checkout.with["persist-credentials"], false);
  assert.equal(go.with["go-version-file"], "go.mod");
  assert.ok(go.with["cache-dependency-path"].includes("server/go.sum"));
  assert.ok(install);
  assert.ok(steps.indexOf(install) < steps.indexOf(run));
  assert.ok(steps.indexOf(install) < steps.indexOf(azure));
  assert.ok(steps.indexOf(azurePrerequisites) < steps.indexOf(azure));
  assert.match(azurePrerequisites.run, /go mod download/);
  assert.match(azurePrerequisites.run, /docker pull mcr\.microsoft\.com\/azure-storage\/azurite:3\.37\.0@sha256:[a-f0-9]{64}/);
  assert.deepEqual(run.env, {
    GH_TOKEN: "",
    GITHUB_TOKEN: "",
    GH_ENTERPRISE_TOKEN: "",
    GITHUB_ENTERPRISE_TOKEN: "",
  });
  assert.equal(job.env, undefined);
  assert.equal(job.if, "github.event_name != 'pull_request' && github.ref == 'refs/heads/main'");
  assert.deepEqual(triggers.push.branches, ["main"]);
  assert.ok(triggers.pull_request, "PR quality and authenticated doctor checks remain enabled");
  assert.equal(job.needs, undefined);
  assert.equal(run["continue-on-error"], undefined);
  assert.deepEqual(azure.env, {
    ...run.env,
    POSTGRES_URL: "postgres://postgres@127.0.0.1:5432/cao?sslmode=disable",
  });
  assert.match(job.services.postgres.image, /^postgres:16-alpine@sha256:[a-f0-9]{64}$/);
  assert.deepEqual(job.services.postgres.env, {
    POSTGRES_DB: "cao",
    POSTGRES_HOST_AUTH_METHOD: "trust",
  });
  assert.deepEqual(job.services.postgres.ports, ["5432:5432"]);
  assert.match(job.services.postgres.options, /pg_isready -U postgres -d cao/);
  assert.equal(azure.if, undefined);
  assert.equal(azure["continue-on-error"], undefined);
  assert.doesNotMatch(JSON.stringify(job), /secrets\.|github\.token|id-token/);
  const { scripts } = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
  assert.match(scripts["test:integration:setup"], /CAO_SETUP_INTEGRATION=1 go -C server test -race/);
  assert.match(scripts["test:integration:setup"], /-run '\^\(TestCAO\(Setup\|HostedCLISetup\)Integration\|TestSetup\)'/);
  assert.match(scripts["test:integration:setup-azure"], /CAO_SETUP_AZURE_INTEGRATION=1 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOVCS='\*:off' go -C server test -race/);
  assert.match(scripts["test:integration:setup-azure"], /-run '\^\(TestCAOSetupAzureIntegration\|TestSetupAzure\|TestLocalTransport\)'/);
  assert.match(scripts["test:integration:azure-install-journey"], /CAO_SETUP_AZURE_INTEGRATION=1 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOVCS='\*:off' go -C server test -race/);
  assert.match(scripts["test:integration:azure-install-journey"], /-run '\^TestCAOAzureInstallJourneyIntegration\$'/);
});

test("Docker samples runtime has an independent read-only workflow", () => {
  const runtime = parse(workflow("actions-runtime.yml"));
  const contracts = parse(workflow("workflow-contracts.yml"));
  for (const pattern of ["server/**", "!server/**/*.md", "activity/**", "dashboard/site/src/data/**",
    "tests/helpers/actions-runtime/**", "tests/integration/*actions-runtime.test.mjs", ".github/workflows/cao-activity.yml"]) {
    assert.ok(runtime.on.pull_request.paths.includes(pattern), pattern);
  }
  assert.deepEqual(runtime.on.push.paths, runtime.on.pull_request.paths);
  assert.deepEqual(runtime.on.push.branches, ["main"]);
  assert.ok(Object.hasOwn(runtime.on, "workflow_dispatch"));
  assert.deepEqual(runtime.permissions, { contents: "read" });
  assert.deepEqual(Object.keys(runtime.jobs), ["actions-runtime"]);
  assert.equal(contracts.jobs["actions-runtime"], undefined);
  assert.notEqual(runtime.concurrency.group, contracts.concurrency.group);
  assert.equal(runtime.concurrency["cancel-in-progress"], contracts.concurrency["cancel-in-progress"]);
  const job = runtime.jobs["actions-runtime"];
  assert.equal(job.needs, undefined);
  assert.equal(job.if, undefined);
  assert.equal(job["timeout-minutes"], 10);
  assert.ok(job.steps.some(step => step.run === "npm run test:integration:actions-runtime"));
  const diagnostics = job.steps.find(step => step.name === "Retain failed simulator diagnostics");
  assert.equal(diagnostics.if, "failure()");
  assert.equal(diagnostics.with["retention-days"], 7);
});

test("Docker runtime runs automatically for simulator, Activity and shared data-boundary changes", () => {
  for (const event of ["pull_request", "push"]) {
    for (const file of [
      "server/internal/webhookgateway/worker.go",
      "server/internal/operational/postgres/webhook_schema.sql",
      "server/go.mod",
      "server/go.sum",
      "tests/helpers/actions-runtime/runner.mjs",
      "tests/integration/activity-actions-runtime.test.mjs",
      "activity/collect-logs.sh",
      "dashboard/site/src/data/model/daily-run-counts.js",
      ".github/workflows/actions-runtime.yml",
      ".github/workflows/cao-activity.yml",
      ".github/workflows/shared/control.md",
    ]) {
      assert.ok(runsForFile("actions-runtime.yml", event, file), `${event}: ${file}`);
    }
    for (const file of [
      "server/README.md",
      "server/internal/webhookgateway/README.md",
      "docs/glossary.md",
      "dashboard/site/src/dashboard-app.js",
      "cmd/gh-cao/main.go",
      "go.mod",
    ]) {
      assert.equal(runsForFile("actions-runtime.yml", event, file), false, `${event}: ${file}`);
    }
  }
});

test("pull request triggers route frontend, control, server, and test changes selectively", () => {
  const cases = [
    ["dashboard/site/src/dashboard-app.js", ["cgo.yml", "cid.yml"]],
    ["dashboard/site/dashboard-fragments/overview.json", ["cgo.yml", "cid.yml"]],
    ["dashboard/site/README.md", []],
    ["docs/glossary.md", ["workflow-contracts.yml"]],
    ["docs/assets/logo-day.svg", ["cid.yml", "svg-contrast-check.yml", "workflow-contracts.yml"]],
    ["public/favicon.svg", ["cid.yml", "workflow-contracts.yml"]],
    ["activity/dashboard-complexity.mjs", ["actions-runtime.yml", "cgo.yml", "workflow-contracts.yml"]],
    ["tests/e2e/dashboard-ingestion-scale.spec.mjs", ["workflow-contracts.yml"]],
    ["server/internal/query/query.go", ["actions-runtime.yml", "cgo.yml", "workflow-contracts.yml"]],
    ["server/spec/main.tsp", ["actions-runtime.yml", "cgo.yml", "workflow-contracts.yml"]],
    ["scripts/run-daily-run-counts-conformance.mjs", ["cgo.yml", "workflow-contracts.yml"]],
    ["tests/contracts/daily-run-counts.json", ["cgo.yml", "workflow-contracts.yml"]],
    ["server/README.md", ["workflow-contracts.yml"]],
    [".github/workflows/shared/control.md", ["actions-runtime.yml", "cao-validate.yml", "workflow-contracts.yml"]],
    [".github/workflows/cid.yml", ["action-lint.yml", "cid.yml"]],
    [".github/workflows/shared/ci.yml", ["action-lint.yml", "actions-runtime.yml", "cao-validate.yml", "workflow-contracts.yml"]],
    [".github/workflows/actions.yml", ["action-lint.yml", "workflow-contracts.yml"]],
    [".github/workflows/actions-runtime.yml", ["action-lint.yml", "actions-runtime.yml", "workflow-contracts.yml"]],
    [".github/workflows/cao-evolution.lock.yml", ["cao-validate.yml", "workflow-contracts.yml"]],
  ];
  for (const [file, expected] of cases) {
    const actual = [...fastWorkflows, ...mainOnlyWorkflows]
      .filter((name) => runsForFile(name, "pull_request", file))
      .sort();
    assert.deepEqual(actual, expected, file);
  }
});

test("main pushes retain browser and integration coverage for representative inputs", () => {
  const cases = [
    ["actions.yml", "tests/e2e/dashboard-mobile-live.spec.mjs"],
    ["actions.yml", "server/cmd/cao-dashboard/serve_pages.go"],
    ["actions.yml", "tests/helpers/dashboard-pages-preview.mjs"],
    ["actions.yml", "tests/e2e/dashboard-tree-analysis.mjs"],
    ["actions.yml", "self-care/dashboard-fragments/overview.json"],
    ["azure-local-integration.yml", "scripts/azure-local/azure-local.sh"],
    ["dashboard-deployed-integration.yml", "server/go.mod"],
    ["dashboard-deployed-integration.yml", "dashboard/site/package-lock.json"],
    ["dashboard-query-parity.yml", "server/internal/query/query.go"],
    ["dashboard-views.yml", "self-care/dashboard.json"],
    ["dashboard-views.yml", "tests/e2e/dashboard-views-live.spec.mjs"],
    ["dashboard-views.yml", "tests/e2e/dashboard-deployed-refresh-helpers.mjs"],
    ["dashboard-views.yml", "dashboard/report/bundle-dashboards.mjs"],
    ["mcp-parity.yml", "activity/mcp-server.mjs"],
    ["cgo.yml", "dashboard/site/src/dashboard-app.js"],
    ["cid.yml", "tests/e2e/dashboard-ingestion-scale.spec.mjs"],
  ];
  for (const [name, file] of cases) {
    assert.ok(runsForFile(name, "push", file), `${name}: ${file}`);
  }
  assert.equal(parse(workflow("actions.yml")).jobs.mobile.if, undefined);
});

test("mixed workflows keep fast checks on PRs and gate expensive jobs off PRs", () => {
  const cases = [
    ["workflow-contracts.yml", ["unit", "test"], [
      "agent-plugin", "installer",
      "full-contracts", "github-issue-query",
      "dependabot-operational-value", "campaign-lifecycle-changes",
    ]],
    ["cgo.yml", ["server-quality", "server-spec"], [
      "windows-local-server", "actions-simulator-postgres",
      "server-backfill", "server-stress", "redis-dashboard-integration",
    ]],
    ["cid.yml", ["lint-unit", "dead-views"], [
      "pages", "playwright-integration", "query-complexity", "lighthouse-performance",
    ]],
  ];
  for (const [name, fastJobs, mainJobs] of cases) {
    const { jobs, concurrency } = parse(workflow(name));
    for (const id of fastJobs) {
      assert.doesNotMatch(jobs[id].if ?? "", /event_name != 'pull_request'/, `${name}: ${id}`);
    }
    for (const id of mainJobs) {
      assert.equal(jobs[id].if, "github.event_name != 'pull_request'", `${name}: ${id}`);
    }
    assert.match(concurrency.group, /github\.event\.pull_request\.number \|\| github\.ref/, name);
    assert.equal(concurrency["cancel-in-progress"], "${{ github.event_name == 'pull_request' }}", name);
  }
  const contracts = parse(workflow("workflow-contracts.yml")).jobs;
  assert.match(contracts["campaign-lifecycle"].if, /github\.event_name != 'pull_request'/);
  assert.equal(contracts["fast-contracts"].needs, undefined, "fast checks run in parallel with unit areas");
  assert.deepEqual(contracts.test.needs, ["changes", "unit", "fast-contracts"]);
  assert.equal(contracts.test.if, "always()");
});

test("PR reporters do not start for skipped main-only producers", () => {
  for (const [name, reports] of [
    ["cid.yml", {
      "query-complexity-comment": "query-complexity",
      "lighthouse-comment": "lighthouse-performance",
    }],
    ["cgo.yml", { "redis-dashboard-comment": "redis-dashboard-integration" }],
  ]) {
    const { jobs } = parse(workflow(name));
    for (const [report, producer] of Object.entries(reports)) {
      assert.ok(jobs[report].if.includes(`needs.${producer}.result != 'skipped'`));
    }
  }
});
