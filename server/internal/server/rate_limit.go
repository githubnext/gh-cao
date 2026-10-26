package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

const (
	generalRateLimit  = 120
	generalRateWindow = time.Minute
	queryRateLimit    = 30
	queryRateWindow   = time.Minute
	authRateLimit     = 10
	authRateWindow    = 5 * time.Minute
	edgeRateLimit     = 1200
	edgeRateWindow    = time.Minute
	publicRateLimit   = 600
	publicRateWindow  = time.Minute
	rateLimitTimeout  = 2 * time.Second
)

// RateLimitPolicy overrides one inbound request token bucket. Zero values keep
// the built-in default for that field.
type RateLimitPolicy struct {
	Capacity int
	Window   time.Duration
}

// RateLimitConfig overrides the inbound request rate limits. The zero value
// selects the production defaults; load tests lower them to exercise
// throttling deterministically.
type RateLimitConfig struct {
	General RateLimitPolicy
	Query   RateLimitPolicy
	Auth    RateLimitPolicy
	Edge    RateLimitPolicy
	Public  RateLimitPolicy
}

func (c RateLimitConfig) validate() error {
	for name, policy := range map[string]RateLimitPolicy{
		"general": c.General, "query": c.Query, "auth": c.Auth, "edge": c.Edge,
		"public": c.Public,
	} {
		if policy.Capacity < 0 {
			return fmt.Errorf("%s rate limit capacity cannot be negative", name)
		}
		if policy.Window != 0 && policy.Window < time.Millisecond {
			return fmt.Errorf("%s rate limit window must be at least one millisecond", name)
		}
	}
	return nil
}

func (c RateLimitConfig) policy(name string) requestRatePolicy {
	var override RateLimitPolicy
	resolved := requestRatePolicy{name: name}
	switch name {
	case "general":
		override, resolved.capacity, resolved.window = c.General, generalRateLimit, generalRateWindow
	case "query":
		override, resolved.capacity, resolved.window = c.Query, queryRateLimit, queryRateWindow
	case "auth":
		override, resolved.capacity, resolved.window = c.Auth, authRateLimit, authRateWindow
	case "edge":
		override, resolved.capacity, resolved.window = c.Edge, edgeRateLimit, edgeRateWindow
	case "public":
		override, resolved.capacity, resolved.window = c.Public, publicRateLimit, publicRateWindow
	}
	if override.Capacity > 0 {
		resolved.capacity = override.Capacity
	}
	if override.Window > 0 {
		resolved.window = override.Window
	}
	return resolved
}

type requestRatePolicy struct {
	name     string
	capacity int
	window   time.Duration
}

type rateLimitReservation struct {
	key    string
	policy requestRatePolicy
}

type rateLimitReservationContextKey struct{}

func (a *App) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		policy, limited := ratePolicy(request, a.config.RateLimits)
		if !limited {
			next.ServeHTTP(response, request)
			return
		}
		a.enforceRateLimit(response, request, next, policy, a.rateLimitSubject(request))
	})
}

// preAuthRateLimit bounds work performed while loading or refreshing a session.
// It deliberately uses only the client address because authenticated identity is
// not available until the access middleware has completed.
func (a *App) preAuthRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if a.oauth == nil || !a.validRateLimitBoundary(request) {
			next.ServeHTTP(response, request)
			return
		}
		if publicServiceEndpoint(request.URL.Path) {
			if request.Method == http.MethodGet || request.Method == http.MethodHead {
				a.enforceRateLimit(response, request, next, a.config.RateLimits.policy("public"), "client:"+a.clientIP(request))
			} else {
				next.ServeHTTP(response, request)
			}
			return
		}
		if !requiresPreAuthRateLimit(request.URL.Path) {
			next.ServeHTTP(response, request)
			return
		}
		policy := a.config.RateLimits.policy("edge")
		a.enforceRateLimit(response, request, next, policy, "client:"+a.clientIP(request))
	})
}

// validRateLimitBoundary prevents an invalid Host or forwarded-host request
// from reaching Redis before the access middleware rejects it. It also ensures
// clientIP only sees forwarding headers after the trusted boundary is verified.
func (a *App) validRateLimitBoundary(request *http.Request) bool {
	return validProxyRequest(request, a.proxyPolicy())
}

