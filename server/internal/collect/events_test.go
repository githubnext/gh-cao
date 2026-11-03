package collect

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestIssueDeliveryQueuesRepositoryRefresh(t *testing.T) {
	store, ctx := integrationStore(t)
	enrollment := Enrollment{Metadata: store, Leases: store}
	if err := enrollment.AddRepositories(ctx, 42, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Enrollment: enrollment, Queue: queue}
	payload := []byte(`{"action":"closed","installation":{"id":42},"repository":{"full_name":"octo/api"},
		"issue":{"number":12,"state":"closed","updated_at":"2026-01-02T03:04:06Z",
		"html_url":"https://github.com/octo/api/issues/12"}}`)
	first, err := admitter.AdmitDelivery(ctx, "issues", payload, "delivery-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if first.Kind != IntentIssueStatus || !first.Enqueued || first.Duplicate {
		t.Fatalf("issue delivery did not enqueue refresh: %+v", first)
	}
	duplicate, err := admitter.AdmitDelivery(ctx, "issues", payload, "delivery-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.Duplicate || duplicate.Enqueued {
		t.Fatalf("repeated issue delivery was not deduplicated: %+v", duplicate)
	}
	depth, err := queue.Depth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth != 1 {
		t.Fatalf("queue depth = %d, want one durable repository refresh", depth)
	}
	leases, err := queue.Lease(ctx, "value-test", 1, 0)
	if err != nil || len(leases) != 1 || !leases[0].Task.ValueRefresh {
		t.Fatalf("enrolled issue did not retain a Value invalidation: %+v, %v", leases, err)
	}
}

func TestParseEventClassifiesDeliveries(t *testing.T) {
	cases := []struct {
		name       string
		event      string
		payload    string
		wantKind   IntentKind
		wantRepo   string
		wantRepos  int
		wantInstal int64
	}{
		{
			name:  "completed workflow run",
			event: "workflow_run",
			payload: `{"action":"completed","installation":{"id":42},
				"repository":{"full_name":"octo/api"},
				"workflow_run":{"status":"completed","conclusion":"success"}}`,
			wantKind: IntentCollect, wantRepo: "octo/api", wantInstal: 42,
		},
		{
			name:     "in-progress workflow run is ignored",
			event:    "workflow_run",
			payload:  `{"action":"in_progress","repository":{"full_name":"octo/api"}}`,
			wantKind: IntentIgnore,
		},
		{
			name:  "installation created enrolls repositories",
			event: "installation",
			payload: `{"action":"created","installation":{"id":7},
				"repositories":[{"full_name":"octo/api"},{"full_name":"octo/web"}]}`,
			wantKind: IntentEnroll, wantRepos: 2, wantInstal: 7,
		},
		{
			name:     "installation deleted removes the installation",
			event:    "installation",
			payload:  `{"action":"deleted","installation":{"id":7}}`,
			wantKind: IntentRemoveInstallation, wantInstal: 7,
		},
		{
			name:  "repositories removed unenrolls",
			event: "installation_repositories",
			payload: `{"action":"removed","installation":{"id":7},
				"repositories_removed":[{"full_name":"octo/web"}]}`,
			wantKind: IntentUnenroll, wantRepos: 1, wantInstal: 7,
		},
		{
			name:     "unrelated events are ignored",
			event:    "push",
			payload:  `{"repository":{"full_name":"octo/api"}}`,
			wantKind: IntentIgnore,
		},
		{
			name:  "default-branch push requests only Value refresh",
			event: "push",
			payload: `{"ref":"refs/heads/main","installation":{"id":42},
				"repository":{"full_name":"octo/api","default_branch":"main"},
				"commits":[{"modified":["README.md"]}]}`,
			wantKind: IntentValueRefresh, wantRepo: "octo/api", wantInstal: 42,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			intent, err := ParseEvent(testCase.event, []byte(testCase.payload))
			if err != nil {
				t.Fatal(err)
			}
			if intent.Kind != testCase.wantKind {
				t.Fatalf("kind = %q, want %q", intent.Kind, testCase.wantKind)
			}
			if intent.Repository != testCase.wantRepo {
				t.Fatalf("repository = %q, want %q", intent.Repository, testCase.wantRepo)
			}
			if len(intent.Repositories) != testCase.wantRepos {
				t.Fatalf("repositories = %d, want %d", len(intent.Repositories), testCase.wantRepos)
			}
			if intent.InstallationID != testCase.wantInstal {
				t.Fatalf("installation = %d, want %d", intent.InstallationID, testCase.wantInstal)
			}
			if (intent.Kind == IntentCollect || intent.Kind == IntentValueRefresh) && !intent.ValueRefresh {
				t.Fatalf("collection event did not request Value invalidation: %+v", intent)
			}
		})
	}
}

