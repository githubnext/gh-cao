export const enterpriseDemo: Readonly<{
  version: number; repositories: number; campaigns: number; installedWorkflows: number;
  activeRepositories: number; installedRepositories: number;
}>;
interface Source {
  rows: Record<string, unknown>[];
  metadata: Record<string, unknown>;
}
export function enterpriseRepository(index: number): string;
export function enterpriseRepositorySources(start: number, count: number, now: number): Record<string, Source>;
export function enterpriseSample(now: number, options?: { start?: number; count?: number; includeHistory?: boolean }): {
  sources: Record<string, Source>; records: Record<string, unknown>[]; repositories: number; runs: number;
};
export function enterpriseMemory(now: number): {
  campaign: string; path: string; content: string; sha256: string; oid: string;
}[];
