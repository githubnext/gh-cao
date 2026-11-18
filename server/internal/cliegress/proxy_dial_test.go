package cliegress

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsTunnelRequest(t *testing.T) {
	connect := func(target string) *http.Request {
		return httptest.NewRequestWithContext(t.Context(), http.MethodConnect, target, nil)
	}
	if !isTunnelRequest(connect("api.github.com:443")) {
		t.Fatal("plain CONNECT authority was rejected")
	}

	withBody := connect("api.github.com:443")
	withBody.ContentLength = 4
	chunked := connect("api.github.com:443")
	chunked.TransferEncoding = []string{"chunked"}
	withScheme := connect("api.github.com:443")
	withScheme.URL.Scheme = "https"
	withPath := connect("api.github.com:443")
	withPath.URL.Path = "/secret"
	withQuery := connect("api.github.com:443")
	withQuery.URL.RawQuery = "a=b"
	mismatch := connect("api.github.com:443")
	mismatch.Host = "evil.example:443"
	noURL := connect("api.github.com:443")
	noURL.URL = nil
	get := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.github.com/", nil)

	for name, request := range map[string]*http.Request{
		"body": withBody, "chunked": chunked, "scheme": withScheme, "path": withPath,
		"query": withQuery, "host mismatch": mismatch, "missing url": noURL, "non-connect": get,
	} {
		if isTunnelRequest(request) {
			t.Fatalf("%s request was accepted as a tunnel", name)
		}
	}
}

func TestIsTunnelRequestAllowsUnknownContentLength(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), http.MethodConnect, "api.github.com:443", nil)
	request.ContentLength = -1
	if !isTunnelRequest(request) {
		t.Fatal("unknown content length must not reject a bodyless CONNECT")
	}
}

// closedAddress returns a loopback address that refuses connections.
func closedAddress(t *testing.T) string {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func liveListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func TestDialFirstFallsBackToLaterAddress(t *testing.T) {
	listener := liveListener(t)
	dialer := &net.Dialer{Timeout: time.Second}
	conn, err := dialFirst(t.Context(), dialer.DialContext, []string{closedAddress(t), listener.Addr().String()})
	if err != nil {
		t.Fatalf("expected fallback to the reachable address: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if conn.RemoteAddr().String() != listener.Addr().String() {
		t.Fatalf("connected to %s, want %s", conn.RemoteAddr(), listener.Addr())
	}
}

func TestDialFirstStopsAtFirstSuccess(t *testing.T) {
	first, second := liveListener(t), liveListener(t)
	attempts := 0
	dialer := &net.Dialer{Timeout: time.Second}
	conn, err := dialFirst(t.Context(), func(ctx context.Context, network, address string) (net.Conn, error) {
		attempts++
		return dialer.DialContext(ctx, network, address)
	}, []string{first.Addr().String(), second.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if attempts != 1 {
		t.Fatalf("dialed %d addresses after the first succeeded", attempts)
	}
}

func TestDialFirstReportsLastFailure(t *testing.T) {
	dialer := &net.Dialer{Timeout: time.Second}
	conn, err := dialFirst(t.Context(), dialer.DialContext, []string{closedAddress(t), closedAddress(t)})
	if err == nil || conn != nil {
		t.Fatal("unreachable addresses must fail without a connection")
	}
}

func TestDialFirstStopsAfterContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0
	dialer := &net.Dialer{Timeout: time.Second}
	conn, err := dialFirst(ctx, func(ctx context.Context, network, address string) (net.Conn, error) {
		attempts++
		cancel()
		return dialer.DialContext(ctx, network, address)
	}, []string{closedAddress(t), closedAddress(t), closedAddress(t)})
	if err == nil || conn != nil {
		t.Fatal("cancelled dial must fail")
	}
	if attempts != 1 {
		t.Fatalf("kept dialing %d addresses after cancellation", attempts)
	}
}

func TestDialFirstWithoutAddressesFails(t *testing.T) {
	dialer := &net.Dialer{Timeout: time.Second}
	conn, err := dialFirst(t.Context(), dialer.DialContext, nil)
	if err == nil || conn != nil {
		t.Fatal("an empty address list must not report success")
	}
}

func TestProxyBadGatewayWhenEveryAddressRefuses(t *testing.T) {
	p, _, _, _ := testProxy(t)
	dialer := &net.Dialer{Timeout: time.Second}
	p.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, closedAddress(t))
	}
	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodConnect, "api.github.com:443", nil))
	if recorder.Code != http.StatusBadGateway || strings.Contains(recorder.Body.String(), "140.82") {
		t.Fatalf("status %d body %q", recorder.Code, recorder.Body.String())
	}
}

func TestProxyRejectsWhenAtCapacity(t *testing.T) {
	p, _, _, dials := testProxy(t)
	p.slots <- struct{}{}
	recorder := httptest.NewRecorder()
	p.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodConnect, "api.github.com:443", nil))
	if recorder.Code != http.StatusServiceUnavailable || dials.Load() != 0 {
		t.Fatalf("status %d dials %d", recorder.Code, dials.Load())
	}
}
