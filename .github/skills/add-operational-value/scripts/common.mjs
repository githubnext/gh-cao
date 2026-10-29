#!/usr/bin/env node

import { execFileSync, spawnSync } from "node:child_process";
import { accessSync, constants, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { pathToFileURL } from "node:url";
import path from "node:path";

export const scriptDir = path.dirname(fileURLToPath(import.meta.url));
export const repoRoot = path.resolve(scriptDir, "../../../..");

export function fail(message) {
  console.error(`error: ${message}`);
  process.exit(1);
}

export function requireArgs(args, minimum, maximum, usage) {
  if (args.length < minimum || args.length > maximum) fail(`usage: ${usage}`);
}

export function requireCommand(command) {
  const result = spawnSync("which", [command], { stdio: "ignore" });
  if (result.status !== 0) fail(`${command} is required`);
}

export function run(command, args = [], options = {}) {
  const isNodeScript = path.extname(command) === ".mjs";
  const result = spawnSync(isNodeScript ? process.execPath : command, isNodeScript ? [command, ...args] : args, {
    encoding: Object.hasOwn(options, "encoding") ? options.encoding : "utf8",
    input: options.input,
    cwd: options.cwd,
    env: options.env,
    stdio: options.stdio,
    maxBuffer: options.maxBuffer ?? 1024 * 1024 * 1024,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    const detail = String(result.stderr ?? "").trim();
    throw new Error(detail || `${command} exited with status ${result.status}`);
  }
  return result.stdout ?? "";
}

export function runJson(command, args = [], options = {}) {
  return JSON.parse(run(command, args, options));
}

export async function importValueModule(file) {
  return import(pathToFileURL(path.resolve(file)).href);
}

export function readJson(file) {
  return JSON.parse(readFileSync(file, "utf8"));
}

export function writeJson(value, spacing = 2) {
  return `${JSON.stringify(value, null, spacing)}\n`;
}

export function isExecutable(file) {
  try {
    accessSync(file, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

export function isRepository(value) {
  return /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9][A-Za-z0-9._-]*$/.test(value);
}

export function isSlug(value) {
  return /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(value);
}

export function isIsoUtc(value) {
  return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/.test(value)
    && !Number.isNaN(Date.parse(value));
}

export function shiftDays(value, days) {
  return new Date(Date.parse(value) + days * 86_400_000)
    .toISOString()
    .replace(".000Z", "Z");
}

export function nowUtc() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
}

export function deepEqual(left, right) {
  return JSON.stringify(left) === JSON.stringify(right);
}

export function escapeXml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}

export function sha256File(file) {
  return execFileSync("shasum", ["-a", "256", file], { encoding: "utf8" }).split(/\s+/)[0];
}
