import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { operationalValueId } from "../../../../dashboard/site/src/data/model/ids.js";
import { canonicalTimestamp, operationalValuePeriod } from "../../../../dashboard/site/src/data/model/schema.js";
import { adaptCachedGhAwJsonl } from "../../../../dashboard/site/src/data/adapters/gh-aw-logs.js";

const digest = value => createHash("sha256").update(JSON.stringify(value)).digest("hex");

/** Publish original scored observations, never pulse's comparisons or inferred event deliveries. */
export function timelineMeasurementEnvelopes(timeline, { campaign } = {}) {
  const definition = timeline?.valueFunction?.definition;
  assert.equal(timeline?.schemaVersion, 3, "A verified schema-v3 timeline is required");
  assert.equal(definition?.schemaVersion, 3, "A frozen schema-v3 definition is required");
  assert.ok(typeof timeline.repository === "string"
    && timeline.repository.toLowerCase() === definition.repository?.toLowerCase()
    && timeline.workflowSlug === definition.slug && timeline.sourcePath === definition.sourcePath,
  "Timeline and frozen definition identities differ");
  assert.match(timeline.valueFunction.sha256, /^[a-f0-9]{64}$/, "Evaluator fingerprint required");
  assert.equal(definition.sourcePath, `.github/workflows/${definition.slug}.md`, "Frozen workflow path must match its filename stem");
  assert.ok(["snapshot", "interval"].includes(definition.evidence?.measurementKind),
    "Native publication requires an explicit measurement kind");
  assert.ok(Array.isArray(definition.metrics) && definition.metrics.length > 0
    && definition.metrics.filter(metric => metric.role === "primary").length === 1,
  "Exactly one frozen primary measure is required");
  assert.ok(Array.isArray(timeline.snapshots) && timeline.snapshots.length > 0
    && timeline.snapshots.length * definition.metrics.length <= 20000, "Native measurement count exceeds budget");
  assert.ok(campaign === undefined || /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(campaign), "Invalid campaign identity");
  const observations = new Map();
  for (const snapshot of timeline.snapshots) {
    assert.ok(snapshot.evidence && Array.isArray(snapshot.provenance) && snapshot.provenance.length > 0,
      "Original evidence and provenance are required");
    for (const source of snapshot.provenance) {
      assert.ok(typeof source.repository === "string"
        && definition.evidence.repositories.some(repository => repository.toLowerCase() === source.repository.toLowerCase())
        && typeof source.kind === "string" && source.kind
        && typeof source.ref === "string" && source.ref, "Evidence provenance is outside the frozen repository scope");
    }
    for (const metric of definition.metrics) {
      assert.ok(Object.hasOwn(snapshot.metrics ?? {}, metric.id), "A frozen metric assessment is missing");
      const value = snapshot.metrics[metric.id];
      assert.ok(value === null || typeof value === "number" && Number.isFinite(value), "Invalid native measurement");
      const evidence = snapshot.evidence;
      const period = operationalValuePeriod({
        windowStart: snapshot.window?.startAt, windowEnd: snapshot.window?.endAt,
        measurementKind: definition.evidence.measurementKind,
        coverageStatus: evidence.coverageStatus
          ?? (definition.evidence.measurementKind === "snapshot" && evidence.valid === true ? "complete" : "unknown"),
        evidenceDigest: digest({ evidence, provenance: snapshot.provenance }),
        ...(evidence.missingReason || value === null ? {
          missingReason: evidence.missingReason ?? "outcome-evidence-unavailable",
        } : {}),
        ...(evidence.eligibleCount !== undefined ? { eligibleCount: evidence.eligibleCount } : {}),
        ...(evidence.populationDigest !== undefined ? { populationDigest: evidence.populationDigest } : {}),
      }, snapshot.observedAt, "native timeline observation");
      const record = {
        timestamp: canonicalTimestamp(snapshot.observedAt, "native observation timestamp"), repository: timeline.repository,
        ...(campaign ? { campaign, campaign_id: `campaign:${campaign}` } : {}),
        value_id: `${definition.slug}.${metric.id}`, value,
        metric_role: metric.role, metric_name: metric.name, metric_unit: metric.unit,
        metric_direction: metric.direction,
        maturity_status: value === null ? "unavailable"
          : evidence.dubious === true ? "interim" : evidence.maturityStatus ?? "matured",
        adoption_at: definition.adoption.adoptedAt,
        evaluation_mode: definition.evaluation.mode,
        workflow_slug: definition.slug, workflow_name: definition.workflowName, workflow_path: definition.sourcePath,
        evaluator_digest: timeline.valueFunction.sha256,
        cadence_days: definition.evidence.window.cadenceDays,
        window_start: period.windowStart, window_end: period.windowEnd,
        measurement_kind: period.measurementKind, coverage_status: period.coverageStatus,
        evidence_digest: period.evidenceDigest,
        ...(period.missingReason !== undefined ? { missing_reason: period.missingReason } : {}),
        ...(period.eligibleCount !== undefined ? { eligible_count: period.eligibleCount } : {}),
        ...(period.populationDigest !== undefined ? { population_digest: period.populationDigest } : {}),
      };
      if (metric.rollup && value !== null) {
        record.rollup_numerator = evidence[metric.rollup.numeratorField];
        record.rollup_denominator = evidence[metric.rollup.denominatorField];
        assert.ok(Number.isFinite(record.rollup_numerator) && record.rollup_numerator >= 0
          && Number.isFinite(record.rollup_denominator) && record.rollup_denominator > 0, "Invalid native rollup");
      }
      const id = operationalValueId({ campaign, repository: record.repository,
        valueId: record.value_id, observedAt: record.timestamp });
      const envelope = { schema_version: 2, kind: "operational_value", operational_value: record };
      const previous = observations.get(id);
      assert.ok(!previous || JSON.stringify(previous) === JSON.stringify(envelope),
        "Conflicting native observation at the same canonical identity");
      observations.set(id, envelope);
    }
  }
  const envelopes = [...observations.values()];
  const content = envelopes.map(envelope => JSON.stringify(envelope)).join("\n") + "\n";
  assert.ok(Buffer.byteLength(content) <= 16 * 1024 * 1024, "Native measurement bytes exceed budget");
  adaptCachedGhAwJsonl(content);
  return envelopes;
}

export function timelineMeasurementsJsonl(timeline, options) {
  return timelineMeasurementEnvelopes(timeline, options).map(envelope => JSON.stringify(envelope)).join("\n") + "\n";
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  assert.ok(process.argv.length === 4, "Usage: export-measurements.mjs TIMELINE_JSON OUTPUT_JSONL");
  assert.ok(statSync(process.argv[2]).size <= 16 * 1024 * 1024, "Timeline input exceeds byte budget");
  let timeline;
  try { timeline = JSON.parse(readFileSync(process.argv[2], "utf8")); }
  catch { throw new Error("Invalid native timeline JSON"); }
  writeFileSync(process.argv[3], timelineMeasurementsJsonl(timeline), { flag: "wx", mode: 0o600 });
}
