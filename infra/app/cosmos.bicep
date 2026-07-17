// ─── Cosmos DB (NoSQL API) for chat session persistence ──────────────
//
// One account, one database ("chat"), one container ("conversations")
// partitioned by /conversationId. Serverless capacity keeps costs to
// pay-per-request; the sample's traffic is bursty and unpredictable.
//
// Local (key-based) auth is disabled — the Function App's Managed
// Identity is granted the built-in "Cosmos DB Built-in Data
// Contributor" data-plane role in rbac.bicep. There are two RBAC
// systems on Cosmos: control plane (Azure RBAC) and data plane
// (SQL role assignments); we only need the data-plane role for
// document read/write.

@description('Name of the Cosmos DB account.')
param name string
@description('Azure region for the account.')
param location string
param tags object = {}

@description('Database name used by the app (COSMOS_DATABASE).')
param databaseName string = 'chat'
@description('Container name used by the app (COSMOS_CONTAINER).')
param containerName string = 'conversations'
@description('Partition key path for the container.')
param partitionKeyPath string = '/conversationId'

resource account 'Microsoft.DocumentDB/databaseAccounts@2024-05-15' = {
  name: name
  location: location
  tags: tags
  kind: 'GlobalDocumentDB'
  properties: {
    databaseAccountOfferType: 'Standard'
    consistencyPolicy: {
      defaultConsistencyLevel: 'Session'
    }
    locations: [
      {
        locationName: location
        failoverPriority: 0
        isZoneRedundant: false
      }
    ]
    capabilities: [
      // Serverless is a "capability" on Cosmos, not a SKU.
      {
        name: 'EnableServerless'
      }
    ]
    // AAD-only. Requires SQL role assignments in rbac.bicep.
    disableLocalAuth: true
    publicNetworkAccess: 'Enabled'
    minimalTlsVersion: 'Tls12'
  }
}

resource database 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases@2024-05-15' = {
  parent: account
  name: databaseName
  properties: {
    resource: {
      id: databaseName
    }
  }
}

resource container 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2024-05-15' = {
  parent: database
  name: containerName
  properties: {
    resource: {
      id: containerName
      partitionKey: {
        paths: [partitionKeyPath]
        kind: 'Hash'
      }
      // Indexing everything is fine for a chat container — writes
      // dominate but each doc is small and hot documents are read
      // by ID. If this became a bottleneck we'd exclude the
      // messages array from indexing.
      indexingPolicy: {
        indexingMode: 'consistent'
        includedPaths: [
          { path: '/*' }
        ]
        excludedPaths: [
          { path: '/"_etag"/?' }
        ]
      }
    }
  }
}

// Briefings container removed — the /api/brief/today endpoint runs the
// multi-agent editor synchronously on every request, so no persistence
// is needed for the demo.

output name string = account.name
output resourceId string = account.id
output endpoint string = account.properties.documentEndpoint
output databaseName string = database.name
output containerName string = container.name
