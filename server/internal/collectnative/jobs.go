package collectnative

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// Job contains ephemeral Actions runner output, not agent-session metrics.
// In particular, runner stdout must never be added to usage artifact totals.
type Job struct {
	Identity
	ID     int64
	Fields map[string]any
	Log    []byte
	Gap    string
}

// JobProvider enumerates one exact attempt before retrieving its scoped logs.
// Implementations must not substitute a run-wide or latest-attempt log archive.
type JobProvider interface {
	Jobs(context.Context, Run, func(Job) error) error
}

func (g GitHub) Jobs(ctx context.Context, run Run, visit func(Job) error) error {
	if run.Validate() != nil {
		return ErrIdentity
	}
	limits := g.Limits.defaults()
	seen := map[int64]bool{}
	total, acquired := -1, 0
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if page > limits.Pages {
			return ErrCapacity
		}
		content, _, _, err := g.get(ctx, run.InstallationID, fmt.Sprintf(
			"repos/%s/actions/runs/%d/attempts/%d/jobs?per_page=100&page=%d", run.Repository, run.RunID, run.Attempt, page))
		if err != nil {
			return err
		}
		acquired += len(content)
		if acquired > limits.Bytes {
			return ErrCapacity
		}
		var result struct {
			Total *int             `json:"total_count"`
			Jobs  []map[string]any `json:"jobs"`
		}
		if err := decode(content, &result); err != nil || result.Total == nil || result.Jobs == nil ||
			*result.Total < 0 || len(result.Jobs) > 100 {
			return errors.New("invalid native attempt job listing")
		}
		if *result.Total > limits.Entries {
			return ErrCapacity
		}
		if total >= 0 && total != *result.Total {
			return errors.New("native attempt job listing changed during acquisition")
		}
		total = *result.Total
		// Validate the whole page before any job-scoped request.
		for _, fields := range result.Jobs {
			id, err := strconv.ParseInt(scalar(fields["id"]), 10, 64)
			if err != nil || id <= 0 || seen[id] || scalar(fields["run_id"]) != strconv.FormatInt(run.RunID, 10) ||
				scalar(fields["run_attempt"]) != strconv.Itoa(run.Attempt) {
				return ErrIdentity
			}
			seen[id] = true
		}
		if len(seen) > total {
			return ErrIdentity
		}
		for _, fields := range result.Jobs {
			id, _ := strconv.ParseInt(scalar(fields["id"]), 10, 64)
			job := Job{Identity: run.Identity, ID: id, Fields: fields}
			// Queued, skipped and not-yet-completed jobs do not have a final log.
			if fields["status"] != "completed" || fields["conclusion"] == "skipped" {
				job.Gap = "job-log-unavailable"
			} else {
				endpoint := fmt.Sprintf("repos/%s/actions/jobs/%d/logs", run.Repository, id)
				var status int
				var headers http.Header
				job.Log, headers, status, err = g.get(ctx, run.InstallationID, endpoint)
				switch {
				case ctx.Err() != nil:
					return ctx.Err()
				case status == http.StatusNotFound || status == http.StatusGone:
					job.Gap = "job-log-unavailable"
				case err != nil:
					return err
				case status == http.StatusFound:
					job.Log, err = g.signedDownload(ctx, headers)
					if err != nil {
						return err
					}
				}
				acquired += len(job.Log)
				if acquired > limits.Bytes {
					return ErrCapacity
				}
			}
			if err := visit(job); err != nil {
				return err
			}
		}
		if len(seen) == total {
			return nil
		}
		if len(result.Jobs) < 100 {
			return errors.New("incomplete native attempt job listing")
		}
	}
}
