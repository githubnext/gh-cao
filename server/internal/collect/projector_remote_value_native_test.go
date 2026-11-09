package collect

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1" // #nosec G505 -- test fixture uses Git's SHA-1 object format.
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func testFileDietSHA(character string) string { return strings.Repeat(character, 40) }

func TestNativeFileDietBlobBatchIsBoundedAndVerified(t *testing.T) {
	content := []byte("first\nsecond\n")
	sum := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(content))), content...)) // #nosec G401 -- validates Git's SHA-1 blob object identifier.
	sha := fmt.Sprintf("%x", sum)
	packet := append([]byte(fmt.Sprintf("%s blob %d\n", sha, len(content))), content...)
	packet = append(packet, '\n')
	measured, err := parseFileDietBlobMeasurements(packet, []string{sha})
	if err != nil || measured[sha] != [2]int{2, len(content)} {
		t.Fatalf("valid bounded Git blob batch rejected: %v %v", measured, err)
	}
	for _, invalid := range [][]byte{
		append(bytes.Clone(packet), '\n'),
		bytes.Replace(bytes.Clone(packet), []byte("second"), []byte("tamper"), 1),
		bytes.Replace(bytes.Clone(packet), []byte(" blob "), []byte(" tree "), 1),
		packet[:len(packet)-1],
	} {
		if _, err := parseFileDietBlobMeasurements(invalid, []string{sha}); err == nil {
			t.Fatal("invalid immutable Git blob batch was accepted")
		}
	}
	if _, err := parseFileDietBlobMeasurements(packet, []string{testFileDietSHA("f")}); err == nil {
		t.Fatal("unreviewed immutable Git blob identity was accepted")
	}
}

func TestNativeFileDietCadenceRequiresCompleteCoverage(t *testing.T) {
	due := fileDietTime("2025-11-17T13:36:21Z")
	packet := model.Row{"windowEnd": due.Format(time.RFC3339), "facts": []any{
		map[string]any{"fact-kind": "contract", "cadence-days": 1},
		map[string]any{"fact-kind": "coverage", "coverage-status": "complete",
			"at": due.Add(-24 * time.Hour).Format(time.RFC3339), "end-at": due.Format(time.RFC3339)},
	}}
	notDue, err := fileDietCadenceSatisfied(packet, due.Add(time.Hour))
	if err != nil || !notDue {
		t.Fatalf("complete retained coverage should suppress duplicate acquisition: %v %v", notDue, err)
	}
	notDue, err = fileDietCadenceSatisfied(packet, due.Add(24*time.Hour+time.Hour))
	if err != nil || notDue {
		t.Fatalf("next UTC coverage window was skipped: %v %v", notDue, err)
	}
	packet["facts"].([]any)[1].(map[string]any)["coverage-status"] = "partial"
	notDue, err = fileDietCadenceSatisfied(packet, due.Add(time.Hour))
	if err != nil || notDue {
		t.Fatalf("partial coverage suppressed retry: %v %v", notDue, err)
	}
}

