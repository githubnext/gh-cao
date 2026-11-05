package collectnative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/mining"
)

type Claim struct {
	Identity
	Token      string
	Epoch      int64
	Generation int64
}

// Store owns durable attempt claims and safe evidence, not canonical entities.
// Publication must atomically fence the scope epoch, token and database expiry.
// Pending evidence is an outbox: acquisition success is not canonical activation.
type Store interface {
	Claim(context.Context, Identity, time.Duration) (Claim, error)
	Publish(context.Context, Claim, Evidence) error
	Release(context.Context, Claim) error
}

// ScopedStore fences a dispatched request against its admission-time epoch,
// not the repository's possibly newer enrollment at execution time.
type ScopedStore interface {
	Store
	ClaimAtEpoch(context.Context, Identity, int64, time.Duration) (Claim, error)
}

type ScopedAttempt struct {
	Identity
	Epoch int64
}

type EnrollmentScope struct {
	Repository     string
	InstallationID int64
	Epoch          int64
}

type ScopeReader interface {
	CurrentScope(context.Context, string) (EnrollmentScope, error)
}

type PublicationConsumer interface {
	Replay(context.Context, int) (int, error)
}

// Destination must durably admit approved content (or an explicit source gap)
// before returning. Admission is retried after any crash before safe publication;
// implementations must be idempotent on repository, run and exact attempt.
type Destination interface {
	Capture(context.Context, Run, []byte, string) error
}

type DestinationFunc func(context.Context, Run, []byte, string) error

func (f DestinationFunc) Capture(ctx context.Context, run Run, content []byte, gap string) error {
	return f(ctx, run, content, gap)
}

type Collector struct {
	Provider     Provider
	Store        Store
	Limits       Limits
	Timeout      time.Duration
	ClaimTTL     time.Duration
	Destinations []Destination
	Audit        AuditRequest
}

func (c Collector) Validate() error {
	if err := c.Audit.Validate(); err != nil {
		return err
	}
	if c.Provider == nil || c.Store == nil {
		return errors.New("native acquisition requires a provider and durable evidence store")
	}
	if c.timeout() >= c.claimTTL() {
		return errors.New("native acquisition timeout must be shorter than its claim TTL")
	}
	if len(c.Destinations) > 4 {
		return errors.New("native acquisition supports at most four declared destinations")
	}
	for _, destination := range c.Destinations {
		if destination == nil {
			return errors.New("native acquisition destination is required")
		}
	}
	return nil
}

func (c Collector) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 10 * time.Minute
	}
	return c.Timeout
}

func (c Collector) claimTTL() time.Duration {
	if c.ClaimTTL <= 0 {
		return 15 * time.Minute
	}
	return c.ClaimTTL
}

// Collect acquires, safely projects and durably publishes one repository.
// Already published attempts remain pending until an independent consumer
// activates them. No task ACK or canonical success is implied by this method.
func (c Collector) Collect(ctx context.Context, repository string, installation int64, since time.Time) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if !coordinate.MatchString(repository) || installation <= 0 {
		return ErrIdentity
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	count := 0
	return c.Provider.Runs(ctx, repository, installation, since, func(run Run) error {
		count++
		if count > c.Limits.defaults().Runs {
			return ErrCapacity
		}
		if run.Validate() != nil || run.Repository != repository || run.InstallationID != installation || !workflowPath.MatchString(run.WorkflowPath) {
			return ErrIdentity
		}
		return c.collectRun(ctx, run)
	})
}

func (c Collector) CollectAttempt(ctx context.Context, identity Identity) error {
	return c.collectAttempt(ctx, identity, 0)
}

func (c Collector) CollectScopedAttempt(ctx context.Context, request ScopedAttempt) error {
	if request.Epoch <= 0 {
		return ErrIdentity
	}
	if _, ok := c.Store.(ScopedStore); !ok {
		return ErrUnsupported
	}
	return c.collectAttempt(ctx, request.Identity, request.Epoch)
}

