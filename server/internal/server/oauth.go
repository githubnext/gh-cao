package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

//go:embed oauth_failure.html
var oauthFailureHTML string

//go:embed oauth_logged_out.html
var oauthLoggedOutHTML string

var oauthFailurePage = template.Must(template.New("oauth-failure").Parse(oauthFailureHTML))

func oauthPageScriptHash(page string) string {
	_, script, found := strings.Cut(page, "<script>")
	if !found {
		panic("OAuth page is missing its script")
	}
	script, _, found = strings.Cut(script, "</script>")
	if !found {
		panic("OAuth page has an unterminated script")
	}
	sum := sha256.Sum256([]byte(script))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func oauthPageStyleHash(page string) string {
	_, style, found := strings.Cut(page, "<style>")
	if !found {
		panic("OAuth page is missing its style")
	}
	style, _, found = strings.Cut(style, "</style>")
	if !found {
		panic("OAuth page has an unterminated style")
	}
	sum := sha256.Sum256([]byte(style))
	return base64.StdEncoding.EncodeToString(sum[:])
}

var oauthFailureScriptHash = oauthPageScriptHash(oauthFailureHTML)
var oauthFailureStyleHash = oauthPageStyleHash(oauthFailureHTML)
var oauthLoggedOutScriptHash = oauthPageScriptHash(oauthLoggedOutHTML)
var oauthLoggedOutStyleHash = oauthPageStyleHash(oauthLoggedOutHTML)

func writeOAuthFailure(response http.ResponseWriter, status int, reason string, traceID trace.TraceID, providerHome, providerHost string) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'sha256-"+oauthFailureStyleHash+"'; script-src 'sha256-"+oauthFailureScriptHash+"'; connect-src 'self'; base-uri 'none'; form-action 'none'")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	id := ""
	if traceID.IsValid() {
		id = traceID.String()
	}
	_ = oauthFailurePage.Execute(response, struct {
		TraceID             string
		AuthorizationFailed bool
		ProviderHome        string
		ProviderHost        string
	}{id, reason == "authorization_failed", providerHome, providerHost})
}

const (
	sessionCookieName            = "cao_session"
	csrfCookieName               = "cao_csrf"
	sessionTTL                   = 30 * 24 * time.Hour
	csrfCookieTTL                = sessionTTL - 24*time.Hour
	tokenRefreshSkew             = 5 * time.Minute
	authorizationRecheckInterval = 5 * time.Minute
)

type GitHubOAuthConfig struct {
	ClientID              string
	ClientSecret          string
	RedirectURL           string
	SessionSecret         string
	PreviousSessionSecret string
	AllowedUsers          []string
	AllowedOrganizations  []string
	AllowedTeams          []string
	// LocalDevelopmentUser replaces membership authorization for one
	// explicitly configured login, only on a validated loopback TLS server.
	LocalDevelopmentUser string
	AuthURL              string
	TokenURL             string
	UserURL              string
	OrgMembershipURL     string
	TeamMembershipURL    string
	RevokeURL            string
	HTTPClient           *http.Client
	RevocationKeyPrefix  string
	// AdminReauthenticationMaxAge bounds how long after an interactive OAuth
	// authorization an administrator may act. Token refresh and periodic
	// revalidation never extend it.
	AdminReauthenticationMaxAge time.Duration
	// SSOOrganizations are organizations the operator attests enforce SAML or
	// OIDC single sign-on. An active membership read with a fresh token proves
	// GitHub accepted that token's SSO authorization.
	SSOOrganizations []string
	loginStateSecret string
}

const (
	defaultAdminReauthenticationMaxAge = 15 * time.Minute
	maxAdminReauthenticationMaxAge     = time.Hour
	assuranceTwoFactor                 = "2fa"
	assuranceSSOPrefix                 = "sso:"
)

type githubOAuth struct {
	config      GitHubOAuthConfig
	client      *http.Client
	sessions    operational.SessionStore
	invalidator operational.SessionInvalidator
	revocations operational.RevocationQueue
	key         []byte
	keys        map[string][]byte
	log         func(context.Context, string)
	// Bound exchanges that might need an in-memory cleanup owner while both
	// GitHub and protected storage are unavailable.
	exchanges chan struct{}
	// Set only when hosted CLI actions are enabled. Rechecked at both ends
	// of explicit repository consent; ordinary login never consults it.
	repositoryConsentAllowed func(oauthSession) bool
}

type oauthSession struct {
	ID                   string    `json:"id"`
	Login                string    `json:"login"`
	GitHubID             int64     `json:"githubId,omitempty"`
	LocalDevelopmentUser string    `json:"localDevelopmentUser,omitempty"`
	AvatarURL            string    `json:"avatarUrl,omitempty"`
	AccessToken          string    `json:"accessToken"`
	RefreshToken         string    `json:"refreshToken"`
	AccessExpires        time.Time `json:"accessExpires"`
	RefreshExpires       time.Time `json:"refreshExpires"`
	// Local access/refresh deadlines above never prove provider expiry.
	// Missing provider deadlines (including legacy records) retain cleanup debt.
	ProviderAccessExpires  time.Time `json:"providerAccessExpires,omitempty"`
	ProviderRefreshExpires time.Time `json:"providerRefreshExpires,omitempty"`
	AuthorizedAt           time.Time `json:"authorizedAt"`
	CSRFToken              string    `json:"csrfToken"`
	// AuthenticatedAt is set only by an interactive authorization-code
	// callback and is the step-up time for administrator operations.
	AuthenticatedAt time.Time `json:"authenticatedAt,omitempty"`
	// Assurance records 2FA enrollment or attested SSO access disclosed at
	// AuthenticatedAt: "2fa" and/or "sso:ORG".
	Assurance []string `json:"assurance,omitempty"`
}

type githubAccount struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
	// TwoFactorAuthentication is present only when GitHub discloses it to the
	// token; absence is treated as unverified.
	TwoFactorAuthentication *bool `json:"two_factor_authentication"`
}

type oauthConsentState struct {
	SessionBinding string `json:"session"`
	GitHubID       int64  `json:"githubId"`
	Login          string `json:"login"`
	ExpiresAt      int64  `json:"expiresAt"`
}

func oauthSessionBinding(id string) string {
	sum := sha256.Sum256([]byte(id))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type tokenResponse struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	TokenType             string `json:"token_type"`
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
}

func providerTokenExpiry(now time.Time, seconds int64) time.Time {
	if seconds <= 0 || seconds > int64((1<<63-1)/time.Second) {
		return time.Time{}
	}
	return now.Add(time.Duration(seconds) * time.Second)
}

func (session *oauthSession) applyTokens(tokens tokenResponse, now time.Time) {
	session.AccessToken = tokens.AccessToken
	session.ProviderAccessExpires = providerTokenExpiry(now, tokens.ExpiresIn)
	session.AccessExpires = session.ProviderAccessExpires
	if session.AccessExpires.IsZero() {
		session.AccessExpires = now.Add(time.Hour)
	}
	if tokens.ExpiresIn < 0 {
		session.AccessExpires = now
	}
	if tokens.RefreshToken != "" {
		if tokens.RefreshToken != session.RefreshToken {
			session.RefreshExpires = now.Add(sessionTTL)
			session.ProviderRefreshExpires = time.Time{}
		}
		session.RefreshToken = tokens.RefreshToken
	}
	if expires := providerTokenExpiry(now, tokens.RefreshTokenExpiresIn); !expires.IsZero() {
		session.RefreshExpires = expires
		session.ProviderRefreshExpires = expires
	}
}

