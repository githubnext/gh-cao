package topology

// These templates are authored in Go; deployment files are reconstructed on demand.
const compactMainBicep = `targetScope = 'resourceGroup'

param location string
@minLength(2)
@maxLength(19)
param deploymentName string
param sshPublicKey string
param administrator string = 'caoadmin'
param vmSize string = 'Standard_B2s'
@minValue(16)
param dataDiskSizeGB int = 64
@minLength(1)
param httpsSourceCidrs array
param managedPostgres bool = false
param postgresServerName string = '${deploymentName}-pg'
@secure()
param postgresAdministratorPassword string = ''
param postgresSkuName string = 'Standard_B2s'
param postgresSkuTier string = 'Burstable'
param githubClientId string
param githubAllowedOrganizations array = []
param githubAllowedTeams array = []
param githubAllowedUsers array = []
@minLength(1)
param githubAdminUsers array
@minValue(7)
@maxValue(3650)
param postgresRunRetentionDays int

resource nsg 'Microsoft.Network/networkSecurityGroups@2024-05-01' = {
  name: '${deploymentName}-https'
  location: location
  properties: {
    securityRules: [
      {
        name: 'reviewed-https'
        properties: {
          priority: 100
          access: 'Allow'
          direction: 'Inbound'
          protocol: 'Tcp'
          sourceAddressPrefixes: httpsSourceCidrs
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '443'
        }
      }
      {
        name: 'deny-other-inbound'
        properties: {
          priority: 4096
          access: 'Deny'
          direction: 'Inbound'
          protocol: '*'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '*'
        }
      }
    ]
  }
}

resource network 'Microsoft.Network/virtualNetworks@2024-05-01' = {
  name: '${deploymentName}-vnet'
  location: location
  properties: {
    addressSpace: { addressPrefixes: ['10.44.0.0/16'] }
    subnets: concat([
      {
        name: 'vm'
        properties: {
          addressPrefix: '10.44.0.0/24'
          defaultOutboundAccess: false
          networkSecurityGroup: { id: nsg.id }
        }
      }
    ], managedPostgres ? [
      {
        name: 'postgres'
        properties: {
          addressPrefix: '10.44.1.0/24'
          delegations: [
            {
              name: 'postgres'
              properties: { serviceName: 'Microsoft.DBforPostgreSQL/flexibleServers' }
            }
          ]
        }
      }
    ] : [])
  }
}

module host 'vm-host.bicep' = {
  name: 'compact-dashboard-host'
  params: {
    location: location
    name: deploymentName
    role: 'dashboard'
    subnetId: resourceId('Microsoft.Network/virtualNetworks/subnets', network.name, 'vm')
    administrator: administrator
    sshPublicKey: sshPublicKey
    vmSize: vmSize
    publicEndpoint: true
    persistentDataDisk: true
    dataDiskSizeGB: dataDiskSizeGB
    localPostgres: !managedPostgres
  }
}

module database 'postgres.bicep' = if (managedPostgres) {
  name: 'compact-managed-postgres'
  params: {
    location: location
    name: postgresServerName
    virtualNetworkId: network.id
    subnetId: resourceId('Microsoft.Network/virtualNetworks/subnets', network.name, 'postgres')
    administratorPassword: postgresAdministratorPassword
    skuName: postgresSkuName
    skuTier: postgresSkuTier
  }
}

output dashboardHostName string = host.outputs.hostName
output githubOAuthRedirectUri string = 'https://${host.outputs.hostName}/auth/callback'
output postgresHostName string = managedPostgres ? database!.outputs.hostName : 'localhost'
output hostedConfiguration object = {
  githubClientId: githubClientId
  githubAllowedOrganizations: githubAllowedOrganizations
  githubAllowedTeams: githubAllowedTeams
  githubAllowedUsers: githubAllowedUsers
  githubAdminUsers: githubAdminUsers
  postgresRunRetentionDays: postgresRunRetentionDays
}
output runtimeActivation string = 'staged: provision database authority, retained evidence, runtime secrets, reviewed host policy and HTTPS before starting CAO'
`

