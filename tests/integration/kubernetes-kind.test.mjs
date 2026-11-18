import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import http from "node:http";
import https from "node:https";
import { join } from "node:path";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { gunzipSync } from "node:zlib";
import { prepareDashboardServerFixture } from "../e2e/dashboard-server-fixture.mjs";

const execute = promisify(execFile);
const root = fileURLToPath(new URL("../../", import.meta.url));

function walkProto(buffer, onField) {
  let offset = 0;
  const readVarint = () => {
    let value = 0n;
    let shift = 0n;
    while (offset < buffer.length) {
      const byte = BigInt(buffer[offset++]);
      value |= (byte & 0x7fn) << shift;
      if ((byte & 0x80n) === 0n) return value;
      shift += 7n;
    }
    throw new Error("Truncated OTLP protobuf varint.");
  };
  while (offset < buffer.length) {
    const tag = Number(readVarint());
    const field = tag >>> 3;
    const wire = tag & 0x07;
    if (wire === 0) {
      onField(field, wire, readVarint());
    } else if (wire === 1) {
      if (offset + 8 > buffer.length) throw new Error("Truncated OTLP protobuf fixed64.");
      onField(field, wire, buffer.subarray(offset, offset += 8));
    } else if (wire === 2) {
      const length = Number(readVarint());
      if (offset + length > buffer.length) throw new Error("Truncated OTLP protobuf bytes.");
      onField(field, wire, buffer.subarray(offset, offset += length));
    } else if (wire === 5) {
      if (offset + 4 > buffer.length) throw new Error("Truncated OTLP protobuf fixed32.");
      onField(field, wire, buffer.subarray(offset, offset += 4));
    } else {
      throw new Error(`Unsupported OTLP protobuf wire type: ${wire}`);
    }
  }
}

function decodeAnyValue(buffer) {
  let result;
  walkProto(buffer, (field, wire, value) => {
    if (field === 1 && wire === 2) result = value.toString("utf8");
    else if (field === 2 && wire === 0) result = value !== 0n;
    else if (field === 3 && wire === 0) result = Number(BigInt.asIntN(64, value));
    else if (field === 4 && wire === 1) result = value.readDoubleLE(0);
    else if (field === 7 && wire === 2) result = value.toString("hex");
  });
  return result;
}

function decodeKeyValue(buffer) {
  let key = "";
  let val;
  walkProto(buffer, (field, wire, value) => {
    if (field === 1 && wire === 2) key = value.toString("utf8");
    else if (field === 2 && wire === 2) val = decodeAnyValue(value);
  });
  return [key, val];
}

function decodeResource(buffer) {
  const attributes = {};
  walkProto(buffer, (field, wire, value) => {
    if (field === 1 && wire === 2) {
      const [k, v] = decodeKeyValue(value);
      if (k) attributes[k] = v;
    }
  });
  return attributes;
}

function decodeScope(buffer) {
  let name = "";
  walkProto(buffer, (field, wire, value) => {
    if (field === 1 && wire === 2) name = value.toString("utf8");
  });
  return name;
}

export function decodeOtlpTraces(buffer) {
  const spans = [];
  walkProto(buffer, (field, wire, resourceSpans) => {
    if (field !== 1 || wire !== 2) return;
    let resourceAttributes = {};
    const scopeSpansList = [];
    walkProto(resourceSpans, (rf, rw, rv) => {
      if (rf === 1 && rw === 2) resourceAttributes = decodeResource(rv);
      else if (rf === 2 && rw === 2) scopeSpansList.push(rv);
    });
    const serviceName = resourceAttributes["service.name"] ?? "";
    for (const scopeSpans of scopeSpansList) {
      let scope = "";
      const rawSpans = [];
      walkProto(scopeSpans, (sf, sw, sv) => {
        if (sf === 1 && sw === 2) scope = decodeScope(sv);
        else if (sf === 2 && sw === 2) rawSpans.push(sv);
      });
      for (const rawSpan of rawSpans) {
        const span = {
          serviceName,
          scope,
          traceId: "",
          spanId: "",
          parentSpanId: "",
          name: "",
          kind: 0,
          status: { code: 0, message: "" },
          attributes: {},
          events: [],
        };
        walkProto(rawSpan, (f, w, v) => {
          if (f === 1 && w === 2) span.traceId = v.toString("hex");
          else if (f === 2 && w === 2) span.spanId = v.toString("hex");
          else if (f === 4 && w === 2) span.parentSpanId = v.toString("hex");
          else if (f === 5 && w === 2) span.name = v.toString("utf8");
          else if (f === 6 && w === 0) span.kind = Number(v);
          else if (f === 9 && w === 2) {
            const [k, val] = decodeKeyValue(v);
            if (k) span.attributes[k] = val;
          } else if (f === 11 && w === 2) {
            const event = { name: "", attributes: {} };
            walkProto(v, (ef, ew, ev) => {
              if (ef === 2 && ew === 2) event.name = ev.toString("utf8");
              else if (ef === 3 && ew === 2) {
                const [k, val] = decodeKeyValue(ev);
                if (k) event.attributes[k] = val;
              }
            });
            span.events.push(event);
          } else if (f === 15 && w === 2) {
            walkProto(v, (stf, stw, stv) => {
              if (stf === 2 && stw === 2) span.status.message = stv.toString("utf8");
              else if (stf === 3 && stw === 0) span.status.code = Number(stv);
            });
          }
        });
        spans.push(span);
      }
    }
  });
  return spans;
}