func (config *GitHubOAuthConfig) validate() error {
	if strings.TrimSpace(config.ClientID) == "" {
		return errors.New("GitHub OAuth client ID is required")
	}
	if strings.TrimSpace(config.ClientSecret) == "" {
		return errors.New("GitHub OAuth client secret is required")
	}
	if strings.TrimSpace(config.RedirectURL) == "" {
		return errors.New("GitHub OAuth redirect URL is required")
	}
	if strings.TrimSpace(config.SessionSecret) == "" || len(config.SessionSecret) < 32 {
		return errors.New("session secret must contain at least 32 characters")
	}
	if config.PreviousSessionSecret != "" && len(config.PreviousSessionSecret) < 32 {
		return errors.New("previous session secret must contain at least 32 characters")
	}
	if config.LocalDevelopmentUser != "" {
		if strings.TrimSpace(config.LocalDevelopmentUser) != config.LocalDevelopmentUser ||
			strings.ContainsAny(config.LocalDevelopmentUser, "/ ,\t\r\n*") {
			return errors.New("CAO_GITHUB_LOCAL_USER must be one exact GitHub login")
		}
		if len(config.AllowedUsers) != 0 || len(config.AllowedOrganizations) != 0 || len(config.AllowedTeams) != 0 || len(config.SSOOrganizations) != 0 {
			return errors.New("CAO_GITHUB_LOCAL_USER cannot be combined with user, organization, team, or SSO authorization")
		}
	} else if len(config.AllowedUsers) == 0 && len(config.AllowedOrganizations) == 0 && len(config.AllowedTeams) == 0 {
		return errors.New("GitHub OAuth authorization requires at least one allowed user, organization, or team")
	}
	if config.AdminReauthenticationMaxAge == 0 {
		config.AdminReauthenticationMaxAge = defaultAdminReauthenticationMaxAge
	}
	if config.AdminReauthenticationMaxAge < time.Minute || config.AdminReauthenticationMaxAge > maxAdminReauthenticationMaxAge {
		return errors.New("CAO_ADMIN_REAUTH_MAX_AGE must be a duration from 1m to 1h")
	}
	for _, org := range config.SSOOrganizations {
		if strings.TrimSpace(org) == "" || strings.ContainsAny(org, "/ ") {
			return errors.New("CAO_GITHUB_SSO_ORGANIZATIONS must list organization logins")
		}
	}
	if config.AuthURL == "" {
		config.AuthURL = "https://github.com/login/oauth/authorize"
	}
	if config.TokenURL == "" {
		config.TokenURL = "https://github.com/login/oauth/access_token"
	}
	if config.UserURL == "" {
		config.UserURL = "https://api.github.com/user"
	}
	if config.OrgMembershipURL == "" {
		config.OrgMembershipURL = "https://api.github.com/user/memberships/orgs/{org}"
	}
	if config.TeamMembershipURL == "" {
		config.TeamMembershipURL = "https://api.github.com/orgs/{org}/teams/{team}/memberships/{user}"
	}
	if config.RevokeURL == "" {
		config.RevokeURL = "https://api.github.com/applications/{client_id}/token"
	}
	return nil
}

func newGitHubOAuth(config GitHubOAuthConfig, sessions operational.SessionStore, invalidator operational.SessionInvalidator, revocations operational.RevocationQueue) *githubOAuth {
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	sum := sha256.Sum256([]byte(config.SessionSecret))
	keys := map[string][]byte{sessionKeyID(sum[:]): sum[:]}
	if config.PreviousSessionSecret != "" {
		previous := sha256.Sum256([]byte(config.PreviousSessionSecret))
		keys[sessionKeyID(previous[:])] = previous[:]
	}
	return &githubOAuth{
		config:      config,
		client:      client,
		sessions:    sessions,
		invalidator: invalidator,
		revocations: revocations,
		key:         sum[:],
		keys:        keys,
		exchanges:   make(chan struct{}, 16),
		log: func(ctx context.Context, branch string) {
			serverLog.PrintfContext(ctx, "oauth branch=%s", branch)
		},
	}
}

func (oauth *githubOAuth) logBranch(ctx context.Context, branch string) {
	oauth.emitAuthBranch(ctx, branch)
}

func (oauth *githubOAuth) emitAuthBranch(ctx context.Context, branch string) {
	if oauth.log != nil {
		oauth.log(ctx, branch)
	}
}

