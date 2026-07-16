// Consolidated role assignments for the sample.
//
// Includes:
//   - Storage / App Insights (from the upstream Go quickstart)
//   - Azure OpenAI  — data-plane inference access
//   - Cosmos DB     — data-plane read/write on the SQL container

param storageAccountName string
param appInsightsName string
param managedIdentityPrincipalId string // Function App UAMI principalId
param userIdentityPrincipalId string = '' // Interactive user (for local debugging)
param allowUserIdentityPrincipal bool = false
param enableBlob bool = true
param enableQueue bool = false
param enableTable bool = false

// Extension parameters for this sample's additional dependencies.
@description('Name of the Azure OpenAI account. Empty string disables AOAI role assignment.')
param openAiAccountName string = ''
@description('Name of the Cosmos DB account. Empty string disables Cosmos role assignment.')
param cosmosAccountName string = ''

// ── Built-in role IDs ────────────────────────────────────────────────
var storageRoleDefinitionId    = 'b7e6dc6d-f1e8-4753-8033-0f276bb0955b' // Storage Blob Data Owner
var queueRoleDefinitionId      = '974c5e8b-45b9-4653-ba55-5f855dd0fb88' // Storage Queue Data Contributor
var tableRoleDefinitionId      = '0a9a7e1f-b9d0-4cc4-a60d-0319b160aaa3' // Storage Table Data Contributor
var monitoringRoleDefinitionId = '3913510d-42f4-4e42-8a64-420c390055eb' // Monitoring Metrics Publisher
var openAiUserRoleDefinitionId = '5e0bd9bd-7b93-4f28-af87-19fc36ad61bd' // Cognitive Services OpenAI User

// Cosmos "Built-in Data Contributor" is a built-in SQL role at the
// account scope. It's a GUID that's constant across all Cosmos
// accounts. Full resourceId is constructed against the account.
var cosmosDataContributorRoleId = '00000000-0000-0000-0000-000000000002'

resource storageAccount 'Microsoft.Storage/storageAccounts@2022-09-01' existing = {
  name: storageAccountName
}

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = {
  name: appInsightsName
}

resource openAiAccount 'Microsoft.CognitiveServices/accounts@2024-10-01' existing = if (!empty(openAiAccountName)) {
  name: openAiAccountName
}

resource cosmosAccount 'Microsoft.DocumentDB/databaseAccounts@2024-05-15' existing = if (!empty(cosmosAccountName)) {
  name: cosmosAccountName
}

// ── Storage (Blob) ───────────────────────────────────────────────────
resource storageRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (enableBlob) {
  name: guid(storageAccount.id, managedIdentityPrincipalId, storageRoleDefinitionId)
  scope: storageAccount
  properties: {
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', storageRoleDefinitionId)
    principalId: managedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}

resource storageRoleAssignment_User 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (enableBlob && allowUserIdentityPrincipal && !empty(userIdentityPrincipalId)) {
  name: guid(storageAccount.id, userIdentityPrincipalId, storageRoleDefinitionId)
  scope: storageAccount
  properties: {
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', storageRoleDefinitionId)
    principalId: userIdentityPrincipalId
    principalType: 'User'
  }
}

// ── Storage (Queue) ──────────────────────────────────────────────────
resource queueRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (enableQueue) {
  name: guid(storageAccount.id, managedIdentityPrincipalId, queueRoleDefinitionId)
  scope: storageAccount
  properties: {
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', queueRoleDefinitionId)
    principalId: managedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}

resource queueRoleAssignment_User 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (enableQueue && allowUserIdentityPrincipal && !empty(userIdentityPrincipalId)) {
  name: guid(storageAccount.id, userIdentityPrincipalId, queueRoleDefinitionId)
  scope: storageAccount
  properties: {
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', queueRoleDefinitionId)
    principalId: userIdentityPrincipalId
    principalType: 'User'
  }
}

// ── Storage (Table) ──────────────────────────────────────────────────
resource tableRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (enableTable) {
  name: guid(storageAccount.id, managedIdentityPrincipalId, tableRoleDefinitionId)
  scope: storageAccount
  properties: {
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', tableRoleDefinitionId)
    principalId: managedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}

resource tableRoleAssignment_User 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (enableTable && allowUserIdentityPrincipal && !empty(userIdentityPrincipalId)) {
  name: guid(storageAccount.id, userIdentityPrincipalId, tableRoleDefinitionId)
  scope: storageAccount
  properties: {
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', tableRoleDefinitionId)
    principalId: userIdentityPrincipalId
    principalType: 'User'
  }
}

// ── Application Insights ─────────────────────────────────────────────
resource appInsightsRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(applicationInsights.id, managedIdentityPrincipalId, monitoringRoleDefinitionId)
  scope: applicationInsights
  properties: {
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', monitoringRoleDefinitionId)
    principalId: managedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}

resource appInsightsRoleAssignment_User 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (allowUserIdentityPrincipal && !empty(userIdentityPrincipalId)) {
  name: guid(applicationInsights.id, userIdentityPrincipalId, monitoringRoleDefinitionId)
  scope: applicationInsights
  properties: {
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', monitoringRoleDefinitionId)
    principalId: userIdentityPrincipalId
    principalType: 'User'
  }
}

// ── Azure OpenAI (data plane) ────────────────────────────────────────
// The "Cognitive Services OpenAI User" role grants POST access to
// completions/embeddings without allowing account-level changes.
resource openAiRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!empty(openAiAccountName)) {
  name: guid(openAiAccount.id, managedIdentityPrincipalId, openAiUserRoleDefinitionId)
  scope: openAiAccount
  properties: {
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', openAiUserRoleDefinitionId)
    principalId: managedIdentityPrincipalId
    principalType: 'ServicePrincipal'
  }
}

resource openAiRoleAssignment_User 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!empty(openAiAccountName) && allowUserIdentityPrincipal && !empty(userIdentityPrincipalId)) {
  name: guid(openAiAccount.id, userIdentityPrincipalId, openAiUserRoleDefinitionId)
  scope: openAiAccount
  properties: {
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', openAiUserRoleDefinitionId)
    principalId: userIdentityPrincipalId
    principalType: 'User'
  }
}

// ── Cosmos DB (data plane, SQL API) ──────────────────────────────────
// Cosmos data-plane RBAC lives in a separate resource type
// (Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments) rather
// than the generic Microsoft.Authorization one. principalType is
// inferred by Cosmos and shouldn't be set here.
resource cosmosRoleAssignment 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2024-05-15' = if (!empty(cosmosAccountName)) {
  parent: cosmosAccount
  name: guid(cosmosAccount.id, managedIdentityPrincipalId, cosmosDataContributorRoleId)
  properties: {
    roleDefinitionId: '${cosmosAccount.id}/sqlRoleDefinitions/${cosmosDataContributorRoleId}'
    principalId: managedIdentityPrincipalId
    scope: cosmosAccount.id
  }
}

resource cosmosRoleAssignment_User 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2024-05-15' = if (!empty(cosmosAccountName) && allowUserIdentityPrincipal && !empty(userIdentityPrincipalId)) {
  parent: cosmosAccount
  name: guid(cosmosAccount.id, userIdentityPrincipalId, cosmosDataContributorRoleId)
  properties: {
    roleDefinitionId: '${cosmosAccount.id}/sqlRoleDefinitions/${cosmosDataContributorRoleId}'
    principalId: userIdentityPrincipalId
    scope: cosmosAccount.id
  }
}
