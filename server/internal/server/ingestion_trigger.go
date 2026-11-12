package server

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"
)

// startIngestion asks the owning local CLI to begin a collection pass now.
// The adapter coalesces requests; it never widens repository scope or quota.
func (a *App) startIngestion(response http.ResponseWriter, request *http.Request) {
	connection := a.operatorConnection()
	if a.oauth != nil || connection == nil || !validOperatorAdapterURL(connection.AdapterURL) {
		writeError(request.Context(), response, http.StatusNotFound, "local ingestion control is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	forward, err := http.NewRequestWithContext(ctx, http.MethodPost, connection.AdapterURL+"/ingest", nil)
	if err != nil {
		writeError(request.Context(), response, http.StatusInternalServerError, "local ingestion request could not be created")
		return
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	defer client.CloseIdleConnections()
	result, err := client.Do(forward)
	if err != nil {
		writeError(request.Context(), response, http.StatusBadGateway, "local ingestion process is unavailable")
		return
	}
	_ = result.Body.Close()
	if result.StatusCode != http.StatusAccepted {
		writeError(request.Context(), response, http.StatusBadGateway, "local ingestion process rejected the request")
		return
	}
	serverLog.PrintfContext(request.Context(), "local ingestion requested")
	writeJSON(response, http.StatusAccepted, map[string]any{"requested": true})
}

func validOperatorAdapterURL(value string) bool {
	adapter, err := url.Parse(value)
	if err != nil || adapter.Scheme != "http" || adapter.User != nil || adapter.RawQuery != "" ||
		adapter.Fragment != "" || !canvasPrefix.MatchString(adapter.Path) || adapter.Port() == "" {
		return false
	}
	ip := net.ParseIP(adapter.Hostname())
	return ip != nil && ip.IsLoopback()
}
