targetScope = 'resourceGroup'

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
