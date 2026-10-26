targetScope = 'resourceGroup'

// Experimental Azure Functions deployment baseline for the Go dashboard server.
// Review and validate tenant-specific security, compliance, networking,
// monitoring, cost, rotation, and rollback requirements before live use.

@description('Azure region for all dashboard resources.')
param location string = resourceGroup().location

@minLength(2)
@maxLength(19)
@description('Globally unique lowercase Function App name and job prefix, starting with a letter and without consecutive hyphens. At most 19 characters keeps the derived -db-provision job within the Container Apps 32-character limit.')
param functionAppName string

@description('Default endpoint access. Enabled (default) serves the dashboard over public HTTPS behind GitHub OAuth sign-in; no VPN, proxy, or corporate-network rule is required. Set Disabled only for a deliberately private deployment.')
@allowed([
  'Disabled'
  'Enabled'
])
param functionPublicNetworkAccess string = 'Enabled'

@description('Default action for unmatched main-site requests. Allow (default) is the public-main model: GitHub OAuth remains the access control. Use Deny with explicit rules only for a deliberately restricted deployment. Never changes SCM restrictions.')
@allowed([
  'Allow'
  'Deny'
])
param functionAccessRestrictionsDefaultAction string = 'Allow'

@description('Reviewed main-site access restriction rules. Explicitly carry forward existing approved rules during redeployment; omission uses an empty list. Unmatched requests use functionAccessRestrictionsDefaultAction.')
param functionAccessRestrictions array = []

@description('Reviewed SCM/Kudu access restriction rules, separate from main-site rules. Explicitly carry forward existing approved SCM rules; omission uses an empty list.')
param functionScmAccessRestrictions array = []

@description('Whether SCM reuses main-site rules. Carry forward the existing approved setting; false keeps SCM independently restricted by default.')
param functionScmAccessRestrictionsUseMain bool = false

@description('Azure App Service plan name for the Function App.')
param hostingPlanName string = '${functionAppName}-plan'

@description('Application Insights component name.')
param applicationInsightsName string = '${functionAppName}-ai'

@description('Optional reviewed HTTPS OTLP/HTTP collector base URL, without credentials, query or fragment. Empty disables Go export; Application Insights settings alone only configure the Functions host.')
param otlpEndpoint string = ''

@description('Explicit opt-in to export bounded operational Go logs to the OTLP collector. Independent audit delivery is unchanged.')
param otlpLogsEnabled bool = false

@secure()
@description('Optional OTLP authentication headers from a secret manager. Stored only in Key Vault; never supply literal values in command-line arguments or checked-in parameters.')
param otlpHeaders string = ''

var otlpEnvironment = [
  {
    name: 'OTEL_EXPORTER_OTLP_ENDPOINT'
    value: otlpEndpoint
  }
  {
    name: 'OTEL_EXPORTER_OTLP_PROTOCOL'
    value: 'http/protobuf'
  }
  {
    name: 'CAO_OTEL_LOGS_ENABLED'
    value: string(otlpLogsEnabled)
  }
]

var otlpHeadersEnabled = !empty(otlpEndpoint) && !empty(otlpHeaders)

@description('Globally unique storage account name for Azure Functions runtime state.')
param storageAccountName string

@description('Globally unique Azure Container Registry name containing the Function App image.')
param containerRegistryName string

@description('Container repository containing cao-dashboard azure-functions. The existing cao-functions image repository remains supported.')
param functionImageRepository string = 'cao-functions'

@description('Immutable or reviewed tag of the CAO Azure Functions runtime image.')
param functionImageTag string

@description('Exact Functions image digest, including sha256:. When supplied, takes precedence over the tag. Does not establish scan or production assurance.')
param functionImageDigest string = ''

@description('Key Vault name used for GitHub OAuth, session, PostgreSQL, and collection secrets.')
param keyVaultName string

@description('Enable purge protection for production vaults. Disable only for explicitly disposable environments that must be purged after testing. Existing protected vaults cannot be downgraded.')
param keyVaultPurgeProtection bool = true

@description('Globally unique name for a new private PostgreSQL Flexible Server.')
param postgresServerName string = '${functionAppName}-pg'