const compactPostgresBicep = `param location string
param name string
param virtualNetworkId string
param subnetId string
@secure()
param administratorPassword string
param skuName string
param skuTier string

resource dns 'Microsoft.Network/privateDnsZones@2024-06-01' = {
  name: '${name}.private.postgres.database.azure.com'
  location: 'global'
}

resource link 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2024-06-01' = {
  parent: dns
  name: 'dashboard'
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: { id: virtualNetworkId }
  }
}

resource server 'Microsoft.DBforPostgreSQL/flexibleServers@2024-08-01' = {
  name: name
  location: location
  sku: { name: skuName, tier: skuTier }
  properties: {
    version: '16'
    administratorLogin: 'caoadmin'
    administratorLoginPassword: administratorPassword
    network: {
      delegatedSubnetResourceId: subnetId
      privateDnsZoneArmResourceId: dns.id
      publicNetworkAccess: 'Disabled'
    }
    storage: { storageSizeGB: 32, autoGrow: 'Enabled' }
    backup: { backupRetentionDays: 7, geoRedundantBackup: 'Disabled' }
    highAvailability: { mode: 'Disabled' }
  }
  dependsOn: [link]
}

resource database 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2024-08-01' = {
  parent: server
  name: 'cao'
  properties: { charset: 'UTF8', collation: 'en_US.utf8' }
}

output hostName string = server.properties.fullyQualifiedDomainName
`

const containerAppsMainBicep = `targetScope = 'resourceGroup'

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
`

const optionalHostBicep = `param location string
param name string
@allowed(['archive', 'gateway-receive', 'gateway-dispatch', 'dashboard'])
param role string
param subnetId string
param administrator string
param sshPublicKey string
param vmSize string
param containerRegistryName string = ''
param publicEndpoint bool = false
param persistentDataDisk bool = false
param localPostgres bool = false
@minValue(16)
param dataDiskSizeGB int = 128

resource address 'Microsoft.Network/publicIPAddresses@2024-05-01' = if (publicEndpoint) {
  name: '${name}-https'
  location: location
  sku: { name: 'Standard' }
  properties: {
    publicIPAllocationMethod: 'Static'
    dnsSettings: { domainNameLabel: name }
  }
}

resource nic 'Microsoft.Network/networkInterfaces@2024-05-01' = {
  name: '${name}-nic'
  location: location
  properties: {
    ipConfigurations: [
      {
        name: 'primary'
        properties: {
          privateIPAllocationMethod: 'Dynamic'
          subnet: { id: subnetId }
          publicIPAddress: publicEndpoint ? { id: address!.id } : null
        }
      }
    ]
  }
}

resource disk 'Microsoft.Compute/disks@2024-03-02' = if (persistentDataDisk) {
  name: '${name}-private-data'
  location: location
  sku: { name: 'StandardSSD_LRS' }
  properties: {
    creationData: { createOption: 'Empty' }
    diskSizeGB: dataDiskSizeGB
    networkAccessPolicy: 'DenyAll'
    publicNetworkAccess: 'Disabled'
  }
}

resource host 'Microsoft.Compute/virtualMachines@2024-07-01' = {
  name: name
  location: location
  tags: { workload: role == 'dashboard' ? 'gh-cao-dashboard' : 'gh-cao-optional', role: role, activation: 'staged' }
  identity: { type: 'SystemAssigned' }
  properties: {
    hardwareProfile: { vmSize: vmSize }
    securityProfile: {
      securityType: 'TrustedLaunch'
      uefiSettings: {
        secureBootEnabled: true
        vTpmEnabled: true
      }
    }
    osProfile: {
      computerName: name
      adminUsername: administrator
      customData: base64(localPostgres ? loadTextContent('host-init-postgres.yaml') : loadTextContent('host-init.yaml'))
      linuxConfiguration: {
        disablePasswordAuthentication: true
        provisionVMAgent: true
        ssh: {
          publicKeys: [
            {
              path: '/home/${administrator}/.ssh/authorized_keys'
              keyData: sshPublicKey
            }
          ]
        }
      }
    }
    storageProfile: {
      imageReference: {
        publisher: 'Canonical'
        offer: 'ubuntu-24_04-lts'
        sku: 'server'
        version: 'latest'
      }
      osDisk: {
        createOption: 'FromImage'
        managedDisk: { storageAccountType: 'StandardSSD_LRS' }
        deleteOption: 'Detach'
      }
      dataDisks: persistentDataDisk ? [
        {
          lun: 0
          name: disk!.name
          createOption: 'Attach'
          managedDisk: { id: disk!.id }
          deleteOption: 'Detach'
        }
      ] : []
    }
    networkProfile: {
      networkInterfaces: [{ id: nic.id }]
    }
  }
}

resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' existing = if (!empty(containerRegistryName)) {
  name: containerRegistryName
}

resource acrPull 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!empty(containerRegistryName)) {
  name: guid(registry!.id, host.id, 'AcrPull')
  scope: registry!
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '7f951dda-4ed3-4680-a7ca-43fe172d538d')
    principalId: host.identity.principalId
    principalType: 'ServicePrincipal'
  }
}

output principalId string = host.identity.principalId
output hostName string = publicEndpoint ? address!.properties.dnsSettings.fqdn : ''
`

