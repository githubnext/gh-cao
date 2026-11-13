package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var proxyLog = logger.New("cao:server:proxy")

type ProxyPolicy struct {
	AllowedHosts         []string
	RequireHTTPS         bool
	TrustForwarded       bool
	TrustedProxyPrefixes []netip.Prefix
}

func validateHostedMode(store operational.Store, config *Config) error {
	profile := config.HostProfile
	if profile.Authentication != HostAuthenticationOAuth {
		if profile.Listener == HostListenerProcess {
			if err := ValidateListen(config.Listen, config.CertFile, config.KeyFile); err != nil {
				return err
			}
			if profile.RequiresHTTPS && (config.CertFile == "" || config.KeyFile == "") {
				return fmt.Errorf("host profile %q requires HTTPS", profile.Name)
			}
		}
		return nil
	}
	if strings.TrimSpace(config.AccessToken) != "" {
		return errors.New("hosted mode does not support local bearer capabilities")
	}
	if store == nil {
		return errors.New("hosted mode requires operational storage")
	}
	if len(config.Proxy.AllowedHosts) == 0 {
		return errors.New("hosted mode requires an explicit trusted proxy host policy")
	}
	policy := config.Proxy
	if profile.RequiresHTTPS && !policy.RequireHTTPS {
		return errors.New("hosted mode requires HTTPS")
	}
	if profile.Listener == HostListenerExternal && config.Proxy.TrustForwarded &&
		len(config.Proxy.TrustedProxyPrefixes) == 0 {
		return errors.New("externally hosted forwarded headers require explicit trusted proxy CIDRs")
	}
	if profile.Listener == HostListenerProcess {
		if config.CertFile == "" && isLoopbackListen(config.Listen) && !config.Proxy.TrustForwarded {
			config.Proxy.TrustForwarded = true
			config.Proxy.TrustedProxyPrefixes = loopbackProxyPrefixes()
		}
		if config.Proxy.TrustForwarded && len(config.Proxy.TrustedProxyPrefixes) == 0 {
			return errors.New("hosted forwarded headers require explicit trusted proxy CIDRs")
		}
		if err := validateHostedListen(
			config.Listen, config.CertFile, config.KeyFile, config.Proxy.TrustForwarded,
		); err != nil {
			return err
		}
	}
	if config.GitHubOAuth == nil {
		return errors.New("hosted mode requires GitHub OAuth configuration")
	}
	if err := config.GitHubOAuth.validate(); err != nil {
		return err
	}
	if err := validateLocalDevelopmentOAuth(*config); err != nil {
		return err
	}
	return nil
}

// proxyRejectionReason names why classifyProxyRequest rejected a request. It
// is stable across host-value and header-content changes, so it is useful to
// log without exposing the request's Host, X-Forwarded-* values, or client
// address.
type proxyRejectionReason string

const (
	proxyRejectionNone            proxyRejectionReason = ""
	proxyRejectionUntrustedPeer   proxyRejectionReason = "untrusted-proxy-peer"
	proxyRejectionEmptyHost       proxyRejectionReason = "empty-host"
	proxyRejectionHostNotAllowed  proxyRejectionReason = "host-not-allowed"
	proxyRejectionInsecureRequest proxyRejectionReason = "insecure-request"
)

// classifyProxyRequest decides whether request satisfies policy and, when it
// does not, which precondition failed. It is a pure function extracted from
// validProxyRequest so the trusted-peer check, host allow-listing, and the
// HTTPS requirement are each independently testable against a plain
// *http.Request, and so a rejection can be logged by reason without
// exposing the request's host or forwarded-header values.
func classifyProxyRequest(request *http.Request, policy ProxyPolicy) proxyRejectionReason {
	host := request.Host
	secure := request.TLS != nil
	if policy.TrustForwarded {
		if len(policy.TrustedProxyPrefixes) > 0 && !trustedProxyPeer(request.RemoteAddr, policy.TrustedProxyPrefixes) {
			return proxyRejectionUntrustedPeer
		}
		if forwarded := forwardedHeader(request, "X-Forwarded-Host"); forwarded != "" {
			host = forwarded
		}
		proto := strings.ToLower(strings.TrimSpace(strings.Split(forwardedHeader(request, "X-Forwarded-Proto"), ",")[0]))
		secure = proto == "https"
	}
	host = strings.ToLower(strings.TrimSpace(strings.Split(host, ",")[0]))
	if host == "" {
		return proxyRejectionEmptyHost
	}
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	allowed := false
	for _, candidate := range policy.AllowedHosts {
		if strings.EqualFold(strings.TrimSpace(candidate), host) {
			allowed = true
			break
		}
	}
	if !allowed {
		return proxyRejectionHostNotAllowed
	}
	if policy.RequireHTTPS && !secure {
		return proxyRejectionInsecureRequest
	}
	return proxyRejectionNone
}

// validProxyRequest reports whether request satisfies policy, logging the
// specific precondition that failed on rejection so a misconfigured proxy
// policy or an unexpected caller is diagnosable without exposing the
// request's host or forwarded-header values.
func validProxyRequest(request *http.Request, policy ProxyPolicy) bool {
	reason := classifyProxyRequest(request, policy)
	if reason != proxyRejectionNone {
		proxyLog.PrintfContext(request.Context(), "proxy request rejected reason=%s", reason)
		return false
	}
	return true
}

func trustedProxyPeer(remoteAddress string, prefixes []netip.Prefix) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	address, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	address = address.Unmap()
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func forwardedHeader(request *http.Request, name string) string {
	values := request.Header.Values(name)
	for valueIndex := len(values) - 1; valueIndex >= 0; valueIndex-- {
		parts := strings.Split(values[valueIndex], ",")
		for partIndex := len(parts) - 1; partIndex >= 0; partIndex-- {
			if value := strings.TrimSpace(parts[partIndex]); value != "" {
				return value
			}
		}
	}
	return ""
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func splitCSV(value string) []string {
	var output []string
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			output = append(output, trimmed)
		}
	}
	return output
}
