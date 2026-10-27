export const localCollection = true;
export const definition = {
  schemaVersion: 3, repository: "example/service", slug: "incident-response",
  sourcePath: ".github/workflows/incident-response.md", workflowName: "Incident response",
  intent: { goal: "Reduce incident response duration.", acceptance: "Independently retained incident timestamps." },
  adoption: { commit: "a".repeat(40), adoptedAt: "2026-10-01T00:00:00Z" },
  evaluation: { mode: "attainment-only" },
  evidence: {
    key: "incident-response", repositories: ["example/service"],
    opportunity: "Repository incidents", filters: [], collection: "Synthetic retained webhook fixture",
    measurementKind: "interval", window: { durationDays: 7, cadenceDays: 7, maturationDays: 0 },
  },
  model: { architecture: "Native incident durations", recommendation: "Inspect retained incident evidence",
    presentation: { label: "Response duration", betterLabel: "Lower is better" } },
  summary: { nativeLabel: "Response duration" },
  metrics: [{ id: "response-hours", role: "primary", name: "Response duration", unit: "hours",
    direction: "decrease", version: "reviewed-v1", formula: "totalResponseHours / completedCount",
    presentation: { name: "Response duration", legendLabel: "Response hours", transform: "identity" } }],
  validationExamples: {
    targetAttained: { totalResponseHours: 2, completedCount: 2 },
    targetMissed: { totalResponseHours: 4, completedCount: 2 }, missing: null, malformed: {},
  },
};
export function scoreMetric(id, evidence) {
  return id === "response-hours" && Number.isFinite(evidence?.totalResponseHours)
    && Number.isFinite(evidence?.completedCount) && evidence.completedCount > 0
    ? evidence.totalResponseHours / evidence.completedCount : null;
}
export async function collectBatch(requests) {
  return requests.map(request => ({
    evidence: request.windowEnd.startsWith("2026-10-08")
      ? { totalResponseHours: 0, completedCount: 2, eligibleCount: 2, coverageStatus: "complete", maturityStatus: "matured" }
      : { coverageStatus: "partial", missingReason: "Incomplete retained deliveries" },
    provenance: [{ repository: "example/service", kind: "synthetic-webhook-fixture", ref: "receipt-generation-1" }],
  }));
}