const optionalHostsBicep = `targetScope = 'resourceGroup'

param location string
param namePrefix string
param baselineVirtualNetworkName string
param postgresServerName string
param containerRegistryName string
param sessionDataLakeEnabled bool = false
param webhookGatewayEnabled bool = false
param administrator string = 'caoadmin'
param sshPublicKey string
param vmSize string = 'Standard_B2s'
param archiveAllowedSourceCidrs array = []
param gatewayAllowedSourceCidrs array = []

module network 'network.bicep' = if (sessionDataLakeEnabled || webhookGatewayEnabled) {
  name: '${namePrefix}-feature-network'
  params: {
    location: location
    namePrefix: namePrefix
    baselineVirtualNetworkName: baselineVirtualNetworkName
    postgresServerName: postgresServerName
    archiveAllowedSourceCidrs: archiveAllowedSourceCidrs
    gatewayAllowedSourceCidrs: gatewayAllowedSourceCidrs
  }
}

module archive 'host.bicep' = if (sessionDataLakeEnabled) {
  name: '${namePrefix}-archive-host'
  params: {
    location: location
    name: '${namePrefix}-archive'
    role: 'archive'
    subnetId: network!.outputs.archiveSubnetId
    administrator: administrator
    sshPublicKey: sshPublicKey
    vmSize: vmSize
    containerRegistryName: containerRegistryName
    publicEndpoint: true
    persistentDataDisk: true
  }
}

module lake 'lake.bicep' = if (sessionDataLakeEnabled) {
  name: '${namePrefix}-session-lake'
  params: {
    location: location
    namePrefix: namePrefix
    virtualNetworkId: network!.outputs.virtualNetworkId
    endpointSubnetId: network!.outputs.endpointSubnetId
    archivePrincipalId: archive!.outputs.principalId
  }
}

module receive 'host.bicep' = if (webhookGatewayEnabled) {
  name: '${namePrefix}-gateway-receive'
  params: {
    location: location
    name: '${namePrefix}-gw-receive'
    role: 'gateway-receive'
    subnetId: network!.outputs.receiveSubnetId
    administrator: administrator
    sshPublicKey: sshPublicKey
    vmSize: vmSize
    containerRegistryName: containerRegistryName
    publicEndpoint: true
  }
}

module dispatch 'host.bicep' = if (webhookGatewayEnabled) {
  name: '${namePrefix}-gateway-dispatch'
  params: {
    location: location
    name: '${namePrefix}-gw-dispatch'
    role: 'gateway-dispatch'
    subnetId: network!.outputs.dispatchSubnetId
    administrator: administrator
    sshPublicKey: sshPublicKey
    vmSize: vmSize
    containerRegistryName: containerRegistryName
  }
}

output archiveHost string = sessionDataLakeEnabled ? archive!.outputs.hostName : ''
output archivePrincipalId string = sessionDataLakeEnabled ? archive!.outputs.principalId : ''
output archiveStorageAccount string = sessionDataLakeEnabled ? lake!.outputs.storageAccountName : ''
output archiveContainer string = sessionDataLakeEnabled ? lake!.outputs.containerName : ''
output archiveAdxEndpoint string = sessionDataLakeEnabled ? lake!.outputs.adxEndpoint : ''
output archiveAdxDatabase string = sessionDataLakeEnabled ? lake!.outputs.adxDatabase : ''
output gatewayHost string = webhookGatewayEnabled ? receive!.outputs.hostName : ''
output gatewayReceivePrincipalId string = webhookGatewayEnabled ? receive!.outputs.principalId : ''
output gatewayDispatchPrincipalId string = webhookGatewayEnabled ? dispatch!.outputs.principalId : ''
output runtimeActivation string = 'staged-only: review OPTIONAL-FEATURES.md and retain independent activation evidence'
`

