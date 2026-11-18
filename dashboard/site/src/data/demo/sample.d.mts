export const enterpriseDemo: Readonly<{
  version: number; repositories: number; campaigns: number; installedWorkflows: number;
  activeRepositories: number; installedRepositories: number; runs: number;
}>;
export const enterpriseRepositoryVocabulary: readonly string[];
export const enterpriseBlockedDomains: readonly string[];
export const enterpriseWorkflowNames: readonly string[];
interface Source {
  rows: Record<string, unknown>[];
  metadata: Record<string, unknown>;
}
type ActivityRecord =
  | { kind: "run"; run: { run_id: number; repository: string; started_at: string; conclusion: string | null; status: string; safe_outputs: Record<string, unknown>[] } }
  | { kind: "safe_output_item"; safe_output: { run_id: number } }
  | { kind: "operational_value"; operational_value: Record<string, unknown> }
  | { kind: "operational_value_evidence"; operational_value_evidence: Record<string, unknown> }
  | { kind: "workflow_value_collection"; workflow_value_collection: Record<string, unknown> };
export function enterpriseRepository(index: number): string;
export function enterpriseRepositorySources(start: number, count: number, now: number): Record<string, Source>;
export function enterpriseActivityBatches(now: number, options?: { start?: number; count?: number; runs?: number; repositories?: number }): Generator<{
  records: ActivityRecord[]; runs: number;
}>;
export function enterpriseSample(now: number, options?: { start?: number; count?: number; includeHistory?: boolean; runs?: number; repositories?: number }): {
  sources: Record<string, Source>; records: ActivityRecord[]; repositories: number; runs: number;
};
export function enterpriseMemory(now: number): {
  campaign: string; path: string; content: string; sha256: string; oid: string;
}[];
