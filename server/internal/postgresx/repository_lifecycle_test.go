package postgresx

import (
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func TestRepositoryLifecycleSurvivesProjectionAndExpires(t *testing.T) {
	store, _ := nativeTestStore(t)
	ctx := t.Context()
	seed, err := store.BeginIngestion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Abort(ctx)
	if err := seed.Append(ctx, "$repositories", model.Row{
		"id": "github:repository:42", "owner": "octo", "name": "api",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Publish(ctx, "initial"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRepositoryLifecycle(ctx, 42, "octo/api", "active", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, lifecycle := range []string{"archived", "active", "deleted"} {
		if err := store.UpdateRepositoryLifecycle(ctx, 42, "octo/api", lifecycle, time.Now()); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := store.db.QueryRowContext(ctx, `SELECT lifecycle FROM repositories WHERE namespace=$1 AND id=$2`,
			store.namespace, "github:repository:42").Scan(&got); err != nil || got != lifecycle {
			t.Fatalf("repository lifecycle = %q, error %v, want %q", got, err, lifecycle)
		}
	}
	writer, err := store.BeginIngestion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(ctx)
	if err := writer.Append(ctx, "$repositories", model.Row{
		"id": "github:repository:42", "owner": "octo", "name": "api",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Publish(ctx, "after-lifecycle"); err != nil {
		t.Fatal(err)
	}
	var lifecycle string
	if err := store.db.QueryRowContext(ctx, `SELECT lifecycle FROM repositories WHERE namespace=$1 AND id=$2`,
		store.namespace, "github:repository:42").Scan(&lifecycle); err != nil || lifecycle != "deleted" {
		t.Fatalf("projection lost lifecycle: %q, %v", lifecycle, err)
	}
	active, err := store.RepositoryActive(ctx, "octo/api")
	if err != nil || active {
		t.Fatalf("deleted repository remains collectible: %t, %v", active, err)
	}
	if err := store.UpdateRepositoryLifecycle(ctx, 42, "octo/api", "active", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT lifecycle FROM repositories WHERE namespace=$1 AND id=$2`,
		store.namespace, "github:repository:42").Scan(&lifecycle); err != nil || lifecycle != "deleted" {
		t.Fatalf("stale created task revived deleted repository: %q, %v", lifecycle, err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE cao_repository_lifecycle SET changed_at=$1 WHERE namespace=$2`,
		time.Now().AddDate(0, 0, -9), store.namespace); err != nil {
		t.Fatal(err)
	}
	if err := store.RunPartitionMaintenance(ctx, time.Now(), 7, 7); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM cao_repository_lifecycle WHERE namespace=$1`, store.namespace).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired lifecycle rows = %d, %v", count, err)
	}
}

func TestRepositoryLifecycleDoesNotResurrectEvictedRepositories(t *testing.T) {
	store, _ := nativeTestStore(t)
	ctx := t.Context()
	for _, state := range []string{"active", "archived", "deleted"} {
		if err := store.UpdateRepositoryLifecycle(ctx, 42, "octo/old", state, time.Now()); err != nil {
			t.Fatal(err)
		}
		writer, err := store.BeginIngestion(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Abort(ctx)
		if _, err := writer.Publish(ctx, "evicted-"+state); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM repositories WHERE namespace=$1`,
			store.namespace).Scan(&count); err != nil || count != 0 {
			t.Fatalf("lifecycle %s resurrected evicted repository: %d, %v", state, count, err)
		}
	}
	if err := store.ClearRepositoryLifecycles(ctx, []int64{0, 42}); err != nil {
		t.Fatal(err)
	}
	active, err := store.RepositoryActive(ctx, "octo/old")
	if err != nil || !active {
		t.Fatalf("verified rediscovery did not clear legacy lifecycle: %t, %v", active, err)
	}
}

func TestRepositoryLifecyclePresence(t *testing.T) {
	mask := repositoryLifecyclePresence()
	if len(mask) != len(entityTables["$repositories"].columns) ||
		strings.Count(mask, "1") != 4 {
		t.Fatalf("invalid repository presence mask %q", mask)
	}
}

func TestParseRepositoryCoordinate(t *testing.T) {
	tests := []struct {
		repository string
		owner      string
		name       string
		ok         bool
	}{
		{repository: "octo/api", owner: "octo", name: "api", ok: true},
		{repository: "octo/api/extra", owner: "", name: "", ok: false},
		{repository: "octo", owner: "", name: "", ok: false},
		{repository: "/api", owner: "", name: "", ok: false},
		{repository: "octo/", owner: "", name: "", ok: false},
		{repository: "", owner: "", name: "", ok: false},
	}
	for _, test := range tests {
		owner, name, ok := parseRepositoryCoordinate(test.repository)
		if owner != test.owner || name != test.name || ok != test.ok {
			t.Fatalf("parseRepositoryCoordinate(%q) = (%q, %q, %t), want (%q, %q, %t)",
				test.repository, owner, name, ok, test.owner, test.name, test.ok)
		}
	}
}

func TestValidRepositoryLifecycle(t *testing.T) {
	for _, lifecycle := range []string{"active", "archived", "deleted"} {
		if !validRepositoryLifecycle(lifecycle) {
			t.Errorf("validRepositoryLifecycle(%q) = false, want true", lifecycle)
		}
	}
	for _, lifecycle := range []string{"", "ACTIVE", "pending", "active "} {
		if validRepositoryLifecycle(lifecycle) {
			t.Errorf("validRepositoryLifecycle(%q) = true, want false", lifecycle)
		}
	}
}

func TestClassifyRepositoryLifecycleUpdateRejectsEachPreconditionInOrder(t *testing.T) {
	validTime := time.Now()
	tests := []struct {
		name       string
		githubID   int64
		repository string
		lifecycle  string
		admittedAt time.Time
		wantStage  repositoryLifecycleRejectionStage
	}{
		{name: "non-positive github id", githubID: 0, repository: "octo/api", lifecycle: "active", admittedAt: validTime, wantStage: repositoryLifecycleRejectionStageID},
		{name: "negative github id", githubID: -1, repository: "octo/api", lifecycle: "active", admittedAt: validTime, wantStage: repositoryLifecycleRejectionStageID},
		{name: "unknown lifecycle value", githubID: 1, repository: "octo/api", lifecycle: "pending", admittedAt: validTime, wantStage: repositoryLifecycleRejectionStageLifecycle},
		{name: "zero admission timestamp", githubID: 1, repository: "octo/api", lifecycle: "active", admittedAt: time.Time{}, wantStage: repositoryLifecycleRejectionStageTimestamp},
		{name: "invalid coordinate", githubID: 1, repository: "octo", lifecycle: "active", admittedAt: validTime, wantStage: repositoryLifecycleRejectionStageCoordinate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			owner, name, stage, err := classifyRepositoryLifecycleUpdate(test.githubID, test.repository, test.lifecycle, test.admittedAt)
			if err == nil || stage != test.wantStage || owner != "" || name != "" {
				t.Fatalf("classifyRepositoryLifecycleUpdate() = (%q, %q, %s, %v), want stage %s with an error",
					owner, name, stage, err, test.wantStage)
			}
		})
	}
}

func TestClassifyRepositoryLifecycleUpdateAcceptsValidInput(t *testing.T) {
	admittedAt := time.Now()
	owner, name, stage, err := classifyRepositoryLifecycleUpdate(42, "octo/api", "archived", admittedAt)
	if err != nil || stage != "" || owner != "octo" || name != "api" {
		t.Fatalf("classifyRepositoryLifecycleUpdate() = (%q, %q, %s, %v), want (octo, api, \"\", nil)",
			owner, name, stage, err)
	}
}
