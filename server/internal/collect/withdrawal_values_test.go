package collect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/ingest"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func retainedValueLine(repository, metric string) string {
	return fmt.Sprintf(`{"schema_version":2,"kind":"operational_value","operational_value":{"repository":%q,"timestamp":%q,"campaign":"maintenance","campaign_id":"campaign:maintenance","value_id":%q,"value":7,"metric_role":"attainment","metric_name":"Accepted evidence","metric_direction":"higher-is-better","maturity_status":"mature"}}`+"\n",
		repository, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), metric)
}

func TestForgetSharedValuesErasesDerivedCopiesPreservesUnrelatedEvidence(t *testing.T) {
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	target := retainedValueLine("Octo/API", "worker.withdrawn")
	other := retainedValueLine("octo/api-extra", "worker.preserved")
	if err := os.WriteFile(lake.operationalValuesPath(), []byte(target+other), 0o600); err != nil {
		t.Fatal(err)
	}
	packetPath := filepath.Join(lake.ShardDirectory(), "independent-packets.jsonl")
	packet := `{"schema_version":2,"kind":"operational_value_evidence","operational_value_evidence":{"repository":"octo/api"}}` + "\n"
	if err := os.WriteFile(packetPath, []byte(packet), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{lake.RunsDirectory(), lake.RecordsDirectory(),
		filepath.Join(lake.RunsDirectory(), ".payloads"), filepath.Join(lake.RecordsDirectory(), ".payloads")} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
		for name, row := range map[string]string{
			"shared.jsonl": `{"kind":"record","collection":"operationalValues","record":{"repositoryId":"repository:octo%2Fapi","value":7}}`,
			"other.jsonl":  `{"kind":"record","collection":"operationalValues","record":{"repositoryId":"repository:octo%2Fapi-extra","value":7}}`,
			"packet.jsonl": `{"kind":"record","collection":"operationalValueEvidence","record":{"repositoryId":"repository:octo%2Fapi","facts":[]}}`,
		} {
			if err := os.WriteFile(filepath.Join(directory, name), []byte(row+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 2 {
		if err := lake.Forget("octo/api"); err != nil {
			t.Fatal(err)
		}
	}
	if content, err := os.ReadFile(lake.operationalValuesPath()); err != nil || string(content) != other {
		t.Fatalf("unrelated raw value changed: %q, %v", content, err)
	}
	if content, err := os.ReadFile(packetPath); err != nil || string(content) != packet { // #nosec G304 -- test-owned temporary packet.
		t.Fatalf("independent packet changed: %q, %v", content, err)
	}
	for _, directory := range []string{lake.RunsDirectory(), lake.RecordsDirectory(),
		filepath.Join(lake.RunsDirectory(), ".payloads"), filepath.Join(lake.RecordsDirectory(), ".payloads")} {
		if _, err := os.Stat(filepath.Join(directory, "shared.jsonl")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("withdrawn derived bytes retained: %v", err)
		}
		for _, name := range []string{"other.jsonl", "packet.jsonl"} {
			if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
				t.Fatalf("unrelated or independent evidence removed: %v", err)
			}
		}
	}
}

func TestEvidenceLockSerializesSharedWritersAndCancelsWait(t *testing.T) {
	lake := Lake{Directory: t.TempDir()}
	unlock, err := lake.lockEvidence(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if release, err := lake.lockEvidence(ctx); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		unlock()
		t.Fatalf("concurrent writer was admitted: %v", err)
	}
	unlock()
	release, err := lake.lockEvidence(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestForgetOwnershipPreflightPrecedesSharedValueMutation(t *testing.T) {
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	target := retainedRepository(t, lake, "octo/api")
	ambiguous := filepath.Join(lake.RunsDirectory(), lake.ShardPrefix("octo/api")+"unknown.jsonl")
	if err := os.WriteFile(ambiguous, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := retainedValueLine("octo/other", "worker.preserved")
	values := retainedValueLine("octo/api", "worker.withdrawn") + other
	if err := WriteFileAtomic(lake.operationalValuesPath(), []byte(values)); err != nil {
		t.Fatal(err)
	}
	if err := lake.ForgetContext(t.Context(), "octo/api"); err == nil {
		t.Fatal("ambiguous shard ownership allowed shared-value mutation")
	}
	if content, err := os.ReadFile(lake.operationalValuesPath()); err != nil || string(content) != values {
		t.Fatalf("failed ownership preflight changed shared values: %q, %v", content, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("failed ownership preflight deleted a repository shard: %v", err)
	}
	if err := os.Remove(ambiguous); err != nil {
		t.Fatal(err)
	}
	if err := lake.ForgetContext(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(lake.operationalValuesPath()); err != nil || string(content) != other {
		t.Fatalf("retry did not preserve unrelated values: %q, %v", content, err)
	}
}

func TestForgetWaitsForSharedWriterThenErasesItsStaleCopy(t *testing.T) {
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	unlock, err := lake.lockEvidence(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- lake.ForgetContext(t.Context(), "octo/api") }()
	other := retainedValueLine("octo/other", "worker.preserved")
	if err := WriteFileAtomic(lake.operationalValuesPath(),
		[]byte(retainedValueLine("octo/api", "worker.withdrawn")+other)); err != nil {
		unlock()
		t.Fatal(err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(lake.operationalValuesPath()); err != nil || string(content) != other {
		t.Fatalf("concurrent stale writer restored withdrawn values: %q, %v", content, err)
	}
}

type unavailableValueTokens struct{ calls int }

func (p *unavailableValueTokens) InstallationToken(context.Context, int64) (string, error) {
	p.calls++
	return "", errors.New("injected token acquisition failure")
}

func TestSharedValueErasureFailureRemainsRetryable(t *testing.T) {
	w := refreshWorker(t)
	path := w.Runner.Lake.operationalValuesPath()
	if err := os.WriteFile(path, []byte("invalid JSON\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Queue.Enqueue(t.Context(), Task{Repository: "octo/api", InstallationID: 7, Erase: true}); err != nil {
		t.Fatal(err)
	}
	if w.process(t.Context(), refreshLease(t, w.Queue)) {
		t.Fatal("failed shared erasure reported evidence changed")
	}
	if depth, err := w.Queue.Depth(t.Context()); err != nil || depth != 1 {
		t.Fatalf("erasure failure lost retry work: %d, %v", depth, err)
	}
	other := retainedValueLine("octo/other", "worker.preserved")
	if err := os.WriteFile(path, []byte(retainedValueLine("octo/api", "worker.withdrawn")+other), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.Runner.Lake.Forget("octo/api"); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != other { // #nosec G304 -- test-owned temporary shard.
		t.Fatalf("repair did not erase only target: %q, %v", content, err)
	}
}

func seedValueInventory(t *testing.T, lake Lake) {
	t.Helper()
	for path, content := range map[string]string{
		lake.ControlSettingsPath():       `{"campaigns":{"maintenance":{}},"policy_resolution":{"status":"available"}}`,
		lake.ControlPlaneInventoryPath(): `{"campaigns":[{"id":"maintenance"}],"workflows":[{"id":"maintenance"}]}`,
		lake.InventoryPath():             `{"campaigns":{"rows":[{"campaign":"maintenance"}]},"repositories":{"rows":[]},"workflows":{"rows":[]},"configuration-policy":{"rows":[]}}`,
	} {
		if err := WriteFileAtomic(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
}

// Exercises real webhook admission, worker erasure, Activity compaction and
// PostgreSQL replacement. No GitHub credential or network acquisition is used.
func TestWithdrawalSharedValuesEndToEnd(t *testing.T) {
	w := refreshWorker(t)
	ctx := t.Context()
	w.Projector.Data = integrationPostgres(t, ctx)
	w.Projector.Enrollment = w.Enrollment
	w.Projector.DatabaseQueriesPath = databaseQueries
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	w.Projector.CatalogRoot = root
	lake := w.Runner.Lake
	seedValueInventory(t, lake)
	other := retainedValueLine("octo/other", "worker.preserved")
	if err := os.WriteFile(lake.operationalValuesPath(), []byte(retainedValueLine("octo/api", "worker.withdrawn")+other), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.Enrollment.AddRepositories(ctx, 7, []string{"octo/api", "octo/other"}); err != nil {
		t.Fatal(err)
	}
	compactActivitySnapshot(t, lake)
	initial, err := w.Projector.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertValues := func(want int) {
		t.Helper()
		result, _, err := w.Projector.Data.ExecuteSQLPlan(ctx,
			[]query.Definition{{Name: "values", From: "$operationalValues"}}, []string{"values"})
		if err != nil || len(result["values"].Rows) != want {
			t.Fatalf("canonical values = %+v, %v; want %d", result, err, want)
		}
		if want == 1 && result["values"].Rows[0]["valueId"] != "worker.preserved" {
			t.Fatalf("wrong value survived withdrawal: %+v", result)
		}
	}
	assertValues(2)
	admitter := Admitter{Queue: w.Queue, Enrollment: w.Enrollment}
	lease := func(ctx context.Context) Lease {
		t.Helper()
		leases, err := w.Queue.Lease(ctx, "withdrawal-test", 1, 0)
		if err != nil || len(leases) != 1 {
			t.Fatalf("withdrawal lease: %+v, %v", leases, err)
		}
		return leases[0]
	}
	withdraw := func(repository string) {
		t.Helper()
		payload := []byte(fmt.Sprintf(`{"action":"removed","installation":{"id":7},"repositories_removed":[{"full_name":%q}]}`, repository))
		if _, err := admitter.AdmitDelivery(ctx, "installation_repositories", payload, repository, time.Hour); err != nil {
			t.Fatal(err)
		}
		scope := lease(ctx)
		if scope.Task.Scope == nil {
			t.Fatalf("withdrawal did not admit scope work: %+v", scope)
		}
		w.process(ctx, scope)
		if !w.process(ctx, lease(ctx)) {
			t.Fatal("withdrawal did not erase shared evidence")
		}
		if err := w.Projector.RequestProjection(ctx); err != nil {
			t.Fatal(err)
		}
	}
	withdraw("octo/api")
	if content, err := os.ReadFile(lake.operationalValuesPath()); err != nil || string(content) != other {
		t.Fatalf("withdrawal retained wrong raw bytes: %q, %v", content, err)
	}
	if _, _, _, err := ingest.ValidateManifest(lake.Directory); err == nil {
		t.Fatal("invalidated shared payload remained replayable")
	}
	if _, err := w.Projector.Project(ctx); err == nil {
		t.Fatal("invalidated snapshot reported successful replacement")
	}
	if pending, err := w.Projector.PendingProjection(ctx); err != nil || !pending {
		t.Fatalf("projection retry was lost: %t, %v", pending, err)
	}
	assertValues(2) // The previous committed snapshot serves until successful retry.
	compactActivitySnapshot(t, lake)
	tokens := &unavailableValueTokens{}
	w.Projector.Tokens = tokens
	next, err := w.Projector.Project(ctx)
	if err != nil || next.Revision <= initial.Revision {
		t.Fatalf("withdrawal retry did not publish a fresh revision: %+v, %v", next, err)
	}
	assertValues(1)
	if tokens.calls != 0 {
		t.Fatalf("core publication must not mint Value acquisition credentials: calls=%d", tokens.calls)
	}
	withdraw("octo/other")
	compactActivitySnapshot(t, lake)
	if _, err := w.Projector.Project(ctx); err != nil {
		t.Fatal(err)
	}
	assertValues(0)
	if tokens.calls != 0 {
		t.Fatalf("empty enrollment attempted acquisition: calls=%d", tokens.calls)
	}
	for _, directory := range []string{lake.ShardDirectory(), lake.RunsDirectory(), lake.RecordsDirectory()} {
		if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			content, err := os.ReadFile(path) // #nosec G304,G122 -- walk is confined to the test-owned temporary lake.
			if err == nil && (strings.Contains(string(content), "worker.withdrawn") ||
				strings.Contains(string(content), "worker.preserved")) {
				t.Fatalf("withdrawn value bytes retained in %s", path)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProjectionPrunesSharedValuesWithZeroEnrollmentWithoutAcquisition(t *testing.T) {
	w := refreshWorker(t)
	p := w.Projector
	p.Enrollment = w.Enrollment
	p.Tokens = &unavailableValueTokens{}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	p.CatalogRoot = root
	seedValueInventory(t, p.Lake)
	if err := os.WriteFile(p.Lake.operationalValuesPath(), []byte(retainedValueLine("octo/withdrawn", "worker.withdrawn")), 0o600); err != nil {
		t.Fatal(err)
	}
	compactActivitySnapshot(t, p.Lake)
	if err := p.prepareReplay(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ingest.ValidateManifest(p.Lake.Directory); err == nil {
		t.Fatal("erasure must invalidate the snapshot until the producer recompacts it")
	}
	compactActivitySnapshot(t, p.Lake)
	if _, _, _, err := ingest.ValidateManifest(p.Lake.Directory); err != nil {
		t.Fatalf("producer did not repair the erased snapshot: %v", err)
	}
	if content, err := os.ReadFile(p.Lake.operationalValuesPath()); !errors.Is(err, os.ErrNotExist) && (err != nil || len(content) > 0) {
		t.Fatalf("zero-enrollment refresh retained withdrawn bytes: %q, %v", content, err)
	}
}
