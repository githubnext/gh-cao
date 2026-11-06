package cli

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/internal/dashboardquery"
)

func TestDashboardDailyCountsGoldenCases(t *testing.T) {
	content, err := os.ReadFile("../../dashboard/site/test/fixtures/daily-run-counts.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Defaults map[string]any `json:"runDefaults"`
		Cases    []struct {
			Name     string             `json:"name"`
			Writes   [][]map[string]any `json:"writes"`
			Expected []map[string]any   `json:"expectedRows"`
			State    string             `json:"stateMode"`
			Excluded int                `json:"excludedRuns"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range fixture.Cases {
		t.Run(scenario.Name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			_, err = db.Exec(`CREATE TABLE __idb_stores(database_name TEXT,name TEXT,key_path TEXT,PRIMARY KEY(database_name,name));
				CREATE TABLE __idb_records(database_name TEXT,store_name TEXT,record_key TEXT,value TEXT,PRIMARY KEY(database_name,store_name,record_key));`)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
			for _, write := range scenario.Writes {
				runs := []map[string]any{}
				for _, incoming := range write {
					run := map[string]any{}
					for key, value := range fixture.Defaults {
						run[key] = value
					}
					for key, value := range incoming {
						run[key] = value
					}
					runs = append(runs, run)
				}
				tx, err := db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				if err := reconcileDashboardDailyCounts(t.Context(), tx, runs, now); err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.State == "missing" {
				_, err = db.Exec(`DELETE FROM __idb_records WHERE store_name='dailyRunCountState'`)
			} else if scenario.State == "null" {
				_, err = db.Exec(`UPDATE __idb_records SET value=json_set(value,'$.status',null) WHERE store_name='dailyRunCountState'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			rows, available, _, err := dashboardquery.ReadDailyRunCounts(t.Context(), db, 100, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.State != "" {
				if available || len(rows) != 0 {
					t.Fatalf("missing/incomplete state became available: %v", rows)
				}
				return
			}
			if !available || !reflect.DeepEqual(rows, scenario.Expected) {
				t.Fatalf("daily golden mismatch:\n%v\nwant %v", rows, scenario.Expected)
			}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			state, err := dashboardDailyRecord(t.Context(), tx, "dailyRunCountState", "daily-run-counts")
			_ = tx.Rollback()
			if err != nil || nativeNumber(state["excludedRuns"]) != float64(scenario.Excluded) {
				t.Fatalf("lost excluded evidence: %v %v", state, err)
			}
		})
	}
}