func (c Collector) collectAttempt(ctx context.Context, identity Identity, epoch int64) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := identity.Validate(); err != nil {
		return err
	}
	provider, supported := c.Provider.(AttemptProvider)
	if !supported {
		return ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	var claim Claim
	var err error
	if epoch > 0 {
		claim, err = c.Store.(ScopedStore).ClaimAtEpoch(ctx, identity, epoch, c.claimTTL())
	} else {
		claim, err = c.Store.Claim(ctx, identity, c.claimTTL())
	}
	if err != nil {
		return err
	}
	defer c.releaseClaim(ctx, claim)
	run, err := provider.Attempt(ctx, identity)
	if err != nil {
		return err
	}
	if run.Identity != identity || !workflowPath.MatchString(run.WorkflowPath) {
		return ErrIdentity
	}
	return c.publishRun(ctx, claim, run)
}

func (c Collector) collectRun(ctx context.Context, run Run) error {
	claim, err := c.Store.Claim(ctx, run.Identity, c.claimTTL())
	if err != nil {
		return err
	}
	defer c.releaseClaim(ctx, claim)
	return c.publishRun(ctx, claim, run)
}

func (c Collector) releaseClaim(ctx context.Context, claim Claim) {
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer stop()
	// Publication clears the claim. A lost release cannot authorize a
	// stale publication; database expiry remains the recovery path.
	_ = c.Store.Release(cleanup, claim)
}

func (c Collector) publishRun(ctx context.Context, claim Claim, run Run) error {
	evidence, err := c.extract(ctx, run)
	if err != nil {
		return err
	}
	return c.Store.Publish(ctx, claim, evidence)
}

