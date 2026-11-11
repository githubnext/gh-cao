import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

test('hosted MCP loads the same named queries advertised by its agent catalog', () => {
  const dockerfile = readFileSync(new URL('../../server/Dockerfile', import.meta.url), 'utf8');
  const queries = JSON.parse(readFileSync(new URL('../../dashboard/site/src/agent/queries.generated.json', import.meta.url), 'utf8'));
  const catalog = JSON.parse(readFileSync(new URL('../../dashboard/site/src/agent/catalog.generated.json', import.meta.url), 'utf8'));
  const serverBuild = dockerfile.split(' AS server-build\n')[1].split('\nFROM ')[0];
  const hostedRuntime = dockerfile.split(' AS dashboard-runtime\n')[1].split('\nFROM ')[0];

  assert.match(dockerfile, /COPY .*\/dist\/src\/agent\/queries\.generated\.json \/app\/agent\/queries\.json/);
  assert.match(dockerfile, /COPY .*\/dist\/src\/agent\/catalog\.generated\.json \/app\/agent\/catalog\.json/);
  assert.match(serverBuild, /COPY --from=dashboard-build \/workspace\/dashboard\/site\/dist\/ \.\/internal\/dashboardassets\/resources\/site\/\nRUN CGO_ENABLED=0 go build/);
  assert.match(hostedRuntime, /^CMD \["serve-hosted", "--listen", "0\.0\.0\.0:8080"\]$/m);
  const queryNames = new Set(queries.map(({ name }) => name));
  for (const { id } of catalog.queries) {
    assert.ok(queryNames.has(id), `agent catalog advertises missing query ${id}`);
  }
  assert.ok(queries.some(({ name }) => name === 'overview-workflow-health-summary'));
  assert.ok(queries.some(({ name }) => name === 'campaign-readme-orchestrators'));
});
