import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const bicep = await readFile(new URL('../../server/azure/main.bicep', import.meta.url), 'utf8');
const collector = await readFile(new URL('../../server/azure/collector.bicep', import.meta.url), 'utf8');
const functionRoles = await readFile(new URL('../../server/azure/function-roles.bicep', import.meta.url), 'utf8');
const generated = JSON.parse(await readFile(new URL('../../server/azure/main.json', import.meta.url), 'utf8'));
const resources = Object.values(generated.resources);
const profile = JSON.parse(await readFile(new URL('../../.github/workflows/cao.azure.json', import.meta.url), 'utf8'));
const network = await readFile(new URL('../../server/azure/network.bicep', import.meta.url), 'utf8');
const ingestion = await readFile(new URL('../../server/azure/ingestion.bicep', import.meta.url), 'utf8');
const provisioning = await readFile(new URL('../../server/azure/provisioning.bicep', import.meta.url), 'utf8');
const provisionScript = await readFile(new URL('../../server/azure/provision.sh', import.meta.url), 'utf8');
const provisionSql = await readFile(new URL('../../server/azure/provision.sql', import.meta.url), 'utf8');
const dockerfile = await readFile(new URL('../../server/Dockerfile', import.meta.url), 'utf8');
const deploymentDocs = await readFile(new URL('../../docs/deployment-azure.md', import.meta.url), 'utf8');

test('Azure uses the consolidated dashboard subcommand without changing image contracts', () => {
  const runtime = dockerfile.split(' AS azure-functions-runtime\n')[1]?.split('\nFROM ')[0];
  assert.ok(runtime, 'the supported Functions image target must remain available');
  assert.match(runtime, /COPY --from=server-build --chown=cao:cao \/out\/cao-dashboard \/app\/cao-dashboard/);
  assert.match(runtime, /^ENTRYPOINT \["\/app\/cao-dashboard"\]$/m);
  assert.match(runtime, /^CMD \["azure-functions"\]$/m);
  const app = resources.find((resource) => resource.type === 'Microsoft.Web/sites');
  assert.equal(app.properties.siteConfig.appCommandLine, 'azure-functions');
  assert.match(bicep, /appCommandLine: 'azure-functions'/);
  assert.match(runtime, /FUNCTIONS_CUSTOMHANDLER_HOST=0\.0\.0\.0/);
  assert.match(runtime, /FUNCTIONS_CUSTOMHANDLER_PORT=8080/);
  assert.doesNotMatch(dockerfile, /\/out\/cao-functions|\/app\/cao-functions|\.\/cmd\/cao-functions/);
  assert.equal(generated.parameters.functionImageRepository.defaultValue, 'cao-functions');
  assert.match(bicep, /param functionImageRepository string = 'cao-functions'/);
});

