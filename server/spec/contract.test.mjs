import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import test from 'node:test'

const directory = dirname(fileURLToPath(import.meta.url))
const implementation = readFileSync(join(directory, '../internal/server/server.go'), 'utf8')
const openapi = JSON.parse(readFileSync(join(directory, 'generated/openapi.json'), 'utf8'))

test('every registered HTTP route has a TypeSpec operation (except static assets)', () => {
  const registered = [
    ...[...implementation.matchAll(/register\("(GET|POST) (\/[^"]+)"/g)]
      .map(([, method, path]) => `${method.toLowerCase()} ${path}`),
    ...[...implementation.matchAll(/mux\.Handle\("(GET|POST) (\/[^"]+)"/g)]
      .map(([, method, path]) => `${method.toLowerCase()} ${path}`)
  ].sort()
  const documented = Object.entries(openapi.paths)
    .flatMap(([path, operations]) => Object.keys(operations)
      .filter(method => ['get', 'post'].includes(method))
      .map(method => `${method} ${path}`))
    .sort()
  assert.deepEqual(documented, registered)
})

test('agent discovery describes public GET and body-free HEAD responses', () => {
  const operations = openapi.paths['/llms.txt']
  assert.ok(operations)
  for (const method of ['get', 'head']) {
    const operation = operations[method]
    assert.ok(operation, `${method} /llms.txt is documented`)
    assert.equal(operation.security, undefined, `${method} /llms.txt is public`)
    assert.deepEqual(operation.responses['200'].headers['Cache-Control'].schema.enum, ['no-store'])
    for (const status of ['429', '503']) {
      assert.ok(operation.responses[status], `${method} /llms.txt describes ${status}`)
    }
  }
  assert.equal(operations.get.responses['200'].content['text/plain; charset=utf-8'].schema.$ref, '#/components/schemas/AgentGuideText')
  assert.equal(openapi.components.schemas.AgentGuideText.type, 'string')
  assert.deepEqual(openapi.components.schemas.AgentGuideText.examples, [
    '# Central Agentic Ops dashboard server\n\nServer commit SHA: unknown\n'
  ])
  assert.deepEqual(operations.head.responses['200'].headers['content-type'].schema.enum, ['text/plain; charset=utf-8'])
  for (const status of ['429', '503']) {
    assert.ok(operations.get.responses[status].content['application/json'])
  }
  for (const response of Object.values(operations.head.responses)) {
    assert.equal(response.content, undefined, 'HEAD responses must not declare a body')
  }
})

test('the generated contract describes the implemented security and wire formats', () => {
  assert.equal(openapi.openapi, '3.1.0')
  assert.equal(openapi.info.version, '2.13.0')
  for (const path of ['/api/runs/{id}/jobs', '/api/runs/{id}/sessions', '/api/sessions/{id}/events']) {
    assert.equal(openapi.paths[path], undefined)
  }
  const query = openapi.paths['/api/v1/query'].post
  assert.ok(query.security.some(entry => 'BearerAuth' in entry))
  assert.ok(query.security.some(entry => 'ApiKeyAuth' in entry))
  assert.ok(query.responses['422'].content['application/json'])
  assert.ok(query.requestBody.content['application/json'])
  assert.ok(query.parameters.some(parameter => parameter.name === 'X-CSRF-Token'))
  assert.ok(openapi.paths['/api/auth/session'].get.security.some(entry => 'ApiKeyAuth' in entry))
  assert.equal(openapi.paths['/api/health'].get.security, undefined)
  assert.equal(openapi.paths['/api/github/webhook'].post.security, undefined)
  assert.ok(openapi.paths['/api/v1/events'].get.responses['200'].content['text/event-stream'])
  assert.ok(openapi.paths['/api/github/webhook'].post.responses['202'])
  const mcp = openapi.paths['/mcp'].post
  assert.ok(mcp.security.some(entry => 'BearerAuth' in entry))
  assert.ok(mcp.security.some(entry => 'ApiKeyAuth' in entry))
  assert.ok(mcp.parameters.some(parameter => parameter.name === 'X-CSRF-Token'))
  assert.ok(mcp.parameters.some(parameter => parameter.name === 'X-GitHub-Actor'))
  assert.ok(mcp.parameters.some(parameter => parameter.name === 'X-GitHub-OIDC-Token'))
  assert.ok(mcp.requestBody.content['application/json'])
  assert.ok(mcp.responses['200'].content['application/json'])
  assert.ok(mcp.responses['202'])
  assert.ok(mcp.responses['404'])
  assert.ok(openapi.paths['/api/admin/rebuild'].post.responses['202'])
  const logs = openapi.paths['/api/admin/logs'].get
  assert.ok(logs.security.some(entry => 'BearerAuth' in entry))
  assert.ok(logs.security.some(entry => 'ApiKeyAuth' in entry))
  assert.equal(logs.responses['200'].content['application/json'].schema.$ref, '#/components/schemas/ServerLogSnapshot')
  assert.ok(logs.parameters.some(parameter => parameter.name === 'X-GitHub-Actor'))
  assert.ok(openapi.paths['/auth/logout'].post.responses['204'])
  assert.equal(openapi.components.securitySchemes.ApiKeyAuth.name, 'cao_session')
  for (const field of ['pushedDown', 'fallbackOperations']) {
    assert.ok(openapi.components.schemas.QueryMetrics.properties[field].anyOf.some(branch => branch.type === 'null'))
  }
  for (const field of ['redisCommands', 'redisRows']) {
    assert.equal(openapi.components.schemas.QueryMetrics.properties[field], undefined)
  }
  assert.equal(openapi.components.schemas.HealthResponse.properties.generation, undefined)
  for (const name of ['HealthResponse', 'ReadinessResponse']) {
    assert.equal(openapi.components.schemas[name].properties.operational.$ref, '#/components/schemas/OperationalState')
  }
  assert.deepEqual(openapi.components.schemas.OperationalCapability.properties.scope.enum, ['unsupported', 'process', 'deployment'])
  assert.deepEqual(openapi.components.schemas.OperationalCapability.properties.persistence.enum, ['volatile', 'restart'])
  assert.equal(openapi.components.schemas.RebuildStatus.properties.generation, undefined)
  for (const field of ['rebuildId', 'auditState']) {
    assert.equal(openapi.components.schemas.RebuildStatus.properties[field].type, 'string')
    assert.ok(!openapi.components.schemas.RebuildStatus.required.includes(field))
  }
  assert.match(openapi.paths['/api/admin/rebuild'].post.description, /require durable audit admission/)
  assert.deepEqual(
    {
      type: openapi.components.schemas.DataState.properties.evaluatedAt.type,
      format: openapi.components.schemas.DataState.properties.evaluatedAt.format
    },
    { type: 'string', format: 'date-time' }
  )
})

test('token broker exposes only the OIDC exchange and exact credential response', () => {
  const operation = openapi.paths['/api/token-broker'].post
  assert.equal(operation.security, undefined)
  assert.ok(operation.requestBody.content['application/json'])
  for (const status of ['200', '204', '400', '401', '403', '413', '415', '422', '429', '501', '503']) {
    assert.deepEqual(operation.responses[status].headers['cache-control'].schema.enum, ['no-store'])
    if (status !== '200') assert.equal(operation.responses[status].content, undefined)
  }
  assert.deepEqual(Object.keys(openapi.components.schemas.BrokerCredential.properties).sort(), ['expires_at', 'token'])
  assert.equal(openapi.components.schemas.CopilotRequestExchange.properties.token_type.enum[0], 'copilot-request')
  assert.equal(openapi.components.schemas.CopilotRequestExchange.properties.provider.enum[0], 'github')
  assert.equal(openapi.components.schemas.TokenBrokerInvalidation.properties.operation.enum[0], 'invalidate')
})

test('private archive queries preserve explicit full-content authority and the shared event contract', () => {
  const status = openapi.paths['/api/v1/session-archive/status'].get
  const query = openapi.paths['/api/v1/session-archive/query'].post
  for (const operation of [status, query]) {
    assert.ok(operation.security.some(entry => 'BearerAuth' in entry))
    assert.ok(operation.security.some(entry => 'ApiKeyAuth' in entry))
    for (const code of ['401', '403', '429', '503']) assert.ok(operation.responses[code])
    assert.match(operation.description, /full-trace-read/)
  }
  assert.ok(query.parameters.some(parameter => parameter.name === 'X-CSRF-Token'))
  for (const code of ['400', '404', '422']) assert.ok(query.responses[code])
  assert.match(query.description, /generation-bound live Azure conformance/)
  const input = openapi.components.schemas.SessionArchiveQuery
  assert.deepEqual(input.properties.kind.enum, ['timeline', 'summary', 'session-query'])
  for (const name of ['sql', 'kql', 'path', 'files', 'objects', 'namespace']) {
    assert.equal(input.properties[name], undefined)
  }
  assert.equal(input.properties.limit.maximum, 10000)
  assert.equal(input.properties.limit.minimum, 1)
  assert.deepEqual(input.properties['language-version'].enum, ['0.1.0'])
  assert.equal(input.properties.query.$ref, '#/components/schemas/SessionArchiveDefinition')
  const definition = openapi.components.schemas.SessionArchiveDefinition
  assert.deepEqual(definition.required, ['name', 'subject', 'from'])
  assert.deepEqual(definition.properties.from.enum, ['archive-events'])
  assert.equal(definition.properties.limit.maximum, 10000)
  for (const key of ['joins', 'union', 'compute', 'window', 'sql', 'stores', 'parameters']) {
    assert.equal(definition.properties[key], undefined)
  }
  assert.deepEqual(Object.keys(definition.properties).sort(), [
    'acceptance', 'aggregate', 'filter', 'from', 'limit', 'name', 'objective', 'order-by', 'select', 'subject'
  ])
  assert.match(definition.description, /UTF-8 lexical string order/)
  assert.match(query.description, /local-only/)
  assert.match(query.description, /session-query/)
  assert.match(query.description, /final response write/)
  assert.ok(openapi.components.schemas.SessionArchiveStatus.properties.queryKinds)
  const result = openapi.components.schemas.SessionArchiveResult
  assert.equal(result.properties.events.items.$ref, '#/components/schemas/Trace.Archive.Event')
  assert.equal(openapi.components.schemas['Trace.Archive.Event'].properties.PayloadJSON.type, 'string')
  for (const field of ['rows', 'columns', 'metadata']) assert.ok(result.properties[field])
  const metadata = openapi.components.schemas.SessionArchiveQueryMetadata
  assert.deepEqual(metadata.properties.upstreamCoverage.enum, ['unknown'])
  assert.deepEqual(metadata.required, ['limited', 'upstreamCoverage', 'admissionGaps', 'tracesWithGaps'])
})

test('webhook status is an authenticated process-local no-store snapshot', () => {
  const operation = openapi.paths['/api/v1/ingestion/webhook/status'].get
  assert.ok(operation.security.some(entry => 'BearerAuth' in entry))
  assert.ok(operation.security.some(entry => 'ApiKeyAuth' in entry))
  for (const status of ['401', '403', '503']) assert.ok(operation.responses[status])
  const response = operation.responses['200']
  assert.deepEqual(response.headers['cache-control'].schema.enum, ['no-store'])
  assert.equal(response.content['application/json'].schema.$ref, '#/components/schemas/WebhookStatus')
  const model = openapi.components.schemas.WebhookStatus
  assert.deepEqual(model.properties.scope.enum, ['process'])
  for (const name of ['received', 'inFlight', 'verified', 'accepted', 'rejected', 'failed', 'duplicates', 'pings']) {
    assert.equal(model.properties[name].type, 'integer')
    assert.ok(model.required.includes(name))
  }
  assert.match(operation.description, /without querying GitHub/)
  assert.match(model.description, /Reset on restart/)
})

test('on-demand value publication preserves the original bounded envelope and administrator boundary', () => {
  const operation = openapi.paths['/api/v1/value'].post
  assert.ok(operation.security.some(entry => 'BearerAuth' in entry))
  assert.ok(operation.security.some(entry => 'ApiKeyAuth' in entry))
  assert.ok(operation.parameters.some(parameter => parameter.name === 'X-CSRF-Token'))
  assert.equal(operation.requestBody.content['application/json'].schema.$ref, '#/components/schemas/ValuePublicationRequest')
  assert.equal(operation.requestBody.content['application/x-ndjson'].schema.$ref, '#/components/schemas/ValuePublicationRequest')
  assert.equal(operation.responses['200'].content['application/json'].schema.$ref, '#/components/schemas/ValuePublication')
  for (const status of ['400', '401', '403', '404', '409', '413', '415', '503']) {
    assert.ok(operation.responses[status], `publication describes ${status}`)
  }
  assert.match(operation.description, /step-up assurance/)
  assert.match(operation.description, /survives Activity snapshot replacement/)
  assert.match(operation.description, /No workflow discovery/)
  assert.deepEqual(openapi.components.schemas.ValuePublicationRequest.properties.schema_version.enum, [2])
  assert.deepEqual(openapi.components.schemas.ValuePublicationRequest.properties.kind.enum, ['operational_value_evidence'])
})

test('activation admission has its own OIDC-only, empty-denial contract', () => {
  const operation = openapi.paths['/api/admission'].post
  assert.deepEqual(operation.security, [{ BearerAuth: [] }])
  assert.equal(operation.parameters.some(parameter => parameter.name === 'X-CSRF-Token'), false)
  assert.ok(operation.requestBody.content['application/json'])
  assert.equal(operation.responses['200'].content['application/json'].schema.$ref, '#/components/schemas/AdmissionAllowances')
  for (const status of ['200', '403', '429', '503']) {
    assert.deepEqual(operation.responses[status].headers['cache-control'].schema.enum, ['no-store'])
    if (status !== '200') assert.equal(operation.responses[status].content, undefined)
  }
  const allowances = openapi.components.schemas.AdmissionAllowances
  assert.deepEqual(allowances.required, ['max_ai_credits'])
  assert.deepEqual(Object.keys(allowances.properties).sort(), [
    'max_ai_credits', 'max_ai_credits_evals', 'max_ai_credits_threat_detection'
  ])
  for (const property of Object.values(allowances.properties)) {
    assert.equal(property.minimum, 1)
    assert.equal(property.maximum, 2147483647)
  }
  const request = openapi.components.schemas.AdmissionRequest
  assert.equal(request.required.length, 10)
  assert.ok(Object.values(request.properties).every(property => property.type === 'string'))
  for (const name of ['AdmissionRequest', 'AdmissionAllowances']) {
    const schema = openapi.components.schemas[name]
    assert.deepEqual(schema.unevaluatedProperties, { not: {} })
    const emitted = JSON.parse(readFileSync(join(directory, `generated/schemas/${name}.json`), 'utf8'))
    const portableProperties = Object.fromEntries(Object.entries(schema.properties).map(([key, property]) => {
      const { format, ...portable } = property
      return [key, format === 'int32' ? portable : property]
    }))
    assert.deepEqual(emitted.properties, portableProperties)
    assert.deepEqual(emitted.unevaluatedProperties, { not: {} })
    assert.deepEqual(emitted.examples, schema.examples)
  }
  for (const name of ['run_id', 'run_attempt']) {
    assert.equal(request.properties[name].pattern, '^[1-9][0-9]*$')
    assert.equal(request.properties[name].maxLength, 128)
  }
  assert.equal(request.properties.workflow_hash.pattern, '^[0-9a-f]{64}:[0-9a-f]{64}$')
  assert.equal(operation.parameters.find(parameter => parameter.name === 'traceparent').required, false)
})

test('GitHub resource operations describe authenticated conditional reads and graceful unavailability', () => {
  for (const [path, model] of [
    ['/api/v1/github/repos/{owner}/{repo}', 'GitHubRepository'],
    ['/api/v1/github/repos/{owner}/{repo}/releases/latest', 'GitHubRelease']
  ]) {
    const operation = openapi.paths[path].get
    assert.ok(operation.security.some(entry => 'BearerAuth' in entry))
    assert.ok(operation.security.some(entry => 'ApiKeyAuth' in entry))
    assert.ok(operation.security.some(entry => 'LocalGitHubResourceSession' in entry))
    assert.ok(operation.parameters.some(parameter => parameter.name === 'If-None-Match'))
    assert.equal(operation.responses['200'].content['application/json'].schema.$ref, `#/components/schemas/${model}`)
    for (const status of ['200', '304']) {
      assert.ok(operation.responses[status].headers.ETag)
      assert.ok(operation.responses[status].headers['Cache-Control'])
    }
    assert.equal(operation.responses['304'].content, undefined)
    for (const status of ['400', '401', '403', '404', '429', '503']) {
      assert.ok(operation.responses[status])
    }
    for (const status of ['429', '503']) {
      assert.ok(operation.responses[status].headers['Retry-After'])
    }
  }
  assert.equal(openapi.components.securitySchemes.LocalGitHubResourceSession.name, 'cao_github_local')
})

test('conclusion admission records only bounded compact sessions with OIDC-only empty responses', () => {
  const operation = openapi.paths['/api/conclusion'].post
  assert.deepEqual(operation.security, [{ BearerAuth: [] }])
  assert.equal(operation.parameters.some(parameter => parameter.name === 'X-CSRF-Token'), false)
  assert.equal(operation.requestBody.content['application/json'].schema.$ref, '#/components/schemas/ConclusionRequest')
  assert.deepEqual(Object.keys(operation.responses), ['204', '403', '429', '503'])
  for (const response of Object.values(operation.responses)) {
    assert.deepEqual(response.headers['cache-control'].schema.enum, ['no-store'])
    assert.equal(response.content, undefined)
  }
  const request = openapi.components.schemas.ConclusionRequest
  assert.equal(request.required.length, 13)
  assert.equal(request.properties.session.maxLength, 16384)
  assert.equal(request.properties.usage_artifact_ids.maxItems, 2)
  assert.equal(request.properties.usage_artifact_ids.items.$ref, '#/components/schemas/UsageArtifactID')
  assert.equal(openapi.components.schemas.UsageArtifactID.pattern, '^[1-9][0-9]{0,19}$')
  for (const [name, value] of Object.entries(openapi.components.schemas.AdmissionRequest.properties)) {
    assert.deepEqual(request.properties[name], value)
  }
  const compaction = openapi.components.schemas.SessionCompaction
  assert.equal(compaction.required.length, 6)
  assert.deepEqual(compaction.properties.version.enum, [1])
  assert.equal(compaction.properties.included_events.maximum, 128)
  for (const name of ['omitted_events', 'oversized_lines']) {
    assert.equal(compaction.properties[name].minimum, 0)
    assert.equal(compaction.properties[name].maximum, 9007199254740991)
  }
  for (const schema of [request, compaction]) {
    assert.deepEqual(schema.unevaluatedProperties, { not: {} })
  }
})

test('hosted repository consent is explicit and server-authorized', () => {
  const login = openapi.paths['/auth/login'].get
  const consent = login.parameters.find(parameter => parameter.name === 'hosted_cli')
  assert.equal(consent.in, 'query')
  assert.deepEqual(consent.schema.enum, ['1'])
  assert.equal(consent.required, false)
  for (const status of ['302', '400', '401', '403']) {
    assert.ok(login.responses[status])
  }
  assert.match(login.description, /existing server-side session/)
  assert.match(login.description, /configured administrator/)
  assert.match(openapi.paths['/auth/callback'].get.description, /matching GitHub account ID and login/)
})

test('local OAuth is an explicit single-user exception rather than a hosted policy bypass', () => {
  const login = openapi.paths['/auth/login'].get
  assert.match(login.description, /CAO_GITHUB_LOCAL_USER/)
  assert.match(login.description, /read:user without read:org/)
  assert.match(login.description, /loopback HTTPS/)
  assert.equal(login.parameters.some(parameter => parameter.name === 'local_user'), false)
  const callback = openapi.paths['/auth/callback'].get
  assert.match(callback.description, /exact configured developer/)
  assert.match(callback.description, /cannot be reused in ordinary hosted mode/)
})

test('structured payload fields stay in sync with Go JSON tags', () => {
  const pairs = [
    ['../internal/admission/protocol.go', 'Request', 'AdmissionRequest'],
    ['../internal/admission/protocol.go', 'Allowances', 'AdmissionAllowances'],
    ['../internal/admission/conclusion_protocol.go', 'SessionCompaction', 'SessionCompaction'],
    ['../internal/server/server.go', 'queryRequest', 'QueryRequest'],
    ['../internal/server/server.go', 'queryResponse', 'QueryResponse'],
    ['../internal/query/types.go', 'Definition', 'QueryDefinition'],
    ['../internal/query/types.go', 'Facet', 'Facet'],
    ['../internal/query/types.go', 'WindowField', 'WindowField'],
    ['../internal/query/types.go', 'OrderField', 'OrderField'],
    ['../internal/model/model.go', 'Source', 'QuerySource'],
    ['../internal/model/model.go', 'Metrics', 'QueryMetrics'],
    ['../internal/server/operations.go', 'rebuildStatus', 'RebuildStatus'],
    ['../internal/server/webhook_status.go', 'webhookStatus', 'WebhookStatus'],
    ['../internal/server/value.go', 'valuePublicationRequest', 'ValuePublicationRequest'],
    ['../internal/server/logs.go', 'serverLogSnapshot', 'ServerLogSnapshot'],
    ['../internal/server/logs.go', 'serverLogRedis', 'ServerLogRedis'],
    ['../internal/server/github_resources.go', 'githubRepositoryResource', 'GitHubRepository'],
    ['../internal/server/github_resources.go', 'githubReleaseResource', 'GitHubRelease'],
    ['../internal/collect/recovery.go', 'Status', 'CollectionStatus'],
    ['../internal/githubquota/usage.go', 'UsageReport', 'QuotaUsage'],
    ['../internal/repositorymemory/memory.go', 'Campaign', 'MemoryCampaign']
  ]
  for (const [file, name, schema] of pairs) {
    const source = readFileSync(join(directory, file), 'utf8')
    const fields = source.match(new RegExp(`type ${name} struct \\{([\\s\\S]*?)\\n\\}`))?.[1]
    assert.ok(fields, `missing Go struct ${name}`)
    const goFields = [...fields.matchAll(/`json:"([^",]+)(?:,omitempty)?"`/g)]
      .map(([, field]) => field).sort()
    const specFields = Object.keys(openapi.components.schemas[schema].properties).sort()
    assert.deepEqual(specFields, goFields, `${schema} differs from ${file}:${name}`)
  }
  const conclusionSource = readFileSync(join(directory, '../internal/admission/conclusion_protocol.go'), 'utf8')
  const ownFields = [...conclusionSource.match(/type ConclusionRequest struct \{([\s\S]*?)\n\}/)[1].matchAll(/`json:"([^"]+)"`/g)].map(([, name]) => name)
  assert.deepEqual(Object.keys(openapi.components.schemas.ConclusionRequest.properties).sort(),
    [...Object.keys(openapi.components.schemas.AdmissionRequest.properties), ...ownFields].sort())
})

test('query contracts expose bounded window operations with canonical wire names', () => {
  const schemas = openapi.components.schemas
  const window = schemas.QueryDefinition.properties.window
  assert.equal(window.type, 'array')
  assert.equal(window.minItems, 1)
  assert.equal(window.maxItems, 8)
  assert.equal(window.items.$ref, '#/components/schemas/WindowField')
  assert.deepEqual(schemas.WindowField.required, ['operation', 'field', 'as', 'order-by'])
  assert.equal(schemas.WindowField.properties['order-by'].items.$ref, '#/components/schemas/OrderField')
  assert.equal(schemas.WindowField.properties['time-field'].type, 'string')
  assert.equal(schemas.WindowField.properties.groupby.items.type, 'string')
  assert.equal(schemas.WindowField.properties.frame.type, 'integer')
  const definition = JSON.parse(readFileSync(join(directory, 'generated/schemas/QueryDefinition.json'), 'utf8'))
  assert.deepEqual(definition.properties.window, {
    type: 'array', items: { $ref: '#/$defs/WindowField' }, minItems: 1, maxItems: 8
  })
  assert.deepEqual(Object.keys(definition.$defs.WindowField.properties).sort(),
    Object.keys(schemas.WindowField.properties).sort())
  const request = JSON.parse(readFileSync(join(directory, 'generated/schemas/QueryRequest.json'), 'utf8'))
  for (const field of ['queries', 'compiledQueries']) {
    assert.equal(request.properties[field].items.$ref, 'QueryDefinition.json')
  }
})

test('server log records preserve reserved wire names and date-time examples', () => {
  const record = openapi.components.schemas.ServerLogRecord
  assert.deepEqual(record.required, ['timestamp', 'namespace', 'message'])
  assert.deepEqual(record.properties, {
    timestamp: { type: 'string', format: 'date-time' },
    namespace: { type: 'string' },
    message: { type: 'string' }
  })
  assert.deepEqual(record.examples, [{
    timestamp: '2026-10-02T19:00:00Z',
    namespace: 'cao:server',
    message: 'listener ready'
  }])
  assert.equal(
    openapi.components.schemas.ServerLogSnapshot.properties.logs.items.$ref,
    '#/components/schemas/ServerLogRecord'
  )
})

test('the server-only MCP logs tool matches the HTTP snapshot contract', () => {
  const contract = JSON.parse(readFileSync(join(directory, '../../dashboard/site/src/agent/mcp-contract.json'), 'utf8'))
  assert.deepEqual(contract.tools.map(tool => tool.name), ['cao_catalog', 'cao_query'])
  assert.deepEqual(contract.serverTools.map(tool => tool.name), ['cao_logs'])
  const logs = contract.serverTools[0]
  assert.deepEqual(logs.inputSchema, { type: 'object', properties: {}, additionalProperties: false })
  assert.equal(logs.annotations.readOnlyHint, true)
  assert.equal(logs.annotations.untrustedContentHint, true)
  const schemas = openapi.components.schemas
  for (const [actual, expected] of [
    [logs.outputSchema, schemas.ServerLogSnapshot],
    [logs.outputSchema.properties.logs.items, schemas.ServerLogRecord],
    [logs.outputSchema.properties.redis, schemas.ServerLogRedis]
  ]) {
    assert.deepEqual(actual.required, expected.required)
    assert.deepEqual(Object.keys(actual.properties).sort(), Object.keys(expected.properties).sort())
  }
  assert.deepEqual(logs.outputSchema.properties.logs.items.properties, schemas.ServerLogRecord.properties)
  assert.deepEqual(
    logs.outputSchema.properties.redis.properties.status.enum.slice().sort(),
    schemas.ServerLogRedis.properties.status.enum.slice().sort()
  )
})

test('selected JSON Schemas and nullable responses are emitted', () => {
  for (const schema of ['AdmissionRequest', 'AdmissionAllowances', 'ConclusionRequest', 'SessionCompaction', 'QueryRequest', 'QueryResponse', 'QueryDefinition', 'RevisionEvent', 'SessionArchiveQuery', 'SessionArchiveResult', 'SessionArchiveStatus', 'WebhookAcknowledgement']) {
    const document = JSON.parse(readFileSync(join(directory, 'generated/schemas', `${schema}.json`), 'utf8'))
    assert.equal(document.$schema, 'https://json-schema.org/draft/2020-12/schema')
    assert.ok(document.properties)
  }
  assert.deepEqual(
    readdirSync(join(directory, 'generated/schemas')).sort(),
    ['AdmissionAllowances.json', 'AdmissionRequest.json', 'ConclusionRequest.json', 'QueryDefinition.json', 'QueryRequest.json', 'QueryResponse.json', 'RevisionEvent.json', 'SessionArchiveDefinition.json', 'SessionArchiveQuery.json', 'SessionArchiveResult.json', 'SessionArchiveStatus.json', 'SessionCompaction.json', 'WebhookAcknowledgement.json']
  )
  const campaign = openapi.paths['/api/v1/memory/{campaign}'].get.responses['200'].content['application/json'].schema
  assert.ok(campaign.anyOf.some(branch => branch.type === 'null'))
  for (const [path, response] of [
    ['/api/health', 'HealthResponse'],
    ['/api/v1/health', 'HealthResponse'],
    ['/api/readiness', 'ReadinessResponse']
  ]) {
    const schema = openapi.paths[path].get.responses['503'].content['application/json'].schema
    assert.ok(schema.anyOf.some(branch => branch.$ref === `#/components/schemas/${response}`), path)
    assert.ok(schema.anyOf.some(branch => branch.required?.includes('error')), path)
    assert.ok(openapi.paths[path].get.responses['429'].content['application/json'], path)
  }
})
