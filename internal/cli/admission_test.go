package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func executeAdmissionTest(t *testing.T, gh ghExecutor, args ...string) (admissionCLIReport, string, error) {
	t.Helper()
	root := newCommand("test", nil, gh, nil)
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs(append([]string{"admission"}, args...))
	if len(args) > 0 && args[0] == "setup" {
		root.SetArgs(append([]string{"setup", "admission"}, args[1:]...))
	}
	err := root.ExecuteContext(t.Context())
	var report admissionCLIReport
	if output.Len() != 0 && !strings.Contains(strings.Join(args, " "), "--help") {
		if decodeErr := json.Unmarshal(output.Bytes(), &report); decodeErr != nil {
			t.Fatalf("invalid JSON output: %s", &output)
		}
	}
	return report, output.String(), err
}

func TestAdmissionCommandsPreviewAndApply(t *testing.T) {
	for _, apply := range []bool{false, true} {
		calls := [][]string{}
		gh := func(_ context.Context, args ...string) (string, string, error) {
			calls = append(calls, args)
			if len(args) > 3 && strings.HasSuffix(args[3], "?per_page=100") {
				return `[{"name":"GH_AW_DEFAULT_ADMISSION_AUDIENCE","value":"old"}]`, "", nil
			}
			return "", "", nil
		}
		args := []string{"setup", "--repo", "acme/project", "--url", "https://cao.example/api/admission", "--json"}
		if !apply {
			args = append(args, "--dry-run")
		}
		report, _, err := executeAdmissionTest(t, gh, args...)
		if err != nil || report.Applied != apply || !report.Enabled ||
			report.Settings.Audience != "https://cao.example/api/admission" || report.Settings.OnOutage != "block" {
			t.Fatalf("setup: %+v %v", report, err)
		}
		wantCalls := 1
		if apply {
			wantCalls = 4
		}
		if len(calls) != wantCalls {
			t.Fatalf("unexpected mutation: %v", calls)
		}
		if apply {
			want := [][]string{
				{"api", "--hostname", "github.com", "repos/acme/project/actions/variables", "--method", "POST", "--raw-field", "name=GH_AW_DEFAULT_ADMISSION_ON_OUTAGE", "--raw-field", "value=block"},
				{"api", "--hostname", "github.com", "repos/acme/project/actions/variables/GH_AW_DEFAULT_ADMISSION_AUDIENCE", "--method", "PATCH", "--raw-field", "value=https://cao.example/api/admission"},
				{"api", "--hostname", "github.com", "repos/acme/project/actions/variables", "--method", "POST", "--raw-field", "name=GH_AW_DEFAULT_ADMISSION_URL", "--raw-field", "value=https://cao.example/api/admission"},
			}
			if !reflect.DeepEqual(calls[1:], want) {
				t.Fatalf("wrong mutation ordering or fields: %v", calls[1:])
			}
		}
	}
}

func TestAdmissionStatusPrecedenceAndDisable(t *testing.T) {
	for _, emptyOverride := range []bool{false, true} {
		gh := func(_ context.Context, args ...string) (string, string, error) {
			if strings.Contains(args[3], "/organization-variables") {
				return `[{"name":"GH_AW_DEFAULT_ADMISSION_URL","value":"https://org.example/api/admission"},{"name":"GH_AW_DEFAULT_ADMISSION_ON_OUTAGE","value":"block"}]`, "", nil
			}
			if emptyOverride {
				return `[{"name":"GH_AW_DEFAULT_ADMISSION_URL","value":""}]`, "", nil
			}
			return `[]`, "", nil
		}
		report, _, err := executeAdmissionTest(t, gh, "status", "--repo", "acme/project", "--json")
		if err != nil || report.Enabled == emptyOverride {
			t.Fatalf("precedence: %+v %v", report, err)
		}
		wantSource := "organization"
		if emptyOverride {
			wantSource = "repository"
		}
		if report.Sources[admissionURLVariable] != wantSource {
			t.Fatalf("source: %+v", report.Sources)
		}
	}
	for _, exists := range []bool{false, true} {
		var mutation []string
		gh := func(_ context.Context, args ...string) (string, string, error) {
			if strings.Contains(args[3], "?per_page") {
				if exists {
					return `[{"name":"GH_AW_DEFAULT_ADMISSION_URL","value":"https://old.example/api/admission"}]`, "", nil
				}
				return "[]", "", nil
			}
			mutation = args
			return "", "", nil
		}
		report, _, err := executeAdmissionTest(t, gh, "disable", "--repo", "acme/project", "--json")
		if err != nil || !report.Applied || report.Enabled || mutation[len(mutation)-1] != "value=" {
			t.Fatalf("disable: %+v %v %v", report, err, mutation)
		}
		if strings.Contains(strings.Join(mutation, " "), "DELETE") {
			t.Fatal("deletion could reactivate inherited gate")
		}
	}
}

