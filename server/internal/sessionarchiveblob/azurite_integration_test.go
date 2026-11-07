package sessionarchiveblob

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/githubnext/gh-cao/server/internal/simulator"
)

type azuriteTransport struct {
	local   *simulator.LocalTransport
	target  *url.URL
	account string
	key     []byte
}

func (a *azuriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.Host != a.account+".blob.core.windows.net" || request.URL.User != nil {
		return nil, errors.New("Azurite fixture blocked an unapproved Blob authority")
	}
	mapped := request.Clone(request.Context())
	target := *request.URL
	target.Scheme, target.Host = a.target.Scheme, a.target.Host
	target.Path = "/" + a.account + request.URL.Path
	mapped.URL, mapped.Host = &target, target.Host
	mapped.Header.Set("x-ms-date", time.Now().UTC().Format(http.TimeFormat))
	mapped.Header.Set("x-ms-version", "2023-11-03")
	length := ""
	if mapped.ContentLength > 0 {
		length = strconv.FormatInt(mapped.ContentLength, 10)
	}
	standard := strings.Join([]string{mapped.Method,
		mapped.Header.Get("Content-Encoding"), mapped.Header.Get("Content-Language"), length,
		mapped.Header.Get("Content-MD5"), mapped.Header.Get("Content-Type"), "",
		mapped.Header.Get("If-Modified-Since"), mapped.Header.Get("If-Match"),
		mapped.Header.Get("If-None-Match"), mapped.Header.Get("If-Unmodified-Since"), mapped.Header.Get("Range"),
	}, "\n")
	var names []string
	azureHeaders := map[string]string{}
	for name, values := range mapped.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-ms-") {
			name = strings.ToLower(name)
			names = append(names, name)
			azureHeaders[name] = strings.Join(values, ",")
		}
	}
	slices.Sort(names)
	var canonical strings.Builder
	canonical.WriteString(standard + "\n")
	for _, name := range names {
		canonical.WriteString(name + ":" + azureHeaders[name] + "\n")
	}
	canonical.WriteString("/" + a.account + mapped.URL.Path)
	names = names[:0]
	for name := range mapped.URL.Query() {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		values := mapped.URL.Query()[name]
		slices.Sort(values)
		canonical.WriteString("\n" + name + ":" + strings.Join(values, ","))
	}
	signature := hmac.New(sha256.New, a.key)
	_, _ = signature.Write([]byte(canonical.String()))
	mapped.Header.Set("Authorization", "SharedKey "+a.account+":"+base64.StdEncoding.EncodeToString(signature.Sum(nil)))
	return a.local.RoundTrip(mapped)
}

func TestAzuriteBlobStoreIntegration(t *testing.T) {
	endpoint := os.Getenv("CAO_AZURITE_BLOB_URL")
	if endpoint == "" {
		t.Skip("requires official Azurite; run npm run test:integration:azure-services")
	}
	account := os.Getenv("CAO_AZURITE_ACCOUNT")
	if !accountPattern.MatchString(account) {
		t.Fatal("Azurite requires an explicit synthetic account")
	}
	target, err := url.Parse(endpoint)
	if err != nil || target.Path != "/"+account {
		t.Fatal("Azurite requires the exact path-style synthetic account endpoint")
	}
	local, err := simulator.NewLocalTransport(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(local.CloseIdleConnections)
	key, err := base64.StdEncoding.DecodeString(os.Getenv("CAO_AZURITE_KEY"))
	if err != nil || len(key) != 32 {
		t.Fatal("Azurite requires a synthetic 32-byte account key")
	}
	client := &http.Client{Transport: &azuriteTransport{local: local, target: target, account: account, key: key},
		Timeout: 5 * time.Second, CheckRedirect: rejectRedirect}
	container := "cao-adapter-" + strings.ToLower(rand.Text())
	root := "https://" + account + ".blob.core.windows.net/" + container
	containerRequest := func(method string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, method, root+"?restype=container", nil) // #nosec G704 -- validated synthetic Blob authority is mapped only to the exact numeric-loopback emulator.
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request) // #nosec G704 -- azuriteTransport rejects other authorities and LocalTransport blocks non-loopback connections.
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Fatalf("Azurite container operation returned HTTP %d", response.StatusCode)
		}
	}
	containerRequest(http.MethodPut)
	t.Cleanup(func() { containerRequest(http.MethodDelete) })
	var store BlobStore = &blobClient{root: root + "/archive", http: client}
	t.Run("private immutable verified objects", func(t *testing.T) {
		if err := store.CheckPrivate(t.Context()); err != nil {
			t.Fatal(err)
		}
		data := []byte("synthetic immutable evidence")
		for range 2 {
			if err := store.WriteImmutable(t.Context(), "objects/evidence", bytes.NewReader(data), int64(len(data)), digest(data)); err != nil {
				t.Fatal(err)
			}
		}
		read, etag, err := store.Read(t.Context(), "objects/evidence", 4096)
		if err != nil || !bytes.Equal(read, data) || etag == "" {
			t.Fatalf("read verified object failed: %v", err)
		}
		if err := store.Verify(t.Context(), "objects/evidence", digest([]byte("wrong")), int64(len(data))); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("corrupt digest accepted: %v", err)
		}
		if _, _, err := store.Read(t.Context(), "missing", 4096); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing object did not return domain error: %v", err)
		}
	})
	t.Run("leased pointer CAS", func(t *testing.T) {
		if err := store.CreatePointer(t.Context(), []byte("{}")); err != nil {
			t.Fatal(err)
		}
		if err := store.CreatePointer(t.Context(), []byte("{}")); !errors.Is(err, ErrObjectExists) {
			t.Fatalf("pointer create was not exclusive: %v", err)
		}
		lease := uuid.NewString()
		if err := store.Lease(t.Context(), "acquire", lease); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := store.Lease(context.WithoutCancel(t.Context()), "release", lease); err != nil {
				t.Error(err)
			}
		})
		_, original, err := store.Read(t.Context(), "active.json", 4096)
		if err != nil {
			t.Fatal(err)
		}
		data := []byte(`{"generation":1}`)
		etag, err := store.ComparePointer(t.Context(), data, original, lease)
		if err != nil || etag == "" || etag == original {
			t.Fatalf("pointer CAS failed: %v", err)
		}
		if _, err := store.ComparePointer(t.Context(), []byte("{}"), original, lease); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale pointer CAS did not conflict: %v", err)
		}
		read, _, err := store.Read(t.Context(), "active.json", 4096)
		if err != nil || !bytes.Equal(read, data) {
			t.Fatal("stale writer changed the committed pointer")
		}
		if err := store.Lease(t.Context(), "renew", lease); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("delete and cancellation", func(t *testing.T) {
		if err := store.Delete(t.Context(), "objects/evidence"); err != nil {
			t.Fatal(err)
		}
		if err := store.Delete(t.Context(), "objects/evidence"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("repeated delete did not report missing object: %v", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, _, err := store.Read(ctx, "active.json", 4096); err == nil {
			t.Fatal("cancelled Blob read succeeded")
		}
	})
}