param postgresAdministratorLogin string = 'caoadmin'

@secure()
@minLength(16)
@description('PostgreSQL administrator password. Use secret-manager input; never commit it.')
param postgresAdministratorPassword string

param postgresSkuName string = 'Standard_B2s'
param postgresSkuTier string = 'Burstable'

@description('Optional existing network security group kept attached to every private subnet. Supply it when an approved NSG is already attached so redeployment does not detach it.')
param subnetNetworkSecurityGroupId string = ''

@minLength(1)
@maxLength(63)
@description('PostgreSQL database that holds the canonical and operational stores. The default matches existing deployments. A new name creates a new, empty database; the previous database is retained, not deleted.')
param postgresDatabaseName string = 'cao'

@minLength(1)
@maxLength(110)
@description('Runtime-only PostgreSQL URL secret. Use a new name and database when moving from the administrator-backed topology.')
param postgresUrlSecretName string = 'cao-postgres-runtime-url'

@secure()
@minLength(16)
@description('Separately generated bootstrap-login password from the operator secret manager. Never reuse the administrator or runtime password.')
param postgresOwnerPassword string

@secure()
@minLength(16)
@description('Separately generated runtime-login password from the operator secret manager.')
param postgresRuntimePassword string

@description('ACR repository containing the azure-db-provision Dockerfile target.')
param provisioningImageRepository string = 'cao-db-provision'

param provisioningImageTag string = functionImageTag

@description('Exact database-provisioning image digest, including sha256:. When supplied, takes precedence over the tag.')
param provisioningImageDigest string = ''

@description('Repository of an explicitly built snapshot or empty ingestion image in this deployment registry.')
param ingestionImageRepository string = 'cao-ingest'

param ingestionImageTag string = functionImageTag

@description('Exact ingestion image digest, including sha256:. When supplied, takes precedence over the tag.')
param ingestionImageDigest string = ''

var functionImageSelector = empty(functionImageDigest) ? ':${functionImageTag}' : '@${functionImageDigest}'
var ingestionImageSelector = empty(ingestionImageDigest) ? ':${ingestionImageTag}' : '@${ingestionImageDigest}'
var provisioningImageSelector = empty(provisioningImageDigest) ? ':${provisioningImageTag}' : '@${provisioningImageDigest}'

@minValue(1)
@description('Maximum bytes retained in the disposable operational cache.')
param operationalCacheMaxBytes int = 33554432

@minValue(1)
@description('Maximum size of one disposable operational cache value.')
param operationalCacheMaxValueBytes int = 4194304

@minValue(1)
@description('Maximum number of disposable operational cache entries.')
param operationalCacheMaxEntries int = 1024

@minValue(1)
@description('Maximum bytes retained in protected operational records.')
param operationalProtectedMaxBytes int = 134217728

@minValue(1)
@description('Maximum number of protected operational records.')
param operationalProtectedMaxEntries int = 200000

@minValue(7)
@maxValue(3650)
@description('Run and linked history retention in days. Before starting any role, explicitly preserve at least the greatest existing effective retention across all roles and the snapshot window. The 45-day default is not an upgrade recommendation.')
param postgresRunRetentionDays int = 45

@description('Public host names that Azure Front Door/App Service is allowed to forward to the Go dashboard handler.')
param allowedHosts array

@description('GitHub organizations whose active members are authorized.')
param githubAllowedOrganizations array

@description('GitHub teams whose active members are authorized, expressed as org/team-slug.')
param githubAllowedTeams array = []

@description('GitHub user logins authorized directly, without organization membership lookup.')
param githubAllowedUsers array = []

@description('GitHub OAuth App client ID. Do not use a PAT; the server only supports the OAuth authorization-code flow.')
param githubClientId string

@secure()
@description('GitHub OAuth App client secret. It is written only to Key Vault and consumed by the Function App through a Key Vault reference.')
param githubClientSecret string

@secure()
@minLength(32)
@description('At least 32 characters of random session secret material used to encrypt server-side OAuth sessions.')
param sessionSecret string

@secure()
@description('Optional previous session secret retained during controlled key rotation until active sessions and pending revocations are drained.')
param previousSessionSecret string = ''