export function decodeOtlpMetrics(buffer) {
  const metrics = [];
  walkProto(buffer, (field, wire, resourceMetrics) => {
    if (field !== 1 || wire !== 2) return;
    let resourceAttributes = {};
    const scopeMetricsList = [];
    walkProto(resourceMetrics, (rf, rw, rv) => {
      if (rf === 1 && rw === 2) resourceAttributes = decodeResource(rv);
      else if (rf === 2 && rw === 2) scopeMetricsList.push(rv);
    });
    const serviceName = resourceAttributes["service.name"] ?? "";
    for (const scopeMetrics of scopeMetricsList) {
      let scope = "";
      const rawMetrics = [];
      walkProto(scopeMetrics, (sf, sw, sv) => {
        if (sf === 1 && sw === 2) scope = decodeScope(sv);
        else if (sf === 2 && sw === 2) rawMetrics.push(sv);
      });
      for (const rawMetric of rawMetrics) {
        const metric = { serviceName, scope, name: "", dataPoints: [] };
        const decodeNumberPoints = (container) => {
          walkProto(container, (df, dw, dv) => {
            if (df !== 1 || dw !== 2) return;
            const point = { attributes: {}, value: 0 };
            walkProto(dv, (pf, pw, pv) => {
              if (pf === 7 && pw === 2) {
                const [k, val] = decodeKeyValue(pv);
                if (k) point.attributes[k] = val;
              } else if (pf === 4 && pw === 1) point.value = pv.readDoubleLE(0);
              else if (pf === 6 && pw === 1) point.value = Number(pv.readBigInt64LE(0));
              else if (pf === 6 && pw === 0) point.value = Number(BigInt.asIntN(64, pv));
            });
            metric.dataPoints.push(point);
          });
        };
        const decodeHistogramPoints = (container) => {
          walkProto(container, (df, dw, dv) => {
            if (df !== 1 || dw !== 2) return;
            const point = { attributes: {}, count: 0 };
            walkProto(dv, (pf, pw, pv) => {
              if (pf === 9 && pw === 2) {
                const [k, val] = decodeKeyValue(pv);
                if (k) point.attributes[k] = val;
              } else if (pf === 4 && pw === 1) point.count = Number(pv.readBigUInt64LE(0));
              else if (pf === 4 && pw === 0) point.count = Number(pv);
            });
            metric.dataPoints.push(point);
          });
        };
        walkProto(rawMetric, (mf, mw, mv) => {
          if (mf === 1 && mw === 2) metric.name = mv.toString("utf8");
          else if ((mf === 5 || mf === 7) && mw === 2) decodeNumberPoints(mv);
          else if (mf === 9 && mw === 2) decodeHistogramPoints(mv);
        });
        metrics.push(metric);
      }
    }
  });
  return metrics;
}

export function decodeOtlpLogs(buffer) {
  const logs = [];
  walkProto(buffer, (field, wire, resourceLogs) => {
    if (field !== 1 || wire !== 2) return;
    let resourceAttributes = {};
    const scopeLogsList = [];
    walkProto(resourceLogs, (rf, rw, rv) => {
      if (rf === 1 && rw === 2) resourceAttributes = decodeResource(rv);
      else if (rf === 2 && rw === 2) scopeLogsList.push(rv);
    });
    const serviceName = resourceAttributes["service.name"] ?? "";
    for (const scopeLogs of scopeLogsList) {
      let scope = "";
      const rawLogs = [];
      walkProto(scopeLogs, (sf, sw, sv) => {
        if (sf === 1 && sw === 2) scope = decodeScope(sv);
        else if (sf === 2 && sw === 2) rawLogs.push(sv);
      });
      for (const rawLog of rawLogs) {
        const record = {
          serviceName,
          scope,
          severityNumber: 0,
          severityText: "",
          body: "",
          traceId: "",
          spanId: "",
          attributes: {},
        };
        walkProto(rawLog, (lf, lw, lv) => {
          if (lf === 2 && lw === 0) record.severityNumber = Number(lv);
          else if (lf === 3 && lw === 2) record.severityText = lv.toString("utf8");
          else if (lf === 5 && lw === 2) record.body = String(decodeAnyValue(lv) ?? "");
          else if (lf === 6 && lw === 2) {
            const [k, val] = decodeKeyValue(lv);
            if (k) record.attributes[k] = val;
          } else if (lf === 9 && lw === 2) record.traceId = lv.toString("hex");
          else if (lf === 10 && lw === 2) record.spanId = lv.toString("hex");
        });
        logs.push(record);
      }
    }
  });
  return logs;
}

