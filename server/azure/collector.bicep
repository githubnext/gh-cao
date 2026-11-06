// Optional collection workers for the server ingestion profile.
//
// This module deploys nothing unless a collector image is supplied, so the
// default deployment stays exactly as it is: a Function App serving the
// dashboard from snapshots published by the Activity workflow.
//
// The workers run native Go acquisition in the `collect` role and share the
// durable PostgreSQL operational queue with the Function App.

@description('Location for the collection workers.')
param location string

@description('Resource tags.')
param tags object

@minLength(2)
@maxLength(22)
@description('Name prefix for the collection resources.')
param namePrefix string

@description('Container image running the collection role.')
param collectorImage string

param environmentName string

@description('Deployment registry containing the private collector image.')
param containerRegistryName string

@minValue(7)
@maxValue(3650)
param postgresRunRetentionDays int = 45

@description('Key Vault URI holding the collection secrets.')
param keyVaultUri string

@description('Key Vault resource name, used to grant the workers secret access.')
param keyVaultName string

@description('Key Vault secret that stores the PostgreSQL URL.')
param postgresUrlSecretName string = 'cao-postgres-runtime-url'

@minValue(1)
@description('Maximum bytes retained in the disposable operational cache.')
param operationalCacheMaxBytes int = 33554432

@minValue(1)
@description('Maximum size of one disposable operational cache value.')
param operationalCacheMaxValueBytes int = 4194304

@minValue(1)
@description('Maximum number of disposable operational cache entries.')
param operationalCacheMaxEntries int = 1024

@minValue(1)
@description('Maximum bytes retained in protected operational records.')
param operationalProtectedMaxBytes int = 134217728

@minValue(1)
@description('Maximum number of protected operational records.')
param operationalProtectedMaxEntries int = 200000

@description('Operational PostgreSQL namespace shared with the Function App.')
param operationalNamespace string

@description('Control repository named by the reviewed inventory.')
param controlRepository string

@minLength(64)
@maxLength(64)
@description('Exact provisioned lowercase SHA-256 digest of immutable reviewed inventory bytes. The workers fail startup if this digest is not in their shared PostgreSQL namespace.')
param nativeInventoryDigest string

@description('GitHub App identifier whose installations define ingestion scope.')
param githubAppId string

@minValue(1)
@description('Fixed number of PostgreSQL-backed collection workers.')
param workerReplicas int = 1

@description('Platform Application Insights setting; does not activate Go OTLP exporters.')
@secure()
param applicationInsightsConnectionString string

@description('Shared Go OTLP/HTTP endpoint, protocol and explicit log opt-in from the parent deployment.')
param otlpEnvironment array = []

param otlpLogsEnabled bool = false

@description('Whether the parent provisioned the cao-otel-headers Key Vault secret.')
param otlpHeadersEnabled bool = false

@description('Bound on the collection task and dead-letter streams.')
@minValue(1000)
param queueMaxLength int = 200000

resource collectorIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: '${namePrefix}-collector-identity'
  location: location
  tags: tags
}

resource keyVault 'Microsoft.KeyVault/vaults@2023-07-01' existing = {
  name: keyVaultName
}

resource environment 'Microsoft.App/managedEnvironments@2024-03-01' existing = {
  name: environmentName
}

resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' existing = {
  name: containerRegistryName
}

module roles 'worker-roles.bicep' = {
  name: 'collector-roles'
  params: {
    keyVaultName: keyVault.name
    containerRegistryName: registry.name
    principalId: collectorIdentity.properties.principalId
    secretNames: concat([
      postgresUrlSecretName
      'cao-collect-private-key'
    ], !otlpHeadersEnabled ? [] : ['cao-otel-headers'])
  }
}

var registries = [
  {
    server: registry.properties.loginServer
    identity: collectorIdentity.id
  }
]

