package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func runAudit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(append([]string{"audit"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestAuditVerifyAndExportPostgres(t *testing.T) {
	t.Setenv("CAO_AUDIT_LOG", filepath.Join(t.TempDir(), "audit.jsonl"))
	store, _, _ := operationalCommandFixture(t)
	for _, actor := range []string{"octocat", "hubot"} {
		if _, err := store.AppendAudit(t.Context(), operational.AuditEvent{Event: "cli_command.admitted", Outcome: "success", Actor: actor}); err != nil {
			t.Fatal(err)
		}
	}
	_ = store.Close()

	output, err := runAudit(t, "verify")
	if err != nil {
		t.Fatal(err)
	}
	var head operational.AuditVerification
	if err := json.Unmarshal([]byte(output), &head); err != nil || head.LastSequence != 2 || len(head.LastHash) != 64 {
		t.Fatalf("verify output = %q, %v", output, err)
	}
	anchor := strconv.FormatInt(head.LastSequence, 10) + ":" + head.LastHash
	if _, err := runAudit(t, "verify", "--anchor", anchor); err != nil {
		t.Fatalf("anchored verify: %v", err)
	}
	if _, err := runAudit(t, "verify", "--anchor", "1:"+head.LastHash); err == nil {
		t.Fatal("mismatched anchor verified")
	}
	if _, err := runAudit(t, "verify", "--anchor", "nope"); err == nil {
		t.Fatal("malformed anchor accepted")
	}

	output, err = runAudit(t, "export")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	// Two admissions, then verified, verified, verify_failed from the runs above.
	if len(lines) != 5 || !strings.Contains(lines[2], `"audit.verified"`) || !strings.Contains(lines[4], `"audit.verify_failed"`) {
		t.Fatalf("export = %q", output)
	}
	output, err = runAudit(t, "export", "--actor", "hubot")
	if err != nil || strings.Count(output, "\n") != 1 || !strings.Contains(output, `"hubot"`) {
		t.Fatalf("filtered export = %q, %v", output, err)
	}
}

func TestParseAuditAnchors(t *testing.T) {
	anchors, err := parseAuditAnchors([]string{" 3:abc ", "10:def:ghi"})
	if err != nil || len(anchors) != 2 {
		t.Fatalf("anchors = %v, %v", anchors, err)
	}
	if anchors[0] != (operational.AuditAnchor{Sequence: 3, Hash: "abc"}) || anchors[1] != (operational.AuditAnchor{Sequence: 10, Hash: "def:ghi"}) {
		t.Fatalf("anchors = %+v", anchors)
	}
	if got, err := parseAuditAnchors(nil); err != nil || len(got) != 0 {
		t.Fatalf("empty = %v, %v", got, err)
	}
	for _, bad := range []string{"", "nope", "x:hash", ":hash", "5"} {
		if _, err := parseAuditAnchors([]string{bad}); err == nil {
			t.Fatalf("anchor %q accepted", bad)
		}
	}
}

func TestVerifyAttrs(t *testing.T) {
	attrs := verifyAttrs(operational.AuditVerification{Events: 4, FirstSequence: 1, LastSequence: 4, LastHash: "h", AnchorsChecked: 2, AnchorsExpired: 1})
	got := map[string]string{}
	for _, attr := range attrs {
		got[attr.Key] = attr.Value.String()
	}
	want := map[string]string{"events": "4", "first_sequence": "1", "last_sequence": "4", "last_hash": "h", "anchors_checked": "2", "anchors_expired": "1"}
	if len(got) != len(want) {
		t.Fatalf("attrs = %v", got)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("%s = %q, want %q", key, got[key], value)
		}
	}
}

func TestParseAuditAnchorBoundaries(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  operational.AuditAnchor
	}{
		{"\t9223372036854775807:hash\n", operational.AuditAnchor{Sequence: 9223372036854775807, Hash: "hash"}},
		{"-9223372036854775808:hash", operational.AuditAnchor{Sequence: -9223372036854775808, Hash: "hash"}},
		{"+1:a:b", operational.AuditAnchor{Sequence: 1, Hash: "a:b"}},
		{"0:", operational.AuditAnchor{Sequence: 0, Hash: ""}},
		{"1: hash", operational.AuditAnchor{Sequence: 1, Hash: " hash"}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseAuditAnchor(tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("parseAuditAnchor(%q) = %+v, %v; want %+v", tc.input, got, err, tc.want)
			}
		})
	}
	for _, input := range []string{"9223372036854775808:hash", "-9223372036854775809:hash", "1 :hash", "1.0:hash", "1", "\n"} {
		t.Run(input, func(t *testing.T) {
			got, err := parseAuditAnchor(input)
			wantErr := fmt.Sprintf("--anchor %q must be SEQUENCE:HASH", input)
			if got != (operational.AuditAnchor{}) || err == nil || err.Error() != wantErr {
				t.Fatalf("parseAuditAnchor(%q) = %+v, %v; want zero anchor and %q", input, got, err, wantErr)
			}
		})
	}
}

func TestParseAuditAnchorsDiscardsPartialResult(t *testing.T) {
	got, err := parseAuditAnchors([]string{"1:first", "invalid", "2:last"})
	if got != nil || err == nil || err.Error() != `--anchor "invalid" must be SEQUENCE:HASH` {
		t.Fatalf("parseAuditAnchors = %+v, %v; want nil and invalid-anchor error", got, err)
	}
}

func TestExportAuditPaginatesInOrder(t *testing.T) {
	store, _, _ := operationalCommandFixture(t)
	defer func() { _ = store.Close() }()
	total := operational.AuditListLimit + 3
	for range total {
		if _, err := store.AppendAudit(t.Context(), operational.AuditEvent{Event: "cli_command.admitted", Outcome: "success", Actor: "octocat"}); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := exportAudit(t.Context(), store, operational.AuditFilter{}, &out); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&out)
	var last int64
	count := 0
	for decoder.More() {
		var event operational.AuditEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event.Sequence <= last {
			t.Fatalf("sequence %d after %d", event.Sequence, last)
		}
		last = event.Sequence
		count++
	}
	if count != total {
		t.Fatalf("exported %d, want %d", count, total)
	}

	out.Reset()
	if err := exportAudit(t.Context(), store, operational.AuditFilter{AfterSequence: last}, &out); err != nil || out.Len() != 0 {
		t.Fatalf("export after head = %q, %v", out.String(), err)
	}
}