func TestParseIssueEventStatusAndBoundaries(t *testing.T) {
	base := `{"action":"%s","installation":{"id":42},"repository":{"full_name":"Octo/Api"},
		"issue":{"number":12,"state":"%s","state_reason":"completed",
		"closed_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:06Z",
		"html_url":"https://github.com/Octo/Api/issues/12"%s}}`
	for _, tc := range []struct {
		action, state, extra string
		want                 IntentKind
	}{
		{"closed", "closed", "", IntentIssueStatus},
		{"reopened", "open", "", IntentIssueStatus},
		{"opened", "open", "", IntentIssueStatus},
		{"edited", "closed", "", IntentIssueStatus},
		{"labeled", "open", "", IntentIssueStatus},
		{"closed", "open", "", IntentIgnore},
		{"deleted", "closed", "", IntentIgnore},
		{"edited", "closed", `,"pull_request":{"url":"https://api.github.com/pull/12"}`, IntentIgnore},
		{"edited", "closed", `,"html_url":"https://github.com/Octo/Api/pull/12"`, IntentIgnore},
	} {
		t.Run(tc.action+"/"+tc.state+tc.extra, func(t *testing.T) {
			intent, err := ParseEvent("issues", []byte(fmt.Sprintf(base, tc.action, tc.state, tc.extra)))
			if err != nil {
				t.Fatal(err)
			}
			enterprise := strings.Replace(fmt.Sprintf(base, "closed", "closed", ""),
				"https://github.com/", "https://github.enterprise.example/", 1)
			if intent, err := ParseEvent("issues", []byte(enterprise)); err != nil || intent.Kind != IntentIssueStatus {
				t.Fatalf("enterprise issue was not classified: %+v (%v)", intent, err)
			}
			if intent.Kind != tc.want {
				t.Fatalf("kind = %q, want %q", intent.Kind, tc.want)
			}
			if tc.want == IntentIssueStatus {
				if intent.Issue.ID != "github:issue:octo/api:12" ||
					intent.Issue.Repository != "octo/api" || intent.Issue.InstallationID != 42 {
					t.Fatalf("unsafe issue identity: %+v", intent.Issue)
				}
				if tc.state == "open" && (intent.Issue.ClosedAt != "" || intent.Issue.StateReason != "") {
					t.Fatalf("reopening retained closing fields: %+v", intent.Issue)
				}
			}
		})
	}
	for _, payload := range []string{
		`{"action":"closed","installation":{"id":42},"repository":{"full_name":"octo/api"},"issue":{"number":12,"state":"closed"}}`,
		`{"action":"closed","installation":{"id":0},"repository":{"full_name":"octo/api"},"issue":{"number":12,"state":"closed","updated_at":"2026-01-02T03:04:06Z"}}`,
		`{"action":"closed","installation":{"id":42},"repository":{"full_name":"../api"},"issue":{"number":12,"state":"closed","updated_at":"2026-01-02T03:04:06Z"}}`,
	} {
		intent, err := ParseEvent("issues", []byte(payload))
		if err != nil || intent.Kind != IntentIgnore {
			t.Fatalf("invalid issue accepted: %+v %v", intent, err)
		}
	}
}

func TestParseWorkflowRunEventClassifiesActions(t *testing.T) {
	cases := []struct {
		name     string
		envelope webhookEnvelope
		wantKind IntentKind
		wantErr  bool
	}{
		{
			name: "completed run with a repository collects",
			envelope: webhookEnvelope{
				Action: "completed",
				Installation: struct {
					ID int64 `json:"id"`
				}{ID: 42},
				Repository: struct {
					ID            int64  `json:"id"`
					FullName      string `json:"full_name"`
					DefaultBranch string `json:"default_branch"`
				}{FullName: "octo/api"},
			},
			wantKind: IntentCollect,
		},
		{
			name:     "in-progress run is ignored",
			envelope: webhookEnvelope{Action: "in_progress"},
			wantKind: IntentIgnore,
		},
		{
			name:     "completed run without a repository fails closed",
			envelope: webhookEnvelope{Action: "completed"},
			wantErr:  true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			intent, err := parseWorkflowRunEvent(testCase.envelope)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if intent.Kind != testCase.wantKind {
				t.Fatalf("kind = %q, want %q", intent.Kind, testCase.wantKind)
			}
		})
	}
}

