targetScope = 'resourceGroup'

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
