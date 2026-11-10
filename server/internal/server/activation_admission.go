package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/githubnext/gh-cao/server/internal/admission"
	"github.com/githubnext/gh-cao/server/internal/operational"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

type AdmissionConfig struct {
	Audience   string
	PolicyFile string
}

func admissionConfigFromEnv() (*AdmissionConfig, error) {
	path, audience := os.Getenv("CAO_ADMISSION_POLICY_FILE"), os.Getenv("CAO_ADMISSION_AUDIENCE")
	if path == "" && audience == "" {
		return nil, nil //nolint:nilnil // Both absent deliberately disable the optional service.
	}
	if path == "" || audience == "" {
		return nil, errors.New("admission requires CAO_ADMISSION_POLICY_FILE and CAO_ADMISSION_AUDIENCE")
	}
	return &AdmissionConfig{Audience: audience, PolicyFile: path}, nil
}

func newActivationAdmission(store operational.Store, config Config) (*admission.Service, error) {
	if config.Admission == nil {
		return nil, nil //nolint:nilnil // An unconfigured service must remain absent.
	}
	decisions, ok := store.(*operationalpostgres.Store)
	if !ok || config.HostProfile.Authentication != HostAuthenticationOAuth || !config.Proxy.RequireHTTPS {
		return nil, errors.New("activation admission requires hosted HTTPS and durable PostgreSQL operational storage")
	}
	verifier, err := admission.NewVerifier(config.Admission.Audience)
	if err != nil {
		return nil, errors.New("admission audience is invalid")
	}
	policy := admission.FilePolicy{Path: config.Admission.PolicyFile}
	if err := policy.Validate(); err != nil {
		return nil, errors.New("admission requires a readable valid protected policy and accounting snapshot")
	}
	runs, ok := config.Database.(*postgresx.Store)
	if !ok || runs == nil {
		return nil, errors.New("activation admission requires the canonical PostgreSQL run store")
	}
	return &admission.Service{
		Verifier: verifier, Policies: policy, Decisions: decisions, Runs: runs,
		ConclusionPolicies: policy, Conclusions: decisions, ConclusionRuns: runs,
	}, nil
}

func (a *App) activationAdmission(w http.ResponseWriter, r *http.Request) {
	a.serveAdmission(w, r, false)
}

func (a *App) conclusionAdmission(w http.ResponseWriter, r *http.Request) {
	a.serveAdmission(w, r, true)
}

func admissionPath(path string) bool {
	return path == "/api/admission" || path == "/api/conclusion"
}

func (a *App) serveAdmission(w http.ResponseWriter, r *http.Request, conclusion bool) {
	w.Header().Set("Cache-Control", "no-store")
	if a.admission == nil || r.Method != http.MethodPost ||
		!validProxyRequest(r, a.proxyPolicy()) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	a.startMu.Lock()
	unavailable := a.draining || a.config.HostProfile.Listener == HostListenerExternal &&
		(a.startContext == nil || a.startContext.Err() != nil)
	a.startMu.Unlock()
	if unavailable {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	// Limit by trusted peer identity, independently of policy and spend.
	sum := sha256.Sum256([]byte(a.clientIP(r)))
	limit, err := a.services.RequestLimiter.TakeRateLimitToken(ctx,
		"activation-admission:"+hex.EncodeToString(sum[:]), 60, time.Minute)
	if err != nil {
		serverLog.PrintfContext(r.Context(), "activation admission dependency unavailable")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !limit.Allowed {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	// Bound body reads on real transports; TimeoutHandler also bounds the
	// response on externally owned listeners that lack read-deadline support.
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(5 * time.Second)
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
	var handler http.Handler = a.admission
	if conclusion {
		handler = http.HandlerFunc(a.admission.Conclude)
	}
	http.TimeoutHandler(handler, time.Until(deadline), "").ServeHTTP(
		&admissionResponse{ResponseWriter: w, headers: make(http.Header)}, r.WithContext(ctx))
}

// Keep timeout middleware and future wrappers inside the admission wire contract.
type admissionResponse struct {
	http.ResponseWriter
	headers http.Header
	status  int
}

func (w *admissionResponse) Header() http.Header { return w.headers }

func (w *admissionResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if status == http.StatusOK {
		w.ResponseWriter.Header().Set("Content-Type", "application/json")
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *admissionResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.status != http.StatusOK {
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}
