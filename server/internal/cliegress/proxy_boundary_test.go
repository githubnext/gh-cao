package cliegress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func connectRequest(t *testing.T, authority string) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(t.Context(), http.MethodConnect, "//"+authority, nil)
}

func TestIsTunnelRequestAcceptsOnlyBareAuthorityConnect(t *testing.T) {
	valid := connectRequest(t, "api.github.com:443")
	valid.URL = &url.URL{Host: "api.github.com:443"}
	valid.Host = "api.github.com:443"
	if !isTunnelRequest(valid) {
		t.Fatal("bare CONNECT was rejected")
	}

	mutations := map[string]func(*http.Request){
		"method":            func(r *http.Request) { r.Method = http.MethodGet },
		"nil url":           func(r *http.Request) { r.URL = nil },
		"host mismatch":     func(r *http.Request) { r.Host = "other.example:443" },
		"userinfo":          func(r *http.Request) { r.URL.User = url.User("name") },
		"query":             func(r *http.Request) { r.URL.RawQuery = "a=b" },
		"path":              func(r *http.Request) { r.URL.Path = "/x" },
		"scheme":            func(r *http.Request) { r.URL.Scheme = "https" },
		"opaque":            func(r *http.Request) { r.URL.Opaque = "x" },
		"fragment":          func(r *http.Request) { r.URL.Fragment = "x" },
		"body length":       func(r *http.Request) { r.ContentLength = 1 },
		"transfer encoding": func(r *http.Request) { r.TransferEncoding = []string{"chunked"} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			request := connectRequest(t, "api.github.com:443")
			request.URL = &url.URL{Host: "api.github.com:443"}
			request.Host = "api.github.com:443"
			mutate(request)
			if isTunnelRequest(request) {
				t.Fatal("non-tunnel request shape was accepted")
			}
		})
	}
}

func TestDialFirstAvailableFallsBackInOrder(t *testing.T) {
	var tried []string
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		tried = append(tried, network+" "+address)
		if address == "[2001:db8::1]:443" {
			return nil, errors.New("no route")
		}
		return client, nil
	}
	conn, err := dialFirstAvailable(t.Context(), dial, []string{"[2001:db8::1]:443", "140.82.114.5:443", "140.82.114.6:443"})
	if err != nil || conn != client {
		t.Fatalf("fallback dial = %v, %v", conn, err)
	}
	if len(tried) != 2 || tried[0] != "tcp [2001:db8::1]:443" || tried[1] != "tcp 140.82.114.5:443" {
		t.Fatalf("unexpected dial order: %v", tried)
	}
}

func TestDialFirstAvailableReturnsLastErrorWhenAllFail(t *testing.T) {
	last := errors.New("last failure")
	calls := 0
	dial := func(context.Context, string, string) (net.Conn, error) {
		calls++
		if calls == 2 {
			return nil, last
		}
		return nil, errors.New("first failure")
	}
	conn, err := dialFirstAvailable(t.Context(), dial, []string{"140.82.114.5:443", "140.82.114.6:443"})
	if conn != nil || !errors.Is(err, last) || calls != 2 {
		t.Fatalf("all-fail result = %v, %v after %d calls", conn, err, calls)
	}
}

func TestDialFirstAvailableStopsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	dial := func(context.Context, string, string) (net.Conn, error) {
		calls++
		cancel()
		return nil, context.Canceled
	}
	conn, err := dialFirstAvailable(ctx, dial, []string{"140.82.114.5:443", "140.82.114.6:443"})
	if conn != nil || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancelled dial = %v, %v after %d calls", conn, err, calls)
	}
}

func TestDialFirstAvailableRejectsEmptyAddressList(t *testing.T) {
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("dialed without an address")
		return nil, nil
	}
	if conn, err := dialFirstAvailable(t.Context(), dial, nil); conn != nil || err == nil {
		t.Fatalf("empty address list = %v, %v", conn, err)
	}
}

func TestProxyReportsBadGatewayWhenEveryDialFails(t *testing.T) {
	p, _, _, _ := testProxy(t)
	p.dial = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("refused") }
	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodConnect, "api.github.com:443", nil))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
}

func TestProxyRejectsRequestsAtCapacity(t *testing.T) {
	p, _, _, dials := testProxy(t)
	p.slots <- struct{}{}
	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodConnect, "api.github.com:443", nil))
	if recorder.Code != http.StatusServiceUnavailable || dials.Load() != 0 {
		t.Fatalf("capacity rejection = %d with %d dials", recorder.Code, dials.Load())
	}
}
