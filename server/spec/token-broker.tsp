import "@typespec/http";

using TypeSpec.Http;
namespace Cao;

/** GitHub Actions OIDC exchange for organization-billed Copilot inference. Additional and duplicate members are rejected. */
model CopilotRequestExchange {
  ...Record<never>;
  token_type: "copilot-request";
  provider: "github";
  /** Signed GitHub Actions identity with the exact public exchange URL as its string audience. Never a GitHub API token. */
  @minLength(1) oidc_token: string;
}

/** A known but unsupported capability; identity and enrollment are verified before returning 501. */
model UnsupportedCopilotTaskExchange {
  ...Record<never>;
  token_type: "copilot-task";
  @minLength(1) oidc_token: string;
}

/** Cleanup obtains a fresh identity for the same verified job; it never includes an issued credential. */
model TokenBrokerInvalidation {
  ...Record<never>;
  operation: "invalidate";
  @minLength(1) oidc_token: string;
}

/** Only the credential and its actual upstream expiry are returned; no provider endpoint or other authority is conveyed. */
model BrokerCredential {
  ...Record<never>;
  @minLength(1) token: string;
  expires_at: utcDateTime;
}

@tag("Token broker")
namespace TokenBroker {
  /** Optional, request-only organization GitHub App broker implementing the gh-aw exchange shape. Authentication is exclusively the oidc_token in the JSON body, not OAuth, CSRF, a bearer header, or admission. The exact configured HTTPS endpoint is the OIDC audience. Enrolled immutable owner/repository IDs, workflow reference and SHA, trusted event/ref and signed job identity must match before issuance. App private keys are read from Azure Key Vault using managed identity. All responses are no-store with empty failure bodies. This initial single-process profile does not claim integrated-deployment conformance; actual Copilot use and renewal require operator acceptance. */
  @route("/api/token-broker")
  @post op exchange(
    @header("content-type") contentType: "application/json",
    @body body: CopilotRequestExchange | UnsupportedCopilotTaskExchange | TokenBrokerInvalidation
  ):
    { @statusCode code: 200; @header("cache-control") cacheControl: "no-store"; @body body: BrokerCredential }
    | { @statusCode code: 204; @header("cache-control") cacheControl: "no-store" }
    | { @statusCode code: 400; @header("cache-control") cacheControl: "no-store" }
    | { @statusCode code: 401; @header("cache-control") cacheControl: "no-store" }
    | { @statusCode code: 403; @header("cache-control") cacheControl: "no-store" }
    | { @statusCode code: 413; @header("cache-control") cacheControl: "no-store" }
    | { @statusCode code: 415; @header("cache-control") cacheControl: "no-store" }
    | { @statusCode code: 422; @header("cache-control") cacheControl: "no-store" }
    | { @statusCode code: 429; @header("cache-control") cacheControl: "no-store" }
    | { @statusCode code: 501; @header("cache-control") cacheControl: "no-store" }
    | { @statusCode code: 503; @header("cache-control") cacheControl: "no-store" };
}