func (c Collector) extract(ctx context.Context, run Run) (Evidence, error) {
	owner, _, _ := strings.Cut(run.Repository, "/")
	fields := map[string]any{
		"repository": run.Repository, "run_id": run.RunID, "run_attempt": run.Attempt, "workflow_path": run.WorkflowPath,
		"organization": owner, "workflow_name": run.WorkflowPath,
	}
	for native, retained := range map[string]string{
		"name": "workflow_name", "display_title": "display_title", "event": "event", "status": "status", "conclusion": "conclusion",
		"head_branch": "head_branch", "head_sha": "head_sha", "created_at": "created_at", "updated_at": "updated_at",
		"run_started_at": "started_at", "html_url": "url",
	} {
		if value, present := run.Fields[native]; present {
			if value == nil {
				fields[retained] = nil
			} else if _, ok := value.(string); ok {
				fields[retained] = value
			} else {
				return Evidence{}, errors.New("invalid native run field")
			}
		}
	}
	evidence := Evidence{Version: 1, Identity: run.Identity, Run: fields}
	files := map[string][]byte{}
	bytesUsed := 0
	artifacts := 0
	err := c.Provider.Artifacts(ctx, run, func(artifact Artifact) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		artifacts++
		if artifacts > c.Limits.defaults().Artifacts {
			return ErrCapacity
		}
		if !artifactName(artifact.Name) || path.Base(artifact.Name) != artifact.Name {
			return ErrIdentity
		}
		names := make([]string, 0, len(artifact.Files))
		for name := range artifact.Files {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			content := artifact.Files[name]
			bytesUsed += len(content)
			if bytesUsed > c.Limits.defaults().Bytes {
				return ErrCapacity
			}
			if path.Clean(name) != name || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
				return ErrIdentity
			}
			if path.Base(name) == "aw_info.json" {
				var info map[string]any
				if err := decode(content, &info); err != nil {
					evidence.Gaps = append(evidence.Gaps, "artifact-identity-unavailable")
					continue
				}
				if strings.EqualFold(scalar(info["repository"]), run.Repository) &&
					scalar(info["run_id"]) == strconv.FormatInt(run.RunID, 10) && scalar(info["run_attempt"]) == strconv.Itoa(run.Attempt) {
					if err := validateAuditEngine(info["engine_id"]); err != nil {
						return err
					}
					context, err := recordedContext(object(info["context"]))
					if err != nil {
						return err
					}
					if context.EpisodeID != "" {
						if err := mergeContext(&evidence.Contexts, context); err != nil {
							return err
						}
					}
					files[artifact.Name+"/"+name] = content
				}
				continue
			}
			info, err := artifactIdentity(artifact.Files, name)
			if err != nil {
				evidence.Gaps = append(evidence.Gaps, "artifact-identity-unavailable")
				continue
			}
			if strings.ToLower(scalar(info["repository"])) != run.Repository || scalar(info["run_id"]) != strconv.FormatInt(run.RunID, 10) {
				evidence.Gaps = append(evidence.Gaps, "artifact-identity-mismatch")
				continue
			}
			attempt, err := strconv.Atoi(scalar(info["run_attempt"]))
			if err != nil || attempt <= 0 {
				evidence.Gaps = append(evidence.Gaps, "artifact-identity-unavailable")
				continue
			}
			if attempt != run.Attempt {
				evidence.Gaps = append(evidence.Gaps, "artifact-other-attempt")
				continue
			}
			location := artifact.Name + "/" + name
			if _, duplicate := files[location]; duplicate {
				return errors.New("ambiguous native artifact evidence")
			}
			files[location] = content
		}
		return nil
	})
	if err != nil {
		return Evidence{}, err
	}
	var session map[string]any
	sessions := candidates(files, "aw_session.jsonl")
	var content []byte
	gap := "missing-session"
	if len(sessions) > 1 {
		gap = "ambiguous-session"
	} else if len(sessions) == 1 {
		content, gap = files[sessions[0]], ""
	}
	if len(sessions) == 0 && slices.ContainsFunc(evidence.Gaps, func(gap string) bool {
		return gap == "artifact-identity-unavailable" || gap == "artifact-identity-mismatch" || gap == "artifact-other-attempt"
	}) {
		gap = "artifact-identity-gap"
	}
	// Each enabled destination owns policy/redaction and durable admission.
	// Raw session bytes never enter the safe acquisition store.
	for _, destination := range c.Destinations {
		if err := destination.Capture(ctx, run, content, gap); err != nil {
			return Evidence{}, err
		}
	}
	jobGaps, err := acquireJobs(ctx, c.Provider, run, fields, c.Limits, &bytesUsed)
	if err != nil {
		return Evidence{}, err
	}
	evidence.Gaps = append(evidence.Gaps, jobGaps...)
	if len(sessions) > 1 {
		evidence.Gaps = append(evidence.Gaps, "ambiguous-session")
	} else if len(sessions) == 1 {
		projected, gaps, err := ProjectSession(ctx, files[sessions[0]], sessions[0], run.Identity, c.Limits)
		switch {
		case errors.Is(err, ErrCapacity), ctx.Err() != nil:
			return Evidence{}, err
		case err != nil:
			evidence.Gaps = append(evidence.Gaps, "invalid-session")
		default:
			session = projected
			evidence.Gaps = append(evidence.Gaps, gaps...)
		}
	}
	if session == nil {
		evidence.Gaps = append(evidence.Gaps, "session-unavailable")
	}
	if err := supplementArtifacts(fields, session, files, run.Identity); err != nil {
		evidence.Gaps = append(evidence.Gaps, "invalid-supplement")
	}
	auditGaps, err := enrichAudit(ctx, fields, files, run.Identity, c.Limits)
	if err != nil {
		return Evidence{}, err
	}
	evidence.Gaps = append(evidence.Gaps, auditGaps...)
	if session != nil {
		fields["cao_unified_session"] = session
	}
	slices.Sort(evidence.Gaps)
	evidence.Gaps = slices.Compact(evidence.Gaps)
	slices.SortFunc(evidence.Contexts, func(a, b mining.Context) int { return strings.Compare(a.HopID, b.HopID) })
	encoded, _, err := evidence.Bytes()
	if err != nil {
		return Evidence{}, err
	}
	if len(encoded) > c.Limits.defaults().Bytes {
		return Evidence{}, ErrCapacity
	}
	// Publish the compactor's retained run, not the pre-compaction input.
	envelope, err := json.Marshal(map[string]any{"schema_version": 5, "kind": "run", "run": fields})
	if err != nil {
		return Evidence{}, err
	}
	var compacted bytes.Buffer
	if err := Compact(ctx, run.Repository, bytes.NewReader(envelope), &compacted, c.Limits); err != nil {
		return Evidence{}, err
	}
	if compacted.Len() == 0 {
		return Evidence{}, errors.New("native compaction discarded owned run evidence")
	}
	var retained struct {
		Run map[string]any `json:"run"`
	}
	if err := decode(compacted.Bytes(), &retained); err != nil || retained.Run == nil {
		return Evidence{}, errors.New("native compaction did not produce one retained run")
	}
	evidence.Run = retained.Run
	return evidence, nil
}

