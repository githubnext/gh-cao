targetScope = 'resourceGroup'

param location string
@minLength(2)
@maxLength(19)
param deploymentName string
param containerRegistryName string
param keyVaultName string
@description('Reviewed dashboard-runtime image with container/PostgreSQL host policy, pinned by digest.')
param dashboardImage string
param provisioningImage string
param ingestionImage string
@description('Enable only after successful provisioning, explicit ingestion and independent runtime approval.')
param runtimeEnabled bool = false
@description('Independently verified private IPv4/loopback ingress peer CIDRs; not client CIDRs.')
@minLength(1)
param trustedProxyCidrs array
param githubClientId string
param githubAllowedOrganizations array = []
param githubAllowedTeams array = []
param githubAllowedUsers array = []
@minLength(1)
param githubAdminUsers array
@secure()
param githubClientSecret string
@secure()
@minLength(32)
param sessionSecret string
@secure()
@minLength(32)
param githubWebhookSecret string
@secure()
@minLength(32)
param postgresAdministratorPassword string
@secure()
@minLength(32)
param postgresOwnerPassword string
@secure()
@minLength(32)
param postgresRuntimePassword string
param postgresSkuName string = 'Standard_B2s'
@allowed(['Burstable', 'GeneralPurpose', 'MemoryOptimized'])
param postgresSkuTier string = 'Burstable'
@minValue(7)
@maxValue(3650)
param postgresRunRetentionDays int

var postgresUrlSecretName = 'cao-postgres-runtime-url'
var operationalCacheMaxBytes = 33554432
var operationalCacheMaxValueBytes = 4194304
var operationalCacheMaxEntries = 1024
var operationalProtectedMaxBytes = 134217728
var operationalProtectedMaxEntries = 200000
var containerSubnet = '10.45.0.0/23'

resource network 'Microsoft.Network/virtualNetworks@2024-05-01' = {
  name: '${deploymentName}-vnet'
  location: location
  properties: {
    addressSpace: { addressPrefixes: ['10.45.0.0/16'] }
    subnets: [
      {
        name: 'containers'
        properties: {
          addressPrefix: containerSubnet
          delegations: [
            {
              name: 'containers'
              properties: { serviceName: 'Microsoft.App/environments' }
            }
          ]
        }
      }
      {
        name: 'postgres'
        properties: {
          addressPrefix: '10.45.2.0/24'
          delegations: [
            {
              name: 'postgres'
              properties: { serviceName: 'Microsoft.DBforPostgreSQL/flexibleServers' }
            }
          ]
        }
      }
    ]
  }
}

module database 'postgres.bicep' = {
  name: 'container-apps-postgres'
  params: {
    location: location
    name: '${deploymentName}-pg'
    virtualNetworkId: network.id
    subnetId: resourceId('Microsoft.Network/virtualNetworks/subnets', network.name, 'postgres')
    administratorPassword: postgresAdministratorPassword
    skuName: postgresSkuName
    skuTier: postgresSkuTier
  }
}

resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' = {
  name: containerRegistryName
  location: location
  sku: { name: 'Basic' }
  properties: { adminUserEnabled: false, publicNetworkAccess: 'Enabled' }
}

resource vault 'Microsoft.KeyVault/vaults@2023-07-01' = {
  name: keyVaultName
  location: location
  properties: {
    tenantId: subscription().tenantId
    sku: { family: 'A', name: 'standard' }
    enableRbacAuthorization: true
    enabledForTemplateDeployment: false
    enableSoftDelete: true
    enablePurgeProtection: true
    softDeleteRetentionInDays: 90
    publicNetworkAccess: 'Enabled'
  }
}

