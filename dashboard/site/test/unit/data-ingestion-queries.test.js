import { describe, expect, it } from 'vitest';
import { queryDashboardSourceObservations } from '../../src/data/queries/ingestion.js';
import { relationshipErrors } from '../../src/data/model/schema.js';
import { normalize } from '../../src/data/normalize/index.js';

const metadata = {
  'as-of': '2026-09-09T05:00:00Z',
  'artifact-generation': 'abc123'
};

describe('dashboard source ingestion queries', () => {
  it('uses unknown for an absent run gh-aw version', () => {
    const adapted = queryDashboardSourceObservations({
      runs: {
        rows: [{
          organization: 'githubnext',
          repository: 'gh-cao',
          workflow: '.github/workflows/dashboard.md',
          run: '12345',
          'run-status': 'completed',
          'observed-at': '2026-09-09T04:30:00Z'
        }],
        metadata
      }
    });

    expect(normalize(adapted.observations).runs[0].ghAwVersion).toBe('unknown');
  });

  it('converts real source-shaped repository, workflow, and run rows', () => {
    const adapted = queryDashboardSourceObservations({
      campaigns: {
        rows: [{
          campaign: 'dashboard',
          'campaign-name': 'CAO Dashboard',
          'campaign-description': 'Deploy the CAO dashboard.',
          'campaign-icon': 'graph',
          'campaign-mode': 'review',
          'campaign-enabled': true,
          'campaign-min-version': 'v0.89.3',
          'campaign-version': 'v1.2.0',
          'campaign-current-version': 'v1.3.0',
          'campaign-update-state': 'update-available',
          'campaign-experimental': true
        }],
        metadata
      },
      repositories: {
        rows: [{
          organization: 'githubnext', repository: 'gh-cao', 'observed-at': '2026-09-09T04:00:00Z',
          'organization-link': { relation: 'organization', href: 'https://github.com/githubnext' },
          'repository-link': { relation: 'repository', href: 'https://github.com/githubnext/gh-cao' }
        }],
        metadata
      },
      workflows: {
        rows: [{
          organization: 'githubnext',
          repository: 'gh-cao',
          workflow: '.github/workflows/dashboard.md',
          campaign: 'dashboard',
          'campaign-name': 'CAO Dashboard',
          'workflow-id': '501',
          'workflow-role': 'worker',
          'workflow-name': 'Dashboard',
          'workflow-active': 'true',
          'workflow-registry-state': 'active',
          'created-at': '2026-09-01T00:00:00Z',
          'updated-at': '2026-09-08T00:00:00Z',
          'gh-aw-version': '0.88.8',
          'gh-aw-current-version': '0.88.9',
          'gh-aw-update-state': 'update-available',
          'workflow-link': { relation: 'workflow', href: 'https://github.com/githubnext/gh-cao/actions' },
          'observed-at': '2026-09-09T04:30:00Z'
        }],
        metadata
      },
      runs: {
        rows: [{
          organization: 'githubnext',
          repository: 'gh-cao',
          workflow: '.github/workflows/dashboard.md',
          run: '12345',
          'run-attempt': 2,
          event: 'workflow_dispatch',
          'run-status': 'completed',
          'run-conclusion': 'success',
          'rollout-mode': 'review',
          'agent-id': 'copilot',
          'agent-version': '1.2.3',
          'model-id': 'model-b',
          'gh-aw-version': '0.89.1',
          'aic-total': 2.5,
          'input-tokens': 100,
          'output-tokens': 20,
          'cache-read-tokens': 10,
          'cache-write-tokens': 5,
          'reasoning-tokens': 15,
          engine: 'copilot',
          'engine-version': '1.2.3',
          'requested-model': 'model-a',
          'resolved-model': 'model-b',
          'failure-message': 'Dependency update failed',
          'failure-log': '2026-09-09T04:48:00Z ##[error]dependency update failed',
          'failure-detail': 'Dependency update failed',
          'started-at': '2026-09-09T04:45:00Z',
          'ended-at': '2026-09-09T04:50:00Z'
        }],
        metadata
      },
      'job-performance': {
        rows: [{
          organization: 'githubnext',
          repository: 'gh-cao',
          workflow: '.github/workflows/dashboard.md',
          run: '12345',
          'run-attempt': 2,
          'job-id': '67890',
          job: 'build',
          'job-status': 'completed',
          'job-conclusion': 'success',
          'started-at': '2026-09-09T04:46:00Z',
          'job-duration-seconds': 240,
          runner: 'ubuntu-latest',
          engine: 'copilot',
          model: 'model-b'
        }],
        metadata
      }
    });

    const batch = normalize(adapted.observations);

    expect(batch.repositories[0]).toMatchObject({
      id: 'repository:githubnext%2Fgh-cao',
      owner: 'githubnext', name: 'gh-cao',
      repositoryLink: { relation: 'repository', href: 'https://github.com/githubnext/gh-cao' }
    });
    expect(batch.campaigns[0]).toMatchObject({
      id: 'campaign:dashboard-sources:dashboard',
      slug: 'dashboard',
      name: 'CAO Dashboard',
      minVersion: 'v0.89.3',
      version: 'v1.2.0',
      currentVersion: 'v1.3.0',
      updateState: 'update-available',
      experimental: true
    });
    expect(batch.workflows[0]).toMatchObject({
      id: 'workflow:githubnext%2Fgh-cao%3A.github%2Fworkflows%2Fdashboard.md',
      githubId: '501',
      repositoryId: 'repository:githubnext%2Fgh-cao',
      state: 'active',
      registryState: 'active',
      createdAt: '2026-09-01T00:00:00Z',
      updatedAt: '2026-09-08T00:00:00Z',
      ghAwVersion: '0.89.1',
      ghAwVersionSource: 'run',
      ghAwRunVersion: '0.89.1',
      ghAwRunVersionRunId: 'github:run:githubnext/gh-cao:12345',
      ghAwInventoryVersion: '0.88.8',
      ghAwInventoryUpdateState: 'update-available',
      ghAwUpdateState: 'unknown',
      campaignId: batch.campaigns[0].id,
      role: 'worker'
    });
    expect(batch.runs[0]).toMatchObject({
      id: 'github:run:githubnext/gh-cao:12345',
      workflowId: batch.workflows[0].id,
      repositoryId: batch.repositories[0].id,
      status: 'completed',
      conclusion: 'success',
      rolloutMode: 'review',
      agentId: 'copilot',
      agentVersion: '1.2.3',
      modelId: 'model-b',
      ghAwVersion: '0.89.1',
      aicTotal: 2.5,
      inputTokens: 100,
      outputTokens: 20,
      cacheReadTokens: 10,
      cacheWriteTokens: 5,
      reasoningTokens: 15,
      engine: 'copilot',
      engineVersion: '1.2.3',
      requestedModel: 'model-a',
      resolvedModel: 'model-b',
      failureMessage: 'Dependency update failed',
      failureLog: '2026-09-09T04:48:00Z ##[error]dependency update failed',
      failureDetail: 'Dependency update failed'
    });
    expect(batch.domains).toEqual([]);
    expect(batch.tools).toEqual([]);
    expect(batch.audits).toEqual([]);
    expect(batch.issues).toEqual([]);
  });

  it('preserves marketplace repository links during canonical ingestion', () => {
    const repositoryLink = {
      relation: 'repository',
      href: 'https://github.com/example/packages',
      label: 'Open example/packages on GitHub'
    };
    const adapted = queryDashboardSourceObservations({
      'marketplace-packages': {
        rows: [{
          id: 'example/packages/demo',
          'registry-id': 'official',
          repository: 'example/packages',
          'repository-link': repositoryLink,
          'verification-status': 'verified',
          'installation-status': 'installed',
          'adoption-count': 42,
          stars: 12,
          ref: 'main',
          'resolved-commit': 'a'.repeat(40),
          source: `example/packages/demo@${'a'.repeat(40)}`,
          'add-command': `gh cao add example/packages/demo@${'a'.repeat(40)}`,
          'observed-at': '2026-09-09T04:00:00Z'
        }],
        metadata
      }
    });

    const packageRecord = normalize(adapted.observations).marketplacePackages[0];
    expect(packageRecord.repositoryLink).toEqual(repositoryLink);
    expect(packageRecord.stars).toBe(12);
    expect(packageRecord).not.toHaveProperty('verificationStatus');
    expect(packageRecord).not.toHaveProperty('installationStatus');
    expect(packageRecord).not.toHaveProperty('adoptionCount');
  });

  it('maps legacy package inventory to canonical campaigns during rollout', () => {
    const adapted = queryDashboardSourceObservations({
      packages: {
        rows: [{
          package: 'optimization',
          'package-name': 'Optimization',
          'package-description': 'Improves workflow efficiency.',
          'package-icon': 'zap',
          'package-mode': 'review',
          'package-worker-count': 4,
          'package-targets': [{ repository: 'github/gh-aw', mode: 'review' }]
        }],
        metadata
      },
      repositories: {
        rows: [{ organization: 'githubnext', repository: 'gh-cao' }],
        metadata
      },
      workflows: {
        rows: [{
          organization: 'githubnext',
          repository: 'gh-cao',
          workflow: '.github/workflows/optimization-token-optimizer.md',
          package: 'optimization',
          'package-name': 'Optimization',
          'package-icon': 'zap',
          'workflow-name': 'Optimization / Token Optimizer',
          'workflow-role': 'worker'
        }],
        metadata
      }
    });

    const batch = normalize(adapted.observations);

    expect(batch.campaigns[0]).toMatchObject({
      slug: 'optimization',
      name: 'Optimization',
      description: 'Improves workflow efficiency.',
      icon: 'zap',
      mode: 'review',
      workerCount: 4,
      targets: [{ repository: 'github/gh-aw', mode: 'review' }]
    });
    expect(batch.workflows[0]).toMatchObject({
      campaignId: batch.campaigns[0].id
    });
    expect(relationshipErrors(batch)).toEqual([]);
  });

  it('accepts source documents without generation metadata', () => {
    expect(queryDashboardSourceObservations({ repositories: { rows: [], metadata: {} } }))
      .toEqual({ observations: [] });
  });

  it('adapts fresh source documents without coordinating artifact generations', () => {
    const sources = {
      repositories: { rows: [], metadata },
      runs: {
        rows: [],
        metadata: { ...metadata, 'artifact-generation': 'different-generation' }
      }
    };

    expect(queryDashboardSourceObservations(sources)).toEqual({ observations: [] });
  });

  it('fails closed when a published source is unavailable', () => {
    expect(() => queryDashboardSourceObservations({
      runs: {
        rows: [],
        metadata: { ...metadata, availability: 'unavailable', error: 'refresh failed' }
      }
    })).toThrow('refresh failed');
  });

  it('joins published transaction logs directly to canonical runs', () => {
    const sources = {
      repositories: {
        rows: [{ organization: 'githubnext', repository: 'gh-cao' }],
        metadata
      },
      workflows: {
        rows: [{ organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md' }],
        metadata
      },
      runs: {
        rows: [{
          organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md',
          run: '303', 'run-attempt': 1, 'started-at': '2026-09-09T04:00:00Z',
          'agent-id': ' ', 'model-id': ''
        }],
        metadata
      },
      'job-performance': {
        rows: [{
          organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md',
          run: '303', 'run-attempt': 1, 'job-id': '404', job: 'agent', 'started-at': '2026-09-09T04:00:00Z'
        }],
        metadata
      },
      audits: {
        rows: [{
          run: '303', 'run-attempt': 1, event: 'event-1',
          'event-timestamp': '2026-09-09T04:00:01Z',
          'event-source': 'token-intervention-lifecycle',
          'event-type': 'token_efficiency.intervention',
          'mcp-server': 'github',
          'mcp-tool': 'search_issues',
          'target-repo': 'octo/example',
          'target-organization': 'octo',
          'target-repository': 'example',
          'target-workflow-path': '.github/workflows/review.md',
          'optimizer-run-attempt': 1,
          'optimizer-workflow-path': '.github/workflows/optimization-token-optimizer.md',
          'optimizer-workflow-name': 'Optimization / Token Optimizer',
          'claim-run-id': '1189001',
          'claim-run-attempt': 1,
          actor: 'maintainer',
          'source-provenance': { kind: 'workflow-dispatch-claim' },
          'opportunity-id': 'token-opportunity:1',
          'intervention-id': 'token-intervention:1',
          'lifecycle-observation-id': 'token-lifecycle:1',
          'intervention-state': 'running',
          'recommendation-disposition': 'applied',
          'safe-output-id': 'github:issue:githubnext/gh-cao:11861',
          'safe-output-url': 'https://github.com/githubnext/gh-cao/issues/11861',
          'implementation-change-id': 'github:pull-request:octo/example:42',
          'implementation-pull-request-url': 'https://github.com/octo/example/pull/42',
          'implementation-run-ids': ['7001'],
          'accepted-at': '2026-09-08T04:00:00Z',
          'source-sequence': 1
        }],
        metadata
      }
    };

    const adapted = queryDashboardSourceObservations(sources);
    const batch = normalize(adapted.observations);

    expect(relationshipErrors(batch)).toEqual([]);
    expect(batch.runs).toEqual([expect.objectContaining({
      agentId: 'copilot', modelId: 'auto'
    })]);
    expect(batch.audits).toEqual([expect.objectContaining({
      runId: 'github:run:githubnext/gh-cao:303',
      sequence: 0,
      source: 'token-intervention-lifecycle',
      type: 'token_efficiency.intervention',
      mcpServer: 'github',
      mcpTool: 'search_issues',
      targetOrganization: 'octo',
      targetRepository: 'example',
      targetWorkflowPath: '.github/workflows/review.md',
      optimizerRunAttempt: 1,
      optimizerWorkflowPath: '.github/workflows/optimization-token-optimizer.md',
      optimizerWorkflowName: 'Optimization / Token Optimizer',
      claimRunId: '1189001',
      claimRunAttempt: 1,
      actor: 'maintainer',
      sourceProvenance: { kind: 'workflow-dispatch-claim' },
      opportunityId: 'token-opportunity:1',
      interventionId: 'token-intervention:1',
      lifecycleObservationId: 'token-lifecycle:1',
      interventionState: 'running',
      recommendationDisposition: 'applied',
      safeOutputId: 'github:issue:githubnext/gh-cao:11861',
      safeOutputUrl: 'https://github.com/githubnext/gh-cao/issues/11861',
      implementationChangeId: 'github:pull-request:octo/example:42',
      implementationPullRequestUrl: 'https://github.com/octo/example/pull/42',
      implementationRunIds: ['7001'],
      acceptedAt: '2026-09-08T04:00:00Z'
    })]);
  });

  it('reconciles retained transaction logs with the published run horizon', () => {
    const sources = {
      repositories: {
        rows: [{ organization: 'githubnext', repository: 'gh-cao' }],
        metadata
      },
      workflows: {
        rows: [{ organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md' }],
        metadata
      },
      runs: {
        rows: [{
          organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md',
          run: '303', 'run-attempt': 1, 'started-at': '2026-09-09T04:00:00Z'
        }],
        metadata
      },
      'job-performance': { rows: [], metadata },
      audits: {
        rows: [
          {
            run: '302', 'run-attempt': 1, event: 'stale-event',
            'event-timestamp': '2026-09-08T04:00:01Z', 'event-source': 'agent', 'event-type': 'user.message'
          },
          {
            run: '303', 'run-attempt': 1, event: 'current-event',
            'event-timestamp': '2026-09-09T04:00:01Z', 'event-source': 'agent', 'event-type': 'user.message'
          }
        ],
        metadata
      }
    };

    const adapted = queryDashboardSourceObservations(sources);
    const batch = normalize(adapted.observations);

    expect(relationshipErrors(batch)).toEqual([]);
    expect(batch.audits).toEqual([expect.objectContaining({
      id: 'current-event', runId: 'github:run:githubnext/gh-cao:303'
    })]);
  });

  it('chunks large run-linked sources and defaults missing attempts', () => {
    const count = 100_001;
    const adapted = queryDashboardSourceObservations({
      runs: {
        rows: [{
          organization: 'githubnext',
          repository: 'gh-cao',
          workflow: '.github/workflows/dashboard.md',
          run: '303',
          'started-at': '2026-09-09T04:00:00Z'
        }],
        metadata
      },
      audits: {
        rows: Array.from({ length: count }, (_, index) => ({
          run: '303',
          event: `event-${index}`,
          'event-timestamp': '2026-09-09T04:00:01Z',
          'event-source': 'agent',
          'event-type': 'user.message'
        })),
        metadata
      }
    });

    expect(adapted.observations.filter(({ kind }) => kind === 'audit')).toHaveLength(count);
    expect(adapted.observations.at(-1)?.data.runId).toBe('github:run:githubnext/gh-cao:303');
  }, 15_000);
});