export function assessKubernetesTelemetry({
  spans = [],
  metrics = [],
  logs = [],
  expectedTraceIds = [],
  readyTraceIds = [],
  jobs = [],
  pods = [],
}) {
  const failures = [];
  const spanNames = new Set(spans.map(({ name }) => name));
  for (const requiredSpan of [
    "cao_dashboard.ingest.run",
    "cao_dashboard.postgres.query",
    "GET /api/health",
    "GET /api/v1/health",
    "GET /api/readiness",
    "GET /auth/login",
    "GET /api/auth/session",
    "GET /api/repositories",
    "GET /llms.txt",
  ]) {
    if (!spanNames.has(requiredSpan)) failures.push(`missing exported OTel span: ${requiredSpan}`);
  }
  const ingestSpans = spans.filter(({ name }) => name === "cao_dashboard.ingest.run");
  if (!ingestSpans.some((span) =>
    span.status.code === 1 &&
    Number(span.attributes["cao_dashboard.ingest.revision"]) >= 1 &&
    Number(span.attributes["cao_dashboard.ingest.source_count"]) > 0
  )) {
    failures.push("cao_dashboard.ingest.run did not record a healthy published revision span");
  }
  const readySet = new Set(readyTraceIds);
  for (const traceId of expectedTraceIds) {
    if (!spans.some((span) => span.traceId === traceId && span.kind === 2)) {
      failures.push(`missing exported server span for response X-Trace-Id ${traceId}`);
    }
  }
  for (const traceId of readyTraceIds) {
    if (!spans.some((span) =>
      span.traceId === traceId &&
      span.name === "GET /api/readiness" &&
      span.attributes["http.response.status_code"] === 200 &&
      span.status.code !== 2
    )) {
      failures.push(`post-ingestion GET /api/readiness trace ${traceId} did not record HTTP 200 without error`);
    }
  }
  for (const span of spans) {
    if (span.events.some(({ name }) => name === "exception")) {
      failures.push(`unexpected exception event on span ${span.name} (${span.traceId})`);
    }
    if (span.status.code === 2) {
      const expectedPreIngestReadiness = span.name === "GET /api/readiness" &&
        span.attributes["http.response.status_code"] === 503 &&
        !readySet.has(span.traceId);
      if (!expectedPreIngestReadiness) {
        failures.push(`unexpected error span ${span.name} (${span.traceId}): ${span.status.message || "STATUS_CODE_ERROR"}`);
      }
    }
  }

  const metricNames = new Set(metrics.map(({ name }) => name));
  for (const requiredMetric of [
    "http.server.request.duration",
    "db.client.operation.duration",
    "go.memory.allocated",
    "go.goroutine.count",
  ]) {
    if (!metricNames.has(requiredMetric)) failures.push(`missing exported OTel metric: ${requiredMetric}`);
  }
  for (const metric of metrics.filter(({ name }) => name === "db.client.operation.duration")) {
    for (const point of metric.dataPoints) {
      if (point.attributes["error.type"]) {
        failures.push(`database operation metric recorded error.type=${point.attributes["error.type"]}`);
      }
    }
  }

  const logScopes = new Set(logs.map(({ scope }) => scope));
  for (const requiredScope of ["cao:ingest", "cao:server"]) {
    if (!logScopes.has(requiredScope)) failures.push(`missing exported OTel log scope: ${requiredScope}`);
  }
  if (!logs.some((record) => record.scope === "cao:ingest" && /published native revision=\d+ collections=\d+/.test(record.body))) {
    failures.push("missing cao:ingest publication log record");
  }
  for (const record of logs) {
    if (record.severityNumber >= 17) {
      failures.push(`unexpected error log in ${record.scope}: ${record.body}`);
    }
  }

  if (!jobs.some((job) => job.metadata?.namespace === "cao-local-system")) {
    failures.push("missing cao-local-system background Jobs");
  }
  if (!jobs.some((job) => job.metadata?.namespace === "cao-local" && job.metadata?.labels?.["cao-profile"] === "snapshot")) {
    failures.push("missing cao-local snapshot ingestion Job");
  }
  for (const job of jobs) {
    const name = `${job.metadata?.namespace ?? "default"}/${job.metadata?.name ?? "unknown"}`;
    const conditions = job.status?.conditions ?? [];
    if (conditions.some(({ type, status }) => type === "Failed" && status === "True") || (job.status?.failed ?? 0) > 0) {
      failures.push(`background Job failed: ${name}`);
    }
    if (!conditions.some(({ type, status }) => type === "Complete" && status === "True")) {
      failures.push(`background Job did not complete: ${name}`);
    }
  }
  for (const pod of pods) {
    const name = `${pod.metadata?.namespace ?? "default"}/${pod.metadata?.name ?? "unknown"}`;
    if (!["Running", "Succeeded"].includes(pod.status?.phase)) {
      failures.push(`unexpected Pod phase for ${name}: ${pod.status?.phase}`);
    }
    for (const status of [...(pod.status?.initContainerStatuses ?? []), ...(pod.status?.containerStatuses ?? [])]) {
      if ((status.restartCount ?? 0) > 0) {
        failures.push(`container ${name}:${status.name} restarted ${status.restartCount} time(s)`);
      }
      const waiting = status.state?.waiting?.reason;
      if (waiting && ["CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError", "CreateContainerError"].includes(waiting)) {
        failures.push(`container ${name}:${status.name} waiting in ${waiting}`);
      }
    }
  }
  return { passed: failures.length === 0, failures };
}

