param location string
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