func candidates(files map[string][]byte, name string) []string {
	result := []string{}
	for location := range files {
		if path.Base(location) == name {
			result = append(result, location)
		}
	}
	slices.Sort(result)
	return result
}

func artifactIdentity(files map[string][]byte, name string) (map[string]any, error) {
	directory := path.Dir(name)
	for {
		if content, present := files[path.Join(directory, "aw_info.json")]; present {
			var info map[string]any
			if err := decode(content, &info); err != nil || info == nil {
				return nil, ErrIdentity
			}
			return info, nil
		}
		if directory == "." {
			break
		}
		directory = path.Dir(directory)
	}
	// Pinned Copilot/routed agent archives stage identity at runtime/aw_info.json,
	// beside rather than above their logs. Never borrow another archive's identity.
	if content, present := files["runtime/aw_info.json"]; present {
		var info map[string]any
		if err := decode(content, &info); err != nil || info == nil {
			return nil, ErrIdentity
		}
		return info, nil
	}
	return nil, ErrIdentity
}

func supplementArtifacts(run, session map[string]any, files map[string][]byte, identity Identity) error {
	var failures []error
	if session["experiments"] == nil {
		var assignments map[string]any
		for _, name := range candidates(files, "assignments.json") {
			var candidate map[string]any
			if err := decode(files[name], &candidate); err != nil || candidate == nil {
				failures = append(failures, errors.New("invalid experiment assignments"))
				continue
			}
			valid := true
			for key, value := range candidate {
				valid = valid && strings.TrimSpace(key) != "" && strings.TrimSpace(text(value)) != ""
			}
			if !valid {
				failures = append(failures, errors.New("invalid experiment assignment value"))
				continue
			}
			if assignments != nil {
				previous, _ := json.Marshal(assignments)
				current, _ := json.Marshal(candidate)
				if !bytes.Equal(previous, current) {
					failures = append(failures, errors.New("ambiguous experiment assignments"))
					assignments = nil
					break
				}
			}
			assignments = candidate
		}
		if assignments != nil && len(failures) == 0 {
			run["experiments"] = map[string]any{"assignments": assignments}
		}
	}
	target := run
	if session != nil {
		target = session
	}
	if err := supplementGraders(target, files, identity); err != nil {
		failures = append(failures, err)
	}
	if session["evals"] == nil {
		names := candidates(files, "evals.jsonl")
		if len(names) > 1 {
			failures = append(failures, errors.New("ambiguous eval artifacts"))
		} else if len(names) == 1 {
			evals, err := artifactEvals(files[names[0]], identity, scalar(first(run["updated_at"], run["created_at"])))
			if err != nil {
				failures = append(failures, err)
			} else {
				run["evals"] = evals
			}
		}
	}
	return errors.Join(failures...)
}

