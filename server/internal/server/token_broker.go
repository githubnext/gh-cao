package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/tokenbroker"
)

const tokenBrokerPath = "/api/token-broker"

func tokenBrokerConfigFromEnv() (*tokenbroker.Config, error) {
	const prefix = "CAO_TOKEN_BROKER_"
	enabled := os.Getenv(prefix + "ENABLED")
	if enabled == "" || enabled == "false" {
		return nil, nil //nolint:nilnil // This optional service is disabled unless explicitly selected.
	}
	if enabled != "true" {
		return nil, errors.New("CAO_TOKEN_BROKER_ENABLED must be true or false")
	}
	appID, appErr := strconv.ParseInt(os.Getenv(prefix+"APP_ID"), 10, 64)
	installationID, installationErr := strconv.ParseInt(os.Getenv(prefix+"INSTALLATION_ID"), 10, 64)
	if appErr != nil || installationErr != nil || appID <= 0 || installationID <= 0 {
		return nil, errors.New("token broker requires positive App and installation IDs")
	}
	return &tokenbroker.Config{
		Audience: os.Getenv(prefix + "URL"), PolicyFile: configuredHostPolicyPath(),
		AppID: appID, InstallationID: installationID, OwnerID: os.Getenv(prefix + "OWNER_ID"),
		KeyVaultSecretURL:       os.Getenv(prefix + "KEY_VAULT_SECRET_URL"),
		ManagedIdentityClientID: os.Getenv(prefix + "MANAGED_IDENTITY_CLIENT_ID"),
	}, nil
}

func newTokenBroker(config Config) (*tokenbroker.Service, error) {
	if config.TokenBroker == nil {
		return nil, nil //nolint:nilnil // Absence must not enable a credential issuer.
	}
	if config.HostProfile.Authentication != HostAuthenticationOAuth || !config.Proxy.RequireHTTPS ||
		config.HostProfile.Listener != HostListenerProcess || !config.SingleReplicaConfirmed ||
		!config.TokenBrokerSingleProcessConfirmed {
		return nil, errors.New("token broker requires hosted HTTPS and a confirmed single process replica")
	}
	endpoint, err := url.Parse(config.TokenBroker.Audience)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil ||
		endpoint.Path != tokenBrokerPath || endpoint.RawPath != "" || endpoint.RawQuery != "" ||
		endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.Port() != "" ||
		strings.ContainsAny(config.TokenBroker.Audience, "\\ \t\r\n") {
		return nil, errors.New("token broker URL must be the exact public HTTPS /api/token-broker endpoint")
	}
	allowed := false
	for _, host := range config.Proxy.AllowedHosts {
		if strings.EqualFold(host, endpoint.Hostname()) {
			allowed = true
		}
	}
	if !allowed {
		return nil, errors.New("token broker URL must use a reviewed allowed host")
	}
	return tokenbroker.New(*config.TokenBroker)
}

func (a *App) exchangeBrokerToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.tokenBroker == nil || r.Method != http.MethodPost || !validProxyRequest(r, a.proxyPolicy()) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	a.startMu.Lock()
	unavailable := a.draining
	a.startMu.Unlock()
	if unavailable {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	sum := sha256.Sum256([]byte(a.clientIP(r)))
	limit, err := a.services.RequestLimiter.TakeRateLimitToken(ctx,
		"token-broker:"+hex.EncodeToString(sum[:]), 60, time.Minute)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !limit.Allowed {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	controller := http.NewResponseController(w)
	deadline, _ := ctx.Deadline()
	if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
	if err := controller.SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer func() {
		_ = controller.Flush()
		_ = controller.SetWriteDeadline(time.Time{})
	}()
	http.TimeoutHandler(a.tokenBroker, time.Until(deadline), "").ServeHTTP(
		&admissionResponse{ResponseWriter: w, headers: make(http.Header)}, r.WithContext(ctx))
}
