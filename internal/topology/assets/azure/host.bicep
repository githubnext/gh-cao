param location string
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
