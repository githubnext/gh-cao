import { describe, expect, it } from 'vitest';
import {
  evaluateSafeOutputDelivery,
  mergeOutputLifecycle,
  outputLifecycleFromSafeOutput,
  safeOutputAdoptionRules
} from '../../src/data/model/safe-output-lifecycle.js';

const deliveredAt = '2026-11-15T10:00:00.000Z';

describe('safe-output lifecycle model', () => {
  it('covers every registered safe-output type and fails closed for custom types', () => {
    const rules = safeOutputAdoptionRules();
    expect(rules).toHaveLength(55);
    expect(new Set(rules.map(({ type }) => type)).size).toBe(55);
    expect(rules.every(({ type }) => evaluateSafeOutputDelivery(type, deliveredAt).state !== 'unknown')).toBe(true);
    expect(evaluateSafeOutputDelivery('create_issue', deliveredAt)).toMatchObject({
      state: 'pending', signal: 'human_decision_pending'
    });
    expect(evaluateSafeOutputDelivery('add_comment', deliveredAt)).toEqual({
      state: 'accepted', signal: 'delivered', at: deliveredAt, source: 'delivery-rule'
    });
    expect(evaluateSafeOutputDelivery('report_incomplete', deliveredAt)).toMatchObject({
      state: 'not_applicable', signal: 'no_repository_value_effect'
    });
    expect(evaluateSafeOutputDelivery('custom_output', deliveredAt)).toMatchObject({
      state: 'unknown', signal: 'unsupported_safe_output'
    });
  });

  it('derives stable delivery and adoption facts from a successful safe_output.result', () => {
    const lifecycle = outputLifecycleFromSafeOutput({
      kind: 'issue',
      source: 'gh-aw-logs',
      sourceId: 'github:run:octo/api:42:usage/aw_session.jsonl:7',
      observedAt: '2026-11-15T10:01:00Z',
      data: {}
    }, {
      runId: 'github:run:octo/api:42',
      type: 'safe_output.result',
      safeOutputType: 'create_issue',
      timestamp: deliveredAt,
      source: 'safe-output',
      summary: 'Review finding',
      correlationId: 'https://github.com/octo/api/issues/7'
    }, {
      repositoryId: 'repository:octo%2Fapi',
      workflowId: 'workflow:octo%2Fapi%3A.github%2Fworkflows%2Freview.md'
    });

    expect(lifecycle).toMatchObject({
      id: 'output-lifecycle:Z2l0aHViOnJ1bjpvY3RvL2FwaTo0MjpjcmVhdGVfaXNzdWU6aHR0cHM6Ly9naXRodWIuY29tL29jdG8vYXBpL2lzc3Vlcy83',
      runId: 'github:run:octo/api:42',
      safeOutputType: 'create_issue',
      targetRepository: 'octo/api',
      targetUrl: 'https://github.com/octo/api/issues/7',
      deliveryState: 'delivered',
      deliveredAt,
      adoptionState: 'pending',
      adoptionSignal: 'human_decision_pending',
      survivalState: 'unknown'
    });
  });

  it('keeps authoritative provider adoption when a later delivery replay arrives', () => {
    const delivered = {
      id: 'output:1', repositoryId: 'repository:1', workflowId: 'workflow:1', runId: 'run:1',
      safeOutputType: 'create_issue', deliveryState: 'delivered', deliverySignal: 'safe_output.result',
      deliveredAt, deliveryObservedAt: '2026-11-15T10:01:00Z',
      deliverySource: 'safe-output', deliveryEvidenceId: 'event:1',
      adoptionState: 'pending', adoptionSignal: 'human_decision_pending',
      adoptionObservedAt: '2026-11-16T10:00:00Z', adoptionSource: 'adoption-registry',
      ruleVersion: 'gh-aw-v1.2.3-adoption-v1', survivalState: 'unknown',
      observedAt: '2026-11-16T10:00:00Z'
    };
    const authoritative = {
      ...delivered,
      adoptionState: 'accepted', adoptionSignal: 'completed',
      adoptionAt: '2026-11-15T12:00:00Z',
      adoptionObservedAt: '2026-11-15T12:05:00Z', adoptionSource: 'github-api',
      survivalState: 'observed', survivalSignal: 'resource_still_accepted',
      survivalObservedAt: '2026-11-15T12:05:00Z'
    };

    expect(mergeOutputLifecycle(authoritative, delivered)).toMatchObject({
      adoptionState: 'accepted',
      adoptionSource: 'github-api',
      survivalState: 'observed'
    });
  });
});
