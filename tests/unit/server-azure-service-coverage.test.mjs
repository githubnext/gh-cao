import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
import test from "node:test";

// This is an inventory guard, not evidence that ARM or managed services ran.
const coverage = {
  "Microsoft.Web/sites": "Functions Core Tools",
  "Microsoft.Web/serverfarms": "deployment contract only",
  "Microsoft.Storage/storageAccounts": "Azurite Blob, Queue and Table",
  "Microsoft.DBforPostgreSQL/flexibleServers": "PostgreSQL 16",
  "Microsoft.DBforPostgreSQL/flexibleServers/databases": "PostgreSQL 16",
  "Microsoft.ContainerRegistry/registries": "Azure CLI protocol simulator",
  "Microsoft.App/managedEnvironments": "deployment contract only",
  "Microsoft.App/containerApps": "deployment contract only",
  "Microsoft.App/jobs": "Azure CLI job protocol simulator",
  "Microsoft.KeyVault/vaults": "local identity/secret protocol fixture",
  "Microsoft.KeyVault/vaults/secrets": "local identity/secret protocol fixture",
  "Microsoft.ManagedIdentity/userAssignedIdentities": "local identity protocol fixture",
  "Microsoft.Authorization/roleAssignments": "deployment contract only",
  "Microsoft.OperationalInsights/workspaces": "deployment contract only",
  "Microsoft.Insights/components": "OTLP protocol receiver, not Application Insights",
  "Microsoft.Insights/diagnosticSettings": "deployment contract only",
  "Microsoft.Network/virtualNetworks": "deployment contract only",
  "Microsoft.Network/privateDnsZones": "deployment contract only",
  "Microsoft.Network/privateDnsZones/virtualNetworkLinks": "deployment contract only",
  "Microsoft.Network/privateEndpoints": "deployment contract only",
  "Microsoft.Network/privateEndpoints/privateDnsZoneGroups": "deployment contract only",
  "Microsoft.Kusto/clusters": "optional official Kusto engine",
  "Microsoft.Kusto/clusters/databases": "optional official Kusto engine",
};
const directory = new URL("../../server/azure/", import.meta.url);
const bicepTypes = new Set();
for (const name of await readdir(directory)) {
  if (!name.endsWith(".bicep")) continue;
  const source = await readFile(new URL(name, directory), "utf8");
  for (const match of source.matchAll(/^\s*resource\s+\w+\s+'([^'@]+)@[^']+'/gm)) {
    bicepTypes.add(match[1]);
  }
}
const generated = JSON.parse(await readFile(new URL("main.json", directory), "utf8"));
const compiledTypes = new Set();
function visit(value) {
  if (!value || typeof value !== "object") return;
  if (typeof value.type === "string" && value.type.startsWith("Microsoft.")
      && value.type !== "Microsoft.Resources/deployments") compiledTypes.add(value.type);
  for (const child of Object.values(value)) visit(child);
}
visit(generated);

test("Every Bicep service has an explicit local mechanism or unavailable managed-service boundary", () => {
  assert.deepEqual([...bicepTypes].sort(), Object.keys(coverage).sort(),
    "new or removed Bicep resource types require a local-coverage review");
  assert.deepEqual([...compiledTypes].sort(), [...bicepTypes].sort(),
    "compiled resources must match the reviewed Bicep service inventory");
});

const documentation = await readFile(new URL("../../scripts/azure-local/README.md", import.meta.url), "utf8");
for (const [type, mechanism] of Object.entries(coverage)) {
  test(`${type}: ${mechanism}`, () => {
    assert.ok(documentation.includes(`\`${type}\``), "document the service's local coverage and limitations");
  });
}
