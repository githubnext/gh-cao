package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	testEvidenceRepo   = "octo/cao"
	testEvidenceDate   = "2026-10-22T00:00:00Z"
	testEvidenceSecret = "credential-value-that-must-not-appear"
)

var (
	testEvidenceRevision = strings.Repeat("a", 40)
	testEvidenceDigest   = "sha256:" + strings.Repeat("b", 64)
	testEvidenceImage    = "ghcr.io/" + testEvidenceRepo + "/cao-server@" + testEvidenceDigest
)

type evidenceFixture struct {
	options      evidenceReleaseOptions
	deps         evidenceDeps
	run          evidenceAPIRun
	jobs         []evidenceAPIJob
	artifacts    []evidenceAPIArtifact
	approvals    []evidenceAPIApproval
	verification string
	verifyErr    error
	calls        [][]string
	responses    map[string]string
	failed       map[string]bool
	version      string
}

func newEvidenceFixture(t *testing.T) *evidenceFixture {
	t.Helper()
	f := &evidenceFixture{
		options: evidenceReleaseOptions{
			Repository: testEvidenceRepo, Revision: testEvidenceRevision, Image: testEvidenceImage,
			PackageRun: 42, Output: filepath.Join(t.TempDir(), "evidence.json"),
		},
		run: evidenceAPIRun{
			ID: 42, RunAttempt: 2, HeadSHA: testEvidenceRevision, Path: ".github/workflows/cao-package.yml",
			Event: "push", Conclusion: "success",
		},
		jobs:      []evidenceAPIJob{{ID: 99, RunID: 42, RunAttempt: 2, HeadSHA: testEvidenceRevision, Conclusion: "success"}},
		artifacts: []evidenceAPIArtifact{{ID: 100, Name: testEvidenceSecret, Digest: testEvidenceDigest, ExpiresAt: testEvidenceDate}},
		approvals: []evidenceAPIApproval{{State: "approved"}},
		responses: map[string]string{}, failed: map[string]bool{},
		version: "gh version 2.83.0 (2025-11-01)\n" + testEvidenceSecret,
	}
	f.run.Repository.FullName = testEvidenceRepo
	f.jobs[0].Steps = append(f.jobs[0].Steps, struct {
		Number     int64  `json:"number"`
		Conclusion string `json:"conclusion"`
	}{Number: 1, Conclusion: "success"})
	f.approvals[0].User.ID = 12
	f.approvals[0].Environments = append(f.approvals[0].Environments, struct {
		ID int64 `json:"id"`
	}{ID: 7})
	f.verification = `[{ "attestation": {"raw":"` + testEvidenceSecret + `"},
  "verificationResult": {"statement": {
    "predicateType":"https://slsa.dev/provenance/v1",
    "subject":[{"name":"ghcr.io/octo/cao/cao-server","digest":{"sha256":"` + strings.Repeat("b", 64) + `"}}],
    "predicate":{"sensitive":"` + testEvidenceSecret + `","runDetails":{"metadata":{
      "invocationId":"https://github.com/octo/cao/actions/runs/42/attempts/2"
    }}}
  }} }]`
	f.deps = evidenceDeps{
		now:      func() time.Time { parsed, _ := time.Parse(time.RFC3339, testEvidenceDate); return parsed },
		version:  "test",
		identity: func() (string, error) { return strings.Repeat("e", 64), nil },
		gh: func(ctx context.Context, args ...string) (string, error) {
			f.calls = append(f.calls, append([]string(nil), args...))
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > time.Minute {
				t.Fatal("evidence subprocess must have a bounded deadline")
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if len(args) == 1 && args[0] == "--version" {
				return f.version, nil
			}
			if args[0] == "attestation" {
				return f.verification, f.verifyErr
			}
			if len(args) != 6 || !reflect.DeepEqual(args[:5], []string{"api", "--hostname", "github.com", "--method", "GET"}) {
				t.Fatalf("unexpected or non-read-only command: %q", args)
			}
			path := strings.TrimPrefix(args[5], "repos/"+testEvidenceRepo+"/")
			if f.failed[path] {
				return "", errors.New(testEvidenceSecret)
			}
			if raw, ok := f.responses[path]; ok {
				return raw, nil
			}
			var result any
			switch path {
			case "actions/runs/42":
				result = f.run
			case "actions/runs/42/attempts/2/jobs?per_page=100&page=1":
				result = map[string]any{"jobs": f.jobs}
			case "actions/runs/42/artifacts?per_page=100&page=1":
				result = map[string]any{"artifacts": f.artifacts}
			case "actions/runs/42/approvals":
				result = f.approvals
			default:
				return "", errors.New(testEvidenceSecret)
			}
			return evidenceTestJSON(t, result), nil
		},
	}
	return f
}

func evidenceTestJSON(t *testing.T, value any) string {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func evidenceTestCollect(t *testing.T, f *evidenceFixture) evidenceReleaseReport {
	t.Helper()
	report, err := collectReleaseEvidence(context.Background(), f.deps, f.options)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(evidenceTestJSON(t, report), testEvidenceSecret) {
		t.Fatal("report exposed sensitive fixture data")
	}
	return report
}

func TestEvidenceReleaseShape(t *testing.T) {
	f := newEvidenceFixture(t)
	report := evidenceTestCollect(t, f)
	if report.Kind != "cao.release.evidence" || report.SchemaVersion != 1 || report.Status != "incomplete" ||
		report.ImageSourceBinding != "unknown" || report.IndependentRetention.Status != "unknown" ||
		report.Governance != "not-assessed" || report.OperatingEffectiveness != "not-assessed" ||
		report.Collector.BinarySHA256 != strings.Repeat("e", 64) || report.Collector.GitHubCLI.Version != "2.83.0" ||
		report.Collector.Version != "test" || report.Collector.GoVersion == "" || report.CollectedAt != testEvidenceDate {
		t.Fatalf("invalid report: %s", evidenceTestJSON(t, report))
	}
	if report.Package.Attempt != 2 || report.Package.SourceBinding != "push-revision-matches" ||
		report.Package.Jobs.Records[0].Reference != "https://github.com/octo/cao/actions/runs/42/job/99" ||
		report.Package.Artifacts.AttemptBinding != "unknown" || report.Package.Approvals.IndependentApproval != "unknown" ||
		*report.Package.Approvals.Records[0].ActorID != 12 || report.Promotion.Status != "unknown" {
		t.Fatal("run, job, approval or artifact binding was lost")
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal([]byte(evidenceTestJSON(t, report)), &shape); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"collector", "package", "promotion", "provenance", "deployment", "imageSourceBinding", "deployedImageDigest", "independentRetention"} {
		if len(shape[field]) == 0 {
			t.Fatalf("missing output field %s", field)
		}
	}
}

func TestEvidenceProvenance(t *testing.T) {
	f := newEvidenceFixture(t)
	f.options.Verify = true
	report := evidenceTestCollect(t, f)
	if report.Provenance.Status != "verified-by-gh" || report.ImageSourceBinding != "verified-by-gh" ||
		!evidenceHashPattern.MatchString(report.Provenance.ReceiptSHA256) || report.Provenance.RawReceiptRetained {
		t.Fatal("exact verified provenance was not recorded")
	}
	want := []string{"attestation", "verify", "oci://" + testEvidenceImage, "--repo", testEvidenceRepo,
		"--signer-workflow", testEvidenceRepo + "/.github/workflows/cao-package-publish.yml",
		"--source-digest", testEvidenceRevision, "--predicate-type", "https://slsa.dev/provenance/v1",
		"--limit", "10", "--format", "json", "--hostname", "github.com"}
	if !reflect.DeepEqual(f.calls[len(f.calls)-1], want) || !reflect.DeepEqual(report.Provenance.Command, want) {
		t.Fatal("verifier policy constraints changed")
	}
	for name, change := range map[string]func(*evidenceFixture){
		"digest": func(f *evidenceFixture) {
			f.verification = strings.ReplaceAll(f.verification, strings.Repeat("b", 64), strings.Repeat("f", 64))
		},
		"subject": func(f *evidenceFixture) {
			f.verification = strings.ReplaceAll(f.verification, "ghcr.io/octo/cao/cao-server", "ghcr.io/other/cao/cao-server")
		},
		"attempt": func(f *evidenceFixture) {
			f.verification = strings.ReplaceAll(f.verification, "/attempts/2", "/attempts/1")
		},
		"predicate": func(f *evidenceFixture) {
			f.verification = strings.ReplaceAll(f.verification, "https://slsa.dev/provenance/v1", "sbom")
		},
		"empty":     func(f *evidenceFixture) { f.verification = "[]" },
		"malformed": func(f *evidenceFixture) { f.verification = testEvidenceSecret },
		"failure":   func(f *evidenceFixture) { f.verifyErr = errors.New(testEvidenceSecret) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newEvidenceFixture(t)
			f.options.Verify = true
			change(f)
			report := evidenceTestCollect(t, f)
			if report.Provenance.Status != "unknown" || report.ImageSourceBinding != "unknown" {
				t.Fatal("unverified or mismatched provenance was accepted")
			}
		})
	}
}

func TestEvidenceOptionalFailuresAndRoles(t *testing.T) {
	f := newEvidenceFixture(t)
	for _, path := range []string{"actions/runs/42/approvals", "actions/runs/42/artifacts?per_page=100&page=1", "actions/runs/42/attempts/2/jobs?per_page=100&page=1"} {
		f.failed[path] = true
	}
	report := evidenceTestCollect(t, f)
	if report.Package.Jobs.Status != "unknown" || report.Package.Artifacts.Status != "unknown" || report.Package.Approvals.Status != "unknown" {
		t.Fatal("missing optional evidence was not marked unknown")
	}
	f.failed = map[string]bool{}
	f.approvals = []evidenceAPIApproval{}
	f.artifacts = append(f.artifacts, evidenceAPIArtifact{ID: 101, Name: "cao-server-42-2"})
	report = evidenceTestCollect(t, f)
	if report.Package.Approvals.Status != "unknown" || report.Package.Approvals.Reason != "no-approval-records" ||
		report.Package.Artifacts.Records[1].Role != "sealed-candidate" {
		t.Fatal("approval absence or allowlisted artifact role was lost")
	}
	for _, response := range []string{testEvidenceSecret, strings.Repeat("x", evidenceResponseLimit+1), `{"jobs":[{}]}`, `{"jobs":null}`} {
		f.responses["actions/runs/42/attempts/2/jobs?per_page=100&page=1"] = response
		if evidenceTestCollect(t, f).Package.Jobs.Status != "unknown" {
			t.Fatal("invalid/oversized evidence accepted")
		}
	}
	f.failed["actions/runs/42"] = true
	if _, err := collectReleaseEvidence(context.Background(), f.deps, f.options); err == nil || strings.Contains(err.Error(), testEvidenceSecret) {
		t.Fatalf("required run failure was not sanitized: %v", err)
	}
}

func TestEvidencePaginationAndAttemptBoundaries(t *testing.T) {
	f := newEvidenceFixture(t)
	for range 101 {
		f.jobs[0].Steps = append(f.jobs[0].Steps, f.jobs[0].Steps[0])
	}
	jobs := make([]evidenceAPIJob, 100)
	for i := range jobs {
		jobs[i] = f.jobs[0]
	}
	for page := 1; page <= 5; page++ {
		key := "actions/runs/42/attempts/2/jobs?per_page=100&page=" + strconv.Itoa(page)
		f.responses[key] = evidenceTestJSON(t, map[string]any{"jobs": jobs})
	}
	report := evidenceTestCollect(t, f)
	if report.Package.Jobs.Status != "incomplete" || len(report.Package.Jobs.Records) != 500 ||
		len(report.Package.Jobs.Records[0].Steps) != 100 || report.Package.Jobs.Records[0].StepsComplete {
		t.Fatal("pagination/step bounds not explicit")
	}
	for _, call := range f.calls {
		if strings.Contains(call[len(call)-1], "page=6") {
			t.Fatal("pagination exceeded its bound")
		}
	}
	f.responses = map[string]string{}
	f.jobs[0].RunAttempt = 1
	if evidenceTestCollect(t, f).Package.Jobs.Status != "unknown" {
		t.Fatal("wrong-attempt jobs accepted")
	}
	f.run.Event, f.run.HeadSHA = "workflow_dispatch", strings.Repeat("f", 40)
	f.jobs[0].RunAttempt, f.jobs[0].HeadSHA = 2, f.run.HeadSHA
	if evidenceTestCollect(t, f).Package.SourceBinding != "unknown" {
		t.Fatal("manual workflow SHA was conflated with product SHA")
	}
	f.options.PromotionRun = 43
	promotion := f.run
	promotion.ID, promotion.Path = 43, ".github/workflows/release-promote.yml"
	f.responses["actions/runs/43"] = evidenceTestJSON(t, promotion)
	report = evidenceTestCollect(t, f)
	if report.Promotion.SourceBinding != "unknown" || report.Promotion.WorkflowRevision != f.run.HeadSHA || report.SourceRevision != testEvidenceRevision {
		t.Fatal("promotion workflow SHA was conflated with product SHA")
	}
}

func TestEvidenceInvalidSelections(t *testing.T) {
	for name, change := range map[string]func(*evidenceReleaseOptions){
		"source":     func(o *evidenceReleaseOptions) { o.Revision = "main" },
		"run":        func(o *evidenceReleaseOptions) { o.PackageRun = -1 },
		"repository": func(o *evidenceReleaseOptions) { o.Repository = "../cao" },
		"image":      func(o *evidenceReleaseOptions) { o.Image = strings.Replace(o.Image, "octo/cao", "other/cao", 1) },
		"tag":        func(o *evidenceReleaseOptions) { o.Image = "ghcr.io/octo/cao/cao-server:latest" },
		"output":     func(o *evidenceReleaseOptions) { o.Output = "" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newEvidenceFixture(t)
			change(&f.options)
			if _, err := collectReleaseEvidence(context.Background(), f.deps, f.options); err == nil || len(f.calls) != 0 {
				t.Fatal("invalid selections must fail before invoking GitHub")
			}
		})
	}
	for name, change := range map[string]func(*evidenceAPIRun){
		"id":         func(r *evidenceAPIRun) { r.ID++ },
		"repository": func(r *evidenceAPIRun) { r.Repository.FullName = "other/cao" },
		"workflow":   func(r *evidenceAPIRun) { r.Path = ".github/workflows/cgo.yml" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newEvidenceFixture(t)
			change(&f.run)
			if _, err := collectReleaseEvidence(context.Background(), f.deps, f.options); err == nil {
				t.Fatal("mismatched run identity accepted")
			}
		})
	}
}

