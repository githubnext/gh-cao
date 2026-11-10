package server

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var actionsOIDCLog = logger.New("cao:server:actions-oidc")

const (
	actionsOIDCIssuer   = "https://token.actions.githubusercontent.com"
	actionsOIDCAudience = "https://cao.githubnext.com"
	actionsOIDCJWKS     = actionsOIDCIssuer + "/.well-known/jwks"
)

type actionsOIDCClaims struct {
	Issuer            string          `json:"iss"`
	Audience          json.RawMessage `json:"aud"`
	Repository        string          `json:"repository"`
	RepositoryID      string          `json:"repository_id"`
	RepositoryOwnerID string          `json:"repository_owner_id"`
	Subject           string          `json:"sub"`
	Ref               string          `json:"ref"`
	Actor             string          `json:"actor"`
	IssuedAt          int64           `json:"iat"`
	NotBefore         int64           `json:"nbf"`
	ExpiresAt         int64           `json:"exp"`
}

type hostedMCPRefusal struct {
	code string
	err  error
}

func (refusal *hostedMCPRefusal) Error() string { return refusal.err.Error() }
func (refusal *hostedMCPRefusal) Unwrap() error { return refusal.err }

func verifyHostedActionsMCP(ctx context.Context, config Config, request *http.Request) (string, error) {
	token, ok := bearerToken(request)
	provenance := request.Header.Get("X-GitHub-OIDC-Token")
	if !ok || token == "" || provenance == "" {
		return "", &hostedMCPRefusal{"credentials_missing", errors.New("actions token and OIDC provenance are required")}
	}
	claims, err := verifyActionsOIDC(ctx, config, provenance)
	if err != nil {
		return "", &hostedMCPRefusal{"oidc_invalid", err}
	}
	metadata, err := actionsRepositoryMetadataForToken(ctx, config, token)
	if err != nil {
		return "", &hostedMCPRefusal{"repository_unavailable", err}
	}
	ref := "refs/heads/" + metadata.DefaultBranch
	owner, name, err := parseActionsRepository(metadata.FullName)
	if err != nil {
		return "", &hostedMCPRefusal{"repository_unavailable", err}
	}
	legacySubject := "repo:" + metadata.FullName + ":ref:" + ref
	immutableSubject := "repo:" + owner + "@" + metadata.OwnerID + "/" + name + "@" + metadata.ID + ":ref:" + ref
	if claims.Ref != ref ||
		claims.RepositoryID != metadata.ID ||
		claims.RepositoryOwnerID != metadata.OwnerID ||
		(claims.Subject != legacySubject && claims.Subject != immutableSubject) {
		return "", &hostedMCPRefusal{"provenance_mismatch", errors.New("GitHub Actions OIDC provenance does not match the repository default branch")}
	}
	if err := verifyGitHubActionsPermissions(ctx, config, token); err != nil {
		return "", &hostedMCPRefusal{"permissions_denied", err}
	}
	return strings.ToLower(claims.Actor), nil
}

type actionsRepositoryMetadata struct {
	FullName      string
	ID            string
	OwnerID       string
	DefaultBranch string
}

