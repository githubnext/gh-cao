package server

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClassifyProxyRequestAcceptsDirectRequest(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://dashboard.example/", nil)
	request.Host = "dashboard.example"
	request.TLS = &tls.ConnectionState{}

	policy := ProxyPolicy{AllowedHosts: []string{"dashboard.example"}, RequireHTTPS: true}
	if reason := classifyProxyRequest(request, policy); reason != proxyRejectionNone {
		t.Fatalf("classifyProxyRequest = %q, want accepted", reason)
	}
}

func TestClassifyProxyRequestRejectsUntrustedPeer(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://internal.example/", nil)
	request.Host = "internal.example"
	request.RemoteAddr = "203.0.113.10:12345"
	request.Header.Set("X-Forwarded-Host", "dashboard.example")
	request.Header.Set("X-Forwarded-Proto", "https")

	prefix := netip.MustParsePrefix("127.0.0.0/8")
	policy := ProxyPolicy{
		AllowedHosts: []string{"dashboard.example"}, RequireHTTPS: true,
		TrustForwarded: true, TrustedProxyPrefixes: []netip.Prefix{prefix},
	}
	if reason := classifyProxyRequest(request, policy); reason != proxyRejectionUntrustedPeer {
		t.Fatalf("classifyProxyRequest = %q, want %q", reason, proxyRejectionUntrustedPeer)
	}
}

func TestClassifyProxyRequestRejectsEmptyHost(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://dashboard.example/", nil)
	request.Host = ""

	policy := ProxyPolicy{AllowedHosts: []string{"dashboard.example"}}
	if reason := classifyProxyRequest(request, policy); reason != proxyRejectionEmptyHost {
		t.Fatalf("classifyProxyRequest = %q, want %q", reason, proxyRejectionEmptyHost)
	}
}

func TestClassifyProxyRequestRejectsHostNotAllowed(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://attacker.example/", nil)
	request.Host = "attacker.example"

	policy := ProxyPolicy{AllowedHosts: []string{"dashboard.example"}}
	if reason := classifyProxyRequest(request, policy); reason != proxyRejectionHostNotAllowed {
		t.Fatalf("classifyProxyRequest = %q, want %q", reason, proxyRejectionHostNotAllowed)
	}
}

func TestClassifyProxyRequestRejectsInsecureRequest(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://dashboard.example/", nil)
	request.Host = "dashboard.example"

	policy := ProxyPolicy{AllowedHosts: []string{"dashboard.example"}, RequireHTTPS: true}
	if reason := classifyProxyRequest(request, policy); reason != proxyRejectionInsecureRequest {
		t.Fatalf("classifyProxyRequest = %q, want %q", reason, proxyRejectionInsecureRequest)
	}
}

func TestClassifyProxyRequestAcceptsInsecureRequestWhenNotRequired(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://dashboard.example/", nil)
	request.Host = "dashboard.example"

	policy := ProxyPolicy{AllowedHosts: []string{"dashboard.example"}, RequireHTTPS: false}
	if reason := classifyProxyRequest(request, policy); reason != proxyRejectionNone {
		t.Fatalf("classifyProxyRequest = %q, want accepted", reason)
	}
}

func TestClassifyProxyRequestPortIsStrippedFromHost(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://dashboard.example:8443/", nil)
	request.Host = "dashboard.example:8443"
	request.TLS = &tls.ConnectionState{}

	policy := ProxyPolicy{AllowedHosts: []string{"dashboard.example"}, RequireHTTPS: true}
	if reason := classifyProxyRequest(request, policy); reason != proxyRejectionNone {
		t.Fatalf("classifyProxyRequest = %q, want accepted", reason)
	}
}