const optionalLakeBicep = `param location string
param namePrefix string
param virtualNetworkId string
param endpointSubnetId string
param archivePrincipalId string

var suffix = uniqueString(resourceGroup().id, namePrefix, 'private-session-lake')
var clusterName = 'lake${suffix}'

resource storage 'Microsoft.Storage/storageAccounts@2023-05-01' = {
  name: 'lake${suffix}'
  location: location
  sku: { name: 'Standard_LRS' }
  kind: 'StorageV2'
  properties: {
    allowSharedKeyAccess: false
    allowBlobPublicAccess: false
    publicNetworkAccess: 'Disabled'
    minimumTlsVersion: 'TLS1_2'
    supportsHttpsTrafficOnly: true
    networkAcls: { defaultAction: 'Deny', bypass: 'None' }
  }
}

resource blobs 'Microsoft.Storage/storageAccounts/blobServices@2023-05-01' = {
  parent: storage
  name: 'default'
  properties: { isVersioningEnabled: false }
}

resource container 'Microsoft.Storage/storageAccounts/blobServices/containers@2023-05-01' = {
  parent: blobs
  name: 'unified-sessions'
  properties: { publicAccess: 'None' }
}

resource blobDns 'Microsoft.Network/privateDnsZones@2024-06-01' = {
  name: 'privatelink.blob.${environment().suffixes.storage}'
  location: 'global'
}

resource blobLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2024-06-01' = {
  parent: blobDns
  name: '${namePrefix}-archive'
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: { id: virtualNetworkId }
  }
}

resource blobEndpoint 'Microsoft.Network/privateEndpoints@2024-05-01' = {
  name: '${namePrefix}-archive-blob'
  location: location
  properties: {
    subnet: { id: endpointSubnetId }
    privateLinkServiceConnections: [
      {
        name: 'blob'
        properties: {
          privateLinkServiceId: storage.id
          groupIds: ['blob']
        }
      }
    ]
  }
}

resource blobZones 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2024-05-01' = {
  parent: blobEndpoint
  name: 'blob'
  properties: {
    privateDnsZoneConfigs: [{ name: 'blob', properties: { privateDnsZoneId: blobDns.id } }]
  }
}

resource cluster 'Microsoft.Kusto/clusters@2024-04-13' = {
  name: clusterName
  location: location
  identity: { type: 'SystemAssigned' }
  sku: { name: 'Dev(No SLA)_Standard_E2a_v4', tier: 'Basic', capacity: 1 }
  properties: {
    publicNetworkAccess: 'Disabled'
    enableDiskEncryption: true
  }
}

resource database 'Microsoft.Kusto/clusters/databases@2024-04-13' = {
  parent: cluster
  name: 'UnifiedSessions'
  location: location
  kind: 'ReadWrite'
  properties: { softDeletePeriod: 'P7D', hotCachePeriod: 'P1D' }
}

resource adxDns 'Microsoft.Network/privateDnsZones@2024-06-01' = {
  name: 'privatelink.${location}.kusto.windows.net'
  location: 'global'
}

resource adxLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2024-06-01' = {
  parent: adxDns
  name: '${namePrefix}-archive'
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: { id: virtualNetworkId }
  }
}

resource adxEndpoint 'Microsoft.Network/privateEndpoints@2024-05-01' = {
  name: '${namePrefix}-archive-adx'
  location: location
  properties: {
    subnet: { id: endpointSubnetId }
    privateLinkServiceConnections: [
      {
        name: 'adx'
        properties: { privateLinkServiceId: cluster.id, groupIds: ['cluster'] }
      }
    ]
  }
}

resource adxZones 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2024-05-01' = {
  parent: adxEndpoint
  name: 'adx'
  properties: {
    privateDnsZoneConfigs: [{ name: 'adx', properties: { privateDnsZoneId: adxDns.id } }]
  }
}

resource adxBlobEndpoint 'Microsoft.Kusto/clusters/managedPrivateEndpoints@2024-04-13' = {
  parent: cluster
  name: 'archive-blob'
  properties: {
    privateLinkResourceId: storage.id
    groupId: 'blob'
    requestMessage: 'Reviewed private unified-session archive access; approve independently before activation.'
  }
}

resource writer 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(container.id, archivePrincipalId, 'Storage Blob Data Contributor')
  scope: container
  properties: {
    principalId: archivePrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'ba92f5b4-2d11-453d-a403-e96b0029c9fe')
  }
}

resource reader 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(container.id, cluster.id, 'Storage Blob Data Reader')
  scope: container
  properties: {
    principalId: cluster.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '2a2b9908-6ea1-4ae2-8e65-a410df84e7d1')
  }
}

output storageAccountName string = storage.name
output containerName string = container.name
output adxEndpoint string = cluster.properties.uri
output adxDatabase string = database.name
`

