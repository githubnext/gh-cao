package dashboardquery

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const DatabaseName = "gh-aw-cao-dashboard-data"
const DatabaseVersion = 47

// ReadDailyRunCounts reads only the independently retained aggregate store.
// An absent store is unavailable, not an authoritative empty history.
func ReadDailyRunCounts(ctx context.Context, db SnapshotReader, maxRows int, maxBytes int64) (result []map[string]any, available bool, undated int64, err error) {
	return readDailyRunCounts(ctx, db, maxRows, maxBytes, false)
}

// ReadDailyRunCountRecords exposes typed aggregate records to the declarative
// database projection, without registering the store as retained Run detail.
func ReadDailyRunCountRecords(ctx context.Context, db SnapshotReader, maxRows int, maxBytes int64) (result []map[string]any, available bool, undated int64, err error) {
	return readDailyRunCounts(ctx, db, maxRows, maxBytes, true)
}

func readDailyRunCounts(ctx context.Context, db SnapshotReader, maxRows int, maxBytes int64, raw bool) (result []map[string]any, available bool, undated int64, err error) {
	stores, err := db.QueryContext(ctx, `SELECT name FROM __idb_stores WHERE database_name=? AND name IN ('dailyRunCounts','runCountContributions','dailyRunCountState')`, DatabaseName)
	if err != nil {
		return nil, false, 0, err
	}
	found := map[string]bool{}
	for stores.Next() {
		var name string
		if err := stores.Scan(&name); err != nil {
			_ = stores.Close()
			return nil, false, 0, err
		}
		found[name] = true
	}
	err = errors.Join(stores.Err(), stores.Close())
	if err != nil || !found["dailyRunCounts"] || !found["runCountContributions"] || !found["dailyRunCountState"] {
		return nil, false, 0, err
	}
	states, err := db.QueryContext(ctx, `SELECT json_extract(value,'$.status') FROM __idb_records
		WHERE database_name=? AND store_name='dailyRunCountState' AND record_key='"daily-run-counts"'`, DatabaseName)
	if err != nil {
		return nil, false, 0, err
	}
	var status sql.NullString
	if states.Next() {
		err = states.Scan(&status)
	}
	err = errors.Join(err, states.Err(), states.Close())
	if err != nil || status.String != "ready" {
		return nil, false, 0, err
	}
	// Both public aliases and counts are projected by SQLite, never reconstructed
	// from the detail Run collection.
	contentSQL := `json_object(
		'day',json_extract(value,'$.day'),'organization',json_extract(value,'$.owner'),
		'repository',json_extract(value,'$.repository'),'workflow',json_extract(value,'$.workflowPath'),
		'total',json_extract(value,'$.total'),'successful',json_extract(value,'$.successful'),
		'failed',json_extract(value,'$.failed'),'approval',json_extract(value,'$.approval'),
		'pending',json_extract(value,'$.pending'),'other',json_extract(value,'$.other'))`
	if raw {
		contentSQL = "value"
	}
	rows, err := db.QueryContext(ctx, `SELECT `+contentSQL+`,
		length(CAST(value AS BLOB))
		FROM __idb_records WHERE database_name=? AND store_name='dailyRunCounts' ORDER BY record_key`, DatabaseName)
	if err != nil {
		return nil, false, 0, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	result = []map[string]any{}
	var bytes int64
	for rows.Next() {
		var content string
		var size int64
		if err := rows.Scan(&content, &size); err != nil {
			return nil, false, 0, err
		}
		bytes += size
		if maxRows > 0 && len(result) >= maxRows || maxBytes > 0 && bytes > maxBytes {
			return nil, false, 0, errors.New("daily run counts exceed local query input budget")
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(content), &row); err != nil {
			return nil, false, 0, err
		}
		public := row
		if raw {
			public = make(map[string]any, len(row))
			for field, value := range row {
				public[field] = value
			}
			public["organization"], public["workflow"] = row["owner"], row["workflowPath"]
		}
		if err := validateDailyRunCounts(public); err != nil {
			return nil, false, 0, err
		}
		result = append(result, row)
	}

	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, false, 0, err
	}
	diagnostics, err := db.QueryContext(ctx, `SELECT count(*) FROM __idb_records WHERE database_name=?
		AND store_name='runCountContributions' AND json_extract(value,'$.run.startedAt') IS NULL`, DatabaseName)
	if err != nil {
		return nil, false, 0, err
	}
	defer func() { err = errors.Join(err, diagnostics.Close()) }()
	if diagnostics.Next() {
		err = diagnostics.Scan(&undated)
	}
	return result, true, undated, errors.Join(err, diagnostics.Err())
}

func validateDailyRunCounts(row map[string]any) error {
	for _, field := range []string{"day", "organization", "repository", "workflow"} {
		if value, ok := row[field].(string); !ok || value == "" {
			return errors.New("daily run counts contain an invalid identity")
		}
	}
	if _, err := time.Parse("2006-01-02", row["day"].(string)); err != nil {
		return errors.New("daily run counts contain an invalid UTC day")
	}
	var sum float64
	for _, field := range []string{"total", "successful", "failed", "approval", "pending", "other"} {
		value, ok := row[field].(float64)
		if !ok || value < 0 || math.Trunc(value) != value || value > 1<<53-1 {
			return errors.New("daily run counts must be nonnegative safe integers")
		}
		if field != "total" {
			sum += value
		}
	}
	if sum != row["total"] {
		return errors.New("daily run outcome counts do not sum to total")
	}
	return nil
}

