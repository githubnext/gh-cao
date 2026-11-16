import { IDBFactory } from 'fake-indexeddb';
import { describe, expect, it } from 'vitest';

import { queryDatabaseSources } from '../../src/data/queries/database.js';
import { replaceCanonicalBatch } from '../../src/data/storage/indexeddb.js';

const observedAt = '2026-09-17T12:00:00.000Z';

function batch() {
  return {
    campaigns: [],
    repositories: [{
      id: 'repository:owner%2Frepo',
      owner: 'owner',
      name: 'repo',
      fullName: 'owner/repo',
      observedAt
    }],
    workflows: [{
      id: 'workflow:owner%2Frepo%3A.github%2Fworkflows%2Fworker.md',
      repositoryId: 'repository:owner%2Frepo',
      path: '.github/workflows/worker.md',
      name: 'Worker',
      campaign: 'sample',
      campaignName: 'Sample',
      role: 'worker',
      observedAt
    }],
    runs: [{
      id: 'github:run:42:attempt:1',
      repositoryId: 'repository:owner%2Frepo',
      workflowId: 'workflow:owner%2Frepo%3A.github%2Fworkflows%2Fworker.md',
      owner: 'owner',
      repository: 'repo',
      workflowPath: '.github/workflows/worker.md',
      githubRunId: '42',
      attempt: 1,
      title: 'Sample run',
      status: 'completed',
      conclusion: 'success',
      startedAt: observedAt,
      completedAt: observedAt,
      observedAt,
      runLink: 'https://github.com/owner/repo/actions/runs/42',
      aicTotal: 3.5,
      tokenUsage: {
        total_input_tokens: 100,
        total_output_tokens: 20
      }
    }],
    domains: [],
    tools: [],
    skills: [],
    friction: [],
    issues: [{
      id: 'event:safe-output',
      sessionId: 'session:42',
      runId: 'github:run:42:attempt:1',
      type: 'safe_output.result',
      source: 'safe-output',
      summary: 'Created issue',
      status: 'created',
      githubEntityType: 'issue',
      isPullRequest: false,
      safeOutputType: 'create_issue',
      correlationId: 'https://github.com/owner/repo/issues/7',
      timestamp: observedAt,
      observedAt,
      sequence: 1
    }],
    outputLifecycles: [{
      id: 'output:1',
      repositoryId: 'repository:owner%2Frepo',
      workflowId: 'workflow:owner%2Frepo%3A.github%2Fworkflows%2Fworker.md',
      runId: 'github:run:42:attempt:1',
      safeOutputType: 'create_issue',
      targetRepository: 'owner/repo',
      targetUrl: 'https://github.com/owner/repo/issues/7',
      title: 'Created issue',
      deliveryState: 'delivered',
      deliverySignal: 'safe_output.result',
      deliveredAt: observedAt,
      deliveryObservedAt: observedAt,
      deliverySource: 'safe-output',
      deliveryEvidenceId: 'event:safe-output',
      adoptionState: 'pending',
      adoptionSignal: 'human_decision_pending',
      adoptionObservedAt: observedAt,
      adoptionSource: 'adoption-registry',
      ruleVersion: 'gh-aw-v1.2.3-adoption-v1',
      survivalState: 'unknown',
      observedAt
    }],
    operationalValues: [],
    marketplacePackages: [],
    audits: [{
      id: 'event:finding',
      sessionId: 'session:42',
      runId: 'github:run:42:attempt:1',
      type: 'audit.finding',
      source: 'audit',
      code: 'prompt_injection_detected',
      summary: 'Prompt injection detected',
      status: 'high',
      timestamp: observedAt,
      observedAt,
      sequence: 2
    }, {
      id: 'event:grader',
      sessionId: 'session:42',
      runId: 'github:run:42:attempt:1',
      type: 'grader.result',
      source: 'grader',
      grader: 'operational-value',
      graderName: 'Operational value',
      status: 'passed',
      value: 0.8,
      observation: { experiment: 'sample-experiment', mature: true, evidenceAt: observedAt },
      timestamp: observedAt,
      observedAt,
      sequence: 3
    }]
  };
}

describe('database warning source queries', () => {
  it('does not synthesize undeclared sources in JavaScript', async () => {
    const indexedDB = new IDBFactory();
    await replaceCanonicalBatch(indexedDB, batch());

    const sources = await queryDatabaseSources(indexedDB, {}, [
      'usage',
      'outcomes',
      'findings',
      'security-findings',
      'detection-observations',
      'work-items',
      'graders',
      'experiments',
      'evals',
      'eval-observations',
      'admissions',
      'safe-output-performance'
    ]);

    expect(Object.keys(sources)).toEqual([
      'usage',
      'outcomes',
      'findings',
      'security-findings',
      'detection-observations',
      'work-items',
      'graders',
      'experiments',
      'evals',
      'eval-observations',
      'admissions',
      'safe-output-performance'
    ]);
    expect(sources.findings.rows).toHaveLength(1);
    expect(sources.findings.rows[0]).toEqual(expect.objectContaining({
      code: 'prompt_injection_detected',
      'finding-summary': 'Prompt injection detected'
    }));
    expect(sources.findings.metadata.availability).not.toBe('unavailable');
    expect(sources.usage.rows).toEqual([
      expect.objectContaining({
        run: '42',
        invocation: 'github:run:42:attempt:1',
        aic: 3.5,
        'estimated-usd': null
      })
    ]);
    expect(sources.usage.metadata.availability).toBe('available');
    expect(sources.outcomes.rows).toHaveLength(1);
    expect(sources.outcomes.metadata.availability).not.toBe('unavailable');
    expect(sources['detection-observations'].rows).toEqual([
      expect.objectContaining({ run: '42', 'detection-state': 'threat', 'detection-signal': 'Prompt injection detected' })
    ]);
    expect(sources['safe-output-performance'].rows).toEqual([
      expect.objectContaining({ run: '42', 'safe-output-count': 1, 'safe-output-kind': 'create-issue' })
    ]);
    expect(sources['work-items'].rows).toEqual([
      expect.objectContaining({ 'work-item-id': 'owner/repo:.github/workflows/worker.md', 'lifecycle-state': 'review' })
    ]);
    for (const name of ['security-findings', 'admissions']) {
      expect(sources[name].rows).toEqual([]);
      expect(sources[name].metadata.availability).toBe('unavailable');
      expect(sources[name].metadata.error).toContain('No canonical database query');
    }
    for (const name of ['graders', 'experiments', 'evals', 'eval-observations']) {
      expect(sources[name].rows).toEqual([]);
      expect(sources[name].metadata.availability).toBe('empty');
    }
  });
});
