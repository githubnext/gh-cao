package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type evidenceCollector struct {
	Version      string       `json:"version"`
	GoVersion    string       `json:"goVersion"`
	BinarySHA256 string       `json:"binarySHA256"`
	GitHubCLI    evidenceTool `json:"githubCli"`
}

type evidenceTool struct {
	evidenceState
	Version string `json:"version,omitempty"`
}

type evidenceReleaseReport struct {
	Kind                   string             `json:"kind"`
	SchemaVersion          int                `json:"schemaVersion"`
	CollectedAt            string             `json:"collectedAt"`
	Status                 string             `json:"status"`
	Repository             string             `json:"repository"`
	SourceRevision         string             `json:"sourceRevision"`
	Image                  string             `json:"image"`
	Collector              evidenceCollector  `json:"collector"`
	Attribution            string             `json:"attribution"`
	Package                evidenceRun        `json:"package"`
	Promotion              evidencePromotion  `json:"promotion"`
	Provenance             evidenceProvenance `json:"provenance"`
	Deployment             evidenceState      `json:"deployment"`
	ImageSourceBinding     string             `json:"imageSourceBinding"`
	DeployedImageDigest    evidenceState      `json:"deployedImageDigest"`
	IndependentRetention   evidenceState      `json:"independentRetention"`
	Governance             string             `json:"governance"`
	OperatingEffectiveness string             `json:"operatingEffectiveness"`
}

type evidencePromotion struct {
	evidenceState
	*evidenceRun
}

type evidenceRun struct {
	ID               int64                             `json:"id"`
	Attempt          int64                             `json:"attempt"`
	Workflow         string                            `json:"workflow"`
	WorkflowRevision string                            `json:"workflowRevision"`
	SourceBinding    string                            `json:"sourceBinding"`
	Conclusion       string                            `json:"conclusion"`
	Reference        string                            `json:"reference"`
	Jobs             evidenceRecords[evidenceJob]      `json:"jobs"`
	Artifacts        evidenceRecords[evidenceArtifact] `json:"artifacts"`
	Approvals        evidenceRecords[evidenceApproval] `json:"approvals"`
}

type evidenceRecords[T any] struct {
	evidenceState
	AttemptBinding      string `json:"attemptBinding,omitempty"`
	IndependentApproval string `json:"independentApproval,omitempty"`
	Records             []T    `json:"records"`
}

type evidenceStep struct {
	Number     *int64 `json:"number"`
	Conclusion string `json:"conclusion"`
}

type evidenceJob struct {
	ID            int64          `json:"id"`
	Conclusion    string         `json:"conclusion"`
	Reference     string         `json:"reference"`
	Steps         []evidenceStep `json:"steps"`
	StepsComplete bool           `json:"stepsComplete"`
}

type evidenceArtifact struct {
	ID        int64   `json:"id"`
	Role      string  `json:"role"`
	Digest    *string `json:"digest"`
	Expired   *bool   `json:"expired"`
	ExpiresAt *string `json:"expiresAt"`
}

type evidenceApproval struct {
	State                string   `json:"state"`
	ActorID              *int64   `json:"actorId"`
	EnvironmentIDs       []*int64 `json:"environmentIds"`
	EnvironmentsComplete bool     `json:"environmentsComplete"`
}

