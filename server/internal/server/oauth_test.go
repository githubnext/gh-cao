package server

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func TestAzureModeRequiresCompleteGitHubOAuthPolicy(t *testing.T) {
	address, closeServer := fakeRedis(t)
	defer closeServer()
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	site := t.TempDir()
	if err := os.WriteFile(site+"/index.html", []byte("<html><head></head></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = New(context.Background(), redisx.NewStore(client, "test"), Config{
		Database:      constructorDatabase(),
		HostProfile:   azureFunctionsHostProfile(),
		SiteDirectory: site,
		AccessToken:   testAccessToken,
		Proxy:         ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true},
		GitHubOAuth:   validOAuthConfig("https://github.test"),
	})
	if err == nil || !strings.Contains(err.Error(), "does not support local bearer") {
		t.Fatalf("expected bearer rejection in Azure mode, got %v", err)
	}

	_, err = New(context.Background(), redisx.NewStore(client, "test"), Config{
		Database:      constructorDatabase(),
		HostProfile:   azureFunctionsHostProfile(),
		SiteDirectory: site,
		Proxy:         ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true},
		GitHubOAuth: &GitHubOAuthConfig{
			ClientID:      "client",
			ClientSecret:  "secret",
			RedirectURL:   "https://dashboard.example.com/auth/callback",
			SessionSecret: "0123456789abcdef0123456789abcdef",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "authorization requires") {
		t.Fatalf("expected explicit authorization policy rejection, got %v", err)
	}
}

func TestGitHubOAuthAllowsExplicitUserWithoutMembershipLookup(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "inactive"})
	config := validOAuthConfig(github.URL)
	config.AllowedOrganizations = nil
	config.AllowedUsers = []string{"OCTOCAT"}
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	account, err := newGitHubOAuth(*config, nil, nil, nil).authorizedAccount(t.Context(), "access")
	if err != nil {
		t.Fatal(err)
	}
	if account.Login != "octocat" {
		t.Fatalf("authorized account = %q, want octocat", account.Login)
	}
}

func TestAzureOAuthLoginCallbackAndAuthorizedAPI(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	app.database = integrationDatabase(t)

	login := httptest.NewRecorder()
	request := azureRequest(t, http.MethodGet, "/auth/login")
	app.Handler().ServeHTTP(login, request)
	if login.Code != http.StatusFound {
		t.Fatalf("login returned %d: %s", login.Code, login.Body.String())
	}
	stateCookie := firstCookie(t, login.Result(), "cao_oauth_state")
	location, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := location.Query().Get("state")
	if state == "" || location.Query().Get("scope") != "read:org read:user" || location.Query().Get("prompt") != "select_account" {
		t.Fatalf("unexpected login redirect: %s", location.String())
	}
	authorizeFakeGitHub(t, app, location.String())

	callback := httptest.NewRecorder()
	request = azureRequest(t, http.MethodGet, "/auth/callback?code=code-1&state="+url.QueryEscape(state))
	request.AddCookie(stateCookie)
	app.Handler().ServeHTTP(callback, request)
	if callback.Code != http.StatusFound {
		t.Fatalf("callback returned %d: %s", callback.Code, callback.Body.String())
	}
	forbidden := callback.Header().Values("Set-Cookie")
	if strings.Contains(strings.Join(forbidden, "\n"), "access-") || strings.Contains(strings.Join(forbidden, "\n"), "refresh-") {
		t.Fatalf("token leaked to cookie: %v", forbidden)
	}
	sessionCookie := firstCookie(t, callback.Result(), sessionCookieName)
	if !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie is not secure: %#v", sessionCookie)
	}
	csrfCookie := firstCookie(t, callback.Result(), csrfCookieName)
	if csrfCookie.HttpOnly || !csrfCookie.Secure {
		t.Fatalf("csrf cookie has unexpected flags: %#v", csrfCookie)
	}
	if csrfCookie.MaxAge != int(csrfCookieTTL.Seconds()) || sessionCookie.MaxAge != int(sessionTTL.Seconds()) {
		t.Fatal("CSRF cookie must expire before the session cookie")
	}

	unauthorized := httptest.NewRecorder()
	request = azureRequest(t, http.MethodPost, "/api/v1/refresh")
	request.Header.Set("Authorization", "Bearer "+testAccessToken)
	app.Handler().ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("Azure mode accepted bearer-only request: %d", unauthorized.Code)
	}

	authorized := httptest.NewRecorder()
	request = azureRequest(t, http.MethodPost, "/api/v1/refresh")
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	app.Handler().ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized refresh returned %d: %s", authorized.Code, authorized.Body.String())
	}
}

