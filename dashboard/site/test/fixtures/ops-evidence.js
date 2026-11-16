import { normalize } from '../../src/data/normalize/index.js';

/** Synthetic test evidence only; never loaded by a development server.
 * @param {string} [now]
 */
export function opsEvidence(now = '2026-11-16T12:00:00Z') {
  const batch = normalize([]);
  const repositoryId = 'repo:octodemo/ops-synthetic';
  batch.repositories = [
    { id: repositoryId, owner: 'octodemo', name: 'ops-synthetic', observedAt: now },
    { id: 'repo:octodemo/no-evidence', owner: 'octodemo', name: 'no-evidence', observedAt: now }
  ];
  for (const [index, [name, status, conclusion]] of [
    ['Blocked synthetic workflow', 'completed', 'failure'],
    ['Review synthetic workflow', 'completed', 'success'],
    ['Active synthetic workflow', 'in-progress', ''],
    ['Queued synthetic workflow', 'queued', ''],
    ['Unknown synthetic workflow', '', ''],
    ['Completed synthetic workflow', 'completed', 'success']
  ].entries()) {
    const workflowId = `workflow:${index}`;
    const path = `.github/workflows/synthetic-${index}.md`;
    batch.workflows.push({
      id: workflowId, repositoryId, path, name, role: 'worker', observedAt: now,
      repositoryLink: { href: 'https://github.com/octodemo/ops-synthetic', label: 'octodemo/ops-synthetic' }
    });
    if (!status) continue;
    batch.runs.push({
      id: `run:${index}`, repositoryId, workflowId, owner: 'octodemo', repository: 'ops-synthetic',
      workflowPath: path, githubRunId: String(100 + index), attempt: 1, title: name,
      status, conclusion, createdAt: now, startedAt: now, updatedAt: now,
      runLink: { href: `https://github.com/octodemo/ops-synthetic/actions/runs/${100 + index}` }
    });
  }
  batch.outputLifecycles = [
    {
      id: 'output:pending', repositoryId, workflowId: 'workflow:1', runId: 'run:1',
      safeOutputType: 'create_pull_request', targetRepository: 'octodemo/target',
      targetUrl: 'https://github.com/octodemo/target/pull/42', title: 'Synthetic proposed repair',
      deliveryState: 'delivered', deliverySignal: 'safe_output.result', deliveredAt: now,
      deliveryObservedAt: now, deliverySource: 'safe-output', deliveryEvidenceId: 'event:pending',
      adoptionState: 'pending', adoptionSignal: 'human_decision_pending',
      adoptionObservedAt: now, adoptionSource: 'adoption-registry',
      ruleVersion: 'gh-aw-v1.2.3-adoption-v1', survivalState: 'unknown', observedAt: now
    },
    {
      id: 'output:fallback', repositoryId, workflowId: 'workflow:5', runId: 'run:5',
      safeOutputType: 'add_comment', title: 'Synthetic delivery fallback',
      deliveryState: 'delivered', deliverySignal: 'safe_output.result', deliveredAt: now,
      deliveryObservedAt: now, deliverySource: 'safe-output', deliveryEvidenceId: 'event:fallback',
      adoptionState: 'accepted', adoptionSignal: 'delivery_fallback', adoptionAt: now,
      adoptionObservedAt: now, adoptionSource: 'adoption-registry',
      ruleVersion: 'gh-aw-v1.2.3-adoption-v1', survivalState: 'unknown', observedAt: now
    }
  ];
  return batch;
}
