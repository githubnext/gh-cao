package dashboardquery

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"
	"time"
	"unicode/utf8"
)

const DatabaseName = "gh-aw-cao-dashboard-data"
const DatabaseVersion = 49

var ErrSnapshotInputBudget = errors.New("snapshot input budget exceeded")

func ReadDailyRunCounts(ctx context.Context, db CanonicalReader, maxRows int, maxBytes int64) ([]map[string]any, bool, int64, error) {
	result, err := db.ReadCanonical(ctx, CanonicalReadRequest{Kind: "daily-run-counts", MaxRows: maxRows, MaxBytes: maxBytes})
	return result.Rows, result.Available, result.Undated, err
}

func ReadDailyRunCountRecords(ctx context.Context, db CanonicalReader, maxRows int, maxBytes int64) ([]map[string]any, bool, int64, error) {
	result, err := db.ReadCanonical(ctx, CanonicalReadRequest{Kind: "daily-run-count-records", MaxRows: maxRows, MaxBytes: maxBytes})
	return result.Rows, result.Available, result.Undated, err
}

func ReadCollection(ctx context.Context, db CanonicalReader, collection string, maxRows int, maxBytes int64) ([]map[string]any, error) {
	return ReadCollectionFields(ctx, db, collection, nil, maxRows, maxBytes)
}

func ReadCollectionFields(ctx context.Context, db CanonicalReader, collection string, fields []string, maxRows int, maxBytes int64) ([]map[string]any, error) {
	result, err := db.ReadCanonical(ctx, CanonicalReadRequest{
		Kind: "collection", Collection: collection, Fields: fields, MaxRows: maxRows, MaxBytes: maxBytes,
	})
	return result.Rows, err
}

func ReadCollectionRecords(ctx context.Context, db CanonicalReader, collection string, recordIDs []string, fields []string, maxBytes int64) ([]map[string]any, error) {
	if len(recordIDs) == 0 {
		return []map[string]any{}, nil
	}
	result, err := db.ReadCanonical(ctx, CanonicalReadRequest{
		Kind: "collection", Collection: collection, Fields: fields, RecordIDs: recordIDs,
		MaxRows: len(recordIDs), MaxBytes: maxBytes,
	})
	return result.Rows, err
}

func ReadStatisticalRunCandidates(ctx context.Context, db CanonicalReader, watermark string, fields []string, maxRows int, maxBytes int64) ([]map[string]any, string, error) {
	result, err := db.ReadCanonical(ctx, CanonicalReadRequest{
		Kind: "statistical-runs", Collection: "runs", Fields: fields, Watermark: watermark,
		MaxRows: maxRows, MaxBytes: maxBytes,
	})
	return result.Rows, result.Watermark, err
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
