package admission

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

var admissionLog = logger.New("cao:admission")

// Decisions atomically preserves the first decision. A nil proposal revokes an
// invocation permanently. Expired/revoked records are never treated as misses.
// Implementations must be durable and deployment-scoped, with bounded capacity.
type Decisions interface {
	ResolveAdmission(context.Context, string, *Allowances) (Allowances, error)
	CheckAdmission(context.Context, string) error
}

type Service struct {
	Verifier           *Verifier
	Policies           PolicySource
	Decisions          Decisions
	Runs               AdmittedRuns
	ConclusionPolicies ConclusionPolicySource
	Conclusions        ConclusionSessions
	ConclusionRuns     ConcludedRuns
}

// AdmittedRuns publishes only verified approvals, never blocked attempts.
type AdmittedRuns interface {
	RecordAdmittedRun(context.Context, Identity) error
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusForbidden
	defer func() {
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
	}()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	identity, data, failure := s.authenticatedRequest(w, r.WithContext(ctx), 8192)
	if failure != 0 {
		status = failure
		return
	}
	input, err := decodeRequest(data)
	if err != nil || !input.matches(identity) {
		return
	}
	// triggering_actor is informational only. This profile authorizes exclusively
	// with the signed original actor_id, never the rerun initiator's privileges.
	proposal, err := observeOperation(ctx, telemetry.SpanAdmissionPolicy, func(ctx context.Context) (Allowances, error) {
		return s.Policies.Evaluate(ctx, identity)
	})
	if err != nil && !errors.Is(err, ErrDenied) {
		status = http.StatusServiceUnavailable
		_, replayErr := observeOperation(ctx, telemetry.SpanAdmissionReplay, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, s.Decisions.CheckAdmission(ctx, identity.Key())
		})
		if errors.Is(replayErr, ErrDenied) {
			status = http.StatusForbidden
		}
		return
	}
	var candidate *Allowances
	if err == nil {
		if !proposal.valid() {
			status = http.StatusServiceUnavailable
			return
		}
		candidate = &proposal
	}
	decision, err := observeOperation(ctx, telemetry.SpanAdmissionDecision, func(ctx context.Context) (Allowances, error) {
		return s.Decisions.ResolveAdmission(ctx, identity.Key(), candidate)
	})
	if candidate == nil {
		if err != nil && !errors.Is(err, ErrDenied) {
			admissionLog.Printf("decision revocation could not be persisted")
		}
		return
	}
	if err != nil {
		if !errors.Is(err, ErrDenied) {
			status = http.StatusServiceUnavailable
		}
		return
	}
	if !decision.valid() || ctx.Err() != nil {
		status = http.StatusServiceUnavailable
		return
	}
	if s.Runs != nil {
		_, err := observeOperation(ctx, telemetry.SpanAdmissionRun, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, s.Runs.RecordAdmittedRun(ctx, identity)
		})
		if err != nil {
			status = http.StatusServiceUnavailable
			return
		}
	}
	body, err := json.Marshal(decision)
	if err != nil {
		status = http.StatusServiceUnavailable
		return
	}
	status = http.StatusOK
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (s *Service) authenticatedRequest(w http.ResponseWriter, r *http.Request, limit int64) (Identity, []byte, int) {
	credential, reason := requestEnvelopeCredential(r, limit)
	if reason != "" {
		admissionLog.Printf("request envelope rejected: reason=%s", reason)
		return Identity{}, nil, http.StatusForbidden
	}
	identity, err := observeOperation(r.Context(), telemetry.SpanAdmissionIdentity, func(ctx context.Context) (Identity, error) {
		return s.Verifier.Verify(ctx, credential)
	})
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return Identity{}, nil, http.StatusServiceUnavailable
		}
		return Identity{}, nil, http.StatusForbidden
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return Identity{}, nil, http.StatusForbidden
	}
	return identity, data, 0
}

// requestEnvelopeCredential validates the request shape before any credential
// is verified and returns the single bearer credential. A non-empty reason is a
// stable code for the first rejected check; it never carries header values.
func requestEnvelopeCredential(r *http.Request, limit int64) (credential, reason string) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery {
		return "", "method_or_query"
	}
	if len(r.Header.Values("Authorization")) != 1 || len(r.Header.Values("Content-Type")) != 1 ||
		len(r.Header.Get("Authorization")) > (16<<10)+7 {
		return "", "header_shape"
	}
	if r.ContentLength > limit {
		return "", "body_too_large"
	}
	headerBytes := 0
	for name, values := range r.Header {
		if strings.EqualFold(name, "traceparent") || strings.EqualFold(name, "tracestate") || strings.EqualFold(name, "baggage") {
			continue
		}
		for _, value := range values {
			headerBytes += len(name) + len(value)
		}
	}
	if headerBytes > 32<<10 {
		return "", "headers_too_large"
	}
	contentType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || len(params) > 1 ||
		(len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8")) {
		return "", "content_type"
	}
	auth := strings.Fields(r.Header.Get("Authorization"))
	if len(auth) != 2 || !strings.EqualFold(auth[0], "bearer") {
		return "", "authorization_scheme"
	}
	return auth[1], ""
}
