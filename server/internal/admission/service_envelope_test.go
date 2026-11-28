package admission

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func envelopeRequest(t *testing.T, mutate func(*http.Request)) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/admission", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer credential")
	r.Header.Set("Content-Type", "application/json")
	if mutate != nil {
		mutate(r)
	}
	return r
}

func TestRequestEnvelopeCredential(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*http.Request)
		limit      int64
		credential string
		reason     string
	}{
		{name: "accepted", limit: 8192, credential: "credential"},
		{name: "scheme is case insensitive", limit: 8192, credential: "credential",
			mutate: func(r *http.Request) { r.Header.Set("Authorization", "bEaReR credential") }},
		{name: "charset parameter", limit: 8192, credential: "credential",
			mutate: func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=UTF-8") }},
		{name: "wrong method", limit: 8192, reason: "method_or_query",
			mutate: func(r *http.Request) { r.Method = http.MethodGet }},
		{name: "query string", limit: 8192, reason: "method_or_query",
			mutate: func(r *http.Request) { r.URL.RawQuery = "a=b" }},
		{name: "forced empty query", limit: 8192, reason: "method_or_query",
			mutate: func(r *http.Request) { r.URL.ForceQuery = true }},
		{name: "duplicate authorization", limit: 8192, reason: "header_shape",
			mutate: func(r *http.Request) { r.Header.Add("Authorization", "Bearer other") }},
		{name: "missing content type", limit: 8192, reason: "header_shape",
			mutate: func(r *http.Request) { r.Header.Del("Content-Type") }},
		{name: "oversized authorization", limit: 8192, reason: "header_shape",
			mutate: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 16<<10+1)) }},
		{name: "declared body over limit", limit: 1, reason: "body_too_large"},
		{name: "headers over budget", limit: 8192, reason: "headers_too_large",
			mutate: func(r *http.Request) {
				r.Header.Set("X-Padding-A", strings.Repeat("a", 16<<10))
				r.Header.Set("X-Padding-B", strings.Repeat("b", 16<<10))
			}},
		{name: "trace headers are not budgeted", limit: 8192, credential: "credential",
			mutate: func(r *http.Request) {
				r.Header.Set("Traceparent", strings.Repeat("a", 40<<10))
				r.Header.Set("Baggage", strings.Repeat("b", 40<<10))
			}},
		{name: "non json content type", limit: 8192, reason: "content_type",
			mutate: func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }},
		{name: "unexpected content type parameter", limit: 8192, reason: "content_type",
			mutate: func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=latin1") }},
		{name: "too many content type parameters", limit: 8192, reason: "content_type",
			mutate: func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=utf-8; a=b") }},
		{name: "non bearer scheme", limit: 8192, reason: "authorization_scheme",
			mutate: func(r *http.Request) { r.Header.Set("Authorization", "Basic credential") }},
		{name: "credential with spaces", limit: 8192, reason: "authorization_scheme",
			mutate: func(r *http.Request) { r.Header.Set("Authorization", "Bearer a b") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			credential, reason := requestEnvelopeCredential(envelopeRequest(t, test.mutate), test.limit)
			if credential != test.credential || reason != test.reason {
				t.Fatalf("got credential %q reason %q, want %q reason %q", credential, reason, test.credential, test.reason)
			}
		})
	}
}

func TestAuthenticatedRequestRejectsEnvelopeBeforeVerification(t *testing.T) {
	// A nil Verifier would panic if verification were reached.
	service := &Service{}
	request := envelopeRequest(t, func(r *http.Request) { r.Header.Set("Authorization", "Basic credential") })
	_, data, status := service.authenticatedRequest(httptest.NewRecorder(), request, 8192)
	if status != http.StatusForbidden || data != nil {
		t.Fatalf("status %d data %q, want 403 and no body", status, data)
	}
}
