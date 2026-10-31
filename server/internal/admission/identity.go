package admission

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"

	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

const Issuer = "https://token.actions.githubusercontent.com"

type Identity struct {
	Issuer, Subject, Repository, RepositoryID, OwnerID string
	RunID, RunAttempt, Ref, Event, Actor, ActorID      string
	WorkflowRef, WorkflowSHA                           string
	CheckRunID                                         string
	EnvironmentPresent                                 bool
}

func (i Identity) Key() string {
	encoded, _ := json.Marshal([]string{
		i.Issuer, i.RepositoryID, i.OwnerID, i.RunID, i.RunAttempt,
		i.WorkflowRef, i.WorkflowSHA, i.ActorID,
	})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type Verifier struct {
	audience       string
	client         *http.Client
	gate           chan struct{}
	keys           map[string]*rsa.PublicKey
	refreshed      time.Time
	lastRefresh    time.Time
	missing        string
	unknownRefresh time.Time
}

func NewVerifier(audience string) (*Verifier, error) {
	if !clean(audience, 2048) || strings.Contains(audience, "${{") {
		return nil, ErrDenied
	}
	return &Verifier{
		audience: audience,
		gate:     make(chan struct{}, 1),
		client: &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}, nil
}

func (v *Verifier) Verify(ctx context.Context, raw string) (Identity, error) {
	if len(raw) > 16<<10 {
		return Identity{}, ErrDenied
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Identity{}, ErrDenied
	}
	var header map[string]json.RawMessage
	var claims map[string]json.RawMessage
	h, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(h) > 1024 || strictJSON(h, &header) != nil || header == nil {
		return Identity{}, ErrDenied
	}
	p, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || strictJSON(p, &claims) != nil || claims == nil {
		return Identity{}, ErrDenied
	}
	var dependencyErr error
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"RS256"}), jwt.WithJSONNumber(), jwt.WithoutClaimsValidation())
	token, err := parser.Parse(raw, func(t *jwt.Token) (any, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok || !clean(kid, 256) {
			return nil, ErrDenied
		}
		key, err := v.key(ctx, kid)
		if errors.Is(err, ErrUnavailable) {
			dependencyErr = err
		}
		return key, err
	})
	if dependencyErr != nil {
		return Identity{}, dependencyErr
	}
	if err != nil || token == nil || !token.Valid {
		return Identity{}, ErrDenied
	}
	get := func(name string) string {
		var value string
		if json.Unmarshal(claims[name], &value) != nil || !clean(value, 2048) {
			return ""
		}
		return value
	}
	date := func(name string) int64 {
		raw := string(claims[name])
		if !decimal.MatchString(raw) {
			return 0
		}
		value, _ := strconv.ParseInt(raw, 10, 64)
		return value
	}
	now := time.Now().Unix()
	iat, nbf, exp := date("iat"), date("nbf"), date("exp")
	if get("iss") != Issuer || get("aud") != v.audience ||
		iat <= 0 || iat > now+30 || iat < now-600 || nbf <= 0 || nbf > now+30 ||
		exp <= now || exp <= iat || exp-iat > 600 || nbf >= exp {
		return Identity{}, ErrDenied
	}
	// This profile intentionally does not admit reusable calls: the protocol
	// cannot distinguish repeated calls of one reference in the same attempt.
	if _, exists := claims["job_workflow_ref"]; exists {
		return Identity{}, ErrDenied
	}
	if _, exists := claims["job_workflow_sha"]; exists {
		return Identity{}, ErrDenied
	}
	i := Identity{
		Issuer: get("iss"), Subject: get("sub"), Repository: get("repository"),
		RepositoryID: get("repository_id"), OwnerID: get("repository_owner_id"),
		RunID: get("run_id"), RunAttempt: get("run_attempt"), Ref: get("ref"),
		Event: get("event_name"), Actor: get("actor"), ActorID: get("actor_id"),
		WorkflowRef: get("workflow_ref"), WorkflowSHA: get("workflow_sha"),
		CheckRunID: get("check_run_id"),
	}
	_, i.EnvironmentPresent = claims["environment"]
	if !repository.MatchString(i.Repository) || i.Subject == "" || i.Ref == "" || i.Event == "" ||
		!clean(i.Actor, 100) || !decimal.MatchString(i.RepositoryID) ||
		!decimal.MatchString(i.OwnerID) || !decimal.MatchString(i.ActorID) ||
		!decimal.MatchString(i.RunID) || len(i.RunID) > 128 ||
		!decimal.MatchString(i.RunAttempt) || len(i.RunAttempt) > 128 ||
		!revision.MatchString(i.WorkflowSHA) || len(i.WorkflowRef) > 1024 {
		return Identity{}, ErrDenied
	}
	if !strings.HasPrefix(i.WorkflowRef, i.Repository+"/.github/workflows/") {
		return Identity{}, ErrDenied
	}
	return i, nil
}

