package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	cao "github.com/githubnext/gh-cao"
)

func modernizationFixturePlan() map[string]any {
	return map[string]any{
		"schema": "cao-modernization/v1", "id": "test", "objective": "Modernize the complete system",
		"repositories": []any{map[string]any{"repository": "octodemo/control", "base_sha": strings.Repeat("a", 40), "base_branch": "main"}},
		"tasks": []any{
			map[string]any{"id": "first", "repository": "octodemo/control", "title": "First task", "objective": "Do first task", "acceptance": []string{"Works"}, "depends_on": []string{}, "specialists": []string{"assessor"}},
			map[string]any{"id": "second", "repository": "octodemo/control", "title": "Dependent task", "objective": "Do dependent task", "acceptance": []string{"Works"}, "depends_on": []string{"first"}, "specialists": []string{"assessor"}},
		},
		"integration_acceptance": []string{"System works"},
	}
}

func TestModernizationUsesProductionPlanAndReadiness(t *testing.T) {
	input := map[string]any{
		"plan": modernizationFixturePlan(), "repositories": []string{"octodemo/control"}, "specialists": []string{"assessor"},
		"candidates": []map[string]any{{"full_name": "octodemo/control", "safe_output_mode": "review", "safe_output_repo": "octodemo/control"}},
		"evidence":   map[string]any{"first": map[string]any{"complete": false, "issue": 12}, "second": map[string]any{"complete": false}},
	}
	request := func(string, map[string]any) (map[string]any, error) {
		return nil, errors.New("unexpected network read")
	}
	var result map[string]any
	err := modernizationEvaluate(t.Context(), input, request, `(engagement.validatePlan(input.plan,input.repositories,input.specialists),
engagement.readyTasks(input.plan,input.evidence,input.candidates,3))`, &result)
	if err != nil {
		t.Fatal(err)
	}
	ready := nativeArray(result["ready"])
	if len(ready) != 1 || ready[0].(map[string]any)["phase"] != "implement" {
		t.Fatalf("production issue/implementation readiness lost: %#v", result)
	}
	plan := modernizationFixturePlan()
	tasks := plan["tasks"].([]any)
	tasks[0].(map[string]any)["depends_on"] = []string{"second"}
	input["plan"] = plan
	if err := modernizationEvaluate(t.Context(), input, request, "engagement.validatePlan(input.plan,input.repositories,input.specialists)", &result); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("cyclic plan admitted: %v", err)
	}
}

type modernizationFixture struct {
	deps      modernizationDeps
	sha       string
	files     map[string][]byte
	writes    int
	dirty     bool
	issue     bool
	failWrite bool
}