func actionsRepositoryMetadataForToken(
	ctx context.Context, config Config, token string,
) (actionsRepositoryMetadata, error) {
	owner, name, err := parseActionsRepository(config.ActionsRepository)
	if err != nil {
		return actionsRepositoryMetadata{}, err
	}
	baseURL, err := resolveGitHubAPIBaseURL(config.GitHubAPIURL)
	if err != nil {
		return actionsRepositoryMetadata{}, err
	}
	client := config.ActionsHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), nil)
	if err != nil {
		return actionsRepositoryMetadata{}, errors.New("GitHub Actions repository identity check could not be created")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-GitHub-Api-Version", actionsGitHubAPIVersion)
	request.Header.Set("User-Agent", "gh-cao-mcp")
	response, err := client.Do(request)
	if err != nil {
		return actionsRepositoryMetadata{}, errors.New("GitHub Actions repository identity check failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return actionsRepositoryMetadata{}, errors.New("GitHub Actions repository identity is unavailable")
	}
	var repository struct {
		FullName      string `json:"full_name"`
		ID            int64  `json:"id"`
		DefaultBranch string `json:"default_branch"`
		Owner         struct {
			ID int64 `json:"id"`
		} `json:"owner"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&repository) != nil ||
		!strings.EqualFold(repository.FullName, strings.TrimSpace(config.ActionsRepository)) ||
		repository.ID <= 0 || repository.Owner.ID <= 0 ||
		repository.DefaultBranch == "" || len(repository.DefaultBranch) > 255 ||
		strings.IndexFunc(repository.DefaultBranch, func(r rune) bool { return r <= ' ' || r == '\x7f' }) >= 0 {
		return actionsRepositoryMetadata{}, errors.New("GitHub Actions repository identity is invalid")
	}
	return actionsRepositoryMetadata{
		FullName: repository.FullName, ID: strconv.FormatInt(repository.ID, 10),
		OwnerID: strconv.FormatInt(repository.Owner.ID, 10), DefaultBranch: repository.DefaultBranch,
	}, nil
}

func verifyActionsOIDC(ctx context.Context, config Config, token string) (actionsOIDCClaims, error) {
	invalid := errors.New("invalid GitHub Actions OIDC provenance")
	if len(token) > 8192 {
		return actionsOIDCClaims{}, invalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return actionsOIDCClaims{}, invalid
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(headerBytes) > 1024 {
		return actionsOIDCClaims{}, invalid
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
		Type      string `json:"typ"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "RS256" ||
		header.KeyID == "" || len(header.KeyID) > 256 {
		return actionsOIDCClaims{}, invalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) > 8192 {
		return actionsOIDCClaims{}, invalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) == 0 {
		return actionsOIDCClaims{}, invalid
	}
	key, err := fetchActionsOIDCKey(ctx, config.ActionsHTTPClient, header.KeyID)
	if err != nil {
		return actionsOIDCClaims{}, invalid
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return actionsOIDCClaims{}, invalid
	}
	var claims actionsOIDCClaims
	if json.Unmarshal(payload, &claims) != nil {
		return actionsOIDCClaims{}, invalid
	}
	var audience string
	if json.Unmarshal(claims.Audience, &audience) != nil {
		var audiences []string
		if json.Unmarshal(claims.Audience, &audiences) != nil || len(audiences) != 1 {
			return actionsOIDCClaims{}, invalid
		}
		audience = audiences[0]
	}
	now := time.Now().Unix()
	repository := strings.TrimSpace(config.ActionsRepository)
	if claims.Issuer != actionsOIDCIssuer || audience != actionsOIDCAudience ||
		!strings.EqualFold(claims.Repository, repository) ||
		claims.Actor == "" || len(claims.Actor) > 100 ||
		strings.IndexFunc(claims.Actor, func(r rune) bool { return r <= ' ' || r == '\x7f' }) >= 0 ||
		claims.IssuedAt <= 0 || claims.IssuedAt > now+30 ||
		claims.ExpiresAt <= now || claims.ExpiresAt-claims.IssuedAt > 600 ||
		claims.NotBefore > now+30 {
		return actionsOIDCClaims{}, invalid
	}
	return claims, nil
}

// jwksKey is one entry in the Actions OIDC JSON Web Key Set response.
type jwksKey struct {
	ID       string `json:"kid"`
	Type     string `json:"kty"`
	Use      string `json:"use"`
	Alg      string `json:"alg"`
	Modulus  string `json:"n"`
	Exponent string `json:"e"`
}

// selectActionsOIDCKey finds the RSA signature key identified by keyID among
// keys and decodes it into an *rsa.PublicKey. It is a pure function extracted
// from fetchActionsOIDCKey's inline loop, so the key-matching, modulus/
// exponent bounds, and odd-public-exponent rejection rules are each testable
// directly against constructed JWKS entries, without an HTTP round trip.
func selectActionsOIDCKey(keys []jwksKey, keyID string) (*rsa.PublicKey, error) {
	for _, key := range keys {
		if key.ID != keyID || key.Type != "RSA" || key.Use != "sig" ||
			(key.Alg != "" && key.Alg != "RS256") {
			continue
		}
		n, nErr := base64.RawURLEncoding.DecodeString(key.Modulus)
		e, eErr := base64.RawURLEncoding.DecodeString(key.Exponent)
		if nErr != nil || eErr != nil || len(n) < 256 || len(n) > 512 || len(e) == 0 || len(e) > 4 {
			return nil, errors.New("invalid Actions OIDC key")
		}
		exponent := new(big.Int).SetBytes(e)
		if !exponent.IsInt64() || exponent.Int64() < 3 || exponent.Int64()%2 == 0 ||
			exponent.Int64() > 1<<31-1 {
			return nil, errors.New("invalid Actions OIDC exponent")
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent.Int64())}, nil
	}
	return nil, errors.New("actions OIDC signing key is unknown")
}

func fetchActionsOIDCKey(ctx context.Context, client *http.Client, keyID string) (*rsa.PublicKey, error) {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, actionsOIDCJWKS, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		actionsOIDCLog.PrintfContext(ctx, "jwks fetch request failed")
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		actionsOIDCLog.PrintfContext(ctx, "jwks fetch rejected status=%d", response.StatusCode)
		return nil, errors.New("actions OIDC JWKS is unavailable")
	}
	var jwks struct {
		Keys []jwksKey `json:"keys"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if decoder.Decode(&jwks) != nil || len(jwks.Keys) > 32 {
		actionsOIDCLog.PrintfContext(ctx, "jwks decode failed")
		return nil, errors.New("invalid Actions OIDC JWKS")
	}
	key, err := selectActionsOIDCKey(jwks.Keys, keyID)
	if err != nil {
		actionsOIDCLog.PrintfContext(ctx, "jwks key selection failed count=%d", len(jwks.Keys))
		return nil, err
	}
	return key, nil
}