@description('Single Log Analytics workspace shared by every role in this deployment.')
param logAnalyticsWorkspaceName string = '${functionAppName}-logs'

// Optional server collection profile.
//
// Leaving collectorImage empty deploys the default profile unchanged: the
// dashboard serves snapshots published by the Activity workflow and the server
// collects nothing. Supplying an image selects the alternative profile, in
// which this deployment collects evidence itself. The two profiles are
// alternatives, never layers.

@description('Optional container image running the collection role. Empty deploys no collection.')
param collectorImage string = ''

@description('GitHub App identifier whose installations define ingestion scope. Required with collectorImage.')
param collectorGithubAppId string = ''

@secure()
@description('GitHub App private key in PEM form. Required with collectorImage.')
param collectorPrivateKey string = ''

@secure()
@description('Shared secret verifying GitHub webhook deliveries. Required with collectorImage.')
param githubWebhookSecret string = ''

@description('GitHub logins permitted to run administrative operations.')
param githubAdminUsers array = []

@description('Control repository used for logical source discovery, in OWNER/REPOSITORY form.')
param collectorControlRepository string = ''

@minValue(1)
@description('Fixed number of PostgreSQL-backed collection workers.')
param collectorWorkerReplicas int = 1

@description('Evidence lake file share tier. Premium is provisioned SSD; Standard is IOPS-throttled by share size.')
@allowed([
  'Premium_LRS'
  'Premium_ZRS'
  'Standard_LRS'
  'Standard_ZRS'
])
param collectorLakeStorageSku string = 'Premium_LRS'

@description('Provision an optional paid ADX destination only. Does not enable export, traces, or mining; Azure Functions is not a supported exporter host.')
param adxEnabled bool = false

@minLength(4)
@maxLength(22)
@description('Globally unique ADX cluster name: lowercase letters and digits only.')
param adxClusterName string = 'adx${uniqueString(resourceGroup().id, functionAppName)}'

@minLength(1)
@maxLength(100)
@description('ADX database identifier: ASCII letter followed by letters, digits, or underscores.')
param adxDatabaseName string = 'CAO'

@description('Reviewed paid ADX SKU. The default is development-only without an SLA, not the free offering.')
param adxSkuName string = 'Dev(No SLA)_Standard_E2a_v4'

@allowed([
  'Basic'
  'Standard'
])
param adxSkuTier string = 'Basic'

@minValue(1)
@description('Reviewed ADX instance count compatible with the selected SKU and tier.')
param adxCapacity int = 1

var collectionEnabled = !empty(collectorImage)

var tags = {
  workload: 'gh-cao-dashboard'
  hostingMode: 'azure-functions'
}
var githubRedirectUri = 'https://${functionAppName}.azurewebsites.net/auth/callback'

module network 'network.bicep' = {
  name: 'private-network-postgres'
  params: {
    location: location
    namePrefix: functionAppName
    postgresServerName: postgresServerName
    postgresAdministratorLogin: postgresAdministratorLogin
    postgresAdministratorPassword: postgresAdministratorPassword
    postgresSkuName: postgresSkuName
    postgresSkuTier: postgresSkuTier
    subnetNetworkSecurityGroupId: subnetNetworkSecurityGroupId
    postgresDatabaseName: postgresDatabaseName
    adxEnabled: adxEnabled
  }
}

module adx 'adx.bicep' = if (adxEnabled) {
  name: 'adx-destination'
  params: {
    location: location
    clusterName: adxClusterName
    databaseName: adxDatabaseName
    skuName: adxSkuName
    skuTier: adxSkuTier
    capacity: adxCapacity
    virtualNetworkId: network.outputs.virtualNetworkId
    privateEndpointSubnetId: network.outputs.adxSubnetId
    tags: tags
  }
}

resource workspace 'Microsoft.OperationalInsights/workspaces@2023-09-01' = {
  name: logAnalyticsWorkspaceName
  location: location
  tags: tags
  properties: {
    sku: {
      name: 'PerGB2018'
    }
    retentionInDays: 30
  }
}