func newModernizationFixture(t *testing.T, approved bool) *modernizationFixture {
	t.Helper()
	t.Setenv("CAO_MODERNIZATION_PREVIEW_DIR", t.TempDir()+"/previews")
	fixture := &modernizationFixture{sha: strings.Repeat("a", 40), files: map[string][]byte{}}
	policy := map[string]any{"version": 1, "gh-aw-version": "v1.2.3", "control-plane": map[string]any{
		"scope":     map[string]any{"allowed-owners": []string{"octodemo"}, "allowed-repositories": []string{"octodemo/control"}},
		"defaults":  map[string]any{"max-repositories": 3},
		"workflows": map[string]any{"modernization-system": map[string]any{"enabled": true, "mode": "review", "max-mode": "review"}},
		"campaigns": map[string]any{"modernization": map[string]any{"enabled": true, "mode": "live",
			"workers": map[string]any{"task": map[string]any{"workflow": "modernization-task", "enabled": true, "max-mode": "live"}}}},
	}}
	fixture.files[policyPath], _ = json.Marshal(policy)
	for _, path := range []string{"modernization/engagement.mjs", "modernization/github.mjs", "modernization/actions.mjs"} {
		content, err := cao.OperatorSources.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fixture.files[path] = content
	}
	if approved {
		fixture.files[modernizationPlanPath], _ = json.Marshal(modernizationFixturePlan())
	}
	for _, workflow := range []string{"modernization", "modernization-system"} {
		for _, suffix := range []string{".md", ".lock.yml"} {
			content, err := os.ReadFile("../../.github/workflows/" + workflow + suffix)
			if err != nil {
				t.Fatal(err)
			}
			fixture.files[".github/workflows/"+workflow+suffix] = content
		}
	}
	fixture.deps.git = func(_ context.Context, args ...string) (string, error) {
		switch args[0] {
		case "remote":
			return "https://github.com/octodemo/control.git", nil
		case "status":
			if fixture.dirty {
				return " M unrelated.txt", nil
			}
			return "", nil
		case "rev-parse":
			return fixture.sha, nil
		default:
			return "", fmt.Errorf("unexpected Git invocation: %v", args)
		}
	}
	fixture.deps.gh = func(_ context.Context, args ...string) (string, string, error) {
		if slices.Contains(args, "POST") {
			fixture.writes++
			if fixture.failWrite {
				return "", "", errors.New("connection lost after write")
			}
			input := args[len(args)-1]
			content, err := os.ReadFile(input)
			if err != nil {
				return "", "", err
			}
			if slices.Contains(args, "graphql") {
				var request map[string]any
				if err := json.Unmarshal(content, &request); err != nil {
					return "", "", err
				}
				variables := request["variables"].(map[string]any)["input"].(map[string]any)
				if variables["expectedHeadOid"] != fixture.sha {
					t.Fatal("approval did not bind expected remote head")
				}
				changes := variables["fileChanges"].(map[string]any)["additions"].([]any)
				if len(changes) != 1 || changes[0].(map[string]any)["path"] != modernizationPlanPath {
					t.Fatalf("approval touched files outside engagement: %s", content)
				}
				return `{"data":{"createCommitOnBranch":{"commit":{"oid":"` + strings.Repeat("b", 40) + `","url":"https://github.com/octodemo/control/commit/` + strings.Repeat("b", 40) + `"}}}}`, "", nil
			}
			var request struct {
				Ref    string
				Inputs map[string]string
			}
			if err := json.Unmarshal(content, &request); err != nil || request.Ref != "main" || request.Inputs["expected_control_sha"] != fixture.sha {
				t.Fatalf("dispatch missing exact reviewed fence: %s %v", content, err)
			}
			return "", "", nil
		}
		endpoint := args[len(args)-1]
		metadata := map[string]any{"id": 1, "full_name": "octodemo/control", "default_branch": "main", "archived": false, "disabled": false, "permissions": map[string]any{"push": true}}
		var data any
		if slices.Contains(args, "--include") {
			switch {
			case strings.Contains(endpoint, "/commits/") && strings.HasSuffix(endpoint, "/status"):
				data = map[string]any{"statuses": []any{}, "total_count": 0}
			case strings.Contains(endpoint, "/commits/"):
				data = map[string]any{"sha": fixture.sha}
			case strings.Contains(endpoint, "/compare/"):
				data = map[string]any{"status": "identical"}
			case strings.Contains(endpoint, "/issues?"):
				data = []any{}
				if fixture.issue {
					var digest string
					if err := modernizationEvaluate(t.Context(), modernizationFixturePlan(), nil, "engagement.planDigest(input)", &digest); err != nil {
						t.Fatal(err)
					}
					data = []any{map[string]any{"number": 12, "state": "open", "user": map[string]any{"type": "Bot"},
						"body":     "<!-- cao-modernization:" + digest + ":first -->\n<!-- cao-modernization-phase:issue -->",
						"html_url": "https://github.com/octodemo/control/issues/12"}}
				}
			case strings.Contains(endpoint, "/pulls?"):
				data = []any{}
			default:
				data = metadata
			}
			content, _ := json.Marshal(data)
			return "HTTP/2 200 OK\r\n\r\n" + string(content), "", nil
		}
		switch {
		case endpoint == "user":
			data = map[string]any{"id": 7, "login": "operator", "type": "User"}
		case endpoint == "repos/octodemo/control":
			data = metadata
		case strings.Contains(endpoint, "/commits/"):
			data = map[string]any{"sha": fixture.sha}
		case strings.Contains(endpoint, "/git/trees/"):
			tree := []map[string]string{}
			for path := range fixture.files {
				tree = append(tree, map[string]string{"path": path, "type": "blob"})
			}
			data = map[string]any{"tree": tree, "truncated": false}
		case strings.Contains(endpoint, "/contents/modernization/squad/.squad/agents?"):
			data = []map[string]string{{"name": "assessor", "type": "dir"}}
		case strings.Contains(endpoint, "/contents/"):
			path, _, _ := strings.Cut(strings.SplitN(endpoint, "/contents/", 2)[1], "?")
			content := fixture.files[path]
			data = map[string]any{"content": base64.StdEncoding.EncodeToString(content), "encoding": "base64", "size": len(content)}
		case strings.Contains(endpoint, "/actions/workflows/"):
			data = map[string]any{"state": "active"}
		default:
			return "", "", fmt.Errorf("unexpected GitHub read: %s", endpoint)
		}
		content, _ := json.Marshal(data)
		return string(content), "", nil
	}
	return fixture
}