func recordOAuthDecision(ctx context.Context, operation, outcome string) {
	attrs := []attribute.KeyValue{
		attribute.String("cao_dashboard.auth.operation", operation),
		attribute.String("cao_dashboard.auth.outcome", outcome),
	}
	trace.SpanFromContext(ctx).AddEvent("cao_dashboard.auth.decision", trace.WithAttributes(attrs...))
	counter, err := otel.Meter("github.com/githubnext/gh-cao/server").Int64Counter(
		"cao_dashboard.auth.decision.count",
		metric.WithUnit("{decision}"),
		metric.WithDescription("OAuth authorization and credential cleanup decisions by fixed operation and outcome"),
	)
	if err == nil {
		counter.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
}

func (oauth *githubOAuth) login(response http.ResponseWriter, request *http.Request) {
	if !navigationRequest(request) {
		oauth.logBranch(request.Context(), "login.embedded_or_subresource_rejected")
		writeError(request.Context(), response, http.StatusUnauthorized, "GitHub sign-in requires a top-level navigation")
		return
	}
	var consent *oauthConsentState
	if values, requested := request.URL.Query()["hosted_cli"]; requested {
		if len(values) != 1 || values[0] != "1" {
			oauth.logBranch(request.Context(), "login.consent_request_invalid")
			writeError(request.Context(), response, http.StatusBadRequest, "Invalid hosted administrator consent request")
			return
		}
		if oauth.repositoryConsentAllowed == nil {
			oauth.logBranch(request.Context(), "login.consent_disabled")
			writeError(request.Context(), response, http.StatusForbidden, "Hosted administrator consent is not enabled")
			return
		}
		session, ok := oauth.session(response, request)
		if !ok {
			oauth.logBranch(request.Context(), "login.consent_session_missing")
			writeError(request.Context(), response, http.StatusUnauthorized, "Sign in before requesting hosted administrator consent")
			return
		}
		if !oauth.repositoryConsentAllowed(session) {
			oauth.logBranch(request.Context(), "login.consent_admin_rejected")
			writeError(request.Context(), response, http.StatusForbidden, "Administrator access is required for repository consent")
			return
		}
		if session.GitHubID <= 0 {
			oauth.logBranch(request.Context(), "login.consent_subject_missing")
			writeError(request.Context(), response, http.StatusUnauthorized, "Sign in again before requesting hosted administrator consent")
			return
		}
		consent = &oauthConsentState{
			SessionBinding: oauthSessionBinding(session.ID), GitHubID: session.GitHubID,
			Login: session.Login, ExpiresAt: time.Now().Add(10 * time.Minute).Unix(),
		}
	}
	loginHint := ""
	if values, requested := request.URL.Query()["login"]; requested {
		if len(values) != 1 || !githubLoginPattern.MatchString(values[0]) {
			oauth.logBranch(request.Context(), "login.login_hint_invalid")
			writeError(request.Context(), response, http.StatusBadRequest, "Invalid GitHub login hint")
			return
		}
		loginHint = values[0]
	}
	state, err := randomToken(32)
	if err != nil {
		oauth.logBranch(request.Context(), "login.state_generation_failed")
		writeError(request.Context(), response, http.StatusInternalServerError, "failed to initialize login")
		return
	}
	cookieState := state
	if consent != nil {
		data, err := json.Marshal(consent)
		if err != nil {
			oauth.logBranch(request.Context(), "login.consent_state_failed")
			writeError(request.Context(), response, http.StatusInternalServerError, "Failed to initialize hosted administrator consent")
			return
		}
		cookieState += "." + base64.RawURLEncoding.EncodeToString(data)
	}
	http.SetCookie(response, &http.Cookie{
		Name:     "cao_oauth_state",
		Value:    oauth.sign(cookieState),
		Path:     "/auth/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	target, _ := url.Parse(oauth.config.AuthURL)
	values := target.Query()
	values.Set("client_id", oauth.config.ClientID)
	values.Set("redirect_uri", oauth.config.RedirectURL)
	values.Set("state", state)
	if strings.HasPrefix(oauth.config.ClientID, "Iv") {
		values.Set("scope", "offline_access")
	} else {
		// GitHub omits two_factor_authentication without read:user (or user).
		scope := "read:org read:user"
		if oauth.config.LocalDevelopmentUser != "" {
			scope = "read:user"
		}
		if consent != nil {
			scope += " repo"
		}
		values.Set("scope", scope)
	}
	values.Set("prompt", "select_account")
	if loginHint != "" {
		values.Set("login", loginHint)
		oauth.logBranch(request.Context(), "login.login_hint_requested")
	}
	oauth.logBranch(request.Context(), "login.account_selection_requested")
	target.RawQuery = values.Encode()
	oauth.logBranch(request.Context(), "login.redirected")
	http.Redirect(response, request, target.String(), http.StatusFound)
}

func (oauth *githubOAuth) loggedOut(response http.ResponseWriter, request *http.Request) {
	oauth.logBranch(request.Context(), "logged_out.rendered")
	oauth.clearSessionCookies(response)
	oauth.clearStateCookie(response)
	oauth.clearAccountHints(response)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'sha256-"+oauthLoggedOutStyleHash+"'; script-src 'sha256-"+oauthLoggedOutScriptHash+"'; base-uri 'none'; form-action 'none'")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(response, oauthLoggedOutHTML)
}

func (oauth *githubOAuth) callback(response http.ResponseWriter, request *http.Request) {
	ctx := telemetry.ExtractTraceparent(request.Context(), request.Header.Get("Traceparent"))
	ctx, span := telemetry.Tracer().Start(ctx, "GET /auth/callback",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("http.route", "/auth/callback"),
			attribute.String("http.request.method", http.MethodGet),
		),
	)
	defer span.End()
	telemetry.SetResponseTraceHeaders(response, span.SpanContext())
	request = request.WithContext(ctx)
	outcome := "success"
	errorType := ""
	defer func() {
		attrs := []attribute.KeyValue{attribute.String("cao_dashboard.auth.callback.outcome", outcome)}
		if errorType != "" {
			attrs = append(attrs, attribute.String("error.type", errorType))
		}
		span.SetAttributes(attrs...)
		counter, err := otel.Meter("github.com/githubnext/gh-cao/server").Int64Counter(
			"cao_dashboard.auth.callback.count",
			metric.WithUnit("{callback}"),
			metric.WithDescription("Completed GitHub OAuth callbacks by outcome and fixed error type"),
		)
		if err == nil {
			counter.Add(ctx, 1, metric.WithAttributes(attrs...))
		}
	}()
	fail := func(status int, reason string) {
		outcome, errorType = "failure", reason
		if status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, "OAuth callback failed")
		}
		home, host := oauth.providerHome()
		writeOAuthFailure(response, status, reason, span.SpanContext().TraceID(), home, host)
	}
	consent, validState := oauth.loginState(request)
	if !validState {
		serverLog.PrintfContext(ctx, "oauth callback rejected invalid state")
		oauth.logBranch(ctx, "callback.state_rejected")
		fail(http.StatusBadRequest, "invalid_state")
		return
	}
	oauth.clearStateCookie(response)
	code := strings.TrimSpace(request.URL.Query().Get("code"))
	if code == "" {
		oauth.logBranch(ctx, "callback.code_missing")
		reason := "missing_code"
		if request.URL.Query().Has("error") {
			reason = "provider_denied"
		}
		fail(http.StatusBadRequest, reason)
		return
	}
	if !oauth.acquireExchange(ctx) {
		fail(http.StatusServiceUnavailable, "credential_cleanup_capacity")
		return
	}
	defer oauth.releaseExchange()
	tokens, err := oauth.exchange(ctx, url.Values{
		"client_id":     {oauth.config.ClientID},
		"client_secret": {oauth.config.ClientSecret},
		"code":          {code},
		"redirect_uri":  {oauth.config.RedirectURL},
	})
	if err != nil {
		serverLog.PrintfContext(ctx, "oauth exchange failed")
		oauth.logBranch(ctx, "callback.exchange_failed")
		fail(http.StatusUnauthorized, "exchange_failed")
		return
	}
	now := time.Now().UTC()
	session := oauthSession{
		AuthorizedAt: now,
	}
	session.applyTokens(tokens, now)
	if session.ProviderAccessExpires.IsZero() {
		oauth.logBranch(ctx, "callback.access_expiry_defaulted")
	} else {
		oauth.logBranch(ctx, "callback.access_expiry_provided")
	}
	if session.ProviderRefreshExpires.IsZero() {
		oauth.logBranch(ctx, "callback.refresh_expiry_defaulted")
	} else {
		oauth.logBranch(ctx, "callback.refresh_expiry_provided")
	}
	retained := false
	defer func() {
		if !retained {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
			revocationErr := oauth.revokeCredentials(cleanup, session)
			cancel()
			if revocationErr != nil {
				queueCtx, queueCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer queueCancel()
				if oauth.queueRevocation(queueCtx, session) != nil {
					oauth.logBranch(ctx, "callback.revocation_queue_failed")
					recordOAuthDecision(context.WithoutCancel(ctx), "callback_cleanup", "queue_failed")
					oauth.retainCredentialCleanup(ctx, session)
				} else {
					oauth.logBranch(ctx, "callback.revocation_queued")
					recordOAuthDecision(context.WithoutCancel(ctx), "callback_cleanup", "queued")
				}
			} else {
				oauth.logBranch(ctx, "callback.credentials_revoked")
				recordOAuthDecision(context.WithoutCancel(ctx), "callback_cleanup", "revoked")
			}
		}
	}()
	account, err := oauth.authorizedAccount(ctx, tokens.AccessToken)
	if err != nil {
		serverLog.PrintfContext(ctx, "oauth authorization failed")
		oauth.logBranch(ctx, "callback.authorization_failed")
		if consent == nil && errors.Is(err, errAccountNotAuthorized) {
			// Users signed in to several GitHub accounts (for example a
			// personal and an Enterprise Managed User account) often reach
			// GitHub's consent with the wrong one. Offer explicit selection.
			if hints := oauth.rememberAccount(response, request, account, false); hasOtherAccount(hints, account.ID) {
				outcome, errorType = "failure", "authorization_failed"
				oauth.logBranch(ctx, "callback.account_selection_redirected")
				http.Redirect(response, request, "/auth/select-account", http.StatusFound)
				return
			}
		}
		fail(http.StatusForbidden, "authorization_failed")
		return
	}
	session.Login = account.Login
	session.GitHubID = account.ID
	session.LocalDevelopmentUser = oauth.config.LocalDevelopmentUser
	session.AuthenticatedAt = now
	session.Assurance = oauth.verifiedAssurance(ctx, tokens.AccessToken, account)
	if consent != nil {
		previous, ok := oauth.loadRequestSession(request)
		if !ok || oauth.repositoryConsentAllowed == nil ||
			!constantTimeTokenEqual(consent.SessionBinding, oauthSessionBinding(previous.ID)) ||
			previous.GitHubID != consent.GitHubID || account.ID != consent.GitHubID ||
			!strings.EqualFold(previous.Login, consent.Login) || !strings.EqualFold(account.Login, consent.Login) ||
			!oauth.repositoryConsentAllowed(session) || !oauth.adminAssured(session, time.Now().UTC()) ||
			time.Now().Unix() >= consent.ExpiresAt {
			oauth.logBranch(ctx, "callback.consent_rejected")
			fail(http.StatusForbidden, "authorization_failed")
			return
		}
		oauth.logBranch(ctx, "callback.consent_authorized")
	}
	sessionID, err := randomToken(32)
	if err != nil {
		oauth.logBranch(ctx, "callback.session_id_generation_failed")
		fail(http.StatusInternalServerError, "session_id_generation_failed")
		return
	}
	csrfToken, err := randomToken(32)
	if err != nil {
		oauth.logBranch(ctx, "callback.csrf_generation_failed")
		fail(http.StatusInternalServerError, "csrf_generation_failed")
		return
	}
	session.ID = sessionID
	session.AvatarURL = account.AvatarURL
	session.CSRFToken = csrfToken
	if err := oauth.saveSession(ctx, session); err != nil {
		serverLog.PrintfContext(ctx, "oauth session save failed")
		oauth.logBranch(ctx, "callback.session_save_failed")
		fail(http.StatusServiceUnavailable, "session_save_failed")
		return
	}
	retained = true
	oauth.setSessionCookies(response, session)
	oauth.clearStateCookie(response)
	oauth.rememberAccount(response, request, account, true)
	serverLog.PrintfContext(ctx, "oauth callback completed")
	oauth.logBranch(ctx, "callback.succeeded")
	http.Redirect(response, request, "/", http.StatusFound)
}

