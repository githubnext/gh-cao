package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/githubnext/gh-cao/internal/dashboardquery"
)

func prepareDashboardCache(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("dashboard cache must be a real directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		info, err := os.Lstat(filepath.Join(directory, "gh-aw-logs.sqlite"+suffix))
		if err == nil && !info.Mode().IsRegular() {
			return errors.New("dashboard database and sidecars must be regular files")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func readDashboardStatus(ctx context.Context, cache string, repositories []string) (metadata map[string]any, err error) {
	filename := filepath.Join(cache, "gh-aw-logs.sqlite")
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, fmt.Errorf("no local dashboard snapshot; run gh cao dashboard: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("dashboard database must be a regular file")
	}
	db, err := openNativeSnapshot(filename)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	var content string
	if err := db.QueryRowContext(ctx, `SELECT value FROM __idb_records WHERE database_name=? AND store_name='transactions' AND record_key='"local-dashboard"'`,
		dashboardquery.DatabaseName).Scan(&content); err != nil {
		return nil, errors.New("cache is not an on-demand dashboard database; select a separate --cache-dir")
	}
	if err := json.Unmarshal([]byte(content), &metadata); err != nil {
		return nil, errors.New("local dashboard metadata is malformed")
	}
	scope := []string{}
	for _, value := range nativeArray(metadata["repositories"]) {
		repository, ok := value.(string)
		if !ok {
			return nil, errors.New("local dashboard metadata contains an invalid repository scope")
		}
		scope = append(scope, repository)
	}
	if !slices.Equal(scope, repositories) {
		return nil, errors.New("dashboard cache repository scope differs from the requested scope; select a separate --cache-dir")
	}
	return metadata, nil
}

func initializeDashboardSnapshot(ctx context.Context, cache string, repositories []string, options dashboardOptions) error {
	if err := prepareDashboardCache(cache); err != nil {
		return err
	}
	lock := filepath.Join(cache, "collection.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		return errors.New("dashboard collection is already locked")
	}
	defer func() { _ = os.Remove(lock) }()
	if _, err := os.Stat(filepath.Join(cache, "gh-aw-logs.sqlite")); err == nil {
		_, err = readDashboardStatus(ctx, cache, repositories)
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	now := time.Now().UTC()
	metadata := dashboardObservationMetadata(repositories, options, now, nil)
	metadata["coverage"] = "Initial collection pending; upstream evidence is not yet available."
	return publishDashboardSnapshot(ctx, cache, map[string][]map[string]any{"transactions": {metadata}})
}

func publishDashboardSnapshot(ctx context.Context, cache string, batch map[string][]map[string]any) (err error) {
	return writeDashboardSnapshot(ctx, cache, batch, false)
}

func writeDashboardSnapshot(ctx context.Context, cache string, batch map[string][]map[string]any, keepHistory bool) (err error) {
	filename := filepath.Join(cache, "gh-aw-logs.sqlite")
	created := false
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		created = true
		if err := file.Close(); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	defer func() {
		if err != nil && created {
			err = errors.Join(err, os.Remove(filename))
		}
	}()
	location := &url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(filename), "/")}
	parameters := location.Query()
	parameters.Set("_pragma", "busy_timeout(5000)")
	location.RawQuery = parameters.Encode()
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer func() { err = errors.Join(err, db.Close()) }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if created {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE __idb_databases(name TEXT PRIMARY KEY, version INTEGER NOT NULL);
			CREATE TABLE __idb_stores(database_name TEXT NOT NULL, name TEXT NOT NULL, key_path TEXT NOT NULL, PRIMARY KEY(database_name,name));
			CREATE TABLE __idb_indexes(database_name TEXT NOT NULL, store_name TEXT NOT NULL, name TEXT NOT NULL, key_path TEXT NOT NULL, PRIMARY KEY(database_name,store_name,name));
			CREATE TABLE __idb_records(database_name TEXT NOT NULL, store_name TEXT NOT NULL, record_key TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY(database_name,store_name,record_key));
		`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO __idb_databases VALUES(?,?)`, dashboardquery.DatabaseName, dashboardquery.DatabaseVersion); err != nil {
			return err
		}
	} else {
		var version int
		if err := tx.QueryRowContext(ctx, `SELECT version FROM __idb_databases WHERE name=?`, dashboardquery.DatabaseName).Scan(&version); err != nil {
			return err
		}
		if version != dashboardquery.DatabaseVersion {
			return errors.New("local dashboard schema changed; select a new --cache-dir (existing database is not migrated)")
		}
	}
	if err := reconcileDashboardDailyCounts(ctx, tx, batch["runs"], time.Now().UTC()); err != nil {
		return err
	}
	if keepHistory {
		if err := retainDashboardHistory(ctx, tx, batch); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM __idb_records WHERE database_name=?
		AND store_name NOT IN ('dailyRunCounts','runCountContributions','dailyRunCountState')`, dashboardquery.DatabaseName); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM __idb_stores WHERE database_name=?
		AND name NOT IN ('dailyRunCounts','runCountContributions','dailyRunCountState')`, dashboardquery.DatabaseName); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM __idb_indexes WHERE database_name=?
		AND store_name NOT IN ('dailyRunCounts','runCountContributions','dailyRunCountState')`, dashboardquery.DatabaseName); err != nil {
			return err
		}
	}
	for store, records := range batch {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO __idb_stores VALUES(?,?,'"id"')`, dashboardquery.DatabaseName, store); err != nil {
			return err
		}
		for _, record := range records {
			key, err := json.Marshal(record["id"])
			if err != nil {
				return err
			}
			content, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO __idb_records VALUES(?,?,?,?)
				ON CONFLICT(database_name,store_name,record_key) DO UPDATE SET value=excluded.value`,
				dashboardquery.DatabaseName, store, string(key), string(content)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func retainDashboardHistory(ctx context.Context, tx *sql.Tx, batch map[string][]map[string]any) error {
	var cutoff string
	for _, row := range batch["transactions"] {
		if row["id"] == "local-dashboard" {
			cutoff = nativeText(row["windowStart"])
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, cutoff); err != nil {
		return errors.New("history publication requires a valid detail retention cutoff")
	}
	rejected := map[string]bool{}
	accepted := make([]map[string]any, 0, len(batch["runs"]))
	for _, incoming := range batch["runs"] {
		id := nativeText(incoming["id"])
		previous, err := dashboardDailyRecord(ctx, tx, "runs", id)
		if err != nil {
			return err
		}
		before, after := nativeNumber(previous["attempt"]), nativeNumber(incoming["attempt"])
		if previous != nil && (after < before ||
			after == before && dashboardDailyRevision(incoming) < dashboardDailyRevision(previous)) {
			rejected[id] = true
			continue
		}
		if previous != nil && before == after {
			for key, value := range previous {
				if _, present := incoming[key]; !present {
					incoming[key] = value
				}
			}
		} else if previous != nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM __idb_records WHERE database_name=?
				AND store_name NOT IN ('dailyRunCounts','runCountContributions','dailyRunCountState')
				AND json_extract(value,'$.runId')=?`, dashboardquery.DatabaseName, id); err != nil {
				return err
			}
		}
		accepted = append(accepted, incoming)
	}
	batch["runs"] = accepted
	if len(rejected) > 0 {
		for store, rows := range batch {
			batch[store] = slices.DeleteFunc(rows, func(row map[string]any) bool {
				return rejected[nativeText(row["runId"])]
			})
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM __idb_records WHERE database_name=? AND store_name='runs'
		AND julianday(json_extract(value,'$.createdAt')) < julianday(?)`, dashboardquery.DatabaseName, cutoff); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM __idb_records WHERE database_name=?
		AND store_name NOT IN ('dailyRunCounts','runCountContributions','dailyRunCountState')
		AND json_extract(value,'$.runId') IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM __idb_records runs WHERE runs.database_name=?
			AND runs.store_name='runs' AND json_extract(runs.value,'$.id')=json_extract(__idb_records.value,'$.runId'))`,
		dashboardquery.DatabaseName, dashboardquery.DatabaseName)
	return err
}

