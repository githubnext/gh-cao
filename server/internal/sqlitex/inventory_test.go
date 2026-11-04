package sqlitex

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestSnapshotRepositoriesUseInventoryOnly(t *testing.T) {
	path := testutil.SQLiteSnapshot(t, map[string][]model.Row{
		"repositories": {
			{"id": "repository:a", "owner": "octo", "name": "one"},
			{"id": "repository:b", "owner": "octo", "name": "two"},
		},
		"runs": {{"id": "run:foreign", "owner": "other", "repository": "not-in-inventory"}},
	})
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	repositories, err := store.SnapshotRepositories(t.Context())
	if err != nil || !reflect.DeepEqual(repositories, []string{"octo/one", "octo/two"}) {
		t.Fatalf("snapshot scope included non-inventory evidence: %v %v", repositories, err)
	}
}

func TestSnapshotRepositoriesRejectOversizedInventory(t *testing.T) {
	repositories := make([]model.Row, 10001)
	for index := range repositories {
		name := fmt.Sprintf("repo-%05d", index)
		repositories[index] = model.Row{"id": "repository:" + name, "owner": "octo", "name": name}
	}
	path := testutil.SQLiteSnapshot(t, map[string][]model.Row{"repositories": repositories})
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := store.SnapshotRepositories(t.Context()); err == nil || !strings.Contains(err.Error(), "10000-repository") {
		t.Fatalf("oversized snapshot widened archive scope: %v", err)
	}
}
