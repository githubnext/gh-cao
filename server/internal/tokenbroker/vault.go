package tokenbroker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const vaultResponseLimit = 64 * 1024

var (
	vaultHostPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,22}[a-z0-9]\.vault\.azure\.net$`)
	vaultPathPattern = regexp.MustCompile(`^/secrets/[a-zA-Z0-9-]{1,127}(/[a-zA-Z0-9-]{1,127})?$`)
)

// KeyVault reads a private key secret using Azure managed identity.
type KeyVault struct {
	SecretURL  string
	ClientID   string
	HTTPClient *http.Client
}

// PrivateKey reads the current secret value without caching credentials or keys.
func (v KeyVault) PrivateKey(ctx context.Context) ([]byte, error) {
	secretURL, err := url.Parse(v.SecretURL)
	if err != nil || strings.Contains(v.SecretURL, "#") || !safeVaultURL(secretURL) {
		return nil, errors.New("invalid Key Vault secret URL")
	}

	client := http.Client{Timeout: 15 * time.Second}
	if v.HTTPClient != nil {
		client = *v.HTTPClient
		if client.Timeout == 0 {
			client.Timeout = 15 * time.Second
		}
	}
	// Copy the client so even an injected client's redirect policy cannot send
	// the managed identity token or deployment header to a different endpoint.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	client.Jar = nil

	token, err := v.managedIdentityToken(ctx, &client)
	if err != nil {
		return nil, err
	}
	query := secretURL.Query()
	query.Set("api-version", "7.4")
	secretURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, secretURL.String(), nil)
	if err != nil {
		return nil, errors.New("cannot create Key Vault request")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	body, err := vaultRequest(&client, request)
	if err != nil {
		return nil, errors.New("key vault secret request failed")
	}
	var secret struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(body, &secret) != nil || strings.TrimSpace(secret.Value) == "" {
		return nil, errors.New("invalid Key Vault secret response")
	}
	return []byte(secret.Value), nil
}

func safeVaultURL(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && u.Opaque == "" &&
		u.User == nil && u.Port() == "" && u.Host == u.Hostname() &&
		vaultHostPattern.MatchString(strings.ToLower(u.Hostname())) &&
		!strings.Contains(u.Hostname(), "--") &&
		u.RawPath == "" && vaultPathPattern.MatchString(u.Path) &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == ""
}

func (v KeyVault) managedIdentityToken(ctx context.Context, client *http.Client) (string, error) {
	endpoint, configured := os.LookupEnv("IDENTITY_ENDPOINT")
	header := os.Getenv("IDENTITY_HEADER")
	apiVersion := "2018-02-01"
	if configured || header != "" {
		u, err := url.Parse(endpoint)
		if err != nil || strings.Contains(endpoint, "#") || !safeIdentityEndpoint(u) || header == "" || strings.ContainsAny(header, "\r\n") {
			return "", errors.New("invalid managed identity configuration")
		}
		apiVersion = "2019-08-01"
	} else {
		endpoint = "http://169.254.169.254/metadata/identity/oauth2/token"
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", errors.New("invalid managed identity configuration")
	}
	query := u.Query()
	query.Set("api-version", apiVersion)
	query.Set("resource", "https://vault.azure.net")
	if v.ClientID != "" {
		query.Set("client_id", v.ClientID)
	}
	u.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", errors.New("cannot create managed identity request")
	}
	if configured {
		request.Header.Set("X-IDENTITY-HEADER", header)
	} else {
		request.Header.Set("Metadata", "true")
	}
	body, err := vaultRequest(client, request)
	if err != nil {
		return "", errors.New("managed identity request failed")
	}
	var response struct {
		AccessToken string          `json:"access_token"`
		TokenType   string          `json:"token_type"`
		ExpiresOn   json.RawMessage `json:"expires_on"`
	}
	if json.Unmarshal(body, &response) != nil ||
		!validIdentityToken(response.AccessToken) ||
		(response.TokenType != "" && !strings.EqualFold(response.TokenType, "Bearer")) {
		return "", errors.New("invalid managed identity response")
	}
	expiry := string(response.ExpiresOn)
	if strings.HasPrefix(expiry, `"`) {
		if json.Unmarshal(response.ExpiresOn, &expiry) != nil {
			return "", errors.New("invalid managed identity response")
		}
	}
	expiresOn, err := strconv.ParseInt(expiry, 10, 64)
	if err != nil || expiresOn <= time.Now().Unix() {
		return "", errors.New("invalid managed identity response")
	}
	return response.AccessToken, nil
}

func safeIdentityEndpoint(u *url.URL) bool {
	if u == nil || u.Scheme != "http" || u.Opaque != "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil {
		return false
	}
	// App Service can expose its identity endpoint on this platform-owned
	// link-local address rather than loopback. Do not admit its neighbors.
	if !ip.IsLoopback() && (u.Hostname() != "169.254.255.2" || u.Path != "/msi/token") {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		return err == nil && n > 0 && n <= 65535
	}
	return u.Host == u.Hostname() || u.Host == "["+u.Hostname()+"]"
}

func validIdentityToken(token string) bool {
	if token == "" {
		return false
	}
	for _, c := range token {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9' || strings.ContainsRune("-._~+/=", c) {
			continue
		}
		return false
	}
	return true
}

func vaultRequest(client *http.Client, request *http.Request) ([]byte, error) {
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("unexpected response status")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, vaultResponseLimit+1))
	if err != nil || len(body) > vaultResponseLimit {
		return nil, errors.New("invalid response body")
	}
	return body, nil
}
