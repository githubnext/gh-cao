import "@typespec/http";

using TypeSpec.Http;
namespace Cao;

model DataState {
  available: boolean;
  rebuildRequired: boolean;
  /** Authoritative source time of the active canonical dataset, when available. */
  evaluatedAt?: utcDateTime;
}
model OperationalCapability {
  scope: "unsupported" | "process" | "deployment";
  persistence: "volatile" | "restart";
}
model OperationalCapabilities {
  cache: OperationalCapability;
  requestLimits: OperationalCapability;
  sessions: OperationalCapability;
  revocations: OperationalCapability;
  githubQuota: OperationalCapability;
  collection: OperationalCapability;
  coordination: OperationalCapability;
  diagnostics: OperationalCapability;
}
model OperationalState {
  connected: boolean;
  /** False until a volatile collector has reconstructed authoritative GitHub scope. */
  recoveryReady: boolean;
  capabilities: OperationalCapabilities;
}
@example(#{
  status: "healthy",
  data: #{
    available: true,
    rebuildRequired: false,
    evaluatedAt: utcDateTime.fromISO("2026-10-02T14:25:38.957Z")
  }
})
model HealthResponse {
  status: "healthy" | "unhealthy";
  operational?: OperationalState;
  data: DataState;
  /** Details are present only for an authorized caller. */
  revision?: int64;
  counts?: Record<int32>;
  sourceCount?: int32;
  rowCount?: int32;
}
model ReadinessResponse {
  ready: boolean;
  operational?: OperationalState;
  data: DataState;
}
model UnhealthyHealth {
  @statusCode code: 503;
  @body body: HealthResponse;
}
model NotReady {
  @statusCode code: 503;
  @body body: ReadinessResponse;
}

@tag("Health")
namespace Health {
  /** Public liveness and storage health, including before the first data ingestion. Hosted requests are limited per client address. */
  @route("/api/health")
  /** An externally hosted handler also returns an Unavailable error until startup completes or after shutdown. */
  @get op health(): HealthResponse | UnhealthyHealth | TooManyRequests | Unavailable;

  /** Alias of /api/health. */
  @route("/api/v1/health")
  @get op healthV1(): HealthResponse | UnhealthyHealth | TooManyRequests | Unavailable;

  /** Public readiness; the selected operational store must be connected, volatile scope recovery must be complete, and Postgres must contain dashboard data. Hosted requests are limited per client address. */
  @route("/api/readiness")
  @get op readiness(): ReadinessResponse | NotReady | TooManyRequests | Unavailable;
}