func (oauth *githubOAuth) logout(response http.ResponseWriter, request *http.Request) {
	if err := oauth.clearRequestSession(response, request); err != nil {
		oauth.logBranch(request.Context(), "logout.failed")
		writeError(request.Context(), response, http.StatusServiceUnavailable, "GitHub credential revocation is temporarily unavailable")
		return
	}
	oauth.logBranch(request.Context(), "logout.succeeded")
	response.WriteHeader(http.StatusNoContent)
}

func (oauth *githubOAuth) switchAccount(response http.ResponseWriter, request *http.Request) {
	if err := oauth.clearRequestSession(response, request); err != nil {
		oauth.logBranch(request.Context(), "switch_account.failed")
		writeError(request.Context(), response, http.StatusServiceUnavailable, "GitHub credential revocation is temporarily unavailable")
		return
	}
	oauth.logBranch(request.Context(), "switch_account.succeeded")
	writeJSON(response, http.StatusOK, map[string]string{"loginUrl": "/auth/login?select_account=1"})
}

func (oauth *githubOAuth) currentAccount(response http.ResponseWriter, request *http.Request) {
	session, ok := oauth.loadRequestSession(request)
	if !ok {
		oauth.logBranch(request.Context(), "current_account.session_missing")
		writeError(request.Context(), response, http.StatusUnauthorized, "GitHub authentication is required")
		return
	}
	oauth.logBranch(request.Context(), "current_account.succeeded")
	payload := map[string]string{
		"login":     session.Login,
		"avatarUrl": session.AvatarURL,
	}
	if until, ok := oauth.adminAssuranceExpires(session); ok && time.Now().UTC().Before(until) {
		payload["adminAssuranceExpiresAt"] = until.Format(time.RFC3339)
	}
	writeJSON(response, http.StatusOK, payload)
}

// verifiedAssurance records disclosed 2FA enrollment or attested SSO access
// for a freshly authorized token, not proof of a new MFA ceremony.
// It never infers assurance from absent data.
func (oauth *githubOAuth) verifiedAssurance(ctx context.Context, accessToken string, account githubAccount) []string {
	var assurance []string
	if account.TwoFactorAuthentication != nil && *account.TwoFactorAuthentication {
		assurance = append(assurance, assuranceTwoFactor)
		oauth.logBranch(ctx, "assurance.two_factor_verified")
	}
	for _, org := range oauth.config.SSOOrganizations {
		if oauth.orgAuthorized(ctx, accessToken, org) {
			assurance = append(assurance, assuranceSSOPrefix+strings.ToLower(org))
			oauth.logBranch(ctx, "assurance.sso_verified")
		}
	}
	if len(assurance) == 0 {
		oauth.logBranch(ctx, "assurance.unverified")
	}
	return assurance
}

// adminAssuranceExpires reports when the session's administrator step-up
// lapses. Sessions without a verified factor never qualify.
func (oauth *githubOAuth) adminAssuranceExpires(session oauthSession) (time.Time, bool) {
	if session.AuthenticatedAt.IsZero() || len(session.Assurance) == 0 {
		return time.Time{}, false
	}
	return session.AuthenticatedAt.Add(oauth.adminReauthenticationMaxAge()), true
}

func (oauth *githubOAuth) adminReauthenticationMaxAge() time.Duration {
	if oauth.config.AdminReauthenticationMaxAge <= 0 {
		return defaultAdminReauthenticationMaxAge
	}
	return oauth.config.AdminReauthenticationMaxAge
}

func (oauth *githubOAuth) adminAssured(session oauthSession, now time.Time) bool {
	until, ok := oauth.adminAssuranceExpires(session)
	return ok && !now.Before(session.AuthenticatedAt) && now.Before(until)
}

// Streams cannot refresh tokens or replace cookies after flushing headers.
// Reconnect through ordinary authentication before any authority needs renewal.
func (oauth *githubOAuth) eventAuthorizationExpires(session oauthSession, allowHealth bool) time.Time {
	until := session.AuthorizedAt.Add(authorizationRecheckInterval)
	if refreshAt := session.AccessExpires.Add(-tokenRefreshSkew); refreshAt.Before(until) {
		until = refreshAt
	}
	if allowHealth {
		if assuranceUntil, ok := oauth.adminAssuranceExpires(session); ok && assuranceUntil.Before(until) {
			until = assuranceUntil
		}
	}
	return until
}

