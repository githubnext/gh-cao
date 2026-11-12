import { execFile } from "node:child_process";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import test from "node:test";
import { goCase } from "../helpers/azure-local-go.mjs";

test("Azure deployment protocols: registry, jobs, Functions configuration and ARM preflight", async () => {
  await goCase("", ["./internal/cli"], [
    "^TestDeployAzure(ImageBindingsFailClosed|ImageReadBackBlocksJobsOnMismatch)$",
    "^TestDeployAzureJobWaitCancellation$",
    "^TestDeployAzureApplyFailsClosedWhenKeyVaultReferencesStayUnresolved$",
    "^TestDeployAzure(PlanRunsOnlyReadOnlyCommands|PreflightChecks|PreflightHealthy)$",
  ]);
});

test("Azure networking, RBAC and optional ADX: exhaustive Bicep contract coverage", async () => {
  await promisify(execFile)(process.execPath, [
    "--test", "tests/unit/server-azure-service-coverage.test.mjs",
    "tests/unit/server-azure-bicep.test.mjs", "tests/unit/server-azure-adx.test.mjs",
  ], { cwd: fileURLToPath(new URL("../../", import.meta.url)), timeout: 30_000, maxBuffer: 4 << 20 });
});