func TestOAuthLoginScopeMatchesApplicationType(t *testing.T) {
	for _, test := range []struct {
		clientID string
		scope    string
	}{
		{"legacy-oauth-client", "read:org read:user"},
		{"Ov23liOAuthClient", "read:org read:user"},
		{"Iv1.github-app", "offline_access"},
		{"Iv23liGitHubApp", "offline_access"},
	} {
		t.Run(test.clientID, func(t *testing.T) {
			oauth := newGitHubOAuth(GitHubOAuthConfig{
				ClientID:      test.clientID,
				AuthURL:       "https://github.test/login/oauth/authorize",
				RedirectURL:   "https://dashboard.example.com/auth/callback",
				SessionSecret: "0123456789abcdef0123456789abcdef",
			}, nil, nil, nil)
			response := httptest.NewRecorder()
			oauth.login(response, azureRequest(t, http.MethodGet, "/auth/login"))
			location, err := url.Parse(response.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusFound || location.Query().Get("scope") != test.scope {
				t.Fatalf("unexpected authorization scope: %s", response.Header().Get("Location"))
			}
			if location.Query().Get("state") == "" || location.Query().Get("prompt") != "select_account" {
				t.Fatal("login must retain state and account selection")
			}
			oauth.repositoryConsentAllowed = func(oauthSession) bool { return true }
			response = httptest.NewRecorder()
			oauth.login(response, azureRequest(t, http.MethodGet, "/auth/login"))
			location, err = url.Parse(response.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusFound || location.Query().Get("scope") != test.scope {
				t.Fatalf("enabled hosted CLI widened ordinary login scope: %s", location.Query().Get("scope"))
			}
		})
	}
}

func TestAzureOAuthRejectsInvalidStateAndDeniedMembership(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, azureRequest(t, http.MethodGet, "/auth/callback?code=code-1&state=bad"))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid state returned %d", response.Code)
	}

	deniedGitHub := fakeGitHub(t, fakeGitHubOptions{membershipState: "pending", accessExpiresIn: 3600})
	app = newAzureTestApp(t, deniedGitHub.URL)
	stateCookie, state := loginState(t, app)
	response = httptest.NewRecorder()
	request := azureRequest(t, http.MethodGet, "/auth/callback?code=code-1&state="+url.QueryEscape(state))
	request.AddCookie(stateCookie)
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("denied membership returned %d: %s", response.Code, response.Body.String())
	}
	if len(response.Result().Cookies()) != 1 {
		t.Fatalf("denied callback should only clear state cookie, got %#v", response.Result().Cookies())
	}
	if !deniedGitHub.sawRevocation("access-old") || !deniedGitHub.sawRevocation("refresh-old") {
		t.Fatal("denied callback left issued credentials active")
	}
	retry := httptest.NewRecorder()
	app.Handler().ServeHTTP(retry, azureRequest(t, http.MethodGet, "/auth/login"))
	location, err := url.Parse(retry.Header().Get("Location"))
	if retry.Code != http.StatusFound || err != nil || location.Query().Get("prompt") != "select_account" {
		t.Fatalf("retry after denied membership did not request account selection: %s (%v)", retry.Header().Get("Location"), err)
	}
}

func TestOAuthRevalidatesMembershipBeforeAccessTokenExpires(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	cookie, _ := callbackSession(t, app)

	github.membershipState = "inactive"
	current := httptest.NewRecorder()
	request := azureRequest(t, http.MethodGet, "/api/auth/session")
	request.AddCookie(cookie)
	app.Handler().ServeHTTP(current, request)
	if current.Code != http.StatusOK {
		t.Fatalf("freshly authorized session returned %d", current.Code)
	}

	session, err := app.oauth.loadSession(t.Context(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	session.AuthorizedAt = time.Now().Add(-authorizationRecheckInterval)
	if err := app.oauth.saveSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	denied := httptest.NewRecorder()
	app.Handler().ServeHTTP(denied, request)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("removed member retained access: %d", denied.Code)
	}
	if !github.sawRevocation("access-old") || !github.sawRevocation("refresh-old") {
		t.Fatal("removed member's credentials were not revoked")
	}
	if _, err := app.oauth.loadSession(t.Context(), cookie.Value); err == nil {
		t.Fatal("removed member's session remained active")
	}
}

func TestOAuthRevalidationPersistsAuthorizationFreshness(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	cookie, _ := callbackSession(t, app)
	session, expected, err := app.oauth.loadSessionRecord(t.Context(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	session.AuthorizedAt = time.Now().Add(-authorizationRecheckInterval)
	saved, err := app.oauth.saveSessionIfUnchanged(t.Context(), session, expected)
	if err != nil || !saved {
		t.Fatalf("could not age authorization: %v", err)
	}
	request := azureRequest(t, http.MethodGet, "/api/auth/session")
	request.AddCookie(cookie)
	current := httptest.NewRecorder()
	app.Handler().ServeHTTP(current, request)
	if current.Code != http.StatusOK {
		t.Fatalf("revalidated session returned %d", current.Code)
	}
	updated, err := app.oauth.loadSession(t.Context(), cookie.Value)
	if err != nil || time.Since(updated.AuthorizedAt) >= authorizationRecheckInterval {
		t.Fatalf("authorization freshness was not persisted: %v", err)
	}
}

func TestOAuthRefreshReconcilesConcurrentAuthorizationRecheck(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: -60, refreshSucceeds: true})
	app := newAzureTestApp(t, github.URL)
	app.database = integrationDatabase(t)
	cookie, csrf := callbackSession(t, app)
	github.onUserRequest = func() {
		current, sealed, err := app.oauth.loadSessionRecord(t.Context(), cookie.Value)
		if err != nil {
			t.Error(err)
			return
		}
		current.AuthorizedAt = time.Now().UTC()
		saved, err := app.oauth.saveSessionIfUnchanged(t.Context(), current, sealed)
		if err != nil || !saved {
			t.Errorf("could not simulate concurrent revalidation: saved=%t err=%v", saved, err)
		}
		github.onUserRequest = nil
	}
	request := azureRequest(t, http.MethodPost, "/api/v1/refresh")
	request.AddCookie(cookie)
	request.AddCookie(csrf)
	request.Header.Set("X-CSRF-Token", csrf.Value)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("concurrent revalidation stranded refresh: %d", response.Code)
	}
	session, err := app.oauth.loadSession(t.Context(), cookie.Value)
	if err != nil || session.AccessToken != "access-new" || session.RefreshToken != "refresh-new" {
		t.Fatalf("refresh did not retain issued tokens: %v", err)
	}
	if github.sawRevocation("access-new") || github.sawRevocation("refresh-new") {
		t.Fatal("successfully persisted refresh credentials were revoked")
	}
}