const optionalNetworkBicep = `param location string
param namePrefix string
param baselineVirtualNetworkName string
param postgresServerName string
param archiveAllowedSourceCidrs array
param gatewayAllowedSourceCidrs array

var roles = ['archive', 'receive', 'dispatch']
var sourceCidrs = [archiveAllowedSourceCidrs, gatewayAllowedSourceCidrs, []]

resource nsg 'Microsoft.Network/networkSecurityGroups@2024-05-01' = [for (role, i) in roles: {
  name: '${namePrefix}-${role}-nsg'
  location: location
  properties: {
    securityRules: concat(empty(sourceCidrs[i]) ? [] : [
      {
        name: 'reviewed-https'
        properties: {
          priority: 100
          access: 'Allow'
          direction: 'Inbound'
          protocol: 'Tcp'
          sourceAddressPrefixes: sourceCidrs[i]
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '443'
        }
      }
    ], [
      {
        name: 'deny-other-inbound'
        properties: {
          priority: 4096
          access: 'Deny'
          direction: 'Inbound'
          protocol: '*'
          sourceAddressPrefix: '*'
          sourcePortRange: '*'
          destinationAddressPrefix: '*'
          destinationPortRange: '*'
        }
      }
    ])
  }
}]

resource egressAddress 'Microsoft.Network/publicIPAddresses@2024-05-01' = {
  name: '${namePrefix}-feature-egress'
  location: location
  sku: { name: 'Standard' }
  properties: { publicIPAllocationMethod: 'Static' }
}

resource nat 'Microsoft.Network/natGateways@2024-05-01' = {
  name: '${namePrefix}-feature-nat'
  location: location
  sku: { name: 'Standard' }
  properties: {
    publicIpAddresses: [{ id: egressAddress.id }]
    idleTimeoutInMinutes: 4
  }
}

var hostSubnets = [for (role, i) in roles: {
  name: role
  properties: {
    addressPrefix: '10.43.${i}.0/24'
    defaultOutboundAccess: false
    networkSecurityGroup: { id: nsg[i].id }
    natGateway: { id: nat.id }
  }
}]

resource network 'Microsoft.Network/virtualNetworks@2024-05-01' = {
  name: '${namePrefix}-features-vnet'
  location: location
  properties: {
    addressSpace: { addressPrefixes: ['10.43.0.0/16'] }
    subnets: concat(hostSubnets, [
      {
        name: 'endpoints'
        properties: {
          addressPrefix: '10.43.3.0/24'
          privateEndpointNetworkPolicies: 'Disabled'
          defaultOutboundAccess: false
        }
      }
    ])
  }
}

resource baseline 'Microsoft.Network/virtualNetworks@2024-05-01' existing = {
  name: baselineVirtualNetworkName
}

resource forward 'Microsoft.Network/virtualNetworks/virtualNetworkPeerings@2024-05-01' = {
  parent: network
  name: 'baseline'
  properties: {
    remoteVirtualNetwork: { id: baseline.id }
    allowVirtualNetworkAccess: true
    allowForwardedTraffic: false
    allowGatewayTransit: false
    useRemoteGateways: false
  }
}

resource reverse 'Microsoft.Network/virtualNetworks/virtualNetworkPeerings@2024-05-01' = {
  parent: baseline
  name: '${namePrefix}-features'
  properties: {
    remoteVirtualNetwork: { id: network.id }
    allowVirtualNetworkAccess: true
    allowForwardedTraffic: false
    allowGatewayTransit: false
    useRemoteGateways: false
  }
}

resource postgresDns 'Microsoft.Network/privateDnsZones@2024-06-01' existing = {
  name: '${postgresServerName}.private.postgres.database.azure.com'
}

resource postgresLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2024-06-01' = {
  parent: postgresDns
  name: '${namePrefix}-features'
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: { id: network.id }
  }
}

output virtualNetworkId string = network.id
output archiveSubnetId string = resourceId('Microsoft.Network/virtualNetworks/subnets', network.name, 'archive')
output receiveSubnetId string = resourceId('Microsoft.Network/virtualNetworks/subnets', network.name, 'receive')
output dispatchSubnetId string = resourceId('Microsoft.Network/virtualNetworks/subnets', network.name, 'dispatch')
output endpointSubnetId string = resourceId('Microsoft.Network/virtualNetworks/subnets', network.name, 'endpoints')
`
