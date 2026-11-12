package collect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
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
