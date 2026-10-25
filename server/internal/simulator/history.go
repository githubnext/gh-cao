package simulator

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// History generates identities on demand; enterprise-scale API pages do not
// allocate the complete repository/run corpus.
type History struct {
	Days       int    `json:"days"`
	RunsPerDay int    `json:"runs_per_day"`
	AsOf       string `json:"as_of"`
}

func (h History) Validate(repositories int) error {
	if repositories < 1 || repositories > maxRepositories || h.Days < 1 || h.Days > 31 || h.RunsPerDay < 1 ||
		h.RunsPerDay > 5_000_000/(repositories*h.Days) {
		return errors.New("synthetic history requires 1-31 days, positive daily runs, and at most 5000000 runs")
	}
	if _, err := time.Parse(time.RFC3339, h.AsOf); err != nil {
		return errors.New("synthetic history as_of must be an RFC3339 timestamp")
	}
	return nil
}

func repositoryName(index int) string { return fmt.Sprintf("simulator/repo-%05d", index+1) }

type HistoricalRun struct {
	ID         int64     `json:"id"`
	Attempt    int       `json:"run_attempt"`
	CreatedAt  time.Time `json:"created_at"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
}

func (h History) Run(repository, offset int) HistoricalRun {
	asOf, _ := time.Parse(time.RFC3339, h.AsOf)
	created := asOf.Add(-time.Duration(offset) * (24 * time.Hour / time.Duration(h.RunsPerDay)))
	return HistoricalRun{
		ID: int64(repository*h.Days*h.RunsPerDay + offset + 1), Attempt: 1,
		CreatedAt: created, Status: "completed", Conclusion: "success",
	}
}

func historyPage(request *http.Request, total int) (int, int, int, bool) {
	page, size := 1, 100
	if value := request.URL.Query().Get("page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 5_000_001 {
			return 0, 0, 0, false
		}
		page = parsed
	}
	if value := request.URL.Query().Get("per_page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, 0, 0, false
		}
		size = parsed
	}
	start := min((page-1)*size, total)
	return start, min(start+size, total), page, true
}

func historyLink(writer http.ResponseWriter, request *http.Request, page int) {
	next := *request.URL
	parameters := next.Query()
	parameters.Set("page", strconv.Itoa(page+1))
	next.RawQuery = parameters.Encode()
	writer.Header().Set("Link", fmt.Sprintf(`<http://%s%s>; rel="next"`, request.Host, next.String()))
}

func (s Scenario) historyResponse(writer http.ResponseWriter, request *http.Request) (any, bool, error) {
	switch request.URL.Path {
	case "/app/installations":
		return []any{map[string]any{"id": 1, "account": map[string]string{"login": "simulator"}}}, true, nil
	case "/installation/repositories":
		offset := 0
		if s.DeleteRepositories || s.RemoveRepositories {
			offset = 1
		}
		total := s.Repositories - offset
		start, end, page, valid := historyPage(request, total)
		if !valid {
			return nil, true, errors.New("invalid repository pagination")
		}
		if end < total {
			historyLink(writer, request, page)
		}
		repositories := make([]any, 0, end-start)
		for index := start + offset; index < end+offset; index++ {
			name := repositoryName(index)
			if index == 0 && s.RenameRepositories {
				name += "-renamed"
			}
			repository := map[string]any{
				"id": index + 1, "full_name": name, "private": false,
				"visibility": "public",
				"archived":   index == 0 && s.ArchiveRepositories && !s.UnarchiveRepositories,
			}
			if s.History != nil {
				repository["pushed_at"] = s.History.AsOf
			}
			repositories = append(repositories, repository)
		}
		return map[string]any{"total_count": total, "repositories": repositories}, true, nil
	}
	if s.History == nil {
		return nil, false, nil
	}
	if !strings.HasPrefix(request.URL.Path, "/repos/simulator/repo-") ||
		!strings.HasSuffix(request.URL.Path, "/actions/runs") {
		return nil, false, nil
	}
	name := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/repos/simulator/repo-"), "/actions/runs")
	if s.RenameRepositories && name == "00001-renamed" {
		name = "00001"
	}
	index, err := strconv.Atoi(name)
	if err != nil || index < 1 || index > s.Repositories {
		return nil, true, errors.New("unknown synthetic repository")
	}
	var after, before time.Time
	if created := request.URL.Query().Get("created"); created != "" {
		start, end, found := strings.Cut(created, "..")
		if !found {
			return nil, true, errors.New("created filter must be an inclusive time range")
		}
		after, err = time.Parse(time.RFC3339, start)
		if err != nil {
			return nil, true, errors.New("invalid created range start")
		}
		before, err = time.Parse(time.RFC3339, end)
		if err != nil || before.Before(after) {
			return nil, true, errors.New("invalid created range end")
		}
	}
	runs := make([]any, 0, s.History.Days*s.History.RunsPerDay)
	for offset := 0; offset < s.History.Days*s.History.RunsPerDay; offset++ {
		run := s.History.Run(index-1, offset)
		if !after.IsZero() && (run.CreatedAt.Before(after) || run.CreatedAt.After(before)) {
			continue
		}
		runs = append(runs, run)
	}
	start, end, page, valid := historyPage(request, len(runs))
	if !valid {
		return nil, true, errors.New("invalid run pagination")
	}
	if end < len(runs) {
		historyLink(writer, request, page)
	}
	return map[string]any{"total_count": len(runs), "workflow_runs": runs[start:end]}, true, nil
}
