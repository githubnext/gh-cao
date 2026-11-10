package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var corsLog = logger.New("cao:server:cors")

// corsHostnamePattern accepts ASCII (punycode) DNS names only. Browsers
// serialize Origin hosts in ASCII, so Unicode, underscore, empty-label, or
// trailing-dot entries could never match and would only obscure the policy.
var corsHostnamePattern = regexp.MustCompile(`^[a-z0-9-]+(?:\.[a-z0-9-]+)*$`)

const (
	maxCORSAllowedOrigins = 32
	defaultCORSMaxAge     = 600
	maxCORSMaxAge         = 86400
	corsAllowedMethods    = "GET, HEAD"
	corsAllowedHeaders    = "Traceparent"
)

// CORSPolicy is the reviewed cross-origin policy from
// control-plane.web.host.cors. The zero value keeps the dashboard strictly
// same-origin: no Access-Control-* headers are ever emitted.
//
// CORS is credential-less by design. The server never emits
// Access-Control-Allow-Credentials, so browsers refuse to expose any
// response to a cross-origin request made with cookies. A listed origin can
// read only what an anonymous caller can read (public health endpoints and
// 401 responses); it can never act as, or read data of, the signed-in user,
// including the CSRF token from /api/auth/session.
type CORSPolicy struct {
	AllowedOrigins []string
	MaxAge         int
}

type corsPolicyDocument struct {
	AllowedOrigins []string `json:"allowed-origins"`
	MaxAge         *int     `json:"max-age"`
}

func (document corsPolicyDocument) resolve() (CORSPolicy, error) {
	policy := CORSPolicy{
		AllowedOrigins: document.AllowedOrigins,
		MaxAge:         defaultCORSMaxAge,
	}
	if document.MaxAge != nil {
		if *document.MaxAge < 1 {
			return CORSPolicy{}, fmt.Errorf("cors max-age must be between 1 and %d seconds", maxCORSMaxAge)
		}
		policy.MaxAge = *document.MaxAge
	}
	return policy.normalize()
}

// normalize validates the policy and returns a copy with canonical,
// deduplicated origins. It fails closed on wildcards, opaque origins, paths,
// credentials in the origin, or plaintext origins other than loopback.
func (policy CORSPolicy) normalize() (CORSPolicy, error) {
	if len(policy.AllowedOrigins) == 0 {
		return CORSPolicy{}, nil
	}
	if len(policy.AllowedOrigins) > maxCORSAllowedOrigins {
		return CORSPolicy{}, fmt.Errorf("cors allowed-origins accepts at most %d origins", maxCORSAllowedOrigins)
	}
	if policy.MaxAge == 0 {
		policy.MaxAge = defaultCORSMaxAge
	}
	if policy.MaxAge < 0 || policy.MaxAge > maxCORSMaxAge {
		return CORSPolicy{}, fmt.Errorf("cors max-age must be between 1 and %d seconds", maxCORSMaxAge)
	}
	seen := map[string]bool{}
	origins := make([]string, 0, len(policy.AllowedOrigins))
	for _, value := range policy.AllowedOrigins {
		origin, err := canonicalCORSOrigin(value)
		if err != nil {
			return CORSPolicy{}, err
		}
		if !seen[origin] {
			seen[origin] = true
			origins = append(origins, origin)
		}
	}
	policy.AllowedOrigins = origins
	return policy, nil
}

func canonicalCORSOrigin(value string) (string, error) {
	invalid := errors.New("cors allowed-origins entries must be exact https origins such as https://example.com")
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "*") {
		return "", invalid
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Host == "" ||
		parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		strings.HasSuffix(value, "?") || strings.HasSuffix(value, "#") ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", invalid
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Host)
	if hostname := strings.ToLower(parsed.Hostname()); net.ParseIP(hostname) == nil &&
		!corsHostnamePattern.MatchString(hostname) {
		return "", invalid
	}
	switch scheme {
	case "https":
	case "http":
		hostname := strings.Trim(parsed.Hostname(), "[]")
		ip := net.ParseIP(hostname)
		if !strings.EqualFold(hostname, "localhost") && (ip == nil || !ip.IsLoopback()) {
			return "", invalid
		}
	default:
		return "", invalid
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", invalid
		}
		if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
			host = strings.ToLower(parsed.Hostname())
			if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
		}
	}
	return scheme + "://" + host, nil
}

