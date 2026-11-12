#!/usr/bin/env node

import { existsSync } from "node:fs";
import { operationalValuePeriod } from "../../../../dashboard/site/src/data/model/schema.js";
import { normalizeValueAcquisition } from "../../../workflows/shared/value-acquisition.mjs";
import {
  fail, importValueModule, isIsoUtc, isRepository, isSlug, run, shiftDays,
} from "./common.mjs";

const args = process.argv.slice(2);
const runCollector = args[0] === "--collector";
if (runCollector) args.shift();
let caoAdapter;
if (args[0] === "--cao-adapter") {
  args.shift();
  caoAdapter = args.shift();
}
if (args.length !== 1) {
  fail("usage: verify-value-function.mjs [--collector] [--cao-adapter PATH] <value-module.mjs>");
}
const valueFunction = args[0];
if (!existsSync(valueFunction)) fail(`value function not found: ${valueFunction}`);

let valueModule;
try {
  valueModule = await importValueModule(valueFunction);
} catch (error) {
  fail(`value module could not be imported: ${error.message}`);
}
const { collectBatch, definition, scoreMetric } = valueModule;
const isString = (value) => typeof value === "string" && value.length > 0;
const isInteger = (value, minimum) => Number.isInteger(value) && value >= minimum;
const isSha = (value) => typeof value === "string" && /^[0-9a-f]{40}$/.test(value);
const adoption = definition.adoption ?? {};
const baselineValid = (adoption.baselineCommit == null && adoption.baselineAt == null)
  || (isSha(adoption.baselineCommit) && isIsoUtc(adoption.baselineAt));
const metrics = definition.metrics;
const examples = definition.validationExamples ?? {};
let acquisitionValid = true;
try {
  normalizeValueAcquisition(definition.acquisition);
} catch {
  acquisitionValid = false;
}
const valid = definition.schemaVersion === 3
  && isSlug(definition.slug)
  && /^\.github\/workflows\/.+\.md$/.test(definition.sourcePath ?? "")
  && isRepository(definition.repository)
  && isString(definition.workflowName)
  && isSha(adoption.commit)
  && isIsoUtc(adoption.adoptedAt)
  && baselineValid
  && !Object.hasOwn(definition, "collector")
  && acquisitionValid
  && typeof collectBatch === "function"
  && typeof scoreMetric === "function"
  && isString(definition.evidence?.key)
  && Array.isArray(definition.evidence?.repositories)
  && definition.evidence.repositories.length > 0
  && definition.evidence.repositories.every(isRepository)
  && isString(definition.evidence?.opportunity)
  && Array.isArray(definition.evidence?.filters)
  && definition.evidence.filters.every((value) => typeof value === "string")
  && isString(definition.evidence?.collection)
  && isInteger(definition.evidence?.window?.durationDays, 1)
  && isInteger(definition.evidence?.window?.cadenceDays, 1)
  && isInteger(definition.evidence?.window?.maturationDays, 0)
  && isString(definition.model?.architecture)
  && isString(definition.model?.recommendation)
  && isString(definition.model?.presentation?.label)
  && isString(definition.model?.presentation?.betterLabel)
  && ["baseline-comparable", "attainment-only"].includes(definition.evaluation?.mode ?? "baseline-comparable")
  && isString(definition.summary?.nativeLabel)
  && Array.isArray(metrics)
  && metrics.length > 0
  && metrics.every((metric) => isString(metric.id)
    && isString(metric.name)
    && ["primary", "diagnostic"].includes(metric.role)
    && isString(metric.formula)
    && ["increase", "decrease", "maintain", "target"].includes(metric.direction)
    && isString(metric.unit)
    && (!["maintain", "target"].includes(metric.direction)
      || (typeof metric.target === "number" && Number.isFinite(metric.target)))
    && isString(metric.presentation?.name)
    && isString(metric.presentation?.legendLabel)
    && metric.presentation.legendLabel.length <= 24
    && metric.presentation?.transform === "identity"
    && (metric.rollup === undefined || (
      isString(metric.rollup?.numeratorField)
      && isString(metric.rollup?.denominatorField)
      && metric.rollup.numeratorField !== metric.rollup.denominatorField
    )))
  && metrics.filter(({ role }) => role === "primary").length === 1
  && new Set(metrics.map(({ id }) => id)).size === metrics.length
  && ["targetAttained", "targetMissed", "missing", "malformed"].every((key) => Object.hasOwn(examples, key));
if (!valid) fail("value-function definition is invalid");

const score = (metricId, evidence) => {
  const result = scoreMetric(metricId, evidence);
  if (result !== null && (typeof result !== "number" || !Number.isFinite(result))) {
    fail(`${metricId} returned an invalid native value`);
  }
  return result;
};

for (const metric of metrics) {
  const results = Object.fromEntries(
    Object.entries(examples).map(([name, evidence]) => [name, score(metric.id, evidence)]),
  );
  const attainedIsBetter = metric.direction === "increase"
    ? results.targetAttained > results.targetMissed
    : metric.direction === "decrease"
      ? results.targetAttained < results.targetMissed
      : Math.abs(results.targetAttained - metric.target) < Math.abs(results.targetMissed - metric.target);
  if (results.targetAttained == null || results.targetMissed == null || !attainedIsBetter) {
    fail(`${metric.id} validation examples do not improve in the declared ${metric.direction} direction`);
  }
  if (metric.role === "primary" && (results.missing !== null || results.malformed !== null)) {
    fail(`primary metric ${metric.id} must return null for missing and malformed evidence`);
  }
}

