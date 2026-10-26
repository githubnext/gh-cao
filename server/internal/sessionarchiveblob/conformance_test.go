package sessionarchiveblob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
	"github.com/githubnext/gh-cao/server/internal/simulator"
)

func realConverter(t *testing.T) *sessionarchive.Converter {
	t.Helper()
	path := os.Getenv("DUCKDB_PATH")
	if path == "" {
		path = "duckdb"
	}
	if _, err := exec.LookPath(path); err != nil &&
		os.Getenv("CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB") != "1" && os.Getenv("CAO_SESSION_ARCHIVE_REQUIRE_AZURE") != "1" {
		t.Skip("pinned DuckDB CLI unavailable; require it with CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB=1")
	}
	converter, err := sessionarchive.NewConverter(sessionarchive.ConverterConfig{DuckDBPath: path})
	if err != nil {
		t.Fatal(err)
	}
	return converter
}

func approvedProjection(t *testing.T, namespace, repository, runID, body string) sessionarchive.Projection {
	t.Helper()
	c := adxtrace.Config{Enabled: true, DurableStorage: true, Directory: t.TempDir(), Repositories: []string{repository}, Mode: "full",
		Policy: adxtrace.Policy{Version: "synthetic-approved-v1", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{"session.format": {"version"}, "session.collection": {"complete"}, "user.message": {"content"}}}}
	artifact := `{"type":"session.format","data":{"version":1}}` + "\n" +
		`{"type":"session.collection","data":{"complete":true}}` + "\n" +
		`{"type":"user.message","data":{"content":` + literal(body) + "}}\n"
	p, err := sessionarchive.Project(c, namespace, repository, runID, "2026-10-25", 1, []byte(artifact), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type interruptedPublisher struct {
	*Archive
	interrupt, committed bool
	protocol             *blobProtocol
}

func (p *interruptedPublisher) Publish(ctx context.Context, manifest sessionarchive.Manifest) error {
	if p.interrupt && !p.committed {
		for _, file := range manifest.Files {
			if file.Members[0].Repository == "octo/api" {
				p.protocol.mu.Lock()
				p.protocol.loseKey = objectKey(file)
				p.protocol.mu.Unlock()
			}
		}
	}
	if err := p.Archive.Publish(ctx, manifest); err != nil {
		return err
	}
	if p.interrupt {
		return errors.New("synthetic lost publication acknowledgement")
	}
	return nil
}

func TestDuckDBBlobPendingWithdrawalIntegration(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("committed=%t", committed), func(t *testing.T) {
			converter := realConverter(t)
			b, protocol, _ := newProtocol(t)
			b.converter = converter
			b.config.RepositoryIDs = []string{"octo/api", "octo/other"}
			c := sessionarchive.Config{Enabled: true, DurableStorage: true, Directory: t.TempDir(),
				Namespace: "private", Repositories: b.config.RepositoryIDs, DuckDBPath: os.Getenv("DUCKDB_PATH"),
				Policy: adxtrace.Policy{Version: "synthetic-approved-v1", ContentApproved: true, RightsApproved: true,
					Events: map[string][]string{"session.format": {"version"}, "session.collection": {"complete"}, "user.message": {"content"}}}}
			if err := os.Chmod(c.Directory, 0o700); err != nil { // #nosec G302 -- Private archive directories require owner traversal.
				t.Fatal(err)
			}
			publisher := &interruptedPublisher{Archive: b, interrupt: true, committed: committed, protocol: protocol}
			open := func() *sessionarchive.Archive {
				a, err := sessionarchive.OpenWithPublisher(c, publisher)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = a.Close() })
				return a
			}
			a := open()
			first := approvedProjection(t, "private", "octo/api", "123", "withdraw this trace")
			other := approvedProjection(t, "private", "octo/other", "456", "retain this trace")
			for _, p := range []sessionarchive.Projection{first, other} {
				if err := a.Admit(t.Context(), p); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.Flush(t.Context()); err == nil || a.Status().StagedTraces != 2 {
				t.Fatal("interrupted publication did not retain local staging")
			}
			if !committed && len(b.state.Pending) != 2 {
				t.Fatal("interrupted upload did not retain remote object inventory")
			}
			if err := b.WithdrawRepository(t.Context(), "octo/api"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := a.WithdrawRepository(ctx, "octo/api"); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled local withdrawal lost its durable intent: %v", err)
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			publisher.interrupt = false
			a = open()
			protocol.mu.Lock()
			protocol.corruptKey = "active.json"
			protocol.mu.Unlock()
			if err := a.Flush(t.Context()); err == nil || a.Status().StagedTraces != 2 {
				t.Fatal("unresolved durable receipt discarded pending evidence")
			}
			protocol.mu.Lock()
			protocol.corruptKey = ""
			protocol.mu.Unlock()
			if err := a.Flush(t.Context()); err != nil {
				t.Fatalf("withdrawal wedged the recovered pending publication: %v", err)
			}
			if a.Status().StagedTraces != 0 || a.Status().ActiveTraces != 1 ||
				!reflect.DeepEqual(b.state.Manifest.Members, []sessionarchive.Member{other.Member()}) {
				t.Fatal("pending reconciliation lost surviving evidence or resurrected withdrawal")
			}
			if err := a.Admit(t.Context(), first); !errors.Is(err, sessionarchive.ErrWithdrawn) {
				t.Fatalf("recovery lost repository replay fencing: %v", err)
			}
			following := approvedProjection(t, "private", "octo/other", "789", "new admission after withdrawal")
			if err := a.Admit(t.Context(), following); err != nil {
				t.Fatalf("withdrawn pending publication blocked another repository: %v", err)
			}
			if err := a.Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(b.state.Manifest.Members) != 2 {
				t.Fatal("surviving repository could not continue publication")
			}
		})
	}
}

