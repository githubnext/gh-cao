package collectnative

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type DiscoveredRun struct {
	Identity
	CreatedAt time.Time
}

// RunDiscovery pages workflow-specific history within an immutable inclusive
// window. It does not acquire artifacts or authorize its own scope.
type RunDiscovery interface {
	DiscoveryWorkflows(context.Context, EnrollmentScope, int) ([]int64, int, error)
	DiscoveryRuns(context.Context, EnrollmentScope, int64, time.Time, time.Time, int) ([]DiscoveredRun, int, error)
}

func validDiscoveryScope(scope EnrollmentScope) bool {
	return scope.Epoch > 0 && (Identity{Repository: scope.Repository, InstallationID: scope.InstallationID, RunID: 1, Attempt: 1}).Validate() == nil
}

func (g GitHub) DiscoveryWorkflows(ctx context.Context, scope EnrollmentScope, page int) ([]int64, int, error) {
	if !validDiscoveryScope(scope) || page <= 0 {
		return nil, 0, ErrIdentity
	}
	if page > g.Limits.defaults().Pages {
		return nil, 0, ErrCapacity
	}
	content, _, _, err := g.get(ctx, scope.InstallationID,
		fmt.Sprintf("repos/%s/actions/workflows?per_page=100&page=%d", scope.Repository, page))
	if err != nil {
		return nil, 0, err
	}
	var response struct {
		Workflows []struct {
			ID   int64  `json:"id"`
			Path string `json:"path"`
		} `json:"workflows"`
	}
	if err := decode(content, &response); err != nil {
		return nil, 0, errors.New("invalid native workflow discovery response")
	}
	if response.Workflows == nil {
		return nil, 0, errors.New("native workflow discovery omitted workflows")
	}
	if len(response.Workflows) > 100 {
		return nil, 0, ErrCapacity
	}
	var workflows []int64
	for _, workflow := range response.Workflows {
		if !strings.HasSuffix(workflow.Path, ".lock.yml") {
			continue
		}
		if workflow.ID <= 0 || !workflowPath.MatchString(workflow.Path) {
			return nil, 0, ErrIdentity
		}
		workflows = append(workflows, workflow.ID)
	}
	next := 0
	if len(response.Workflows) == 100 {
		next = page + 1
	}
	return workflows, next, nil
}

func (g GitHub) DiscoveryRuns(ctx context.Context, scope EnrollmentScope, workflow int64, after, before time.Time, page int) ([]DiscoveredRun, int, error) {
	if !validDiscoveryScope(scope) || workflow <= 0 || page <= 0 || after.IsZero() || !after.Before(before) {
		return nil, 0, ErrIdentity
	}
	if page > g.Limits.defaults().Pages {
		return nil, 0, ErrCapacity
	}
	window := after.UTC().Format(time.RFC3339) + ".." + before.UTC().Format(time.RFC3339)
	content, _, _, err := g.get(ctx, scope.InstallationID,
		fmt.Sprintf("repos/%s/actions/workflows/%d/runs?per_page=100&page=%d&created=%s",
			scope.Repository, workflow, page, url.QueryEscape(window)))
	if err != nil {
		return nil, 0, err
	}
	var response struct {
		Total *int64           `json:"total_count"`
		Runs  []map[string]any `json:"workflow_runs"`
	}
	if err := decode(content, &response); err != nil {
		return nil, 0, errors.New("invalid native historical run page")
	}
	// GitHub caps filtered run searches at 1,000 results. A capped search is
	// incomplete evidence, not a successfully completed historical cursor.
	if response.Total == nil || *response.Total < int64(len(response.Runs)) || response.Runs == nil {
		return nil, 0, errors.New("native historical discovery omitted its result population")
	}
	if *response.Total > 1000 || *response.Total > int64(g.Limits.defaults().Runs) || len(response.Runs) > 100 {
		return nil, 0, ErrCapacity
	}
	var runs []DiscoveredRun
	for _, fields := range response.Runs {
		run, err := githubRun(fields, scope.Repository, scope.InstallationID)
		if err != nil || scalar(fields["workflow_id"]) != fmt.Sprint(workflow) {
			return nil, 0, ErrIdentity
		}
		created, err := time.Parse(time.RFC3339Nano, scalar(fields["created_at"]))
		if err != nil || created.Before(after) || created.After(before) {
			return nil, 0, ErrIdentity
		}
		runs = append(runs, DiscoveredRun{Identity: run.Identity, CreatedAt: created})
	}
	next := 0
	if len(response.Runs) == 100 && int64(page)*100 < *response.Total {
		next = page + 1
	}
	if len(response.Runs) < 100 && int64(page-1)*100+int64(len(response.Runs)) < *response.Total {
		return nil, 0, errors.New("native historical discovery returned an incomplete page")
	}
	return runs, next, nil
}