func readDashboardIngestion(ctx context.Context, cache string) (state dashboardIngestion, err error) {
	state.Cursors = map[string]dashboardHistoryCursor{}
	db, err := openNativeSnapshot(filepath.Join(cache, "gh-aw-logs.sqlite"))
	if err != nil {
		return state, err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	var content string
	err = db.QueryRowContext(ctx, `SELECT value FROM __idb_records WHERE database_name=?
		AND store_name='transactions' AND record_key='"local-dashboard-ingestion"'`, dashboardquery.DatabaseName).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal([]byte(content), &state); err != nil || state.Cursors == nil {
		return state, errors.New("local dashboard ingestion checkpoint is malformed")
	}
	if state.ID != "local-dashboard-ingestion" || state.Kind != "local-dashboard-ingestion" || state.NextRepo < 0 {
		return state, errors.New("local dashboard ingestion checkpoint has invalid identity or repository cursor")
	}
	for _, cursor := range state.Cursors {
		if cursor.Page < 1 || cursor.End.IsZero() || !cursor.Start.IsZero() && cursor.Start.After(cursor.End) {
			return state, errors.New("local dashboard history cursor is invalid")
		}
	}
	return state, nil
}

func dashboardIngestionRecord(state dashboardIngestion) (map[string]any, error) {
	content, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var record map[string]any
	err = json.Unmarshal(content, &record)
	return record, err
}

func writeDashboardIngestion(ctx context.Context, cache string, state dashboardIngestion) (err error) {
	content, err := json.Marshal(state)
	if err != nil {
		return err
	}
	location := &url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(cache, "gh-aw-logs.sqlite"))}
	parameters := location.Query()
	parameters.Set("mode", "rw")
	parameters.Set("_pragma", "busy_timeout(5000)")
	location.RawQuery = parameters.Encode()
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	_, err = db.ExecContext(ctx, `INSERT INTO __idb_records VALUES(?, 'transactions', '"local-dashboard-ingestion"', ?)
		ON CONFLICT(database_name,store_name,record_key) DO UPDATE SET value=excluded.value`,
		dashboardquery.DatabaseName, string(content))
	return err
}