test('Derived Container Apps names are bounded before Azure creates any resources', () => {
  assert.equal(generated.parameters.functionAppName.minLength, 2);
  assert.equal(generated.parameters.functionAppName.maxLength, 19);
  for (const [moduleName, limit, suffixes] of [
    ['database-provisioning', 19, ['-db-provision']],
    ['snapshot-ingestion', 19, ['-ingest']],
    ['collection-workers', 22, ['-collector', '-backfill']],
  ]) {
    const template = resources.find((resource) => resource.name === moduleName)?.properties.template;
    assert.ok(template, `${moduleName} must have a compiled template`);
    assert.equal(template.parameters.namePrefix.maxLength, limit);
    const children = Object.values(template.resources);
    for (const suffix of suffixes) {
      assert.ok(children.some((resource) => ['Microsoft.App/jobs', 'Microsoft.App/containerApps'].includes(resource.type)
        && resource.name.includes(suffix)), `${moduleName} must declare ${suffix}`);
      assert.ok(limit + suffix.length <= 32, `${moduleName}${suffix} exceeds the Azure name limit`);
    }
    for (const resource of children.filter((resource) => resource.type === 'Microsoft.ManagedIdentity/userAssignedIdentities')) {
      const suffix = resource.name.match(/\{0\}([^']+)'/)?.[1];
      assert.ok(suffix, `expected an explicit identity suffix: ${resource.name}`);
      assert.ok(limit + suffix.length <= 128, 'managed identity name exceeds the Azure name limit');
    }
  }
});

test('Key Vault purge protection remains default-on with an explicit disposable opt-out', () => {
  assert.equal(generated.parameters.keyVaultPurgeProtection.type, 'bool');
  assert.equal(generated.parameters.keyVaultPurgeProtection.defaultValue, true);
  const vault = resources.find((resource) => resource.type === 'Microsoft.KeyVault/vaults');
  assert.equal(vault.properties.enablePurgeProtection, "[if(parameters('keyVaultPurgeProtection'), true(), null())]");
  assert.equal(vault.properties.softDeleteRetentionInDays, 90);
  assert.match(bicep, /enablePurgeProtection: keyVaultPurgeProtection \? true : null/);
});

test('Native source-build digest selectors bind each distinct runtime and job target', () => {
  for (const role of ['function', 'ingestion', 'provisioning']) {
    assert.equal(generated.parameters[`${role}ImageDigest`].type, 'string');
    assert.equal(generated.parameters[`${role}ImageDigest`].defaultValue, '');
    assert.equal(generated.variables[`${role}ImageSelector`],
      `[if(empty(parameters('${role}ImageDigest')), format(':{0}', parameters('${role}ImageTag')), format('@{0}', parameters('${role}ImageDigest')))]`);
  }
  const app = resources.find((resource) => resource.type === 'Microsoft.Web/sites');
  assert.match(app.properties.siteConfig.linuxFxVersion, /variables\('functionImageSelector'\)/);
  assert.match(generated.outputs.functionContainerImage.value, /variables\('functionImageSelector'\)/);
  for (const [moduleName, role] of [['snapshot-ingestion', 'ingestion'], ['database-provisioning', 'provisioning']]) {
    const module = resources.find((resource) => resource.name === moduleName);
    assert.match(module.properties.parameters[`${role}Image`].value, new RegExp(`variables\\('${role}ImageSelector'\\)`));
    assert.match(module.properties.parameters[`${role}Image`].value, new RegExp(`parameters\\('${role}ImageRepository'\\)`));
    const job = Object.values(module.properties.template.resources).find((resource) => resource.type === 'Microsoft.App/jobs');
    assert.equal(job.properties.template.containers.length, 1);
    assert.equal(job.properties.template.containers[0].image, `[parameters('${role}Image')]`);
  }
});

test('Documented bootstrap supplies required inputs and explicit reviewed retention', () => {
  const command = deploymentDocs.match(/```bash\n(\s*az deployment group create[\s\S]*?)```/)?.[1];
  assert.ok(command, 'expected the documented bootstrap command');
  const supplied = new Set([...command.matchAll(/^\s+(\w+)=/gm)].map((match) => match[1]));
  for (const secret of ['githubClientSecret', 'sessionSecret', 'postgresAdministratorPassword', 'postgresOwnerPassword', 'postgresRuntimePassword']) {
    supplied.add(secret);
    assert.match(deploymentDocs, new RegExp(`\`${secret}\``));
  }
  assert.equal(supplied.has('postgresRunRetentionDays'), true);
  const missing = Object.entries(generated.parameters)
    .filter(([name, parameter]) => !supplied.has(name)
      && !Object.hasOwn(parameter, 'defaultValue'))
    .map(([name]) => name);
  assert.deepEqual(missing, [], 'the bootstrap must supply every parameter without a default');
});

test('Azure ingress defaults to public main site behind GitHub sign-in with default-deny SCM', () => {
  assert.equal(generated.parameters.functionPublicNetworkAccess.defaultValue, 'Enabled');
  assert.deepEqual(generated.parameters.functionPublicNetworkAccess.allowedValues, ['Disabled', 'Enabled']);
  assert.equal(generated.parameters.functionAccessRestrictionsDefaultAction.type, 'string');
  assert.equal(generated.parameters.functionAccessRestrictionsDefaultAction.defaultValue, 'Allow');
  assert.deepEqual(generated.parameters.functionAccessRestrictionsDefaultAction.allowedValues, ['Allow', 'Deny']);
  assert.deepEqual(generated.parameters.functionAccessRestrictions.defaultValue, []);
  assert.deepEqual(generated.parameters.functionScmAccessRestrictions.defaultValue, []);
  assert.equal(generated.parameters.functionScmAccessRestrictionsUseMain.type, 'bool');
  assert.equal(generated.parameters.functionScmAccessRestrictionsUseMain.defaultValue, false);
  const app = resources.find((resource) => resource.type === 'Microsoft.Web/sites');
  assert.equal(app.properties.publicNetworkAccess, "[parameters('functionPublicNetworkAccess')]");
  assert.equal(app.properties.siteConfig.ipSecurityRestrictionsDefaultAction, "[parameters('functionAccessRestrictionsDefaultAction')]");
  assert.equal(app.properties.siteConfig.scmIpSecurityRestrictionsDefaultAction, 'Deny');
  assert.equal(app.properties.siteConfig.scmIpSecurityRestrictionsUseMain, "[parameters('functionScmAccessRestrictionsUseMain')]");
  assert.equal(app.properties.siteConfig.scmIpSecurityRestrictions, "[parameters('functionScmAccessRestrictions')]");
  assert.equal(app.properties.siteConfig.ipSecurityRestrictions, "[parameters('functionAccessRestrictions')]");
});

test('Documented public-main opt-in uses Allow with no custom rules and independent default-deny SCM', () => {
  const example = deploymentDocs.match(/#### Explicit public-main access with independently restricted SCM[\s\S]*?```json\n([\s\S]*?)```/)?.[1];
  assert.ok(example, 'expected a generic public-main parameter example');
  const parameters = JSON.parse(example);
  assert.deepEqual(parameters.functionPublicNetworkAccess, { value: 'Enabled' });
  assert.deepEqual(parameters.functionAccessRestrictionsDefaultAction, { value: 'Allow' });
  assert.deepEqual(parameters.functionAccessRestrictions, { value: [] });
  assert.deepEqual(parameters.functionScmAccessRestrictionsUseMain, { value: false });
  assert.deepEqual(parameters.functionScmAccessRestrictions.value, [{
    name: 'approved-admin-network',
    ipAddress: '192.0.2.0/24',
    action: 'Allow',
    priority: 100,
  }]);
  const app = resources.find((resource) => resource.type === 'Microsoft.Web/sites');
  const config = app.properties.siteConfig;
  for (const [property, parameter] of [
    ['ipSecurityRestrictionsDefaultAction', 'functionAccessRestrictionsDefaultAction'],
    ['ipSecurityRestrictions', 'functionAccessRestrictions'],
    ['scmIpSecurityRestrictions', 'functionScmAccessRestrictions'],
    ['scmIpSecurityRestrictionsUseMain', 'functionScmAccessRestrictionsUseMain'],
  ]) {
    assert.equal(config[property], `[parameters('${parameter}')]`);
    assert.equal(Object.hasOwn(parameters, parameter), true);
  }
  assert.equal(app.properties.publicNetworkAccess, "[parameters('functionPublicNetworkAccess')]");
  assert.equal(config.scmIpSecurityRestrictionsDefaultAction, 'Deny');
  assert.equal(JSON.stringify(generated).split("[parameters('functionAccessRestrictionsDefaultAction')]").length - 1, 1,
    'the opt-in default action must be wired only to the main site');
  assert.equal(app.properties.httpsOnly, true);
  assert.equal(config.minTlsVersion, '1.2');
  assert.equal(config.ftpsState, 'Disabled');
});

test('Every Azure role explicitly preserves the run window for linked evidence', () => {
  for (const source of [bicep, ingestion, collector]) {
    assert.match(source, /name:\s*'CAO_POSTGRES_LINKED_RETENTION_DAYS'\s+value:\s*string\(postgresRunRetentionDays\)/);
  }
  const app = resources.find((resource) => resource.type === 'Microsoft.Web/sites');
  for (const setting of ['CAO_POSTGRES_RUN_RETENTION_DAYS', 'CAO_POSTGRES_LINKED_RETENTION_DAYS']) {
    assert.ok(app.properties.siteConfig.appSettings.includes(`createObject('name', '${setting}', 'value', string(parameters('postgresRunRetentionDays')))`));
  }
  let checkedContainers = 0;
  for (const name of ['snapshot-ingestion', 'collection-workers']) {
    const module = resources.find((resource) => resource.name === name);
    assert.equal(module.properties.parameters.postgresRunRetentionDays.value, "[parameters('postgresRunRetentionDays')]");
    const template = module.properties.template;
    const workers = Object.values(template.resources).filter((resource) =>
      ['Microsoft.App/jobs', 'Microsoft.App/containerApps'].includes(resource.type));
    assert.equal(workers.length, name === 'snapshot-ingestion' ? 1 : 2);
    for (const worker of workers) {
      for (const container of worker.properties.template.containers) {
        const environment = Array.isArray(container.env) ? container.env : template.variables.collectionBaseEnvironment;
        if (!Array.isArray(container.env)) {
          assert.equal(container.env, "[variables('collectionEnvironment')]");
          assert.match(template.variables.collectionEnvironment, /concat\(variables\('collectionBaseEnvironment'\), parameters\('otlpEnvironment'\)/);
        }
        for (const setting of ['CAO_POSTGRES_RUN_RETENTION_DAYS', 'CAO_POSTGRES_LINKED_RETENTION_DAYS']) {
          assert.deepEqual(environment.filter((entry) => entry.name === setting), [{
            name: setting,
            value: "[string(parameters('postgresRunRetentionDays'))]",
          }], `${container.name} must preserve ${setting}`);
        }
        checkedContainers++;
      }
    }
  }
  assert.equal(checkedContainers, 3, 'ingestion, collector and backfill must each receive retention settings');
});

test('Azure selects PostgreSQL operational storage without Redis', () => {
  assert.equal(profile.extends, 'cao.json');
  assert.deepEqual(profile['control-plane'].web.host, {
    target: { module: 'azure-functions' },
    'operational-store': { backend: 'postgres' },
  });
  assert.doesNotMatch(bicep, /Microsoft\.Cache|CAO_REDIS_URL|redisEnterprise|redisConnectionString/i);
  assert.doesNotMatch(collector, /redis-streams|cao-redis|CAO_REDIS_URL/i);
  assert.match(bicep, /name:\s*'CAO_POLICY_PATH'\s+value:\s*'\.github\/workflows\/cao\.azure\.json'/);
  assert.match(collector, /name:\s*'CAO_POLICY_PATH'\s+value:\s*'\/app\/\.github\/workflows\/cao\.azure\.json'/);
});

  test('Azure baseline provisions one shared telemetry pair and private PostgreSQL', () => {
    const count = (type) => resources.filter((resource) => resource.type === type).length;
    assert.equal(count('Microsoft.Insights/components'), 1);
    assert.equal(count('Microsoft.OperationalInsights/workspaces'), 1);
    assert.equal(count('Microsoft.Storage/storageAccounts'), 1);
    assert.equal(count('Microsoft.App/managedEnvironments'), 1);
    assert.match(bicep, /WorkspaceResourceId:\s*workspace\.id/);
    assert.match(bicep, /workspaceId:\s*workspace\.id/);
    assert.match(bicep, /virtualNetworkSubnetId:\s*network\.outputs\.functionSubnetId/);
    assert.match(bicep, /infrastructureSubnetId:\s*network\.outputs\.containerSubnetId/);
    assert.match(bicep, /sslmode=verify-full/);
    assert.match(network, /Microsoft\.DBforPostgreSQL\/flexibleServers@/);
    assert.match(network, /publicNetworkAccess:\s*'Disabled'/);
    assert.match(network, /Microsoft\.Network\/privateDnsZones@/);
    assert.match(network, /backupRetentionDays:\s*7/);
    assert.doesNotMatch(network, /githubClient|sessionSecret|Microsoft\.Web\/sites@/);
    assert.doesNotMatch(collector, /resource environment '[^']+' = \{/);
  });

  test('Private ingestion and collection pull ACR images with managed identity', () => {
    assert.match(ingestion, /triggerType:\s*'Manual'/);
    assert.match(ingestion, /registries:\s*\[/);
    assert.match(ingestion, /server:\s*registry\.properties\.loginServer/);
    assert.match(ingestion, /identity:\s*identity\.id/);
    assert.match(ingestion, /keyVaultUrl:.*secrets\/\$\{postgresUrlSecretName\}/);
    assert.match(ingestion, /'--redis-namespace'\s+canonicalNamespace/);
    assert.match(ingestion, /param canonicalNamespace string = 'azure-dashboard'/);
    assert.match(ingestion, /dependsOn:\s*\[roles\]/);
    assert.equal((collector.match(/registries:\s*registries/g) ?? []).length, 2);
    assert.equal((collector.match(/dependsOn:\s*\[roles\]/g) ?? []).length, 2);
    assert.equal((collector.match(/'--database-queries', '\/app\/queries\/database.json'/g) ?? []).length, 2);
    assert.match(collector, /name:\s*'CAO_COLLECT_CATALOG_ROOT'\s+value:\s*'\/app\/catalog'/);
    assert.match(collector, /name:\s*'CAO_POSTGRES_RUN_RETENTION_DAYS'/);
  });

  test('Azure image inputs are explicit and default Docker target remains hosted dashboard', () => {
    const stages = dockerfile.split('\n').filter((line) => line.startsWith('FROM '));
    assert.equal(stages.at(-1), 'FROM dashboard-runtime AS final');
    assert.doesNotMatch(dockerfile, /cao\.mjs download/);
    assert.match(dockerfile, /FROM scratch AS cao-ingest-source\nARG CAO_INGEST_SOURCE\nCOPY \$\{CAO_INGEST_SOURCE:-\.cao-ingest-source-required\}\/ \/\n/);
    assert.match(dockerfile, /COPY --from=cao-ingest-source --chown=cao:cao \/ \/data\//);
    assert.match(dockerfile, /FROM dashboard-runtime AS azure-empty-ingest-runtime/);
    assert.match(dockerfile, /COPY --chown=cao:cao server\/azure\/empty-data\/ \/data\//);
  });
test('Azure shares PostgreSQL and operational budgets across dashboard and collection roles', () => {
  for (const source of [bicep, collector]) {
    assert.match(source, /name:\s*'CAO_POSTGRES_URL'\s+secretRef:|'CAO_POSTGRES_URL'[\s\S]*?secrets\/\$\{postgresUrlSecretName\}/);
    assert.match(source, /CAO_OPERATIONAL_CACHE_MAX_BYTES/);
    assert.match(source, /CAO_OPERATIONAL_CACHE_MAX_VALUE_BYTES/);
    assert.match(source, /CAO_OPERATIONAL_CACHE_MAX_ENTRIES/);
    assert.match(source, /CAO_OPERATIONAL_PROTECTED_MAX_BYTES/);
    assert.match(source, /CAO_OPERATIONAL_PROTECTED_MAX_ENTRIES/);
  }
  assert.equal(generated.parameters.operationalCacheMaxBytes.defaultValue, 33554432);
  assert.equal(generated.parameters.operationalCacheMaxValueBytes.defaultValue, 4194304);
  assert.equal(generated.parameters.operationalCacheMaxEntries.defaultValue, 1024);
  assert.equal(generated.parameters.operationalProtectedMaxBytes.defaultValue, 134217728);
  assert.equal(generated.parameters.operationalProtectedMaxEntries.defaultValue, 200000);
  assert.ok(!('redisMaxBytes' in generated.parameters));
});

test('Azure collection uses fixed PostgreSQL-backed workers', () => {
  assert.match(collector, /param workerReplicas int = 1/);
  assert.match(collector, /minReplicas:\s*workerReplicas/);
  assert.match(collector, /maxReplicas:\s*workerReplicas/);
  assert.doesNotMatch(collector, /rules:\s*\[/);
  assert.doesNotMatch(collector, /taskStreamKey|streamKey|consumerGroup/);
});

test('Azure dashboard Bicep does not output secrets or PAT-based configuration', () => {
  const outputLines = bicep.split('\n').filter((line) => line.trim().startsWith('output '));
  assert.ok(outputLines.length > 0, 'expected deterministic non-secret outputs');
  for (const line of outputLines) {
    const [, name] = line.trim().split(/\s+/);
    assert.doesNotMatch(name, /secret|token|connection|accessKey|primaryKey/i);
  }
  assert.doesNotMatch(bicep, /CAO_GITHUB_PAT|GITHUB_PAT|PERSONAL_ACCESS_TOKEN/i);
  assert.match(bicep, /OAuth authorization-code flow/);
});

test('Azure dashboard Bicep keeps secret-bearing settings in Key Vault', () => {
  assert.match(bicep, /resource keyVault 'Microsoft\.KeyVault\/vaults@/);
  assert.match(bicep, /enableRbacAuthorization:\s*true/);
  assert.match(bicep, /enableSoftDelete:\s*true/);
  assert.match(bicep, /enablePurgeProtection:\s*keyVaultPurgeProtection \? true : null/);
  assert.match(bicep, /enabledForTemplateDeployment:\s*false/);
  assert.match(bicep, /param previousSessionSecret string = ''/);
  assert.match(bicep, /name:\s*'cao-session-secret-previous'/);
  assert.match(bicep, /identity:\s*{\s*type:\s*'SystemAssigned'/);
  assert.match(functionRoles, /Key Vault Secrets User/);

  for (const setting of ['CAO_POSTGRES_URL', 'CAO_GITHUB_CLIENT_SECRET', 'CAO_SESSION_SECRET']) {
    const pattern = new RegExp(`name:\\s*'${setting}'[\\s\\S]*?value:\\s*'@Microsoft\\.KeyVault\\(SecretUri=`);
    assert.match(bicep, pattern, `${setting} must use a Key Vault reference`);
  }
  assert.match(bicep, /name:\s*'AzureWebJobsStorage__accountName'/);
  assert.match(bicep, /name:\s*'AzureWebJobsStorage__credential'\s+value:\s*'managedidentity'/);
  assert.match(bicep, /allowSharedKeyAccess:\s*false/);
  assert.match(functionRoles, /Storage Blob Data Owner/);
  assert.match(functionRoles, /Storage Queue Data Contributor/);
  assert.match(functionRoles, /Storage Table Data Contributor/);
  assert.match(functionRoles, /AcrPull/);
  assert.match(
    functionRoles,
    /guid\(storage\.id,\s*functionPrincipalId,\s*'Storage Blob Data Owner'\)/,
    'storage role assignment IDs must change when the Function App managed identity is replaced',
  );
  assert.match(
    functionRoles,
    /guid\(secrets\[i\]\.id,\s*functionPrincipalId,\s*'Key Vault Secrets User'\)/,
    'Key Vault role assignment IDs must change when the Function App managed identity is replaced',
  );
  assert.match(
    functionRoles,
    /guid\(registry\.id,\s*functionPrincipalId,\s*'AcrPull'\)/,
    'registry role assignment IDs must change when the Function App managed identity is replaced',
  );
  assert.doesNotMatch(bicep, /secretUriWithVersion/, 'Key Vault references must follow the current secret version for rotation');

  assert.match(bicep, /httpsOnly:\s*true/);
  assert.match(bicep, /kind:\s*'functionapp,linux,container'/);
  assert.match(bicep, /linuxFxVersion:\s*'DOCKER\|\$\{registry\.properties\.loginServer\}/);
  assert.match(bicep, /acrUseManagedIdentityCreds:\s*true/);
  assert.match(bicep, /name:\s*'FUNCTIONS_WORKER_RUNTIME'\s+value:\s*'custom'/);
  assert.match(bicep, /name:\s*'WEBSITES_PORT'\s+value:\s*'8080'/);
  assert.match(bicep, /name:\s*'WEBSITES_ENABLE_APP_SERVICE_STORAGE'\s+value:\s*'false'/);
  assert.match(bicep, /name:\s*'CAO_DATABASE_QUERIES'\s+value:\s*'\/app\/queries\/database\.json'/);
  assert.match(bicep, /name:\s*'CAO_GITHUB_ALLOWED_USERS'\s+value:\s*join\(githubAllowedUsers,\s*','\)/);
  assert.match(bicep, /alwaysOn:\s*true/);
  assert.match(bicep, /minimumElasticInstanceCount:\s*1/);
  assert.match(bicep, /ftpsState:\s*'Disabled'/);
  assert.match(bicep, /allowBlobPublicAccess:\s*false/);
  assert.match(bicep, /supportsHttpsTrafficOnly:\s*true/);
});

test('Azure redeployment can keep an existing subnet NSG without forcing one on other deployments', () => {
  assert.equal(generated.parameters.subnetNetworkSecurityGroupId.type, 'string');
  assert.equal(generated.parameters.subnetNetworkSecurityGroupId.defaultValue, '');
  assert.match(bicep, /subnetNetworkSecurityGroupId: subnetNetworkSecurityGroupId/);
  assert.match(network, /param subnetNetworkSecurityGroupId string = ''/);
  assert.equal(network.match(/properties: union\(subnetNetworkSecurityGroup, \{/g)?.length, 4,
    'every subnet must carry the optional NSG');
});

test('Azure database and runtime URL secret names remain configurable without reusing the admin URL', () => {
  assert.equal(generated.parameters.postgresDatabaseName.defaultValue, 'cao');
  assert.equal(generated.parameters.postgresUrlSecretName.defaultValue, 'cao-postgres-runtime-url');
  assert.equal(generated.parameters.postgresUrlSecretName.maxLength, 110);
  assert.match(network, /param postgresDatabaseName string = 'cao'/);
  assert.match(network, /parent: postgres\s+name: postgresDatabaseName/);
  assert.match(bicep, /postgresDatabaseName: postgresDatabaseName/);
  assert.match(bicep, /parent: keyVault\s+name: postgresUrlSecretName/);
  assert.match(bicep, /name:\s*'CAO_POSTGRES_URL'\s+value:\s*'@Microsoft\.KeyVault\(SecretUri=\$\{keyVault\.properties\.vaultUri\}secrets\/\$\{postgresUrlSecretName\}\)'/);
  for (const source of [ingestion, collector]) {
    assert.match(source, /param postgresUrlSecretName string = 'cao-postgres-runtime-url'/);
    assert.match(source, /keyVaultUrl: '\$\{[^}]+\}secrets\/\$\{postgresUrlSecretName\}'/);
  }
  for (const source of [bicep, network, ingestion, collector]) {
    assert.doesNotMatch(source, /secrets\/cao-postgres-url|name: 'cao'\s*$/m, 'Key Vault and database names must come from parameters');
  }
  for (const name of ['snapshot-ingestion', 'collection-workers']) {
    const module = resources.find((resource) => resource.name === name);
    assert.equal(module.properties.parameters.postgresUrlSecretName.value, "[parameters('postgresUrlSecretName')]");
  }
  assert.match(deploymentDocs, /### Moving to a new database/);
});

test('Azure ingestion has no privileged initialization or secret access', () => {
  const module = resources.find((resource) => resource.name === 'snapshot-ingestion');
  const job = Object.values(module.properties.template.resources).find((resource) => resource.type === 'Microsoft.App/jobs');
  assert.equal(job.properties.template.initContainers, undefined);
  assert.equal(module.properties.parameters.canonicalNamespace.value, 'azure-dashboard');
  assert.doesNotMatch(JSON.stringify(module), /CAO_DATABASE_ADMIN_URL|bootstrap|owner-password|runtime-password/);
  assert.deepEqual(job.properties.configuration.secrets, [{
    name: 'cao-postgres-url',
    keyVaultUrl: "[format('{0}secrets/{1}', reference(resourceId('Microsoft.KeyVault/vaults', parameters('keyVaultName')), '2023-07-01').vaultUri, parameters('postgresUrlSecretName'))]",
    identity: "[resourceId('Microsoft.ManagedIdentity/userAssignedIdentities', format('{0}-ingest-identity', parameters('namePrefix')))]",
  }]);
  assert.deepEqual(job.properties.template.containers[0].env.find((entry) => entry.name === 'CAO_POSTGRES_URL'),
    { name: 'CAO_POSTGRES_URL', secretRef: 'cao-postgres-url' });
  assert.match(deploymentDocs, /database bootstrap --store canonical/);
  assert.match(deploymentDocs, /database bootstrap --store operational/);
});

test('Runtime URL uses only the restricted login and secure runtime password', () => {
  const runtimeSecret = resources.find((resource) => resource.type === 'Microsoft.KeyVault/vaults/secrets'
    && resource.name === "[format('{0}/{1}', parameters('keyVaultName'), parameters('postgresUrlSecretName'))]");
  assert.ok(runtimeSecret);
  assert.match(runtimeSecret.properties.value, /postgresql:\/\/cao_runtime_login:/);
  assert.match(runtimeSecret.properties.value, /uriComponent\(parameters\('postgresRuntimePassword'\)\)/);
  assert.match(runtimeSecret.properties.value, /options=-csearch_path%3Dcao/);
  assert.doesNotMatch(runtimeSecret.properties.value, /Administrator|OwnerPassword/);
  const app = resources.find((resource) => resource.type === 'Microsoft.Web/sites');
  assert.doesNotMatch(app.properties.siteConfig.appSettings, /CAO_DATABASE_ADMIN_URL|owner-password|runtime-password/);
  assert.match(app.properties.siteConfig.appSettings,
    /CAO_POSTGRES_URL.*?secrets\/\{1\}.*?parameters\('postgresUrlSecretName'\)/);
  for (const name of ['snapshot-ingestion', 'collection-workers']) {
    const module = resources.find((resource) => resource.name === name);
    assert.equal(module.properties.parameters.postgresUrlSecretName.value, "[parameters('postgresUrlSecretName')]");
    assert.doesNotMatch(JSON.stringify(module), /CAO_DATABASE_ADMIN_URL|owner-password|runtime-password/);
  }
  for (const name of ['postgresAdministratorPassword', 'postgresOwnerPassword', 'postgresRuntimePassword']) {
    assert.equal(generated.parameters[name].type.toLowerCase(), 'securestring');
    assert.equal(generated.parameters[name].defaultValue, undefined);
  }
});

test('Only the separate provisioning identity can resolve the administrator secret', () => {
  const module = resources.find((resource) => resource.name === 'database-provisioning');
  const template = module.properties.template;
  const job = template.resources.find((resource) => resource.type === 'Microsoft.App/jobs');
  const identity = template.resources.find((resource) => resource.type === 'Microsoft.ManagedIdentity/userAssignedIdentities');
  assert.equal(identity.name, "[format('{0}-db-provision-identity', parameters('namePrefix'))]");
  assert.equal(job.properties.configuration.triggerType, 'Manual');
  assert.equal(job.properties.configuration.replicaRetryLimit, 0);
  assert.equal(job.properties.template.containers[0].image, "[parameters('provisioningImage')]");
  assert.deepEqual(job.properties.template.containers[0].env.find((entry) => entry.name === 'CAO_DATABASE_ADMIN_URL'),
    { name: 'CAO_DATABASE_ADMIN_URL', secretRef: "[variables('secretNames')[0]]" });
  assert.equal(template.variables.secretNames[0], "[format('{0}-admin', parameters('postgresUrlSecretName'))]");
  const grants = template.resources.find((resource) => resource.type === 'Microsoft.Resources/deployments');
  assert.equal(grants.properties.parameters.secretNames.value, "[variables('secretNames')]");
  assert.match(grants.properties.parameters.principalId.value, /db-provision-identity/);

  // Inspect every nested RBAC template, not just the provisioning module.
  function checkRoles(current) {
    for (const resource of Object.values(current.resources ?? {})) {
      if (resource.type === 'Microsoft.Authorization/roleAssignments'
        && resource.properties.roleDefinitionId.includes('4633458b-17de-408a-b874-0445c86b69e6')) {
        assert.equal(resource.scope,
          "[resourceId('Microsoft.KeyVault/vaults/secrets', parameters('keyVaultName'), parameters('secretNames')[copyIndex()])]");
        assert.equal(resource.copy.count, "[length(parameters('secretNames'))]");
      }
      if (resource.properties?.template) checkRoles(resource.properties.template);
    }
  }
  checkRoles(generated);
  const functionGrants = resources.find((resource) => resource.name === 'function-managed-identity-roles');
  assert.match(functionGrants.properties.parameters.secretNames.value, /parameters\('postgresUrlSecretName'\)/);
  assert.doesNotMatch(functionGrants.properties.parameters.secretNames.value, /-admin|owner-password|runtime-password/);
  const ingestGrants = resources.find((resource) => resource.name === 'snapshot-ingestion')
    .properties.template.resources.find((resource) => resource.type === 'Microsoft.Resources/deployments');
  assert.deepEqual(ingestGrants.properties.parameters.secretNames.value, ["[parameters('postgresUrlSecretName')]"]);
  const collectorGrants = resources.find((resource) => resource.name === 'collection-workers')
    .properties.template.resources.find((resource) => resource.name === 'collector-roles');
  assert.equal(collectorGrants.properties.parameters.secretNames.value,
    "[concat(createArray(parameters('postgresUrlSecretName'), 'cao-collect-private-key'), if(not(parameters('otlpHeadersEnabled')), createArray(), createArray('cao-otel-headers')))]");
});

test('Provisioning verifies PostgreSQL TLS against the system CA bundle', () => {
  const job = provisioning.slice(provisioning.indexOf("name: 'PGSSLMODE'"));
  assert.match(job, /name: 'PGSSLMODE'\s+value: 'verify-full'/);
  assert.match(job, /name: 'PGSSLROOTCERT'\s+value: 'system'/);
  assert.match(provisionScript, /export PGSSLMODE="\$\{PGSSLMODE:-verify-full\}"/);
  assert.match(provisionScript, /export PGSSLROOTCERT="\$\{PGSSLROOTCERT:-system\}"/);
  assert.ok(provisionScript.indexOf('export PGSSLROOTCERT') < provisionScript.indexOf('psql -X'));
  assert.doesNotMatch(provisionScript + provisioning + bicep, /sslmode=(require|verify-ca|prefer|disable)/);
});

test('Provisioning uses the deployment ACR and checked-in grants under the non-login owner', () => {
  const module = resources.find((resource) => resource.name === 'database-provisioning');
  assert.match(module.properties.parameters.provisioningImage.value, /Microsoft\.ContainerRegistry\/registries/);
  assert.match(module.properties.parameters.provisioningImage.value, /provisioningImageRepository/);
  assert.doesNotMatch(module.properties.parameters.provisioningImage.value, /docker\.io|postgres:16/);
  assert.match(dockerfile, /FROM postgres:16-alpine@sha256:[a-f0-9]{64} AS azure-db-provision/);
  assert.match(dockerfile, /COPY server\/postgres\/\*\.sql \/app\/postgres\//);
  assert.match(provisionScript, /PGOPTIONS='-c role=cao_owner -c search_path=cao'/);
  for (const store of ['canonical', 'operational']) {
    assert.match(provisionScript, new RegExp(`database bootstrap --store ${store}`));
    assert.match(provisionScript, new RegExp(`-f /app/postgres/${store}-grants.sql`));
  }
  for (const source of [bicep, ingestion, provisioning]) {
    assert.doesNotMatch(source, /GRANT |REVOKE |CREATE ROLE|cao_operational_audit_events/);
  }
  for (const role of ['cao_owner', 'cao_runtime', 'cao_retention', 'cao_evidence']) {
    assert.ok(provisionSql.includes(`'${role}'`));
  }
  assert.match(provisionSql, /NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS/);
  assert.match(provisionSql, /GRANT TEMPORARY ON DATABASE %I TO cao_runtime/);
  assert.match(provisionSql, /CREATE SCHEMA IF NOT EXISTS cao AUTHORIZATION cao_owner/);
  assert.doesNotMatch(provisionSql, /cao_retention_login|cao_evidence_login|GRANT ALL|ALL TABLES/);
  for (const name of ['CacheMaxBytes', 'CacheMaxValueBytes', 'CacheMaxEntries', 'ProtectedMaxBytes', 'ProtectedMaxEntries']) {
    assert.equal(module.properties.parameters[`operational${name}`].value, `[parameters('operational${name}')]`);
  }
  for (const setting of ['CAO_POSTGRES_RUN_RETENTION_DAYS', 'CAO_POSTGRES_LINKED_RETENTION_DAYS']) {
    assert.deepEqual(module.properties.template.resources.find((resource) => resource.type === 'Microsoft.App/jobs')
      .properties.template.containers[0].env.find((entry) => entry.name === setting),
    { name: setting, value: "[string(parameters('postgresRunRetentionDays'))]" });
  }
});

test('No nested deployment outputs a password or credential-bearing URL', () => {
  function checkOutputs(template) {
    for (const [name, output] of Object.entries(template.outputs ?? {})) {
      assert.doesNotMatch(name, /password|secret|token|connection/i);
      assert.doesNotMatch(JSON.stringify(output), /Password|postgresql:\/\/|listSecrets|listKeys|ADMIN_URL/);
    }
    for (const resource of Object.values(template.resources ?? {})) {
      if (resource.properties?.template) checkOutputs(resource.properties.template);
    }
  }
  checkOutputs(generated);
});

test('Azure Go exporters have an explicit shared OTLP route and secret-scoped authentication', () => {
  assert.equal(generated.parameters.otlpEndpoint.defaultValue, '');
  assert.equal(generated.parameters.otlpLogsEnabled.defaultValue, false);
  assert.equal(generated.parameters.otlpHeaders.type, 'securestring');
  assert.equal(generated.parameters.otlpHeaders.defaultValue, '');
  assert.deepEqual(generated.variables.otlpEnvironment, [
    { name: 'OTEL_EXPORTER_OTLP_ENDPOINT', value: "[parameters('otlpEndpoint')]" },
    { name: 'OTEL_EXPORTER_OTLP_PROTOCOL', value: 'http/protobuf' },
    { name: 'CAO_OTEL_LOGS_ENABLED', value: "[string(parameters('otlpLogsEnabled'))]" },
  ]);
  assert.equal(generated.variables.otlpHeadersEnabled,
    "[and(not(empty(parameters('otlpEndpoint'))), not(empty(parameters('otlpHeaders'))))]");
  const headers = resources.find((resource) => resource.type === 'Microsoft.KeyVault/vaults/secrets'
    && resource.name.includes('cao-otel-headers'));
  assert.equal(headers.condition, "[variables('otlpHeadersEnabled')]");
  assert.equal(headers.properties.value, "[parameters('otlpHeaders')]");

  const app = resources.find((resource) => resource.type === 'Microsoft.Web/sites');
  assert.match(app.properties.siteConfig.appSettings, /variables\('otlpEnvironment'\)/);
  assert.match(app.properties.siteConfig.appSettings, /if\(not\(variables\('otlpHeadersEnabled'\)\)/);
  assert.match(app.properties.siteConfig.appSettings,
    /'OTEL_EXPORTER_OTLP_HEADERS', 'value', format\('@Microsoft.KeyVault\(SecretUri=\{0\}secrets\/cao-otel-headers\)'/);
  assert.match(app.properties.siteConfig.appSettings, /if\(not\(parameters\('otlpLogsEnabled'\)\).*?'DEBUG', 'value', 'cao:server,cao:telemetry'/);
  const grants = resources.find((resource) => resource.name === 'function-managed-identity-roles');
  assert.match(grants.properties.parameters.secretNames.value,
    /if\(not\(variables\('otlpHeadersEnabled'\)\), createArray\(\), createArray\('cao-otel-headers'\)\)/);

  const workers = resources.find((resource) => resource.name === 'collection-workers');
  assert.equal(workers.properties.parameters.otlpEnvironment.value, "[variables('otlpEnvironment')]");
  assert.equal(workers.properties.parameters.otlpHeadersEnabled.value, "[variables('otlpHeadersEnabled')]");
  assert.equal(workers.properties.parameters.otlpLogsEnabled.value, "[parameters('otlpLogsEnabled')]");
  const template = workers.properties.template;
  assert.match(template.variables.collectionEnvironment,
    /concat\(variables\('collectionBaseEnvironment'\), parameters\('otlpEnvironment'\)/);
  assert.match(template.variables.collectionEnvironment, /'OTEL_EXPORTER_OTLP_HEADERS', 'secretRef', 'cao-otel-headers'/);
  assert.match(template.variables.collectionSecrets,
    /'cao-otel-headers', 'keyVaultUrl', format\('\{0\}secrets\/cao-otel-headers', parameters\('keyVaultUri'\)\), 'identity', resourceId/);
  const debug = template.variables.collectionBaseEnvironment.find((entry) => entry.name === 'DEBUG');
  assert.equal(debug.value,
    "[if(parameters('otlpLogsEnabled'), 'cao:telemetry,cao:collect:recovery', 'cao:collect:*')]");
  const consumers = template.resources.filter((resource) =>
    ['Microsoft.App/jobs', 'Microsoft.App/containerApps'].includes(resource.type));
  assert.equal(consumers.length, 2);
  for (const consumer of consumers) {
    assert.equal(consumer.properties.configuration.secrets, "[variables('collectionSecrets')]");
    assert.equal(consumer.properties.template.containers[0].env, "[variables('collectionEnvironment')]");
    assert.doesNotMatch(JSON.stringify(consumer.properties.template), /livenessProbe|readinessProbe|4318|otlpHeaders/);
  }
  assert.doesNotMatch(app.properties.siteConfig.appSettings, /parameters\('otlpHeaders'\)/);
  assert.doesNotMatch(JSON.stringify(generated.outputs), /otlpHeaders|OTEL_EXPORTER_OTLP_HEADERS/);
});
