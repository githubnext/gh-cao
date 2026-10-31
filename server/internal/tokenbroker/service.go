// Package tokenbroker implements a request-only, single-process GitHub Actions
// credential broker. Deployment must not share an endpoint across replicas.
package tokenbroker

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/githubnext/gh-cao/server/internal/admission"
)

type IdentityVerifier interface {
	Verify(context.Context, string) (admission.Identity, error)
}

type PrivateKeyReader interface {
	PrivateKey(context.Context) ([]byte, error)
}

type Config struct {
	Audience, PolicyFile                                string
	AppID, InstallationID                               int64
	OwnerID, KeyVaultSecretURL, ManagedIdentityClientID string
	Verifier                                            IdentityVerifier
	KeyReader                                           PrivateKeyReader
	HTTPClient                                          *http.Client
	Now                                                 func() time.Time
}

const (
	requestTimeout = 8 * time.Second
	maxContexts    = 4096
	maxTokens      = 16
)

type credential struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type heldToken struct {
	sealed  []byte
	expires time.Time
}

type execution struct {
	tokens      []heldToken
	until       time.Time
	window      time.Time
	requests    int
	invalidated bool
}

type Service struct {
	config       Config
	verifier     IdentityVerifier
	keyReader    PrivateKeyReader
	client       *http.Client
	now          func() time.Time
	gate         chan struct{}
	contexts     map[string]*execution
	cipher       cipher.AEAD
	sequence     uint64
	closed       bool
	tenantWindow time.Time
	tenantMints  int
}