func testFileDietReplay() (fileDietProof, fileDietMeasurements) {
	adopted := "1186030a620f4113f655c156bedf70cf2c164f79"
	parent, merged := testFileDietSHA("2"), testFileDietSHA("3")
	at := "2026-01-05T00:00:00Z"
	created := "2026-01-02T00:00:00Z"
	target := "pkg/a.go"
	issue := fileDietIssue{
		Number: 1, ID: "I_1", Title: "[file-diet] Refactor pkg/a.go", CreatedAt: created,
		TargetPath: &target, Receipt: &fileDietReceipt{Status: "missing"},
		Graph: fileDietGraph{fileDietTarget: fileDietTarget{ID: "I_1", Number: 1,
			Repository: dailyFileDietRepository, URL: "https://github.com/github/gh-aw/issues/1"},
			HistoryComplete: true, Events: []fileDietGraphEvent{}, Edges: []fileDietEdge{}},
		Timeline: fileDietTimeline{Complete: true, Events: []fileDietTimelineEvent{{
			Type: "connected", At: "2026-01-04T00:00:00Z",
			Target: &fileDietTimelineTarget{Kind: "pull-request", Number: 2},
		}}},
	}
	pull := fileDietPull{ID: "PR_2", Number: 2, Title: "Decompose pkg/a.go",
		CreatedAt: "2026-01-03T00:00:00Z", MergedAt: &at,
		PreMergeSHA: parent, MergeSHA: &merged, Files: []fileDietPullFile{{
			Path: "pkg/replacement.go", Status: "added",
		}},
		Graph: fileDietGraph{fileDietTarget: fileDietTarget{ID: "PR_2", Number: 2,
			Repository: dailyFileDietRepository, URL: "https://github.com/github/gh-aw/pull/2"},
			State: "MERGED", MergedAt: &at, MergeCommit: &merged,
			ClosingLinksComplete: true, HistoryComplete: true, Edges: []fileDietEdge{},
			Events: []fileDietGraphEvent{}},
	}
	mkFile := func(path, blob string, lines int) fileDietFile {
		return fileDietFile{Path: path, Blob: testFileDietSHA(blob), Lines: lines, Bytes: lines * 2}
	}
	inventories := map[string]fileDietInventory{
		adopted: {Commit: adopted, Files: []fileDietFile{mkFile("pkg/a.go", "a", 1129)}},
		parent:  {Commit: parent, Files: []fileDietFile{mkFile("pkg/a.go", "a", 1129), mkFile("pkg/unrelated.go", "b", 900)}},
		merged:  {Commit: merged, Files: []fileDietFile{mkFile("pkg/a.go", "c", 248), mkFile("pkg/replacement.go", "d", 850)}},
	}
	proof := fileDietProof{SchemaVersion: 1, Repository: dailyFileDietRepository,
		Aliases:    []string{dailyFileDietRepository, "githubnext/gh-aw"},
		SourcePath: fileDietSourcePath, Adoption: fileDietAdoption{Commit: adopted,
			BaselineCommit: testFileDietSHA("0"), At: "2025-11-15T13:36:21Z"},
		Until: "2026-05-01T00:00:00Z", ObservedAt: "2026-05-01T01:00:00Z",
		Discovery: []fileDietDiscovery{{Complete: true}}, Issues: []fileDietIssue{issue},
		Pulls: []fileDietPull{pull}, Snapshots: []fileDietSnapshot{
			{At: "2025-11-15T13:36:21Z", Commit: adopted},
			{At: "2026-05-01T00:00:00Z", Commit: merged},
		}}
	return proof, fileDietMeasurements{inventories: inventories}
}

func TestNativeFileDietReplayMatchesIndependentActionsOracle(t *testing.T) {
	proof, measured := testFileDietReplay()
	ledger, err := fileDietReplay(proof, measured)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := fileDietExport(ledger)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	oracles := []string{
		filepath.Join(root, ".github", "skills", "add-operational-value", "examples", "daily-file-diet.mjs"),
		filepath.Join(root, ".github", "skills", "add-operational-value", "scripts", "export-evidence.mjs"),
		filepath.Join(root, "dashboard", "site", "src", "data", "model", "operational-value-evidence.js"),
	}
	input := struct {
		Proof       fileDietProof             `json:"proof"`
		Inventories map[string][]fileDietFile `json:"inventories"`
	}{Proof: proof, Inventories: map[string][]fileDietFile{}}
	for revision, inventory := range measured.inventories {
		input.Inventories[revision] = inventory.Files
	}
	content, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	code := `import { pathToFileURL } from 'node:url';
const {fileDietEvents} = await import(pathToFileURL(process.argv[2]));
const {exportOperationalValueEvidence} = await import(pathToFileURL(process.argv[3]));
const {validateOperationalValueEvidence} = await import(pathToFileURL(process.argv[4]));
let input = '';
for await (const chunk of process.stdin) input += chunk;
const {proof, inventories} = JSON.parse(input);
process.stdout.write(JSON.stringify(validateOperationalValueEvidence(exportOperationalValueEvidence(fileDietEvents({
  proof, inventories: new Map(Object.entries(inventories))
})))));`
	command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", code, "oracle", oracles[0], oracles[1], oracles[2]) // #nosec G204 -- fixed checked-in independent fixture implementation.
	command.Stdin = bytes.NewReader(content)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Actions oracle rejected the same proof: %v: %.1000s", err, output)
	}
	var expected model.Row
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	normalize := func(row model.Row) []byte {
		encoded, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	if !bytes.Equal(normalize(packet), normalize(expected)) {
		gotFacts, wantFacts := packet["facts"].([]any), expected["facts"].([]any)
		t.Fatalf("File Diet Go proof replay differs from Actions oracle: digest %v / %v, facts %d / %d; first differing fact %s",
			packet["contractDigest"], expected["contractDigest"], len(gotFacts), len(wantFacts),
			firstFileDietDifference(gotFacts, wantFacts))
	}
}

