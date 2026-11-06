package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/internal/dashboardquery"
)

var dashboardDailyStores = []string{"dailyRunCounts", "runCountContributions", "dailyRunCountState"}
var dashboardDailyCounters = []string{"total", "successful", "failed", "approval", "pending", "other"}
var dashboardDailyFields = []string{
	"id", "repositoryId", "workflowId", "owner", "repository", "workflowPath",
	"startedAt", "status", "conclusion", "attempt", "updatedAt", "completedAt",
}

func dashboardDailyNumber(value any) float64 {
	if value == nil {
		return 0
	}
	return nativeNumber(value)
}

func dashboardDailyRecord(ctx context.Context, tx *sql.Tx, store string, id any) (map[string]any, error) {
	key, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	var content string
	err = tx.QueryRowContext(ctx, `SELECT value FROM __idb_records WHERE database_name=? AND store_name=? AND record_key=?`,
		dashboardquery.DatabaseName, store, string(key)).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(content), &record); err != nil {
		return nil, fmt.Errorf("invalid local daily count %s record: %w", store, err)
	}
	return record, nil
}

func putDashboardDailyRecord(ctx context.Context, tx *sql.Tx, store string, record map[string]any) error {
	key, err := json.Marshal(record["id"])
	if err != nil {
		return err
	}
	content, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO __idb_records VALUES(?,?,?,?)
		ON CONFLICT(database_name,store_name,record_key) DO UPDATE SET value=excluded.value`,
		dashboardquery.DatabaseName, store, string(key), string(content))
	return err
}

func dashboardDailyRevision(run map[string]any) float64 {
	return math.Max(0, math.Max(nativeTimestamp(run["updatedAt"]), nativeTimestamp(run["completedAt"])))
}

func compactDashboardDailyRun(run map[string]any) map[string]any {
	compact := map[string]any{}
	for _, field := range dashboardDailyFields {
		if value, exists := run[field]; exists {
			if field == "startedAt" || field == "updatedAt" || field == "completedAt" {
				if parsed, err := time.Parse(time.RFC3339Nano, nativeText(value)); err == nil {
					value = parsed.UTC().Format(time.RFC3339Nano)
				}
			}
			compact[field] = value
		}
	}
	return compact
}

func reconcileDashboardDailyRun(previous, incoming map[string]any) (map[string]any, bool) {
	next := compactDashboardDailyRun(incoming)
	if len(previous) == 0 {
		return next, false
	}
	before := compactDashboardDailyRun(previous)
	merged := compactDashboardDailyRun(before)
	for field, value := range next {
		merged[field] = value
	}
	attempt := func(run map[string]any) float64 {
		if run["attempt"] == nil {
			return 1
		}
		return nativeNumber(run["attempt"])
	}
	delta := attempt(merged) - attempt(before)
	oldTime, newTime := dashboardDailyRevision(before), dashboardDailyRevision(next)
	if delta < 0 || delta == 0 && newTime > 0 && oldTime > newTime {
		return before, false
	}
	if delta > 0 {
		for _, field := range []string{"startedAt", "status", "conclusion", "updatedAt", "completedAt"} {
			if _, exists := next[field]; !exists {
				delete(merged, field)
			}
		}
	}
	if delta == 0 && oldTime > 0 && newTime == 0 {
		for field, value := range before {
			merged[field] = value
		}
	}
	key := func(run map[string]any) string {
		values := make([]any, len(dashboardDailyFields))
		for index, field := range dashboardDailyFields {
			values[index] = run[field]
		}
		content, _ := json.Marshal(values)
		return string(content)
	}
	conflict := delta == 0 && oldTime > 0 && oldTime == newTime && key(before) != key(merged)
	if conflict {
		terminal := func(run map[string]any) bool {
			switch strings.ReplaceAll(nativeText(run["conclusion"]), "_", "-") {
			case "success", "failure", "startup-failure", "stale", "timed-out", "action-required":
				return true
			}
			return run["status"] == "completed"
		}
		if terminal(before) && !terminal(merged) || terminal(before) == terminal(merged) && key(before) > key(merged) {
			return before, true
		}
	}
	return merged, conflict
}

func dashboardDailyContribution(run map[string]any) map[string]any {
	start, err := time.Parse(time.RFC3339Nano, nativeText(run["startedAt"]))
	if err != nil {
		return nil
	}
	for _, field := range []string{"repositoryId", "workflowId", "owner", "repository", "workflowPath"} {
		if nativeText(run[field]) == "" {
			return nil
		}
	}
	day := start.UTC().Format("2006-01-02")
	key, _ := json.Marshal([]any{day, run["repositoryId"], run["workflowId"]})
	result := map[string]any{"id": string(key), "day": day}
	for _, field := range []string{"repositoryId", "workflowId", "owner", "repository", "workflowPath"} {
		result[field] = run[field]
	}
	category := "other"
	switch strings.ReplaceAll(nativeText(run["conclusion"]), "_", "-") {
	case "success":
		category = "successful"
	case "failure", "startup-failure", "stale", "timed-out":
		category = "failed"
	case "action-required":
		category = "approval"
	default:
		if status := nativeText(run["status"]); status != "" && status != "completed" {
			category = "pending"
		}
	}
	for _, field := range dashboardDailyCounters {
		result[field] = 0
		if field == "total" || field == category {
			result[field] = 1
		}
	}
	return result
}

func applyDashboardDailyDelta(ctx context.Context, tx *sql.Tx, contribution map[string]any, sign int) error {
	if contribution == nil {
		return nil
	}
	current, err := dashboardDailyRecord(ctx, tx, "dailyRunCounts", contribution["id"])
	if err != nil {
		return err
	}
	next := map[string]any{}
	for field, value := range contribution {
		next[field] = value
	}
	for _, field := range dashboardDailyCounters {
		if current != nil && current[field] == nil {
			return errors.New("local daily count is missing a required counter")
		}
		value := dashboardDailyNumber(current[field]) + float64(sign)*nativeNumber(contribution[field])
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || math.Trunc(value) != value || value > 1<<53-1 {
			return errors.New("local daily count delta is not a nonnegative safe integer")
		}
		next[field] = value
	}
	if nativeNumber(next["total"]) != nativeNumber(next["successful"])+nativeNumber(next["failed"])+
		nativeNumber(next["approval"])+nativeNumber(next["pending"])+nativeNumber(next["other"]) {
		return errors.New("local daily count classes do not sum to total")
	}
	if next["total"] == float64(0) {
		key, _ := json.Marshal(next["id"])
		_, err := tx.ExecContext(ctx, `DELETE FROM __idb_records WHERE database_name=? AND store_name='dailyRunCounts' AND record_key=?`,
			dashboardquery.DatabaseName, string(key))
		return err
	}
	return putDashboardDailyRecord(ctx, tx, "dailyRunCounts", next)
}

func reconcileDashboardDailyCounts(ctx context.Context, tx *sql.Tx, runs []map[string]any, now time.Time) error {
	for _, store := range dashboardDailyStores {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO __idb_stores VALUES(?,?,'"id"')`, dashboardquery.DatabaseName, store); err != nil {
			return err
		}
	}
	state, err := dashboardDailyRecord(ctx, tx, "dailyRunCountState", "daily-run-counts")
	if err != nil {
		return err
	}
	watermark := math.Max(float64(now.UnixMilli()), dashboardDailyNumber(state["retentionWatermark"]))
	if math.IsNaN(watermark) || math.IsInf(watermark, 0) || watermark < 0 {
		return errors.New("invalid local daily count retention watermark")
	}
	day := time.UnixMilli(int64(watermark)).UTC()
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	cutoff := day.AddDate(0, 0, -(int(day.Weekday())+6)%7-14)
	end := cutoff.AddDate(0, 0, 21).Format("2006-01-02")
	for _, statement := range []string{
		`DELETE FROM __idb_records WHERE database_name=? AND store_name='dailyRunCounts'
			AND (json_extract(value,'$.day')<? OR json_extract(value,'$.day')>=?)`,
		`DELETE FROM __idb_records WHERE database_name=? AND store_name='runCountContributions'
			AND (coalesce(json_extract(value,'$.retentionDay'),substr(json_extract(value,'$.run.startedAt'),1,10),
				strftime('%Y-%m-%d',json_extract(value,'$.observedAt')/1000,'unixepoch'))<?
			OR coalesce(json_extract(value,'$.retentionDay'),substr(json_extract(value,'$.run.startedAt'),1,10))>=?)`,
	} {
		if _, err := tx.ExecContext(ctx, statement, dashboardquery.DatabaseName, cutoff.Format("2006-01-02"), end); err != nil {
			return err
		}
	}
	for _, incoming := range runs {
		previous, err := dashboardDailyRecord(ctx, tx, "runCountContributions", incoming["id"])
		if err != nil {
			return err
		}
		run, conflict := reconcileDashboardDailyRun(nativeObject(previous["run"]), incoming)
		contribution := dashboardDailyContribution(run)
		expired := contribution != nil && (nativeText(contribution["day"]) < cutoff.Format("2006-01-02") || nativeText(contribution["day"]) >= end)
		if expired && previous == nil {
			continue
		}
		if previous == nil && contribution == nil && dashboardDailyRevision(run) > 0 &&
			dashboardDailyRevision(run) < float64(cutoff.UnixMilli()) {
			continue
		}
		if expired && previous["retired"] != true && dashboardDailyNumber(run["attempt"]) <= dashboardDailyNumber(nativeObject(previous["run"])["attempt"]) &&
			dashboardDailyRevision(run) <= dashboardDailyRevision(nativeObject(previous["run"])) {
			continue
		}
		if expired {
			contribution = nil
		}
		before := nativeObject(previous["contribution"])
		if len(before) == 0 {
			before = nil
		}
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(contribution)
		if string(beforeJSON) != string(afterJSON) {
			if err := applyDashboardDailyDelta(ctx, tx, before, -1); err != nil {
				return err
			}
			if err := applyDashboardDailyDelta(ctx, tx, contribution, 1); err != nil {
				return err
			}
		}
		observed := previous["observedAt"]
		if observed == nil {
			observed = watermark
			if revision := dashboardDailyRevision(run); revision > 0 {
				observed = math.Min(watermark, revision)
			}
		}
		receipt := map[string]any{"id": run["id"], "run": run, "contribution": contribution, "conflict": conflict, "observedAt": observed}
		if previous["conflict"] == true && dashboardDailyRevision(run) == dashboardDailyRevision(nativeObject(previous["run"])) &&
			dashboardDailyNumber(run["attempt"]) == dashboardDailyNumber(nativeObject(previous["run"])["attempt"]) {
			receipt["conflict"] = true
		}
		if expired {
			receipt["retired"] = true
		}
		if contribution == nil && previous != nil {
			retentionDay := nativeText(previous["retentionDay"])
			if retentionDay == "" {
				retentionDay = nativeText(nativeObject(previous["contribution"])["day"])
			}
			if retentionDay != "" {
				receipt["retentionDay"] = retentionDay
			}
		}
		if err := putDashboardDailyRecord(ctx, tx, "runCountContributions", receipt); err != nil {
			return err
		}
	}
	var excluded, conflicts int
	if err := tx.QueryRowContext(ctx, `SELECT
		coalesce(sum(json_extract(value,'$.contribution') IS NULL AND coalesce(json_extract(value,'$.retired'),0)=0),0),
		coalesce(sum(json_extract(value,'$.conflict')=1),0)
		FROM __idb_records WHERE database_name=? AND store_name='runCountContributions'`,
		dashboardquery.DatabaseName).Scan(&excluded, &conflicts); err != nil {
		return err
	}
	return putDashboardDailyRecord(ctx, tx, "dailyRunCountState", map[string]any{
		"id": "daily-run-counts", "status": "ready", "coverage": "bounded-gh-aw-observations",
		"excludedRuns": excluded, "conflictedRuns": conflicts,
		"retentionCutoff": cutoff.Format("2006-01-02"), "retentionWatermark": watermark,
	})
}