var ErrSnapshotInputBudget = errors.New("snapshot input budget exceeded")

// SnapshotReader accepts a database or an already pinned read transaction.
type SnapshotReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// ReadCollection preserves canonical IndexedDB key order and omits storage-only
// fields. Positive limits bound allocation before materializing a collection.
func ReadCollection(ctx context.Context, db SnapshotReader, collection string, maxRows int, maxBytes int64) (result []map[string]any, err error) {
	return ReadCollectionFields(ctx, db, collection, nil, maxRows, maxBytes)
}

// ReadCollectionFields projects known-live fields inside SQLite. A nil field
// list preserves every public field, and limits still cover full stored records.
func ReadCollectionFields(ctx context.Context, db SnapshotReader, collection string, fields []string, maxRows int, maxBytes int64) (result []map[string]any, err error) {
	return readCollectionFields(ctx, db, collection, fields, maxRows, maxBytes, "", nil)
}

func readCollectionFields(ctx context.Context, db SnapshotReader, collection string, fields []string, maxRows int, maxBytes int64, where string, selection []any) (result []map[string]any, err error) {
	contentSQL := "value"
	args := []any{}
	if fields != nil {
		placeholders := make([]string, len(fields))
		for index, field := range fields {
			placeholders[index] = "?"
			args = append(args, field)
		}
		contentSQL = `CASE WHEN json_type(value)='object' THEN (
			SELECT json_group_object(key,json(CASE type
				WHEN 'true' THEN 'true' WHEN 'false' THEN 'false' WHEN 'null' THEN 'null'
				WHEN 'array' THEN value WHEN 'object' THEN value
				ELSE json_quote(value) END))
			FROM json_each(__idb_records.value) WHERE key IN (` + strings.Join(placeholders, ",") + `)
		) END`
	}
	args = append(args, DatabaseName, collection)
	args = append(args, selection...)
	rows, err := db.QueryContext(ctx, `SELECT record_key, `+contentSQL+`, length(CAST(value AS BLOB))
		FROM __idb_records WHERE database_name=? AND store_name=?`+where, args...)
	if err != nil {
		return nil, fmt.Errorf("read canonical %s: %w", collection, err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	type entry struct {
		key    any
		record map[string]any
	}
	entries := []entry{}
	var bytes int64
	for rows.Next() {
		var keyJSON, content string
		var storedBytes int64
		if err := rows.Scan(&keyJSON, &content, &storedBytes); err != nil {
			return nil, err
		}
		bytes += int64(len(keyJSON)) + storedBytes
		if maxRows > 0 && len(entries) >= maxRows || maxBytes > 0 && bytes > maxBytes {
			return nil, fmt.Errorf("canonical %s exceeds local query input budget: %w", collection, ErrSnapshotInputBudget)
		}
		var key any
		if err := json.Unmarshal([]byte(keyJSON), &key); err != nil || !ValidRecordKey(key) {
			return nil, fmt.Errorf("invalid canonical %s record key", collection)
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(content), &record); err != nil || record == nil {
			return nil, fmt.Errorf("invalid canonical %s record JSON", collection)
		}
		if collection != "transactions" {
			delete(record, "_queryKeys")
			delete(record, "_storage")
		}
		entries = append(entries, entry{key: key, record: record})
	}
	slices.SortFunc(entries, func(left, right entry) int { return CompareRecordKeys(left.key, right.key) })
	result = make([]map[string]any, len(entries))
	for index, entry := range entries {
		result[index] = entry.record
	}
	return result, rows.Err()
}

func ValidRecordKey(key any) bool {
	switch key := key.(type) {
	case float64, string:
		return true
	case []any:
		for _, part := range key {
			if !ValidRecordKey(part) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func CompareRecordKeys(left, right any) int {
	rank := func(key any) int {
		switch key.(type) {
		case float64:
			return 1
		case string:
			return 3
		default:
			return 4
		}
	}
	if difference := rank(left) - rank(right); difference != 0 {
		return difference
	}
	switch left := left.(type) {
	case float64:
		return cmp.Compare(left, right.(float64))
	case string:
		return compareRecordStrings(left, right.(string))
	default:
		return slices.CompareFunc(left.([]any), right.([]any), CompareRecordKeys)
	}

}

func compareRecordStrings(left, right string) int {
	for len(left) > 0 && len(right) > 0 {
		l, leftSize := utf8.DecodeRuneInString(left)
		r, rightSize := utf8.DecodeRuneInString(right)
		if l != r {
			// IndexedDB compares UTF-16 units, so supplementary characters sort
			// by their high surrogate before BMP characters above U+DFFF.
			leftUnit, rightUnit := l, r
			if l >= 0x10000 {
				leftUnit = 0xd800 + ((l - 0x10000) >> 10)
			}
			if r >= 0x10000 {
				rightUnit = 0xd800 + ((r - 0x10000) >> 10)
			}
			if order := cmp.Compare(leftUnit, rightUnit); order != 0 {
				return order
			}
			return cmp.Compare(l, r)
		}
		left, right = left[leftSize:], right[rightSize:]
	}
	return cmp.Compare(len(left), len(right))
}
