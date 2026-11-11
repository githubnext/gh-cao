param location string
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