type evidenceAPIRun struct {
	ID         int64  `json:"id"`
	RunAttempt int64  `json:"run_attempt"`
	HeadSHA    string `json:"head_sha"`
	Path       string `json:"path"`
	Event      string `json:"event"`
	Conclusion string `json:"conclusion"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

type evidenceAPIJob struct {
	ID         int64  `json:"id"`
	RunID      int64  `json:"run_id"`
	RunAttempt int64  `json:"run_attempt"`
	HeadSHA    string `json:"head_sha"`
	Conclusion string `json:"conclusion"`
	Steps      []struct {
		Number     int64  `json:"number"`
		Conclusion string `json:"conclusion"`
	} `json:"steps"`
}

type evidenceAPIArtifact struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Digest    string `json:"digest"`
	Expired   *bool  `json:"expired"`
	ExpiresAt string `json:"expires_at"`
}

type evidenceAPIApproval struct {
	State string `json:"state"`
	User  struct {
		ID int64 `json:"id"`
	} `json:"user"`
	Environments []struct {
		ID int64 `json:"id"`
	} `json:"environments"`
}

func readEvidenceInventory[T any](c evidenceClient, path, key string) ([]T, evidenceState, error) {
	result := []T{}
	for page := 1; page <= 5; page++ {
		var data map[string]json.RawMessage
		if err := c.get(fmt.Sprintf("%s?per_page=100&page=%d", path, page), &data); err != nil {
			return nil, evidenceState{}, err
		}
		var records []T
		if json.Unmarshal(data[key], &records) != nil || records == nil || len(records) > 100 {
			return nil, evidenceState{}, errors.New("invalid evidence inventory")
		}
		result = append(result, records...)
		if len(records) < 100 {
			return result, evidenceState{Status: "observed"}, nil
		}
	}
	return result, evidenceState{Status: "incomplete", Reason: "pagination-bound"}, nil
}

func evidenceIDPointer(value int64) *int64 {
	if evidenceID(value) {
		return &value
	}
	return nil
}

func (c evidenceClient) jobs(run evidenceAPIRun, reference string) evidenceRecords[evidenceJob] {
	output := evidenceRecords[evidenceJob]{evidenceState: evidenceUnknown("unavailable-or-invalid")}
	path := fmt.Sprintf("actions/runs/%d/attempts/%d/jobs", run.ID, run.RunAttempt)
	jobs, state, err := readEvidenceInventory[evidenceAPIJob](c, path, "jobs")
	if err != nil {
		return output
	}
	result := make([]evidenceJob, 0, len(jobs))
	for _, job := range jobs {
		if !evidenceID(job.ID) || job.RunID != run.ID || job.RunAttempt != run.RunAttempt || job.HeadSHA != run.HeadSHA {
			return output
		}
		steps := make([]evidenceStep, 0, min(len(job.Steps), 100))
		for _, step := range job.Steps[:min(len(job.Steps), 100)] {
			steps = append(steps, evidenceStep{Number: evidenceIDPointer(step.Number), Conclusion: evidenceConclusion(step.Conclusion)})
		}
		result = append(result, evidenceJob{
			ID: job.ID, Conclusion: evidenceConclusion(job.Conclusion),
			Reference: fmt.Sprintf("%s/job/%d", strings.Split(reference, "/attempts/")[0], job.ID),
			Steps:     steps, StepsComplete: job.Steps != nil && len(job.Steps) <= 100,
		})
	}
	return evidenceRecords[evidenceJob]{evidenceState: state, Records: result}
}

func (c evidenceClient) artifacts(run evidenceAPIRun) evidenceRecords[evidenceArtifact] {
	output := evidenceRecords[evidenceArtifact]{evidenceState: evidenceUnknown("unavailable-or-invalid"), AttemptBinding: "unknown"}
	artifacts, state, err := readEvidenceInventory[evidenceAPIArtifact](c, fmt.Sprintf("actions/runs/%d/artifacts", run.ID), "artifacts")
	if err != nil {
		return output
	}
	result := make([]evidenceArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if !evidenceID(artifact.ID) {
			return output
		}
		record := evidenceArtifact{ID: artifact.ID, Role: "unknown", Expired: artifact.Expired}
		switch artifact.Name {
		case fmt.Sprintf("cao-server-%d-%d", run.ID, run.RunAttempt):
			record.Role = "sealed-candidate"
		case fmt.Sprintf("release-promotion-evidence-%d", run.ID):
			record.Role = "promotion-evidence"
		}
		if evidenceDigestPattern.MatchString(artifact.Digest) {
			record.Digest = &artifact.Digest
		}
		if evidenceTimestamp(artifact.ExpiresAt) {
			record.ExpiresAt = &artifact.ExpiresAt
		}
		result = append(result, record)
	}
	return evidenceRecords[evidenceArtifact]{evidenceState: state, AttemptBinding: "unknown", Records: result}
}

func (c evidenceClient) approvals(runID int64) evidenceRecords[evidenceApproval] {
	output := evidenceRecords[evidenceApproval]{
		evidenceState: evidenceUnknown("unavailable-or-invalid"), AttemptBinding: "unknown", IndependentApproval: "unknown",
	}
	var reviews []evidenceAPIApproval
	if c.get(fmt.Sprintf("actions/runs/%d/approvals", runID), &reviews) != nil || reviews == nil || len(reviews) > 100 {
		return output
	}
	if len(reviews) == 0 {
		output.evidenceState = evidenceUnknown("no-approval-records")
		return output
	}
	result := make([]evidenceApproval, 0, len(reviews))
	for _, review := range reviews {
		state := review.State
		if state != "approved" && state != "rejected" && state != "pending" {
			state = "unknown"
		}
		environments := make([]*int64, 0, min(len(review.Environments), 100))
		for _, env := range review.Environments[:min(len(review.Environments), 100)] {
			environments = append(environments, evidenceIDPointer(env.ID))
		}
		result = append(result, evidenceApproval{
			State: state, ActorID: evidenceIDPointer(review.User.ID), EnvironmentIDs: environments,
			EnvironmentsComplete: review.Environments != nil && len(review.Environments) <= 100,
		})
	}
	output.evidenceState, output.Records = evidenceState{Status: "observed"}, result
	return output
}

func (c evidenceClient) run(runID int64, workflow, revision string, product bool) (evidenceRun, error) {
	var run evidenceAPIRun
	if err := c.get(fmt.Sprintf("actions/runs/%d", runID), &run); err != nil {
		return evidenceRun{}, err
	}
	if run.ID != runID || !evidenceID(run.RunAttempt) || !evidenceRevisionPattern.MatchString(run.HeadSHA) ||
		!strings.EqualFold(run.Repository.FullName, c.repository) || run.Path != ".github/workflows/"+workflow {
		return evidenceRun{}, errors.New("run identity does not match the selected repository and workflow")
	}
	reference := fmt.Sprintf("https://github.com/%s/actions/runs/%d/attempts/%d", c.repository, runID, run.RunAttempt)
	binding := "unknown"
	if product && run.Event == "push" && run.HeadSHA == revision {
		binding = "push-revision-matches"
	}
	return evidenceRun{
		ID: runID, Attempt: run.RunAttempt, Workflow: run.Path, WorkflowRevision: run.HeadSHA,
		SourceBinding: binding, Conclusion: evidenceConclusion(run.Conclusion), Reference: reference,
		Jobs: c.jobs(run, reference), Artifacts: c.artifacts(run), Approvals: c.approvals(runID),
	}, nil
}

func collectReleaseEvidence(ctx context.Context, deps evidenceDeps, options evidenceReleaseOptions) (evidenceReleaseReport, error) {
	if err := options.validate(); err != nil {
		return evidenceReleaseReport{}, err
	}
	client := evidenceClient{ctx: ctx, deps: deps, repository: options.Repository}
	identity, err := deps.identity()
	if err != nil || !evidenceHashPattern.MatchString(identity) {
		return evidenceReleaseReport{}, errors.New("collector executable identity unavailable")
	}
	tool := evidenceTool{evidenceState: evidenceUnknown("unavailable-or-invalid")}
	versionCtx, cancel := context.WithTimeout(ctx, time.Minute)
	version, versionErr := deps.gh(versionCtx, "--version")
	cancel()
	if match := regexp.MustCompile(`^gh version ([0-9]+\.[0-9]+\.[0-9]+)(?:[ +(\-]|$)`).FindStringSubmatch(version); versionErr == nil && len(version) <= 4096 && len(match) == 2 {
		tool = evidenceTool{evidenceState: evidenceState{Status: "observed"}, Version: match[1]}
	}
	build, err := client.run(options.PackageRun, "cao-package.yml", options.Revision, true)
	if err != nil {
		return evidenceReleaseReport{}, err
	}
	promotion := evidencePromotion{evidenceState: evidenceUnknown("not-selected")}
	if options.PromotionRun != 0 {
		run, err := client.run(options.PromotionRun, "release-promote.yml", options.Revision, false)
		if err != nil {
			return evidenceReleaseReport{}, err
		}
		promotion = evidencePromotion{evidenceState: evidenceState{Status: "observed"}, evidenceRun: &run}
	}
	provenance := evidenceProvenance{evidenceState: evidenceUnknown("verification-not-selected")}
	if options.Verify {
		provenance = client.provenance(options, build.Reference)
	}
	deployment := evidenceState{Status: "unknown", Reason: "deployment-evidence-not-collected"}
	if ctx.Err() != nil {
		return evidenceReleaseReport{}, ctx.Err()
	}
	binding := "unknown"
	if provenance.Status == "verified-by-gh" {
		binding = "verified-by-gh"
	}
	return evidenceReleaseReport{
		Kind: "cao.release.evidence", SchemaVersion: 1, CollectedAt: deps.now().UTC().Format(time.RFC3339),
		Status: "incomplete", Repository: options.Repository, SourceRevision: options.Revision, Image: options.Image,
		Collector:   evidenceCollector{Version: deps.version, GoVersion: runtime.Version(), BinarySHA256: identity, GitHubCLI: tool},
		Attribution: "owner-invoked-read-only-github-collection", Package: build, Promotion: promotion,
		Provenance: provenance, Deployment: deployment, ImageSourceBinding: binding,
		DeployedImageDigest:  evidenceUnknown("configured-read-back-is-not-running-image-measurement"),
		IndependentRetention: evidenceUnknown("owner-must-export-and-verify"),
		Governance:           "not-assessed", OperatingEffectiveness: "not-assessed",
	}, nil
}
