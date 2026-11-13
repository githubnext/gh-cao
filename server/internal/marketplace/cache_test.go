package marketplace

import (
	"context"
	"testing"
	"time"
)

// memoryCache is a minimal in-memory Cache double, keyed by (registryID,
// generation) exactly like the Operational-backed implementation, so tests can
// assert isolation and TTL propagation without a real Operational server.
type memoryCache struct {
	entries map[[2]string][]byte
	ttls    map[[2]string]time.Duration
	getErr  error
	setErr  error
}

func newMemoryCache() *memoryCache {
	return &memoryCache{entries: map[[2]string][]byte{}, ttls: map[[2]string]time.Duration{}}
}

func (c *memoryCache) Get(_ context.Context, registryID, generation string) ([]byte, bool, error) {
	if c.getErr != nil {
		return nil, false, c.getErr
	}
	data, ok := c.entries[[2]string{registryID, generation}]
	return data, ok, nil
}

func (c *memoryCache) Set(_ context.Context, registryID, generation string, data []byte, ttl time.Duration) error {
	if c.setErr != nil {
		return c.setErr
	}
	c.entries[[2]string{registryID, generation}] = data
	c.ttls[[2]string{registryID, generation}] = ttl
	return nil
}

func TestResolveCachesAResolvedRegistryAndReusesItOnASubsequentCall(t *testing.T) {
	server := newFakeGitHubServer(t, fakeGitHubConfig{})
	cache := newMemoryCache()
	config := &Config{
		CacheTTL:   90 * time.Second,
		Registries: []Registry{{ID: "official", Repository: "example/packages", Ref: "main", APIURL: server.baseURL()}},
	}
	opts := Options{HTTPClient: insecureTestClient()}

	first := Resolve(t.Context(), config, "generation-1", cache, opts)
	if len(first.Packages) != 1 {
		t.Fatalf("expected a package on the first resolve, got: %#v", first.Packages)
	}
	requestsAfterFirst := server.requestCount()
	if requestsAfterFirst == 0 {
		t.Fatal("expected the first resolve to call the fake GitHub API")
	}

	second := Resolve(t.Context(), config, "generation-1", cache, opts)
	if len(second.Packages) != 1 || second.Packages[0].ID != first.Packages[0].ID {
		t.Fatalf("expected the cached package to be reused, got: %#v", second.Packages)
	}
	if server.requestCount() != requestsAfterFirst {
		t.Fatalf("expected the second resolve to be served entirely from cache, but it made %d more requests",
			server.requestCount()-requestsAfterFirst)
	}
	if ttl := cache.ttls[[2]string{"official", "generation-1"}]; ttl != 90*time.Second {
		t.Fatalf("expected the configured cache TTL to be used, got: %v", ttl)
	}
}

func TestResolveCacheIsIsolatedByGeneration(t *testing.T) {
	server := newFakeGitHubServer(t, fakeGitHubConfig{})
	cache := newMemoryCache()
	config := &Config{Registries: []Registry{{ID: "official", Repository: "example/packages", Ref: "main", APIURL: server.baseURL()}}}
	opts := Options{HTTPClient: insecureTestClient()}

	Resolve(t.Context(), config, "generation-1", cache, opts)
	requestsAfterFirst := server.requestCount()

	Resolve(t.Context(), config, "generation-2", cache, opts)
	if server.requestCount() == requestsAfterFirst {
		t.Fatal("expected a different generation to miss the cache and re-resolve")
	}
}

func TestResolveCacheIsIsolatedByRegistryID(t *testing.T) {
	server := newFakeGitHubServer(t, fakeGitHubConfig{})
	cache := newMemoryCache()
	opts := Options{HTTPClient: insecureTestClient()}

	Resolve(t.Context(), &Config{Registries: []Registry{{ID: "official", Repository: "example/packages", Ref: "main", APIURL: server.baseURL()}}},
		"generation-1", cache, opts)
	requestsAfterFirst := server.requestCount()

	Resolve(t.Context(), &Config{Registries: []Registry{{ID: "third-party", Repository: "example/packages", Ref: "main", APIURL: server.baseURL()}}},
		"generation-1", cache, opts)
	if server.requestCount() == requestsAfterFirst {
		t.Fatal("expected a different registry id to miss the cache and re-resolve")
	}
}

