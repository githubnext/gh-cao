package redisx

import (
	"strings"
	"testing"
)

func TestStoreNamespacesUseDisjointOperationalKeys(t *testing.T) {
	first := NewStore(&Client{}, "first")
	second := NewStore(&Client{}, "second")
	for _, suffix := range []string{"state:ingestion-health", "lock:collector", "repository-memory:campaign:example", "marketplace:registry:example", "g:revision"} {
		if first.Key(suffix) == second.Key(suffix) {
			t.Fatalf("namespaces shared Redis key %q", suffix)
		}
		if !strings.HasPrefix(first.Key(suffix), "cao:first:") || !strings.HasPrefix(second.Key(suffix), "cao:second:") {
			t.Fatalf("operational key %q is not namespaced", suffix)
		}
	}
}
