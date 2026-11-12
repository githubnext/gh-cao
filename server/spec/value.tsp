import "@typespec/http";
import "@typespec/json-schema";

using TypeSpec.Http;
using TypeSpec.JsonSchema;
namespace Cao;

/** Original bounded facts from a validated operational-value skill report, not HTML or computed dashboard labels. */
model ValueEvidencePacket {
  schemaVersion: 1;
  id: string;
  repository: string;
  workflow: string;
  sourcePath: string;
  observedAt: utcDateTime;
  windowStart: utcDateTime;
  windowEnd: utcDateTime;
  contractDigest: string;
  @maxItems(20000) facts: Record<string | float64 | boolean>[];
  repositoryId?: string;
  workflowId?: string;
  provenance?: unknown[];
}

model ValuePublicationRequest {
  schema_version: 2;
  kind: "operational_value_evidence";
  operational_value_evidence: ValueEvidencePacket;
}

model ValuePublication {
  id: string;
  revision: int64;
  updated: boolean;
  observedAt: utcDateTime;
}

@tag("Operational value")
namespace Value {
  /**
   * Explicitly publish one skill-generated evidence packet (maximum 8 MiB) to
   * PostgreSQL. No workflow discovery, acquisition, evaluator execution or
   * scheduled evaluation is performed. Requires a hosted administrator OAuth
   * session with current step-up assurance and CSRF, or a local bearer
   * capability backed by PostgreSQL.
   *
   * Repository and workflow must already be observed. Publication is atomic,
   * namespace-scoped and survives Activity snapshot replacement. Exact retries
   * do not advance the revision. Older/conflicting captures, changed frozen
   * contracts and rewritten immutable history are rejected. A new publication
   * advances the query revision and notifies active dashboard subscriptions.
   * Evidence capture/cutoff timestamps are retained, never replaced with upload
   * time. All evidence timestamps must be representable at PostgreSQL
   * microsecond precision; finer nonzero fractions are rejected, not rounded.
   * The body is the original single schema-v2 JSONL transport envelope.
   */
  @useAuth(DashboardAuth)
  @route("/api/v1/value")
  @post op publish(
    @header("X-CSRF-Token") csrfToken?: string,
    @header contentType?: "application/json" | "application/x-ndjson",
    @body body: ValuePublicationRequest
  ): ValuePublication | BadRequest | Unauthorized | Forbidden | NotFound |
    Conflict | Failure<413> | Failure<415> | Unavailable;
}