func (oauth *githubOAuth) clearRequestSession(response http.ResponseWriter, request *http.Request) error {
	session, ok := oauth.loadRequestSession(request)
	sealed := ""
	if ok {
		staged, stagedValue, err := oauth.stageRevocation(request.Context(), session.ID)
		if err != nil {
			serverLog.PrintfContext(request.Context(), "oauth credential revocation staging failed")
			oauth.logBranch(request.Context(), "session_clear.revocation_staging_failed")
			return err
		}
		session = staged
		sealed = stagedValue
		if sealed == "" {
			oauth.logBranch(request.Context(), "session_clear.already_absent")
			ok = false
		} else {
			oauth.logBranch(request.Context(), "session_clear.revocation_staged")
		}
	} else {
		oauth.logBranch(request.Context(), "session_clear.session_missing")
	}
	oauth.clearSessionCookies(response)
	if ok {
		if err := oauth.revokeCredentials(request.Context(), session); err != nil {
			serverLog.PrintfContext(request.Context(), "oauth credential revocation queued")
			oauth.logBranch(request.Context(), "session_clear.revocation_queued")
		} else {
			if err := oauth.completeRevocation(request.Context(), session.ID, sealed); err != nil {
				oauth.logBranch(request.Context(), "session_clear.revocation_completion_failed")
			} else {
				oauth.logBranch(request.Context(), "session_clear.revocation_completed")
			}
		}
	}
	return nil
}

func (oauth *githubOAuth) session(response http.ResponseWriter, request *http.Request) (oauthSession, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		oauth.logBranch(request.Context(), "session.cookie_missing")
		return oauthSession{}, false
	}
	session, expected, err := oauth.loadSessionRecord(request.Context(), cookie.Value)
	if err != nil {
		oauth.logBranch(request.Context(), "session.load_failed")
		return oauthSession{}, false
	}
	if time.Now().UTC().Add(tokenRefreshSkew).Before(session.AccessExpires) {
		if time.Since(session.AuthorizedAt) >= authorizationRecheckInterval {
			ctx := telemetry.ExtractTraceparent(request.Context(), request.Header.Get("Traceparent"))
			ctx, span := telemetry.Tracer().Start(ctx, "cao_dashboard.auth.revalidate")
			defer span.End()
			account, err := oauth.authorizedAccount(ctx, session.AccessToken)
			if err != nil || !strings.EqualFold(account.Login, session.Login) {
				oauth.logBranch(ctx, "session.authorization_rejected")
				recordOAuthDecision(ctx, "session_revalidation", "rejected")
				oauth.invalidateSessionIfUnchanged(response, ctx, session.ID, expected)
				return oauthSession{}, false
			}
			session.AvatarURL = account.AvatarURL
			session.AuthorizedAt = time.Now().UTC()
			saved, err := oauth.saveSessionIfUnchanged(ctx, session, expected)
			if err != nil {
				oauth.logBranch(ctx, "session.revalidation_save_failed")
				recordOAuthDecision(ctx, "session_revalidation", "store_failed")
				return oauthSession{}, false
			}
			if !saved {
				latest, err := oauth.loadSession(ctx, session.ID)
				if err != nil || time.Since(latest.AuthorizedAt) >= authorizationRecheckInterval {
					oauth.logBranch(ctx, "session.revalidation_superseded")
					recordOAuthDecision(ctx, "session_revalidation", "superseded")
					return oauthSession{}, false
				}
				session = latest
				recordOAuthDecision(ctx, "session_revalidation", "concurrent_accepted")
			} else {
				oauth.logBranch(ctx, "session.authorization_revalidated")
				recordOAuthDecision(ctx, "session_revalidation", "accepted")
			}
		}
		oauth.logBranch(request.Context(), "session.active")
		csrfCookie, err := request.Cookie(csrfCookieName)
		if err != nil || csrfCookie.Value == "" {
			oauth.setSessionCookies(response, session)
		}
		return session, true
	}
	if session.RefreshToken == "" || !time.Now().UTC().Before(session.RefreshExpires) {
		oauth.logBranch(request.Context(), "session.expired_revocation_required")
		oauth.invalidateSessionIfUnchanged(response, request.Context(), session.ID, expected)
		return oauthSession{}, false
	}
	if !oauth.acquireExchange(request.Context()) {
		return oauthSession{}, false
	}
	defer oauth.releaseExchange()
	refreshed, err := oauth.exchange(request.Context(), url.Values{
		"client_id":     {oauth.config.ClientID},
		"client_secret": {oauth.config.ClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {session.RefreshToken},
	})
	if err != nil {
		serverLog.PrintfContext(request.Context(), "oauth token refresh failed")
		oauth.logBranch(request.Context(), "refresh.exchange_failed")
		oauth.invalidateSessionIfUnchanged(response, request.Context(), session.ID, expected)
		return oauthSession{}, false
	}
	previous := session
	now := time.Now().UTC()
	if refreshed.RefreshToken != "" {
		oauth.logBranch(request.Context(), "refresh.refresh_token_rotated")
	} else {
		oauth.logBranch(request.Context(), "refresh.refresh_token_reused")
	}
	session.applyTokens(refreshed, now)
	if refreshed.RefreshTokenExpiresIn > 0 {
		oauth.logBranch(request.Context(), "refresh.refresh_expiry_updated")
	} else {
		oauth.logBranch(request.Context(), "refresh.refresh_expiry_retained")
	}
	if session.ProviderAccessExpires.IsZero() {
		oauth.logBranch(request.Context(), "refresh.access_expiry_defaulted")
	} else {
		oauth.logBranch(request.Context(), "refresh.access_expiry_provided")
	}
	account, err := oauth.authorizedAccount(request.Context(), refreshed.AccessToken)
	if err != nil || !strings.EqualFold(account.Login, session.Login) {
		if oauth.revokeCredentials(request.Context(), session) != nil {
			if oauth.queueRevocation(request.Context(), session) != nil {
				oauth.logBranch(request.Context(), "refresh.rejected_revocation_queue_failed")
				oauth.retainCredentialCleanup(request.Context(), session)
			} else {
				oauth.logBranch(request.Context(), "refresh.rejected_revocation_queued")
			}
		} else {
			oauth.logBranch(request.Context(), "refresh.rejected_credentials_revoked")
		}
		oauth.invalidateSessionIfUnchanged(response, request.Context(), session.ID, expected)
		serverLog.PrintfContext(request.Context(), "oauth authorization revalidation failed")
		oauth.logBranch(request.Context(), "refresh.authorization_rejected")
		return oauthSession{}, false
	}
	session.AvatarURL = account.AvatarURL
	session.AuthorizedAt = time.Now().UTC()
	saved, err := oauth.saveSessionIfUnchanged(request.Context(), session, expected)
	if err == nil && !saved {
		latest, latestSealed, loadErr := oauth.loadSessionRecord(request.Context(), session.ID)
		if loadErr == nil &&
			latest.ID == session.ID && latest.Login == session.Login &&
			latest.AccessToken == previous.AccessToken && latest.RefreshToken == previous.RefreshToken &&
			latest.CSRFToken == session.CSRFToken && latest.AuthorizedAt.After(previous.AuthorizedAt) {
			expected = latestSealed
			saved, err = oauth.saveSessionIfUnchanged(request.Context(), session, expected)
			if saved && err == nil {
				oauth.logBranch(request.Context(), "refresh.concurrent_revalidation_reconciled")
				recordOAuthDecision(request.Context(), "session_revalidation", "concurrent_refresh_reconciled")
			}
		}
	}
	if err != nil {
		serverLog.PrintfContext(request.Context(), "oauth refreshed session save failed")
		oauth.logBranch(request.Context(), "refresh.session_save_failed")
		if oauth.revokeCredentials(request.Context(), session) != nil {
			if oauth.queueRevocation(request.Context(), session) != nil {
				oauth.logBranch(request.Context(), "refresh.save_failed_revocation_queue_failed")
				oauth.retainCredentialCleanup(request.Context(), session)
			} else {
				oauth.logBranch(request.Context(), "refresh.save_failed_revocation_queued")
			}
		} else {
			oauth.logBranch(request.Context(), "refresh.save_failed_credentials_revoked")
		}
		oauth.invalidateSessionIfUnchanged(response, request.Context(), session.ID, expected)
		return oauthSession{}, false
	}
	if !saved {
		serverLog.PrintfContext(request.Context(), "oauth refreshed session superseded")
		oauth.logBranch(request.Context(), "refresh.session_superseded")
		if oauth.revokeCredentials(request.Context(), session) != nil {
			if oauth.queueRevocation(request.Context(), session) != nil {
				oauth.logBranch(request.Context(), "refresh.superseded_revocation_queue_failed")
				oauth.retainCredentialCleanup(request.Context(), session)
			} else {
				oauth.logBranch(request.Context(), "refresh.superseded_revocation_queued")
			}
		} else {
			oauth.logBranch(request.Context(), "refresh.superseded_credentials_revoked")
		}
		oauth.clearSessionCookies(response)
		return oauthSession{}, false
	}
	serverLog.PrintfContext(request.Context(), "oauth session refreshed")
	oauth.logBranch(request.Context(), "refresh.succeeded")
	oauth.setSessionCookies(response, session)
	return session, true
}