var collectionBaseEnvironment = [
  {
    name: 'CAO_POSTGRES_URL'
    secretRef: 'cao-postgres-url'
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
    name: 'CAO_POLICY_PATH'
    value: '/app/.github/workflows/cao.azure.json'
  }
  {
    name: 'REDIS_NAMESPACE'
    value: 'azure-dashboard'
  }
  {
    name: 'CAO_OPERATIONAL_NAMESPACE'
    value: operationalNamespace
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
  {
    name: 'CAO_COLLECT_APP_ID'
    value: githubAppId
  }
  {
    name: 'CAO_COLLECT_NATIVE'
    value: 'true'
  }
  {
    name: 'CAO_COLLECT_NATIVE_INVENTORY_DIGEST'
    value: nativeInventoryDigest
  }
  {
    name: 'CAO_COLLECT_PRIVATE_KEY'
    secretRef: 'cao-collect-private-key'
  }
  {
    name: 'CAO_COLLECT_CONTROL_REPOSITORY'
    value: controlRepository
  }
  {
    name: 'CAO_COLLECT_QUEUE_MAX_LENGTH'
    value: string(queueMaxLength)
  }
  {
    name: 'APPLICATIONINSIGHTS_CONNECTION_STRING'
    secretRef: 'application-insights-connection-string'
  }
  {
    name: 'OTEL_SERVICE_NAME'
    value: '${namePrefix}-collector'
  }
  {
    name: 'DEBUG'
    value: otlpLogsEnabled ? 'cao:telemetry,cao:collect:recovery' : 'cao:collect:*'
  }
]

var collectionEnvironment = concat(collectionBaseEnvironment, otlpEnvironment, !otlpHeadersEnabled ? [] : [
  {
    name: 'OTEL_EXPORTER_OTLP_HEADERS'
    secretRef: 'cao-otel-headers'
  }
])

var collectionSecrets = concat([
  {
    name: 'cao-postgres-url'
    keyVaultUrl: '${keyVaultUri}secrets/${postgresUrlSecretName}'
    identity: collectorIdentity.id
  }
  {
    name: 'cao-collect-private-key'
    keyVaultUrl: '${keyVaultUri}secrets/cao-collect-private-key'
    identity: collectorIdentity.id
  }
  {
    name: 'application-insights-connection-string'
    value: applicationInsightsConnectionString
  }
], !otlpHeadersEnabled ? [] : [
  {
    name: 'cao-otel-headers'
    keyVaultUrl: '${keyVaultUri}secrets/cao-otel-headers'
    identity: collectorIdentity.id
  }
])

resource collectors 'Microsoft.App/containerApps@2024-03-01' = {
  name: '${namePrefix}-collector'
  location: location
  tags: tags
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${collectorIdentity.id}': {}
    }
  }
  properties: {
    environmentId: environment.id
    configuration: {
      activeRevisionsMode: 'Single'
      registries: registries
      secrets: collectionSecrets
    }
    template: {
      containers: [
        {
          name: 'collector'
          image: collectorImage
          command: ['/app/cao-dashboard']
          args: ['collect', '--database-queries', '/app/queries/database.json']
          env: collectionEnvironment
          resources: {
            cpu: json('1.0')
            memory: '2Gi'
          }
        }
      ]
      scale: {
        minReplicas: workerReplicas
        maxReplicas: workerReplicas
      }
    }
  }
  dependsOn: [roles]
}

// Cold start runs as a job rather than as part of a worker, so repopulating a
// database is an explicit, observable operation that can be re-run safely.
resource backfill 'Microsoft.App/jobs@2024-03-01' = {
  name: '${namePrefix}-backfill'
  location: location
  tags: tags
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${collectorIdentity.id}': {}
    }
  }
  properties: {
    environmentId: environment.id
    configuration: {
      triggerType: 'Manual'
      registries: registries
      replicaTimeout: 7200
      replicaRetryLimit: 1
      manualTriggerConfig: {
        parallelism: 1
        replicaCompletionCount: 1
      }
      secrets: collectionSecrets
    }
    template: {
      containers: [
        {
          name: 'backfill'
          image: collectorImage
          command: ['/app/cao-dashboard']
          args: ['backfill', '--database-queries', '/app/queries/database.json']
          env: collectionEnvironment
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

output collectorIdentityPrincipalId string = collectorIdentity.properties.principalId
output collectorAppName string = collectors.name
output backfillJobName string = backfill.name
