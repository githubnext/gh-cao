package adxingest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

func TestTokenTransportCachesTokensAndHonorsCancellation(t *testing.T) {
	tokens, requests := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokens++
			if err := r.ParseForm(); err != nil || r.Form.Get("client_secret") != "test-secret" ||
				r.Form.Get("scope") != "https://kusto.kusto.windows.net/.default" {
				http.Error(w, "bad credentials", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer","expires_in":3600}`))
			return
		}
		requests++
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "bad token", http.StatusUnauthorized)
		}
	}))
	defer server.Close()
	transport := &tokenTransport{base: http.DefaultTransport, credential: clientcredentials.Config{
		ClientID: "test-client", ClientSecret: "test-secret", TokenURL: server.URL + "/token",
		Scopes: []string{"https://kusto.kusto.windows.net/.default"}, AuthStyle: oauth2.AuthStyleInParams,
	}}
	client := &Client{endpoint: server.URL, http: &http.Client{Transport: transport, Timeout: time.Second}}
	for range 2 {
		if _, err := client.request(t.Context(), "/data", nil); err != nil {
			t.Fatal(err)
		}
	}
	if tokens != 1 || requests != 2 {
		t.Fatalf("token reuse: tokens=%d requests=%d", tokens, requests)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.request(ctx, "/data", nil); err == nil {
		t.Fatal("cancelled HTTP request succeeded")
	}
}

func TestClientRejectsRedirectsAndRedactsResponseFailures(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTooManyRequests, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/leak")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("SECRET-SYNTHETIC-RESPONSE"))
			}))
			defer server.Close()
			client := &Client{endpoint: server.URL, http: &http.Client{CheckRedirect: noRedirect}}
			_, err := client.request(t.Context(), "/request", nil)
			if err == nil || strings.Contains(err.Error(), "SECRET") || calls != 1 {
				t.Fatalf("unsafe response: %v calls=%d", err, calls)
			}
		})
	}
}

func TestClientRejectsOversizedAndMalformedQueryResults(t *testing.T) {
	for _, content := range []string{
		strings.Repeat("x", (1<<20)+1),
		`{"Tables":[]}`,
		`{"Tables":[{"TableName":"Table_0","Rows":[]},{"TableName":"QueryStatus","Rows":[["unknown"]]}]}`,
		`{"Tables":[{"TableName":"Table_0","Rows":[]}],"error":{"message":"SYNTHETIC SECRET"}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(content))
		}))
		client := &Client{endpoint: server.URL, database: "CAO", http: server.Client()}
		_, err := client.query(t.Context(), "/query", "test")
		server.Close()
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("unsafe query result: %v", err)
		}
	}
}

func TestCapacityFailsClosedForMissingSizeEvidence(t *testing.T) {
	sim, client := newSimulator(t)
	sim.bytes = -1
	if err := client.Capacity(t.Context()); !errors.Is(err, ErrStorage) {
		t.Fatalf("unknown capacity accepted: %v", err)
	}
}
