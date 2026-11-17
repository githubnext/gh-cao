package repositorymemory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
)

type capacityAdapter struct {
	name string
	new  func(t *testing.T, maxValueBytes int64) RemoteCache
}

func capacityAdapters() []capacityAdapter {
	return []capacityAdapter{
		{"memory", func(t *testing.T, maxValueBytes int64) RemoteCache {
			t.Helper()
			store, err := memory.New(memory.Config{MaxCacheValueBytes: maxValueBytes})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			return store
		}},
		{"postgres", func(t *testing.T, maxValueBytes int64) RemoteCache {
			t.Helper()
			url := os.Getenv("POSTGRES_URL")
			if url == "" {
				t.Skip("POSTGRES_URL is required for real PostgreSQL conformance")
			}
			var nonce [16]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				t.Fatal(err)
			}
			store, err := postgres.New(t.Context(), url,
				"repositorymemory-capacity-"+hex.EncodeToString(nonce[:]),
				postgres.Config{MaxCacheValueBytes: maxValueBytes})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := store.DeleteNamespace(ctx); err != nil {
					t.Error(err)
				}
				_ = store.Close()
			})
			return store
		}},
	}
}

// Redis skips disposable-cache admission without an error. The other adapters
// report operational.ErrCapacity, which must degrade to an uncached read
// instead of failing a validated GitHub resolution.
func TestRemoteResolverServesUncachedWhenCacheRejectsValue(t *testing.T) {
	for _, adapter := range capacityAdapters() {
		t.Run(adapter.name, func(t *testing.T) {
			content := []byte(strings.Repeat("a", 2048))
			source := &remoteSource{
				commit: testOID,
				entries: []githubapp.GitTreeEntry{
					{Path: "notes.md", OID: testOID, Mode: "100644", Type: "blob", Size: int64(len(content))},
				},
				content:  content,
				response: githubapp.APIResponse{ETag: `"ref-v1"`},
			}
			// The campaign manifest fits; the 2 KiB file does not.
			resolver := newCapacityResolver(adapter.new(t, 1024), source)

			campaign, err := resolver.Campaign(t.Context(), "example")
			if err != nil || campaign == nil || len(campaign.Files) != 1 {
				t.Fatalf("campaign: %#v, %v", campaign, err)
			}
			for range 2 {
				got, err := resolver.Content(t.Context(), "example", "notes.md")
				if err != nil || string(got) != string(content) {
					t.Fatalf("content was not served uncached: %d bytes, %v", len(got), err)
				}
			}
			if source.blobCalls != 2 {
				t.Fatalf("rejected value must not be reported as cached: blob=%d", source.blobCalls)
			}
		})
	}
}

func TestRemoteResolverServesCampaignWhenCacheRejectsManifest(t *testing.T) {
	for _, adapter := range capacityAdapters() {
		t.Run(adapter.name, func(t *testing.T) {
			source := &remoteSource{
				commit: testOID,
				entries: []githubapp.GitTreeEntry{
					{Path: "notes.md", OID: testOID, Mode: "100644", Type: "blob", Size: 5},
				},
				content:  []byte("hello"),
				response: githubapp.APIResponse{ETag: `"ref-v1"`},
			}
			resolver := newCapacityResolver(adapter.new(t, 8), source)

			for range 2 {
				campaign, err := resolver.Campaign(t.Context(), "example")
				if err != nil || campaign == nil || len(campaign.Files) != 1 {
					t.Fatalf("campaign: %#v, %v", campaign, err)
				}
			}
			got, err := resolver.Content(t.Context(), "example", "notes.md")
			if err != nil || string(got) != "hello" {
				t.Fatalf("content: %q, %v", got, err)
			}
			if source.treeCalls != 3 {
				t.Fatalf("rejected manifest must not be reported as cached: tree=%d", source.treeCalls)
			}
		})
	}
}

func TestRemoteResolverServesMissingBranchWhenCacheRejectsNegativeEntry(t *testing.T) {
	for _, adapter := range capacityAdapters() {
		t.Run(adapter.name, func(t *testing.T) {
			resolver := newCapacityResolver(adapter.new(t, 8), &remoteSource{refErr: githubapp.ErrNotFound})
			campaign, err := resolver.Campaign(t.Context(), "missing")
			if err != nil || campaign != nil {
				t.Fatalf("missing branch: %#v, %v", campaign, err)
			}
		})
	}
}

// Only an explicit capacity rejection is non-fatal; infrastructure failures
// keep propagating.
func TestRemoteResolverPropagatesCacheWriteFailures(t *testing.T) {
	writeErr := errors.New("cache unavailable")
	source := &remoteSource{
		commit: testOID,
		entries: []githubapp.GitTreeEntry{
			{Path: "notes.md", OID: testOID, Mode: "100644", Type: "blob", Size: 5},
		},
		content:  []byte("hello"),
		response: githubapp.APIResponse{ETag: `"ref-v1"`},
	}
	cache := &failingWriteCache{remoteCache: newRemoteCache(), err: writeErr, failCampaign: true}
	resolver := &RemoteResolver{
		Cache: cache, Installations: remoteInstallation{id: 42}, Source: source,
		Governor: &remoteGovernor{}, ControlRepository: "owner/control",
	}
	if _, err := resolver.Campaign(t.Context(), "example"); !errors.Is(err, writeErr) {
		t.Fatalf("campaign write failure was swallowed: %v", err)
	}
	cache.failCampaign = false
	if _, err := resolver.Campaign(t.Context(), "example"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Content(t.Context(), "example", "notes.md"); !errors.Is(err, writeErr) {
		t.Fatalf("file write failure was swallowed: %v", err)
	}
}

type failingWriteCache struct {
	*remoteCache
	err          error
	failCampaign bool
}

func (c *failingWriteCache) CacheRepositoryMemoryCampaign(
	ctx context.Context, campaign string, content []byte, ttl time.Duration,
) error {
	if c.failCampaign {
		return c.err
	}
	return c.remoteCache.CacheRepositoryMemoryCampaign(ctx, campaign, content, ttl)
}

func (c *failingWriteCache) CacheRepositoryMemoryFile(
	context.Context, string, string, string, []byte, time.Duration,
) error {
	return c.err
}

func newCapacityResolver(cache RemoteCache, source *remoteSource) *RemoteResolver {
	return &RemoteResolver{
		Cache: cache, Installations: remoteInstallation{id: 42}, Source: source,
		Governor: &remoteGovernor{}, ControlRepository: "owner/control",
	}
}
