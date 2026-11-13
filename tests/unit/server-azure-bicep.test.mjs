import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const template = JSON.parse(await readFile(new URL('../../internal/topology/assets/container-apps/main.generated.json', import.meta.url), 'utf8'));
const resources = template.resources;
const moduleFor = (name) => resources.find((resource) => resource.name === name).properties.template;
const resourcesFor = (name) => Object.values(moduleFor(name).resources);
const provisioning = resourcesFor('container-apps-provisioning');
const ingestion = resourcesFor('container-apps-ingestion');
const provisionScript = await readFile(new URL('../../server/azure/provision.sh', import.meta.url), 'utf8');

test('Container Apps starts only the reviewed hosted Go runtime after explicit activation', () => {
  const web = resources.find((resource) => resource.type === 'Microsoft.App/containerApps');
  assert.equal(template.parameters.runtimeEnabled.defaultValue, false);
  assert.equal(web.condition, "[parameters('runtimeEnabled')]");
  assert.equal(web.properties.configuration.ingress.allowInsecure, false);
  assert.equal(web.properties.configuration.ingress.targetPort, 8080);
  assert.deepEqual(web.properties.template.scale, { minReplicas: 1, maxReplicas: 1 });
  const container = web.properties.template.containers[0];
  assert.equal(container.args[0], 'serve-hosted');
  assert.ok(container.args.includes('postgres'));
  const settings = Object.fromEntries(container.env.map((entry) => [entry.name, entry]));
  assert.equal(settings.CAO_EXTERNAL_INGESTION.value, 'true');
  assert.equal(settings.CAO_POSTGRES_URL.secretRef, "[variables('postgresUrlSecretName')]");
  for (const name of ['CAO_DATABASE_ADMIN_URL', 'CAO_POSTGRES_OWNER_PASSWORD', 'CAO_POSTGRES_RUNTIME_PASSWORD']) {
    assert.equal(settings[name], undefined);
  }
  assert.doesNotMatch(JSON.stringify(template), /Microsoft\.Web\/|FUNCTIONS_WORKER_RUNTIME|AzureWebJobsStorage/);
});

test('Provisioning and ingestion are manual jobs with separate identities and credential scopes', () => {
  for (const children of [provisioning, ingestion]) {
    const job = children.find((resource) => resource.type === 'Microsoft.App/jobs');
    assert.equal(job.properties.configuration.triggerType, 'Manual');
    assert.equal(job.properties.configuration.replicaRetryLimit, 0);
    assert.equal(job.identity.type, 'UserAssigned');
  }
  const provisionRoles = provisioning.find((resource) => resource.name === 'database-provisioning-roles');
  const ingestionRoles = ingestion.find((resource) => resource.name === 'ingestion-roles');
  assert.match(provisionRoles.properties.parameters.secretNames.value, /variables\('secretNames'\)/);
  assert.deepEqual(moduleFor('container-apps-provisioning').variables.secretNames, [
    "[format('{0}-admin', parameters('postgresUrlSecretName'))]",
    "[format('{0}-owner-password', parameters('postgresUrlSecretName'))]",
    "[format('{0}-runtime-password', parameters('postgresUrlSecretName'))]",
  ]);
  assert.deepEqual(ingestionRoles.properties.parameters.secretNames.value, ["[parameters('postgresUrlSecretName')]"]);
  assert.doesNotMatch(JSON.stringify(ingestion), /CAO_DATABASE_ADMIN_URL|CAO_POSTGRES_OWNER_PASSWORD|CAO_POSTGRES_RUNTIME_PASSWORD/);
  const webRoles = resources.find((resource) => resource.name === 'container-apps-web-roles');
  assert.doesNotMatch(webRoles.properties.parameters.secretNames.value, /-admin|owner-password|runtime-password/);
});

test('Private PostgreSQL and verified provisioning TLS remain mandatory', () => {
  const postgres = resourcesFor('container-apps-postgres').find((resource) => resource.type === 'Microsoft.DBforPostgreSQL/flexibleServers');
  assert.equal(postgres.properties.network.publicNetworkAccess, 'Disabled');
  const job = provisioning.find((resource) => resource.type === 'Microsoft.App/jobs');
  const settings = Object.fromEntries(job.properties.template.containers[0].env.map((entry) => [entry.name, entry.value]));
  assert.equal(settings.PGSSLMODE, 'verify-full');
  assert.equal(settings.PGSSLROOTCERT, 'system');
  assert.match(provisionScript, /ON_ERROR_STOP/);
});

test('Every hosted role preserves the same explicit retained-evidence window', () => {
  for (const children of [resources, provisioning, ingestion]) {
    const role = children.find((resource) => ['Microsoft.App/containerApps', 'Microsoft.App/jobs'].includes(resource.type));
    const settings = Object.fromEntries(role.properties.template.containers[0].env.map((entry) => [entry.name, entry.value]));
    assert.equal(settings.CAO_POSTGRES_RUN_RETENTION_DAYS, "[string(parameters('postgresRunRetentionDays'))]");
    assert.equal(settings.CAO_POSTGRES_LINKED_RETENTION_DAYS, settings.CAO_POSTGRES_RUN_RETENTION_DAYS);
  }
});