var secretNames = [
  'github-oauth-client-secret'
  'cao-session-secret'
  'cao-github-webhook-secret'
  postgresUrlSecretName
  '${postgresUrlSecretName}-admin'
  '${postgresUrlSecretName}-owner-password'
  '${postgresUrlSecretName}-runtime-password'
]
var secretValues = [
  githubClientSecret
  sessionSecret
  githubWebhookSecret
  'postgresql://cao_runtime_login:${uriComponent(postgresRuntimePassword)}@${database.outputs.hostName}:5432/cao?sslmode=verify-full&options=-csearch_path%3Dcao'
  'postgresql://caoadmin:${uriComponent(postgresAdministratorPassword)}@${database.outputs.hostName}:5432/cao?sslmode=verify-full'
  postgresOwnerPassword
  postgresRuntimePassword
]
resource secrets 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = [for (name, i) in secretNames: {
  parent: vault
  name: name
  properties: { value: secretValues[i] }
}]

resource workspace 'Microsoft.OperationalInsights/workspaces@2023-09-01' = {
  name: '${deploymentName}-logs'
  location: location
  properties: {
    sku: { name: 'PerGB2018' }
    retentionInDays: 30
  }
}

resource environment 'Microsoft.App/managedEnvironments@2024-03-01' = {
  name: '${deploymentName}-apps'
  location: location
  properties: {
    appLogsConfiguration: { destination: 'azure-monitor' }
    vnetConfiguration: {
      infrastructureSubnetId: resourceId('Microsoft.Network/virtualNetworks/subnets', network.name, 'containers')
      internal: false
    }
    workloadProfiles: [
      { name: 'Consumption', workloadProfileType: 'Consumption' }
    ]
    zoneRedundant: false
  }
}

resource diagnostics 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = {
  name: 'container-logs'
  scope: environment
  properties: {
    workspaceId: workspace.id
    logs: [
      { categoryGroup: 'allLogs', enabled: true }
    ]
  }
}

module provision 'provisioning.bicep' = {
  name: 'container-apps-provisioning'
  params: {
    location: location
    namePrefix: deploymentName
    environmentName: environment.name
    containerRegistryName: registry.name
    keyVaultName: vault.name
    provisioningImage: provisioningImage
    postgresUrlSecretName: postgresUrlSecretName
    postgresHostName: database.outputs.hostName
    postgresDatabaseName: 'cao'
    postgresRunRetentionDays: postgresRunRetentionDays
    operationalCacheMaxBytes: operationalCacheMaxBytes
    operationalCacheMaxValueBytes: operationalCacheMaxValueBytes
    operationalCacheMaxEntries: operationalCacheMaxEntries
    operationalProtectedMaxBytes: operationalProtectedMaxBytes
    operationalProtectedMaxEntries: operationalProtectedMaxEntries
  }
  dependsOn: [secrets]
}

module ingestion 'ingestion.bicep' = {
  name: 'container-apps-ingestion'
  params: {
    location: location
    namePrefix: deploymentName
    environmentName: environment.name
    containerRegistryName: registry.name
    keyVaultName: vault.name
    ingestionImage: ingestionImage
    postgresUrlSecretName: postgresUrlSecretName
    postgresRunRetentionDays: postgresRunRetentionDays
  }
  dependsOn: [secrets]
}

resource identity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${deploymentName}-web-identity'
  location: location
}

var webSecretNames = [
  'github-oauth-client-secret'
  'cao-session-secret'
  'cao-github-webhook-secret'
  postgresUrlSecretName
]
module roles 'worker-roles.bicep' = {
  name: 'container-apps-web-roles'
  params: {
    keyVaultName: vault.name
    containerRegistryName: registry.name
    principalId: identity.properties.principalId
    secretNames: webSecretNames
  }
  dependsOn: [secrets]
}