func firstFileDietDifference(left, right []any) string {
	for i := 0; i < min(len(left), len(right)); i++ {
		a, _ := json.Marshal(left[i])
		b, _ := json.Marshal(right[i])
		if !bytes.Equal(a, b) {
			return string(a) + " vs " + string(b)
		}
	}
	return "no differing fact"
}

func TestNativeFileDietRequiresFrozenManifestAndScope(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	instrument, err := os.ReadFile(filepath.Join(root, ".github", "skills", "add-operational-value",
		"examples", "acquire-file-diet.mjs")) // #nosec G304 -- reads a fixed reviewed source under the repository root.
	if err != nil || fileDietRawBytesDigest(instrument) != fileDietAcquisitionInstrumentSHA {
		t.Fatalf("reviewed acquisition instrument changed without the native equivalent: %v", err)
	}
	collector := filepath.Join(root, dailyFileDietCollector)
	code := `import { pathToFileURL } from 'node:url';
const {definition} = await import(pathToFileURL(process.argv[2]));
process.stdout.write(JSON.stringify(definition.acquisition));`
	command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", code, "oracle", collector) // #nosec G204 -- fixed reviewed collector fixture.
	content, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read frozen Actions collector manifest: %v: %.300s", err, content)
	}
	var reviewed remoteValueAcquisition
	if err := json.Unmarshal(content, &reviewed); err != nil {
		t.Fatal(err)
	}
	if len(reviewed.Requirements) != len(dailyFileDietRequirements) {
		t.Fatalf("reviewed Actions manifest changed without native implementation: %+v", reviewed)
	}
	required := map[string]bool{}
	for _, requirement := range dailyFileDietRequirements {
		required[remoteValueRequirementKey(requirement)] = true
	}
	for _, requirement := range reviewed.Requirements {
		if !required[remoteValueRequirementKey(requirement)] {
			t.Fatalf("Actions collector primitive lacks native implementation: %+v", requirement)
		}
		delete(required, remoteValueRequirementKey(requirement))
	}
	if len(required) > 0 {
		t.Fatalf("native frozen acquisition diverged from Actions: %+v", required)
	}
	entry := remoteValueEntry{Repository: dailyFileDietRepository, Workflow: dailyFileDietWorkflow,
		Collector: dailyFileDietCollector, APIRequests: 700,
		Acquisition: &remoteValueAcquisition{Requirements: append([]remoteValueRequirement(nil), dailyFileDietRequirements...)}}
	if err := reviewedDailyFileDiet(entry); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []remoteValueEntry{
		{Repository: "other/gh-aw", Workflow: entry.Workflow, Collector: entry.Collector,
			APIRequests: entry.APIRequests, Acquisition: entry.Acquisition},
		{Repository: entry.Repository, Workflow: entry.Workflow, Collector: ".github/value-collectors/unreviewed.mjs",
			APIRequests: entry.APIRequests, Acquisition: entry.Acquisition},
		{Repository: entry.Repository, Workflow: entry.Workflow, Collector: entry.Collector,
			APIRequests: 0, Acquisition: entry.Acquisition},
	} {
		if err := reviewedDailyFileDiet(invalid); err == nil {
			t.Fatalf("unreviewed File Diet registration accepted: %+v", invalid)
		}
	}
	broken := entry
	broken.Acquisition = &remoteValueAcquisition{Requirements: append([]remoteValueRequirement(nil), dailyFileDietRequirements...)}
	broken.Acquisition.Requirements[len(broken.Acquisition.Requirements)-1].Pool = "core"
	if err := reviewedDailyFileDiet(broken); err == nil {
		t.Fatal("git blob measurement was allowed to borrow GitHub core quota")
	}
	valid, rejected := supportedRemoteValueEntries([]remoteValueEntry{broken, entry})
	if rejected == nil || len(valid) != 1 || valid[0].Workflow != dailyFileDietWorkflow {
		t.Fatalf("unsupported registration suppressed later reviewed collector: %+v %v", valid, rejected)
	}
	for _, request := range []string{"repos/github/gh-aw/issues/1/comments", "search/repositories?q=github"} {
		if _, err := fileDietRequirement(request, ""); err == nil {
			t.Fatalf("out-of-manifest File Diet endpoint admitted: %q", request)
		}
	}
}

