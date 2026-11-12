import "@typespec/http";

using TypeSpec.Http;
namespace Cao;

@example(#{login: "octocat", avatarUrl: "https://avatars.githubusercontent.com/u/1"})
model Account {
  login: string;
  avatarUrl: string;
}

@tag("Hosted authentication")
namespace Auth {
  /** OAuth profiles only. Ordinary hosted login requests read:org read:user for classic OAuth Apps, or offline_access for GitHub Apps. The explicitly configured CAO_GITHUB_LOCAL_USER single-user development mode requires direct loopback HTTPS, requests read:user without read:org, and replaces organization/team authorization with an exact configured GitHub login. It cannot be selected by a request parameter or used by remote/external/platform hosting. hosted_cli=1 explicitly starts repository consent only when hosted CLI is enabled and the existing server-side session belongs to a configured administrator with a known GitHub account ID. Classic OAuth adds repo only for that flow. Its signed state binds the initiating session, account ID, login and ten-minute expiry; the callback must retain that subject and current administrator authorization. The query parameter alone grants no authority. login is an optional GitHub login hint forwarded to GitHub's account chooser alongside the always-forced prompt=select_account; a repeated or syntactically invalid hint returns 400. GitHub may still return a different identity, so the hint grants no authority. Bounded model: specs/tla/OAuthAccountSelection.tla. */
  @route("/auth/login")
  @get op login(@query select_account?: string, @query hosted_cli?: "1", @query login?: string):
    { @statusCode code: 302; @header location: string } | BadRequest | Unauthorized | Forbidden | Unavailable;

  /** OAuth profiles only. Exchange the GitHub OAuth code and signed state; sets the HttpOnly session and CSRF cookies, then redirects home. Local development authenticates only the exact configured developer login without membership reads; its sessions cannot be reused in ordinary hosted mode or for a different configured developer. An administrator-consent callback requires its original active session, matching GitHub account ID and login, current administrator allowlisting, and verified factor evidence. Mismatched or unauthorized callbacks are rejected and issued credentials are revoked or queued for revocation. Failure pages are HTML. Every callback that resolves a GitHub identity records its account ID, login and access outcome in the encrypted, HttpOnly cao_accounts cookie scoped to /auth/ (at most five entries; it grants no authority). An ordinary sign-in denied for one account when this browser has used another redirects to /auth/select-account instead of the failure page. */
  @route("/auth/callback")
  @get op callback(@query code?: string, @query state?: string):
    { @statusCode code: 302; @header location: string } | BadRequest | Forbidden | Unavailable;

  /** Hosted profile only. A public, static HTML confirmation page. */
  @route("/auth/logged-out")
  @get op loggedOut(): { @header contentType: "text/html; charset=utf-8"; @body body: string };

  /** OAuth profiles only. A public HTML dialog listing GitHub accounts recorded in the cao_accounts cookie, each linking to /auth/login with a login hint, and naming the configured GitHub host. Redirects to an account-selection login when fewer than two accounts are recorded. The logged-out page clears the cookie. */
  @route("/auth/select-account")
  @get op selectAccount(): { @header contentType: "text/html; charset=utf-8"; @body body: string } | { @statusCode code: 302; @header location: string };

  /** Hosted profile only. An authenticated GitHub account (not OAuth tokens). */
  @useAuth(ApiKeyAuth<ApiKeyLocation.cookie, "cao_session">)
  @route("/api/auth/session")
  @get op session(): Account | Unauthorized;

  /** Hosted profile only. Revokes the server-side session and clears cookies. Requires the X-CSRF-Token returned in the cao_csrf cookie. */
  @useAuth(ApiKeyAuth<ApiKeyLocation.cookie, "cao_session">)
  @route("/auth/logout")
  @post op logout(@header("X-CSRF-Token") csrfToken: string):
    { @statusCode code: 204 } | Unauthorized | Forbidden | Unavailable;

  /** Hosted profile only. Revokes the current session and returns the explicit account-selection login path. */
  @useAuth(ApiKeyAuth<ApiKeyLocation.cookie, "cao_session">)
  @route("/auth/switch-account")
  @post op switchAccount(@header("X-CSRF-Token") csrfToken: string):
    { loginUrl: string } | Unauthorized | Forbidden | Unavailable;
}
