package simulator

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func companyFixture(t *testing.T, name string) Company {
	t.Helper()
	data, err := os.ReadFile("../../../tests/fixtures/company-stress/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	company, err := LoadCompany(data)
	if err != nil {
		t.Fatal(err)
	}
	return company
}

func TestCompanyPresets(t *testing.T) {
	for _, name := range []string{"small", "medium", "large"} {
		company := companyFixture(t, name)
		totals, err := company.Totals()
		if err != nil || totals.DailyPRs != company.Developers*10 || totals.DailyRuns != totals.DailyPRs*5 ||
			totals.AIC != float64(totals.Runs)*100 {
			t.Fatalf("%s: %+v %v", name, totals, err)
		}
		if name == "large" && totals != (CompanyTotals{100_000, 500_000, 50_000_000, 100_000, 500_000, 50_000_000}) {
			t.Fatalf("large preset changed: %+v", totals)
		}
	}
}

func TestCompanyValidation(t *testing.T) {
	company := companyFixture(t, "small")
	data, err := json.Marshal(company)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		"null", "{}", string(data) + " {}", strings.Replace(string(data), `"version":1`, `"version":2`, 1),
		strings.Replace(string(data), `"days":1`, `"days":1.5`, 1),
		strings.Replace(string(data), `"seed":42`, `"unknown":42`, 1),
		strings.Replace(string(data), `"developers":10`, `"developers":9223372036854775808`, 1),
	} {
		if _, err := LoadCompany([]byte(input)); err == nil {
			t.Fatalf("accepted invalid company %s", input)
		}
	}
	for _, change := range []func(*Company){
		func(c *Company) { c.Repositories = 0 },
		func(c *Company) { c.Developers = -1 },
		func(c *Company) { c.Days = 32 },
		func(c *Company) { c.AICPerRun = -1 },
		func(c *Company) { c.Distribution = "unknown" },
		func(c *Company) { c.Seed = -1 },
		func(c *Company) { c.Developers, c.PRsPerDeveloperPerDay, c.AWRunsPerPR = 1_000_000, 1000, 1000 },
	} {
		invalid := company
		change(&invalid)
		directory := t.TempDir()
		if _, err := invalid.WriteLake(t.Context(), directory, time.Now()); err == nil {
			t.Fatal("invalid configuration generated evidence")
		}
		files, err := os.ReadDir(directory)
		if err != nil || len(files) != 0 {
			t.Fatal("invalid configuration touched disk")
		}
	}
}

func TestCompanyEvidence(t *testing.T) {
	company := companyFixture(t, "small")
	company.Repositories, company.Developers, company.Days = 7, 3, 2
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, distribution := range []string{"uniform", "burst"} {
		company.Distribution = distribution
		var previous []byte
		for range 2 {
			directory := t.TempDir()
			totals, err := company.WriteLake(t.Context(), directory, start)
			if err != nil || totals.PRs != 60 || totals.Runs != 300 || totals.AIC != 30_000 {
				t.Fatalf("totals: %+v %v", totals, err)
			}
			data, err := os.ReadFile(filepath.Join(directory, "gh-aw-logs-runs/synthetic.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if previous != nil && string(previous) != string(data) {
				t.Fatal("company evidence is nondeterministic")
			}
			previous = data
			counts := map[string]int{}
			repos := map[string]bool{}
			runs := map[string]bool{}
			var previousRun map[string]any
			scanner := bufio.NewScanner(strings.NewReader(string(data)))
			for scanner.Scan() {
				var record struct {
					Collection string         `json:"collection"`
					Record     map[string]any `json:"record"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
					t.Fatal(err)
				}
				if record.Collection == "" {
					continue
				}
				counts[record.Collection]++
				if record.Collection == "runs" {
					id := record.Record["id"].(string)
					if runs[id] {
						t.Fatal("duplicate synthetic run identity")
					}
					runs[id] = true
					repos[record.Record["repositoryId"].(string)] = true
					if record.Record["aicTotal"] != float64(100) {
						t.Fatal("lost synthetic AIC")
					}
					if (counts["runs"]-1)%int(company.AWRunsPerPR) != 0 &&
						(record.Record["repositoryId"] != previousRun["repositoryId"] ||
							record.Record["createdAt"] != previousRun["createdAt"]) {
						t.Fatal("PR fan-out changed repository or timestamp")
					}
					previousRun = record.Record
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(counts, map[string]int{"repositories": 7, "workflows": 7, "runs": 300}) || len(repos) != 7 {
				t.Fatalf("evidence counts: %v repos=%d", counts, len(repos))
			}
		}
	}
}

func TestCompanyZeroVolumeAndCancellation(t *testing.T) {
	for _, zero := range []string{"prs", "runs", "aic"} {
		company := companyFixture(t, "small")
		switch zero {
		case "prs":
			company.PRsPerDeveloperPerDay = 0
		case "runs":
			company.AWRunsPerPR = 0
		case "aic":
			company.AICPerRun = 0
		}
		if totals, err := company.WriteLake(t.Context(), t.TempDir(), time.Now()); err != nil || totals.AIC != 0 {
			t.Fatalf("%s: %+v %v", zero, totals, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := companyFixture(t, "small").WriteLake(ctx, t.TempDir(), time.Now()); err != context.Canceled {
		t.Fatalf("canceled generation: %v", err)
	}
}