func (policy CORSPolicy) allows(origin string) bool {
	for _, allowed := range policy.AllowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

// corsOutcome classifies how the cors middleware disposed of one request,
// so the decision is testable and logged without exposing the request's
// Origin, Host, or forwarded-header values.
type corsOutcome string

const (
	// corsOutcomeNotCrossOrigin covers requests the middleware leaves
	// entirely unchanged: no Origin header, an origin outside the
	// allowlist, or a request whose host fails the same validation the
	// access middleware applies.
	corsOutcomeNotCrossOrigin corsOutcome = "not-cross-origin"
	// corsOutcomePreflightRejected is an allowed-origin preflight for a
	// method the policy never permits.
	corsOutcomePreflightRejected corsOutcome = "preflight-rejected"
	// corsOutcomePreflightAllowed is an allowed-origin preflight answered
	// with the policy's methods, headers, and max-age.
	corsOutcomePreflightAllowed corsOutcome = "preflight-allowed"
	// corsOutcomeSimpleAllowed is a non-preflight request from an allowed
	// origin, forwarded to next with Access-Control-Allow-Origin set.
	corsOutcomeSimpleAllowed corsOutcome = "simple-allowed"
)

// classifyCORSRequest applies cors's decision tree over one request against
// policy and the request's host validity, without touching the response. It
// is extracted from cors's http.HandlerFunc so the outcome for every
// combination of origin, method, and host validity is directly testable
// against a *http.Request, and so the outcome can be logged by name without
// exposing the request's Origin, Host, or forwarded-header values.
func classifyCORSRequest(request *http.Request, policy CORSPolicy, validHost bool) corsOutcome {
	origin := request.Header.Get("Origin")
	if origin == "" || !policy.allows(origin) || !validHost {
		return corsOutcomeNotCrossOrigin
	}
	if requested := request.Header.Get("Access-Control-Request-Method"); request.Method == http.MethodOptions && requested != "" {
		if requested != http.MethodGet && requested != http.MethodHead {
			return corsOutcomePreflightRejected
		}
		return corsOutcomePreflightAllowed
	}
	return corsOutcomeSimpleAllowed
}

// cors applies the reviewed cross-origin policy before authentication so a
// preflight never needs credentials. Requests from origins outside the
// allowlist receive no Access-Control-* headers and are otherwise unchanged.
func (a *App) cors(next http.Handler) http.Handler {
	policy := a.config.CORS
	if len(policy.AllowedOrigins) == 0 {
		return next
	}
	maxAge := strconv.Itoa(policy.MaxAge)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		headers := response.Header()
		headers.Add("Vary", "Origin")
		outcome := classifyCORSRequest(request, policy, a.validCORSRequestHost(request))
		corsLog.PrintfContext(request.Context(), "cors request classified outcome=%s", outcome)
		if outcome == corsOutcomeNotCrossOrigin {
			next.ServeHTTP(response, request)
			return
		}
		origin := request.Header.Get("Origin")
		headers.Set("Access-Control-Allow-Origin", origin)
		switch outcome {
		case corsOutcomePreflightRejected:
			headers.Add("Vary", "Access-Control-Request-Method")
			headers.Add("Vary", "Access-Control-Request-Headers")
			response.WriteHeader(http.StatusNoContent)
		case corsOutcomePreflightAllowed:
			headers.Add("Vary", "Access-Control-Request-Method")
			headers.Add("Vary", "Access-Control-Request-Headers")
			headers.Set("Access-Control-Allow-Methods", corsAllowedMethods)
			headers.Set("Access-Control-Allow-Headers", corsAllowedHeaders)
			headers.Set("Access-Control-Max-Age", maxAge)
			response.WriteHeader(http.StatusNoContent)
		default:
			next.ServeHTTP(response, request)
		}
	})
}

// validCORSRequestHost applies the same host validation as the access
// middleware so a preflight answered before authentication can never succeed
// for a request that access control would reject as misdirected.
func (a *App) validCORSRequestHost(request *http.Request) bool {
	if a.oauth != nil {
		return validProxyRequest(request, a.proxyPolicy())
	}
	return validLocalRequestHost(request.Host)
}
