import "@typespec/http";

using TypeSpec.Http;
namespace Cao;

/** gh-aw v2 diagnostic metadata, never authorization or credential-selection evidence. */
model BrokerJobContext {
  /** Original logical GITHUB_JOB value, not percent-encoded. May be omitted when unavailable. */
  job_id?: string;
}

/** GitHub Actions OIDC exchange for organization-billed Copilot inference. Additional and duplicate members are rejected. */
model CopilotRequestExchange {
  ...Record<never>;
  ...BrokerJobContext;
  token_type: "copilot-request";
  provider: "github";
  /** Signed GitHub Actions identity with the exact public exchange URL as its string audience. Never a GitHub API token. */
  @minLength(1) oidc_token: string;
}

/** A known but unsupported capability; identity and enrollment are verified before returning 501. */
model UnsupportedCopilotTaskExchange {
  ...Record<never>;
  ...BrokerJobContext;
  token_type: "copilot-task";
  @minLength(1) oidc_token: string;
}

/** A known but unsupported capability; no GitHub tools or safe-output credential is issued. */
model UnsupportedGitHubAppExchange {
  ...Record<never>;
  ...BrokerJobContext;
  token_type: "github-app";
  @minLength(1) oidc_token: string;
}

/** Cleanup obtains a fresh identity for the same verified job; it never includes an issued credential. */
model TokenBrokerInvalidation {
  ...Record<never>;
  ...BrokerJobContext;
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
  /** Optional, request-only organization GitHub App broker accepting gh-aw v2.0.0 exchange envelopes. Only copilot-request is supported; copilot-task and github-app return 501 after identity and enrollment checks. Authentication is exclusively the oidc_token in the JSON body, not OAuth, CSRF, a bearer header, or admission. The exact configured HTTPS endpoint is the OIDC audience. Enrolled immutable owner/repository IDs, workflow reference and SHA, trusted event/ref/type, signed github-hosted runner and signed job identity must match before issuance. Diagnostic job_id never selects credentials or grants authority. App private keys are read from Azure Key Vault using managed identity. All responses are no-store with empty failure bodies. This initial single-process profile does not claim integrated-deployment conformance; actual Copilot use and renewal require operator acceptance. */
  @route("/api/token-broker")
  @post op exchange(
    @header("content-type") contentType: "application/json",
    @body body: CopilotRequestExchange | UnsupportedCopilotTaskExchange | UnsupportedGitHubAppExchange | TokenBrokerInvalidation
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
