package server

import (
	"context"
	"net/http"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/model"
)

// gitHubQuotaUsageSourceName is the admin-authorized runtime source that
// exposes retained GitHub API quota usage to Dashboard Language queries.
const gitHubQuotaUsageSourceName = "github-quota-usage"

// gitHubQuotaAggregateBucket labels the limit-weighted usage of every bucket.
const gitHubQuotaAggregateBucket = "all buckets"

// gitHubQuotaUsage reports retained GitHub API quota usage per bucket and in
// aggregate. It exposes bucket identity and counts only, never credentials.
func (a *App) gitHubQuotaUsage(response http.ResponseWriter, request *http.Request) {
	if !a.adminAuthorized(request) {
		a.writeAdminDenied(response, request, "administrative authorization is required")
		return
	}
	if a.quota == nil {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "github quota usage is unavailable")
		return
	}
	report, err := a.quota.Usage(request.Context())
	if err != nil {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "github quota usage is unavailable")
		return
	}
	serverLog.PrintfContext(request.Context(), "github quota usage served points=%d slots=%d", len(report.Buckets), len(report.Aggregate))
	writeJSON(response, http.StatusOK, report)
}

func (a *App) gitHubQuotaUsageSource(ctx context.Context, allowed bool) (model.Source, error) {
	if !allowed || a.quota == nil {
		return unavailableSource(gitHubQuotaUsageSourceName), nil
	}
	report, err := a.quota.Usage(ctx)
	if err != nil {
		return model.Source{}, err
	}
	return gitHubQuotaUsageSource(report), nil
}

// gitHubQuotaUsageSource renders one row per bucket and slot plus one
// aggregate row per slot, so a chart can plot each bucket and the
// limit-weighted total as separate series.
func gitHubQuotaUsageSource(report githubquota.UsageReport) model.Source {
	rows := make([]model.Row, 0, len(report.Buckets)+len(report.Aggregate))
	for _, point := range report.Aggregate {
		rows = append(rows, model.Row{
			"observed-at":   point.Time.UTC().Format(time.RFC3339),
			"scope":         "aggregate",
			"bucket":        gitHubQuotaAggregateBucket,
			"app":           nil,
			"installation":  nil,
			"resource":      nil,
			"buckets":       point.Buckets,
			"limit":         point.Limit,
			"used":          point.Used,
			"reserved":      point.Reserved,
			"usage-percent": point.UsagePercent,
		})
	}
	for _, point := range report.Buckets {
		rows = append(rows, model.Row{
			"observed-at":   point.Time.UTC().Format(time.RFC3339),
			"scope":         "bucket",
			"bucket":        point.Bucket.String(),
			"app":           point.Bucket.App,
			"installation":  point.Bucket.Installation,
			"resource":      point.Bucket.Resource,
			"buckets":       1,
			"limit":         point.Limit,
			"used":          point.Used,
			"reserved":      point.Reserved,
			"usage-percent": point.UsagePercent,
		})
	}
	return model.Source{
		Source: gitHubQuotaUsageSourceName,
		Rows:   rows,
		Metadata: model.Metadata{
			"source-id":    gitHubQuotaUsageSourceName,
			"availability": "available",
			"completeness": "complete",
			"freshness":    "current",
		},
	}
}
