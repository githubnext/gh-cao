package tokenbroker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestV2RequestShapes(t *testing.T) {
	for _, tokenType := range []string{"copilot-request", "copilot-task", "github-app", "unknown", "invalidate"} {
		for _, job := range []any{nil, "", "agent", "job ü;%\r\n", 1, true, []string{"agent"}, map[string]string{"job": "agent"}} {
			body := map[string]any{"oidc_token": "valid", "job_id": job}
			if tokenType == "invalidate" {
				body["operation"] = tokenType
			} else {
				body["token_type"] = tokenType
			}
			if tokenType == "copilot-request" {
				body["provider"] = "github"
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/token-broker", strings.NewReader(string(raw)))
			r.Header.Set("Content-Type", "application/json")
			_, status := decodeRequest(httptest.NewRecorder(), r)
			want := http.StatusBadRequest
			if _, ok := job.(string); ok {
				want = 0
				if tokenType == "unknown" {
					want = http.StatusUnprocessableEntity
				}
			}
			if status != want {
				t.Errorf("%s job=%v: status=%d want=%d", tokenType, job, status, want)
			}
		}
	}
}

func TestUniqueJSONIsDepthBoundedAndStrict(t *testing.T) {
	for _, value := range []string{
		`{"a":1,"a":2}`,
		`{"a":{"b":1,"b":2}}`,
		`{"a":1,"\u0061":2}`,
		`{} {}`,
		`{"a":}`,
		string([]byte{'"', 0xff, '"'}),
		strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66),
	} {
		if uniqueJSON([]byte(value)) == nil {
			t.Fatal("duplicate, invalid, or excessively nested JSON accepted")
		}
	}
	if err := uniqueJSON([]byte(`{"a":[1,{"b":true}],"c":null}`)); err != nil {
		t.Fatal("bounded unique JSON rejected")
	}
}
