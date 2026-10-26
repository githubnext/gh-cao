package sessionarchive

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func openTestArchive(t *testing.T, c Config, publisher Publisher) *Archive {
	t.Helper()
	_ = testConverter(t)
	a, err := OpenWithPublisher(c, publisher)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func pinTestArchive(t *testing.T, a *Archive) *Snapshot {
	t.Helper()
	s, err := a.Pin(t.Context(), Scope{Namespace: a.config.Namespace, Repositories: a.config.Repositories})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestDuckDBArchiveIntegration(t *testing.T) {
	c := testConfig(t)
	a := openTestArchive(t, c, nil)
	p, q := testProjection(t, c, "123", "original"), testProjection(t, c, "456", "another run")
	for _, projection := range []Projection{p, q} {
		if err := a.Admit(t.Context(), projection); err != nil {
			t.Fatal(err)
		}
	}
	if a.Status().Generation != 0 || a.Status().StagedTraces != 2 {
		t.Fatal("admission falsely claimed analytical publication")
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(a.state.Manifest.Files) != 1 || len(a.state.Manifest.Files[0].Members) != 2 {
		t.Fatal("sessions were not packed into one partition file")
	}
	first := pinTestArchive(t, a)
	if first.Generation() != 1 {
		t.Fatal("snapshot did not pin publication generation")
	}
	if _, err := first.Timeline(t.Context(), TimelineRequest{"octo/api", "123", 1, 3}); !errors.Is(err, ErrLimit) {
		t.Fatalf("trace was silently truncated: %v", err)
	}
	if _, err := first.Timeline(t.Context(), TimelineRequest{"octo/api", "999", 1, 100}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown trace not distinguished: %v", err)
	}
	correction := testProjection(t, c, "123", "corrected")
	if err := a.Admit(t.Context(), correction); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	current := pinTestArchive(t, a)
	oldRows, err := first.Timeline(t.Context(), TimelineRequest{"octo/api", "123", 1, 100})
	if err != nil || !reflect.DeepEqual(oldRows, p.Events) {
		t.Fatalf("ordinary correction mutated pinned snapshot: %v", err)
	}
	newRows, err := current.Timeline(t.Context(), TimelineRequest{"octo/api", "123", 1, 100})
	if err != nil || !reflect.DeepEqual(newRows, correction.Events) {
		t.Fatalf("current snapshot did not select correction: %v", err)
	}
	summary, err := current.Summary(t.Context())
	if err != nil || len(summary) != 2 || summary[0].Events != 4 || summary[0].ProjectionID != correction.Version {
		t.Fatalf("packed superseded evidence inflated summary: %#v %v", summary, err)
	}
	if err := a.Admit(t.Context(), p); !errors.Is(err, ErrReplay) {
		t.Fatalf("old projection resurrected: %v", err)
	}
	if err := a.Withdraw(t.Context(), "octo/api", "123", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Summary(t.Context()); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("withdrawal did not override historical pin: %v", err)
	}
	if err := a.Admit(t.Context(), correction); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("withdrawn evidence admitted: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := openTestArchive(t, c, nil)
	snapshot := pinTestArchive(t, restarted)
	summary, err = snapshot.Summary(t.Context())
	if err != nil || len(summary) != 1 || summary[0].RunID != "456" {
		t.Fatalf("recovery lost withdrawal or active membership: %#v %v", summary, err)
	}
	if err := restarted.Admit(t.Context(), p); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("restart lost replay fence: %v", err)
	}
}

type recordingPublisher struct {
	fail      bool
	manifests []Manifest
	published Manifest
}

func (p *recordingPublisher) Publish(_ context.Context, manifest Manifest) error {
	p.manifests = append(p.manifests, cloneManifest(manifest))
	if p.fail {
		return errors.New("destination unavailable")
	}
	p.published = cloneManifest(manifest)
	return nil
}

func (p *recordingPublisher) ResolvePending(_ context.Context, manifest Manifest) (bool, error) {
	if p.fail {
		return false, errors.New("destination unavailable")
	}
	if p.published.Generation < manifest.Generation {
		return false, nil
	}
	if reflect.DeepEqual(p.published, manifest) {
		return true, nil
	}
	return false, ErrIntegrity
}

func TestDuckDBPublisherRecoveryIntegration(t *testing.T) {
	c := testConfig(t)
	publisher := &recordingPublisher{fail: true}
	a := openTestArchive(t, c, publisher)
	p := testProjection(t, c, "123", "full approved body")
	if err := a.Admit(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(t.Context()); err == nil {
		t.Fatal("failed destination claimed publication")
	}
	if a.Status().Generation != 0 || a.Status().StagedTraces != 1 || a.state.Pending == nil {
		t.Fatal("failed publication released durable evidence")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	publisher.fail = false
	a = openTestArchive(t, c, publisher)
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(publisher.manifests) != 2 || !reflect.DeepEqual(publisher.manifests[0], publisher.manifests[1]) {
		t.Fatal("restart retried a different candidate after ambiguous publication")
	}
	if a.Status().Generation != 1 || a.Status().StagedTraces != 0 {
		t.Fatal("destination acknowledgement not reflected in durable receipt")
	}
	files, err := diskFiles(a.root, 100)
	if err != nil {
		t.Fatal(err)
	}
	for path := range files {
		if strings.HasPrefix(path, "staging/") || strings.HasSuffix(path, ".parquet") {
			t.Fatalf("hosted mode retained full historical copy: %s", path)
		}
	}
	if _, err := a.Pin(t.Context(), Scope{c.Namespace, c.Repositories}); err == nil {
		t.Fatal("hosted mode silently fell back to local analytical query")
	}
	if err := a.Withdraw(t.Context(), "octo/api", "123", 1); err != nil {
		t.Fatal(err)
	}
	last := publisher.manifests[len(publisher.manifests)-1]
	if len(last.Members) != 0 || len(last.Fences) != 1 || !last.Fences[0].Withdrawn {
		t.Fatal("remote withdrawal did not carry exact attempt fence")
	}
}

func TestDuckDBArchiveRecoveryAndRetentionIntegration(t *testing.T) {
	c := testConfig(t)
	a := openTestArchive(t, c, nil)
	p := testProjection(t, c, "123", "recover staged body")
	if err := a.Admit(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(c); !errors.Is(err, ErrBusy) {
		t.Fatalf("multiple owners admitted: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openTestArchive(t, c, nil)
	if a.Status().StagedTraces != 1 {
		t.Fatal("staged evidence did not survive restart")
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	oldPath := a.state.Manifest.Files[0].Path
	future := time.Now().Add(c.Retention + time.Hour)
	a.now = func() time.Time { return future }
	if err := a.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if a.Status().ActiveTraces != 0 {
		t.Fatal("independent archive retention did not retire logical evidence")
	}
	if _, err := a.root.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unreferenced expired physical file retained after grace: %v", err)
	}
	if err := a.Admit(t.Context(), p); err != nil {
		t.Fatalf("idempotent retained fence receipt failed: %v", err)
	}
	if a.Status().StagedTraces != 0 {
		t.Fatal("expired old input resurrected body")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	_ = openTestArchive(t, c, nil)
}

func TestDuckDBArchiveCapacityAndIntegrityIntegration(t *testing.T) {
	c := testConfig(t)
	c.MaxEntries = 2
	a := openTestArchive(t, c, nil)
	p := testProjection(t, c, "123", "first")
	second := testProjection(t, c, "123", "second")
	third := testProjection(t, c, "123", "third")
	for _, p := range []Projection{p, second} {
		if err := a.Admit(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		if err := a.Flush(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Admit(t.Context(), third); !errors.Is(err, ErrCapacity) {
		t.Fatalf("fence budget silently discarded supersession history: %v", err)
	}
	if err := a.Admit(t.Context(), p); !errors.Is(err, ErrReplay) {
		t.Fatalf("capacity failure destroyed old-version replay fence: %v", err)
	}
	if a.Status().StagedTraces != 0 || a.Status().ActiveTraces != 1 {
		t.Fatal("failed capacity admission changed published evidence")
	}
	active := a.state.Manifest.Files[len(a.state.Manifest.Files)-1]
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(c.Directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	content, err := privateRead(root, active.Path, 128<<20)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)/2] ^= 1
	file, err := root.OpenFile(active.Path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(c); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("corrupt published file recovered as ready: %v", err)
	}
}

func TestDuckDBArchivePendingWithdrawalIntegration(t *testing.T) {
	c := testConfig(t)
	publisher := &recordingPublisher{}
	a := openTestArchive(t, c, publisher)
	p := testProjection(t, c, "123", "first")
	if err := a.Admit(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	publisher.fail = true
	p = testProjection(t, c, "123", "corrected")
	if err := a.Admit(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(t.Context()); err == nil {
		t.Fatal("outage did not retain pending publication")
	}
	if err := a.Withdraw(t.Context(), "octo/api", "123", 1); err == nil {
		t.Fatal("outage falsely acknowledged destination withdrawal")
	}
	if err := a.Admit(t.Context(), p); !errors.Is(err, ErrBusy) && !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("pending withdrawal failed to fence admission: %v", err)
	}
	if !a.state.Fences[memberKey(p.Member())].Withdrawn {
		t.Fatal("pending destination outage prevented durable local revocation")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	publisher.fail = false
	a = openTestArchive(t, c, publisher)
	pending := cloneManifest(*a.state.Pending)
	write := a.writeState
	a.writeState = func([]byte) error { return errors.New("synthetic state write failure") }
	if err := a.Flush(t.Context()); err == nil || a.state.Pending == nil || !reflect.DeepEqual(*a.state.Pending, pending) {
		t.Fatal("failed pending replacement did not preserve its durable candidate")
	}
	a.writeState = write
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	last := publisher.manifests[len(publisher.manifests)-1]
	if len(last.Members) != 0 || !last.Fences[0].Withdrawn {
		t.Fatal("recovery did not finish generation-bound withdrawal after ambiguous publication")
	}
	if err := a.Admit(t.Context(), p); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("restart lost permanent withdrawal")
	}
}

func TestDuckDBArchiveRecoveryOrphansAndGapsIntegration(t *testing.T) {
	c := testConfig(t)
	a := openTestArchive(t, c, nil)
	p := testProjection(t, c, "123", "approved")
	if err := a.Admit(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := durableWrite(a.root, "staging/interrupted.json", []byte("{}"), true); err != nil {
		t.Fatal(err)
	}
	if err := durableWrite(a.root, "generations/interrupted.json", []byte("{}"), true); err != nil {
		t.Fatal(err)
	}
	file, err := a.root.OpenFile("state.json.tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openTestArchive(t, c, nil)
	for _, path := range []string{"staging/interrupted.json", "generations/interrupted.json", "state.json.tmp"} {
		if _, err := a.root.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("uncommitted orphan survived owner recovery: %s %v", path, err)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(c.Directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.Remove("state.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(c); err == nil {
		t.Fatal("missing authority/replay state silently rebuilt from historical files")
	}
}
func TestDuckDBArchiveCaptureCacheIntegration(t *testing.T) {
	c := testConfig(t)
	a := openTestArchive(t, c, nil)
	logs, cache, target := t.TempDir(), t.TempDir(), t.TempDir()
	run := filepath.Join(logs, "run-123", "usage")
	if err := os.MkdirAll(run, 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"type":"session.format","data":{"version":1}}
{"type":"user.message","data":{"content":"private complete acquired message"}}
`
	if err := os.WriteFile(filepath.Join(run, "aw_session.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "aw_info.json"), []byte(`{"repository":"octo/api","run_id":123,"run_attempt":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	shard, err := json.Marshal(map[string]any{"kind": "run", "run": map[string]any{
		"repository": "octo/api", "run_id": 123, "run_attempt": 1, "status": "completed", "completed_at": time.Now().Format(time.RFC3339)}})
	if err != nil {
		t.Fatal(err)
	}
	shard = append(shard, '\n')
	if err := os.WriteFile(filepath.Join(cache, "octo-api.jsonl"), shard, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.PrepareCache(t.Context(), "octo/api", filepath.Join(cache, "octo-api"), filepath.Join(target, "octo-api")); err != nil {
		t.Fatal(err)
	}
	targetRoot, err := os.OpenRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = targetRoot.Close() }()
	filtered, err := privateRead(targetRoot, "octo-api.jsonl", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 0 {
		t.Fatal("uncaptured cached attempt suppressed authorized reacquisition")
	}
	if err := os.Remove(filepath.Join(run, "aw_info.json")); err != nil {
		t.Fatal(err)
	}
	if err := a.Capture(t.Context(), "octo/api", logs, filepath.Join(cache, "octo-api")); err == nil || a.Status().Gaps != 1 || a.Status().StagedTraces != 0 {
		t.Fatalf("artifact identity gap was not durable, explicit and excluded: %v %#v", err, a.Status())
	}
	if err := os.WriteFile(filepath.Join(run, "aw_info.json"), []byte(`{"repository":"octo/api","run_id":123,"run_attempt":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Capture(t.Context(), "octo/api", logs, filepath.Join(cache, "octo-api")); err != nil {
		t.Fatal(err)
	}
	if a.Status().StagedTraces != 1 || a.Status().Gaps != 0 {
		t.Fatal("acquisition did not durably stage complete approved session")
	}
	if err := a.PrepareCache(t.Context(), "octo/api", filepath.Join(cache, "octo-api"), filepath.Join(target, "octo-api")); err != nil {
		t.Fatal(err)
	}
	filtered, err = privateRead(targetRoot, "octo-api.jsonl", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(filtered) != string(shard) {
		t.Fatal("durably captured attempt remained acquisition-blocking")
	}
	key := memberKey(Member{Namespace: c.Namespace, Repository: "octo/api", RunID: "123", Attempt: 1})
	receipt := a.state.Records[key]
	for _, published := range []bool{false, true} {
		if published {
			if err := a.Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.Capture(t.Context(), "octo/api", t.TempDir(), filepath.Join(cache, "octo-api")); err != nil {
			t.Fatalf("cache-only revisit lost durable admission (published=%t): %v", published, err)
		}
		current := a.state.Records[key]
		if current.Gap != "" || current.Member != receipt.Member || current.CapturedAt != receipt.CapturedAt {
			t.Fatal("cache-only revisit changed the successful receipt")
		}
	}
	if err := a.WithdrawRepository(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Pin(t.Context(), Scope{c.Namespace, c.Repositories}); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("repository withdrawal did not revoke read access")
	}
}