func (oauth *githubOAuth) invalidateSessionIfUnchanged(response http.ResponseWriter, ctx context.Context, sessionID, expected string) {
	session, sealed, err := oauth.stageRevocationIfUnchanged(ctx, sessionID, expected)
	if errors.Is(err, errSessionSuperseded) {
		oauth.logBranch(ctx, "invalidation.session_superseded")
		return
	}
	if err != nil {
		serverLog.PrintfContext(ctx, "oauth credential revocation staging failed")
		oauth.logBranch(ctx, "invalidation.revocation_staging_failed")
		oauth.clearSessionCookies(response)
		return
	}
	oauth.clearSessionCookies(response)
	if sealed == "" {
		oauth.logBranch(ctx, "invalidation.session_absent")
		return
	}
	if oauth.revokeCredentials(ctx, session) == nil {
		if oauth.completeRevocation(ctx, session.ID, sealed) != nil {
			oauth.logBranch(ctx, "invalidation.revocation_completion_failed")
		} else {
			oauth.logBranch(ctx, "invalidation.revocation_completed")
		}
	} else {
		oauth.logBranch(ctx, "invalidation.revocation_deferred")
	}
}

func (oauth *githubOAuth) requestHasSession(request *http.Request) bool {
	_, ok := oauth.loadRequestSession(request)
	return ok
}

func (oauth *githubOAuth) validState(request *http.Request) bool {
	_, ok := oauth.loginState(request)
	return ok
}