func TestOAuthQueuesCredentialsWhenCallbackAuthorizationFails(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "inactive", accessExpiresIn: 3600, rejectRevocation: true})
	app := newAzureTestApp(t, github.URL)
	stateCookie, state := loginState(t, app)
	response := httptest.NewRecorder()
	request := azureRequest(t, http.MethodGet, "/auth/callback?code=code-1&state="+url.QueryEscape(state))
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	request = request.WithContext(ctx)
	github.onUserRequest = cancel
	request.AddCookie(stateCookie)
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("denied callback returned %d", response.Code)
	}
	record, err := app.oauth.revocations.PendingRevocation(t.Context(), app.oauth.config.RevocationKeyPrefix)
	if err != nil || record.Value == "" {
		t.Fatalf("unrevoked callback credentials were not queued: %v", err)
	}
	github.rejectRevocation = false
	app.oauth.retryPendingRevocations(t.Context(), 1)
	if !github.sawRevocation("access-old") || !github.sawRevocation("refresh-old") {
		t.Fatal("callback credentials could not be revoked from the retry queue")
	}
}

func TestOAuthRevokesCallbackCredentialsAfterClientCancellation(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	stateCookie, state := loginState(t, app)
	request := azureRequest(t, http.MethodGet, "/auth/callback?code=code-1&state="+url.QueryEscape(state))
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	request = request.WithContext(ctx)
	request.AddCookie(stateCookie)
	github.onUserRequest = cancel
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if !github.sawRevocation("access-old") || !github.sawRevocation("refresh-old") {
		t.Fatal("canceled callback left exchanged credentials active")
	}
}

func TestAzureOAuthRefreshRotationLogoutAndRefreshFailure(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: -60, refreshSucceeds: true})
	app := newAzureTestApp(t, github.URL)
	app.database = integrationDatabase(t)
	sessionCookie, csrfCookie := callbackSession(t, app)

	refreshed := httptest.NewRecorder()
	request := azureRequest(t, http.MethodPost, "/api/v1/refresh")
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	app.Handler().ServeHTTP(refreshed, request)
	if refreshed.Code != http.StatusOK {
		t.Fatalf("refresh rotation request returned %d: %s", refreshed.Code, refreshed.Body.String())
	}
	if renewed := firstCookie(t, refreshed.Result(), sessionCookieName); renewed.Value != sessionCookie.Value ||
		renewed.MaxAge != int(sessionTTL.Seconds()) || !renewed.HttpOnly || !renewed.Secure {
		t.Fatal("token refresh did not renew the secure session cookie")
	}
	if renewed := firstCookie(t, refreshed.Result(), csrfCookieName); renewed.Value != csrfCookie.Value ||
		renewed.MaxAge != int(csrfCookieTTL.Seconds()) || renewed.HttpOnly || !renewed.Secure {
		t.Fatal("token refresh did not renew the browser-readable CSRF cookie")
	}

	logout := httptest.NewRecorder()
	request = azureRequest(t, http.MethodPost, "/auth/logout")
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	app.Handler().ServeHTTP(logout, request)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout returned %d", logout.Code)
	}
	if !github.sawRevocation("access-new") || !github.sawRevocation("refresh-new") {
		t.Fatalf("logout did not revoke rotated tokens: %#v", github.revoked)
	}

	failingGitHub := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: -60, refreshSucceeds: false})
	app = newAzureTestApp(t, failingGitHub.URL)
	sessionCookie, csrfCookie = callbackSession(t, app)
	failed := httptest.NewRecorder()
	request = azureRequest(t, http.MethodPost, "/api/v1/refresh")
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	app.Handler().ServeHTTP(failed, request)
	if failed.Code != http.StatusUnauthorized {
		t.Fatalf("refresh failure returned %d: %s", failed.Code, failed.Body.String())
	}
	if firstCookie(t, failed.Result(), sessionCookieName).MaxAge >= 0 {
		t.Fatal("failed token refresh renewed a revoked session")
	}

	removedGitHub := fakeGitHub(t, fakeGitHubOptions{
		membershipState:          "active",
		refreshedMembershipState: "inactive",
		accessExpiresIn:          -60,
		refreshSucceeds:          true,
	})
	app = newAzureTestApp(t, removedGitHub.URL)
	sessionCookie, csrfCookie = callbackSession(t, app)
	removed := httptest.NewRecorder()
	request = azureRequest(t, http.MethodPost, "/api/v1/refresh")
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	app.Handler().ServeHTTP(removed, request)
	if removed.Code != http.StatusUnauthorized {
		t.Fatalf("removed member refresh returned %d: %s", removed.Code, removed.Body.String())
	}

	if !removedGitHub.sawRevocation("access-new") {
		t.Fatalf("removed member's refreshed token was not revoked: %#v", removedGitHub.revoked)
	}
}