func TestNativeFileDietProofArchiveRejectsTamper(t *testing.T) {
	proof, measured := testFileDietReplay()
	proof.InventoryPartitions = []fileDietPartition{}
	proof.UniqueBlobs = 4
	files := map[string][]byte{}
	for _, revision := range []string{proof.Adoption.Commit, proof.Pulls[0].PreMergeSHA, *proof.Pulls[0].MergeSHA} {
		inventory := measured.inventories[revision]
		inventory.InventoryDigest = fileDietJSONDigest(inventory.Files)
		name := "inventory-" + revision + ".json.gz"
		proof.InventoryPartitions = append(proof.InventoryPartitions, fileDietPartition{
			File: name, Commit: revision, SHA256: fileDietJSONDigest(inventory),
		})
		var err error
		files[name], err = fileDietArchiveProof(inventory)
		if err != nil {
			t.Fatal(err)
		}
	}
	var err error
	files["proof.json.gz"], err = fileDietArchiveProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileDietLoadProof(files); err != nil {
		t.Fatalf("valid bounded File Diet proof rejected: %v", err)
	}
	changed := map[string][]byte{}
	for key, value := range files {
		changed[key] = bytes.Clone(value)
	}
	var plain bytes.Buffer
	writer := gzip.NewWriter(&plain)
	_, _ = writer.Write([]byte(`{"commit":"wrong","files":[],"inventoryDigest":"broken"}`))
	_ = writer.Close()
	changed[proof.InventoryPartitions[0].File] = plain.Bytes()
	if _, err := fileDietLoadProof(changed); err == nil {
		t.Fatal("tampered accepted File Diet inventory replayed")
	}
}

func TestNativeFileDietGitScopeAndNoAmbientCredentials(t *testing.T) {
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fileDietGitWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	if _, err := newNativeFileDietGit(context.Background(), workspace, "another/private-repository"); err == nil {
		t.Fatal("unreviewed Git target admitted")
	}
	git, err := newNativeFileDietGit(t.Context(), workspace, dailyFileDietRepository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(git.dir, "HEAD")); err != nil {
		t.Fatalf("private immutable Git workspace not initialized: %v", err)
	}
	if err := git.fetch(t.Context(), []string{"not-an-immutable-commit"}, false); err == nil {
		t.Fatal("unreviewed immutable Git object identity was fetched")
	}
	if _, err := parseFileDietTree([]byte("120000 blob "+testFileDietSHA("a")+"\tpkg/link.go\x00"), "docs/", 1); err == nil {
		t.Fatal("out-of-scope immutable Git tree admitted")
	}
	if _, err := fileDietReferenceFor("<!-- gh-aw-workflow-id: another -->"); !errors.Is(err, errFileDietReferenceNotFound) {
		t.Fatalf("unrelated tracking marker was not treated as absent: %v", err)
	}
}