resource containerEnvironment 'Microsoft.App/managedEnvironments@2024-03-01' = {
  name: '${functionAppName}-jobs'
  location: location
  tags: tags
  properties: {
    appLogsConfiguration: {
      destination: 'azure-monitor'
    }
    vnetConfiguration: {
      infrastructureSubnetId: network.outputs.containerSubnetId
      internal: true
    }
    zoneRedundant: false
  }
}

resource containerDiagnostics 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = {
  name: 'container-logs'
  scope: containerEnvironment
  properties: {
    workspaceId: workspace.id
    logs: [
      {
        categoryGroup: 'allLogs'
        enabled: true
      }
    ]
  }
}

resource storage 'Microsoft.Storage/storageAccounts@2023-05-01' = {
  name: storageAccountName
  location: location
  tags: tags
  sku: {
    name: 'Standard_LRS'
  }
  kind: 'StorageV2'
  properties: {
    allowSharedKeyAccess: false
    allowBlobPublicAccess: false
    minimumTlsVersion: 'TLS1_2'
    supportsHttpsTrafficOnly: true
  }
}

resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' = {
  name: containerRegistryName
  location: location
  tags: tags
  sku: {
    name: 'Basic'
  }
  properties: {
    adminUserEnabled: false
    publicNetworkAccess: 'Enabled'
  }
}

resource keyVault 'Microsoft.KeyVault/vaults@2023-07-01' = {
  name: keyVaultName
  location: location
  tags: tags
  properties: {
    tenantId: subscription().tenantId
    sku: {
      family: 'A'
      name: 'standard'
    }
    enableRbacAuthorization: true
    enabledForTemplateDeployment: false
    enableSoftDelete: true
    enablePurgeProtection: keyVaultPurgeProtection ? true : null
    softDeleteRetentionInDays: 90
    publicNetworkAccess: 'Enabled'
  }
}

resource githubClientSecretValue 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: keyVault
  name: 'github-oauth-client-secret'
  properties: {
    value: githubClientSecret
    contentType: 'GitHub OAuth client secret for CAO dashboard Azure Functions mode'
  }
}

resource sessionSecretValue 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: keyVault
  name: 'cao-session-secret'
  properties: {
    value: sessionSecret
    contentType: 'CAO dashboard session encryption secret'
  }
}

resource previousSessionSecretValue 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = if (!empty(previousSessionSecret)) {
  parent: keyVault
  name: 'cao-session-secret-previous'
  properties: {
    value: previousSessionSecret
    contentType: 'Previous CAO dashboard session encryption secret retained during rotation'
  }
}

resource postgresConnectionStringValue 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: keyVault
  name: postgresUrlSecretName
  properties: {
    value: 'postgresql://cao_runtime_login:${uriComponent(postgresRuntimePassword)}@${network.outputs.postgresHostName}:5432/${uriComponent(network.outputs.databaseName)}?sslmode=verify-full&options=-csearch_path%3Dcao'
    contentType: 'CAO runtime-only PostgreSQL connection URL'
  }
}

resource postgresAdminUrl 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: keyVault
  name: '${postgresUrlSecretName}-admin'
  properties: {
    value: 'postgresql://${uriComponent(postgresAdministratorLogin)}:${uriComponent(postgresAdministratorPassword)}@${network.outputs.postgresHostName}:5432/${uriComponent(network.outputs.databaseName)}?sslmode=verify-full'
    contentType: 'Short-lived CAO database provisioning only'
  }
}

resource postgresOwnerPasswordValue 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: keyVault
  name: '${postgresUrlSecretName}-owner-password'
  properties: {
    value: postgresOwnerPassword
  }
}

resource postgresRuntimePasswordValue 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: keyVault
  name: '${postgresUrlSecretName}-runtime-password'
  properties: {
    value: postgresRuntimePassword
  }
}

resource collectorPrivateKeyValue 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = if (collectionEnabled) {
  parent: keyVault
  name: 'cao-collect-private-key'
  properties: {
    value: collectorPrivateKey
    contentType: 'GitHub App private key for the collection profile'
  }
}

resource githubWebhookSecretValue 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = if (collectionEnabled) {
  parent: keyVault
  name: 'cao-github-webhook-secret'
  properties: {
    value: githubWebhookSecret
    contentType: 'GitHub webhook shared secret'
  }
}

