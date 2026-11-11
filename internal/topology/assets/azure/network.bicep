param location string
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
