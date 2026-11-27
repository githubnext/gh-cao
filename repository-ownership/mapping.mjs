import { parseDocument } from "yaml";

const REPOSITORY = /^[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,98})\/[A-Za-z0-9_.-]{1,100}$/;
const IDENTITIES = {
  team: /^github:team:[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})\/[A-Za-z0-9][A-Za-z0-9_-]{0,99}$/,
  person: /^github:user:[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$/,
  service: /^service:[A-Za-z0-9][A-Za-z0-9_.-]{0,99}\/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$/,
};
const UTC = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/;
const validatedMappings = new WeakSet();

function object(value, keys, required, location) {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${location} must be an object`);
  }
  for (const key of Object.keys(value)) {
    if (!keys.includes(key)) throw new Error(`${location} has unknown field ${key}`);
  }
  for (const key of required) {
    if (!Object.hasOwn(value, key)) throw new Error(`${location} is missing ${key}`);
  }
}

function timestamp(value, location) {
  if (typeof value !== "string" || !UTC.test(value)) throw new Error(`${location} must be a UTC timestamp`);
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed) || new Date(parsed).toISOString() !== value.replace("Z", ".000Z")) {
    throw new Error(`${location} must be a valid UTC timestamp`);
  }
  return parsed;
}

/**
 * Validate a human-authored YAML mapping. This does not verify identities,
 * review history, repository eligibility, or create operational evidence.
 */
export function parseMapping(source) {
  if (typeof source !== "string" || Buffer.byteLength(source, "utf8") > 262144) {
    throw new Error("mapping must be text no larger than 256 KiB");
  }
  const document = parseDocument(source, { uniqueKeys: true });
  if (document.errors.length) throw new Error(`invalid YAML: ${document.errors[0].message}`);
  const mapping = document.toJS({ maxAliasCount: 0 });
  object(mapping, ["version", "repositories"], ["version", "repositories"], "mapping");
  if (mapping.version !== 1 || !Array.isArray(mapping.repositories) || mapping.repositories.length > 10000) {
    throw new Error("mapping requires version 1 and at most 10000 repositories");
  }
  const repositories = new Set();
  for (const entry of mapping.repositories) {
    object(entry, ["repository", "owners", "reviewed_at", "review_due_at", "exemption"],
      ["repository", "owners", "reviewed_at", "review_due_at"], "repository entry");
    if (typeof entry.repository !== "string" || !REPOSITORY.test(entry.repository)) {
      throw new Error("invalid repository name");
    }
    const key = entry.repository.toLowerCase();
    if (repositories.has(key)) throw new Error(`duplicate repository ${entry.repository}`);
    repositories.add(key);
    if (!Array.isArray(entry.owners) || entry.owners.length === 0 || entry.owners.length > 100) {
      throw new Error(`${entry.repository} requires 1 to 100 owners`);
    }
    const owners = new Set();
    for (const owner of entry.owners) {
      object(owner, ["kind", "id"], ["kind", "id"], "owner");
      if (!Object.hasOwn(IDENTITIES, owner.kind) || typeof owner.id !== "string" || !IDENTITIES[owner.kind].test(owner.id)) {
        throw new Error(`invalid owner identity in ${entry.repository}`);
      }
      const ownerKey = owner.id.toLowerCase();
      if (owners.has(ownerKey)) throw new Error(`duplicate owner in ${entry.repository}`);
      owners.add(ownerKey);
    }
    const reviewed = timestamp(entry.reviewed_at, "reviewed_at");
    if (timestamp(entry.review_due_at, "review_due_at") <= reviewed) {
      throw new Error("review_due_at must be later than reviewed_at");
    }
    if (Object.hasOwn(entry, "exemption")) {
      object(entry.exemption, ["reason", "expires_at"], ["reason", "expires_at"], "exemption");
      if (typeof entry.exemption.reason !== "string" || !entry.exemption.reason.trim() || entry.exemption.reason.length > 1000) {
        throw new Error("exemption requires a nonempty reason of at most 1000 characters");
      }
      if (timestamp(entry.exemption.expires_at, "exemption.expires_at") <= reviewed) {
        throw new Error("exemption expiry must be later than reviewed_at");
      }
    }
  }
  for (const entry of mapping.repositories) {
    for (const owner of entry.owners) Object.freeze(owner);
    Object.freeze(entry.owners);
    if (entry.exemption) Object.freeze(entry.exemption);
    Object.freeze(entry);
  }
  Object.freeze(mapping.repositories);
  Object.freeze(mapping);
  validatedMappings.add(mapping);
  return mapping;
}

/**
 * Time classification only; this deliberately does not authorize actions or
 * treat a declared exemption as a verified permission to suppress findings.
 */
export function mappingStatus(mapping, repository, at) {
  if (!validatedMappings.has(mapping)) throw new Error("mapping must be parsed and validated first");
  if (typeof repository !== "string" || !REPOSITORY.test(repository)) throw new Error("invalid repository name");
  const now = timestamp(at, "at");
  const entry = mapping.repositories.find(item => item.repository.toLowerCase() === repository.toLowerCase());
  if (!entry) return { status: "unmapped", exemption: "none" };
  const reviewed = timestamp(entry.reviewed_at, "reviewed_at");
  const due = timestamp(entry.review_due_at, "review_due_at");
  const status = now < reviewed ? "not-yet-reviewed" : now < due ? "current" : "stale";
  const exemption = !entry.exemption ? "none"
    : now < reviewed ? "not-yet-effective"
      : now < timestamp(entry.exemption.expires_at, "exemption.expires_at") ? "declared-current" : "expired";
  return { status, exemption };
}