func TestParseInstallationEventClassifiesActions(t *testing.T) {
	cases := []struct {
		action   string
		wantKind IntentKind
	}{
		{"created", IntentEnroll},
		{"new_permissions_accepted", IntentEnroll},
		{"unsuspend", IntentEnroll},
		{"deleted", IntentRemoveInstallation},
		{"suspend", IntentRemoveInstallation},
		{"renamed", IntentIgnore},
	}
	for _, testCase := range cases {
		t.Run(testCase.action, func(t *testing.T) {
			envelope := webhookEnvelope{
				Action: testCase.action,
				Installation: struct {
					ID int64 `json:"id"`
				}{ID: 7},
			}
			intent := parseInstallationEvent(envelope)
			if intent.Kind != testCase.wantKind {
				t.Fatalf("kind = %q, want %q", intent.Kind, testCase.wantKind)
			}
			if testCase.wantKind != IntentIgnore && intent.InstallationID != 7 {
				t.Fatalf("installation = %d, want 7", intent.InstallationID)
			}
		})
	}
}

func TestParseInstallationRepositoriesEventClassifiesActions(t *testing.T) {
	cases := []struct {
		action   string
		wantKind IntentKind
	}{
		{"added", IntentEnroll},
		{"removed", IntentUnenroll},
		{"other", IntentIgnore},
	}
	for _, testCase := range cases {
		t.Run(testCase.action, func(t *testing.T) {
			envelope := webhookEnvelope{Action: testCase.action}
			intent := parseInstallationRepositoriesEvent(envelope)
			if intent.Kind != testCase.wantKind {
				t.Fatalf("kind = %q, want %q", intent.Kind, testCase.wantKind)
			}
		})
	}
}

func TestParseEventFailsClosedOnUnusablePayload(t *testing.T) {
	if _, err := ParseEvent("workflow_run", []byte("not json")); err == nil {
		t.Fatal("expected a parse failure")
	}
	if _, err := ParseEvent("workflow_run", []byte(`{"action":"completed"}`)); err == nil {
		t.Fatal("expected a missing-repository failure")
	}
}

func TestNormalizeRepositoryRejectsUnsafeReferences(t *testing.T) {
	valid := []struct{ input, want string }{
		{"octo/api", "octo/api"},
		{" Octo/Api ", "octo/api"},
	}
	for _, testCase := range valid {
		input, want := testCase.input, testCase.want
		got, err := NormalizeRepository(input)
		if err != nil {
			t.Fatalf("%q: %v", input, err)
		}
		if got != want {
			t.Fatalf("%q normalized to %q, want %q", input, got, want)
		}
	}
	invalid := []string{"", "octo", "octo/api/extra", "../etc", "octo/../api", "octo/a pi", "-octo/api"}
	for _, input := range invalid {
		if _, err := NormalizeRepository(input); err == nil {
			t.Fatalf("expected %q to be rejected", input)
		}
	}
}

func TestDecideCollectInstallationRequiresEnrollmentAndInstallation(t *testing.T) {
	cases := []struct {
		name           string
		enrolled       bool
		installationID int64
		wantOK         bool
		wantID         int64
	}{
		{name: "enrolled with installation", enrolled: true, installationID: 42, wantOK: true, wantID: 42},
		{name: "enrolled without installation mapping", enrolled: true, installationID: 0, wantOK: false},
		{name: "not enrolled but installation known", enrolled: false, installationID: 42, wantOK: false},
		{name: "not enrolled and no installation", enrolled: false, installationID: 0, wantOK: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			gotID, gotOK := decideCollectInstallation(testCase.enrolled, testCase.installationID)
			if gotOK != testCase.wantOK {
				t.Fatalf("ok = %t, want %t", gotOK, testCase.wantOK)
			}
			if gotOK && gotID != testCase.wantID {
				t.Fatalf("installationID = %d, want %d", gotID, testCase.wantID)
			}
			if !gotOK && gotID != 0 {
				t.Fatalf("installationID = %d, want 0 when rejected", gotID)
			}
		})
	}
}