export function assessAspireTelemetry({
  resources = [],
  errorSpans = [],
  errorLogs = [],
  clusterName = process.env.CAO_LOCAL_KIND_CLUSTER || "cao-local",
}) {
  const failures = [];
  const byName = new Map(resources.map((resource) => [
    resource.displayName ?? resource.DisplayName ?? resource.name ?? resource.Name,
    resource,
  ]));
  for (const required of [clusterName, "build", "database"]) {
    if (!byName.has(required)) failures.push(`missing Aspire resource: ${required}`);
  }
  for (const [name, resource] of byName.entries()) {
    const state = resource.state ?? resource.State ?? "";
    const exitCode = resource.exitCode ?? resource.ExitCode;
    const exception = resource.exceptionMessage ?? resource.ExceptionMessage;
    if (exception) failures.push(`Aspire resource ${name} reported exception: ${exception}`);
    if (name === clusterName) {
      if (state !== "Running") failures.push(`Aspire cluster resource ${name} is ${state}, want Running`);
    } else if (["prepare", "build", "database"].includes(name)) {
      if (state !== "Finished" || (exitCode !== undefined && exitCode !== null && exitCode !== 0)) {
        failures.push(`Aspire step ${name} finished with state=${state} exitCode=${exitCode}`);
      }
    }
  }
  const spanCount = Array.isArray(errorSpans)
    ? errorSpans.length
    : (errorSpans.returnedCount ?? errorSpans.totalCount ??
      (errorSpans.data?.resourceSpans ?? []).reduce((sum, rs) =>
        sum + (rs.scopeSpans ?? []).reduce((inner, ss) => inner + (ss.spans ?? []).length, 0), 0));
  if (spanCount > 0) failures.push(`Aspire OTel reported ${spanCount} error span(s)`);
  const logCount = Array.isArray(errorLogs)
    ? errorLogs.length
    : (errorLogs.returnedCount ?? errorLogs.totalCount ??
      (errorLogs.data?.resourceLogs ?? []).reduce((sum, rl) =>
        sum + (rl.scopeLogs ?? []).reduce((inner, sl) => inner + (sl.logRecords ?? []).length, 0), 0));
  if (logCount > 0) failures.push(`Aspire OTel reported ${logCount} error log record(s)`);
  return { passed: failures.length === 0, failures };
}

