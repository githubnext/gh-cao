#!/usr/bin/env node

import { readFileSync, readdirSync } from "node:fs";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const REPOSITORY_COORDINATE = /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/;
const MODULE_SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const moduleDirectory = fileURLToPath(new URL("./operational-value/", import.meta.url));

function fail(message) {
  console.error(message);
  process.exit(1);
}

function shiftDays(timestamp, days) {
  return new Date(Date.parse(timestamp) + days * 86_400_000).toISOString();
}

let request;
try {
  request = JSON.parse(readFileSync(0, "utf8"));
} catch {
  fail("Invalid operational value request");
}
if (
  request?.schemaVersion !== 1
  || (request.localOnly !== undefined && typeof request.localOnly !== "boolean")
  || typeof request.timestamp !== "string"
  || Number.isNaN(Date.parse(request.timestamp))
  || !Array.isArray(request.repositories)
  || request.repositories.some((repository) => (
    typeof repository !== "string" || !REPOSITORY_COORDINATE.test(repository)
  ))
) {
  fail("Invalid operational value request");
}

const selectedModule = process.env.CAO_OPERATIONAL_VALUE_MODULE;
if (selectedModule && !MODULE_SLUG.test(selectedModule)) {
  fail("CAO_OPERATIONAL_VALUE_MODULE must be a workflow slug");
}
const history = request.historyTimestamps;
if (history !== undefined && (
  !selectedModule || !Array.isArray(history) || history.length === 0 || history.length > 366
  || history.at(-1) !== request.timestamp
  || history.some((timestamp, index) => (
    typeof timestamp !== "string" || !Number.isFinite(Date.parse(timestamp))
    || new Date(timestamp).toISOString() !== timestamp
    || Date.parse(timestamp) > Date.parse(request.timestamp)
    || (index > 0 && Date.parse(timestamp) <= Date.parse(history[index - 1]))
  ))
)) {
  fail("Invalid operational value history batch");
}
const moduleFiles = readdirSync(moduleDirectory)
  .filter((name) => MODULE_SLUG.test(name.replace(/\.mjs$/, "")) && name.endsWith(".mjs"))
  .filter((name) => !selectedModule || name === `${selectedModule}.mjs`)
  .sort();
if (selectedModule && moduleFiles.length === 0) {
  fail(`Operational value module not found: ${selectedModule}`);
}