func TestDashboardDailyCountsRetentionAndAtomicPublication(t *testing.T) {
	cache := t.TempDir()
	now := time.Now().UTC()
	run := map[string]any{"id": "run:1", "repositoryId": "repo", "workflowId": "workflow",
		"owner": "octo", "repository": "api", "workflowPath": ".github/workflows/agent.md",
		"startedAt": now.Add(-time.Hour).Format(time.RFC3339Nano), "updatedAt": now.Format(time.RFC3339Nano),
		"status": "in_progress", "attempt": 1}
	batch := map[string][]map[string]any{"runs": {run}}
	if err := publishDashboardSnapshot(t.Context(), cache, batch); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cache, "gh-aw-logs.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := publishDashboardSnapshot(t.Context(), cache, map[string][]map[string]any{"runs": {}}); err != nil {
		t.Fatal(err)
	}
	counts, available, _, err := dashboardquery.ReadDailyRunCounts(t.Context(), db, 100, 1<<20)
	if err != nil || !available || len(counts) != 1 || counts[0]["pending"] != float64(1) {
		t.Fatalf("detail expiry deleted independent daily evidence: %v %v", counts, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	correction := compactDashboardDailyRun(run)
	correction["conclusion"], correction["status"] = "success", "completed"
	correction["updatedAt"] = now.Add(time.Minute).Format(time.RFC3339Nano)
	if err := reconcileDashboardDailyCounts(t.Context(), tx, []map[string]any{correction}, now); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	counts, _, _, err = dashboardquery.ReadDailyRunCounts(t.Context(), db, 100, 1<<20)
	if err != nil || counts[0]["pending"] != float64(1) {
		t.Fatalf("rollback did not preserve counts: %v %v", counts, err)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	future := now.AddDate(0, 0, 28)
	if err := reconcileDashboardDailyCounts(t.Context(), tx, []map[string]any{run}, future); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := reconcileDashboardDailyCounts(t.Context(), tx, []map[string]any{run}, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	counts, _, _, err = dashboardquery.ReadDailyRunCounts(t.Context(), db, 100, 1<<20)
	if err != nil || len(counts) != 0 {
		t.Fatalf("clock regression resurrected expired counts: %v %v", counts, err)
	}
}

func TestDashboardDailyCountsCanonicalQueries(t *testing.T) {
	cache := t.TempDir()
	now := time.Now().UTC().Add(-time.Second)
	metadata, err := collectDashboard(t.Context(), []string{"octo/api"}, cache,
		dashboardOptions{window: 48 * time.Hour, count: 200}, dashboardTestGH(t, now), dashboardTestLogs(t))
	if err != nil {
		t.Fatal(err)
	}
	db, err := openNativeSnapshot(filepath.Join(cache, "gh-aw-logs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	catalog, err := loadNativeCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"repository-run-inventory", "runs-daily-conclusions"} {
		result, err := catalog.runQuery(t.Context(), db, name, nil, 100)
		if err != nil {
			t.Fatal(err)
		}
		quality := nativeObject(result["metadata"])
		if quality["availability"] != "available" || quality["completeness"] != "partial" ||
			quality["aggregate-coverage"] != "bounded-gh-aw-observations" || quality["as-of"] != metadata["windowEnd"] {
			t.Fatalf("%s lost daily observation provenance: %v", name, result)
		}
		rows := result["rows"].([]map[string]any)
		var total float64
		for _, row := range rows {
			total += nativeNumber(row["runs"])
		}
		if total != 1 {
			t.Fatalf("%s lost or duplicated logical Runs: %v", name, rows)
		}
	}
}

func TestDashboardDailyCountsCachedStartPreservesAttemptScope(t *testing.T) {
	run := map[string]any{"databaseId": 1, "workflowDatabaseId": 42, "attempt": 1,
		"startedAt": "2026-11-06T00:00:00Z", "status": "in_progress"}
	fresh := map[string]any{"databaseId": 1, "attempt": 1, "status": "completed", "conclusion": "success"}
	merged := mergeDashboardRunListing(run, fresh)
	if merged["startedAt"] != run["startedAt"] || merged["conclusion"] != "success" {
		t.Fatalf("sparse same-attempt listing erased observed start: %v", merged)
	}
	fresh["attempt"], fresh["status"] = 2, "queued"
	delete(fresh, "conclusion")
	merged = mergeDashboardRunListing(run, fresh)
	if _, exists := merged["startedAt"]; exists {
		t.Fatal("rerun inherited a previous attempt's UTC start day")
	}
}

func TestDashboardDailyCountsReplayGuardsAndCorruption(t *testing.T) {
	now := time.Now().UTC()
	run := map[string]any{"id": "run:1", "repositoryId": "repo", "workflowId": "workflow",
		"owner": "octo", "repository": "api", "workflowPath": ".github/workflows/agent.md",
		"startedAt": now.Add(-time.Hour).Format(time.RFC3339Nano), "updatedAt": now.Format(time.RFC3339Nano),
		"status": "completed", "conclusion": "success", "attempt": 1}
	for _, scenario := range []string{"retired-replay", "ancient-undated", "persistent-conflict", "missing-counter", "invalid-total"} {
		t.Run(scenario, func(t *testing.T) {
			cache := t.TempDir()
			if err := publishDashboardSnapshot(t.Context(), cache, map[string][]map[string]any{"runs": {run}}); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", filepath.Join(cache, "gh-aw-logs.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			write := func(records ...map[string]any) error {
				tx, err := db.Begin()
				if err != nil {
					return err
				}
				defer func() { _ = tx.Rollback() }()
				if err := reconcileDashboardDailyCounts(t.Context(), tx, records, now); err != nil {
					return err
				}
				return tx.Commit()
			}
			correction := compactDashboardDailyRun(run)
			switch scenario {
			case "retired-replay":
				correction["startedAt"] = now.AddDate(0, 0, -28).Format(time.RFC3339Nano)
				correction["updatedAt"] = now.Add(time.Minute).Format(time.RFC3339Nano)
				if err := write(correction); err != nil {
					t.Fatal(err)
				}
				if err := write(run); err != nil {
					t.Fatal(err)
				}
				counts, _, _, err := dashboardquery.ReadDailyRunCounts(t.Context(), db, 100, 1<<20)
				if err != nil || len(counts) != 0 {
					t.Fatalf("stale replay resurrected a retired contribution: %v %v", counts, err)
				}
				correction["startedAt"] = run["startedAt"]
				correction["updatedAt"] = now.Add(2 * time.Minute).Format(time.RFC3339Nano)
				if err := write(correction); err != nil {
					t.Fatal(err)
				}
				counts, _, _, err = dashboardquery.ReadDailyRunCounts(t.Context(), db, 100, 1<<20)
				if err != nil || len(counts) != 1 || counts[0]["total"] != float64(1) {
					t.Fatalf("authoritative correction lost surviving replay guard: %v %v", counts, err)
				}
			case "ancient-undated":
				correction["id"] = "undated"
				delete(correction, "startedAt")
				correction["updatedAt"] = now.AddDate(0, 0, -28).Format(time.RFC3339Nano)
				for range 2 {
					if err := write(correction); err != nil {
						t.Fatal(err)
					}
				}
				var receipts int
				if err := db.QueryRow(`SELECT count(*) FROM __idb_records WHERE store_name='runCountContributions'`).Scan(&receipts); err != nil || receipts != 1 {
					t.Fatalf("ancient undated evidence acquired a new receipt lifetime: %d %v", receipts, err)
				}
			case "persistent-conflict":
				correction["conclusion"] = "failure"
				if err := write(correction); err != nil {
					t.Fatal(err)
				}
				if err := write(run); err != nil {
					t.Fatal(err)
				}
				var conflicts int
				if err := db.QueryRow(`SELECT json_extract(value,'$.conflictedRuns') FROM __idb_records WHERE store_name='dailyRunCountState'`).Scan(&conflicts); err != nil || conflicts != 1 {
					t.Fatalf("exact replay erased equal-revision conflict: %d %v", conflicts, err)
				}
				correction["updatedAt"] = now.Add(time.Minute).Format(time.RFC3339Nano)
				if err := write(correction); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow(`SELECT json_extract(value,'$.conflictedRuns') FROM __idb_records WHERE store_name='dailyRunCountState'`).Scan(&conflicts); err != nil || conflicts != 0 {
					t.Fatalf("new revision retained stale conflict: %d %v", conflicts, err)
				}
			default:
				statement := `UPDATE __idb_records SET value=json_remove(value,'$.successful') WHERE store_name='dailyRunCounts'`
				if scenario == "invalid-total" {
					statement = `UPDATE __idb_records SET value=json_set(value,'$.total',2) WHERE store_name='dailyRunCounts'`
				}
				if _, err := db.Exec(statement); err != nil {
					t.Fatal(err)
				}
				correction["conclusion"] = "failure"
				correction["updatedAt"] = now.Add(time.Minute).Format(time.RFC3339Nano)
				if err := write(correction); err == nil {
					t.Fatal("corrupt aggregate was silently reconciled")
				}
				var conclusion string
				if err := db.QueryRow(`SELECT json_extract(value,'$.run.conclusion') FROM __idb_records WHERE store_name='runCountContributions'`).Scan(&conclusion); err != nil || conclusion != "success" {
					t.Fatalf("failed reconciliation changed the receipt: %s %v", conclusion, err)
				}
			}
		})
	}
}