var dashboardHost = '${deploymentName}.${environment.properties.defaultDomain}'
resource dashboard 'Microsoft.App/containerApps@2024-03-01' = if (runtimeEnabled) {
  name: deploymentName
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: { '${identity.id}': {} }
  }
  properties: {
    managedEnvironmentId: environment.id
    workloadProfileName: 'Consumption'
    configuration: {
      activeRevisionsMode: 'Single'
      ingress: {
        external: true
        targetPort: 8080
        transport: 'http'
        allowInsecure: false
      }
      registries: [
        { server: registry.properties.loginServer, identity: identity.id }
      ]
      secrets: [for name in webSecretNames: {
        name: name
        keyVaultUrl: '${vault.properties.vaultUri}secrets/${name}'
        identity: identity.id
      }]
    }
    template: {
      scale: { minReplicas: 1, maxReplicas: 1 }
      containers: [
        {
          name: 'dashboard'
          image: dashboardImage
          command: ['/app/cao-dashboard']
          args: [
            'serve-hosted'
            '--listen'
            '0.0.0.0:8080'
            '--operational-store'
            'postgres'
            '--site'
            '/app/site'
            '--database-queries'
            '/app/queries/database.json'
          ]
          env: [
            { name: 'CAO_POLICY_PATH', value: '/app/.github/workflows/cao.json' }
            { name: 'CAO_ALLOWED_HOSTS', value: dashboardHost }
            { name: 'CAO_TRUSTED_PROXY_CIDRS', value: join(trustedProxyCidrs, ',') }
            { name: 'CAO_GITHUB_REDIRECT_URL', value: 'https://${dashboardHost}/auth/callback' }
            { name: 'CAO_GITHUB_CLIENT_ID', value: githubClientId }
            { name: 'CAO_GITHUB_CLIENT_SECRET', secretRef: webSecretNames[0] }
            { name: 'CAO_SESSION_SECRET', secretRef: webSecretNames[1] }
            { name: 'CAO_GITHUB_WEBHOOK_SECRET', secretRef: webSecretNames[2] }
            { name: 'CAO_POSTGRES_URL', secretRef: postgresUrlSecretName }
            { name: 'CAO_GITHUB_ALLOWED_ORGS', value: join(githubAllowedOrganizations, ',') }
            { name: 'CAO_GITHUB_ALLOWED_TEAMS', value: join(githubAllowedTeams, ',') }
            { name: 'CAO_GITHUB_ALLOWED_USERS', value: join(githubAllowedUsers, ',') }
            { name: 'CAO_GITHUB_ADMIN_USERS', value: join(githubAdminUsers, ',') }
            { name: 'CAO_EXTERNAL_INGESTION', value: 'true' }
            { name: 'REDIS_NAMESPACE', value: 'azure-dashboard' }
            { name: 'CAO_OPERATIONAL_NAMESPACE', value: 'azure-dashboard-operational' }
            { name: 'CAO_POSTGRES_RUN_RETENTION_DAYS', value: string(postgresRunRetentionDays) }
            { name: 'CAO_POSTGRES_LINKED_RETENTION_DAYS', value: string(postgresRunRetentionDays) }
            { name: 'CAO_OPERATIONAL_CACHE_MAX_BYTES', value: string(operationalCacheMaxBytes) }
            { name: 'CAO_OPERATIONAL_CACHE_MAX_VALUE_BYTES', value: string(operationalCacheMaxValueBytes) }
            { name: 'CAO_OPERATIONAL_CACHE_MAX_ENTRIES', value: string(operationalCacheMaxEntries) }
            { name: 'CAO_OPERATIONAL_PROTECTED_MAX_BYTES', value: string(operationalProtectedMaxBytes) }
            { name: 'CAO_OPERATIONAL_PROTECTED_MAX_ENTRIES', value: string(operationalProtectedMaxEntries) }
          ]
          resources: { cpu: json('1.0'), memory: '2Gi' }
          probes: [
            { type: 'Startup', tcpSocket: { port: 8080 }, periodSeconds: 5, failureThreshold: 60 }
            { type: 'Liveness', tcpSocket: { port: 8080 }, periodSeconds: 30 }
            { type: 'Readiness', tcpSocket: { port: 8080 }, periodSeconds: 10 }
          ]
        }
      ]
    }
  }
  dependsOn: [roles]
}

output dashboardHostName string = dashboardHost
output githubRedirectUri string = 'https://${dashboardHost}/auth/callback'
output containerRegistryLoginServer string = registry.properties.loginServer
output environmentName string = environment.name
output provisioningJobName string = provision.outputs.jobName
output ingestionJobName string = ingestion.outputs.jobName