func (v *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	select {
	case v.gate <- struct{}{}:
		defer func() { <-v.gate }()
	case <-ctx.Done():
		return nil, ErrUnavailable
	}
	now := time.Now()
	if now.Sub(v.refreshed) < 5*time.Minute {
		if key := v.keys[kid]; key != nil {
			return key, nil
		}
		if kid == v.missing && now.Sub(v.lastRefresh) < 30*time.Second {
			return nil, ErrDenied
		}
		if now.Sub(v.unknownRefresh) < 30*time.Second {
			return nil, ErrUnavailable
		}
		v.unknownRefresh = now
	}
	if now.Sub(v.lastRefresh) < time.Second {
		return nil, ErrUnavailable
	}
	v.lastRefresh = now
	return observeOperation(ctx, telemetry.SpanAdmissionKeys, func(ctx context.Context) (*rsa.PublicKey, error) {
		return v.refreshKey(ctx, kid, now)
	})
}

func (v *Verifier) refreshKey(ctx context.Context, kid string, now time.Time) (*rsa.PublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, Issuer+"/.well-known/jwks", nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	response, err := v.client.Do(request)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return nil, ErrUnavailable
	}
	keys, err := parseJWKS(data)
	if err != nil {
		return nil, err
	}
	v.keys, v.refreshed = keys, now
	admissionLog.Printf("refreshed token signing keys: count=%d", len(keys))
	if key := keys[kid]; key != nil {
		return key, nil
	}
	admissionLog.Printf("token signing key not found in refreshed set")
	v.missing = kid
	return nil, ErrDenied
}

// parseJWKS decodes a JWKS document into RSA signing keys keyed by kid. Keys
// that are not RS256 signature keys are skipped; malformed or oversized keys,
// duplicate IDs, and empty or oversized documents are unavailable.
func parseJWKS(data []byte) (map[string]*rsa.PublicKey, error) {
	var document struct {
		Keys []struct {
			ID   string `json:"kid"`
			Type string `json:"kty"`
			Use  string `json:"use"`
			Alg  string `json:"alg"`
			N    string `json:"n"`
			E    string `json:"e"`
		} `json:"keys"`
	}
	if json.Unmarshal(data, &document) != nil || len(document.Keys) == 0 || len(document.Keys) > 64 {
		return nil, ErrUnavailable
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, key := range document.Keys {
		if key.Type != "RSA" || key.Use != "sig" || (key.Alg != "" && key.Alg != "RS256") {
			continue
		}
		n, ne := base64.RawURLEncoding.DecodeString(key.N)
		e, ee := base64.RawURLEncoding.DecodeString(key.E)
		if ne != nil || ee != nil || len(n) < 256 || len(n) > 512 || len(e) == 0 || len(e) > 4 || keys[key.ID] != nil {
			return nil, ErrUnavailable
		}
		exponent := new(big.Int).SetBytes(e)
		if !exponent.IsInt64() || exponent.Int64() < 3 || exponent.Int64()%2 == 0 || exponent.Int64() > 1<<31-1 {
			return nil, ErrUnavailable
		}
		keys[key.ID] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent.Int64())}
	}
	return keys, nil
}
