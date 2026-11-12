import assert from "node:assert/strict";
import { createHmac, randomBytes } from "node:crypto";
import { readFile } from "node:fs/promises";
import http from "node:http";
import test from "node:test";
import { goCase, localEndpoint } from "../helpers/azure-local-go.mjs";

const runtimePath = process.argv[2];
if (!runtimePath) throw new Error("runtime state JSON path is required");
const runtime = JSON.parse(await readFile(runtimePath, "utf8"));

function storageRequest(service, method, path, body = "", headers = {}, invalidKey = false) {
  const endpoint = localEndpoint(runtime.storage[`${service}Endpoint`], "http:");
  assert.match(runtime.storage.account, /^[a-z0-9]+$/);
  assert.equal(endpoint.pathname, `/${runtime.storage.account}`);
  const url = new URL(`${endpoint}${path}`);
  const date = new Date().toUTCString();
  const content = Buffer.from(body);
  headers = {
    "x-ms-date": date,
    "x-ms-version": "2019-02-02",
    "content-length": String(content.length),
    ...headers,
  };
  const resource = `/${runtime.storage.account}${url.pathname}`;
  let canonical;
  if (service === "table") {
    canonical = `${date}\n${resource}`;
  } else {
    const standard = [
      method, headers["content-encoding"], headers["content-language"],
      content.length ? String(content.length) : "", headers["content-md5"],
      headers["content-type"], "", headers["if-modified-since"], headers["if-match"],
      headers["if-none-match"], headers["if-unmodified-since"], headers.range,
    ].map((value) => value || "").join("\n");
    const azureHeaders = Object.keys(headers).filter((name) => name.startsWith("x-ms-"))
      .sort().map((name) => `${name}:${headers[name]}\n`).join("");
    const query = [...new Set([...url.searchParams.keys()].map((name) => name.toLowerCase()))]
      .sort().map((name) => `\n${name}:${url.searchParams.getAll(name).sort().join(",")}`).join("");
    canonical = `${standard}\n${azureHeaders}${resource}${query}`;
  }
  const key = invalidKey ? randomBytes(32) : Buffer.from(runtime.storage.key, "base64");
  const signature = createHmac("sha256", key).update(canonical).digest("base64");
  headers.authorization = `${service === "table" ? "SharedKeyLite" : "SharedKey"} ${runtime.storage.account}:${signature}`;
  // Node's HTTP client does not consult ambient proxy settings or follow redirects.
  return new Promise((resolve, reject) => {
    const request = http.request(url, { method, headers, agent: false }, (response) => {
      const chunks = [];
      let length = 0;
      response.on("data", (chunk) => {
        length += chunk.length;
        if (length > 1 << 20) response.destroy(new Error("emulator response exceeds test limit"));
        else chunks.push(chunk);
      });
      response.on("error", reject);
      response.on("end", () => resolve({
        status: response.statusCode,
        headers: response.headers,
        body: Buffer.concat(chunks).toString("utf8"),
      }));
    });
    request.setTimeout(5000, () => request.destroy(new Error("emulator request timed out")));
    request.on("error", reject);
    request.end(content);
  });
}

test("Azure Storage Blob: signed writes, reads, ETag conflicts and denied credentials", async (t) => {
  const container = `cao-test-${randomBytes(8).toString("hex")}`;
  assert.equal((await storageRequest("blob", "PUT", `/${container}?restype=container`)).status, 201);
  t.after(async () => {
    assert.equal((await storageRequest("blob", "DELETE", `/${container}?restype=container`)).status, 202);
  });
  const path = `/${container}/evidence.json`;
  const written = await storageRequest("blob", "PUT", path, '{"synthetic":true}', {
    "x-ms-blob-type": "BlockBlob",
  });
  assert.equal(written.status, 201);
  const read = await storageRequest("blob", "GET", path);
  assert.equal(read.status, 200);
  assert.deepEqual(JSON.parse(read.body), { synthetic: true });
  assert.equal(read.headers.etag, written.headers.etag);
  assert.equal((await storageRequest("blob", "PUT", path, "replacement", {
    "x-ms-blob-type": "BlockBlob", "if-match": '"stale"',
  })).status, 412);
  assert.equal((await storageRequest("blob", "GET", path, "", {}, true)).status, 403);
  assert.equal((await storageRequest("blob", "GET", path)).body, read.body);
  assert.equal((await storageRequest("blob", "GET", `/${container}/missing`)).status, 404);
});