async function startOtlpCollector(cluster) {
  const spans = [];
  const metrics = [];
  const logs = [];
  const server = http.createServer((request, response) => {
    if (request.method === "GET" && request.url === "/healthz") {
      response.writeHead(200, { "Content-Type": "text/plain" });
      response.end("ok");
      return;
    }
    const chunks = [];
    request.on("data", (chunk) => chunks.push(chunk));
    request.on("end", () => {
      try {
        let body = Buffer.concat(chunks);
        if (request.headers["content-encoding"] === "gzip") body = gunzipSync(body);
        if (request.method === "POST" && request.url === "/v1/traces") {
          spans.push(...decodeOtlpTraces(body));
        } else if (request.method === "POST" && request.url === "/v1/metrics") {
          metrics.push(...decodeOtlpMetrics(body));
        } else if (request.method === "POST" && request.url === "/v1/logs") {
          logs.push(...decodeOtlpLogs(body));
        } else {
          response.writeHead(404);
          response.end();
          return;
        }
        response.writeHead(200, { "Content-Type": "application/x-protobuf" });
        response.end();
      } catch {
        response.writeHead(400);
        response.end();
      }
    });
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "0.0.0.0", resolve);
  });
  const { port } = server.address();
  const node = `${cluster}-control-plane`;
  const candidates = [];
  try {
    const { stdout } = await execute("docker", ["exec", node, "getent", "ahostsv4", "host.docker.internal"], {
      cwd: root, timeout: 10_000,
    });
    for (const line of stdout.trim().split(/\r?\n/)) {
      const ip = line.trim().split(/\s+/)[0];
      if (ip && !candidates.includes(ip)) candidates.push(ip);
    }
  } catch {
    // Linux kind nodes without Docker Desktop do not define host.docker.internal.
  }
  const { stdout: gateways } = await execute("docker", [
    "container", "inspect", node, "--format", "{{range .NetworkSettings.Networks}}{{.Gateway}}\n{{end}}",
  ], { cwd: root, timeout: 10_000 });
  for (const line of gateways.trim().split(/\r?\n/)) {
    const ip = line.trim();
    if (ip && !candidates.includes(ip)) candidates.push(ip);
  }
  let endpoint = "";
  for (const host of candidates) {
    const url = `http://${host}:${port}`;
    try {
      await execute("docker", ["exec", node, "curl", "-fsS", "--max-time", "2", `${url}/healthz`], {
        cwd: root, timeout: 10_000,
      });
      endpoint = url;
      break;
    } catch {
      // Try the next candidate gateway.
    }
  }
  if (!endpoint) {
    await new Promise((resolve) => server.close(() => resolve()));
    assert.fail(`Kind node ${node} could not reach OTLP collector on port ${port} via [${candidates.join(", ")}]`);
  }
  return {
    endpoint,
    spans,
    metrics,
    logs,
    close: () => new Promise((resolve) => server.close(() => resolve())),
  };
}

