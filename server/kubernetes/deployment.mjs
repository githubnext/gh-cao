import { createHash, createPublicKey, createSign } from "node:crypto";
import { readFileSync } from "node:fs";
import { basename } from "node:path";
import { isDeepStrictEqual } from "node:util";
import { pathToFileURL } from "node:url";
import { parseAllDocuments } from "yaml";
import { reviewedBareInventory } from "./review-inventory.mjs";

const hash = (content) => createHash("sha256").update(content).digest("hex");
const collectorKeys = ["CAO_COLLECT_APP_ID", "CAO_COLLECT_CONTROL_REPOSITORY",
  "CAO_COLLECT_NATIVE_INVENTORY_DIGEST", "CAO_COLLECT_INVENTORY_LIMIT"];

export function readSettings(content, keys) {
  const result = {};
  for (const line of content.split(/\r?\n/)) {
    if (!line.trim() || line.trimStart().startsWith("#")) continue;
    const separator = line.indexOf("=");
    const name = line.slice(0, separator);
    if (separator < 1 || !keys.includes(name) || Object.hasOwn(result, name) || !line.slice(separator + 1)) {
      throw new Error("Invalid, duplicate or unsupported deployment setting.");
    }
    result[name] = line.slice(separator + 1);
  }
  if (Object.keys(result).length !== keys.length) throw new Error("Missing deployment setting.");
  return result;
}

export function readImages(content, policy) {
  const images = readSettings(content, ["RUNTIME_IMAGE", "MAINTENANCE_IMAGE", "POLICY_SHA256"]);
  for (const [key, repository] of [["RUNTIME_IMAGE", "cao-dashboard"], ["MAINTENANCE_IMAGE", "cao-db-maintenance"]]) {
    if (!new RegExp(`^${repository}:sha256-[a-f0-9]{64}$`).test(images[key])) {
      throw new Error("Build content-addressed Kubernetes images first.");
    }
  }
  if (images.POLICY_SHA256 !== hash(policy)) throw new Error("Build/runtime policy digest mismatch; rebuild with the reviewed policy.");
  return images;
}

export function renderManifest(content, images, profile, nativeConfig = "") {
  if (!["snapshot", "native"].includes(profile) ||
      (profile === "native" && !/^cao-native-[a-f0-9]{24}$/.test(nativeConfig))) {
    throw new Error("An explicit valid ingestion profile and native configuration are required.");
  }
  const documents = parseAllDocuments(content, { version: "1.1" }).map((document) => {
    if (document.errors.length) throw document.errors[0];
    return document.toJS();
  });
  for (const document of documents) {
    const template = document.kind === "CronJob" ? document.spec.jobTemplate.spec.template : document.spec?.template;
    if (!template) continue;
    if (template.spec.containers.some(({ image }) => ["cao-dashboard:local", "cao-db-maintenance:local"].includes(image))) {
      template.metadata ??= {};
      template.metadata.annotations = { ...template.metadata.annotations, "cao-local-policy-sha256": images.POLICY_SHA256 };
    }
    for (const container of template.spec.containers) {
      if (container.image === "cao-dashboard:local") container.image = images.RUNTIME_IMAGE;
      if (container.image === "cao-db-maintenance:local") container.image = images.MAINTENANCE_IMAGE;
      for (const entry of container.envFrom ?? []) {
        if (entry.configMapRef?.name === "cao-native") entry.configMapRef.name = nativeConfig;
      }
    }
    for (const volume of template.spec.volumes ?? []) {
      if (volume.configMap?.name === "cao-policy") volume.configMap.name = `cao-policy-${images.POLICY_SHA256.slice(0, 24)}`;
      if (volume.configMap?.name === "cao-native-inventory") volume.configMap.name = `${nativeConfig}-inventory`;
      if (volume.secret?.secretName === "cao-collector-key") volume.secret.secretName = `${nativeConfig}-key`;
    }
    if (document.kind === "Deployment" && document.metadata.name === "cao-web") {
      document.metadata.annotations = { ...document.metadata.annotations,
        "cao-local-profile": profile, "cao-local-native-config": nativeConfig };
      const web = template.spec.containers.find(({ name }) => name === "web");
      web.env = web.env.filter(({ name }) => !["CAO_EXTERNAL_INGESTION", "CAO_COLLECT_NATIVE", "CAO_COLLECT_ADMIT_ONLY"].includes(name));
      web.env.push(
        { name: "CAO_EXTERNAL_INGESTION", value: String(profile === "snapshot") },
        { name: "CAO_COLLECT_NATIVE", value: String(profile === "native") },
        { name: "CAO_COLLECT_ADMIT_ONLY", value: String(profile === "native") },
      );
      if (profile === "native") web.envFrom.push({ configMapRef: { name: nativeConfig } });
    }
  }
  return documents.length === 1 ? documents[0] : { apiVersion: "v1", kind: "List", items: documents };
}