func TestOAuthRestoresExpiredCSRFCookieBeforeSessionExpiry(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	app.database = integrationDatabase(t)
	sessionCookie, csrfCookie := callbackSession(t, app)

	recovery := httptest.NewRecorder()
	request := azureRequest(t, http.MethodGet, "/api/auth/session")
	request.AddCookie(sessionCookie)
	app.Handler().ServeHTTP(recovery, request)
	if recovery.Code != http.StatusOK {
		t.Fatalf("CSRF recovery returned %d: %s", recovery.Code, recovery.Body.String())
	}
	if renewed := firstCookie(t, recovery.Result(), csrfCookieName); renewed.Value != csrfCookie.Value ||
		renewed.MaxAge != int(csrfCookieTTL.Seconds()) {
		t.Fatal("authenticated read did not restore the missing CSRF cookie")
	}
	if renewed := firstCookie(t, recovery.Result(), sessionCookieName); renewed.Value != sessionCookie.Value ||
		renewed.MaxAge != int(sessionTTL.Seconds()) {
		t.Fatal("CSRF recovery did not renew the matching session cookie")
	}

	empty := httptest.NewRecorder()
	request = azureRequest(t, http.MethodGet, "/api/auth/session")
	request.AddCookie(sessionCookie)
	request.AddCookie(&http.Cookie{
		Name: csrfCookieName, Value: "", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	app.Handler().ServeHTTP(empty, request)
	if empty.Code != http.StatusOK || firstCookie(t, empty.Result(), csrfCookieName).Value != csrfCookie.Value {
		t.Fatal("authenticated read did not replace an empty CSRF cookie")
	}

	denied := httptest.NewRecorder()
	request = azureRequest(t, http.MethodPost, "/api/v1/refresh")
	request.AddCookie(sessionCookie)
	app.Handler().ServeHTTP(denied, request)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF header bypassed mutation protection: %d", denied.Code)
	}
}

func TestLogoutStagesCredentialsWithoutRefreshing(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{
		membershipState: "active",
		accessExpiresIn: -60,
		refreshSucceeds: false,
	})
	app := newAzureTestApp(t, github.URL)
	sessionCookie, csrfCookie := callbackSession(t, app)

	logout := httptest.NewRecorder()
	request := azureRequest(t, http.MethodPost, "/auth/logout")
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	app.Handler().ServeHTTP(logout, request)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout returned %d: %s", logout.Code, logout.Body.String())
	}
	if !github.sawRevocation("access-old") || !github.sawRevocation("refresh-old") {
		t.Fatalf("logout did not revoke unrefreshed credentials: %#v", github.revoked)
	}
}

func TestOAuthRevocationKeysCanUseDurablePrefix(t *testing.T) {
	address, closeServer := fakeRedis(t)
	t.Cleanup(closeServer)
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	first := redisx.NewStore(client, "ephemeral-first")
	second := redisx.NewStore(client, "ephemeral-second")
	if err := first.QueueRevocation(t.Context(), "session", "sealed", "durable:"); err != nil {
		t.Fatal(err)
	}
	record, err := second.PendingRevocation(t.Context(), "durable:")
	if err != nil || record.Value != "sealed" {
		t.Fatalf("revocations did not survive namespace replacement: %#v, %v", record, err)
	}
	if err := second.CompleteRevocation(t.Context(), "session", "sealed", "durable:"); err != nil {
		t.Fatal(err)
	}
	record, err = first.PendingRevocation(t.Context(), "durable:")
	if err != nil || record.Value != "" {
		t.Fatalf("completion did not remove the stable revocation: %#v, %v", record, err)
	}
}

func TestSessionEncryptionSupportsControlledKeyRotation(t *testing.T) {
	oldSecret := "old-session-secret-0123456789abcdef"
	newSecret := "new-session-secret-0123456789abcdef"
	oldOAuth := newGitHubOAuth(GitHubOAuthConfig{SessionSecret: oldSecret}, nil, nil, nil)
	sealed, err := oldOAuth.seal([]byte("encrypted session"))
	if err != nil {
		t.Fatal(err)
	}

	rotated := newGitHubOAuth(GitHubOAuthConfig{
		SessionSecret:         newSecret,
		PreviousSessionSecret: oldSecret,
	}, nil, nil, nil)
	plain, err := rotated.open(sealed)
	if err != nil || string(plain) != "encrypted session" {
		t.Fatalf("rotated key ring could not decrypt prior session: %q, %v", plain, err)
	}
	legacy := strings.SplitN(sealed, ".", 2)[1]
	plain, err = rotated.open(legacy)
	if err != nil || string(plain) != "encrypted session" {
		t.Fatalf("rotated key ring could not decrypt legacy session: %q, %v", plain, err)
	}

	withoutPrevious := newGitHubOAuth(GitHubOAuthConfig{SessionSecret: newSecret}, nil, nil, nil)
	if _, err := withoutPrevious.open(sealed); err == nil {
		t.Fatal("session encrypted with an unavailable key was accepted")
	}
}

