import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const template = JSON.parse(await readFile(new URL('../../server/azure/main.json', import.meta.url), 'utf8'));
const resources = Object.values(template.resources);
const adx = resources.find((resource) => resource.name === 'adx-destination');
const destination = adx.properties.template;
const children = Object.values(destination.resources);
const network = resources.find((resource) => resource.name === 'private-network-postgres');
const networkTemplate = network.properties.template;

test('ADX provisioning is opt-in and passes reviewed destination and capacity parameters', () => {
  assert.equal(template.parameters.adxEnabled.defaultValue, false);
  assert.equal(adx.condition, "[parameters('adxEnabled')]");
  for (const [child, parent] of [
    ['clusterName', 'adxClusterName'], ['databaseName', 'adxDatabaseName'],
    ['skuName', 'adxSkuName'], ['skuTier', 'adxSkuTier'], ['capacity', 'adxCapacity'],
  ]) {
    assert.equal(adx.properties.parameters[child].value, `[parameters('${parent}')]`);
  }
  assert.equal(template.parameters.adxClusterName.minLength, 4);
  assert.equal(template.parameters.adxClusterName.maxLength, 22);
  assert.equal(template.parameters.adxDatabaseName.maxLength, 100);
  assert.equal(template.parameters.adxDatabaseName.defaultValue, 'CAO');
  assert.equal(template.parameters.adxSkuName.defaultValue, 'Dev(No SLA)_Standard_D11_v2');
  assert.equal(template.parameters.adxSkuTier.defaultValue, 'Basic');
  assert.equal(template.parameters.adxCapacity.defaultValue, 1);
  assert.equal(template.parameters.adxCapacity.minValue, 1);
  const cluster = children.find((resource) => resource.type === 'Microsoft.Kusto/clusters');
  assert.deepEqual(cluster.sku, {
    name: "[parameters('skuName')]", tier: "[parameters('skuTier')]", capacity: "[parameters('capacity')]",
  });
});

test('ADX has private-only streaming ingestion with private endpoint DNS on its own subnet', () => {
  const cluster = children.find((resource) => resource.type === 'Microsoft.Kusto/clusters');
  assert.deepEqual(cluster.properties, {
    publicNetworkAccess: 'Disabled', enableDiskEncryption: true, enableStreamingIngest: true,
  });
  const endpoint = children.find((resource) => resource.type === 'Microsoft.Network/privateEndpoints');
  assert.equal(endpoint.properties.subnet.id, "[parameters('privateEndpointSubnetId')]");
  assert.deepEqual(endpoint.properties.privateLinkServiceConnections[0].properties, {
    privateLinkServiceId: "[resourceId('Microsoft.Kusto/clusters', parameters('clusterName'))]",
    groupIds: ['cluster'],
  });
  const dns = children.find((resource) => resource.type === 'Microsoft.Network/privateDnsZones');
  assert.equal(dns.name, "[format('privatelink.{0}.kusto.windows.net', parameters('location'))]");
  const link = children.find((resource) => resource.type === 'Microsoft.Network/privateDnsZones/virtualNetworkLinks');
  assert.equal(link.properties.registrationEnabled, false);
  assert.equal(link.properties.virtualNetwork.id, "[parameters('virtualNetworkId')]");
  const group = children.find((resource) => resource.type === 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups');
  assert.equal(group.properties.privateDnsZoneConfigs[0].properties.privateDnsZoneId,
    "[resourceId('Microsoft.Network/privateDnsZones', format('privatelink.{0}.kusto.windows.net', parameters('location')))]");
  assert.equal(networkTemplate.parameters.adxEnabled.defaultValue, false);
  assert.equal(network.properties.parameters.adxEnabled.value, "[parameters('adxEnabled')]");
  const vnet = Object.values(networkTemplate.resources).find((resource) => resource.type === 'Microsoft.Network/virtualNetworks');
  assert.match(vnet.properties.subnets, /if\(parameters\('adxEnabled'\), createArray\(createObject\('name', 'adx'/);
  assert.match(vnet.properties.subnets, /union\(variables\('subnetNetworkSecurityGroup'\), createObject\('addressPrefix', '10\.42\.4\.0\/24', 'privateEndpointNetworkPolicies', 'Disabled'\)\)/);
  for (const [parameter, output] of [['privateEndpointSubnetId', 'adxSubnetId'], ['virtualNetworkId', 'virtualNetworkId']]) {
    assert.match(adx.properties.parameters[parameter].value, new RegExp(`outputs\\.${output}\\.value`));
  }
});

test('ADX provisions a bounded-retention database without schema execution or runtime grants', () => {
  const database = children.find((resource) => resource.type === 'Microsoft.Kusto/clusters/databases');
  assert.equal(database.kind, 'ReadWrite');
  assert.deepEqual(database.properties, { softDeletePeriod: 'P7D', hotCachePeriod: 'P1D' });
  assert.doesNotMatch(JSON.stringify(children), /principalAssignments|roleAssignments|\/scripts|deploymentScripts|clientSecret/i);
  for (const [output, child] of [['adxClusterId', 'clusterId'], ['adxQueryEndpoint', 'queryEndpoint'], ['adxDatabaseName', 'databaseName']]) {
    assert.match(template.outputs[output].value, /^\[if\(parameters\('adxEnabled'\),/);
    assert.match(template.outputs[output].value, new RegExp(`outputs\\.${child}\\.value, ''\\)\\]$`));
  }
  assert.match(destination.outputs.queryEndpoint.value, /\.uri\]$/);
});

test('Provisioning ADX never enables export on unsupported Functions or acquisition jobs', () => {
  assert.doesNotMatch(JSON.stringify(resources), /CAO_ADX_/);
  assert.match(template.parameters.adxEnabled.metadata.description, /Azure Functions is not a supported exporter host/);
});