test("Azure Storage Queue: visibility, receipts and acknowledgement", async (t) => {
  const queue = `cao-test-${randomBytes(8).toString("hex")}`;
  assert.equal((await storageRequest("queue", "PUT", `/${queue}`)).status, 201);
  t.after(async () => {
    assert.equal((await storageRequest("queue", "DELETE", `/${queue}`)).status, 204);
  });
  const messages = `/${queue}/messages`;
  assert.equal((await storageRequest("queue", "POST", messages,
    "<QueueMessage><MessageText>synthetic-message</MessageText></QueueMessage>",
    { "content-type": "application/xml" })).status, 201);
  const received = await storageRequest("queue", "GET", `${messages}?visibilitytimeout=30`);
  assert.equal(received.status, 200);
  assert.match(received.body, /<MessageText>synthetic-message<\/MessageText>/);
  const id = received.body.match(/<MessageId>([^<]+)<\/MessageId>/)?.[1];
  const receipt = received.body.match(/<PopReceipt>([^<]+)<\/PopReceipt>/)?.[1];
  assert.ok(id);
  assert.ok(receipt);
  const hidden = await storageRequest("queue", "GET", messages);
  assert.equal(hidden.status, 200);
  assert.doesNotMatch(hidden.body, /<MessageId>/);
  assert.equal((await storageRequest("queue", "DELETE",
    `${messages}/${encodeURIComponent(id)}?popreceipt=${encodeURIComponent(receipt)}`)).status, 204);
  const empty = await storageRequest("queue", "GET", messages);
  assert.equal(empty.status, 200);
  assert.doesNotMatch(empty.body, /<MessageId>/);
});

test("Azure Storage Table: entity persistence and stale ETag rejection", async (t) => {
  const table = `CaoTest${randomBytes(8).toString("hex")}`;
  const headers = { "content-type": "application/json", accept: "application/json;odata=nometadata" };
  assert.equal((await storageRequest("table", "POST", "/Tables",
    JSON.stringify({ TableName: table }), headers)).status, 201);
  t.after(async () => {
    assert.equal((await storageRequest("table", "DELETE", `/Tables('${table}')`, "", headers)).status, 204);
  });
  const entity = { PartitionKey: "synthetic", RowKey: "one", value: "original" };
  const inserted = await storageRequest("table", "POST", `/${table}`, JSON.stringify(entity), headers);
  assert.equal(inserted.status, 201);
  const path = `/${table}(PartitionKey='synthetic',RowKey='one')`;
  const read = await storageRequest("table", "GET", path, "", headers);
  assert.equal(read.status, 200);
  assert.equal(JSON.parse(read.body).value, "original");
  assert.ok(read.headers.etag);
  assert.equal((await storageRequest("table", "PUT", path,
    JSON.stringify({ ...entity, value: "updated" }), { ...headers, "if-match": read.headers.etag })).status, 204);
  assert.equal((await storageRequest("table", "PUT", path,
    JSON.stringify({ ...entity, value: "stale" }), { ...headers, "if-match": read.headers.etag })).status, 412);
  assert.equal(JSON.parse((await storageRequest("table", "GET", path, "", headers)).body).value, "updated");
  assert.equal((await storageRequest("table", "GET",
    `/${table}(PartitionKey='synthetic',RowKey='missing')`, "", headers)).status, 404);
});

test("Azure Go service contracts", { concurrency: true }, async (t) => {
  await Promise.all([
    t.test("Azure PostgreSQL operational adapter: sessions, CAS, queue capacity and leases", async () => {
      await goCase("server", ["./internal/operational"], ["^TestAdapters$/^postgres$"], { postgres: true }, runtime);
    }),
    t.test("Azure server adapters: canonical PostgreSQL, Blob archive, Key Vault and monitoring", async () => {
      await goCase("server", [
        "./internal/postgresx", "./internal/sessionarchiveblob", "./internal/tokenbroker", "./internal/telemetry",
      ], [
        "^TestFreshSchemaReopen$",
        "^TestAzuriteBlobStoreIntegration$",
        "^TestArchive(ServiceInjectionPreservesActivationGates|InjectedServicesHTTPIntegration)$",
        "^TestAzureKeyVaultHTTPIntegration$",
        "^TestAzureDeploymentTelemetryReceipts$",
      ], { postgres: true, blob: true }, runtime);
    }),
  ]);
});