func TestRefreshedSessionCannotResurrectAfterRevocationStaging(t *testing.T) {
	address, closeServer := fakeRedis(t)
	t.Cleanup(closeServer)
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	oauth := newGitHubOAuth(GitHubOAuthConfig{
		SessionSecret: "session-secret-0123456789abcdef",
	}, redisx.NewStore(client, "oauth-cas-test"), redisx.NewStore(client, "oauth-cas-test"), redisx.NewStore(client, "oauth-cas-test"))
	session := oauthSession{
		ID: "session-id", Login: "octocat", AccessToken: "old",
		AccessExpires: time.Now().Add(time.Hour), RefreshExpires: time.Now().Add(24 * time.Hour),
	}
	if err := oauth.saveSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	_, expected, err := oauth.loadSessionRecord(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	refreshed := session
	refreshed.AccessToken = "refreshed"
	saved, err := oauth.saveSessionIfUnchanged(t.Context(), refreshed, expected)
	if err != nil || !saved {
		t.Fatalf("could not simulate concurrent refresh: saved=%t err=%v", saved, err)
	}
	staged, sealed, err := oauth.stageRevocation(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if staged.AccessToken != "refreshed" {
		t.Fatalf("logout queued stale credentials: %#v", staged)
	}
	session.AccessToken = "late-refresh"
	saved, err = oauth.saveSessionIfUnchanged(t.Context(), session, expected)
	if err != nil {
		t.Fatal(err)
	}
	if saved {
		t.Fatal("refresh restored a session after revocation staging")
	}
	if _, err := oauth.loadSession(t.Context(), session.ID); err == nil {
		t.Fatal("revoked session remained active")
	}

	replacement := staged
	replacement.AccessToken = "newer-queued-token"
	data, _ := json.Marshal(replacement) // #nosec G117 -- test fixture is immediately encrypted to exercise queued credential replacement.
	replacementSealed, _ := oauth.seal(data)
	if err := oauth.revocations.QueueRevocation(t.Context(), session.ID, replacementSealed, oauth.config.RevocationKeyPrefix); err != nil {
		t.Fatal(err)
	}
	if err := oauth.completeRevocation(t.Context(), session.ID, sealed); err != nil {
		t.Fatal(err)
	}
	value, err := oauth.revocations.PendingRevocation(t.Context(), oauth.config.RevocationKeyPrefix)
	if err != nil || value.Value != replacementSealed {
		t.Fatal("stale completion deleted a newer queued credential record")
	}
}

func TestFailedRevalidationCannotRevokeSupersedingSession(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	cookie, _ := callbackSession(t, app)
	session, expected, err := app.oauth.loadSessionRecord(t.Context(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	session.AuthorizedAt = time.Now().UTC()
	saved, err := app.oauth.saveSessionIfUnchanged(t.Context(), session, expected)
	if err != nil || !saved {
		t.Fatalf("could not simulate concurrent revalidation: %v", err)
	}
	response := httptest.NewRecorder()
	app.oauth.invalidateSessionIfUnchanged(response, t.Context(), session.ID, expected)
	if _, err := app.oauth.loadSession(t.Context(), session.ID); err != nil {
		t.Fatalf("superseding session was revoked: %v", err)
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("superseded revalidation cleared a live session cookie")
	}
}

func TestHostedOAuthExposesAndSwitchesCurrentAccount(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	sessionCookie, csrfCookie := callbackSession(t, app)

	current := httptest.NewRecorder()
	request := azureRequest(t, http.MethodGet, "/api/auth/session")
	request.AddCookie(sessionCookie)
	app.Handler().ServeHTTP(current, request)
	if current.Code != http.StatusOK ||
		!strings.Contains(current.Body.String(), `"login":"octocat"`) ||
		!strings.Contains(current.Body.String(), `"avatarUrl":"`+github.URL+`/avatars/octocat.png"`) {
		t.Fatalf("current account returned %d: %s", current.Code, current.Body.String())
	}

	switched := httptest.NewRecorder()
	request = azureRequest(t, http.MethodPost, "/auth/switch-account")
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	app.Handler().ServeHTTP(switched, request)
	if switched.Code != http.StatusOK || !strings.Contains(switched.Body.String(), `/auth/login?select_account=1`) {
		t.Fatalf("account switch returned %d: %s", switched.Code, switched.Body.String())
	}
	if firstCookie(t, switched.Result(), sessionCookieName).MaxAge >= 0 {
		t.Fatal("account switch did not clear the existing session cookie")
	}

	login := httptest.NewRecorder()
	app.Handler().ServeHTTP(login, azureRequest(t, http.MethodGet, "/auth/login?select_account=1"))
	location, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Query().Get("prompt") != "select_account" {
		t.Fatalf("account switch did not request GitHub account selection: %s", location.String())
	}
}

func TestHostedOAuthLoggedOutPageRequiresExplicitLogin(t *testing.T) {
	app := newAzureTestApp(t, fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600}).URL)
	response := httptest.NewRecorder()
	request := azureRequest(t, http.MethodGet, "/auth/logged-out")
	request.AddCookie(&http.Cookie{
		Name: sessionCookieName, Value: "stale-session",
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	request.AddCookie(&http.Cookie{
		Name: csrfCookieName, Value: "stale-csrf",
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	request.AddCookie(&http.Cookie{
		Name: "cao_oauth_state", Value: "stale-state",
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})

	app.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("logged-out page returned %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("logged-out page may be cached")
	}
	for name, path := range map[string]string{
		sessionCookieName: "/", csrfCookieName: "/", "cao_oauth_state": "/auth/",
	} {
		cookie := firstCookie(t, response.Result(), name)
		if cookie.Value != "" || cookie.MaxAge >= 0 || cookie.Path != path || !cookie.Secure {
			t.Errorf("logged-out page did not clear %s cookie: %#v", name, cookie)
		}
	}
	if !strings.Contains(response.Body.String(), `href="/auth/login?select_account=1"`) {
		t.Fatal("logged-out page does not offer sign-in with account selection")
	}
	if !strings.Contains(response.Body.String(), `href="https://github.com/" target="_blank" rel="noreferrer noopener"`) {
		t.Fatal("logged-out page does not offer a way to add another GitHub account")
	}
	if response.Header().Get("Location") != "" {
		t.Fatal("logged-out page unexpectedly restarted OAuth")
	}
	login := httptest.NewRecorder()
	app.Handler().ServeHTTP(login, azureRequest(t, http.MethodGet, "/auth/login?select_account=1"))
	location, err := url.Parse(login.Header().Get("Location"))
	if err != nil || location.Query().Get("prompt") != "select_account" {
		t.Fatalf("retry did not ask GitHub to select an account: %s (%v)", login.Header().Get("Location"), err)
	}
}

func TestHostedOAuthLogsBranchesWithoutCredentialValues(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	app.database = integrationDatabase(t)
	var branches []string
	app.oauth.log = func(_ context.Context, branch string) {
		branches = append(branches, branch)
	}

	sessionCookie, csrfCookie := callbackSession(t, app)
	current := httptest.NewRecorder()
	request := azureRequest(t, http.MethodGet, "/api/auth/session")
	request.AddCookie(sessionCookie)
	app.Handler().ServeHTTP(current, request)

	app.Handler().ServeHTTP(httptest.NewRecorder(), azureRequest(t, http.MethodGet, "/api/health"))
	health := httptest.NewRecorder()
	request = azureRequest(t, http.MethodGet, "/api/health")
	request.AddCookie(sessionCookie)
	app.Handler().ServeHTTP(health, request)

	rejected := httptest.NewRecorder()
	request = azureRequest(t, http.MethodPost, "/auth/logout")
	request.AddCookie(sessionCookie)
	app.Handler().ServeHTTP(rejected, request)

	logout := authenticatedAuthMutation(t, app, "/auth/logout", sessionCookie, csrfCookie)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout returned %d: %s", logout.Code, logout.Body.String())
	}
	app.Handler().ServeHTTP(httptest.NewRecorder(), azureRequest(t, http.MethodGet, "/auth/logged-out"))

	for _, expected := range []string{
		"access.public_allowed",
		"login.account_selection_requested",
		"exchange.succeeded",
		"identity.loaded",
		"organization_membership.active",
		"authorization.organization_allowed",
		"callback.succeeded",
		"access.safe_method",
		"current_account.succeeded",
		"health.details_redacted",
		"health.details_authorized",
		"access.csrf_rejected",
		"session_clear.revocation_staged",
		"session_clear.revocation_completed",
		"logout.succeeded",
		"logged_out.rendered",
	} {
		if !slices.Contains(branches, expected) {
			t.Errorf("missing authentication branch log %q in %v", expected, branches)
		}
	}
	logged := strings.Join(branches, "\n")
	for _, secret := range []string{
		sessionCookie.Value,
		csrfCookie.Value,
		"access-old",
		"refresh-old",
		"octocat",
		app.oauth.config.ClientSecret,
		app.oauth.config.SessionSecret,
	} {
		if secret != "" && strings.Contains(logged, secret) {
			t.Errorf("authentication branch logs contain sensitive value %q", secret)
		}
	}
}

func TestOAuthBranchLogsUseFixedIdentifiers(t *testing.T) {
	for _, path := range []string{"oauth.go", "operations.go", "server.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "logBranch" && selector.Sel.Name != "logAuthBranch") {
				return true
			}
			if len(call.Args) != 2 {
				t.Errorf("%s logBranch call must have a context and fixed branch identifier", path)
				return true
			}
			literal, ok := call.Args[1].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Errorf("%s logBranch argument must be a fixed string literal", path)
				return true
			}
			branch, err := strconv.Unquote(literal.Value)
			if err != nil || branch == "" || strings.ContainsAny(branch, " \t\r\n%") {
				t.Errorf("%s logBranch identifier %q is invalid", path, literal.Value)
			}
			return true
		})
	}
}

func TestHostedOAuthQueuesFailedCredentialRevocation(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{
		membershipState:  "active",
		accessExpiresIn:  3600,
		rejectRevocation: true,
	})
	app := newAzureTestApp(t, github.URL)
	sessionCookie, csrfCookie := callbackSession(t, app)

	switched := httptest.NewRecorder()
	request := azureRequest(t, http.MethodPost, "/auth/switch-account")
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	app.Handler().ServeHTTP(switched, request)
	if switched.Code != http.StatusOK {
		t.Fatalf("failed account revocation returned %d: %s", switched.Code, switched.Body.String())
	}
	if firstCookie(t, switched.Result(), sessionCookieName).MaxAge >= 0 {
		t.Fatal("failed account revocation did not clear the active session cookie")
	}

	current := httptest.NewRecorder()
	request = azureRequest(t, http.MethodGet, "/api/auth/session")
	request.AddCookie(sessionCookie)
	app.Handler().ServeHTTP(current, request)
	if current.Code != http.StatusUnauthorized {
		t.Fatalf("failed account revocation retained an active server session: %d", current.Code)
	}

	github.rejectRevocation = false
	app.oauth.retryPendingRevocations(t.Context(), 8)
	if !github.sawRevocation("access-old") || !github.sawRevocation("refresh-old") {
		t.Fatalf("queued credentials were not revoked on retry: %#v", github.revoked)
	}
}

