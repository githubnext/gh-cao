import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('../../server/azure/adx.bicep', import.meta.url), 'utf8');

test('Standalone optional ADX retains reviewed capacity and private streaming ingestion', () => {
  assert.match(source, /param skuName string = 'Dev\(No SLA\)_Standard_E2a_v4'/);
  assert.match(source, /@minValue\(1\)\s+param capacity int = 1/);
  assert.match(source, /publicNetworkAccess: 'Disabled'/);
  assert.match(source, /enableDiskEncryption: true/);
  assert.match(source, /enableStreamingIngest: true/);
  assert.match(source, /subnet:\s*\{\s*id: privateEndpointSubnetId/);
  assert.match(source, /groupIds: \['cluster'\]/);
  assert.match(source, /registrationEnabled: false/);
  assert.match(source, /virtualNetwork:\s*\{\s*id: virtualNetworkId/);
});

test('ADX is bounded-retention destination infrastructure, not an exporter or authority grant', () => {
  assert.match(source, /softDeletePeriod: 'P7D'/);
  assert.match(source, /hotCachePeriod: 'P1D'/);
  assert.doesNotMatch(source, /principalAssignments|roleAssignments|\/scripts|deploymentScripts|clientSecret|CAO_ADX_/i);
  assert.match(source, /output queryEndpoint string = cluster\.properties\.uri/);
});