func (oauth *githubOAuth) loginState(request *http.Request) (*oauthConsentState, bool) {
	state := request.URL.Query().Get("state")
	cookie, err := request.Cookie("cao_oauth_state")
	// A provider state is opaque randomness, never the signed cookie's
	// consent payload. Accepting dots here would permit a consent downgrade.
	if err != nil || state == "" || strings.Contains(state, ".") {
		return nil, false
	}
	if constantTimeTokenEqual(cookie.Value, oauth.sign(state)) {
		return nil, true
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 || parts[0] != state ||
		!constantTimeTokenEqual(cookie.Value, oauth.sign(state+"."+parts[1])) {
		return nil, false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var consent oauthConsentState
	if json.Unmarshal(data, &consent) != nil || consent.SessionBinding == "" ||
		consent.GitHubID <= 0 || consent.Login == "" || time.Now().Unix() >= consent.ExpiresAt {
		return nil, false
	}
	return &consent, true
}

func (oauth *githubOAuth) exchange(ctx context.Context, values url.Values) (tokenResponse, error) {
	// #nosec G704 -- endpoints are fixed GitHub defaults in production and test-only overrides are explicit configuration.
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, oauth.config.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		oauth.logBranch(ctx, "exchange.request_creation_failed")
		return tokenResponse{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := oauth.client.Do(request) // #nosec G704 -- see endpoint validation note above.
	if err != nil {
		oauth.logBranch(ctx, "exchange.request_failed")
		return tokenResponse{}, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		oauth.logBranch(ctx, "exchange.status_rejected")
		return tokenResponse{}, errors.New("GitHub OAuth token endpoint rejected the request")
	}
	var tokens tokenResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&tokens); err != nil {
		oauth.logBranch(ctx, "exchange.response_decode_failed")
		return tokenResponse{}, err
	}
	if tokens.Error != "" || tokens.AccessToken == "" {
		oauth.logBranch(ctx, "exchange.token_missing")
		return tokenResponse{}, errors.New("GitHub OAuth token endpoint did not return an access token")
	}
	oauth.logBranch(ctx, "exchange.succeeded")
	return tokens, nil
}

func (oauth *githubOAuth) authorizedAccount(ctx context.Context, accessToken string) (githubAccount, error) {
	account, err := oauth.githubAccount(ctx, accessToken)
	if err != nil {
		oauth.logBranch(ctx, "authorization.identity_failed")
		return githubAccount{}, err
	}
	if oauth.config.LocalDevelopmentUser != "" {
		if account.ID <= 0 || !strings.EqualFold(account.Login, oauth.config.LocalDevelopmentUser) {
			oauth.logBranch(ctx, "authorization.local_user_rejected")
			return account, fmt.Errorf("GitHub account is not the configured local developer: %w", errAccountNotAuthorized)
		}
		oauth.logBranch(ctx, "authorization.local_user_allowed")
		return account, nil
	}
	for _, login := range oauth.config.AllowedUsers {
		if strings.EqualFold(strings.TrimSpace(login), account.Login) {
			oauth.logBranch(ctx, "authorization.user_allowed")
			return account, nil
		}
	}
	for _, org := range oauth.config.AllowedOrganizations {
		if oauth.orgAuthorized(ctx, accessToken, org) {
			oauth.logBranch(ctx, "authorization.organization_allowed")
			return account, nil
		}
	}
	for _, team := range oauth.config.AllowedTeams {
		parts := strings.Split(team, "/")
		if len(parts) == 2 && oauth.teamAuthorized(ctx, accessToken, parts[0], parts[1], account.Login) {
			oauth.logBranch(ctx, "authorization.team_allowed")
			return account, nil
		}
		if len(parts) != 2 {
			oauth.logBranch(ctx, "authorization.team_policy_invalid")
		}
	}
	oauth.logBranch(ctx, "authorization.denied")
	// The identity is returned only to support account selection hints.
	return account, errAccountNotAuthorized
}

func (oauth *githubOAuth) githubAccount(ctx context.Context, accessToken string) (githubAccount, error) {
	var account githubAccount
	if err := oauth.githubJSON(ctx, http.MethodGet, oauth.config.UserURL, accessToken, nil, &account); err != nil {
		oauth.logBranch(ctx, "identity.request_failed")
		return githubAccount{}, err
	}
	if strings.TrimSpace(account.Login) == "" {
		oauth.logBranch(ctx, "identity.login_missing")
		return githubAccount{}, errors.New("GitHub user response did not include a login")
	}
	oauth.logBranch(ctx, "identity.loaded")
	return account, nil
}

func (oauth *githubOAuth) orgAuthorized(ctx context.Context, accessToken, org string) bool {
	var payload struct {
		State string `json:"state"`
	}
	endpoint := strings.ReplaceAll(oauth.config.OrgMembershipURL, "{org}", url.PathEscape(org))
	if err := oauth.githubJSON(ctx, http.MethodGet, endpoint, accessToken, nil, &payload); err != nil {
		oauth.logBranch(ctx, "organization_membership.request_failed")
		return false
	}
	if payload.State != "active" {
		oauth.logBranch(ctx, "organization_membership.inactive")
		return false
	}
	oauth.logBranch(ctx, "organization_membership.active")
	return true
}

func (oauth *githubOAuth) teamAuthorized(ctx context.Context, accessToken, org, team, login string) bool {
	var payload struct {
		State string `json:"state"`
	}
	endpoint := strings.ReplaceAll(oauth.config.TeamMembershipURL, "{org}", url.PathEscape(org))
	endpoint = strings.ReplaceAll(endpoint, "{team}", url.PathEscape(team))
	endpoint = strings.ReplaceAll(endpoint, "{user}", url.PathEscape(login))
	if err := oauth.githubJSON(ctx, http.MethodGet, endpoint, accessToken, nil, &payload); err != nil {
		oauth.logBranch(ctx, "team_membership.request_failed")
		return false
	}
	if payload.State != "active" {
		oauth.logBranch(ctx, "team_membership.inactive")
		return false
	}
	oauth.logBranch(ctx, "team_membership.active")
	return true
}

func (oauth *githubOAuth) revoke(ctx context.Context, token string) error {
	if token == "" {
		oauth.logBranch(ctx, "revocation.token_absent")
		return nil
	}
	body, _ := json.Marshal(map[string]string{"access_token": token})
	endpoint := strings.ReplaceAll(oauth.config.RevokeURL, "{client_id}", url.PathEscape(oauth.config.ClientID))
	err := oauth.githubJSON(ctx, http.MethodDelete, endpoint, "", bytes.NewReader(body), nil, func(request *http.Request, _ *map[int]bool) {
		request.SetBasicAuth(oauth.config.ClientID, oauth.config.ClientSecret)
	}, acceptNotFound)
	if err != nil {
		oauth.logBranch(ctx, "revocation.request_failed")
	} else {
		oauth.logBranch(ctx, "revocation.request_succeeded")
	}
	return err
}

func (oauth *githubOAuth) revokeCredentials(ctx context.Context, session oauthSession) error {
	return errors.Join(
		oauth.revoke(ctx, session.AccessToken),
		oauth.revoke(ctx, session.RefreshToken),
	)
}

func (oauth *githubOAuth) acquireExchange(ctx context.Context) bool {
	select {
	case oauth.exchanges <- struct{}{}:
		return true
	default:
		oauth.logBranch(ctx, "exchange.cleanup_capacity_exhausted")
		return false
	}
}

func (oauth *githubOAuth) releaseExchange() { <-oauth.exchanges }

// A failed queue write must not release the final owner of newly issued
// credentials. Cancellation removes request authority, not cleanup ownership.
func (oauth *githubOAuth) retainCredentialCleanup(ctx context.Context, session oauthSession) {
	for !credentialsExpired(session) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		err := oauth.revokeCredentials(cleanup, session)
		cancel()
		if err == nil {
			oauth.logBranch(ctx, "credential_cleanup.revoked")
			return
		}
		queue, queueCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err = oauth.queueRevocation(queue, session)
		queueCancel()
		if err == nil {
			oauth.logBranch(ctx, "credential_cleanup.queued")
			return
		}
		oauth.logBranch(ctx, "credential_cleanup.retained")
		time.Sleep(time.Second)
	}
	oauth.logBranch(ctx, "credential_cleanup.provider_expired")
}

func (oauth *githubOAuth) stageRevocation(ctx context.Context, sessionID string) (oauthSession, string, error) {
	return oauth.stageRevocationIfUnchanged(ctx, sessionID, "")
}

var errSessionSuperseded = operational.ErrConflict

func (oauth *githubOAuth) stageRevocationIfUnchanged(ctx context.Context, sessionID, expected string) (oauthSession, string, error) {
	sealed, err := oauth.invalidator.InvalidateSession(ctx, sessionID, expected, oauth.config.RevocationKeyPrefix)
	if err != nil {
		return oauthSession{}, "", err
	}
	if sealed == "" {
		return oauthSession{}, "", nil
	}
	plain, err := oauth.open(sealed)
	if err != nil {
		return oauthSession{}, "", err
	}
	var session oauthSession
	if err := json.Unmarshal(plain, &session); err != nil {
		return oauthSession{}, "", err
	}
	return session, sealed, nil
}

func (oauth *githubOAuth) queueRevocation(ctx context.Context, session oauthSession) error {
	retryID, err := randomToken(16)
	if err != nil {
		return err
	}
	session.ID = retryID
	data, err := json.Marshal(session) // #nosec G117 -- token fields are sealed with AES-GCM before entering the revocation queue.
	if err != nil {
		return err
	}
	sealed, err := oauth.seal(data)
	if err != nil {
		return err
	}
	return oauth.revocations.QueueRevocation(ctx, session.ID, sealed, oauth.config.RevocationKeyPrefix)
}

func (oauth *githubOAuth) completeRevocation(ctx context.Context, sessionID, expected string) error {
	return oauth.revocations.CompleteRevocation(ctx, sessionID, expected, oauth.config.RevocationKeyPrefix)
}

func (oauth *githubOAuth) retryPendingRevocations(ctx context.Context, limit int) {
	for range limit {
		record, err := oauth.revocations.PendingRevocation(ctx, oauth.config.RevocationKeyPrefix)
		if err != nil {
			oauth.logBranch(ctx, "revocation_retry.record_read_failed")
			return
		}
		if record.Value == "" {
			oauth.logBranch(ctx, "revocation_retry.queue_empty")
			return
		}
		sealed := record.Value
		plain, err := oauth.open(sealed)
		if err != nil {
			oauth.logBranch(ctx, "revocation_retry.decrypt_failed")
			return
		}
		var session oauthSession
		if json.Unmarshal(plain, &session) != nil {
			oauth.logBranch(ctx, "revocation_retry.decode_failed")
			return
		}
		if credentialsExpired(session) {
			if oauth.completeRevocation(ctx, session.ID, sealed) != nil {
				oauth.logBranch(ctx, "revocation_retry.expired_removal_failed")
				continue
			}
			oauth.logBranch(ctx, "revocation_retry.expired_removed")
			continue
		}
		if oauth.revokeCredentials(ctx, session) != nil {
			oauth.logBranch(ctx, "revocation_retry.request_failed")
			return
		}
		if oauth.completeRevocation(ctx, session.ID, sealed) != nil {
			oauth.logBranch(ctx, "revocation_retry.completion_failed")
			continue
		}
		oauth.logBranch(ctx, "revocation_retry.completed")
	}
	oauth.logBranch(ctx, "revocation_retry.limit_reached")
}

func (oauth *githubOAuth) runRevocationWorker(ctx context.Context) {
	oauth.retryPendingRevocations(ctx, 16)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			oauth.logBranch(ctx, "revocation_worker.stopped")
			return
		case <-ticker.C:
			oauth.logBranch(ctx, "revocation_worker.tick")
			oauth.retryPendingRevocations(ctx, 16)
		}
	}
}