resource plan 'Microsoft.Web/serverfarms@2023-12-01' = {
  name: hostingPlanName
  location: location
  tags: tags
  kind: 'elastic'
  sku: {
    name: 'EP1'
    tier: 'ElasticPremium'
  }
  properties: {
    reserved: true
  }
}

resource insights 'Microsoft.Insights/components@2020-02-02' = {
  name: applicationInsightsName
  location: location
  tags: tags
  kind: 'web'
  properties: {
    Application_Type: 'web'
    WorkspaceResourceId: workspace.id
  }
}

resource otlpHeadersValue 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = if (otlpHeadersEnabled) {
  parent: keyVault
  name: 'cao-otel-headers'
  properties: {
    value: otlpHeaders
    contentType: 'OTLP collector authentication headers'
  }
}

resource functionApp 'Microsoft.Web/sites@2023-12-01' = {
  name: functionAppName
  location: location
  tags: tags
  kind: 'functionapp,linux,container'
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    serverFarmId: plan.id
    virtualNetworkSubnetId: network.outputs.functionSubnetId
    httpsOnly: true
    publicNetworkAccess: functionPublicNetworkAccess
    clientAffinityEnabled: false
    siteConfig: {
      ipSecurityRestrictionsDefaultAction: functionAccessRestrictionsDefaultAction
      ipSecurityRestrictions: functionAccessRestrictions
      scmIpSecurityRestrictionsDefaultAction: 'Deny'
      scmIpSecurityRestrictions: functionScmAccessRestrictions
      scmIpSecurityRestrictionsUseMain: functionScmAccessRestrictionsUseMain
      alwaysOn: true
      minimumElasticInstanceCount: 1
      ftpsState: 'Disabled'
      minTlsVersion: '1.2'
      http20Enabled: true
      linuxFxVersion: 'DOCKER|${registry.properties.loginServer}/${functionImageRepository}${functionImageSelector}'
      appCommandLine: 'azure-functions'
      acrUseManagedIdentityCreds: true
      appSettings: concat([
        {
          name: 'FUNCTIONS_EXTENSION_VERSION'
          value: '~4'
        }
        {
          name: 'FUNCTIONS_WORKER_RUNTIME'
          value: 'custom'
        }
        {
          name: 'WEBSITES_PORT'
          value: '8080'
        }
        {
          name: 'WEBSITES_ENABLE_APP_SERVICE_STORAGE'
          value: 'false'
        }
        {
          name: 'APPLICATIONINSIGHTS_CONNECTION_STRING'
          value: insights.properties.ConnectionString
        }
        {
          name: 'AzureWebJobsStorage__accountName'
          value: storage.name
        }
        {
          name: 'AzureWebJobsStorage__credential'
          value: 'managedidentity'
        }
        {
          name: 'CAO_DASHBOARD_HOSTING'
          value: 'azure-functions'
        }
        {
          name: 'CAO_POLICY_PATH'
          value: '.github/workflows/cao.azure.json'
        }
        {
          name: 'CAO_DATABASE_QUERIES'
          value: '/app/queries/database.json'
        }
        {
          name: 'CAO_AZURE_ALLOWED_HOSTS'
          value: join(allowedHosts, ',')
        }
        {
          name: 'CAO_AZURE_REQUIRE_HTTPS'
          value: 'true'
        }
        {
          name: 'REDIS_NAMESPACE'
          value: 'azure-dashboard'
        }
        {
          name: 'CAO_OPERATIONAL_NAMESPACE'
          value: 'azure-dashboard-operational'
        }
        {
          name: 'CAO_OPERATIONAL_CACHE_MAX_BYTES'
          value: string(operationalCacheMaxBytes)
        }
        {
          name: 'CAO_OPERATIONAL_CACHE_MAX_VALUE_BYTES'
          value: string(operationalCacheMaxValueBytes)
        }
        {
          name: 'CAO_OPERATIONAL_CACHE_MAX_ENTRIES'
          value: string(operationalCacheMaxEntries)
        }
        {
          name: 'CAO_OPERATIONAL_PROTECTED_MAX_BYTES'
          value: string(operationalProtectedMaxBytes)
        }
        {
          name: 'CAO_OPERATIONAL_PROTECTED_MAX_ENTRIES'
          value: string(operationalProtectedMaxEntries)
        }
        {
          name: 'CAO_POSTGRES_URL'
          value: '@Microsoft.KeyVault(SecretUri=${keyVault.properties.vaultUri}secrets/${postgresUrlSecretName})'
        }
        {
          name: 'CAO_POSTGRES_RUN_RETENTION_DAYS'
          value: string(postgresRunRetentionDays)
        }
        {
          name: 'CAO_POSTGRES_LINKED_RETENTION_DAYS'
          value: string(postgresRunRetentionDays)
        }
        {
          name: 'CAO_GITHUB_CLIENT_ID'
          value: githubClientId
        }
        {
          name: 'CAO_GITHUB_CLIENT_SECRET'
          value: '@Microsoft.KeyVault(SecretUri=${keyVault.properties.vaultUri}secrets/github-oauth-client-secret)'
        }
        {
          name: 'CAO_GITHUB_REDIRECT_URL'
          value: githubRedirectUri
        }
        {
          name: 'CAO_GITHUB_ALLOWED_ORGS'
          value: join(githubAllowedOrganizations, ',')
        }
        {
          name: 'CAO_GITHUB_ALLOWED_TEAMS'
          value: join(githubAllowedTeams, ',')
        }
        {
          name: 'CAO_GITHUB_ALLOWED_USERS'
          value: join(githubAllowedUsers, ',')
        }
        {
          name: 'CAO_SESSION_SECRET'
          value: '@Microsoft.KeyVault(SecretUri=${keyVault.properties.vaultUri}secrets/cao-session-secret)'
        }
      ], otlpEnvironment, !otlpLogsEnabled ? [] : [
        {
          name: 'DEBUG'
          value: 'cao:server,cao:telemetry'
        }
      ], !otlpHeadersEnabled ? [] : [
        {
          name: 'OTEL_EXPORTER_OTLP_HEADERS'
          value: '@Microsoft.KeyVault(SecretUri=${keyVault.properties.vaultUri}secrets/cao-otel-headers)'
        }
      ], empty(previousSessionSecret) ? [] : [
        {
          name: 'CAO_SESSION_SECRET_PREVIOUS'
          value: '@Microsoft.KeyVault(SecretUri=${keyVault.properties.vaultUri}secrets/cao-session-secret-previous)'
        }
      ], !collectionEnabled ? [] : [
        // With collection configured the Function App admits webhook
        // deliveries into the collection queue. It performs no collection of
        // its own, so its per-request work stays constant.
        {
          name: 'CAO_GITHUB_WEBHOOK_SECRET'
          value: '@Microsoft.KeyVault(SecretUri=${keyVault.properties.vaultUri}secrets/cao-github-webhook-secret)'
        }
        {
          name: 'CAO_GITHUB_ADMIN_USERS'
          value: join(githubAdminUsers, ',')
        }
        {
          name: 'CAO_COLLECT_APP_ID'
          value: collectorGithubAppId
        }
        {
          // The Function App verifies deliveries and enqueues work. It never
          // calls GitHub and never projects, so it is given no App private key
          // and no evidence lake: the internet-facing front end holds no
          // credential it cannot use.
          name: 'CAO_COLLECT_ADMIT_ONLY'
          value: 'true'
        }
        {
          name: 'CAO_COLLECT_CONTROL_REPOSITORY'
          value: collectorControlRepository
        }
      ])
    }
  }
  dependsOn: [
    githubClientSecretValue
    postgresConnectionStringValue
    sessionSecretValue
    previousSessionSecretValue
    otlpHeadersValue
  ]
}