func TestAdmissionRejectsInvalidSettingsWithoutGitHub(t *testing.T) {
	for _, flags := range [][]string{
		{"--repo", "../attacker"},
		{"--url", "http://cao.example/api/admission"},
		{"--url", "https://cao.example/other"},
		{"--url", "https://user:secret@cao.example/api/admission"},
		{"--url", "https://cao.example/api/admission?"},
		{"--url", "https://cao.example/api/admission#"},
		{"--url", "https://cao.example/api/admission?x=1"},
		{"--url", "https://cao.example/api/%61dmission"},
		{"--audience", "${{ secrets.TOKEN }}"},
		{"--audience", "bad\nvalue"},
		{"--on-outage", "allow"},
	} {
		args := append([]string{"setup", "--repo", "acme/project", "--url", "https://cao.example/api/admission", "--json"}, flags...)
		_, _, err := executeAdmissionTest(t, func(context.Context, ...string) (string, string, error) {
			t.Fatal("invalid configuration reached GitHub")
			return "", "", nil
		}, args...)
		if err == nil {
			t.Fatalf("accepted %v", flags)
		}
	}
}

func TestAdmissionFailureRedactionAndPartialApply(t *testing.T) {
	calls := 0
	gh := func(_ context.Context, _ ...string) (string, string, error) {
		calls++
		if calls == 1 {
			return "[]", "", nil
		}
		if calls == 3 {
			return "", "synthetic-sensitive-diagnostic", errors.New("synthetic-sensitive-error")
		}
		return "", "", nil
	}
	_, out, err := executeAdmissionTest(t, gh, "setup", "--repo", "acme/project", "--url", "https://cao.example/api/admission", "--json")
	if err == nil || calls != 3 || !strings.Contains(err.Error(), "earlier settings may have changed") ||
		strings.Contains(out+err.Error(), "synthetic-sensitive") {
		t.Fatalf("unsafe partial failure: %v %s calls=%d", err, out, calls)
	}
	for _, response := range []string{"", "null", "{}", "invalid", `[{"name":"OTHER","value":"unexpected"}]`,
		`[{"name":"GH_AW_DEFAULT_ADMISSION_URL","value":"a"},{"name":"GH_AW_DEFAULT_ADMISSION_URL","value":"b"}]`} {
		if _, err := readAdmissionVariables(func(...string) (string, error) { return response, nil }, "unused"); err == nil {
			t.Fatalf("accepted malformed metadata %s", response)
		}
	}
	values, err := readAdmissionVariables(func(...string) (string, error) {
		return "[]\n" + `[{"name":"GH_AW_DEFAULT_ADMISSION_ON_OUTAGE","value":"ignore"}]`, nil
	}, "unused")
	if err != nil || values[admissionOutageVariable] != "ignore" {
		t.Fatalf("pagination: %v %v", values, err)
	}
}

func TestAdmissionHelpOffline(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"setup", "--help"}, {"status", "--help"}, {"disable", "--help"}} {
		_, output, err := executeAdmissionTest(t, func(context.Context, ...string) (string, string, error) {
			t.Fatal("help invoked GitHub")
			return "", "", nil
		}, args...)
		if err != nil || !strings.Contains(output, "gh cao admission") {
			t.Fatalf("help: %v %s", err, output)
		}
	}
}

func TestAdmissionDisableDryRunDoesNotMutate(t *testing.T) {
	calls := 0
	report, _, err := executeAdmissionTest(t, func(_ context.Context, args ...string) (string, string, error) {
		calls++
		for _, arg := range args {
			if arg == "--method" {
				t.Fatal("dry-run attempted a mutation")
			}
		}
		return "[]", "", nil
	}, "disable", "--repo", "acme/project", "--dry-run", "--json")
	if err != nil || report.Applied || calls != 1 {
		t.Fatalf("disable dry-run: %+v %v calls=%d", report, err, calls)
	}
	for _, operation := range []string{"setup", "disable"} {
		_, _, err := executeAdmissionTest(t, func(context.Context, ...string) (string, string, error) {
			t.Fatal("removed --apply reached GitHub")
			return "", "", nil
		}, operation, "--apply")
		if err == nil {
			t.Fatal("obsolete --apply accepted")
		}
	}
}
