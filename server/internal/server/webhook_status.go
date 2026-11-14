package server

import (
	"net/http"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

const webhookStatusSourceName = "webhook-status"

type webhookStatus struct {
	Configured     bool   `json:"configured"`
	Scope          string `json:"scope"`
	Received       int64  `json:"received"`
	InFlight       int64  `json:"inFlight"`
	Verified       int64  `json:"verified"`
	Accepted       int64  `json:"accepted"`
	Rejected       int64  `json:"rejected"`
	Failed         int64  `json:"failed"`
	Duplicates     int64  `json:"duplicates"`
	Pings          int64  `json:"pings"`
	LastReceivedAt string `json:"lastReceivedAt,omitempty"`
	LastAcceptedAt string `json:"lastAcceptedAt,omitempty"`
	LastFailureAt  string `json:"lastFailureAt,omitempty"`
	LastPingAt     string `json:"lastPingAt,omitempty"`
}

type webhookMetrics struct {
	mu     sync.Mutex
	status webhookStatus
}

func (m *webhookMetrics) begin() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Received++
	m.status.InFlight++
	m.status.LastReceivedAt = time.Now().UTC().Format(time.RFC3339Nano)
}

func (m *webhookMetrics) verified() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Verified++
}

func (m *webhookMetrics) duplicate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Duplicates++
}

func (m *webhookMetrics) finish(status int, ping bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.InFlight--
	now := time.Now().UTC().Format(time.RFC3339Nano)
	switch {
	case status >= 200 && status < 300:
		m.status.Accepted++
		m.status.LastAcceptedAt = now
		if ping {
			m.status.Pings++
			m.status.LastPingAt = now
		}
	case status >= 400 && status < 500:
		m.status.Rejected++
	default:
		m.status.Failed++
		m.status.LastFailureAt = now
	}
}

func (a *App) webhookStatus(response http.ResponseWriter, request *http.Request) {
	if !a.adminAuthorized(request) {
		a.writeAdminDenied(response, request, "administrative authorization is required")
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, a.webhookSnapshot())
}

func (a *App) webhookSnapshot() webhookStatus {
	a.webhookMetrics.mu.Lock()
	status := a.webhookMetrics.status
	a.webhookMetrics.mu.Unlock()
	status.Configured = len(a.webhookSecret) > 0 && len(a.Webhooks().Listeners()) > 0
	status.Scope = "process"
	return status
}

func (a *App) webhookStatusSource(allowed bool) model.Source {
	if !allowed {
		return unavailableSource(webhookStatusSourceName)
	}
	status := a.webhookSnapshot()
	row := model.Row{
		"configured": status.Configured, "scope": status.Scope,
		"received": status.Received, "in-flight": status.InFlight,
		"verified": status.Verified, "accepted": status.Accepted,
		"rejected": status.Rejected, "failed": status.Failed,
		"duplicates": status.Duplicates, "pings": status.Pings,
	}
	for field, value := range map[string]string{
		"last-received-at": status.LastReceivedAt, "last-accepted-at": status.LastAcceptedAt,
		"last-failure-at": status.LastFailureAt, "last-ping-at": status.LastPingAt,
	} {
		if value != "" {
			row[field] = value
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return model.Source{
		Source: webhookStatusSourceName, Rows: []model.Row{row},
		Metadata: model.Metadata{
			"source-id": webhookStatusSourceName, "source-kind": "runtime",
			"as-of": now, "retrieved-at": now, "availability": "available",
			"completeness": "complete", "freshness": "fresh",
		},
	}
}

type webhookResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *webhookResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *webhookResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (w *webhookResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