func TestModernizationPreviewExecutionAndReplay(t *testing.T) {
	for _, action := range []string{"assess", "approve-plan", "run", "verify"} {
		t.Run(action, func(t *testing.T) {
			fixture := newModernizationFixture(t, action == "run" || action == "verify")
			var plan map[string]any
			if action == "approve-plan" {
				plan = modernizationFixturePlan()
			}
			preview, err := modernizationOperation(t.Context(), fixture.deps, "octodemo/control", "preview", action, "review", "", plan)
			if err != nil || preview["canExecute"] != true {
				t.Fatalf("preview rejected: %#v %v", preview, err)
			}
			if fixture.writes != 0 {
				t.Fatal("preview mutated GitHub")
			}
			id := nativeText(preview["previewId"])
			result, err := modernizationOperation(t.Context(), fixture.deps, "octodemo/control", "execute", "", "", id, nil)
			if err != nil || result["accepted"] != true || fixture.writes != 1 {
				t.Fatalf("execution failed: %#v %v writes=%d", result, err, fixture.writes)
			}
			if _, err := modernizationOperation(t.Context(), fixture.deps, "octodemo/control", "execute", "", "", id, nil); err == nil || fixture.writes != 1 {
				t.Fatalf("preview replay allowed: %v", err)
			}
		})
	}
}

func TestModernizationRejectsChangedAndUncertainReviews(t *testing.T) {
	for _, change := range []string{"head", "dirty", "evidence", "expired", "uncertain"} {
		t.Run(change, func(t *testing.T) {
			fixture := newModernizationFixture(t, change == "evidence")
			action := "approve-plan"
			plan := modernizationFixturePlan()
			if change == "evidence" {
				action, plan = "run", nil
			}
			preview, err := modernizationOperation(t.Context(), fixture.deps, "octodemo/control", "preview", action, "review", "", plan)
			if err != nil || preview["canExecute"] != true {
				t.Fatalf("preview: %#v %v", preview, err)
			}
			switch change {
			case "head":
				fixture.sha = strings.Repeat("c", 40)
			case "dirty":
				fixture.dirty = true
			case "evidence":
				fixture.issue = true
			case "expired":
				directory, err := modernizationPreviewDirectory()
				if err != nil {
					t.Fatal(err)
				}
				name := directory + "/" + nativeText(preview["previewId"]) + ".json"
				content, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				var review modernizationReview
				if err := json.Unmarshal(content, &review); err != nil {
					t.Fatal(err)
				}
				review.Expires = review.Expires.Add(-10 * 60 * 1e9)
				content, _ = json.Marshal(review)
				if err := os.WriteFile(name, content, 0o600); err != nil {
					t.Fatal(err)
				}
			case "uncertain":
				fixture.failWrite = true
			}
			id := nativeText(preview["previewId"])
			_, err = modernizationOperation(t.Context(), fixture.deps, "octodemo/control", "execute", "", "", id, nil)
			if err == nil {
				t.Fatal("changed or uncertain review executed")
			}
			expectedWrites := 0
			if change == "uncertain" {
				expectedWrites = 1
				_, _ = modernizationOperation(t.Context(), fixture.deps, "octodemo/control", "execute", "", "", id, nil)
			}
			if fixture.writes != expectedWrites {
				t.Fatalf("unsafe mutation/retry count=%d expected=%d: %v", fixture.writes, expectedWrites, err)
			}
		})
	}
}

func TestModernizationMissingFenceAndPolicyCeiling(t *testing.T) {
	fixture := newModernizationFixture(t, true)
	delete(fixture.files, ".github/workflows/modernization.lock.yml")
	result, err := modernizationOperation(t.Context(), fixture.deps, "octodemo/control", "preview", "run", "review", "", nil)
	if err != nil || result["canExecute"] != false || !strings.Contains(nativeText(result["reason"]), "fence") {
		t.Fatalf("unfenced dispatch admitted: %#v %v", result, err)
	}
	result, err = modernizationOperation(t.Context(), fixture.deps, "octodemo/control", "preview", "verify", "live", "", nil)
	if err == nil || result != nil {
		t.Fatalf("live system verification admitted: %#v %v", result, err)
	}
	source, err := cao.OperatorSources.ReadFile("modernization/engagement.mjs")
	if err != nil || len(source) == 0 {
		t.Fatal("production engagement source is not embedded")
	}
}