module functionRoles 'function-roles.bicep' = {
  name: 'function-managed-identity-roles'
  params: {
    storageAccountName: storage.name
    keyVaultName: keyVault.name
    containerRegistryName: registry.name
    functionPrincipalId: functionApp.identity.principalId
    secretNames: concat([
      postgresUrlSecretName
      githubClientSecretValue.name
      sessionSecretValue.name
    ], empty(previousSessionSecret) ? [] : [previousSessionSecretValue.name], !collectionEnabled ? [] : [githubWebhookSecretValue.name], !otlpHeadersEnabled ? [] : ['cao-otel-headers'])
  }
}

module collection 'collector.bicep' = if (collectionEnabled) {
  name: 'collection-workers'
  params: {
    location: location
    tags: tags
    namePrefix: functionAppName
    environmentName: containerEnvironment.name
    containerRegistryName: registry.name
    collectorImage: collectorImage
    postgresRunRetentionDays: postgresRunRetentionDays
    keyVaultUri: keyVault.properties.vaultUri
    keyVaultName: keyVault.name
    postgresUrlSecretName: postgresUrlSecretName
    operationalNamespace: 'azure-dashboard-operational'
    operationalCacheMaxBytes: operationalCacheMaxBytes
    operationalCacheMaxValueBytes: operationalCacheMaxValueBytes
    operationalCacheMaxEntries: operationalCacheMaxEntries
    operationalProtectedMaxBytes: operationalProtectedMaxBytes
    operationalProtectedMaxEntries: operationalProtectedMaxEntries
    controlRepository: collectorControlRepository
    githubAppId: collectorGithubAppId
    workerReplicas: collectorWorkerReplicas
    lakeStorageSku: collectorLakeStorageSku
    applicationInsightsConnectionString: insights.properties.ConnectionString
    otlpEnvironment: otlpEnvironment
    otlpLogsEnabled: otlpLogsEnabled
    otlpHeadersEnabled: otlpHeadersEnabled
  }
  dependsOn: [
    collectorPrivateKeyValue
    postgresConnectionStringValue
    otlpHeadersValue
  ]
}

