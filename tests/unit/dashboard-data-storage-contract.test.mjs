import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { SQL_EXPORT_VERSION } from "../../dashboard/site/src/data/adapters/sql-export.js";
import { CANONICAL_SCHEMA_VERSION, ENTITY_KINDS } from "../../dashboard/site/src/data/model/schema.js";
import { normalize } from "../../dashboard/site/src/data/normalize/index.js";
import {
  CANONICAL_DATABASE_SCHEMA,
  DATABASE_NAME,
  DATABASE_VERSION,
  ENTITY_STORES,
} from "../../dashboard/site/src/data/storage/indexeddb.js";
import { root } from "./workflow-contract.helpers.mjs";

const specification = readFileSync(join(root, "specs", "dashboard-data.md"), "utf8");
const dataModel = readFileSync(join(root, "docs", "dashboard-data-model.md"), "utf8");
const ingestion = readFileSync(join(root, "docs", "dashboard-data-ingestion.md"), "utf8");

test("dashboard data specification matches implemented storage versions", () => {
  assert.match(specification, new RegExp(`\\| Canonical model \\| ${CANONICAL_SCHEMA_VERSION} \\|`));
  assert.match(specification, new RegExp(`\\| Browser IndexedDB \\| ${DATABASE_VERSION} \\|`));
  assert.match(specification, new RegExp(`\\| Static SQL export \\| ${SQL_EXPORT_VERSION} \\|`));
  assert.match(specification, new RegExp(`const DATABASE_NAME = "${DATABASE_NAME}";`));
  assert.match(specification, new RegExp(`const DATABASE_VERSION = ${DATABASE_VERSION};`));
  assert.match(specification, new RegExp(`IndexedDB version ${DATABASE_VERSION} SHALL define:`));

  for (const document of [dataModel, ingestion]) {
    assert.match(document, new RegExp(`canonical model is version ${CANONICAL_SCHEMA_VERSION}`, "i"));
    assert.match(document, new RegExp(`IndexedDB version ${DATABASE_VERSION}`));
  }
});

test("dashboard data specification lists every implemented store and index", () => {
  for (const [storeName, definition] of Object.entries(CANONICAL_DATABASE_SCHEMA)) {
    assert.match(specification, new RegExp(`\`${storeName}\``), `missing store ${storeName}`);
    for (const [indexName, keyPath] of Object.entries(definition.indexes)) {
      const renderedKeyPath = Array.isArray(keyPath)
        ? `\\[${keyPath.join(", ")}\\]`
        : keyPath;
      assert.match(
        specification,
        new RegExp(`\`${indexName} -> ${renderedKeyPath}\``),
        `missing index ${storeName}.${indexName}`,
      );
    }
  }
});

test("Campaign is a required canonical entity and persisted database collection", () => {
  const coreEntities = specification.match(/## 6\.1 Core Entities[\s\S]*?```text\n([\s\S]*?)```/);
  assert.ok(coreEntities, "missing normative core entity list");
  assert.ok(coreEntities[1].split("\n").includes("Campaign"));
  assert.match(specification, /\*\*CAM-001\*\*/);
  assert.match(specification, /Postgres SHALL expose a physical `campaigns`\s+table/);
  assert.ok(ENTITY_KINDS.includes("campaign"));
  assert.ok(ENTITY_STORES.includes("campaigns"));
  assert.deepEqual(normalize([]).campaigns, []);
  assert.deepEqual(CANONICAL_DATABASE_SCHEMA.campaigns, {
    keyPath: "id",
    indexes: { bySlug: "slug" },
  });
});

test("run identity documentation matches canonical normalization", () => {
  const identity = "github:run:<owner>/<repository>:<run-id>";
  assert.match(dataModel, new RegExp(identity.replaceAll(/[.*+?^${}()|[\]\\]/g, "\\$&")));
  assert.doesNotMatch(dataModel, /github:run:<run-id>:attempt:<attempt>/);
  assert.doesNotMatch(specification, /github:run:<id>:attempt:<attempt>/);
});