export function nativeInputs(source, configuration, policy, key) {
  if (Buffer.byteLength(source) > 900000) throw new Error("Inventory exceeds the Kubernetes transport bound.");
  const config = readSettings(configuration, collectorKeys);
  if (!/^[a-z0-9][a-z0-9-]*\/[a-z0-9._-]+$/.test(config.CAO_COLLECT_CONTROL_REPOSITORY) ||
      config.CAO_COLLECT_CONTROL_REPOSITORY.length > 256) {
    throw new Error("Use canonical lowercase owner/repository for the native control repository.");
  }
  if (!/^[1-9][0-9]*$/.test(config.CAO_COLLECT_APP_ID) ||
      !/^[1-9][0-9]*$/.test(config.CAO_COLLECT_INVENTORY_LIMIT) ||
      config.CAO_COLLECT_NATIVE_INVENTORY_DIGEST !== hash(source)) {
    throw new Error("Invalid App identity, repository limit or reviewed inventory digest.");
  }
  const inventory = JSON.parse(source);
  const reviewed = JSON.parse(reviewedBareInventory(policy, config.CAO_COLLECT_CONTROL_REPOSITORY, inventory.sourceSHA));
  if (!isDeepStrictEqual(inventory, reviewed)) throw new Error("Inventory does not match the reviewed policy and control repository.");
  const control = reviewed.sources["configuration-policy"].rows[0].document["control-plane"];
  const declared = control.scope?.["allowed-repositories"];
  const activity = control.activity?.repositories;
  if (!Array.isArray(declared) || !Array.isArray(activity) ||
      ![...declared, ...activity].every((repository) => typeof repository === "string")) {
    throw new Error("Native scope requires explicit repository allowlist and Activity scope.");
  }
  const repositories = declared.map((repository) => repository.toLowerCase());
  if (new Set(repositories).size !== repositories.length ||
      repositories.length !== Number(config.CAO_COLLECT_INVENTORY_LIMIT) ||
      !repositories.includes(config.CAO_COLLECT_CONTROL_REPOSITORY) ||
      !isDeepStrictEqual([...repositories].sort(), activity.map((repository) => repository.toLowerCase()).sort())) {
    throw new Error("Native scope requires matching explicit repository allowlist, Activity scope and installed repository limit.");
  }
  const publicKey = createPublicKey(key).export({ type: "spki", format: "der" });
  return { config, repositories, name: `cao-native-${hash(configuration + hash(source) + hash(publicKey)).slice(0, 24)}` };
}

export async function verifyAppScope(appID, key, expected, request = fetch) {
  const allowed = expected.map((repository) => repository.toLowerCase());
  let requests = 0;
  async function api(path, authorization, options = {}) {
    if (options.method !== "DELETE" && ++requests > 200) throw new Error("App scope verification exceeded its request bound.");
    const response = await request(`https://api.github.com${path}`, {
      ...options, signal: AbortSignal.timeout(15000),
      headers: { Accept: "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28",
        Authorization: `Bearer ${authorization}`, "Content-Type": "application/json" },
    });
    if (!response.ok) throw new Error(`App scope verification failed (HTTP ${response.status}).`);
    return response;
  }
  async function pages(path, authorization, field) {
    const rows = [];
    let next = `${path}?per_page=100`;
    const visited = new Set();
    while (next) {
      if (visited.has(next)) throw new Error("Cyclic App scope pagination.");
      visited.add(next);
      const response = await api(next, authorization);
      const body = await response.json();
      const page = field ? body[field] : body;
      if (!Array.isArray(page)) throw new Error("Invalid App scope response.");
      rows.push(...page);
      const link = response.headers.get("link")?.split(",").find((part) => /;\s*rel="next"/.test(part));
      next = "";
      if (link) {
        const match = link.match(/^\s*<([^>]+)>/);
        const url = match && new URL(match[1]);
        if (!url || url.origin !== "https://api.github.com" || url.pathname !== path ||
            url.username || url.password || url.hash) throw new Error("Invalid App scope pagination destination.");
        next = url.pathname + url.search;
      }
    }
    return rows;
  }
  const now = Math.floor(Date.now() / 1000);
  const unsigned = [Buffer.from('{"alg":"RS256","typ":"JWT"}').toString("base64url"),
    Buffer.from(JSON.stringify({ iat: now - 60, exp: now + 540, iss: appID })).toString("base64url")].join(".");
  const jwt = `${unsigned}.${createSign("RSA-SHA256").update(unsigned).sign(key).toString("base64url")}`;
  const installations = await pages("/app/installations", jwt);
  const observed = new Set();
  for (const installation of installations) {
    if (!Number.isSafeInteger(installation.id) || installation.id < 1 || installation.suspended_at) {
      throw new Error("Invalid or suspended App installation.");
    }
    const minted = await api(`/app/installations/${installation.id}/access_tokens`, jwt,
      { method: "POST", body: JSON.stringify({ permissions: { metadata: "read" } }) });
    const { token } = await minted.json();
    if (typeof token !== "string" || !token) throw new Error("App scope token was not returned.");
    try {
      for (const repository of await pages("/installation/repositories", token, "repositories")) {
        const name = typeof repository.full_name === "string" ? repository.full_name.toLowerCase() : "";
        if (!allowed.includes(name)) throw new Error("App installation includes an unreviewed repository.");
        observed.add(name);
      }
    } finally {
      await api("/installation/token", token, { method: "DELETE" });
    }
  }
  if (!isDeepStrictEqual([...observed].sort(), allowed.sort())) {
    throw new Error("App installation membership does not match the reviewed repository scope.");
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [command, ...args] = process.argv.slice(2);
  if (command === "render" && args.length === 5) {
    const [file, lock, policy, profile, config] = args;
    const images = readImages(readFileSync(lock, "utf8"), readFileSync(policy, "utf8"));
    console.log(JSON.stringify(renderManifest(readFileSync(file, "utf8"), images, profile, config)));
  } else if (command === "images" && args.length === 2) {
    readImages(readFileSync(args[0], "utf8"), readFileSync(args[1], "utf8"));
  } else if (command === "verify-native" && args.length === 4) {
    const [source, config, policy, key] = args.map((file) => readFileSync(file, "utf8"));
    const input = nativeInputs(source, config, policy, key);
    await verifyAppScope(input.config.CAO_COLLECT_APP_ID, key, input.repositories);
    console.log(input.name);
  } else {
    throw new Error(`Unsupported ${basename(process.argv[1])} command or arguments.`);
  }
}
