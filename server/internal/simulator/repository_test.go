package simulator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRepositoryScenarioAPIAndDeliveries(t *testing.T) {
	for _, history := range []bool{false, true} {
		for _, action := range []string{"created", "archived", "unarchived", "renamed", "deleted", "removed", "renamed-deleted"} {
			t.Run(fmt.Sprintf("%s/history=%t", action, history), func(t *testing.T) {
				scenario := Scenario{Name: "repositories", Repositories: 101}
				switch action {
				case "created":
					scenario.CreateRepositories = true
				case "archived":
					scenario.ArchiveRepositories = true
				case "unarchived":
					scenario.ArchiveRepositories, scenario.UnarchiveRepositories = true, true
				case "renamed":
					scenario.RenameRepositories = true
				case "deleted":
					scenario.DeleteRepositories = true
				case "removed":
					scenario.RemoveRepositories = true
				case "renamed-deleted":
					scenario.RenameRepositories, scenario.DeleteRepositories = true, true
				}
				if history {
					scenario.History = &History{Days: 1, RunsPerDay: 1, AsOf: "2026-10-24T00:00:00Z"}
				}
				api, err := NewAPIHandler(scenario, 1)
				if err != nil {
					t.Fatal(err)
				}
				get := func(path string, target any) int {
					t.Helper()
					request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
					response := httptest.NewRecorder()
					api.ServeHTTP(response, request)
					if target != nil {
						if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
							t.Fatal(err)
						}
					}
					return response.Code
				}
				var page struct {
					Total int `json:"total_count"`
					Repos []struct {
						ID       int64  `json:"id"`
						FullName string `json:"full_name"`
						Archived bool   `json:"archived"`
					} `json:"repositories"`
				}
				if status := get("/installation/repositories?per_page=100&page=1", &page); status != 200 {
					t.Fatalf("enumeration status = %d", status)
				}
				total := 101
				if scenario.DeleteRepositories || scenario.RemoveRepositories {
					total--
				}
				if page.Total != total || len(page.Repos) != 100 {
					t.Fatalf("incorrect repository enumeration: %+v", page)
				}
				first := page.Repos[0]
				if total == 100 {
					if first.ID != 2 {
						t.Fatalf("removed first repository remains in discovery: %+v", first)
					}
				} else {
					name := repositoryName(0)
					if scenario.RenameRepositories {
						name += "-renamed"
					}
					if first.ID != 1 || first.FullName != name || first.Archived != (action == "archived") {
						t.Fatalf("incorrect first repository state: %+v", first)
					}
				}
				var metadata map[string]any
				status := get("/repos/"+repositoryName(0), &metadata)
				if scenario.DeleteRepositories || scenario.RenameRepositories {
					if status != http.StatusNotFound {
						t.Fatalf("retired coordinate remains accessible: %d, %v", status, metadata)
					}
					if status := get("/repos/"+repositoryName(0)+"/actions/runs", nil); status != http.StatusNotFound {
						t.Fatalf("retired coordinate still serves runs: %d", status)
					}
				} else if status != http.StatusOK || metadata["archived"] != (action == "archived") {
					t.Fatalf("metadata differs from enumeration: %d, %v", status, metadata)
				}
				if action == "renamed" {
					if status := get("/repos/"+repositoryName(0)+"-renamed", &metadata); status != 200 || metadata["id"] != float64(1) {
						t.Fatalf("rename lost immutable identity: %d, %v", status, metadata)
					}
				}
				deliveries, err := scenario.Generate()
				if err != nil {
					t.Fatal(err)
				}
				var last struct {
					Action     string `json:"action"`
					Repository struct {
						ID       int64  `json:"id"`
						FullName string `json:"full_name"`
					} `json:"repository"`
				}
				for _, delivery := range deliveries {
					if delivery.Event != "repository" {
						continue
					}
					if err := json.Unmarshal(delivery.Payload, &last); err != nil {
						t.Fatal(err)
					}
				}
				if action != "removed" && last.Repository.ID != 1 {
					t.Fatalf("lifecycle event lost immutable identity: %+v", last)
				}
				if scenario.RenameRepositories && last.Repository.FullName != repositoryName(0)+"-renamed" {
					t.Fatalf("lifecycle event still uses old name: %+v", last)
				}
			})
		}
	}
}
