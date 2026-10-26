param location string
param namePrefix string
param postgresServerName string
param postgresAdministratorLogin string
@secure()
param postgresAdministratorPassword string
param postgresSkuName string = 'Standard_B2s'
param postgresSkuTier string = 'Burstable'
@description('Optional existing network security group to keep attached to every subnet. Empty leaves subnets without an NSG.')
param subnetNetworkSecurityGroupId string = ''
@minLength(1)
@maxLength(63)
@description('PostgreSQL database that holds the canonical and operational stores. Changing it creates a new, empty database; the previous database is not deleted.')
param postgresDatabaseName string = 'cao'

param adxEnabled bool = false

var subnetNetworkSecurityGroup = empty(subnetNetworkSecurityGroupId) ? {} : {
  networkSecurityGroup: {
    id: subnetNetworkSecurityGroupId
  }
}

resource network 'Microsoft.Network/virtualNetworks@2024-05-01' = {
  name: '${namePrefix}-vnet'
  location: location
  properties: {
    addressSpace: {
      addressPrefixes: ['10.42.0.0/16']
    }
    subnets: concat([
      {
        name: 'functions'
        properties: union(subnetNetworkSecurityGroup, {
          addressPrefix: '10.42.0.0/24'
          delegations: [
            {
              name: 'functions'
              properties: {
                serviceName: 'Microsoft.Web/serverFarms'
              }
            }
          ]
        })
      }
      {
        name: 'postgres'
        properties: union(subnetNetworkSecurityGroup, {
          addressPrefix: '10.42.1.0/24'
          delegations: [
            {
              name: 'postgres'
              properties: {
                serviceName: 'Microsoft.DBforPostgreSQL/flexibleServers'
              }
            }
          ]
        })
      }
      {
        name: 'containers'
        properties: union(subnetNetworkSecurityGroup, {
          addressPrefix: '10.42.2.0/23'
        })
      }
    ], adxEnabled ? [
      {
        name: 'adx'
        properties: union(subnetNetworkSecurityGroup, {
          addressPrefix: '10.42.4.0/24'
          privateEndpointNetworkPolicies: 'Disabled'
        })
      }
    ] : [])
  }
}

resource dns 'Microsoft.Network/privateDnsZones@2024-06-01' = {
  name: '${postgresServerName}.private.postgres.database.azure.com'
  location: 'global'
}

resource dnsLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2024-06-01' = {
  parent: dns
  name: '${namePrefix}-postgres'
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: {
      id: network.id
    }
  }
}

resource postgres 'Microsoft.DBforPostgreSQL/flexibleServers@2024-08-01' = {
  name: postgresServerName
  location: location
  sku: {
    name: postgresSkuName
    tier: postgresSkuTier
  }
  properties: {
    version: '16'
    administratorLogin: postgresAdministratorLogin
    administratorLoginPassword: postgresAdministratorPassword
    network: {
      delegatedSubnetResourceId: resourceId('Microsoft.Network/virtualNetworks/subnets', network.name, 'postgres')
      privateDnsZoneArmResourceId: dns.id
      publicNetworkAccess: 'Disabled'
    }
    storage: {
      storageSizeGB: 32
      autoGrow: 'Enabled'
    }
    backup: {
      backupRetentionDays: 7
      geoRedundantBackup: 'Disabled'
    }
    highAvailability: {
      mode: 'Disabled'
    }
  }
  dependsOn: [dnsLink]
}

resource database 'Microsoft.DBforPostgreSQL/flexibleServers/databases@2024-08-01' = {
  parent: postgres
  name: postgresDatabaseName
  properties: {
    charset: 'UTF8'
    collation: 'en_US.utf8'
  }
}

output postgresHostName string = postgres.properties.fullyQualifiedDomainName
output databaseName string = database.name
output functionSubnetId string = resourceId('Microsoft.Network/virtualNetworks/subnets', network.name, 'functions')
output containerSubnetId string = resourceId('Microsoft.Network/virtualNetworks/subnets', network.name, 'containers')
output virtualNetworkId string = network.id
output adxSubnetId string = adxEnabled ? resourceId('Microsoft.Network/virtualNetworks/subnets', network.name, 'adx') : ''
