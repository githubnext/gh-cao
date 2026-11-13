package postgresx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil/pgtemplate"
)

func nativeTestStore(t *testing.T) (*Store, *pgx.ConnConfig) {
	t.Helper()
	endpoint := os.Getenv("POSTGRES_URL")
	if endpoint == "" {
		t.Skip("POSTGRES_URL is unset")
	}
	if config, ok := pgtemplate.Clone(t, t.Context(), endpoint, func(ctx context.Context, config *pgx.ConnConfig) error {
		store, err := NewConfig(ctx, config)
		if err != nil {
			return err
		}
		return store.Close()
	}); ok {
		store, err := NewConfig(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store, config
	}
	config, err := pgx.ParseConfig(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config.Copy())
	schema := fmt.Sprintf("cao_native_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rows, err := admin.QueryContext(ctx, `SELECT c.relname FROM pg_class c
			JOIN pg_namespace n ON n.oid=c.relnamespace
			WHERE n.nspname=$1 AND c.relkind='p'`, schema)
		if err == nil {
			defer func() { _ = rows.Close() }()
			var parents []string
			for rows.Next() {
				var name string
				if err = rows.Scan(&name); err != nil {
					break
				}
				parents = append(parents, name)
			}
			if err == nil {
				err = rows.Err()
			}
			_ = rows.Close()
			if err == nil {
				for _, name := range parents {
					if _, err = admin.ExecContext(ctx, "DROP TABLE "+pgx.Identifier{schema, name}.Sanitize()+" CASCADE"); err != nil {
						break
					}
				}
			}
		}
		if err != nil {
			t.Error("remove isolated Postgres partitioned tables")
		} else if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error("remove isolated Postgres test schema")
		}
		_ = admin.Close()
	})
	config.RuntimeParams["search_path"] = schema
	store, err := NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, config
}

func nativeParents(ctx context.Context, t *testing.T, w *Writer) {
	t.Helper()
	for _, record := range []struct {
		source string
		row    model.Row
	}{
		{"$repositories", model.Row{"id": "repository", "owner": "octo", "name": "api"}},
		{"$workflows", model.Row{"id": "workflow", "repositoryId": "repository", "path": ".github/workflows/test.md"}},
		{"$runs", model.Row{"id": "run", "workflowId": "workflow", "repositoryId": "repository", "owner": "octo", "repository": "api", "githubRunId": "9007199254740993"}},
	} {
		if err := w.Append(ctx, record.source, record.row); err != nil {
			t.Fatal(err)
		}
	}
}

func nativeSeed(ctx context.Context, t *testing.T, store *Store, sources map[string][]model.Row) {
	t.Helper()
	w, err := store.BeginIngestion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort(ctx)
	nativeParents(ctx, t, w)
	for name, rows := range sources {
		for _, row := range rows {
			if err := w.Append(ctx, name, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := w.Publish(ctx, "typed"); err != nil {
		t.Fatal(err)
	}
}

func TestFreshSchemaReopen(t *testing.T) {
	store, config := nativeTestStore(t)
	nativeSeed(t.Context(), t, store, nil)
	before, err := store.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	after, err := reopened.State(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("fresh reopen changed state: %+v %v", after, err)
	}
	if _, err := store.db.ExecContext(t.Context(), "UPDATE cao_contract SET digest='changed'"); err != nil {
		t.Fatal(err)
	}
	if changed, err := NewConfig(t.Context(), config); err == nil {
		_ = changed.Close()
		t.Fatal("different physical contract was accepted")
	}
}

func TestNumericSessionStatusUsesNativeTextStorage(t *testing.T) {
	store, _ := nativeTestStore(t)
	nativeSeed(t.Context(), t, store, map[string][]model.Row{"$audits": {
		{"id": "firewall", "runId": "run", "source": "session", "type": "firewall.event", "status": json.Number("200")},
		{"id": "unknown", "runId": "run", "source": "session", "type": "firewall.event", "status": nil},
		{"id": "absent", "runId": "run", "source": "session", "type": "firewall.event"},
	}})
	result, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{{
		Name: "statuses", From: "$audits", Select: []query.SelectedField{{Field: "id"}, {Field: "status"}},
	}}, []string{"statuses"})
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Row{{"id": "firewall", "status": "200"}, {"id": "unknown", "status": nil}, {"id": "absent"}}
	if !reflect.DeepEqual(result["statuses"].Rows, want) {
		t.Fatalf("numeric status or unknown presence changed: %#v", result["statuses"].Rows)
	}
}

func TestNativeStorageAndSQLPresence(t *testing.T) {
	store, _ := nativeTestStore(t)
	nativeSeed(t.Context(), t, store, map[string][]model.Row{"$domains": {
		{"id": "null", "runId": "run", "domain": nil, "requestCount": json.Number("9007199254740993")},
		{"id": "absent", "runId": "run", "unused": make(chan int)},
	}})
	result, metrics, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{{Name: "native", From: "$domains", Select: []query.SelectedField{{Field: "id"}, {Field: "domain"}, {Field: "requestCount"}}}}, []string{"native"})
	if err != nil {
		t.Fatal(err)
	}
	rows := result["native"].Rows
	if len(rows) != 2 || rows[0]["domain"] != nil || rows[0]["requestCount"] != json.Number("9007199254740993") {
		t.Fatalf("typed rows lost precision/null: %#v", rows)
	}
	if _, present := rows[0]["domain"]; !present {
		t.Fatal("explicit null became absent")
	}
	if _, present := rows[1]["domain"]; present {
		t.Fatal("absent became explicit null")
	}
	if len(metrics.PushedDown) == 0 {
		t.Fatalf("not a complete SQL execution: %+v", metrics)
	}
	var forbidden int
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM information_schema.columns
		WHERE table_schema=current_schema() AND (data_type IN ('json','jsonb') OR table_name IN ('cao_sources','cao_source_rows','cao_values') OR (table_name LIKE '%\_values' AND table_name <> 'operational_values'))`).Scan(&forbidden); err != nil {
		t.Fatal(err)
	}
	if forbidden != 0 {
		t.Fatal("forbidden generic or persisted JSON storage exists")
	}
}

func TestNativeAtomicFailureAndBoundedCopy(t *testing.T) {
	store, _ := nativeTestStore(t)
	nativeSeed(t.Context(), t, store, nil)
	before, _ := store.State(t.Context())
	for _, failure := range []string{"duplicate", "orphan", "type"} {
		w, err := store.BeginIngestion(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		nativeParents(t.Context(), t, w)
		row := model.Row{"id": "bad", "runId": "run"}
		if failure == "orphan" {
			row["runId"] = "missing"
		}
		if failure == "type" {
			row["requestCount"] = "invalid"
		}
		err = w.Append(t.Context(), "$domains", row)
		if failure == "duplicate" && err == nil {
			err = w.Append(t.Context(), "$domains", row)
		}
		if err == nil {
			_, err = w.Publish(t.Context(), "bad")
		}
		w.Abort(t.Context())
		if err == nil {
			t.Fatalf("%s published", failure)
		}
		after, err := store.State(t.Context())
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("failure changed publication: %+v %v", after, err)
		}
	}
	w, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort(t.Context())
	nativeParents(t.Context(), t, w)
	for index := range BatchRows*3 + 1 {
		if err := w.Append(t.Context(), "$domains", model.Row{"id": fmt.Sprint(index), "runId": "run", "domain": strings.Repeat("x", 50)}); err != nil {
			t.Fatal(err)
		}
		if w.rows >= BatchRows || w.bytes >= BatchBytes {
			t.Fatal("batch memory was not bounded")
		}
	}
	state, err := w.Publish(t.Context(), "many")
	if err != nil {
		t.Fatal(err)
	}
	if state.Counts["$domains"] != BatchRows*3+1 {
		t.Fatalf("CopyFrom lost rows: %+v", state.Counts)
	}
}

func TestNativeSnapshotAndNamespaceIsolation(t *testing.T) {
	store, config := nativeTestStore(t)
	nativeSeed(t.Context(), t, store, nil)
	other, err := NewConfig(t.Context(), config, "other")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	nativeSeed(t.Context(), t, other, nil)
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader NativeReader) error {
		before, err := reader.State(ctx)
		if err != nil {
			return err
		}
		nativeSeed(ctx, t, store, map[string][]model.Row{"$domains": {{"id": "new", "runId": "run"}}})
		after, err := reader.State(ctx)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("repeatable-read publication changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteNamespace(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err := other.State(t.Context())
	if err != nil || !state.Ready || state.Counts["$runs"] != 1 {
		t.Fatalf("namespace isolation failed: %+v %v", state, err)
	}
}

func TestNativeInventoryEnrichment(t *testing.T) {
	store, _ := nativeTestStore(t)
	w, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	defer w.Abort(t.Context())
	nativeParents(t.Context(), t, w)
	if err := w.AppendInventory(t.Context(), "$repositories", model.Row{"id": "repository", "owner": "must-not-replace", "visibility": "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Publish(t.Context(), "inventory"); err != nil {
		t.Fatal(err)
	}
	var owner, visibility string
	if err := store.db.QueryRowContext(t.Context(), "SELECT owner,visibility FROM repositories WHERE namespace=$1", store.namespace).Scan(&owner, &visibility); err != nil {
		t.Fatal(err)
	}
	if owner != "octo" || visibility != "private" {
		t.Fatalf("native observation precedence lost: %s %s", owner, visibility)
	}
}

func TestLinkedRetentionDoesNotProvisionOldShards(t *testing.T) {
	t.Setenv("CAO_POSTGRES_LINKED_RETENTION_DAYS", "")
	store, _ := nativeTestStore(t)
	now := time.Now().UTC()
	day := dayStart(now.AddDate(0, 0, -21)).Format("20060102")
	for _, table := range []struct {
		name string
		want bool
	}{
		{"runs", true},
		{"audits", false},
		{"tools", false},
	} {
		var exists bool
		if err := store.db.QueryRowContext(t.Context(), "SELECT to_regclass($1) IS NOT NULL", table.name+"_d"+day).Scan(&exists); err != nil || exists != table.want {
			t.Fatalf("%s historical partition exists=%v, want %v: %v", table.name, exists, table.want, err)
		}
	}
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	for _, record := range []struct {
		source string
		row    model.Row
	}{
		{"$repositories", model.Row{"id": "repository"}},
		{"$workflows", model.Row{"id": "workflow", "repositoryId": "repository"}},
		{"$runs", model.Row{"id": "historical", "repositoryId": "repository", "workflowId": "workflow", "createdAt": now.AddDate(0, 0, -21).Format(time.RFC3339)}},
		{"$runs", model.Row{"id": "recent", "repositoryId": "repository", "workflowId": "workflow", "createdAt": now.Format(time.RFC3339)}},
		{"$tools", model.Row{"id": "old-tool", "runId": "historical"}},
		{"$tools", model.Row{"id": "recent-tool", "runId": "recent"}},
	} {
		if err := writer.Append(t.Context(), record.source, record.row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Publish(t.Context(), "linked-retention"); err != nil {
		t.Fatal(err)
	}
	var runs, tools int
	if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM runs WHERE namespace=$1", store.namespace).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM tools WHERE namespace=$1", store.namespace).Scan(&tools); err != nil {
		t.Fatal(err)
	}
	if runs != 2 || tools != 1 {
		t.Fatalf("historical ingestion kept %d runs and %d tools, want 2 and 1", runs, tools)
	}
}

func TestDailyRunPartitionsAndRetention(t *testing.T) {
	t.Setenv("CAO_POSTGRES_LINKED_RETENTION_DAYS", "30")
	store, _ := nativeTestStore(t)
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -28).Format(time.RFC3339)
	middle := now.AddDate(0, 0, -14).Format(time.RFC3339)
	current := now.Format(time.RFC3339)
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	for _, record := range []struct {
		source string
		row    model.Row
	}{
		{"$repositories", model.Row{"id": "repository"}},
		{"$workflows", model.Row{"id": "workflow", "repositoryId": "repository"}},
		{"$experiments", model.Row{"id": "experiment", "workflowId": "workflow"}},
		{"$graders", model.Row{"id": "grader", "workflowId": "workflow"}},
		{"$evals", model.Row{"id": "eval", "workflowId": "workflow"}},
		{"$runs", model.Row{"id": "old", "repositoryId": "repository", "workflowId": "workflow", "createdAt": old}},
		{"$runs", model.Row{"id": "middle", "repositoryId": "repository", "workflowId": "workflow", "createdAt": middle}},
		{"$runs", model.Row{"id": "current", "repositoryId": "repository", "workflowId": "workflow", "createdAt": current}},
	} {
		if err := writer.Append(t.Context(), record.source, record.row); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{"$audits", "$domains", "$evalObservations", "$experimentAssignments", "$friction", "$graderObservations", "$issues", "$skills", "$tools"} {
		for _, run := range []string{"old", "middle", "current"} {
			row := model.Row{"id": source + run, "runId": run}
			switch source {
			case "$evalObservations":
				row["evalId"] = "eval"
			case "$experimentAssignments":
				row["experimentId"] = "experiment"
			case "$graderObservations":
				row["graderId"] = "grader"
			}
			if err := writer.Append(t.Context(), source, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := writer.Publish(t.Context(), "daily"); err != nil {
		t.Fatal(err)
	}
	var partitions int
	if err := store.db.QueryRowContext(t.Context(), "SELECT count(DISTINCT tableoid) FROM runs WHERE namespace=$1", store.namespace).Scan(&partitions); err != nil || partitions != 3 {
		t.Fatalf("runs did not route to three daily partitions: %d %v", partitions, err)
	}
	var future bool
	if err := store.db.QueryRowContext(t.Context(), "SELECT to_regclass($1) IS NOT NULL", "runs_d"+now.AddDate(0, 0, futureRunDays).Format("20060102")).Scan(&future); err != nil || !future {
		t.Fatalf("future daily partition was not pre-created: %v %v", future, err)
	}
	result, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{{Name: "runs", From: "$runs", Select: []query.SelectedField{{Field: "id"}}}}, []string{"runs"})
	if err != nil || len(result["runs"].Rows) != 3 {
		t.Fatalf("parent query lost partitions: %v %v", result, err)
	}
	active, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	maintenance := make(chan error, 1)
	go func() { maintenance <- store.RunPartitionMaintenance(t.Context(), now, 30, 7) }()
	select {
	case err := <-maintenance:
		t.Fatalf("maintenance raced an active ingestion: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	active.Abort(t.Context())
	if err := <-maintenance; err != nil {
		t.Fatal(err)
	}
	state, err := store.State(t.Context())
	if err != nil || state.Revision <= 1 || state.Counts["$runs"] != 3 || state.Counts["$tools"] != 1 {
		t.Fatalf("retention did not publish updated counts and revision: %+v %v", state, err)
	}
	for _, table := range []string{"audits", "domains", "eval_observations", "experiment_assignments", "friction", "grader_observations", "issues", "skills", "tools"} {
		var count int
		if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table+" WHERE namespace=$1", store.namespace).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s coordinated retention count = %d, err = %v", table, count, err)
		}
	}
	var retainedRuns int
	if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM runs WHERE namespace=$1", store.namespace).Scan(&retainedRuns); err != nil || retainedRuns != 3 {
		t.Fatalf("run retention count = %d, err = %v", retainedRuns, err)
	}
	if err := store.RunPartitionMaintenance(t.Context(), now, 14, 7); err != nil {
		t.Fatal(err)
	}
	if state, err = store.State(t.Context()); err != nil || state.Counts["$runs"] != 2 || state.Counts["$tools"] != 1 {
		t.Fatalf("run retention did not remove the expired run: %+v %v", state, err)
	}
	if _, err := store.db.ExecContext(t.Context(), `INSERT INTO runs(namespace,ordinal,present_fields,id,repository_id,workflow_id,run_at)
		VALUES($1,2,repeat('0',73)::bit varying,'unroutable','repository','workflow','1900-01-01')`, store.namespace); err == nil || !strings.Contains(err.Error(), "no partition") {
		t.Fatalf("write unexpectedly created an out-of-window partition: %v", err)
	}
	var before, after int
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM pg_inherits WHERE inhparent='runs'::regclass`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, day := range []time.Time{now, now} {
		if err := store.RunPartitionMaintenance(t.Context(), day, 14, 7); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM pg_inherits WHERE inhparent='runs'::regclass`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after < before || after > before+1 {
		t.Fatalf("repeated daily maintenance duplicated partitions: before=%d after=%d", before, after)
	}
	if repeat, err := store.State(t.Context()); err != nil || repeat.Revision != state.Revision || repeat.Counts["$runs"] != 2 {
		t.Fatalf("repeated maintenance changed logical state: %+v %v", repeat, err)
	}
	missingDay := now.AddDate(0, 0, futureRunDays)
	missingName := "tools_d" + missingDay.Format("20060102")
	if _, err := store.db.ExecContext(t.Context(), "DROP TABLE "+missingName); err != nil {
		t.Fatal(err)
	}
	if err := store.RunPartitionMaintenance(t.Context(), now, 14, 7); err != nil {
		t.Fatal(err)
	}
	var restored bool
	if err := store.db.QueryRowContext(t.Context(), `SELECT EXISTS(
		SELECT 1 FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid
		WHERE i.inhparent='tools'::regclass AND c.relname=$1)`, missingName).Scan(&restored); err != nil || !restored {
		t.Fatalf("missing child partition was not restored: %v %v", restored, err)
	}
	later := now.AddDate(0, 0, 1)
	if err := store.RunPartitionMaintenance(t.Context(), later, 14, 7); err != nil {
		t.Fatal(err)
	}
	futureDay := later.AddDate(0, 0, futureRunDays)
	var ready bool
	if err := store.db.QueryRowContext(t.Context(), "SELECT to_regclass($1) IS NOT NULL", "runs_d"+futureDay.Format("20060102")).Scan(&ready); err != nil || !ready {
		t.Fatalf("daily rollover did not pre-create the next future partition: %v %v", ready, err)
	}
}

func TestDailyPartitionsRetainPartialCutoffDay(t *testing.T) {
	t.Setenv("CAO_POSTGRES_LINKED_RETENTION_DAYS", "14")
	store, _ := nativeTestStore(t)
	now := dayStart(time.Now()).Add(12 * time.Hour)
	cutoffDay := dayStart(now.AddDate(0, 0, -7))
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	writer.ingestedAt = now
	for _, record := range []struct {
		source string
		row    model.Row
	}{
		{"$repositories", model.Row{"id": "repository"}},
		{"$workflows", model.Row{"id": "workflow", "repositoryId": "repository"}},
	} {
		if err := writer.Append(t.Context(), record.source, record.row); err != nil {
			t.Fatal(err)
		}
	}
	for index, at := range []time.Time{cutoffDay.Add(-time.Microsecond), cutoffDay, cutoffDay.AddDate(0, 0, 1)} {
		id := fmt.Sprintf("run-%d", index)
		if err := writer.Append(t.Context(), "$runs", model.Row{
			"id": id, "repositoryId": "repository", "workflowId": "workflow", "createdAt": at.Format(time.RFC3339Nano),
		}); err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{"$tools", "$domains"} {
			if err := writer.Append(t.Context(), source, model.Row{"id": source + id, "runId": id}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := writer.Publish(t.Context(), "daily-boundary"); err != nil {
		t.Fatal(err)
	}
	var partitions int
	if err := store.db.QueryRowContext(t.Context(), "SELECT count(DISTINCT tableoid) FROM runs WHERE namespace=$1", store.namespace).Scan(&partitions); err != nil || partitions != 3 {
		t.Fatalf("adjacent UTC days did not route to separate partitions: %d %v", partitions, err)
	}
	for _, step := range []struct {
		at   time.Time
		want int
	}{
		{now, 2},
		{dayStart(now).AddDate(0, 0, 1).Add(-time.Nanosecond), 2},
		{dayStart(now).AddDate(0, 0, 1), 1},
	} {
		if err := store.RunPartitionMaintenance(t.Context(), step.at, 7, 7); err != nil {
			t.Fatal(err)
		}
		state, err := store.State(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{"$runs", "$tools", "$domains"} {
			if state.Counts[source] != step.want {
				t.Fatalf("%s at %s retained %d rows, want %d", source, step.at, state.Counts[source], step.want)
			}
		}
	}
}
