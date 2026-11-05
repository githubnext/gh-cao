package collectnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

type stepProvider struct {
	staticProvider
	files map[string][]byte
	gap   string
}

func (p stepProvider) StepLogs(context.Context, Run) (StepLogArchive, error) {
	return StepLogArchive{Files: p.files, Gap: p.gap}, nil
}

func TestNativeValidationPinnedStepGrouping(t *testing.T) {
	for _, stepCount := range []int{1, 6} {
		t.Run(fmt.Sprint(stepCount), func(t *testing.T) {
			files := map[string][]byte{}
			for i := 1; i <= stepCount; i++ {
				files[fmt.Sprintf("agent/%d_private step.txt", i)] = []byte(strings.Repeat("2026-10-15T10:00:00Z ##[error]private diagnostic\n", 9))
			}
			fields := map[string]any{"conclusion": "failure"}
			used := 0
			gaps, err := acquireValidation(t.Context(), stepProvider{files: files}, nativeRun(), fields, Limits{}, &used)
			if err != nil || len(gaps) != 0 || len(array(object(fields["audit"])["errors"])) != stepCount {
				t.Fatalf("one issue per step violated: %s %v %v", mustJSON(t, fields), gaps, err)
			}
			_, err = enrichAuditReports(t.Context(), fields, map[string][]byte{
				"agent/agent_output.json": []byte(`{"items":[{"type":"missing_tool","tool":"read"}]}`),
			}, testIdentity(), Limits{})
			if err != nil || len(array(object(fields["audit"])["errors"])) != stepCount {
				t.Fatal("agent reports displaced acquired validation issues")
			}
			findings, _ := defaultAuditFindings(fields)
			found := false
			for _, finding := range findings {
				found = found || object(finding)["code"] == "multiple_errors"
			}
			if found != (stepCount > 5) {
				t.Fatal("multiple_errors counted annotation lines rather than step issues")
			}
			if strings.Contains(string(mustJSON(t, fields)), "private") {
				t.Fatal("raw step name/diagnostic was persisted")
			}
		})
	}
}

func TestNativeValidationToolResultAndFallback(t *testing.T) {
	annotation := []byte(`2026-10-15T10:00:00Z ##[error]{"type":"user","message":{"content":[{"type":"tool_result","content":"private error"}]}}`)
	if !agentToolResultAnnotation(annotation) || agentToolResultAnnotation([]byte(`##[error]{"type":"user","message":{"content":[{"type":"text"}]}}`)) {
		t.Fatal("tool-result annotation classifier differs from upstream")
	}
	fields := map[string]any{"conclusion": "failure"}
	used := 0
	gaps, err := acquireValidation(t.Context(), stepProvider{files: map[string][]byte{"agent/1_run.txt": annotation}},
		nativeRun(), fields, Limits{}, &used)
	if err != nil || len(gaps) != 0 || len(array(object(fields["audit"])["errors"])) != 0 {
		t.Fatal("tool result treated as workflow failure")
	}
	if err := finishValidation(t.Context(), fields, nil, Limits{}); err != nil || len(array(object(fields["audit"])["errors"])) != 1 {
		t.Fatal("last nonempty step fallback was omitted")
	}
	for _, conclusion := range []string{"success", "timed_out", "failure"} {
		fields := map[string]any{"conclusion": conclusion}
		err := finishValidation(t.Context(), fields, map[string][]byte{"agent/agent-stdio.log": []byte("private agent stderr")}, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if count := len(array(object(fields["audit"])["errors"])); (count == 1) != (conclusion == "failure") {
			t.Fatal("validation fallback ran for a non-failure conclusion")
		}
	}
	used = 0
	_, err = acquireValidation(t.Context(), stepProvider{files: map[string][]byte{"agent/1_run.txt": []byte("too large")}},
		nativeRun(), map[string]any{"conclusion": "failure"}, Limits{Bytes: 2}, &used)
	if !errors.Is(err, ErrCapacity) {
		t.Fatal("step acquisition exceeded aggregate byte budget")
	}
}

func TestGitHubNativeExactAttemptStepArchive(t *testing.T) {
	archive := zipArtifact(t, map[string][]byte{
		"agent/1_Run.txt": []byte("2026-10-15T10:00:00Z ##[error]private failure"),
		"ignored.json":    []byte("private payload"),
	})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4900")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
		if r.URL.Path != "/repos/octo/api/actions/runs/303/attempts/1/logs" {
			t.Errorf("step acquisition escaped exact attempt: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(archive)
	}))
	defer api.Close()
	provider := GitHub{BaseURL: api.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}
	fields, used := map[string]any{"conclusion": "failure"}, 0
	t.Setenv("PATH", "/nonexistent")
	gaps, err := acquireValidation(t.Context(), provider, nativeRun(), fields, Limits{}, &used)
	if err != nil || len(gaps) != 0 || len(array(object(fields["audit"])["errors"])) != 1 {
		t.Fatalf("real step archive not integrated: %s %v %v", mustJSON(t, fields), gaps, err)
	}
	var serialized map[string]any
	if json.Unmarshal(mustJSON(t, fields), &serialized) != nil {
		t.Fatal("invalid safe diagnostic projection")
	}
	gaps, err = acquireValidation(t.Context(), stepProvider{gap: "workflow-step-logs-unavailable"},
		nativeRun(), map[string]any{"conclusion": "failure"}, Limits{}, &used)
	if err != nil || !slices.Contains(gaps, "workflow-step-logs-unavailable") {
		t.Fatal("unavailable archive reported as complete validation")
	}
}