func TestResolveAppliesDefaultTTLWhenPolicyOmitsCacheTTLSeconds(t *testing.T) {
	server := newFakeGitHubServer(t, fakeGitHubConfig{})
	cache := newMemoryCache()
	config := &Config{Registries: []Registry{{ID: "official", Repository: "example/packages", Ref: "main", APIURL: server.baseURL()}}}

	Resolve(t.Context(), config, "generation-1", cache, Options{HTTPClient: insecureTestClient()})

	if ttl := cache.ttls[[2]string{"official", "generation-1"}]; ttl != DefaultCacheTTL {
		t.Fatalf("expected the default cache TTL to apply, got: %v", ttl)
	}
}

func TestResolveTreatsACacheReadErrorAsAMissRatherThanFailing(t *testing.T) {
	server := newFakeGitHubServer(t, fakeGitHubConfig{})
	cache := newMemoryCache()
	cache.getErr = context.DeadlineExceeded
	config := &Config{Registries: []Registry{{ID: "official", Repository: "example/packages", Ref: "main", APIURL: server.baseURL()}}}

	result := Resolve(t.Context(), config, "generation-1", cache, Options{HTTPClient: insecureTestClient()})
	if len(result.Packages) != 1 {
		t.Fatalf("expected resolution to still succeed on a cache read error, got: %#v", result.Packages)
	}
}

func TestResolveTreatsACacheWriteErrorAsNonFatal(t *testing.T) {
	server := newFakeGitHubServer(t, fakeGitHubConfig{})
	cache := newMemoryCache()
	cache.setErr = context.DeadlineExceeded
	config := &Config{Registries: []Registry{{ID: "official", Repository: "example/packages", Ref: "main", APIURL: server.baseURL()}}}

	result := Resolve(t.Context(), config, "generation-1", cache, Options{HTTPClient: insecureTestClient()})
	if len(result.Packages) != 1 {
		t.Fatalf("expected resolution to still succeed on a cache write error, got: %#v", result.Packages)
	}
}

func TestResolveWithoutACacheStillResolvesEveryRegistryEachCall(t *testing.T) {
	server := newFakeGitHubServer(t, fakeGitHubConfig{})
	config := &Config{Registries: []Registry{{ID: "official", Repository: "example/packages", Ref: "main", APIURL: server.baseURL()}}}
	opts := Options{HTTPClient: insecureTestClient()}

	Resolve(t.Context(), config, "generation-1", nil, opts)
	requestsAfterFirst := server.requestCount()
	Resolve(t.Context(), config, "generation-1", nil, opts)
	if server.requestCount() == requestsAfterFirst {
		t.Fatal("expected a nil cache to force re-resolution on every call")
	}
}

func TestLoadCachedRegistryRejectsCorruptPayloads(t *testing.T) {
	cache := newMemoryCache()
	cache.entries[[2]string{"official", "generation-1"}] = []byte("not json")
	if _, hit := loadCachedRegistry(t.Context(), cache, "official", "generation-1"); hit {
		t.Fatal("expected a corrupt cache payload to be treated as a miss")
	}
}

func TestDecodeCachedRegistryPayloadRejectsEmptyBytes(t *testing.T) {
	if packages, ok := decodeCachedRegistryPayload(nil); ok || packages != nil {
		t.Fatalf("expected empty bytes to decode as a miss, got packages=%#v ok=%t", packages, ok)
	}
}

func TestDecodeCachedRegistryPayloadRejectsMalformedJSON(t *testing.T) {
	if _, ok := decodeCachedRegistryPayload([]byte("{not valid")); ok {
		t.Fatal("expected malformed JSON to decode as a miss")
	}
}

func TestDecodeCachedRegistryPayloadAcceptsAWellFormedPayload(t *testing.T) {
	packages, ok := decodeCachedRegistryPayload([]byte(`{"packages":[{"id":"a/b"}]}`))
	if !ok {
		t.Fatal("expected a well-formed payload to decode successfully")
	}
	if len(packages) != 1 || packages[0].ID != "a/b" {
		t.Fatalf("expected the decoded package to round-trip, got: %#v", packages)
	}
}

func TestLoadCachedRegistryTreatsACacheReadErrorAsAMiss(t *testing.T) {
	cache := newMemoryCache()
	cache.getErr = context.DeadlineExceeded
	if _, hit := loadCachedRegistry(t.Context(), cache, "official", "generation-1"); hit {
		t.Fatal("expected a cache read error to be treated as a miss")
	}
}