func (a *App) enforceRateLimit(
	response http.ResponseWriter,
	request *http.Request,
	next http.Handler,
	policy requestRatePolicy,
	subject string,
) {
	sum := sha256.Sum256([]byte(subject))
	key := policy.name + ":" + hex.EncodeToString(sum[:])
	ctx, cancel := context.WithTimeout(request.Context(), rateLimitTimeout)
	defer cancel()
	result, err := a.services.RequestLimiter.TakeRateLimitToken(ctx, key, policy.capacity, policy.window)
	if err != nil {
		serverLog.Printf("rate limit unavailable policy=%s", policy.name)
		writeError(response, http.StatusServiceUnavailable, "request rate limiter is unavailable")
		return
	}
	resetSeconds := cooldownSeconds(result.ResetAfter)
	response.Header().Set("RateLimit-Limit", strconv.Itoa(policy.capacity))
	response.Header().Set("RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
	response.Header().Set("RateLimit-Reset", strconv.Itoa(resetSeconds))
	response.Header().Set(
		"RateLimit-Policy",
		strconv.Itoa(policy.capacity)+";w="+strconv.FormatInt(int64(policy.window/time.Second), 10),
	)
	if !result.Allowed {
		response.Header().Set("Retry-After", strconv.Itoa(cooldownSeconds(result.RetryAfter)))
		writeError(response, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	if policy.name == "query" {
		request = request.WithContext(context.WithValue(
			request.Context(),
			rateLimitReservationContextKey{},
			rateLimitReservation{key: key, policy: policy},
		))
	}
	next.ServeHTTP(response, request)
}

func (a *App) chargeQueryRateLimit(
	ctx context.Context,
	response http.ResponseWriter,
	cost int,
) (int, error) {
	reservation, ok := ctx.Value(rateLimitReservationContextKey{}).(rateLimitReservation)
	if !ok || cost <= 1 {
		return http.StatusOK, nil
	}
	cost = min(cost, reservation.policy.capacity)
	chargeCtx, cancel := context.WithTimeout(ctx, rateLimitTimeout)
	defer cancel()
	result, err := a.services.RequestLimiter.TakeRateLimitTokens(
		chargeCtx,
		reservation.key,
		reservation.policy.capacity,
		reservation.policy.window,
		cost-1,
	)
	if err != nil {
		serverLog.Printf("rate limit unavailable policy=%s", reservation.policy.name)
		return http.StatusServiceUnavailable, errors.New("request rate limiter is unavailable")
	}
	if response != nil {
		response.Header().Set("RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
		response.Header().Set("RateLimit-Reset", strconv.Itoa(cooldownSeconds(result.ResetAfter)))
	}
	if !result.Allowed {
		if response != nil {
			response.Header().Set("Retry-After", strconv.Itoa(cooldownSeconds(result.RetryAfter)))
		}
		return http.StatusTooManyRequests, errors.New("rate limit exceeded")
	}
	return http.StatusOK, nil
}

func queryRateLimitCost(metrics model.Metrics) int {
	durationCost := int((metrics.DurationMS + 999) / 1000)
	operationCost := ceilingUnits(metrics.Operations, 250_000)
	rowCost := ceilingUnits(metrics.PeakWorkingRows, 100_000)
	memoryCost := ceilingUnits64(metrics.PeakWorkingBytes, 16<<20)
	cost := max(1, durationCost, operationCost, rowCost, memoryCost)
	return min(cost, queryRateLimit)
}

func ceilingUnits(value, unit int) int {
	if value <= 0 {
		return 0
	}
	return (value + unit - 1) / unit
}

func ceilingUnits64(value int64, unit int64) int {
	if value <= 0 {
		return 0
	}
	return int((value + unit - 1) / unit)
}

func requiresPreAuthRateLimit(path string) bool {
	return !publicServiceEndpoint(path) &&
		!strings.HasPrefix(path, "/auth/logged-out")
}

func ratePolicy(request *http.Request, limits RateLimitConfig) (requestRatePolicy, bool) {
	switch {
	case publicServiceEndpoint(request.URL.Path), request.URL.Path == "/api/github/webhook":
		return requestRatePolicy{}, false
	case request.URL.Path == "/auth/login", request.URL.Path == "/auth/callback":
		return limits.policy("auth"), true
	case request.URL.Path == "/api/v1/query", request.URL.Path == "/api/v1/session-archive/query", request.URL.Path == "/mcp":
		return limits.policy("query"), true
	case strings.HasPrefix(request.URL.Path, "/api/"), strings.HasPrefix(request.URL.Path, "/auth/"):
		return limits.policy("general"), true
	default:
		return requestRatePolicy{}, false
	}
}

func (a *App) rateLimitSubject(request *http.Request) string {
	if session, ok := request.Context().Value(oauthSessionContextKey{}).(oauthSession); ok &&
		strings.TrimSpace(session.Login) != "" {
		return "user:" + strings.ToLower(strings.TrimSpace(session.Login))
	}
	if actor, ok := request.Context().Value(githubActionsActorContextKey{}).(string); ok && actor != "" {
		return "actions-actor:" + actor
	}
	if request.URL.Path == "/auth/callback" && a.oauth != nil && a.oauth.validState(request) {
		if cookie, err := request.Cookie("cao_oauth_state"); err == nil && cookie.Value != "" {
			return "oauth-state:" + cookie.Value
		}
	}
	return "client:" + a.clientIP(request)
}

// clientIPSource names which input resolveClientIP used to produce a
// client address. It is stable across header-content and address-value
// changes, so it is useful to log without exposing the resolved address
// itself.
type clientIPSource string

const (
	clientIPSourceUntrustedBoundary clientIPSource = "untrusted-boundary"
	clientIPSourceForwardedFor      clientIPSource = "x-forwarded-for"
	clientIPSourceForwardedHeader   clientIPSource = "forwarded-header"
	clientIPSourceRemoteFallback    clientIPSource = "remote-fallback"
)

// resolveClientIP applies the standard priority for the caller's address
// visible to rate limiting: a trusted X-Forwarded-For value, then a trusted
// RFC 7239 Forwarded value, then the direct TCP peer address. It is a pure
// function over the already-extracted header values and trust decision, so
// every branch — an untrusted boundary, each forwarded header, and a
// malformed forwarded value falling back to the peer address — is testable
// without constructing an *http.Request. It returns the resolved address and
// which input supplied it, so callers can log the source without exposing
// the address itself.
func resolveClientIP(trustBoundary bool, forwardedFor, forwardedHeaderValue, remoteAddress string) (string, clientIPSource) {
	if !trustBoundary {
		return remoteIP(remoteAddress), clientIPSourceUntrustedBoundary
	}
	if forwardedFor != "" {
		if ip := parseForwardedIP(forwardedFor); ip != nil {
			return ip.String(), clientIPSourceForwardedFor
		}
		return remoteIP(remoteAddress), clientIPSourceRemoteFallback
	}
	if ip := parseForwardedFor(forwardedHeaderValue); ip != nil {
		return ip.String(), clientIPSourceForwardedHeader
	}
	return remoteIP(remoteAddress), clientIPSourceRemoteFallback
}

func (a *App) clientIP(request *http.Request) string {
	policy := a.proxyPolicy()
	trustBoundary := policy.TrustForwarded &&
		(len(policy.TrustedProxyPrefixes) == 0 || trustedProxyPeer(request.RemoteAddr, policy.TrustedProxyPrefixes))
	ip, source := resolveClientIP(
		trustBoundary,
		forwardedHeader(request, "X-Forwarded-For"),
		forwardedHeader(request, "Forwarded"),
		request.RemoteAddr,
	)
	serverLog.Printf("client address resolved source=%s", source)
	return ip
}

func remoteIP(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
		return host
	}
	if ip := net.ParseIP(address); ip != nil {
		return ip.String()
	}
	return "unknown"
}

func parseForwardedIP(value string) net.IP {
	value = strings.TrimSpace(value)
	if ip := net.ParseIP(value); ip != nil {
		return ip
	}
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

func parseForwardedFor(value string) net.IP {
	for _, parameter := range strings.Split(value, ";") {
		name, candidate, found := strings.Cut(parameter, "=")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "for") {
			continue
		}
		candidate = strings.Trim(strings.TrimSpace(candidate), `"`)
		return parseForwardedIP(candidate)
	}
	return nil
}

func cooldownSeconds(duration time.Duration) int {
	seconds := int((duration + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}