func TestDuckDBBlobPublicationIntegration(t *testing.T) {
	converter := realConverter(t)
	a, s, _ := newProtocol(t)
	a.converter = converter
	first := approvedProjection(t, "private", "octo/api", "123", "first approved content")
	other := approvedProjection(t, "private", "octo/api", "456", "other approved content")
	file, err := converter.Write(t.Context(), filepath.Join(t.TempDir(), "packed.parquet"), []sessionarchive.Projection{first, other})
	if err != nil {
		t.Fatal(err)
	}
	manifest := sessionarchive.Manifest{Version: 1, Generation: 1, CreatedAt: time.Now().UTC(),
		Files: []sessionarchive.File{file}, Members: []sessionarchive.Member{first.Member(), other.Member()}}
	if err := a.Publish(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	if len(file.Members) != 2 || file.Rows != 6 || !strings.Contains(objectKey(file), "/2026-10-25/") {
		t.Fatal("actual file was not packed or day-partitioned")
	}
	content, err := os.ReadFile(file.Path)
	if err != nil || !reflect.DeepEqual(content, s.objects[objectKey(file)]) {
		t.Fatal("actual Parquet object differed from verified approved bytes")
	}
	if err := os.Remove(file.Path); err != nil {
		t.Fatal(err)
	}
	// Acknowledged local bodies may be removed; subsequent complete manifests
	// must use durable Blob receipt metadata rather than reopen deleted paths.
	manifest.Generation++
	manifest.CreatedAt = time.Now().UTC()
	if err := a.Publish(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	// Shared durable capture/spool really invokes the hosted publisher.
	c := sessionarchive.Config{Enabled: true, DurableStorage: true, Directory: t.TempDir(),
		Namespace: "private", Repositories: []string{"octo/api"}, DuckDBPath: os.Getenv("DUCKDB_PATH"),
		Policy: adxtrace.Policy{Version: "synthetic-approved-v1", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{"session.format": {"version"}, "session.collection": {"complete"}, "user.message": {"content"}}}}
	b, _, _ := newProtocol(t)
	b.converter = converter
	if err := os.Chmod(c.Directory, 0o700); err != nil { // #nosec G302 -- Private POSIX directory needs owner execute permission.
		t.Fatal(err)
	}
	staging, err := sessionarchive.OpenWithPublisher(c, b)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = staging.Close() }()
	if err := staging.Admit(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := staging.Admit(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if err := staging.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if staging.Status().Generation != 1 || staging.Status().StagedTraces != 0 || b.Status().Generation != 1 {
		t.Fatal("shared durable admission was acknowledged before hosted generation")
	}
	correction := approvedProjection(t, "private", "octo/api", "123", "corrected approved content")
	if err := staging.Admit(t.Context(), correction); err != nil {
		t.Fatal(err)
	}
	if err := staging.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b.state.Manifest.Generation != 2 || len(b.state.Manifest.Members) != 2 ||
		!slices.Contains(b.state.Manifest.Members, correction.Member()) {
		t.Fatal("shared observed-version fence rejected current correction")
	}
	if err := staging.Withdraw(t.Context(), "octo/api", "123", 1); err != nil {
		t.Fatal(err)
	}
	if b.state.Manifest.Generation != 3 || len(b.state.Manifest.Members) != 1 ||
		b.state.Manifest.Members[0] != other.Member() || !errors.Is(staging.Admit(t.Context(), first), sessionarchive.ErrWithdrawn) {
		t.Fatal("shared and hosted permanent withdrawal state diverged")
	}
}

// TestKustoArchiveMembership executes the production membership/join template
// in Microsoft's real engine. Synthetic datatable input isolates KQL semantics;
// this does not assert Azure external storage authentication or pruning.
func TestKustoArchiveMembership(t *testing.T) {
	endpoint := os.Getenv("CAO_KUSTO_EMULATOR_URL")
	if endpoint == "" {
		if os.Getenv("CAO_SESSION_ARCHIVE_REQUIRE_KUSTO") == "1" {
			t.Fatal("required real Kusto engine is unavailable")
		}
		t.Skip("requires Microsoft's real Kusto emulator")
	}
	database := "ArchiveAcceptance" + strings.ReplaceAll(uuid.NewString(), "-", "")
	engine, err := simulator.NewKusto(endpoint, database, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.Execute(t.Context(), "/v1/rest/mgmt",
		".create database "+database+` persist (@"/kustodata/`+database+`/md",@"/kustodata/`+database+`/data")`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := engine.Execute(ctx, "/v1/rest/mgmt", ".drop database "+database); err != nil {
			t.Error(err)
		}
	})
	manifest := fixture(t, 1, "selected")
	member, file := manifest.Members[0], manifest.Files[0]
	rows := make([]string, 0, 7)
	for _, item := range []struct {
		namespace, repository, version, object string
		attempt, sequence                      int64
	}{
		{member.Namespace, member.Repository, member.Version, file.Digest, 1, 0},
		{member.Namespace, member.Repository, member.Version, file.Digest, 1, 1},
		{member.Namespace, member.Repository, digest([]byte("superseded")), file.Digest, 1, 0},
		{member.Namespace, member.Repository, member.Version, file.Digest, 2, 0},
		{"other-namespace", member.Repository, member.Version, file.Digest, 1, 0},
		{member.Namespace, "other/repository", member.Version, file.Digest, 1, 0},
		{member.Namespace, member.Repository, member.Version, digest([]byte("unselected-object")), 1, 0},
	} {
		rows = append(rows, fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%d,%s,%s,%d",
			literal(digest([]byte(member.Namespace))), literal(digest([]byte(member.Repository))), literal("undated"), literal(item.object),
			literal(item.namespace), literal(item.repository), literal(member.RunID), item.attempt, literal(item.version), literal(member.ID), item.sequence))
	}
	source := "datatable(ArchiveNamespace:string,ArchiveRepository:string,ArchiveDay:string,ArchiveObject:string," +
		"Namespace:string,Repository:string,RunId:string,RunAttempt:long,ProjectionId:string,TraceId:string,Sequence:long)[" + strings.Join(rows, ",") + "]"
	query := strings.Replace(membershipKQL("Events", manifest.Files, manifest.Members),
		`external_table("Events")`, source, 1) + "| summarize Events=count(),First=min(Sequence),Last=max(Sequence)"
	content, err := engine.Execute(t.Context(), "/v1/rest/query", query)
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Tables []queryTable }
	if json.Unmarshal(content, &response) != nil || len(response.Tables) == 0 ||
		len(response.Tables[0].Rows) != 1 || len(response.Tables[0].Rows[0]) != 3 {
		t.Fatal("real Kusto did not enforce exact selected generation/projection membership")
	}
	actual := make([]int64, 0, 3)
	for _, cell := range response.Tables[0].Rows[0] {
		var value int64
		if json.Unmarshal(cell, &value) != nil {
			t.Fatal("real Kusto returned invalid count/ordering evidence")
		}
		actual = append(actual, value)
	}
	if !slices.Equal(actual, []int64{2, 0, 1}) {
		t.Fatal("real Kusto did not enforce exact selected generation/projection membership")
	}
	verification := strings.Replace(fileVerificationKQL("Events", file), `external_table("Events")`,
		kustoEventSource(fixtureEvents(member), file), 1)
	content, err = engine.Execute(t.Context(), "/v1/rest/query", verification)
	if err != nil {
		t.Fatal(err)
	}
	response.Tables = nil
	if json.Unmarshal(content, &response) != nil || len(response.Tables) == 0 ||
		len(response.Tables[0].Rows) != 1 || len(response.Tables[0].Rows[0]) != 11 {
		t.Fatal("real Kusto rejected production content/sequence verification")
	}
	expected := []int64{member.Events, member.Events, 0, member.Events - 1, 0}
	for i, cell := range response.Tables[0].Rows[0][6:] {
		var value int64
		if json.Unmarshal(cell, &value) != nil || value != expected[i] {
			t.Fatal("real Kusto failed production content/sequence verification")
		}
	}
}

func kustoEventSource(events []sessionarchive.Event, file sessionarchive.File) string {
	values := make([]string, 0, len(events))
	for _, event := range events {
		value, _ := json.Marshal(event)
		values = append(values, "dynamic("+string(value)+")")
	}
	columns := strings.Split(EventSchema(), ",")
	project := make([]string, 0, len(columns))
	for _, column := range columns {
		name, kind, _ := strings.Cut(column, ":")
		expression := "Event[" + literal(name) + "]"
		switch kind {
		case "long":
			expression = "tolong(" + expression + ")"
		case "string":
			expression = "tostring(" + expression + ")"
		}
		project = append(project, name+"="+expression)
	}
	return "(datatable(Event:dynamic)[" + strings.Join(values, ",") + "] | project " + strings.Join(project, ",") +
		" | extend ArchiveNamespace=" + literal(digest([]byte(file.Members[0].Namespace))) +
		",ArchiveRepository=" + literal(digest([]byte(file.Members[0].Repository))) +
		",ArchiveDay=" + literal(fileDay(file)) + ",ArchiveObject=" + literal(file.Digest) + ")"
}

// TestAzureArchiveConformance is deliberately environment-dependent. Required
// mode fails (never skips) when any evidence is absent. Only a dedicated empty
// conformance prefix/table may be used; the test never provisions resources.
func TestAzureArchiveConformance(t *testing.T) {
	if os.Getenv("CAO_SESSION_ARCHIVE_REQUIRE_AZURE") != "1" {
		t.Skip("no live Azure acceptance claimed; require with CAO_SESSION_ARCHIVE_REQUIRE_AZURE=1")
	}
	if os.Getenv("CAO_SESSION_ARCHIVE_AZURE_ALLOW_WRITES") != "1" {
		t.Fatal("explicit dedicated-destination conformance write approval is required")
	}
	config, err := FromEnv(os.Getenv)
	if err != nil || !config.Enabled || !strings.HasPrefix(config.Prefix, "conformance-") || len(config.RepositoryIDs) != 1 {
		t.Fatal("requires enabled dedicated conformance-* Blob prefix, provisioned partitioned external table and one exact synthetic repository")
	}
	if config.RetirementGrace != 5*time.Minute {
		t.Fatal("dedicated conformance requires RETIREMENT_GRACE=5m and a test timeout of at least 15m")
	}
	// This test is the explicit destination acceptance procedure, not runtime
	// self-certification. The resulting binding is emitted only after all cases.
	config.Acceptance = config.AcceptanceBinding()
	converter := realConverter(t)
	a, err := New(t.Context(), config, converter)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	if a.Status().Generation != 0 || a.active.Revisions != 0 {
		t.Fatal("conformance destination is not empty; do not reuse production or prior test state")
	}
	if competing, err := New(t.Context(), config, converter); err == nil {
		_ = competing.Close()
		t.Fatal("live overlapping owner acquired the same active-pointer lease")
	} else if !errors.Is(err, ErrLease) {
		t.Fatal("live overlap did not produce the expected lease conflict")
	}
	repository := config.RepositoryIDs[0]
	p := approvedProjection(t, config.Namespace, repository, "123", "synthetic original")
	q := approvedProjection(t, config.Namespace, repository, "456", "synthetic other")
	write := func(projections ...sessionarchive.Projection) sessionarchive.File {
		file, err := converter.Write(t.Context(), filepath.Join(t.TempDir(), "packed.parquet"), projections)
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	original := write(p, q)
	first := sessionarchive.Manifest{Version: 1, Generation: 1, CreatedAt: time.Now().UTC(),
		Files: []sessionarchive.File{original}, Members: []sessionarchive.Member{p.Member(), q.Member()}}
	if err := a.Publish(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(t.Context(), first); err != nil {
		t.Fatal("live immutable retry failed")
	}
	scope := sessionarchive.Scope{Namespace: config.Namespace, Repositories: []string{repository}}
	generation, actual, err := a.Timeline(t.Context(), scope, sessionarchive.TimelineRequest{Repository: repository, RunID: "123", Attempt: 1, Limit: 100})
	if err != nil || generation != 1 || !reflect.DeepEqual(actual, p.Events) {
		t.Fatal("live ADX failed complete approved-event reconstruction")
	}
	correction := approvedProjection(t, config.Namespace, repository, "123", "synthetic corrected")
	corrected := write(correction)
	second := sessionarchive.Manifest{Version: 1, Generation: 2, CreatedAt: time.Now().UTC(),
		Files: []sessionarchive.File{original, corrected}, Members: []sessionarchive.Member{correction.Member(), q.Member()}}
	oldCtx, _, _, _, oldFinish, err := a.pin(t.Context(), scope, repository, "123", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if oldCtx.Err() != nil || oldFinish() != nil {
		t.Fatal("live ordinary correction invalidated a pinned reader")
	}
	generation, summaries, err := a.Summaries(t.Context(), scope, repository, 100)
	if err != nil || generation != 2 || len(summaries) != 2 {
		t.Fatal("live packed superseded rows inflated or corrupted summary")
	}
	for _, summary := range summaries {
		if summary.Events != 3 || (summary.RunID == "123" && summary.ProjectionID != correction.Version) {
			t.Fatal("live ADX selected an unapproved projection or duplicated events")
		}
	}
	stale := first
	stale.Generation = 3
	if !errors.Is(a.Publish(t.Context(), stale), ErrWithdrawn) {
		t.Fatal("live supersession fence failed")
	}
	third := sessionarchive.Manifest{Version: 1, Generation: 3, CreatedAt: time.Now().UTC(),
		Files: []sessionarchive.File{original}, Members: []sessionarchive.Member{q.Member()}}
	withdrawCtx, _, _, _, withdrawFinish, err := a.pin(t.Context(), scope, repository, "123", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(t.Context(), third); err != nil {
		t.Fatal(err)
	}
	if withdrawCtx.Err() == nil || !errors.Is(withdrawFinish(), ErrWithdrawn) {
		t.Fatal("live withdrawal did not cancel and deny pinned results")
	}
	if _, _, err := a.Timeline(t.Context(), scope, sessionarchive.TimelineRequest{Repository: repository, RunID: "123", Attempt: 1, Limit: 100}); !errors.Is(err, sessionarchive.ErrNotFound) {
		t.Fatal("live withdrawn projection still queryable")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(t.Context(), config, converter)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	if a.Status().Generation != 3 || !errors.Is(a.Publish(t.Context(), sessionarchive.Manifest{
		Version: 1, Generation: 4, CreatedAt: time.Now().UTC(), Files: []sessionarchive.File{corrected},
		Members: []sessionarchive.Member{correction.Member()}}), ErrWithdrawn) {
		t.Fatal("live restart lost withdrawal fence")
	}
	if err := a.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := a.blob.verify(t.Context(), objectKey(corrected), corrected.Digest, corrected.Bytes); err != nil {
		t.Fatal("live retirement deleted an object before the persisted recovery grace")
	}
	deadline := a.active.ReadersUntil.Add(config.RetirementGrace + time.Second)
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-t.Context().Done():
		t.Fatal("live retirement acceptance did not reach the real persisted grace")
	case <-timer.C:
	}
	if err := a.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := a.blob.verify(t.Context(), objectKey(corrected), corrected.Digest, corrected.Bytes); !isStatus(err, http.StatusNotFound) {
		t.Fatal("live eligible retired object was not physically deleted")
	}
	if err := a.blob.verify(t.Context(), objectKey(original), original.Digest, original.Bytes); err != nil {
		t.Fatal("live retirement deleted a currently selected packed file")
	}
	t.Log("Azure destination conformance passed; explicit runtime acceptance binding:", config.Acceptance)
}