func TestNativeFileDietLegacyRunReferenceRequiresExactGitHubURL(t *testing.T) {
	valid := "<!-- gh-aw-agentic-workflow: Daily File Diet, workflow_id: daily-file-diet, id: 42, run: https://github.com/github/gh-aw/actions/runs/42 -->"
	reference, err := fileDietReferenceFor(valid)
	if err != nil || reference == nil || *reference.RunID != "42" {
		t.Fatalf("valid legacy File Diet run reference rejected: reference=%+v err=%v", reference, err)
	}

	for _, runURL := range []string{
		"https://attacker.example/https://github.com/github/gh-aw/actions/runs/42",
		"https://github.com/github/gh-aw/actions/runs/42.attacker.example",
	} {
		body := "<!-- gh-aw-agentic-workflow: Daily File Diet, workflow_id: daily-file-diet, id: 42, run: " + runURL + " -->"
		if _, err := fileDietReferenceFor(body); err == nil {
			t.Errorf("accepted a non-exact legacy run URL: %q", runURL)
		}
	}
}

func TestNativeFileDietHistoricalSeed(t *testing.T) {
	if os.Getenv("CAO_TEST_REMOTE_VALUE_GIT") != "1" {
		t.Skip("set CAO_TEST_REMOTE_VALUE_GIT=1 to fetch the frozen public historical proof")
	}
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fileDietGitWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	seed, err := fileDietSeed(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if seed.proof.Adoption.Commit != "1186030a620f4113f655c156bedf70cf2c164f79" ||
		len(seed.inventories) == 0 || seed.proof.UniqueBlobs == 0 {
		t.Fatal("frozen historical Git seed lacked native proof")
	}
	ledger, err := fileDietReplay(seed.proof, fileDietMeasurements{inventories: seed.inventories})
	if err != nil {
		t.Fatalf("frozen seed failed native replay: %v", err)
	}
	if _, err := fileDietExport(ledger); err != nil {
		t.Fatalf("frozen seed failed native evidence export: %v", err)
	}
}

func TestNativeFileDietNewPrivateProofReplaysInActionsRuntime(t *testing.T) {
	proof, measured := testFileDietReplay()
	measured.uniqueBlobs = 4
	original := map[string][]byte{}
	for revision, inventory := range measured.inventories {
		inventory.InventoryDigest = fileDietJSONDigest(inventory.Files)
		name := "inventory-" + revision + ".json.gz"
		proof.InventoryPartitions = append(proof.InventoryPartitions, fileDietPartition{
			File: name, Commit: revision, SHA256: fileDietJSONDigest(inventory),
		})
		content, err := fileDietArchiveProof(inventory)
		if err != nil {
			t.Fatal(err)
		}
		original[name] = content
	}
	proof.UniqueBlobs = 4
	var err error
	original["proof.json.gz"], err = fileDietArchiveProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := fileDietLoadProof(original)
	if err != nil {
		t.Fatal(err)
	}
	next := prior.proof
	next.Until = "2026-05-02T00:00:00Z"
	next.Snapshots = append(next.Snapshots, fileDietSnapshot{At: next.Until, Commit: *next.Pulls[0].MergeSHA})
	proofFiles, err := fileDietBuildProofFiles(&next, measured, &prior)
	if err != nil {
		t.Fatalf("new native proof rejected on private re-read: %v", err)
	}
	if _, err := fileDietRestore(proofFiles); err != nil {
		t.Fatalf("native proof did not survive PostgreSQL file shape: %v", err)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fileDietGitWorkspace(root) // private project-owned scratch, not system temp
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	for _, file := range proofFiles {
		path := filepath.Join(workspace, strings.TrimPrefix(file.Path, "report/proof/"))
		if err := os.WriteFile(path, file.Content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(root, ".github", "skills", "add-operational-value",
		"examples", "daily-file-diet.mjs")
	code := `import { pathToFileURL } from 'node:url';
const {readFileDietProof} = await import(pathToFileURL(process.argv[2]));
process.stdout.write(readFileDietProof(process.argv[3]).proof.until);`
	command := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", code, "oracle", source, workspace) // #nosec G204 -- Actions oracle reads only test-owned proof bytes.
	output, err := command.CombinedOutput()
	if err != nil || string(output) != next.Until {
		t.Fatalf("Actions reader rejected native private proof: %.400s %v", output, err)
	}
}

func TestNativeFileDietIncrementalAcquisitionIsBoundedAndFailClosed(t *testing.T) {
	previous, _ := testFileDietReplay()
	previous.History = []fileDietHistoryRevision{{Commit: previous.Adoption.Commit, At: previous.Adoption.At}}
	until := "2026-05-02T00:00:00Z"
	reads := []string{}
	read := func(_ context.Context, endpoint, query string, binary bool) ([]byte, error) {
		if binary {
			return nil, fmt.Errorf("unreviewed File Diet binary acquisition %s", endpoint)
		}
		reads = append(reads, endpoint)
		switch {
		case endpoint == "repos/github/gh-aw" || endpoint == "repos/githubnext/gh-aw":
			return []byte(`{"id":42,"full_name":"github/gh-aw"}`), nil
		case strings.HasPrefix(endpoint, "repos/github/gh-aw/commits?path="):
			return []byte(`[]`), nil
		case strings.HasPrefix(endpoint, "search/issues?"):
			return []byte(`{"incomplete_results":false,"total_count":0,"items":[]}`), nil
		case endpoint == "graphql":
			if query != remoteValueExpectedGraphQuery(dailyFileDietRepository, []int{1}) {
				return nil, fmt.Errorf("unexpected File Diet graph query")
			}
			return []byte(`{"data":{"repository":{"n1":{
				  "__typename":"Issue","id":"I_1","number":1,"url":"https://github.com/github/gh-aw/issues/1",
				  "repository":{"nameWithOwner":"github/gh-aw"},"state":"OPEN",
				  "createdAt":"2026-01-02T00:00:00Z","updatedAt":"2026-05-01T00:00:00Z",
				  "timelineItems":{"nodes":[],"pageInfo":{"hasNextPage":false}}
				}}}}`), nil
		case strings.Contains(endpoint, "/issues/1/timeline?"):
			return []byte(`[]`), nil
		case strings.HasPrefix(endpoint, "repos/github/gh-aw/commits?until="):
			return []byte(`[{"sha":"` + testFileDietSHA("3") +
				`","commit":{"committer":{"date":"2026-05-01T00:00:00Z"}}}]`), nil
		}
		return nil, fmt.Errorf("unreviewed File Diet read %s", endpoint)
	}
	acquired, err := fileDietAcquireHistory(t.Context(), read, until, &previous)
	if err != nil || len(acquired.Issues) != 1 || len(acquired.Pulls) != 1 ||
		len(acquired.Snapshots) != 3 || acquired.Snapshots[2].At != until ||
		acquired.Adoption != previous.Adoption {
		t.Fatalf("incremental File Diet proof = %+v, %v", acquired, err)
	}
	for _, endpoint := range reads {
		if strings.HasPrefix(endpoint, "search/issues?") &&
			!strings.Contains(endpoint, "updated%3A2026-05-01..2026-05-02") {
			t.Fatalf("File Diet discovery exceeded prior cutoff: %s", endpoint)
		}
	}
	if len(reads) > 16 {
		t.Fatalf("one-day File Diet refresh made unbounded requests: %d", len(reads))
	}
	denySearch := func(ctx context.Context, endpoint, query string, binary bool) ([]byte, error) {
		if strings.HasPrefix(endpoint, "search/issues?") {
			return []byte(`{"incomplete_results":true,"total_count":0,"items":[]}`), nil
		}
		return read(ctx, endpoint, query, binary)
	}
	if _, err := fileDietAcquireHistory(t.Context(), denySearch, until, &previous); err == nil {
		t.Fatal("incomplete File Diet issue discovery was accepted")
	}
	denyAlias := func(ctx context.Context, endpoint, query string, binary bool) ([]byte, error) {
		if endpoint == "repos/githubnext/gh-aw" {
			return []byte(`{"id":7,"full_name":"other/repository"}`), nil
		}
		return read(ctx, endpoint, query, binary)
	}
	if _, err := fileDietAcquireHistory(t.Context(), denyAlias, until, &previous); err == nil {
		t.Fatal("cross-repository File Diet alias was accepted")
	}
}

func TestNativeFileDietBatchesMutablePullGraphsAndRejectsChangedIdentities(t *testing.T) {
	for _, failure := range []string{"", "missing-alias", "wrong-number", "wrong-kind", "wrong-id"} {
		t.Run(failure, func(t *testing.T) {
			previous, _ := testFileDietReplay()
			previous.History = []fileDietHistoryRevision{{Commit: previous.Adoption.Commit, At: previous.Adoption.At}}
			for number := 3; number <= 13; number++ {
				previous.Pulls = append(previous.Pulls, fileDietPull{Number: number, ID: fmt.Sprintf("PR_%d", number)})
			}
			graphBatches := [][]int{}
			pullReads := 0
			read := func(_ context.Context, endpoint, query string, binary bool) ([]byte, error) {
				if binary {
					return nil, errors.New("unexpected binary acquisition")
				}
				switch {
				case endpoint == "repos/github/gh-aw" || endpoint == "repos/githubnext/gh-aw":
					return []byte(`{"id":42,"full_name":"github/gh-aw"}`), nil
				case strings.HasPrefix(endpoint, "repos/github/gh-aw/commits?path="):
					return []byte(`[]`), nil
				case strings.HasPrefix(endpoint, "search/issues?"):
					return []byte(`{"incomplete_results":false,"total_count":0,"items":[]}`), nil
				case endpoint == "graphql":
					entities := map[string]any{}
					numbers := []int{}
					for number := 1; number <= 13; number++ {
						if !strings.Contains(query, fmt.Sprintf("n%d: issueOrPullRequest(", number)) {
							continue
						}
						numbers = append(numbers, number)
						entity := map[string]any{
							"__typename": "PullRequest", "id": fmt.Sprintf("PR_%d", number), "number": number,
							"url":        fmt.Sprintf("https://github.com/github/gh-aw/pull/%d", number),
							"repository": map[string]any{"nameWithOwner": dailyFileDietRepository},
							"state":      "OPEN", "createdAt": "2026-01-03T00:00:00Z", "updatedAt": previous.Until,
							"timelineItems":           map[string]any{"nodes": []any{}, "pageInfo": map[string]any{"hasNextPage": false}},
							"closingIssuesReferences": map[string]any{"nodes": []any{}, "pageInfo": map[string]any{"hasNextPage": false}},
						}
						if number == 1 {
							entity["__typename"], entity["id"], entity["url"] =
								"Issue", "I_1", "https://github.com/github/gh-aw/issues/1"
						}
						if number == 3 {
							switch failure {
							case "missing-alias":
								continue
							case "wrong-number":
								entity["number"], entity["url"] = 99, "https://github.com/github/gh-aw/pull/99"
							case "wrong-kind":
								entity["__typename"] = "Issue"
							case "wrong-id":
								entity["id"] = "PR_other"
							}
						}
						entities[fmt.Sprintf("n%d", number)] = entity
					}
					graphBatches = append(graphBatches, numbers)
					if len(numbers) == 0 || len(numbers) > 10 ||
						query != remoteValueExpectedGraphQuery(dailyFileDietRepository, numbers) {
						t.Fatalf("unbounded or unexpected graph query: %+v", numbers)
					}
					return json.Marshal(map[string]any{"data": map[string]any{"repository": entities}})
				case strings.Contains(endpoint, "/issues/1/timeline?"):
					return []byte(`[]`), nil
				case strings.Contains(endpoint, "/files?"):
					return []byte(`[]`), nil
				case strings.HasPrefix(endpoint, "repos/github/gh-aw/pulls/"):
					var number int
					if _, err := fmt.Sscanf(endpoint, "repos/github/gh-aw/pulls/%d", &number); err != nil {
						return nil, err
					}
					if number == 2 {
						t.Fatal("immutable merged pull was fetched again")
					}
					pullReads++
					return json.Marshal(map[string]any{
						"number": number, "node_id": fmt.Sprintf("PR_%d", number), "title": "Split oversized Go file",
						"created_at": "2026-01-03T00:00:00Z", "changed_files": 0,
						"head": map[string]any{"sha": testFileDietSHA("a")},
						"base": map[string]any{"sha": testFileDietSHA("b")},
					})
				case strings.HasPrefix(endpoint, "repos/github/gh-aw/commits?until="):
					return []byte(`[{"sha":"` + testFileDietSHA("3") +
						`","commit":{"committer":{"date":"2026-05-01T00:00:00Z"}}}]`), nil
				}
				return nil, fmt.Errorf("unreviewed File Diet read %s", endpoint)
			}
			acquired, err := fileDietAcquireHistory(t.Context(), read, "2026-05-02T00:00:00Z", &previous)
			if failure != "" {
				if err == nil {
					t.Fatal("invalid implementation graph was accepted")
				}
				return
			}
			batches, _ := json.Marshal(graphBatches)
			if err != nil || string(batches) != `[[1],[3,4,5,6,7,8,9,10,11,12],[13]]` ||
				pullReads != 11 || len(acquired.Pulls) != 12 {
				t.Fatalf("mutable pull batches=%s reads=%d pulls=%d err=%v",
					batches, pullReads, len(acquired.Pulls), err)
			}
			before, _ := json.Marshal(previous.Pulls[0])
			after, _ := json.Marshal(acquired.Pulls[0])
			if !bytes.Equal(before, after) {
				t.Fatal("immutable merged pull changed")
			}
			for _, discovery := range acquired.Discovery {
				if !discovery.Complete {
					t.Fatal("native acquisition selected incomplete shadow discovery")
				}
			}
		})
	}
}

func TestNativeFileDietEvidenceAcceptedByProductionPostgres(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	proof, measured := testFileDietReplay()
	ledger, err := fileDietReplay(proof, measured)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := fileDietExport(ledger)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := data.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	repositoryID := "repository:" + model.EncodeCoordinate(dailyFileDietRepository)
	workflowID := "workflow:" + model.EncodeCoordinate(dailyFileDietRepository+":"+fileDietSourcePath)
	for source, row := range map[string]model.Row{
		"$repositories": {"id": repositoryID, "owner": "github", "name": "gh-aw"},
		"$workflows":    {"id": workflowID, "repositoryId": repositoryID, "path": fileDietSourcePath},
	} {
		if err := writer.Append(t.Context(), source, row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Publish(t.Context(), "file-diet-production-parent"); err != nil {
		t.Fatal(err)
	}
	accepted, err := data.PublishOperationalValueWithProof(t.Context(), packet,
		dailyFileDietRepository, dailyFileDietWorkflow, []postgresx.ValueProofFile{{
			Path: "report/proof/proof.json.gz", Content: []byte("frozen-private-proof"),
		}}, time.Now())
	if err != nil || !accepted.Updated {
		t.Fatalf("native File Diet packet rejected by production publication: %+v %v", accepted, err)
	}
	retained, err := data.ReadOperationalValue(t.Context(), dailyFileDietRepository, dailyFileDietWorkflow)
	if err != nil || retained["contractDigest"] != packet["contractDigest"] {
		t.Fatalf("accepted File Diet evidence not retained: %v %v", retained, err)
	}
	files, err := data.ReadValueProof(t.Context(), dailyFileDietRepository, dailyFileDietWorkflow)
	if err != nil || len(files) != 1 || string(files[0].Content) != "frozen-private-proof" {
		t.Fatalf("accepted private File Diet proof not retained: %v %v", files, err)
	}
}
