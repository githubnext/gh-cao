package postgresx

import (
	"context"
	"database/sql"
	"errors"

	"github.com/githubnext/gh-cao/internal/dashboardquery"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
)

var statisticalLog = logger.New("cao:postgresx:sampling")

func selectStatisticalSnapshot(ctx context.Context, quality model.Metadata, recorded sql.NullTime, candidates []map[string]any, inputErr error) ([]map[string]any, map[string]any, error) {
	watermark, policy := dashboardquery.CanonicalStatisticalWatermark(candidates), "canonical-run-max"
	if recorded.Valid {
		watermark, policy = recorded.Time.UTC().Format("2006-01-02T15:04:05.000Z"), "recorded-run-as-of"
	}
	var selected []map[string]any
	var err error
	frame := map[string]any{"weight-valid": false}
	switch {
	case errors.Is(inputErr, dashboardquery.ErrStatisticalBudget):
		statisticalLog.Print("event=statistical-source status=unavailable reason=input-budget")
		quality["availability"] = "unavailable"
		quality["completeness"] = "unknown"
		quality["error"] = "statistical candidate frame exceeds input budget"
	case watermark == "":
		statisticalLog.Print("event=statistical-source status=unavailable reason=missing-clock")
		quality["availability"] = "unavailable"
		quality["completeness"] = "unknown"
		quality["error"] = "statistical sampling requires a recorded canonical watermark"
	default:
		selected, frame, err = dashboardquery.SampleStatisticalRuns(candidates, dashboardquery.StatisticalOptions{
			Context: ctx, Logf: statisticalLog.Printf,
			Watermark: watermark, Capacity: dashboardquery.StatisticalPostgresCapacity,
			Complete: quality["completeness"] == "complete" && quality["availability"] != "unavailable",
		})
		if err != nil {
			if !errors.Is(err, dashboardquery.ErrStatisticalBudget) {
				return nil, nil, err
			}
			frame = map[string]any{"weight-valid": false}
			quality["availability"] = "unavailable"
			quality["completeness"] = "unknown"
			quality["error"] = "statistical candidate frame exceeds input budget"
		}
	}
	frame["watermark-policy"] = policy
	quality["as-of"] = watermark
	return selected, frame, nil
}