func TestAzureProxyPolicyFailsClosed(t *testing.T) {
	app := newAzureTestApp(t, fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600}).URL)
	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://evil.example/api/v1/health", nil)
	request.Host = "evil.example"
	request.Header.Set("X-Forwarded-Proto", "https")
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusMisdirectedRequest {
		t.Fatalf("unexpected proxy rejection status: %d", response.Code)
	}
}

type fakeGitHubOptions struct {
	membershipState          string
	refreshedMembershipState string
	accessExpiresIn          int64
	refreshSucceeds          bool
	rejectRevocation         bool
	omitExpiryMetadata       bool
	omitRefreshToken         bool
	// nil grants the requested scopes; a non-nil slice models reduced consent.
	grantedScopes         []string
	appDisclosesTwoFactor bool
}

type fakeOAuthGrant struct {
	clientID string
	scopes   []string
}

func (grant fakeOAuthGrant) isGitHubApp() bool {
	return strings.HasPrefix(grant.clientID, "Iv")
}

func (grant fakeOAuthGrant) hasScope(scope string) bool {
	return slices.Contains(grant.scopes, scope)
}

type fakeGitHubServer struct {
	*httptest.Server
	revoked          []string
	rejectRevocation bool
	membershipState  string
	onUserRequest    func()
	// twoFactor controls the /user two_factor_authentication field: nil
	// omits it, matching tokens GitHub does not disclose it to.
	twoFactor          *bool
	grantMu            sync.Mutex
	grant              fakeOAuthGrant
	tokens             map[string]fakeOAuthGrant
	login              string
	accountID          int64
	accessToken        string
	refreshToken       string
	membershipRequests int
}