if (process.argv.some((arg) => arg.replaceAll("\\", "/").endsWith("tests/integration/kubernetes-kind.test.mjs"))) {
  test("kind runs maintenance, snapshot ingestion, hosted HTTPS and OTel failure checks", {
    skip: process.env.CAO_KUBERNETES_INTEGRATION !== "1",
    timeout: 540_000,
  }, async (t) => {
    const cluster = process.env.CAO_LOCAL_KIND_CLUSTER;
    assert.match(cluster ?? "", /^[a-z0-9]([-a-z0-9]{0,30}[a-z0-9])?$/, "Select an explicit disposable kind cluster");
    const kubectl = (...args) => execute("kubectl", ["--context", `kind-${cluster}`, ...args], {
      cwd: root, timeout: 30_000,
    });
    for (const namespace of ["cao-local", "cao-local-system"]) {
      const { stdout } = await kubectl("get", "namespace", namespace, "-o", "json");
      assert.equal(JSON.parse(stdout).metadata.labels["app.kubernetes.io/part-of"], "cao-local");
    }
    const existing = await kubectl("-n", "cao-local", "get", "deployment", "cao-web", "--ignore-not-found", "-o", "name");
    assert.equal(existing.stdout.trim(), "", "Refusing to replace an existing web deployment");

    const otel = await startOtlpCollector(cluster);
    t.after(() => otel.close());
    const databaseEnvPath = join(root, ".tmp/cao-kubernetes-local/database.env");
    const originalDatabaseEnv = await readFile(databaseEnvPath, "utf8");
    const applyDatabaseConfig = async (content) => {
      await writeFile(databaseEnvPath, content, { mode: 0o600 });
      for (const namespace of ["cao-local-system", "cao-local"]) {
        const { stdout } = await kubectl("-n", namespace, "create", "configmap", "cao-database",
          `--from-env-file=${databaseEnvPath}`, "--dry-run=client", "-o", "json");
        const child = spawn("kubectl", ["--context", `kind-${cluster}`, "-n", namespace, "apply", "-f", "-"], {
          cwd: root, stdio: ["pipe", "ignore", "pipe"],
        });
        child.stdin.end(stdout);
        const code = await new Promise((resolve, reject) => {
          child.once("error", reject);
          child.once("close", resolve);
        });
        assert.equal(code, 0);
      }
    };
    await applyDatabaseConfig([
      originalDatabaseEnv.trimEnd(),
      "OTEL_SDK_DISABLED=false",
      `OTEL_EXPORTER_OTLP_ENDPOINT=${otel.endpoint}`,
      "OTEL_BSP_SCHEDULE_DELAY=200",
      "CAO_OTEL_LOGS_ENABLED=true",
      "DEBUG=cao:cli,cao:ingest,cao:server,cao:telemetry,cao:doctor",
      "",
    ].join("\n"));
    t.after(() => applyDatabaseConfig(originalDatabaseEnv));

    const { stdout: job } = await kubectl("-n", "cao-local-system", "create", "job",
      "--from=cronjob/cao-canonical-maintenance", `cao-maintenance-test-${Date.now()}`, "-o", "name");
    t.after(() => kubectl("-n", "cao-local-system", "delete", job.trim(), "--wait=false"));
    let completed = false;
    for (let attempt = 0; attempt < 120; attempt++) {
      const { stdout } = await kubectl("-n", "cao-local-system", "get", job.trim(), "-o", "json");
      const conditions = JSON.parse(stdout).status?.conditions ?? [];
      assert.ok(!conditions.some(({ type, status }) => type === "Failed" && status === "True"), "Maintenance Job failed");
      if (conditions.some(({ type, status }) => type === "Complete" && status === "True")) {
        completed = true;
        break;
      }
      await delay(1000);
    }
    assert.ok(completed, "Maintenance Job timed out");

    const directory = await mkdtemp(join(root, ".tmp/kubernetes-smoke-"));
    t.after(() => rm(directory, { recursive: true, force: true }));
    const oauth = join(directory, "oauth.env");
    await writeFile(oauth, [
      "CAO_GITHUB_CLIENT_ID=kubernetes-test-only",
      "CAO_GITHUB_CLIENT_SECRET=kubernetes-test-only",
      "CAO_GITHUB_ALLOWED_USERS=kubernetes-test-only",
      "CAO_GITHUB_ADMIN_USERS=kubernetes-test-only",
      "",
    ].join("\n"), { mode: 0o600 });

    // With no canonical snapshot, serve must time out its rollout, not claim readiness.
    t.after(() => kubectl("-n", "cao-local", "delete", "deployment/cao-web", "service/cao-web",
      "--ignore-not-found", "--wait=false"));
    await assert.rejects(execute("bash", ["server/kubernetes/local.sh", "serve", oauth], {
      cwd: root, env: process.env, timeout: 240_000,
    }), (error) => {
      assert.equal(error.code, 1, error.stderr);
      assert.match(error.stderr, /timed out waiting for the condition/);
      return true;
    });

    const forwarding = spawn("kubectl", ["--context", `kind-${cluster}`, "-n", "cao-local",
      "port-forward", "--address", "127.0.0.1", "deployment/cao-web", ":8443", "--pod-running-timeout=120s"], {
      cwd: root, stdio: ["ignore", "pipe", "pipe"],
    });
    let forwardLog = "";
    forwarding.stdout.on("data", (chunk) => { forwardLog += chunk; });
    forwarding.stderr.on("data", (chunk) => { forwardLog += chunk; });
    const closed = new Promise((resolve, reject) => {
      forwarding.once("error", reject);
      forwarding.once("close", resolve);
    });
    const stopForwarding = async () => {
      if (forwarding.exitCode === null && forwarding.signalCode === null) forwarding.kill();
      await closed;
    };
    t.after(stopForwarding);
    let port;
    for (let attempt = 0; attempt < 120; attempt++) {
      port = /Forwarding from 127\.0\.0\.1:(\d+) -> 8443/.exec(forwardLog)?.[1];
      if (port) break;
      assert.equal(forwarding.exitCode, null, forwardLog);
      await delay(1000);
    }
    assert.ok(port, `Port-forward did not start: ${forwardLog}`);
    const ca = await readFile(join(root, ".tmp/cao-kubernetes-local/ca.crt"));
    const expectedTraceIds = [];
    const readyTraceIds = [];
    const get = (path, { readyTrace = false } = {}) => new Promise((resolve, reject) => {
      const request = https.get({
        hostname: "localhost", family: 4, port, path, ca,
        headers: { Host: "localhost:8443" }, timeout: 10_000,
      }, (response) => {
        let body = "";
        response.setEncoding("utf8");
        response.on("data", (chunk) => { body += chunk; });
        response.on("end", () => {
          const traceId = response.headers["x-trace-id"];
          const spanId = response.headers["x-span-id"];
          assert.match(String(traceId ?? ""), /^[0-9a-f]{32}$/, `Missing X-Trace-Id on ${path}`);
          assert.match(String(spanId ?? ""), /^[0-9a-f]{16}$/, `Missing X-Span-Id on ${path}`);
          expectedTraceIds.push(traceId);
          if (readyTrace) readyTraceIds.push(traceId);
          resolve({ status: response.statusCode, headers: response.headers, body });
        });
        response.on("error", reject);
      });
      request.on("timeout", () => request.destroy(new Error("HTTPS request timed out")));
      request.on("error", reject);
    });

    const health = await get("/api/health");
    assert.equal(health.status, 200, health.body);
    assert.equal(JSON.parse(health.body).status, "healthy");
    const readiness = await get("/api/readiness");
    assert.equal(readiness.status, 503, readiness.body);
    const unavailable = JSON.parse(readiness.body);
    assert.equal(unavailable.ready, false);
    assert.equal(unavailable.operational.connected, true);
    assert.equal(unavailable.data.available, false);
    assert.equal(unavailable.data.rebuildRequired, true);
    const login = await get("/auth/login");
    assert.equal(login.status, 302, login.body);
    const redirect = new URL(login.headers.location);
    assert.equal(redirect.origin, "https://github.com");
    assert.equal(redirect.pathname, "/login/oauth/authorize");
    assert.equal(redirect.searchParams.get("redirect_uri"), "https://localhost:8443/auth/callback");
    assert.ok(login.headers["set-cookie"].some((cookie) => /cao_oauth_state=/.test(cookie) && /Secure/.test(cookie) && /HttpOnly/.test(cookie)));
    const denied = await get("/api/auth/session");
    assert.equal(denied.status, 401, denied.body);
    const { stdout } = await kubectl("-n", "cao-local", "get", "pods", "-l", "app=cao-web", "-o", "json");
    const pods = JSON.parse(stdout).items;
    assert.equal(pods.length, 1);
    assert.equal(pods[0].status.phase, "Running");
    assert.equal(pods[0].status.containerStatuses[0].ready, false);
    assert.equal(pods[0].status.containerStatuses[0].restartCount, 0);

    // Ingest a deterministic, hash-verified snapshot fixture and verify readiness transitions to 200.
    const snapshot = join(directory, "snapshot");
    prepareDashboardServerFixture(snapshot, Date.now() + 12 * 3_600_000);
    t.after(() => kubectl("-n", "cao-local", "delete", "jobs", "-l", "cao-profile=snapshot", "--wait=false"));
    t.after(() => kubectl("-n", "cao-local-system", "exec", "statefulset/cao-postgres", "--", "sh", "-ec",
      'PGPASSWORD="$POSTGRES_PASSWORD" psql -U postgres -d cao -v ON_ERROR_STOP=1 -c "TRUNCATE cao.cao_state, cao.daily_run_count_state, cao.daily_run_counts, cao.run_count_contributions CASCADE;"'));
    const { stdout: ingestStdout } = await execute("bash", ["server/kubernetes/local.sh", "ingest", snapshot], {
      cwd: root, env: process.env, timeout: 240_000,
    });
    const ingestLine = ingestStdout.trim().split(/\r?\n/).findLast((line) => line.trim().startsWith("{"));
    assert.ok(ingestLine, `Expected JSON ingestion result, got: ${ingestStdout}`);
    const ingested = JSON.parse(ingestLine);
    assert.ok(ingested.revision >= 1);
    assert.match(ingested.dataRevision, /^sha256:[0-9a-f]{64}$/);
    assert.ok(ingested.evaluatedAt);
    for (const collection of ["$repositories", "$workflows", "$runs", "$domains", "$tools", "$audits", "$issues"]) {
      assert.equal(ingested.counts[collection], 1, `Expected 1 record in ${collection}`);
    }

    await execute("bash", ["server/kubernetes/local.sh", "serve", oauth], {
      cwd: root, env: process.env, timeout: 240_000,
    });

    const populatedReadiness = await get("/api/readiness", { readyTrace: true });
    assert.equal(populatedReadiness.status, 200, populatedReadiness.body);
    const readyBody = JSON.parse(populatedReadiness.body);
    assert.equal(readyBody.ready, true);
    assert.equal(readyBody.operational.connected, true);
    assert.equal(readyBody.data.available, true);
    assert.equal(readyBody.data.rebuildRequired, false);
    assert.equal(readyBody.data.evaluatedAt, ingested.evaluatedAt);

    for (const endpoint of ["/api/health", "/api/v1/health"]) {
      const response = await get(endpoint);
      assert.equal(response.status, 200, response.body);
      const body = JSON.parse(response.body);
      assert.equal(body.status, "healthy");
      assert.equal(body.operational.connected, true);
      assert.equal(body.data.available, true);
      assert.equal(body.data.rebuildRequired, false);
      assert.equal(body.data.evaluatedAt, ingested.evaluatedAt);
      assert.equal(body.counts, undefined);
    }
    const llms = await get("/llms.txt");
    assert.equal(llms.status, 200, llms.body);
    assert.match(llms.body, /^# Central Agentic Ops dashboard server/m);
    assert.match(llms.body, /^Server commit SHA: (?:[0-9a-f]{40}|unknown)$/m);
    const repositoriesDenied = await get("/api/repositories");
    assert.equal(repositoriesDenied.status, 401, repositoriesDenied.body);

    const { stdout: readyPodsStdout } = await kubectl("-n", "cao-local", "get", "pods", "-l", "app=cao-web", "-o", "json");
    const readyPods = JSON.parse(readyPodsStdout).items;
    assert.equal(readyPods.length, 1);
    assert.equal(readyPods[0].status.phase, "Running");
    assert.equal(readyPods[0].status.containerStatuses[0].ready, true);
    assert.equal(readyPods[0].status.containerStatuses[0].restartCount, 0);

    const { stdout: doctorStdout } = await kubectl("-n", "cao-local", "exec", "deployment/cao-web", "--",
      "/app/cao-dashboard", "doctor", "--format", "json", "--deep", "--database-queries", "/app/queries/database.json");
    const doctorReport = JSON.parse(doctorStdout);
    assert.equal(doctorReport.summary.fail, 0, doctorStdout);
    assert.equal(doctorReport.summary.status, "pass", doctorStdout);
    for (const checkId of ["data.active", "data.schema", "data.integrity", "data.sources", "query.definitions", "query.reads"]) {
      const check = doctorReport.checks.find(({ id }) => id === checkId);
      assert.equal(check?.status, "pass", `Doctor check ${checkId} did not pass: ${JSON.stringify(check)}`);
    }

    const readCollections = ["repositories", "workflows", "runs", "domains", "tools", "audits", "issues"];
    const readChild = spawn("kubectl", ["--context", `kind-${cluster}`, "-n", "cao-local", "exec", "-i",
      "deployment/cao-web", "--", "/app/cao-dashboard", "read"], {
      cwd: root, stdio: ["pipe", "pipe", "pipe"],
    });
    let readStdout = "";
    let readStderr = "";
    readChild.stdout.on("data", (chunk) => { readStdout += chunk; });
    readChild.stderr.on("data", (chunk) => { readStderr += chunk; });
    readChild.stdin.end(readCollections.map((collection) => JSON.stringify({ kind: "collection", collection }) + "\n").join(""));
    const readExit = await new Promise((resolve, reject) => {
      readChild.once("error", reject);
      readChild.once("close", resolve);
    });
    assert.equal(readExit, 0, readStderr);
    const readResults = readStdout.trim().split(/\r?\n/).map((line) => JSON.parse(line));
    assert.equal(readResults.length, readCollections.length);
    const byCollection = Object.fromEntries(readCollections.map((name, index) => [name, readResults[index]]));
    assert.equal(byCollection.repositories.rows[0]?.id, "repository:githubnext/gh-cao");
    assert.equal(byCollection.workflows.rows[0]?.id, "workflow:githubnext/gh-cao:dashboard");
    assert.equal(byCollection.runs.rows[0]?.id, "run:424242");
    assert.equal(byCollection.domains.rows[0]?.id, "domain:424242:api.github.com");
    assert.equal(byCollection.tools.rows[0]?.id, "tool:424242:search-issues");
    assert.equal(byCollection.audits.rows[0]?.id, "audit:424242:complete");
    assert.equal(byCollection.issues.rows[0]?.id, "issue:githubnext/gh-cao:42");

    // Collect cluster Jobs and Pods before terminating cao-web so cao-web shuts down cleanly and flushes OTLP telemetry.
    const [systemJobs, localJobs, systemPods, localPods] = await Promise.all([
      kubectl("-n", "cao-local-system", "get", "jobs", "-o", "json"),
      kubectl("-n", "cao-local", "get", "jobs", "-o", "json"),
      kubectl("-n", "cao-local-system", "get", "pods", "-o", "json"),
      kubectl("-n", "cao-local", "get", "pods", "-o", "json"),
    ]);
    await stopForwarding();
    await kubectl("-n", "cao-local", "delete", "deployment/cao-web", "service/cao-web",
      "--ignore-not-found", "--wait=true");

    const assessment = assessKubernetesTelemetry({
      spans: otel.spans,
      metrics: otel.metrics,
      logs: otel.logs,
      expectedTraceIds,
      readyTraceIds,
      jobs: [...JSON.parse(systemJobs.stdout).items, ...JSON.parse(localJobs.stdout).items],
      pods: [...JSON.parse(systemPods.stdout).items, ...JSON.parse(localPods.stdout).items],
    });
    assert.deepEqual(assessment.failures, []);
    assert.equal(assessment.passed, true);
  });
}
