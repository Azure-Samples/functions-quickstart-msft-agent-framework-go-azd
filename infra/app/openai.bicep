// ─── Azure OpenAI account + chat model deployment ────────────────────
//
// This module provisions a Cognitive Services account of kind
// "OpenAI" and a single chat model deployment. It intentionally
// disables local (API-key) auth so the only way to invoke the
// endpoint is via AAD — the Function App's User-Assigned Managed
// Identity is granted the "Cognitive Services OpenAI User" role in
// rbac.bicep.
//
// Model choice: gpt-4o-mini on the current recommended API version.
// The deployment SKU is "GlobalStandard" so it works in every region
// that has OpenAI presence, without provisioned throughput.

@description('Name of the Azure OpenAI account.')
param name string
@description('Azure region for the account.')
param location string
param tags object = {}

@description('Model deployment name (referenced by the app as AZURE_OPENAI_DEPLOYMENT).')
param deploymentName string = 'gpt-5-mini'
@description('Underlying model name.')
param modelName string = 'gpt-5-mini'
@description('Model version.')
param modelVersion string = '2025-08-07'
@description('Deployment capacity in units of 1000 tokens per minute.')
param capacity int = 30

resource account 'Microsoft.CognitiveServices/accounts@2024-10-01' = {
  name: name
  location: location
  tags: tags
  kind: 'OpenAI'
  sku: {
    name: 'S0'
  }
  properties: {
    // Custom subdomain is required for AAD auth to work.
    customSubDomainName: name
    disableLocalAuth: true
    publicNetworkAccess: 'Enabled'
    networkAcls: {
      defaultAction: 'Allow'
    }
  }
}

resource deployment 'Microsoft.CognitiveServices/accounts/deployments@2024-10-01' = {
  parent: account
  name: deploymentName
  sku: {
    name: 'GlobalStandard'
    capacity: capacity
  }
  properties: {
    model: {
      format: 'OpenAI'
      name: modelName
      version: modelVersion
    }
  }
}

output name string = account.name
output resourceId string = account.id
output endpoint string = account.properties.endpoint
output deploymentName string = deployment.name
