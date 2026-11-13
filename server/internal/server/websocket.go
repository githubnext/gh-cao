package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

const dashboardSocketProtocol = "cao-dashboard"

var errNoLocalIngestionProgress = errors.New("no local ingestion progress receipt")

func websocketCapability(request *http.Request) (string, error) {
	var token string
	for _, protocol := range strings.Split(request.Header.Get("Sec-WebSocket-Protocol"), ",") {
		if encoded, ok := strings.CutPrefix(strings.TrimSpace(protocol), "cao-bearer."); ok {
			if token != "" {
				return "", errors.New("multiple WebSocket capabilities")
			}
			decoded, err := base64.RawURLEncoding.DecodeString(encoded)
			if err != nil {
				return "", err
			}
			token = string(decoded)
		}
	}
	return token, nil
}

type localIngestionProgress struct {
	PassID       string `json:"passId"`
	Repository   string `json:"repository"`
	Phase        string `json:"phase"`
	ReceivedRuns int    `json:"receivedRuns"`
	StartedAt    string `json:"startedAt"`
	UpdatedAt    string `json:"updatedAt"`
}

func readLocalIngestionProgress(directory string) (*localIngestionProgress, error) {
	if directory == "" {
		return nil, errNoLocalIngestionProgress
	}
	// #nosec G304 -- the operator configures the private local acquisition directory.
	file, err := os.Open(filepath.Join(directory, "ingestion-progress.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNoLocalIngestionProgress
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var value localIngestionProgress
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid local ingestion progress receipt")
	}
	_, startErr := time.Parse(time.RFC3339Nano, value.StartedAt)
	_, updateErr := time.Parse(time.RFC3339Nano, value.UpdatedAt)
	if value.PassID == "" || value.ReceivedRuns < 0 || value.ReceivedRuns > 1000 ||
		len(value.Repository) > 256 || startErr != nil || updateErr != nil ||
		!slices.Contains([]string{"discovering", "receiving", "ingesting", "waiting", "paused", "failed"}, value.Phase) {
		return nil, errors.New("invalid local ingestion progress receipt")
	}
	return &value, nil
}

func (a *App) websocketEvents(response http.ResponseWriter, request *http.Request) {
	origin := "http://" + request.Host
	if request.TLS != nil {
		origin = "https://" + request.Host
	}
	if request.Header.Get("Origin") != origin || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(request.Context(), response, http.StatusForbidden, "same-origin WebSocket required")
		return
	}
	server := websocket.Server{
		Handshake: func(config *websocket.Config, _ *http.Request) error {
			if !slices.Contains(config.Protocol, dashboardSocketProtocol) {
				return errors.New("dashboard WebSocket protocol required")
			}
			config.Protocol = []string{dashboardSocketProtocol}
			return nil
		},
		Handler: func(connection *websocket.Conn) {
			a.streamWebsocketEvents(connection, request)
		},
	}
	server.ServeHTTP(response, request)
}

func (a *App) streamWebsocketEvents(connection *websocket.Conn, request *http.Request) {
	defer func() { _ = connection.Close() }()
	connection.MaxPayloadBytes = 1024
	a.startMu.Lock()
	drain := a.drain
	a.startMu.Unlock()
	channel := a.hub.Subscribe(request.Context(), a, true)
	if channel == nil {
		return
	}
	defer a.hub.Unsubscribe(channel)
	disconnected := make(chan struct{})
	go func() {
		defer close(disconnected)
		// Read control frames and detect disconnection; no application input is accepted.
		var input string
		_ = websocket.Message.Receive(connection, &input)
	}()
	send := func(value any) error {
		if err := connection.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		return websocket.JSON.Send(connection, value)
	}
	active, err := a.database.State(request.Context())
	if err != nil {
		serverLog.PrintfContext(request.Context(), "WebSocket revision unavailable")
		return
	}
	healthRevision, err := a.ingestionHealthRevision(request.Context(), true)
	if err != nil {
		serverLog.PrintfContext(request.Context(), "WebSocket health revision unavailable")
		return
	}
	last := eventObservation{revision: active.Revision, healthRevision: healthRevision}
	sendRevision := func() error {
		return send(map[string]int64{"revision": last.revision, "healthRevision": last.healthRevision})
	}
	if err := sendRevision(); err != nil {
		return
	}
	var previous *localIngestionProgress
	first := true
	sendProgress := func(ctx context.Context) error {
		progress, err := readLocalIngestionProgress(a.config.SourceDirectory)
		if err != nil && !errors.Is(err, errNoLocalIngestionProgress) {
			serverLog.PrintfContext(ctx, "local ingestion progress unavailable")
			return err
		}
		if first || (previous == nil) != (progress == nil) || previous != nil && progress != nil && *previous != *progress {
			if err := send(struct {
				Type     string                  `json:"type"`
				Progress *localIngestionProgress `json:"progress"`
			}{"ingestion-progress", progress}); err != nil {
				return err
			}
			first, previous = false, progress
		}
		return nil
	}
	if err := sendProgress(request.Context()); err != nil {
		return
	}
	progressTicker := time.NewTicker(250 * time.Millisecond)
	defer progressTicker.Stop()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case observation, ok := <-channel:
			if !ok {
				return
			}
			if observation != last {
				last = observation
				if err := sendRevision(); err != nil {
					return
				}
			}
		case <-progressTicker.C:
			if err := sendProgress(request.Context()); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := sendRevision(); err != nil {
				return
			}
		case <-disconnected:
			return
		case <-drain:
			return
		case <-request.Context().Done():
			return
		}
	}
}