func supplementGraders(target map[string]any, files map[string][]byte, identity Identity) error {
	names := candidates(files, "grader_results.json")
	session := object(target["graders"])
	separate, usage := []string{}, []string{}
	for _, name := range names {
		if strings.Contains(name, "usage/graders/") {
			usage = append(usage, name)
		} else {
			separate = append(separate, name)
		}
	}
	if len(separate) > 1 || (session == nil && len(separate) == 0 && len(usage) > 1) {
		return errors.New("ambiguous grader results")
	}
	var name string
	switch {
	case len(separate) == 1:
		name = separate[0]
	case session == nil && len(usage) == 1:
		name = usage[0]
	default:
		return nil
	}
	var results map[string]any
	if err := decode(files[name], &results); err != nil || (scalar(results["version"]) != "1" && scalar(results["version"]) != "2") {
		return errors.New("invalid grader results")
	}
	input, ok := results["results"].([]any)
	owned := object(results["run"])
	if !ok || (results["run"] != nil && (owned == nil ||
		(owned["id"] != nil && scalar(owned["id"]) != strconv.FormatInt(identity.RunID, 10)) ||
		(owned["attempt"] != nil && scalar(owned["attempt"]) != strconv.Itoa(identity.Attempt)))) {
		return ErrIdentity
	}
	ids := map[string]bool{}
	for _, candidate := range input {
		result := object(candidate)
		id := text(result["id"])
		if strings.TrimSpace(id) == "" || ids[id] || (result["value"] != nil && !finite(result["value"])) {
			return errors.New("invalid grader result identity or value")
		}
		if _, present := result["value"]; !present {
			return errors.New("missing grader value")
		}
		ids[id] = true
	}
	records := []sessionRecord{}
	manifestName := strings.TrimSuffix(name, "grader_results.json") + "grader_manifest.json"
	if content, present := files[manifestName]; present {
		var manifest map[string]any
		if err := decode(content, &manifest); err != nil || (scalar(manifest["version"]) != "1" && scalar(manifest["version"]) != "2") {
			return errors.New("invalid grader manifest")
		}
		if _, ok := manifest["graders"].([]any); !ok {
			return errors.New("missing grader manifest entries")
		}
		records = append(records, sessionRecord{kind: "grader.manifest", data: manifest})
	}
	records = append(records, sessionRecord{kind: "grader.result", data: results})
	document := map[string]any{}
	if err := evaluations(document, records, identity, name); err != nil {
		return err
	}
	projected := object(document["graders"])
	additional, ok := projected["results"].([]map[string]any)
	if !ok {
		return ErrIdentity
	}
	retained, _ := session["results"].([]map[string]any)
	seen := map[string]bool{}
	for _, result := range retained {
		seen[text(result["id"])] = true
	}
	for _, result := range additional {
		result["sessionPayloadRef"] = name
		if session == nil || ((result["source"] == "inline" || result["source"] == "operational-value") && !seen[text(result["id"])]) {
			retained = append(retained, result)
		}
	}
	target["graders"] = map[string]any{"results": retained}
	return nil
}

func artifactEvals(content []byte, identity Identity, observed string) ([]map[string]any, error) {
	results := []map[string]any{}
	for _, line := range bytes.Split(content, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record map[string]any
		if err := decode(line, &record); err != nil {
			return nil, errors.New("invalid eval JSON")
		}
		name, answer := text(record["id"]), strings.ToUpper(strings.TrimSpace(text(record["answer"])))
		clock, present := record["timestamp"]
		if !present {
			clock = observed
		}
		timestamp := eventClock(clock)
		owned := map[string]any{}
		for key, value := range record {
			owned[key] = value
		}
		for _, field := range []string{"runId", "run_id", "runid"} {
			if value := scalar(owned[field]); value == "github:run:"+identity.Repository+":"+strconv.FormatInt(identity.RunID, 10) {
				delete(owned, field)
			}
		}
		if strings.TrimSpace(name) == "" || (answer != "YES" && answer != "NO" && answer != "UNKNOWN") || timestamp == "" || !matches(owned, identity) {
			return nil, errors.New("invalid eval evidence")
		}
		result := map[string]any{"id": name, "answer": answer, "runId": strconv.FormatInt(identity.RunID, 10), "timestamp": timestamp}
		for _, key := range []string{"experimentId", "variant"} {
			if value, ok := record[key].(string); ok {
				result[key] = value
			}
		}
		results = append(results, result)
	}
	return results, nil
}

func recordedContext(input map[string]any) (mining.Context, error) {
	fields := map[string]string{}
	for _, name := range []string{"episode_id", "hop_id", "parent_hop_id", "repo", "run_id", "workflow_id", "root_repo", "root_run_id", "root_workflow_id"} {
		if value, present := input[name]; present {
			s := scalar(value)
			if len(s) > 2048 || (value != nil && s == "" && value != "") {
				return mining.Context{}, errors.New("invalid recorded episode context")
			}
			fields[name] = s
		}
	}
	content, err := json.Marshal(fields)
	if err != nil {
		return mining.Context{}, err
	}
	var result mining.Context
	if err := json.Unmarshal(content, &result); err != nil {
		return result, err
	}
	return result, nil
}

func mergeContext(contexts *[]mining.Context, next mining.Context) error {
	for i, previous := range *contexts {
		if previous.HopID == next.HopID {
			merged, err := previous.Merge(next)
			if err != nil {
				return err
			}
			(*contexts)[i] = merged
			return nil
		}
	}
	*contexts = append(*contexts, next)
	return nil
}
