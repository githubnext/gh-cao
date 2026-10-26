param location string
param clusterName string
param databaseName string
param virtualNetworkId string
param privateEndpointSubnetId string
param tags object = {}

@description('Reviewed paid ADX SKU. The default is a development SKU without an SLA, not a free cluster.')
param skuName string = 'Dev(No SLA)_Standard_D11_v2'

@allowed([
  'Basic'
  'Standard'
])
param skuTier string = 'Basic'

@minValue(1)
param capacity int = 1

resource cluster 'Microsoft.Kusto/clusters@2024-04-13' = {
  name: clusterName
  location: location
  tags: tags
  sku: {
    name: skuName
    tier: skuTier
    capacity: capacity
  }
  properties: {
    publicNetworkAccess: 'Disabled'
    enableDiskEncryption: true
    enableStreamingIngest: true
  }
}

resource database 'Microsoft.Kusto/clusters/databases@2024-04-13' = {
  parent: cluster
  name: databaseName
  location: location
  kind: 'ReadWrite'
  properties: {
    softDeletePeriod: 'P7D'
    hotCachePeriod: 'P1D'
  }
}

resource dns 'Microsoft.Network/privateDnsZones@2024-06-01' = {
  name: 'privatelink.${location}.kusto.windows.net'
  location: 'global'
}

resource dnsLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2024-06-01' = {
  parent: dns
  name: '${clusterName}-adx'
  location: 'global'
  properties: {
    registrationEnabled: false
    virtualNetwork: {
      id: virtualNetworkId
    }
  }
}

resource endpoint 'Microsoft.Network/privateEndpoints@2024-05-01' = {
  name: '${clusterName}-adx'
  location: location
  tags: tags
  properties: {
    subnet: {
      id: privateEndpointSubnetId
    }
    privateLinkServiceConnections: [
      {
        name: 'adx'
        properties: {
          privateLinkServiceId: cluster.id
          groupIds: ['cluster']
        }
      }
    ]
  }
}

resource dnsZoneGroup 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2024-05-01' = {
  parent: endpoint
  name: 'adx'
  properties: {
    privateDnsZoneConfigs: [
      {
        name: 'adx'
        properties: {
          privateDnsZoneId: dns.id
        }
      }
    ]
  }
}

output clusterId string = cluster.id
output queryEndpoint string = cluster.properties.uri
output databaseName string = database.name