if (runCollector) {
  const mode = definition.evaluation?.mode ?? "baseline-comparable";
  const observedAt = mode === "attainment-only"
    ? adoption.adoptedAt
    : (adoption.baselineAt ?? adoption.adoptedAt);
  const windowEnd = shiftDays(observedAt, -definition.evidence.window.maturationDays);
  const windowStart = shiftDays(windowEnd, -definition.evidence.window.durationDays);
  let collections;
  try {
    collections = await collectBatch([
      { windowStart, windowEnd, observedAt },
    ]);
  } catch (error) {
    fail(`batch collector did not exit cleanly: ${error.message}`);
  }
  const validCollections = Array.isArray(collections) && collections.length === 1
    && collections.every((collection) => collection.evidence && typeof collection.evidence === "object"
      && (!Object.hasOwn(collection, "commit") || /^[0-9a-fA-F]{40}$/.test(collection.commit))
      && Array.isArray(collection.provenance) && collection.provenance.length > 0
      && collection.provenance.every((item) => isRepository(item.repository)
        && isString(item.kind) && isString(item.ref)));
  if (!validCollections) fail("collector smoke test returned invalid evidence or provenance");
  for (const metric of metrics) {
    const result = score(metric.id, collections[0].evidence);
    if (mode === "baseline-comparable" && result === null) {
      fail(`${metric.id} has no valid score for collected baseline evidence`);
    }
  }
}

if (caoAdapter) {
  if (!existsSync(caoAdapter)) fail(`CAO adapter not found: ${caoAdapter}`);
  const timestamp = shiftDays(adoption.adoptedAt, definition.evidence.window.cadenceDays);
  const request = {
    schemaVersion: 1,
    timestamp,
    repositories: definition.evidence.repositories,
  };
  let records;
  try {
    const output = run(process.execPath, [caoAdapter], {
      input: `${JSON.stringify(request)}\n`,
      env: { ...process.env, CAO_OPERATIONAL_VALUE_MODULE: definition.slug },
    });
    records = output.split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
  } catch (error) {
    fail(`CAO adapter did not exit cleanly: ${error.message}`);
  }
  const expectedIds = new Set(metrics.map(({ id }) => `${definition.slug}.${id}`));
  const definitionRecords = records.filter(({ kind }) => kind === "operational_value_definition");
  records = records.filter(({ kind }) => kind !== "operational_value_definition");
  const definitionIds = definitionRecords[0]?.valueIds;
  const uniqueDefinitionIds = new Set(Array.isArray(definitionIds) ? definitionIds : []);
  const validDefinitions = definitionRecords.length === 1
    && Array.isArray(definitionIds)
    && definitionIds.length === expectedIds.size
    && uniqueDefinitionIds.size === expectedIds.size
    && [...expectedIds].every((valueId) => uniqueDefinitionIds.has(valueId))
    && definitionRecords[0].workflowSlug === definition.slug
    && definitionRecords[0].adoptedAt === definition.adoption.adoptedAt
    && definitionRecords[0].evaluationMode === (definition.evaluation?.mode ?? "baseline-comparable")
    && definitionRecords[0].cadenceDays === definition.evidence.window.cadenceDays
    && Array.isArray(definitionRecords[0].repositories)
    && JSON.stringify(definitionRecords[0].repositories.map((repository) => repository.toLowerCase()).toSorted())
      === JSON.stringify(definition.evidence.repositories.map((repository) => repository.toLowerCase()).toSorted());
  const recordKeys = records.map(
    ({ repository, valueId }) => `${String(repository).toLowerCase()}\0${valueId}`,
  );
  const validRecords = records.length <= definition.evidence.repositories.length * metrics.length
    && new Set(recordKeys).size === recordKeys.length
    && records.every((record) => {
      const metric = metrics.find(({ id }) => record.valueId === `${definition.slug}.${id}`);
      let period;
      try {
        period = operationalValuePeriod(record, timestamp, "CAO adapter record");
      } catch (error) {
        fail(error.message);
      }
      const hasRollup = Object.hasOwn(record, "rollupNumerator")
        || Object.hasOwn(record, "rollupDenominator");
      const hasValidRollup = Object.hasOwn(record, "rollupNumerator")
        && typeof record.rollupNumerator === "number"
        && Number.isFinite(record.rollupNumerator)
        && record.rollupNumerator >= 0
        && typeof record.rollupDenominator === "number"
        && Number.isFinite(record.rollupDenominator)
        && record.rollupDenominator > 0;
      const unavailable = record.value === null
        && record.maturityStatus === "unavailable"
        && isString(period.windowStart)
        && isString(period.missingReason)
        && !hasRollup;
      return record.timestamp === timestamp
        && definition.evidence.repositories.some(
          (repository) => repository.toLowerCase() === String(record.repository).toLowerCase(),
        )
        && expectedIds.has(record.valueId)
        && (unavailable || typeof record.value === "number" && Number.isFinite(record.value))
        && record.metricUnit === metric?.unit
        && (unavailable || (metric?.rollup ? hasValidRollup : !hasRollup || hasValidRollup));
    });
  if (!validDefinitions || !validRecords) fail("CAO adapter returned invalid repository metric JSONL");
}

console.log(`verified ${valueFunction}`);
