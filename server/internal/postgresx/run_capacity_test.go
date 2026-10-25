package postgresx

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func capacitySnapshot(t *testing.T, store *Store, count, tools int, domain string) error {
	t.Helper()
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Abort(t.Context()) })
	at := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour).Format(time.RFC3339)
	type record struct {
		source string
		row    model.Row
	}
	records := []record{
		{"$repositories", model.Row{"id": "repository", "owner": "octo", "name": "api"}},
		{"$workflows", model.Row{"id": "workflow", "repositoryId": "repository", "path": ".github/workflows/test.md"}},
	}
	for index := 1; index <= count; index++ {
		id := fmt.Sprintf("run:%02d", index)
		records = append(records,
			record{"$runs", model.Row{
				"id": id, "repositoryId": "repository", "workflowId": "workflow",
				"owner": "octo", "repository": "api", "workflowPath": ".github/workflows/test.md",
				"githubRunId": fmt.Sprint(index), "status": "completed", "conclusion": "success",
				"createdAt": at, "startedAt": at, "completedAt": at,
			}},
			record{"$audits", model.Row{"id": "audit:" + id, "runId": id, "source": "session", "type": "agent.step"}},
			record{"$domains", model.Row{"id": "domain:" + id, "runId": id, "domain": domain}},
		)
		for tool := range tools {
			records = append(records, record{"$tools", model.Row{"id": fmt.Sprintf("tool:%s:%d", id, tool), "runId": id, "name": "test"}})
		}
	}
	for _, record := range records {
		if err := writer.Append(t.Context(), record.source, record.row); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Quality(t.Context(), "$runs", model.Metadata{
		"availability": "available", "completeness": "complete", "as-of": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = writer.Publish(t.Context(), "capacity-fixture")
	if err != nil {
		writer.Abort(t.Context())
	}
	return err
}

func assertCapacityIDs(t *testing.T, store *Store, expected string) {
	t.Helper()
	var ids string
	if err := store.db.QueryRowContext(t.Context(), `SELECT coalesce(string_agg(id,',' ORDER BY id),'')
		FROM runs WHERE namespace=$1`, store.namespace).Scan(&ids); err != nil || ids != expected {
		t.Fatalf("retained runs=%s want=%s err=%v", ids, expected, err)
	}
	for _, source := range []string{"$audits", "$domains", "$tools"} {
		var orphans int
		if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+entityTables[source].name+` c
			LEFT JOIN runs r ON r.namespace=c.namespace AND r.id=c.run_id AND r.run_at=c.run_at
			WHERE c.namespace=$1 AND r.id IS NULL`, store.namespace).Scan(&orphans); err != nil || orphans != 0 {
			t.Fatalf("%s orphan count=%d err=%v", source, orphans, err)
		}
	}
}

func TestRunCapacityKeepsNewestSubtreesAndDailyCounts(t *testing.T) {
	store, _ := nativeTestStore(t)
	store.runCapacity = runCapacity{rows: 3, bytes: query.MaxWorkingBytes}
	for _, count := range []int{6, 6, 7} {
		if err := capacitySnapshot(t, store, count, 1, "example.com"); err != nil {
			t.Fatal(err)
		}
		expected := "run:04,run:05,run:06"
		if count == 7 {
			expected = "run:05,run:06,run:07"
		}
		assertCapacityIDs(t, store, expected)
		state, err := store.State(t.Context())
		if err != nil || state.Counts["$runs"] != 3 || state.Counts["$audits"] != 3 ||
			state.Counts["$tools"] != 3 || state.Counts["$domains"] != 3 ||
			state.Counts["$repositories"] != 1 || state.Counts["$workflows"] != 1 {
			t.Fatalf("capacity changed inventory or lost an owned bundle: %+v %v", state, err)
		}
		var daily int
		if err := store.db.QueryRowContext(t.Context(), `SELECT sum(total) FROM daily_run_counts
			WHERE namespace=$1`, store.namespace).Scan(&daily); err != nil || daily != count {
			t.Fatalf("capacity eviction/replay changed daily contributions: %d want=%d err=%v", daily, count, err)
		}
		var completeness string
		if err := store.db.QueryRowContext(t.Context(), `SELECT completeness FROM cao_quality
			WHERE namespace=$1 AND collection='$runs'`, store.namespace).Scan(&completeness); err != nil || completeness != "partial" {
			t.Fatalf("evicted population claimed complete coverage: %s %v", completeness, err)
		}
		var frameComplete bool
		if err := store.db.QueryRowContext(t.Context(), `SELECT weight_valid FROM cao_sample_statistical_frame
			WHERE namespace=$1`, store.namespace).Scan(&frameComplete); err != nil || frameComplete {
			t.Fatalf("evicted population produced valid population weights: %v %v", frameComplete, err)
		}
	}
}

func TestRunCapacityBoundsChildFanout(t *testing.T) {
	store, _ := nativeTestStore(t)
	store.runCapacity = runCapacity{rows: 4, bytes: query.MaxWorkingBytes}
	if err := capacitySnapshot(t, store, 4, 3, "example.com"); err != nil {
		t.Fatal(err)
	}
	assertCapacityIDs(t, store, "run:04")
	state, err := store.State(t.Context())
	if err != nil || state.Counts["$tools"] != 3 {
		t.Fatalf("child source exceeded its query boundary: %+v %v", state, err)
	}
}

func TestRunCapacityBoundsLogicalBytesAndRollsBackOversize(t *testing.T) {
	store, _ := nativeTestStore(t)
	payload := strings.Repeat("x", 4096)
	if err := capacitySnapshot(t, store, 1, 1, payload); err != nil {
		t.Fatal(err)
	}
	var bundleBytes int64
	for _, source := range []string{"$runs", "$audits", "$tools", "$domains"} {
		table := entityTables[source]
		var size int64
		if err := store.db.QueryRowContext(t.Context(), "SELECT sum("+entityBytesSQL(table, "t")+
			") FROM "+table.name+" t WHERE namespace=$1", store.namespace).Scan(&size); err != nil {
			t.Fatal(err)
		}
		bundleBytes += size
	}
	store.runCapacity = runCapacity{rows: 10, bytes: bundleBytes + bundleBytes/2}
	if err := capacitySnapshot(t, store, 3, 1, payload); err != nil {
		t.Fatal(err)
	}
	assertCapacityIDs(t, store, "run:03")
	before, err := store.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	store.runCapacity.bytes = 1
	if err := capacitySnapshot(t, store, 4, 1, payload); err == nil ||
		!strings.Contains(err.Error(), "cannot fit") {
		t.Fatalf("oversize newest bundle was silently discarded: %v", err)
	}
	after, err := store.State(t.Context())
	if err != nil || before.Revision != after.Revision {
		t.Fatalf("failed capacity write changed publication: before=%+v after=%+v err=%v", before, after, err)
	}
	assertCapacityIDs(t, store, "run:03")
	var total int
	if err := store.db.QueryRowContext(t.Context(), "SELECT sum(total) FROM daily_run_counts WHERE namespace=$1",
		store.namespace).Scan(&total); err != nil || total != 3 {
		t.Fatalf("failed capacity write leaked daily contributions: %d %v", total, err)
	}
}

func TestRunCapacityIsNamespaceLocalAndIncludesSparseAdmission(t *testing.T) {
	store, config := nativeTestStore(t)
	other, err := NewConfig(t.Context(), config, "other")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if err := capacitySnapshot(t, other, 6, 1, "example.com"); err != nil {
		t.Fatal(err)
	}
	store.runCapacity = runCapacity{rows: 3, bytes: query.MaxWorkingBytes}
	if err := capacitySnapshot(t, store, 6, 1, "example.com"); err != nil {
		t.Fatal(err)
	}
	identity := admissionIdentity()
	if err := store.RecordAdmittedRun(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	assertCapacityIDs(t, store, "github:run:acme/project:9007199254740993,run:05,run:06")
	assertCapacityIDs(t, other, "run:01,run:02,run:03,run:04,run:05,run:06")
	store.runCapacity.rows = 1
	second := identity
	second.RunID = "9007199254740994"
	if err := store.RecordAdmittedRun(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	assertCapacityIDs(t, store, "github:run:acme/project:9007199254740994")
	assertCapacityIDs(t, other, "run:01,run:02,run:03,run:04,run:05,run:06")
}

func TestRunCapacityIncludesConclusionPublication(t *testing.T) {
	store, _ := nativeTestStore(t)
	store.runCapacity = runCapacity{rows: 16, bytes: query.MaxWorkingBytes}
	if err := capacitySnapshot(t, store, 10, 1, "example.com"); err != nil {
		t.Fatal(err)
	}
	identity := admissionIdentity()
	if err := store.RecordConcludedRun(t.Context(), identity, canonicalConclusionInput(identity), time.Now()); err != nil {
		t.Fatal(err)
	}
	state, err := store.State(t.Context())
	if err != nil || state.Counts["$audits"] > 16 || state.Counts["$runs"] >= 11 {
		t.Fatalf("conclusion did not enforce child-source capacity: %+v %v", state, err)
	}
	var latest bool
	if err := store.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM runs
		WHERE namespace=$1 AND id='github:run:acme/project:9007199254740993')`, store.namespace).Scan(&latest); err != nil || !latest {
		t.Fatalf("conclusion lost fresh Run: %v %v", latest, err)
	}
}

func TestRunCapacityOrdersByTimeBeforeIdentity(t *testing.T) {
	store, _ := nativeTestStore(t)
	store.runCapacity = runCapacity{rows: 1, bytes: query.MaxWorkingBytes}
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	if err := writer.Append(t.Context(), "$repositories", model.Row{"id": "repository", "owner": "octo", "name": "api"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(t.Context(), "$workflows", model.Row{
		"id": "workflow", "repositoryId": "repository", "path": ".github/workflows/test.md",
	}); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"z-oldest", "a-newest"} {
		at := time.Now().UTC().Truncate(time.Hour).Add(time.Duration(index-2) * time.Hour).Format(time.RFC3339)
		if err := writer.Append(t.Context(), "$runs", model.Row{
			"id": id, "repositoryId": "repository", "workflowId": "workflow",
			"status": "completed", "createdAt": at, "startedAt": at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Publish(t.Context(), "time-order"); err != nil {
		t.Fatal(err)
	}
	assertCapacityIDs(t, store, "a-newest")
}
