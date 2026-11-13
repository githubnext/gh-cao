package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

func TestLocalWebsocketStreamsProgressWithoutCanonicalRevision(t *testing.T) {
	directory := t.TempDir()
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		config:   Config{SourceDirectory: directory, HostProfile: localHostProfile()},
		database: &eventTestDatabase{}, services: store.OperationalServices(),
		hub: newEventHub(), accessToken: testAccessToken,
		drain: make(chan struct{}),
	}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	defer app.hub.shutdown()
	config, err := websocket.NewConfig(strings.Replace(server.URL, "http:", "ws:", 1)+"/api/v1/events/ws", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	config.Protocol = []string{dashboardSocketProtocol, "cao-bearer." + base64.RawURLEncoding.EncodeToString([]byte(testAccessToken))}
	connection, err := websocket.DialConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	if got := connection.Config().Protocol; len(got) != 1 || got[0] != dashboardSocketProtocol {
		t.Fatalf("server echoed a credential protocol: %v", got)
	}
	if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := websocket.JSON.Receive(connection, &event); err != nil || event["revision"] != float64(0) {
		t.Fatalf("missing initial revision: %v, %v", event, err)
	}
	if err := websocket.JSON.Receive(connection, &event); err != nil || event["type"] != "ingestion-progress" || event["progress"] != nil {
		t.Fatalf("missing unavailable progress: %v, %v", event, err)
	}
	progress := localIngestionProgress{
		PassID: "pass-1", Repository: "octo/api", Phase: "receiving", ReceivedRuns: 7,
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano), UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	content, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(directory, "progress.tmp")
	if err := os.WriteFile(staging, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, filepath.Join(directory, "ingestion-progress.json")); err != nil {
		t.Fatal(err)
	}
	if err := websocket.JSON.Receive(connection, &event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "ingestion-progress" || event["progress"].(map[string]any)["receivedRuns"] != float64(7) {
		t.Fatalf("progress waited for a canonical revision: %v", event)
	}
	app.database.(*eventTestDatabase).revision.Store(8)
	app.hub.Broadcast(8)
	if err := websocket.JSON.Receive(connection, &event); err != nil || event["revision"] != float64(8) {
		t.Fatalf("revision was not delivered on the same connection: %v, %v", event, err)
	}
	close(app.drain)
	if err := websocket.JSON.Receive(connection, &event); err == nil {
		t.Fatal("local WebSocket remained open after server drain")
	}
}

func TestHostedWebsocketIsNotRegistered(t *testing.T) {
	app, _, _, session := eventAuthorizationApp(t, "active")
	if err := app.oauth.saveSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	request := azureRequest(t, http.MethodGet, "/api/v1/events/ws")
	request.AddCookie(eventRequestCookie(sessionCookieName, session.ID))
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("hosted WebSocket route returned %d, want 404", response.Code)
	}
}

func TestLocalWebsocketRejectsMissingCapabilityAndForeignOrigins(t *testing.T) {
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	app := &App{config: Config{HostProfile: localHostProfile()}, accessToken: testAccessToken, services: store.OperationalServices()}
	for _, test := range []struct {
		name, origin, token string
		status              int
	}{
		{"missing capability", "http://localhost", "", http.StatusUnauthorized},
		{"wrong capability", "http://localhost", "wrong", http.StatusUnauthorized},
		{"foreign origin", "http://evil.example", testAccessToken, http.StatusForbidden},
		{"missing origin", "", testAccessToken, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/v1/events/ws", nil)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Sec-WebSocket-Protocol", dashboardSocketProtocol+", cao-bearer."+base64.RawURLEncoding.EncodeToString([]byte(test.token)))
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestLocalProgressRejectsMalformedTelemetry(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "ingestion-progress.json"), []byte(`{"receivedRuns":-1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLocalIngestionProgress(directory); err == nil {
		t.Fatal("malformed progress was accepted as success")
	}
}
