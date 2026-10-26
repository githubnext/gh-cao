package sessionarchiveblob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

type bearerTransport struct {
	credential clientcredentials.Config
	base       http.RoundTripper
	mu         sync.Mutex
	token      *oauth2.Token
}

func (t *bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	if !t.token.Valid() {
		// The token endpoint uses the same no-redirect policy as service requests.
		ctx := context.WithValue(r.Context(), oauth2.HTTPClient, &http.Client{
			Transport: t.base, CheckRedirect: rejectRedirect, Timeout: 20 * time.Second,
		})
		token, err := t.credential.Token(ctx)
		if err != nil {
			t.mu.Unlock()
			return nil, errors.New("archive Entra token acquisition failed")
		}
		t.token = token
	}
	token := t.token.AccessToken
	t.mu.Unlock()
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	return t.base.RoundTrip(r)
}

func newHTTP(c Config, scope string) *http.Client {
	return &http.Client{Timeout: c.QueryTimeout + 10*time.Second, CheckRedirect: rejectRedirect,
		Transport: &bearerTransport{base: http.DefaultTransport, credential: clientcredentials.Config{
			ClientID: c.ClientID, ClientSecret: c.ClientSecret,
			TokenURL: "https://login.microsoftonline.com/" + c.TenantID + "/oauth2/v2.0/token",
			Scopes:   []string{scope}, AuthStyle: oauth2.AuthStyleInParams,
		}}}
}

func rejectRedirect(_ *http.Request, _ []*http.Request) error {
	return errors.New("archive redirects are prohibited")
}

type blobClient struct {
	root string
	http *http.Client
}

type statusError int

func (s statusError) Error() string { return fmt.Sprintf("archive Blob returned HTTP %d", s) }

func isStatus(err error, status int) bool {
	var e statusError
	return errors.As(err, &e) && int(e) == status
}

func (b *blobClient) request(ctx context.Context, method, key, query string, body io.Reader, size int64, headers http.Header) (*http.Response, error) {
	// Keys are generated internally, never accepted as URLs or SAS.
	if strings.ContainsAny(key, "?#%\\") || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return nil, ErrScope
	}
	target := b.root
	if key != "" {
		target += "/" + key
	}
	if query != "" {
		target += "?" + query
	}
	r, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, errors.New("construct archive Blob request failed")
	}
	r.ContentLength = size
	r.Header = headers.Clone()
	r.Header.Set("x-ms-version", "2023-11-03")
	r.Header.Set("x-ms-date", time.Now().UTC().Format(http.TimeFormat))
	response, err := b.http.Do(r)
	if err != nil {
		return nil, errors.New("archive Blob request failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		return nil, statusError(response.StatusCode)
	}
	return response, nil
}

func (b *blobClient) private(ctx context.Context) error {
	u, err := url.Parse(b.root)
	if err != nil {
		return ErrScope
	}
	parent := *b
	u.Path = "/" + strings.Split(strings.TrimPrefix(u.Path, "/"), "/")[0]
	parent.root = u.String()
	response, err := parent.request(ctx, http.MethodGet, "", "restype=container", nil, 0, http.Header{})
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.Header.Get("x-ms-blob-public-access") != "" {
		return errors.New("archive Blob container must be private")
	}
	return nil
}

func (b *blobClient) get(ctx context.Context, key string, max int64) ([]byte, string, error) {
	response, err := b.request(ctx, http.MethodGet, key, "", nil, 0, http.Header{})
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, "", ErrCapacity
	}
	etag := response.Header.Get("ETag")
	if etag == "" {
		return nil, "", ErrIntegrity
	}
	return data, etag, nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (b *blobClient) verify(ctx context.Context, key, expected string, size int64) error {
	response, err := b.request(ctx, http.MethodGet, key, "", nil, 0, http.Header{})
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(response.Body, size+1))
	if err != nil || count != size || hex.EncodeToString(hash.Sum(nil)) != expected {
		return ErrIntegrity
	}
	return nil
}

func (b *blobClient) immutable(ctx context.Context, key string, body io.Reader, size int64, expected string) error {
	headers := http.Header{"If-None-Match": {"*"}, "x-ms-blob-type": {"BlockBlob"},
		"Content-Type": {"application/octet-stream"}}
	response, err := b.request(ctx, http.MethodPut, key, "", body, size, headers)
	if err != nil && !isStatus(err, http.StatusPreconditionFailed) {
		return err
	}
	if response != nil {
		_ = response.Body.Close()
	}
	// Includes deduplication after an ambiguous upload acknowledgement. Never
	// trust object names or a caller-provided metadata digest alone.
	return b.verify(ctx, key, expected, size)
}

func (b *blobClient) immutableJSON(ctx context.Context, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return ErrIntegrity
	}
	return b.immutable(ctx, key, bytes.NewReader(data), int64(len(data)), digest(data))
}

func (b *blobClient) lease(ctx context.Context, action, id string) (string, error) {
	headers := http.Header{"x-ms-lease-action": {action}}
	if action == "acquire" {
		headers.Set("x-ms-lease-duration", "60")
		headers.Set("x-ms-proposed-lease-id", id)
	} else {
		headers.Set("x-ms-lease-id", id)
	}
	response, err := b.request(ctx, http.MethodPut, "active.json", "comp=lease", nil, 0, headers)
	if err != nil {
		return "", ErrLease
	}
	defer func() { _ = response.Body.Close() }()
	lease := response.Header.Get("x-ms-lease-id")
	if action != "release" && lease != id {
		return "", ErrLease
	}
	return lease, nil
}
