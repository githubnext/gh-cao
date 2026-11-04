package postgresx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestNativeAcquisitionAtomicCanonicalHandoff(t *testing.T) {
	first, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	second, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	identity := acquisitionIdentity()
	raw, err := os.ReadFile(filepath.Join("../../../dashboard/site/test/fixtures", "unified-telemetry-session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"runId":"303",`), []byte(`"runId":"303","repository":"octo/api",`), 1)
	session, _, err := collectnative.ProjectSession(t.Context(), raw, "usage/aw_session.jsonl", identity, collectnative.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	evidence := acquisitionEvidence(identity)
	evidence.Run["cao_unified_session"] = session
	evidence.Run["workflow_name"] = "Test"
	evidence.Run["created_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	evidence.Run["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	evidence.Run["status"], evidence.Run["conclusion"] = "completed", "success"
	expected := nativeOracleRun(t, evidence)
	a, b := first.Acquisition(), second.Acquisition()
	if err := a.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	claim, err := a.Claim(t.Context(), identity, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(t.Context(), claim, evidence); err != nil {
		t.Fatal(err)
	}
	_, digest, err := b.Pending(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/nonexistent")
	var workers sync.WaitGroup
	changed := make(chan bool, 2)
	for _, store := range []postgresx.Acquisition{a, b} {
		workers.Go(func() {
			published, err := store.Activate(t.Context(), postgresx.AcquisitionPublication{Identity: identity, Digest: digest, Epoch: claim.Epoch})
			if err != nil {
				t.Error(err)
			}
			changed <- published
		})
	}
	workers.Wait()
	if first, second := <-changed, <-changed; first == second {
		t.Fatal("concurrent consumers did not publish exactly one canonical revision")
	}
	if _, _, err := a.Pending(t.Context(), identity); err == nil {
		t.Fatal("canonical publication did not consume its safe-evidence digest")
	}
	contract, err := collectnative.CanonicalContract()
	if err != nil {
		t.Fatal(err)
	}
	selected := make([]query.SelectedField, 0, 3+len(contract.RoutingFields))
	selected = append(selected, query.SelectedField{Field: "id"}, query.SelectedField{Field: "inputTokens"},
		query.SelectedField{Field: "routingAggregateVersion"})
	for field := range contract.RoutingFields {
		selected = append(selected, query.SelectedField{Field: field})
	}
	results, _, err := second.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "runs", From: "$runs", Select: selected},
		{Name: "tools", From: "$tools"},
		{Name: "issues", From: "$issues"},
	}, []string{"runs", "tools", "issues"})
	if err != nil {
		t.Fatal(err)
	}
	runs := results["runs"].Rows
	if len(runs) != 1 || fmt.Sprint(runs[0]["inputTokens"]) != "12" || fmt.Sprint(runs[0]["routingAggregateVersion"]) != "1" {
		t.Fatalf("canonical/route fields unavailable: %+v", runs)
	}
	for field := range contract.RoutingFields {
		actual, _ := json.Marshal(runs[0][field])
		want, _ := json.Marshal(expected[field])
		if !routingValuesEqual(actual, want) {
			t.Errorf("native PostgreSQL routing %s: got=%s want=%s", field, actual, want)
		}
	}
	if len(results["tools"].Rows) == 0 || len(results["issues"].Rows) != 1 {
		t.Fatal("canonical consumers cannot resolve session facts")
	}

	for _, source := range results {
		if source.Metadata["completeness"] != "partial" {
			t.Fatal("native acquisition invented complete coverage")
		}
	}
}

func nativeOracleRun(t *testing.T, evidence collectnative.Evidence) map[string]any {
	t.Helper()
	content, _, err := evidence.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `
	import { adaptCachedGhAwJsonl } from './dashboard/site/src/data/adapters/gh-aw-logs.js';
	import { normalize } from './dashboard/site/src/data/normalize/index.js';
	let content=''; for await (const chunk of process.stdin) content += chunk;
	const evidence = JSON.parse(content);
	const batch = normalize(adaptCachedGhAwJsonl(JSON.stringify({schema_version:5,kind:'run',run:evidence.run})).observations);
	console.log(JSON.stringify(batch.runs[0]));`)
	command.Dir = "../../.."
	command.Stdin = bytes.NewReader(content)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("canonical Activity oracle failed: %v %s", err, output)
	}
	var result map[string]any
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func routingValuesEqual(actual, expected []byte) bool {
	if bytes.Equal(actual, expected) {
		return true
	}
	var a, b any
	if json.Unmarshal(actual, &a) != nil || json.Unmarshal(expected, &b) != nil {
		return false
	}
	left, numeric := a.(float64)
	right, expectedNumeric := b.(float64)
	// PostgreSQL sums recorded decimal credits exactly; the independent
	// JavaScript oracle can accumulate floating-point roundoff.
	return numeric && expectedNumeric && math.Abs(left-right) <= 1e-12*max(1, math.Abs(right))
}

func publishNative(t *testing.T, acquisition postgresx.Acquisition, evidence collectnative.Evidence) postgresx.AcquisitionPublication {
	t.Helper()
	claim, err := acquisition.Claim(t.Context(), evidence.Identity, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := acquisition.Publish(t.Context(), claim, evidence); err != nil {
		t.Fatal(err)
	}
	_, digest, err := acquisition.Pending(t.Context(), evidence.Identity)
	if err != nil {
		t.Fatal(err)
	}
	return postgresx.AcquisitionPublication{Identity: evidence.Identity, Digest: digest, Epoch: claim.Epoch}
}

func TestNativeAcquisitionActivationCASRecoveryAndAttemptPrecedence(t *testing.T) {
	store := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	acquisition := store.Acquisition()
	identity := acquisitionIdentity()
	if err := acquisition.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	original := publishNative(t, acquisition, acquisitionEvidence(identity))
	stale := original
	stale.Digest = strings.Repeat("0", 64)
	if _, err := acquisition.Activate(t.Context(), stale); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("stale digest activated: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := acquisition.Activate(ctx, original); err == nil {
		t.Fatal("canceled activation succeeded")
	}
	pending, err := acquisition.Publications(t.Context(), 1)
	if err != nil || len(pending) != 1 || !reflect.DeepEqual(pending[0], original) {
		t.Fatal("failed activation acknowledged its pending intent")
	}
	newer := identity
	newer.Attempt++
	next := publishNative(t, acquisition, acquisitionEvidence(newer))
	if changed, err := acquisition.Activate(t.Context(), next); err != nil || !changed {
		t.Fatalf("newer attempt failed: %t %v", changed, err)
	}
	before, err := store.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := acquisition.Activate(t.Context(), original); err != nil || changed {
		t.Fatalf("older attempt replaced winning canonical facts: %t %v", changed, err)
	}
	if count, err := acquisition.Replay(t.Context(), 1); err != nil || count != 0 {
		t.Fatalf("consumed intents replayed: %d %v", count, err)
	}
	after, err := store.State(t.Context())
	if err != nil || after.Revision != before.Revision {
		t.Fatal("older attempt/retry created another canonical revision")
	}
	for _, installation := range []int64{0, identity.InstallationID} {
		if err := acquisition.SetEnrollment(t.Context(), identity.Repository, installation); err != nil {
			t.Fatal(err)
		}
	}
	republished := publishNative(t, acquisition, acquisitionEvidence(newer))
	if republished.Digest != next.Digest || republished.Epoch <= next.Epoch {
		t.Fatal("test did not restore identical evidence under a new ownership epoch")
	}
	if _, err := acquisition.Activate(t.Context(), next); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("stale activation consumed its successor's identical evidence: %v", err)
	}
	if pending, err := acquisition.Publications(t.Context(), 1); err != nil || len(pending) != 1 {
		t.Fatal("stale activation acknowledged a successor")
	}
}
