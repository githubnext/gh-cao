package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAdmissionOrganizationAndEnterpriseScopes(t *testing.T) {
	for _, enterprise := range []bool{false, true} {
		_, output, _ := testCommand(t, nil, nil)
		var calls [][]string
		gh := func(_ context.Context, args ...string) (string, string, error) {
			calls = append(calls, args)
			if args[3] == "graphql" {
				joined := strings.Join(args, " ")
				if !strings.Contains(joined, "enterprise=company") || !strings.Contains(joined, "$endCursor") ||
					!strings.Contains(joined, "--paginate") {
					t.Fatal("enterprise discovery is not bounded/paginated")
				}
				return `["beta"]` + "\n" + `["alpha"]`, "", nil
			}
			if strings.Contains(args[3], "?per_page") {
				return "[]", "", nil
			}
			if args[len(args)-1] != "visibility=all" {
				t.Fatalf("organization rollout omitted all-repository visibility: %v", args)
			}
			return "", "", nil
		}
		root := newCommand("test", nil, gh, nil)
		root.SetOut(output)
		root.SetErr(output)
		args := []string{"setup", "admission", "--url", "https://cao.example/api/admission", "--json"}
		if enterprise {
			args = append(args, "--enterprise", "company")
		} else {
			args = append(args, "--org", "beta", "--org", "alpha", "--org", "ALPHA")
		}
		root.SetArgs(args)
		if err := root.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		var report struct {
			Results []admissionCLIReport `json:"results"`
		}
		if err := json.Unmarshal(output.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Results) != 2 || report.Results[0].Repository != "alpha" ||
			report.Results[1].Repository != "beta" || !report.Results[1].Applied ||
			report.Results[0].Scope != "organization" {
			t.Fatalf("wrong scope: %s", output)
		}
		offset := 0
		if enterprise {
			offset = 1
		}
		if len(calls) != offset+8 || !strings.Contains(calls[offset+1][3], "orgs/beta/actions/variables?") ||
			calls[offset+2][3] != "orgs/alpha/actions/variables" {
			t.Fatalf("writes preceded fleet preflight: %v", calls)
		}
	}
}

func TestAdmissionCAOPolicyScope(t *testing.T) {
	for _, exact := range []bool{true, false} {
		t.Run(map[bool]string{true: "exact", false: "owner-wide"}[exact], func(t *testing.T) {
			nativeSetupTestDirectory(t)
			policy := nativeSetupTestPolicy()
			scope := map[string]any{"allowed-owners": []string{"org"}}
			if exact {
				scope["allowed-repositories"] = []string{"org/two", "org/one"}
			}
			nativeSetupMap(policy["control-plane"])["scope"] = scope
			nativeSetupTestSave(t, policy)
			discovered := false
			var reads []string
			gh := func(_ context.Context, args ...string) (string, string, error) {
				if strings.Contains(args[3], "/repos?type=all") {
					discovered = true
					return `["org/two","org/one"]`, "", nil
				}
				for _, arg := range args {
					if arg == "--method" {
						t.Fatal("fleet dry-run mutated variables")
					}
				}
				reads = append(reads, args[3])
				return "[]", "", nil
			}
			root, output, _ := testCommand(t, gh, nil)
			root.SetArgs([]string{"setup", "admission", "--all-repos", "--url", "https://cao.example/api/admission", "--dry-run", "--json"})
			if err := root.ExecuteContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if discovered == exact || len(reads) != 2 || !strings.Contains(reads[0], "repos/org/one/") ||
				!strings.Contains(output.String(), `"applied": false`) {
				t.Fatalf("wrong policy selection: discovered=%t reads=%v output=%s", discovered, reads, output)
			}
		})
	}
}

func TestAdmissionFleetPreflightAndPartialFailure(t *testing.T) {
	for _, failPreflight := range []bool{true, false} {
		writes := 0
		gh := func(_ context.Context, args ...string) (string, string, error) {
			if strings.Contains(args[3], "?per_page") {
				if failPreflight && strings.Contains(args[3], "orgs/beta/") {
					return "", "sensitive diagnostic", errors.New("synthetic failure")
				}
				return "[]", "", nil
			}
			writes++
			if strings.Contains(args[3], "orgs/beta/") {
				return "", "sensitive diagnostic", errors.New("synthetic failure")
			}
			return "", "", nil
		}
		root, output, _ := testCommand(t, gh, nil)
		root.SetArgs([]string{"setup", "admission", "--org", "alpha", "--org", "beta", "--url", "https://cao.example/api/admission", "--json"})
		err := root.ExecuteContext(t.Context())
		if err == nil || strings.Contains(err.Error()+output.String(), "sensitive diagnostic") {
			t.Fatalf("failure not safely surfaced: %v %s", err, output)
		}
		if failPreflight {
			if writes != 0 || !strings.Contains(err.Error(), "no changes applied") {
				t.Fatalf("preflight mutated: %d %v", writes, err)
			}
		} else if writes != 4 || !strings.Contains(output.String(), `"applied": true`) ||
			!strings.Contains(err.Error(), "earlier settings may have changed") {
			t.Fatalf("partial progress lost or auto-retried: writes=%d err=%v output=%s", writes, err, output)
		}
	}
}

func TestAdmissionScopeValidation(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--repo", "org/one", "--org", "org"}, {"--org", "../bad"},
		{"--enterprise", "bad/enterprise"}, {"--all-repos", "--org", "org"},
	} {
		root, _, _ := testCommand(t, nil, nil)
		root.SetArgs(append([]string{"admission", "disable", "--dry-run"}, args...))
		if err := root.ExecuteContext(t.Context()); err == nil {
			t.Fatalf("invalid scope accepted: %v", args)
		}
	}
	for _, value := range []string{"null", `["alpha",null]`, "{}", ""} {
		if _, err := decodeAdmissionNames(value); err == nil {
			t.Fatalf("invalid discovery accepted: %s", value)
		}
	}
}
