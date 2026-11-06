package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

const (
	dailyFileDietRepository = "github/gh-aw"
	dailyFileDietWorkflow   = "daily-file-diet"
	dailyFileDietCollector  = ".github/value-collectors/daily-file-diet.mjs"
)

var (
	fileDietRepositoryEndpoint = regexp.MustCompile(`^repos/[\w.-]+/[\w.-]+$`)
	fileDietSourceEndpoint     = regexp.MustCompile(`^repos/[\w.-]+/[\w.-]+/commits(?:\?path=|/[a-f0-9]+$)`)
	fileDietTimelineEndpoint   = regexp.MustCompile(`^repos/[\w.-]+/[\w.-]+/issues/[0-9]+/timeline\?`)
	fileDietRunEndpoint        = regexp.MustCompile(`^repos/[\w.-]+/[\w.-]+/actions/runs/[0-9]+$`)
	fileDietArtifactEndpoint   = regexp.MustCompile(`^repos/[\w.-]+/[\w.-]+/actions/(?:runs/[0-9]+/artifacts|artifacts/[0-9]+(?:/zip)?)\b`)
	fileDietPullEndpoint       = regexp.MustCompile(`^repos/[\w.-]+/[\w.-]+/pulls/[0-9]+(?:/files\?)?`)
)

// These are the frozen requirements from the reviewed Actions collector. The
// identity, pool, source, and primitive must agree before a native read occurs.
var dailyFileDietRequirements = []remoteValueRequirement{
	{"github", "core", "github.repository-metadata.v1", "repository", "invocation"},
	{"github", "core", "github.workflow-source-history.v1", "daily-file-diet", "invocation"},
	{"github", "search", "github.issue-search.v1", "daily-file-diet-outputs", "invocation"},
	{"github", "core", "github.issue-timeline.v1", "daily-file-diet-outputs", "invocation"},
	{"github", "graphql", "github.issue-pull-relationships.v1", "daily-file-diet-outputs", "invocation"},
	{"github", "core", "github.actions-run-provenance.v1", "daily-file-diet-outputs", "invocation"},
	{"github", "core", "github.actions-artifact-receipts.v1", "daily-file-diet-outputs", "invocation"},
	{"github", "core", "github.pull-metadata-files.v1", "daily-file-diet-outputs", "invocation"},
	{"github", "core", "github.repository-commit-history.v1", "daily-file-diet-snapshots", "invocation"},
	{"git", "git", "git.commit-inventory.v1", "daily-file-diet-go-files", "immutable"},
	{"git", "git", "git.blob-measurement.v1", "daily-file-diet-go-files", "immutable"},
}

func reviewedDailyFileDiet(entry remoteValueEntry) error {
	if entry.Repository != dailyFileDietRepository || entry.Workflow != dailyFileDietWorkflow ||
		entry.Collector != dailyFileDietCollector || entry.APIRequests < 1 || entry.APIRequests > 1000 ||
		entry.Acquisition == nil || len(entry.Acquisition.Requirements) != len(dailyFileDietRequirements) {
		return errors.New("unsupported reviewed remote Value collector registration")
	}
	required := make(map[string]bool, len(dailyFileDietRequirements))
	for _, requirement := range dailyFileDietRequirements {
		required[remoteValueRequirementKey(requirement)] = true
	}
	for _, requirement := range entry.Acquisition.Requirements {
		key := remoteValueRequirementKey(requirement)
		if !required[key] {
			return errors.New("unsupported reviewed remote Value acquisition manifest")
		}
		delete(required, key)
	}
	if len(required) > 0 {
		return errors.New("incomplete reviewed remote Value acquisition manifest")
	}
	return nil
}

func fileDietRequirement(endpoint, query string) (remoteValueRequirement, error) {
	lookup := func(primitive string) (remoteValueRequirement, error) {
		for _, requirement := range dailyFileDietRequirements {
			if requirement.Primitive == primitive {
				return requirement, nil
			}
		}
		return remoteValueRequirement{}, errors.New("unreviewed File Diet acquisition primitive")
	}
	switch {
	case query != "" || endpoint == "graphql":
		if endpoint != "graphql" || query == "" {
			break
		}
		return lookup("github.issue-pull-relationships.v1")
	case strings.HasPrefix(endpoint, "search/issues?"):
		return lookup("github.issue-search.v1")
	case fileDietRepositoryEndpoint.MatchString(endpoint):
		return lookup("github.repository-metadata.v1")
	case fileDietSourceEndpoint.MatchString(endpoint):
		return lookup("github.workflow-source-history.v1")
	case strings.HasPrefix(endpoint, "repos/") && strings.Contains(endpoint, "/commits?until="):
		return lookup("github.repository-commit-history.v1")
	case fileDietTimelineEndpoint.MatchString(endpoint):
		return lookup("github.issue-timeline.v1")
	case fileDietRunEndpoint.MatchString(endpoint):
		return lookup("github.actions-run-provenance.v1")
	case fileDietArtifactEndpoint.MatchString(endpoint):
		return lookup("github.actions-artifact-receipts.v1")
	case fileDietPullEndpoint.MatchString(endpoint):
		return lookup("github.pull-metadata-files.v1")
	}
	return remoteValueRequirement{}, errors.New("File Diet GitHub request has no reviewed acquisition requirement")
}

type nativeFileDietReader struct {
	api   *remoteValueAPI
	cache map[string][]byte
}

func (r *nativeFileDietReader) read(ctx context.Context, endpoint, query string, binary bool) ([]byte, error) {
	if r.api.authorize != nil {
		if err := r.api.authorize(ctx); err != nil {
			return nil, err
		}
	}
	requirement, err := fileDietRequirement(endpoint, query)
	if err != nil {
		return nil, err
	}
	key := remoteValueRequirementID(requirement) + "\x00" + endpoint + "\x00" + query
	if binary {
		key += "\x00binary"
	}
	if value, ok := r.cache[key]; ok {
		return bytes.Clone(value), nil
	}
	var value []byte
	if query != "" {
		value, err = r.api.graphQL(ctx, query, remoteValueRequirementID(requirement))
	} else {
		var status int
		value, _, status, err = r.api.request(ctx, endpoint, remoteValueRequirementID(requirement))
		if err == nil && status != http.StatusOK {
			if status == http.StatusNotFound && fileDietRunEndpoint.MatchString(endpoint) {
				value = []byte(fmt.Sprintf(`{"unavailable":{"status":404,"endpoint":%q,"reason":"Producer run is no longer available"}}`, endpoint))
			} else {
				return nil, fmt.Errorf("File Diet GitHub evidence unavailable (HTTP %d)", status)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if !binary {
		var payload any
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil || payload == nil {
			return nil, errors.New("File Diet GitHub response is not complete JSON")
		}
		if query != "" {
			var graph struct {
				Data   json.RawMessage   `json:"data"`
				Errors []json.RawMessage `json:"errors"`
			}
			if err := json.Unmarshal(value, &graph); err != nil || len(graph.Data) == 0 ||
				bytes.Equal(graph.Data, []byte("null")) || len(graph.Errors) != 0 {
				return nil, errors.New("File Diet GraphQL response is incomplete")
			}
		}
	}
	if r.cache == nil {
		r.cache = map[string][]byte{}
	}
	r.cache[key] = bytes.Clone(value)
	return value, nil
}