func New(config Config) (*Service, error) {
	u, err := url.Parse(config.Audience)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.Fragment != "" || u.RawQuery != "" || u.ForceQuery || u.Path != "/api/token-broker" ||
		len(config.Audience) > 2048 || strings.ContainsAny(config.Audience, "\\#") ||
		strings.ContainsFunc(config.Audience, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) ||
		strings.Contains(config.Audience, "${{") || config.PolicyFile == "" ||
		config.AppID <= 0 || config.InstallationID <= 0 || !stableID.MatchString(config.OwnerID) {
		return nil, errors.New("invalid token broker configuration")
	}
	verifier := config.Verifier
	if verifier == nil {
		verifier, err = admission.NewVerifier(config.Audience)
		if err != nil {
			return nil, errors.New("invalid token broker verifier configuration")
		}
	}
	reader := config.KeyReader
	if reader == nil {
		vaultURL, err := url.Parse(config.KeyVaultSecretURL)
		if err != nil || strings.Contains(config.KeyVaultSecretURL, "#") || !safeVaultURL(vaultURL) {
			return nil, errors.New("invalid token broker key configuration")
		}
		reader = KeyVault{SecretURL: config.KeyVaultSecretURL, ClientID: config.ManagedIdentityClientID}
	}
	client := &http.Client{Timeout: requestTimeout}
	if config.HTTPClient != nil {
		*client = *config.HTTPClient
	}
	if client.Timeout <= 0 || client.Timeout > requestTimeout {
		client.Timeout = requestTimeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	if client.Transport == nil {
		client.Transport = http.DefaultTransport
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, errors.New("cannot initialize token protection")
	}
	block, err := aes.NewCipher(key[:])
	clear(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Service{
		config: config, verifier: verifier, keyReader: reader, client: client, now: now,
		gate: make(chan struct{}, 1), contexts: map[string]*execution{}, cipher: aead,
	}, nil
}

func executionKey(i admission.Identity) string {
	data, _ := json.Marshal([]string{
		i.Issuer, i.OwnerID, i.RepositoryID, i.Repository, i.WorkflowRef,
		i.WorkflowSHA, i.Ref, i.Event, i.RunID, i.RunAttempt, i.CheckRunID,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path != "/api/token-broker" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(requestTimeout))
	defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
	input, status := decodeRequest(w, r)
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	identity, err := s.verifier.Verify(ctx, input.identity)
	if err != nil {
		status = http.StatusUnauthorized
		if errors.Is(err, admission.ErrUnavailable) {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		return
	}
	// Serialize policy evaluation, issuance and cleanup, including shutdown, so
	// invalidation cannot race an in-flight mint or policy evaluation.
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !s.authorized(identity) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if !input.invalidate && input.tokenType == "copilot-task" {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	if !input.invalidate && input.provider != "github" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	now := s.now()
	s.prune(now)
	key := executionKey(identity)
	state := s.contexts[key]
	if s.closed {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if state == nil {
		if len(s.contexts) >= maxContexts {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		state = &execution{until: now.Add(70 * time.Minute)}
		s.contexts[key] = state
	}
	if now.Sub(state.window) >= time.Minute {
		state.window, state.requests = now, 0
	}
	state.requests++
	if state.requests > 30 {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if input.invalidate {
		state.invalidated = true
		if s.revoke(ctx, key, state) != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if state.invalidated {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if state.requests > 6 || len(state.tokens) >= maxTokens {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if now.Sub(s.tenantWindow) >= time.Minute {
		s.tenantWindow, s.tenantMints = now, 0
	}
	if s.tenantMints >= 60 {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	s.tenantMints++
	result, issueErr := s.issue(ctx, identity)
	var response []byte
	if issueErr == nil {
		response, issueErr = json.Marshal(result)
		if len(response) > bodyLimit {
			issueErr = errUpstream
		}
	}
	if result.Token != "" {
		expiry := result.ExpiresAt
		if expiry.IsZero() {
			expiry = now.Add(time.Hour)
		}
		// Record every returned credential before any attempt to send it. Even
		// rejected upstream scope/expiry results must be revoked or retained.
		state.tokens = append(state.tokens, heldToken{
			sealed: s.seal(key, result.Token), expires: expiry,
		})
		state.until = now.Add(70 * time.Minute)
	}
	if issueErr != nil {
		_ = s.revoke(ctx, key, state)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(response); err != nil {
		// Delivery may have been partial. Retain the credential until cleanup.
		return
	}
}

func (s *Service) seal(key, token string) []byte {
	nonce := make([]byte, s.cipher.NonceSize())
	// All calls hold the service gate. A counter supplies unique nonces for
	// the random process-local key without persisting any credential state.
	s.sequence++
	binary.BigEndian.PutUint64(nonce[len(nonce)-8:], s.sequence)
	return s.cipher.Seal(nonce, nonce, []byte(token), []byte(key))
}

func (s *Service) prune(now time.Time) {
	for key, state := range s.contexts {
		remaining := state.tokens[:0]
		for _, token := range state.tokens {
			if token.expires.After(now) {
				remaining = append(remaining, token)
			}
		}
		clear(state.tokens[len(remaining):])
		state.tokens = remaining
		if len(state.tokens) == 0 && !state.until.After(now) {
			delete(s.contexts, key)
		}
	}
}

func (s *Service) revoke(ctx context.Context, key string, state *execution) error {
	var failed bool
	remaining := state.tokens[:0]
	for _, token := range state.tokens {
		n := s.cipher.NonceSize()
		plain, err := s.cipher.Open(nil, token.sealed[:n], token.sealed[n:], []byte(key))
		if err == nil {
			err = s.revokeToken(ctx, string(plain))
		}
		clear(plain)
		if err != nil {
			failed = true
			remaining = append(remaining, token)
		}
	}
	clear(state.tokens[len(remaining):])
	state.tokens = remaining
	if failed {
		return errors.New("credential cleanup unavailable")
	}
	return nil
}

// Close stops issuance and attempts to revoke all outstanding credentials.
// Failed cleanup remains in memory so callers can retry Close with a fresh context.
func (s *Service) Close(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return errors.New("token broker shutdown timed out")
	}
	s.closed = true
	s.prune(s.now())
	var failed bool
	for key, state := range s.contexts {
		if s.revoke(ctx, key, state) != nil {
			failed = true
		}
	}
	if failed {
		return errors.New("token broker cleanup unavailable")
	}
	return nil
}
