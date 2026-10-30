package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

const maxCompanyRecords int64 = 5_000_000

// Company models PR-driven AW volume, not agent execution or measured spend.
type Company struct {
	Version               int     `json:"version"`
	Name                  string  `json:"name"`
	Repositories          int     `json:"repositories"`
	Developers            int64   `json:"developers"`
	PRsPerDeveloperPerDay int64   `json:"prsPerDeveloperPerDay"`
	AWRunsPerPR           int64   `json:"awRunsPerPR"`
	Days                  int     `json:"days"`
	AICPerRun             float64 `json:"aicPerRun"`
	Seed                  int64   `json:"seed"`
	Distribution          string  `json:"distribution"`
}

type CompanyTotals struct {
	DailyPRs  int64   `json:"dailyPRs"`
	DailyRuns int64   `json:"dailyRuns"`
	DailyAIC  float64 `json:"dailyAIC"`
	PRs       int64   `json:"prs"`
	Runs      int64   `json:"runs"`
	AIC       float64 `json:"aic"`
}

func LoadCompany(data []byte) (Company, error) {
	var company Company
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&company); err != nil {
		return Company{}, fmt.Errorf("decode company scenario: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Company{}, errors.New("company scenario must contain exactly one JSON object")
	}
	_, err := company.Totals()
	return company, err
}

// Totals validates bounds before multiplication, allocation, or file creation.
func (c Company) Totals() (CompanyTotals, error) {
	if c.Version != 1 || strings.TrimSpace(c.Name) == "" {
		return CompanyTotals{}, errors.New("company scenario requires version 1 and a name")
	}
	if c.Repositories < 1 || c.Repositories > maxRepositories || c.Developers < 1 || c.Developers > 1_000_000 ||
		c.Days < 1 || c.Days > 31 || c.PRsPerDeveloperPerDay < 0 || c.PRsPerDeveloperPerDay > 1000 ||
		c.AWRunsPerPR < 0 || c.AWRunsPerPR > 1000 {
		return CompanyTotals{}, errors.New("company scenario requires 1-50000 repositories, 1-1000000 developers, 1-31 days, and 0-1000 PRs/developer/day and AW runs/PR")
	}
	if c.Seed < 0 || c.Distribution != "uniform" && c.Distribution != "burst" {
		return CompanyTotals{}, errors.New("company scenario requires a nonnegative seed and uniform or burst distribution")
	}
	if c.AICPerRun < 0 || math.IsNaN(c.AICPerRun) || math.IsInf(c.AICPerRun, 0) {
		return CompanyTotals{}, errors.New("company AIC/run must be finite and nonnegative")
	}
	dailyPRs := c.Developers * c.PRsPerDeveloperPerDay
	prs := dailyPRs * int64(c.Days)
	if prs > maxCompanyRecords || prs > 0 && c.AWRunsPerPR > maxCompanyRecords/prs {
		return CompanyTotals{}, errors.New("company scenario exceeds 5000000 PRs or AW records; reduce the explicit inputs")
	}
	dailyRuns := dailyPRs * c.AWRunsPerPR
	runs := dailyRuns * int64(c.Days)
	aic := float64(runs) * c.AICPerRun
	if math.IsInf(aic, 0) {
		return CompanyTotals{}, errors.New("company modeled AIC total overflows")
	}
	return CompanyTotals{DailyPRs: dailyPRs, DailyRuns: dailyRuns, DailyAIC: float64(dailyRuns) * c.AICPerRun,
		PRs: prs, Runs: runs, AIC: aic}, nil
}

// WriteLake emits completed-run-shaped evidence directly, without GitHub calls,
// workflow execution, completion callbacks, or a collection subprocess.
func (c Company) WriteLake(ctx context.Context, directory string, start time.Time) (CompanyTotals, error) {
	totals, err := c.Totals()
	if err != nil {
		return CompanyTotals{}, err
	}
	if start.IsZero() {
		return CompanyTotals{}, errors.New("company scenario requires a nonzero start time")
	}
	err = writeSyntheticLake(ctx, directory, int64(c.Repositories)*2+totals.Runs, func(record func(string, model.Row) error) error {
		for repo := range c.Repositories {
			name := repositoryName(repo)
			repository, workflow := companyParentIDs(repo)
			if err := record("repositories", model.Row{
				"id": repository, "owner": "simulator", "name": strings.TrimPrefix(name, "simulator/"),
				"visibility": "private", "observedAt": start.Format(time.RFC3339),
			}); err != nil {
				return err
			}
			if err := record("workflows", model.Row{
				"id": workflow, "repositoryId": repository, "path": ".github/workflows/synthetic.md",
				"name": "Synthetic PR workload", "state": "active", "role": "worker",
				"observedAt": start.Format(time.RFC3339),
			}); err != nil {
				return err
			}
		}
		for ordinal := int64(0); ordinal < totals.Runs; ordinal++ {
			pr := ordinal / c.AWRunsPerPR
			repo := int((pr + c.Seed%int64(c.Repositories)) % int64(c.Repositories))
			repository, workflow := companyParentIDs(repo)
			day, slot := pr/totals.DailyPRs, pr%totals.DailyPRs
			second := slot * 86400 / totals.DailyPRs
			if c.Distribution == "burst" && (slot+c.Seed%10)%10 == 0 {
				second = 10 * 3600
			}
			at := start.AddDate(0, 0, int(day)).Add(time.Duration(second) * time.Second).Format(time.RFC3339)
			if err := record("runs", model.Row{
				"id": fmt.Sprintf("run:%d", ordinal+1), "repositoryId": repository, "workflowId": workflow,
				"owner": "simulator", "repository": strings.TrimPrefix(repositoryName(repo), "simulator/"),
				"workflowPath": ".github/workflows/synthetic.md", "githubRunId": fmt.Sprint(ordinal + 1),
				"attempt": 1, "status": "completed", "conclusion": "success",
				"createdAt": at, "startedAt": at, "completedAt": at, "updatedAt": at, "observedAt": at,
				"aicTotal": c.AICPerRun,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return totals, err
}

func companyParentIDs(repository int) (string, string) {
	name := repositoryName(repository)
	return "repository:" + name, "workflow:" + name + ":synthetic"
}