for (const moduleFile of moduleFiles) {
  const valueModule = await import(pathToFileURL(path.join(moduleDirectory, moduleFile)).href);
  const { collectBatch, definition, scoreMetric } = valueModule;
  if (history && valueModule.historyBatch !== true) {
    fail(`Operational value module does not support local history batches: ${moduleFile}`);
  }
  if (
    !MODULE_SLUG.test(definition?.slug ?? "")
    || typeof collectBatch !== "function"
    || typeof scoreMetric !== "function"
    || !Array.isArray(definition?.metrics)
    || !Array.isArray(definition?.evidence?.repositories)
    || typeof definition?.adoption?.adoptedAt !== "string"
    || Number.isNaN(Date.parse(definition.adoption.adoptedAt))
    || !["baseline-comparable", "attainment-only"].includes(definition?.evaluation?.mode)
    || typeof definition?.evidence?.window?.cadenceDays !== "number"
    || !Number.isFinite(definition.evidence.window.cadenceDays)
    || definition.evidence.window.cadenceDays <= 0
  ) {
    fail(`Invalid operational value module: ${moduleFile}`);
  }
  console.log(JSON.stringify({
    kind: "operational_value_definition",
    workflowSlug: definition.slug,
    adoptedAt: definition.adoption.adoptedAt,
    evaluationMode: definition.evaluation?.mode ?? "baseline-comparable",
    cadenceDays: definition.evidence.window.cadenceDays,
    repositories: definition.evidence.repositories,
    valueIds: definition.metrics.map((metric) => `${definition.slug}.${metric.id}`),
    ...(valueModule.historyBatch === true ? { historyBatch: true } : {}),
  }));
  if (request.localOnly && request.disabledWorkflows?.includes(definition.slug)) continue;

  const supported = new Set(
    definition.evidence.repositories.map((repository) => String(repository).toLowerCase()),
  );
  const repositories = request.repositories.filter((repository) => supported.has(repository.toLowerCase())
    && !(request.localOnly && request.disabledCollections?.includes(`${repository.toLowerCase()}:${definition.slug}`)));
  if (repositories.length === 0) continue;

  const { durationDays, maturationDays } = definition.evidence.window;
  const observedAt = request.timestamp;
  const firstMatureAt = shiftDays(definition.adoption.adoptedAt, durationDays + maturationDays);
  const windows = (history ?? [observedAt]).flatMap((timestamp) => {
    const interim = definition.evaluation?.mode === "attainment-only"
      && Date.parse(timestamp) < Date.parse(firstMatureAt);
    const availableEnd = shiftDays(timestamp, -maturationDays);
    const cadenceMs = definition.evidence.window.cadenceDays * 86_400_000;
    const adoptionMs = Date.parse(definition.adoption.adoptedAt);
    const alignedEnd = new Date(adoptionMs + Math.floor(
      (Date.parse(availableEnd) - adoptionMs) / cadenceMs,
    ) * cadenceMs).toISOString();
    const windowEnd = interim ? timestamp : selectedModule ? availableEnd : alignedEnd;
    return repositories.map((repository) => ({
      repository,
      observedAt: timestamp,
      windowStart: interim ? definition.adoption.adoptedAt : shiftDays(windowEnd, -durationDays),
      windowEnd,
    }));
  });

  const evaluatorDigest = createHash("sha256")
    .update(readFileSync(fileURLToPath(import.meta.url)))
    .update(readFileSync(path.join(moduleDirectory, moduleFile)))
    .digest("hex");
  if (process.env.CAO_DASHBOARD_QUERY_SOCKET && !selectedModule) {
    const query = {
      name: "reusable-operational-values",
      from: "operationalValues",
      filter: { predicates: [
        { field: "campaign", equals: path.basename(path.dirname(moduleDirectory)) },
        { field: "repository", in: repositories },
        { field: "valueId", in: definition.metrics.map((metric) => `${definition.slug}.${metric.id}`) },
        { field: "evaluator-digest", equals: evaluatorDigest },
        { field: "timestamp", gte: new Date(Date.parse(observedAt) - definition.evidence.window.cadenceDays * 86_400_000 + 1).toISOString() },
        { field: "timestamp", lt: new Date(Date.parse(observedAt) + 1).toISOString() },
      ] },
      "order-by": [{ field: "timestamp", direction: "desc" }],
    };
    const { executeCanonicalQuery } = await import(process.env.CAO_CANONICAL_QUERY_MODULE
      || new URL("../.github/skills/add-operational-value/scripts/canonical-query.mjs", import.meta.url).href);
    const { rows } = await executeCanonicalQuery(query);
    if (!Array.isArray(rows)) fail("Invalid retained operational-value measurements");
    const cached = repositories.flatMap((repository) => definition.metrics.map((metric) => rows.find((row) => (
      row.repository === repository && row.valueId === `${definition.slug}.${metric.id}`
    ))));
    if (cached.length > 0 && cached.every((row) => row
      && typeof row.value === "number" && Number.isFinite(row.value)
      && row["maturity-status"] !== "unavailable"
      && (!["interval", "snapshot"].includes(definition.evidence.measurementKind)
        || windows.some((window) => window.repository === row.repository
          && window.windowStart === row["window-start"] && window.windowEnd === row["window-end"]
          && definition.evidence.measurementKind === row["measurement-kind"])))) {
      for (const row of cached) console.log(JSON.stringify({
        timestamp: row.timestamp, repository: row.repository, valueId: row.valueId, value: row.value,
        metricRole: row["operational-value-role"], metricName: row["operational-value-name"],
        metricUnit: row["operational-value-unit"], metricDirection: row["operational-value-direction"],
        maturityStatus: row["maturity-status"], adoptionAt: row["adoption-at"],
        evaluationMode: row["evaluation-mode"], workflowSlug: row["workflow-slug"],
        workflowName: row["workflow-name"], evaluatorDigest,
        cadenceDays: row["cadence-days"],
        windowStart: row["window-start"], windowEnd: row["window-end"],
        measurementKind: row["measurement-kind"], coverageStatus: row["coverage-status"],
        missingReason: row["missing-reason"], eligibleCount: row["eligible-count"],
        populationDigest: row["population-digest"], evidenceDigest: row["evidence-digest"],
        ...(row["rollup-numerator"] !== undefined ? {
          rollupNumerator: row["rollup-numerator"], rollupDenominator: row["rollup-denominator"],
        } : {}),
      }));
      continue;
    }
  }

  if (request.localOnly && valueModule.localCollection !== true) {
    console.log(JSON.stringify({ kind: "operational_value_deferred", workflowSlug: definition.slug,
      reason: "Remote acquisition requires enforceable shared API admission; retained evidence is unchanged." }));
    continue;
  }
  let collections;
  try {
    collections = await collectBatch(windows, {
      database: request.database,
      ...(request.localOnly ? { localOnly: true } : {}),
      ...(history ? { history: true } : {}),
    });
  } catch (error) {
    fail(error instanceof Error ? error.message : String(error));
  }
  if (!Array.isArray(collections) || collections.length !== windows.length) {
    fail(`Operational value collector returned an invalid result: ${definition.slug}`);
  }

  for (let index = 0; index < windows.length; index += 1) {
    const collection = collections[index];
    const evidence = collection.evidence;
    const measurementKind = definition.evidence.measurementKind;
    const hasPeriod = ["interval", "snapshot"].includes(measurementKind);
    const coverageStatus = evidence.coverageStatus
      ?? (measurementKind === "snapshot" && evidence.valid === true ? "complete" : "unknown");
    const evidenceDigest = hasPeriod ? createHash("sha256")
      .update(JSON.stringify({ evidence, provenance: collection.provenance }))
      .digest("hex") : undefined;
    for (const metric of definition.metrics) {
      const value = scoreMetric(metric.id, evidence);
      if (value === null && !hasPeriod) continue;
      if (value !== null && (typeof value !== "number" || !Number.isFinite(value))) {
        fail(`Operational value metric returned an invalid value: ${definition.slug}.${metric.id}`);
      }
      const rollupNumerator = metric.rollup && value !== null
        ? Number(collections[index].evidence?.[metric.rollup.numeratorField])
        : undefined;
      const rollupDenominator = metric.rollup && value !== null
        ? Number(collections[index].evidence?.[metric.rollup.denominatorField])
        : undefined;
      if (metric.rollup && value !== null && (
        !Number.isFinite(rollupNumerator)
        || !Number.isFinite(rollupDenominator)
        || rollupNumerator < 0
        || rollupDenominator <= 0
      )) {
        fail(`Operational value metric returned invalid rollup evidence: ${definition.slug}.${metric.id}`);
      }
      console.log(JSON.stringify({
        timestamp: windows[index].observedAt,
        repository: windows[index].repository,
        valueId: `${definition.slug}.${metric.id}`,
        value,
        metricRole: metric.role,
        metricName: metric.name,
        metricUnit: metric.unit,
        cadenceDays: definition.evidence.window.cadenceDays,
        metricDirection: metric.direction,
        maturityStatus: value === null ? "unavailable" : evidence?.maturityStatus ?? "matured",
        adoptionAt: definition.adoption.adoptedAt,
        evaluationMode: definition.evaluation?.mode ?? "baseline-comparable",
        workflowSlug: definition.slug,
        workflowName: definition.workflowName,
        evaluatorDigest,
        ...(hasPeriod ? {
          windowStart: windows[index].windowStart,
          windowEnd: windows[index].windowEnd,
          measurementKind, coverageStatus, evidenceDigest,
          missingReason: evidence.missingReason ?? (value === null ? "outcome-evidence-unavailable" : undefined),
          eligibleCount: evidence.eligibleCount,
          populationDigest: evidence.populationDigest,
        } : {}),
        ...(metric.rollup && value !== null ? { rollupNumerator, rollupDenominator } : {}),
      }));
    }
  }
}
