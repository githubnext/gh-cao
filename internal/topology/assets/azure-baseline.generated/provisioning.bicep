param location string
@minLength(2)
@maxLength(19)
param namePrefix string
param environmentName string
param containerRegistryName string
param keyVaultName string
param provisioningImage string
param postgresUrlSecretName string
param postgresHostName string
param postgresDatabaseName string
@minValue(7)
@maxValue(3650)
param postgresRunRetentionDays int
param operationalCacheMaxBytes int
param operationalCacheMaxValueBytes int
param operationalCacheMaxEntries int
param operationalProtectedMaxBytes int
param operationalProtectedMaxEntries int

resource environment 'Microsoft.App/managedEnvironments@2024-03-01' existing = {
  name: environmentName
}
resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' existing = {
  name: containerRegistryName
}
resource keyVault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: keyVaultName
}
resource identity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${namePrefix}-db-provision-identity'
  location: location
}

var secretNames = [
  '${postgresUrlSecretName}-admin'
  '${postgresUrlSecretName}-owner-password'
  '${postgresUrlSecretName}-runtime-password'
]
module roles 'worker-roles.bicep' = {
  name: 'database-provisioning-roles'
  params: {
    keyVaultName: keyVault.name
    containerRegistryName: registry.name
    principalId: identity.properties.principalId
    secretNames: secretNames
  }
}

resource job 'Microsoft.App/jobs@2024-03-01' = {
  name: '${namePrefix}-db-provision'
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${identity.id}': {}
    }
  }
  properties: {
    environmentId: environment.id
    configuration: {
      triggerType: 'Manual'
      replicaTimeout: 7200
      replicaRetryLimit: 0
      manualTriggerConfig: {
        parallelism: 1
        replicaCompletionCount: 1
      }
      registries: [
        {
          server: registry.properties.loginServer
          identity: identity.id
        }
      ]
      secrets: [for secretName in secretNames: {
        name: secretName
        keyVaultUrl: '${keyVault.properties.vaultUri}secrets/${secretName}'
        identity: identity.id
      }]
    }
    template: {
      containers: [
        {
          name: 'db-provision'
          image: provisioningImage
          env: [
            {
              name: 'CAO_DATABASE_ADMIN_URL'
              secretRef: secretNames[0]
            }
            {
              name: 'CAO_POSTGRES_OWNER_PASSWORD'
              secretRef: secretNames[1]
            }
            {
              name: 'CAO_POSTGRES_RUNTIME_PASSWORD'
              secretRef: secretNames[2]
            }
            {
              name: 'PGHOST'
              value: postgresHostName
            }
            {
              name: 'PGDATABASE'
              value: postgresDatabaseName
            }
            {
              name: 'PGSSLMODE'
              value: 'verify-full'
            }
            {
              // libpq otherwise requires ~/.postgresql/root.crt for verify-full.
              name: 'PGSSLROOTCERT'
              value: 'system'
            }
            {
              name: 'CAO_POSTGRES_RUN_RETENTION_DAYS'
              value: string(postgresRunRetentionDays)
            }
            {
              name: 'CAO_POSTGRES_LINKED_RETENTION_DAYS'
              value: string(postgresRunRetentionDays)
            }
            {
              name: 'CAO_OPERATIONAL_CACHE_MAX_BYTES'
              value: string(operationalCacheMaxBytes)
            }
            {
              name: 'CAO_OPERATIONAL_CACHE_MAX_VALUE_BYTES'
              value: string(operationalCacheMaxValueBytes)
            }
            {
              name: 'CAO_OPERATIONAL_CACHE_MAX_ENTRIES'
              value: string(operationalCacheMaxEntries)
            }
            {
              name: 'CAO_OPERATIONAL_PROTECTED_MAX_BYTES'
              value: string(operationalProtectedMaxBytes)
            }
            {
              name: 'CAO_OPERATIONAL_PROTECTED_MAX_ENTRIES'
              value: string(operationalProtectedMaxEntries)
            }
          ]
          resources: {
            cpu: json('1.0')
            memory: '2Gi'
          }
        }
      ]
    }
  }
  dependsOn: [roles]
}

output jobName string = job.name