// assuredSession models recent OAuth completion with disclosed 2FA enabled,
// not a freshly performed second-factor challenge.
func assuredSession(login string) oauthSession {
	return oauthSession{Login: login, AuthenticatedAt: time.Now().UTC(), Assurance: []string{assuranceTwoFactor}}
}

func fakeGitHub(t *testing.T, options fakeGitHubOptions) *fakeGitHubServer {
	t.Helper()
	twoFactor := true
	server := &fakeGitHubServer{
		twoFactor: &twoFactor, tokens: make(map[string]fakeOAuthGrant),
		login: "octocat", accountID: 1, accessToken: "access-old", refreshToken: "refresh-old",
	}
	server.rejectRevocation = options.rejectRevocation
	server.membershipState = options.membershipState
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/authorize", func(response http.ResponseWriter, request *http.Request) {
		scopes := strings.Fields(request.URL.Query().Get("scope"))
		if options.grantedScopes != nil {
			scopes = options.grantedScopes
		}
		server.grantMu.Lock()
		server.grant = fakeOAuthGrant{clientID: request.URL.Query().Get("client_id"), scopes: scopes}
		server.grantMu.Unlock()
		response.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/login/oauth/access_token", func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Form.Get("grant_type") == "refresh_token" {
			if !options.refreshSucceeds {
				response.WriteHeader(http.StatusUnauthorized)
				return
			}
			server.grantMu.Lock()
			server.tokens["access-new"] = server.tokens["access-old"]
			server.grantMu.Unlock()
			// #nosec G117 -- test fixture intentionally models GitHub's token endpoint JSON.
			_ = json.NewEncoder(response).Encode(tokenResponse{AccessToken: "access-new", RefreshToken: "refresh-new", ExpiresIn: 3600, RefreshTokenExpiresIn: 7200})
			return
		}
		server.grantMu.Lock()
		accessToken, refreshToken := server.accessToken, server.refreshToken
		server.tokens[accessToken] = server.grant
		server.grantMu.Unlock()
		tokens := tokenResponse{AccessToken: accessToken, RefreshToken: refreshToken, ExpiresIn: options.accessExpiresIn, RefreshTokenExpiresIn: 7200}
		if options.omitExpiryMetadata {
			tokens.ExpiresIn, tokens.RefreshTokenExpiresIn = 0, 0
		}
		if options.omitRefreshToken {
			tokens.RefreshToken = ""
		}
		// #nosec G117 -- test fixture intentionally models GitHub's token endpoint JSON.
		_ = json.NewEncoder(response).Encode(tokens)
	})
	mux.HandleFunc("/user", func(response http.ResponseWriter, request *http.Request) {
		if server.onUserRequest != nil {
			server.onUserRequest()
		}
		server.grantMu.Lock()
		payload := map[string]any{
			"id":         server.accountID,
			"login":      server.login,
			"avatar_url": server.URL + "/avatars/octocat.png",
		}
		server.grantMu.Unlock()
		grant := server.tokenGrant(request.Header.Get("Authorization"))
		disclosesPrivateUser := grant.hasScope("read:user") || grant.hasScope("user")
		if grant.isGitHubApp() {
			disclosesPrivateUser = options.appDisclosesTwoFactor
		}
		if server.twoFactor != nil && disclosesPrivateUser {
			payload["two_factor_authentication"] = *server.twoFactor
		}
		_ = json.NewEncoder(response).Encode(payload)
	})
	mux.HandleFunc("/user/memberships/orgs/example", func(response http.ResponseWriter, request *http.Request) {
		server.grantMu.Lock()
		server.membershipRequests++
		server.grantMu.Unlock()
		state := server.membershipState
		if options.refreshedMembershipState != "" && request.Header.Get("Authorization") == "Bearer "+"access-new" {
			state = options.refreshedMembershipState
		}
		_ = json.NewEncoder(response).Encode(map[string]string{"state": state})
	})
	mux.HandleFunc("/applications/{client_id}/token", func(response http.ResponseWriter, request *http.Request) {
		clientID, clientSecret, ok := request.BasicAuth()
		if !ok || clientID != request.PathValue("client_id") || clientSecret != "secret" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if server.rejectRevocation {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var payload map[string]string
		_ = json.NewDecoder(request.Body).Decode(&payload)
		server.revoked = append(server.revoked, payload["access_token"])
		response.WriteHeader(http.StatusNoContent)
	})
	server.Server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func (server *fakeGitHubServer) tokenGrant(authorization string) fakeOAuthGrant {
	server.grantMu.Lock()
	defer server.grantMu.Unlock()
	return server.tokens[strings.TrimPrefix(authorization, "Bearer ")]
}

func (server *fakeGitHubServer) sawRevocation(token string) bool {
	for _, candidate := range server.revoked {
		if candidate == token {
			return true
		}
	}
	return false
}

func newAzureTestApp(t *testing.T, githubURL string) *App {
	t.Helper()
	address, closeServer := fakeRedis(t)
	t.Cleanup(closeServer)
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	site := t.TempDir()
	if err := os.WriteFile(site+"/index.html", []byte("<html><head></head></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{
		Database:      constructorDatabase(),
		HostProfile:   azureFunctionsHostProfile(),
		SiteDirectory: site,
		Proxy:         ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true},
		GitHubOAuth:   validOAuthConfig(githubURL),
	}
	app, err := New(context.Background(), redisx.NewStore(client, "test"), config)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func validOAuthConfig(baseURL string) *GitHubOAuthConfig {
	return &GitHubOAuthConfig{
		ClientID:             "client",
		ClientSecret:         "secret",
		RedirectURL:          "https://dashboard.example.com/auth/callback",
		SessionSecret:        "0123456789abcdef0123456789abcdef",
		AllowedOrganizations: []string{"example"},
		AuthURL:              baseURL + "/login/oauth/authorize",
		TokenURL:             baseURL + "/login/oauth/access_token",
		UserURL:              baseURL + "/user",
		OrgMembershipURL:     baseURL + "/user/memberships/orgs/{org}",
		RevokeURL:            baseURL + "/applications/{client_id}/token",
	}
}

func azureRequest(t *testing.T, method, path string) *http.Request {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, "https://dashboard.example.com"+path, strings.NewReader(""))
	request.Host = "dashboard.example.com"
	request.Header.Set("X-Forwarded-Host", "dashboard.example.com")
	request.Header.Set("X-Forwarded-Proto", "https")
	return request
}

func loginState(t *testing.T, app *App) (*http.Cookie, string) {
	t.Helper()
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, azureRequest(t, http.MethodGet, "/auth/login"))
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	authorizeFakeGitHub(t, app, location.String())
	return firstCookie(t, response.Result(), "cao_oauth_state"), location.Query().Get("state")
}

func authorizeFakeGitHub(t *testing.T, app *App, location string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, location, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := app.oauth.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("fake GitHub authorization returned %d", response.StatusCode)
	}
}

func callbackSession(t *testing.T, app *App) (*http.Cookie, *http.Cookie) {
	t.Helper()
	stateCookie, state := loginState(t, app)
	response := oauthCallbackResponse(t, app, stateCookie, state)
	if response.Code != http.StatusFound {
		t.Fatalf("callback returned %d: %s", response.Code, response.Body.String())
	}
	return firstCookie(t, response.Result(), sessionCookieName), firstCookie(t, response.Result(), csrfCookieName)
}

func oauthCallbackResponse(t *testing.T, app *App, stateCookie *http.Cookie, state string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	request := azureRequest(t, http.MethodGet, "/auth/callback?code=code-1&state="+url.QueryEscape(state))
	request.AddCookie(stateCookie)
	for _, cookie := range cookies {
		if cookie != nil {
			request.AddCookie(cookie)
		}
	}
	app.Handler().ServeHTTP(response, request)
	return response
}

func firstCookie(t *testing.T, response *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing cookie %s in %#v", name, response.Cookies())
	return nil
}