module ingestion 'ingestion.bicep' = {
  name: 'snapshot-ingestion'
  params: {
    location: location
    namePrefix: functionAppName
    environmentName: containerEnvironment.name
    containerRegistryName: registry.name
    keyVaultName: keyVault.name
    ingestionImage: '${registry.properties.loginServer}/${ingestionImageRepository}${ingestionImageSelector}'
    postgresRunRetentionDays: postgresRunRetentionDays
    postgresUrlSecretName: postgresUrlSecretName
    canonicalNamespace: 'azure-dashboard'
  }
  dependsOn: [postgresConnectionStringValue]
}

module provisioning 'provisioning.bicep' = {
  name: 'database-provisioning'
  params: {
    location: location
    namePrefix: functionAppName
    environmentName: containerEnvironment.name
    containerRegistryName: registry.name
    keyVaultName: keyVault.name
    provisioningImage: '${registry.properties.loginServer}/${provisioningImageRepository}${provisioningImageSelector}'
    postgresUrlSecretName: postgresUrlSecretName
    postgresHostName: network.outputs.postgresHostName
    postgresDatabaseName: network.outputs.databaseName
    postgresRunRetentionDays: postgresRunRetentionDays
    operationalCacheMaxBytes: operationalCacheMaxBytes
    operationalCacheMaxValueBytes: operationalCacheMaxValueBytes
    operationalCacheMaxEntries: operationalCacheMaxEntries
    operationalProtectedMaxBytes: operationalProtectedMaxBytes
    operationalProtectedMaxEntries: operationalProtectedMaxEntries
  }
  dependsOn: [postgresAdminUrl, postgresOwnerPasswordValue, postgresRuntimePasswordValue]
}

output collectionEnabled bool = collectionEnabled
output containerRegistryLoginServer string = registry.properties.loginServer
output functionContainerImage string = '${registry.properties.loginServer}/${functionImageRepository}${functionImageSelector}'
output functionHostName string = functionApp.properties.defaultHostName
output githubOAuthRedirectUri string = githubRedirectUri
output keyVaultUri string = keyVault.properties.vaultUri
output ingestionJobName string = ingestion.outputs.jobName
output provisioningJobName string = provisioning.outputs.jobName
output logAnalyticsWorkspaceId string = workspace.id
output applicationInsightsId string = insights.id
output postgresHostName string = network.outputs.postgresHostName
output adxClusterId string = adxEnabled ? adx!.outputs.clusterId : ''
output adxQueryEndpoint string = adxEnabled ? adx!.outputs.queryEndpoint : ''
output adxDatabaseName string = adxEnabled ? adx!.outputs.databaseName : ''