func credentialsExpired(session oauthSession) bool {
	now := time.Now().UTC()
	for _, credential := range []struct {
		token   string
		expires time.Time
	}{
		{session.AccessToken, session.ProviderAccessExpires},
		{session.RefreshToken, session.ProviderRefreshExpires},
	} {
		if credential.token != "" && (credential.expires.IsZero() || now.Before(credential.expires)) {
			return false
		}
	}
	return true
}

type githubRequestOption func(*http.Request, *map[int]bool)

func acceptNotFound(_ *http.Request, accepted *map[int]bool) {
	(*accepted)[http.StatusNotFound] = true
}

func (oauth *githubOAuth) githubJSON(ctx context.Context, method, endpoint, token string, body io.Reader, output any, configure ...githubRequestOption) error {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body) // #nosec G704 -- endpoints come only from validated server-side OAuth configuration.
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	accepted := map[int]bool{}
	for _, apply := range configure {
		apply(request, &accepted)
	}
	response, err := oauth.client.Do(request) // #nosec G704 -- the request endpoint is fixed server-side OAuth configuration, never user input.
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()
	if (response.StatusCode < 200 || response.StatusCode >= 300) && !accepted[response.StatusCode] {
		return fmt.Errorf("GitHub API returned %d", response.StatusCode)
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output)
}

func (oauth *githubOAuth) loadRequestSession(request *http.Request) (oauthSession, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return oauthSession{}, false
	}
	session, err := oauth.loadSession(request.Context(), cookie.Value)
	return session, err == nil
}

func (oauth *githubOAuth) loadSession(ctx context.Context, sessionID string) (oauthSession, error) {
	session, _, err := oauth.loadSessionRecord(ctx, sessionID)
	return session, err
}

func (oauth *githubOAuth) loadSessionRecord(ctx context.Context, sessionID string) (oauthSession, string, error) {
	sealed, err := oauth.sessions.SessionRecord(ctx, sessionID)
	if err != nil {
		return oauthSession{}, "", fmt.Errorf("load session: %w", err)
	}
	if sealed == "" {
		return oauthSession{}, "", errors.New("session is unavailable")
	}
	plain, err := oauth.open(sealed)
	if err != nil {
		return oauthSession{}, "", err
	}
	var session oauthSession
	if err := json.Unmarshal(plain, &session); err != nil {
		return oauthSession{}, "", err
	}
	if !strings.EqualFold(session.LocalDevelopmentUser, oauth.config.LocalDevelopmentUser) {
		oauth.logBranch(ctx, "session.authorization_mode_rejected")
		return oauthSession{}, "", errors.New("session does not match the configured OAuth authorization mode")
	}
	return session, sealed, nil
}

func (oauth *githubOAuth) saveSession(ctx context.Context, session oauthSession) error {
	data, err := json.Marshal(session) // #nosec G117 -- marshaled token fields are encrypted with AES-GCM before Redis storage.
	if err != nil {
		return err
	}
	sealed, err := oauth.seal(data)
	if err != nil {
		return err
	}
	return oauth.sessions.PutSession(ctx, session.ID, sealed, sessionTTL, oauth.config.RevocationKeyPrefix)
}

func (oauth *githubOAuth) saveSessionIfUnchanged(ctx context.Context, session oauthSession, expected string) (bool, error) {
	data, err := json.Marshal(session) // #nosec G117 -- token fields are sealed with AES-GCM before Redis persistence.
	if err != nil {
		return false, err
	}
	sealed, err := oauth.seal(data)
	if err != nil {
		return false, err
	}
	return oauth.sessions.CompareSession(ctx, session.ID, expected, sealed, sessionTTL, oauth.config.RevocationKeyPrefix)
}

func (oauth *githubOAuth) deleteSession(ctx context.Context, sessionID string) error {
	return oauth.sessions.DeleteSession(ctx, sessionID)
}

func (oauth *githubOAuth) setSessionCookies(response http.ResponseWriter, session oauthSession) {
	http.SetCookie(response, &http.Cookie{Name: sessionCookieName, Value: session.ID, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds())})
	// #nosec G124 -- CSRF token must be browser-readable so same-origin fetch requests can mirror it in X-CSRF-Token.
	http.SetCookie(response, &http.Cookie{Name: csrfCookieName, Value: url.QueryEscape(session.CSRFToken), Path: "/", Secure: true, HttpOnly: false, SameSite: http.SameSiteLaxMode, MaxAge: int(csrfCookieTTL.Seconds())})
}

func (oauth *githubOAuth) clearSessionCookies(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	// #nosec G124 -- CSRF token cookie is intentionally not HttpOnly; it carries no session authority.
	http.SetCookie(response, &http.Cookie{Name: csrfCookieName, Value: "", Path: "/", Secure: true, HttpOnly: false, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func (oauth *githubOAuth) clearStateCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{Name: "cao_oauth_state", Value: "", Path: "/auth/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func (oauth *githubOAuth) sign(value string) string {
	key := oauth.key
	if oauth.config.loginStateSecret != "" {
		key = []byte(oauth.config.loginStateSecret)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return value + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (oauth *githubOAuth) seal(plain []byte) (string, error) {
	block, err := aes.NewCipher(oauth.key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, nil))
	return sessionKeyID(oauth.key) + "." + sealed, nil
}

func (oauth *githubOAuth) open(sealed string) ([]byte, error) {
	if parts := strings.SplitN(sealed, ".", 2); len(parts) == 2 {
		key, ok := oauth.keys[parts[0]]
		if !ok {
			return nil, errors.New("encrypted session key is unavailable")
		}
		return openWithKey(parts[1], key)
	}
	if plain, err := openWithKey(sealed, oauth.key); err == nil {
		return plain, nil
	}
	for id, key := range oauth.keys {
		if id == sessionKeyID(oauth.key) {
			continue
		}
		if plain, err := openWithKey(sealed, key); err == nil {
			return plain, nil
		}
	}
	return nil, errors.New("encrypted session is invalid")
}

func openWithKey(sealed string, key []byte) ([]byte, error) {
	data, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < aead.NonceSize() {
		return nil, errors.New("encrypted session is invalid")
	}
	return aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], nil)
}

func sessionKeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

func randomToken(bytes int) (string, error) {
	data := make([]byte, bytes)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