func TestEvidenceCommandPersistence(t *testing.T) {
	f := newEvidenceFixture(t)
	args := []string{"release", "--repo", testEvidenceRepo, "--revision", testEvidenceRevision,
		"--image", testEvidenceImage, "--package-run", "42", "--output", f.options.Output}
	execute := func() error {
		cmd := evidenceCommand(f.deps)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		return cmd.ExecuteContext(context.Background())
	}
	if err := execute(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.options.Output)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(f.options.Output)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("output must have private permissions")
	}
	var report evidenceReleaseReport
	if json.Unmarshal(before, &report) != nil || report.Deployment.Status != "unknown" || report.Status != "incomplete" {
		t.Fatal("persisted report lost evidence shape")
	}
	if err := execute(); err == nil {
		t.Fatal("existing output was overwritten")
	}
	after, err := os.ReadFile(f.options.Output)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("existing output changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output := filepath.Join(t.TempDir(), "cancelled.json")
	if err := writeReleaseEvidence(ctx, output, report); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled write was not rejected")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("cancelled collection wrote evidence")
	}
	f.options.Output = filepath.Join(t.TempDir(), "zero-promotion.json")
	args = append(args[:len(args)-1], f.options.Output, "--promotion-run", "0")
	if err := execute(); err == nil {
		t.Fatal("explicit zero promotion run must fail")
	}
	if _, err := os.Stat(f.options.Output); !os.IsNotExist(err) {
		t.Fatal("invalid promotion selection wrote evidence")
	}
}
