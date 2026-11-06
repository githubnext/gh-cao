package collect

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/model"
)

type fileDietEvidenceRef struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}
type fileDietMetric struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Unit      string `json:"unit"`
	Direction string `json:"direction"`
	Version   string `json:"version"`
	Formula   string `json:"formula"`
}
type fileDietIntent struct {
	Goal         string                `json:"goal"`
	Acceptance   string                `json:"acceptance"`
	Requirements []string              `json:"requirements"`
	Sources      []fileDietEvidenceRef `json:"sources"`
}
type fileDietLineage struct {
	OutputID         string `json:"outputId"`
	ImplementationID string `json:"implementationId"`
	Relation         string `json:"relation"`
	At               string `json:"at"`
}
type fileDietEvent struct {
	ID          string
	At          string
	Value       int
	Attribution string
	Label       string
	Title       string
	Detail      string
	Reference   string
	Evidence    []fileDietEvidenceRef
	Lineage     []fileDietLineage
}
type fileDietOutput struct {
	ID                string
	Label             string
	Title             string
	URL               string
	CreatedAt         string
	State             string
	Origin            string
	ImplementationIDs []string
	Evidence          []fileDietEvidenceRef
}
type fileDietObservation struct {
	At    string
	Value int
	Ref   string
	URL   string
	Items []fileDietEvidenceRef
}
type fileDietLedger struct {
	Proof        fileDietProof
	Metric       fileDietMetric
	Snapshot     fileDietMetric
	Intent       fileDietIntent
	Events       []fileDietEvent
	Outputs      []fileDietOutput
	Observations []fileDietObservation
	Limitations  []string
	Coverage     string
}

func fileDietEncodeJSON(value any) []byte {
	var buffer bytes.Buffer
	writer := json.NewEncoder(&buffer)
	writer.SetEscapeHTML(false)
	if writer.Encode(value) != nil {
		return nil
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
}
func fileDietDigest(value any) string {
	return fmt.Sprintf("%x", sha256.Sum256(fileDietEncodeJSON(value)))
}

func fileDietFileReference(file fileDietFile, commit, prefix string) (fileDietEvidenceRef, error) {
	if !validFileDietSHA(commit) || !fileDietEligible(file.Path) {
		return fileDietEvidenceRef{}, errors.New("invalid immutable File Diet file reference")
	}
	segments := strings.Split(file.Path, "/")
	for index, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return fileDietEvidenceRef{}, errors.New("unsafe immutable File Diet file reference")
		}
		segments[index] = url.PathEscape(segment)
	}
	line := max(1, min(file.Lines, 800))
	if file.Lines < 800 {
		line = 1
	}
	return fileDietEvidenceRef{
		Label: fmt.Sprintf("%s%s (%d lines)", prefix, file.Path, file.Lines),
		URL: fmt.Sprintf("https://github.com/%s/blob/%s/%s#L%d", dailyFileDietRepository,
			commit, strings.Join(segments, "/"), line),
	}, nil
}

func fileDietReplay(proof fileDietProof, measured fileDietMeasurements) (fileDietLedger, error) {
	var result fileDietLedger
	if proof.Adoption.Commit != "1186030a620f4113f655c156bedf70cf2c164f79" ||
		proof.Adoption.At != "2025-11-15T13:36:21Z" ||
		len(proof.Snapshots) == 0 || proof.Snapshots[0].At != proof.Adoption.At ||
		proof.Snapshots[len(proof.Snapshots)-1].At != proof.Until {
		return result, errors.New("File Diet frozen adoption or snapshot cutoff changed")
	}
	root := "https://github.com/" + dailyFileDietRepository
	adopted := root + "/blob/" + proof.Adoption.Commit + "/.github/workflows"
	intent := fileDietIntent{
		Goal:       "Identify oversized production Go files and promote behavior-preserving decomposition.",
		Acceptance: "This named structural facet measures net 800+ file reductions at linked merges; full behavior-preserving acceptance remains unknown.",
		Requirements: []string{
			"Inspect the largest non-test Go source file under pkg/ and propose a focused refactoring issue when the workflow's size threshold is met.",
			"Preserve behavior and public API; keep every new file under 500 lines and require at least 80% test coverage for new files.",
			"Require passing tests, lint and build. Creating the proposal is not the same as implementing it.",
		},
		Sources: []fileDietEvidenceRef{
			{"Workflow Markdown at adoption", adopted + "/daily-file-diet.md"},
			{"Compiled workflow at adoption", adopted + "/daily-file-diet.lock.yml"},
			{"Reporting import at adoption", adopted + "/shared/reporting.md"},
			{"Serena import at adoption", adopted + "/shared/mcp/serena.md"},
		},
	}
	metric := fileDietMetric{
		"oversized-files-reduced", "Workflow-enabled oversized-file reductions", "files",
		"increase", "observed-800-line-net-reductions-v1",
		"Count regular non-test Go files under pkg/ with at least 800 newline characters before the merge, minus the same count after it, including replacements.",
	}
	snapshot := fileDietMetric{
		"oversized-files", "Oversized production Go files", "files",
		"decrease", "observed-800-line-snapshot-v1",
		"Count regular non-test Go files under pkg/ with at least 800 newline characters at the snapshot revision.",
	}
	result = fileDietLedger{Proof: proof, Metric: metric, Snapshot: snapshot, Intent: intent,
		Events: []fileDietEvent{}, Outputs: []fileDietOutput{}, Observations: []fileDietObservation{}}
	inventory := func(commit string) ([]fileDietFile, error) {
		return fileDietInventoryByRevision(measured, commit)
	}
	eligible := make([]fileDietIssue, 0)
	receipt := map[int]bool{}
	implementations := map[string][]string{}
	for _, issue := range proof.Issues {
		if issue.Eligible != nil && !*issue.Eligible || issue.Receipt != nil &&
			issue.Receipt.Status == "not-applied-to-output" {
			continue
		}
		if issue.Receipt != nil && issue.Receipt.Status == "available" {
			if issue.Reference.RunID == nil || issue.Run.Validation == nil ||
				strconv.FormatInt(issue.Receipt.RunID, 10) != *issue.Reference.RunID ||
				issue.Receipt.HeadSHA != issue.Run.Validation.HeadSHA ||
				fileDietRawBytesDigest([]byte(issue.Receipt.Content)) != issue.Receipt.EntrySHA256 ||
				issue.Receipt.Digest != "sha256:"+issue.Receipt.ArchiveSHA256 {
				return result, errors.New("File Diet applied receipt is not verified")
			}
			var source struct {
				Type     string `json:"type"`
				Number   int    `json:"number"`
				Repo     string `json:"repo"`
				URL      string `json:"url"`
				Metadata *struct {
					NodeID string `json:"node_id"`
				} `json:"metadata"`
			}
			if err := json.Unmarshal([]byte(issue.Receipt.Content), &source); err != nil ||
				source.Type != "create_issue" || source.Number != issue.Number ||
				source.Repo != dailyFileDietRepository && source.Repo != "githubnext/gh-aw" ||
				source.URL != fmt.Sprintf("https://github.com/%s/issues/%d", source.Repo, source.Number) ||
				source.Metadata != nil && source.Metadata.NodeID != issue.ID {
				return result, errors.New("File Diet creation receipt does not identify output")
			}
			receipt[issue.Number] = true
		}
		eligible = append(eligible, issue)
		implementations[issue.ID] = []string{}
	}
	var undatedLinks int
	for _, pull := range proof.Pulls {
		if pull.MergedAt == nil || !fileDietTime(*pull.MergedAt).Before(fileDietTime(proof.Until)) {
			continue
		}
		if pull.MergeSHA == nil || pull.PreMergeSHA == "" ||
			pull.Graph.ID != pull.ID || pull.Graph.State != "MERGED" ||
			pull.Graph.MergeCommit == nil || *pull.Graph.MergeCommit != *pull.MergeSHA ||
			pull.Graph.MergedAt == nil || *pull.Graph.MergedAt != *pull.MergedAt {
			return result, errors.New("File Diet accepted implementation revision mismatch or not merged")
		}
		linked := []fileDietIssue{}
		lineage := []fileDietLineage{}
		for _, issue := range eligible {
			if fileDietTime(issue.CreatedAt).After(fileDietTime(pull.CreatedAt)) {
				continue
			}
			var connection *fileDietTimelineEvent
			for index := range issue.Timeline.Events {
				event := &issue.Timeline.Events[index]
				if event.Target != nil && event.Target.Kind == "pull-request" &&
					event.Target.Number == pull.Number &&
					(event.Type == "connected" || event.Type == "disconnected") &&
					!fileDietTime(event.At).After(fileDietTime(*pull.MergedAt)) &&
					(connection == nil || !fileDietTime(event.At).Before(fileDietTime(connection.At))) {
					connection = event
				}
			}
			connected := connection != nil && connection.Type == "connected"
			closing := false
			var closedBy, referencedBy *fileDietEdge
			for index := range pull.Graph.Edges {
				edge := &pull.Graph.Edges[index]
				if edge.Relation == "declares-closing" && edge.Target.ID == issue.ID {
					closing = true
				}
			}
			for index := range issue.Graph.Edges {
				edge := &issue.Graph.Edges[index]
				if edge.Target.ID != pull.ID {
					continue
				}
				if edge.Relation == "closed-by" && !fileDietTime(edge.At).Before(fileDietTime(*pull.MergedAt)) &&
					!fileDietTime(edge.At).After(fileDietTime(proof.ObservedAt)) && closedBy == nil {
					closedBy = edge
				}
				if edge.Relation == "referenced-by" && !fileDietTime(edge.At).After(fileDietTime(*pull.MergedAt)) &&
					referencedBy == nil {
					referencedBy = edge
				}
			}
			if closing && !connected && closedBy == nil {
				undatedLinks++
			}
			if !closing && !connected {
				continue
			}
			linked = append(linked, issue)
			implementations[issue.ID] = append(implementations[issue.ID], pull.ID)
			if referencedBy != nil {
				lineage = append(lineage, fileDietLineage{issue.ID, pull.ID, "referenced-by", referencedBy.At})
			}
			if connected {
				lineage = append(lineage, fileDietLineage{issue.ID, pull.ID, "connected", connection.At})
			}
			if closedBy != nil {
				lineage = append(lineage, fileDietLineage{issue.ID, pull.ID, "closed-by", closedBy.At})
			}
		}
		if len(linked) == 0 {
			continue
		}
		before, err := inventory(pull.PreMergeSHA)
		if err != nil {
			return result, err
		}
		after, err := inventory(*pull.MergeSHA)
		if err != nil {
			return result, err
		}
		oversized := func(files []fileDietFile) int {
			count := 0
			for _, file := range files {
				if file.Lines >= 800 {
					count++
				}
			}
			return count
		}
		byPath := func(files []fileDietFile, path string) *fileDietFile {
			for index := range files {
				if files[index].Path == path {
					return &files[index]
				}
			}
			return nil
		}
		targetPaths := []string{}
		for _, issue := range linked {
			if issue.TargetPath != nil && !slices.Contains(targetPaths, *issue.TargetPath) {
				targetPaths = append(targetPaths, *issue.TargetPath)
			}
		}
		measurement := func(file *fileDietFile) string {
			if file == nil {
				return "absent"
			}
			return strconv.Itoa(file.Lines)
		}
		targets := []string{}
		refs := []fileDietEvidenceRef{}
		for _, target := range targetPaths {
			original, accepted := byPath(before, target), byPath(after, target)
			targets = append(targets, fmt.Sprintf("%s: %s -> %s lines", target, measurement(original), measurement(accepted)))
			for _, candidate := range []struct {
				file   *fileDietFile
				commit string
				prefix string
			}{{original, pull.PreMergeSHA, "Before: "}, {accepted, *pull.MergeSHA, "After: "}} {
				if candidate.file != nil {
					ref, err := fileDietFileReference(*candidate.file, candidate.commit, candidate.prefix)
					if err != nil {
						return result, err
					}
					refs = append(refs, ref)
				}
			}
		}
		additions := []string{}
		for _, replacement := range pull.Files {
			if replacement.Status != "added" && replacement.Status != "renamed" ||
				!fileDietEligible(replacement.Path) {
				continue
			}
			accepted := byPath(after, replacement.Path)
			additions = append(additions, fmt.Sprintf("%s (%s)", replacement.Path, measurement(accepted)))
			if accepted != nil && !slices.Contains(targetPaths, accepted.Path) {
				ref, err := fileDietFileReference(*accepted, *pull.MergeSHA, "Added/renamed: ")
				if err != nil {
					return result, err
				}
				refs = append(refs, ref)
			}
		}
		evidence := []fileDietEvidenceRef{
			{"Merged PR", fmt.Sprintf("%s/pull/%d", root, pull.Number)},
			{"Accepted change diff", root + "/compare/" + pull.PreMergeSHA + "..." + *pull.MergeSHA},
		}
		evidence = append(evidence, refs...)
		for _, issue := range linked {
			evidence = append(evidence, fileDietEvidenceRef{
				fmt.Sprintf("Origin issue #%d", issue.Number),
				fmt.Sprintf("%s/issues/%d", root, issue.Number),
			})
		}
		evidence = append(evidence, fileDietEvidenceRef{"Before revision", root + "/tree/" + pull.PreMergeSHA},
			fileDietEvidenceRef{"Merge revision", root + "/tree/" + *pull.MergeSHA})
		attribution := "corroborated"
		for _, issue := range linked {
			if receipt[issue.Number] {
				attribution = "receipt-verified"
			}
		}
		detail := strings.Join(targets, ". ")
		if len(additions) != 0 {
			detail += ". Added " + strings.Join(additions, ", ")
		}
		detail += ". Net change includes replacement oversized files. Behavior preservation and full acceptance remain unverified. Shared workflow-enabled contribution."
		result.Events = append(result.Events, fileDietEvent{
			ID: pull.ID, At: *pull.MergedAt, Value: oversized(before) - oversized(after),
			Attribution: attribution, Label: fmt.Sprintf("%s#%d", dailyFileDietRepository, pull.Number),
			Title: pull.Title, Detail: detail, Reference: *pull.MergeSHA,
			Evidence: evidence, Lineage: lineage,
		})
	}
	complete := len(proof.Discovery) != 0
	for _, discovery := range proof.Discovery {
		complete = complete && discovery.Complete
	}
	for _, issue := range proof.Issues {
		complete = complete && issue.Timeline.Complete && issue.Graph.HistoryComplete
	}
	for _, pull := range proof.Pulls {
		if pull.MergedAt != nil {
			complete = complete && pull.Graph.ClosingLinksComplete && pull.Graph.HistoryComplete
		}
	}
	result.Coverage = "partial"
	if complete {
		result.Coverage = "complete"
	}
	for _, issue := range eligible {
		state := "unknown"
		if issue.Graph.HistoryComplete {
			state = "open"
			events := make([]fileDietGraphEvent, 0)
			for _, event := range issue.Graph.Events {
				if (event.Type == "closed" || event.Type == "reopened") &&
					fileDietTime(event.At).Before(fileDietTime(proof.Until)) {
					events = append(events, event)
				}
			}
			sort.SliceStable(events, func(i, j int) bool {
				return fileDietTime(events[i].At).Before(fileDietTime(events[j].At))
			})
			if len(events) > 0 && events[len(events)-1].Type == "closed" {
				state = "closed"
			}
		}
		refs := []fileDietEvidenceRef{}
		if issue.Run.Validation != nil && issue.Run.Validation.URL != "" {
			refs = append(refs, fileDietEvidenceRef{"Referenced producer run", issue.Run.Validation.URL})
		}
		origin := "corroborated"
		if receipt[issue.Number] {
			origin = "receipt-verified"
		}
		result.Outputs = append(result.Outputs, fileDietOutput{
			ID: issue.ID, Label: fmt.Sprintf("%s#%d", dailyFileDietRepository, issue.Number),
			Title: issue.Title, URL: fmt.Sprintf("%s/issues/%d", root, issue.Number),
			CreatedAt: issue.CreatedAt, State: state, Origin: origin,
			ImplementationIDs: implementations[issue.ID], Evidence: refs,
		})
	}
	for _, snapshot := range proof.Snapshots {
		files, err := inventory(snapshot.Commit)
		if err != nil {
			return result, err
		}
		refs := []fileDietEvidenceRef{}
		var count int
		for _, file := range files {
			if file.Lines >= 800 {
				count++
				ref, err := fileDietFileReference(file, snapshot.Commit, "")
				if err != nil {
					return result, err
				}
				refs = append(refs, ref)
			}
		}
		result.Observations = append(result.Observations, fileDietObservation{
			At: snapshot.At, Value: count, Ref: snapshot.Commit,
			URL: root + "/tree/" + snapshot.Commit, Items: refs,
		})
	}
	notes := []string{
		"Coverage is the surviving discovered File Diet issue cohort and its explicit merge links, not deleted outputs or an exclusive causal effect.",
		"Intent is consolidated from the adoption-time Markdown and imports. Its original issue-creation threshold was 1000 lines; the preserved retrospective instrument measures the separately named 800+ structural facet, not unchanged full acceptance.",
	}
	for _, issue := range proof.Issues {
		if issue.Eligible != nil && !*issue.Eligible || issue.Receipt != nil && issue.Receipt.Status == "not-applied-to-output" {
			notes = append(notes, "Tracked auxiliary outputs and artifacts naming a different created output remain in the lifecycle proof but are not credited as eligible File Diet deliveries.")
			break
		}
	}
	if !complete {
		notes = append(notes, "Discovery or relationship history is incomplete; observed events do not establish complete-period totals.")
	}
	if undatedLinks > 0 {
		notes = append(notes, fmt.Sprintf("%d retained closing links lack a dated connection or closed-by event; these corroborate lineage rather than prove when that relationship was recorded.", undatedLinks))
	}
	notes = append(notes,
		"Creation receipts strengthen attribution within the observed contribution; they do not cap its independently measured native change.",
		"Full behavior preservation, required coverage and all-new-files-under-500 acceptance remain unverified; this measures structural progress.",
		fmt.Sprintf("The repository oversized-file count changes from %d to %d. Other actors' growth is not attributed to this workflow.",
			result.Observations[0].Value, result.Observations[len(result.Observations)-1].Value),
		"This contribution pulse is not a continuous repository-state or response-latency series; sparse repository snapshots cannot locate changes between their cutoffs.")
	if proof.Acquisition != nil {
		notes = append(notes, "GitHub relationships were freshly collected; immutable inventories and verified receipts are reused by digest where available. Expired receipts remain unavailable unless previously preserved.")
	} else {
		notes = append(notes, "Replay validates preserved digests and identities; it does not reacquire Git objects, historical links or expired receipt archives.")
	}
	result.Limitations = notes
	return result, nil
}

func fileDietExport(ledger fileDietLedger) (model.Row, error) {
	proof, metric, snapshot := ledger.Proof, ledger.Metric, ledger.Snapshot
	if ledger.Coverage != "complete" || len(ledger.Observations) < 2 ||
		!fileDietTime(proof.Until).After(fileDietTime(proof.Adoption.At)) ||
		fileDietTime(proof.Until).After(fileDietTime(proof.ObservedAt)) {
		return nil, errors.New("incomplete File Diet acquisition; retaining accepted proof")
	}
	type contractInput struct {
		Intent         fileDietIntent `json:"intent"`
		Metric         fileDietMetric `json:"metric"`
		Basis          string         `json:"basis"`
		Snapshot       fileDietMetric `json:"snapshot"`
		CadenceDays    int            `json:"cadenceDays"`
		MaturationDays int            `json:"maturationDays"`
	}
	contract := fileDietDigest(contractInput{ledger.Intent, metric, "workflow-contribution", snapshot, 1, 0})
	facts := []any{}
	add := func(kind, id string, fields model.Row) {
		fact := map[string]any{"fact-kind": kind, "fact-id": id}
		for key, value := range fields {
			if value != nil {
				fact[key] = value
			}
		}
		facts = append(facts, fact)
	}
	add("contract", "contract", model.Row{
		"goal": ledger.Intent.Goal, "acceptance": ledger.Intent.Acceptance,
		"metric-id": metric.ID, "metric-name": metric.Name,
		"metric-unit": metric.Unit, "metric-direction": metric.Direction,
		"metric-version": metric.Version, "metric-formula": metric.Formula,
		"result-label": "file reductions", "measurement-basis": "workflow-contribution",
		"snapshot-name": snapshot.Name, "snapshot-unit": snapshot.Unit,
		"snapshot-direction": snapshot.Direction, "snapshot-id": snapshot.ID,
		"snapshot-version": snapshot.Version, "snapshot-formula": snapshot.Formula,
		"outputs-available": true, "repository-state-available": true, "checks-available": true,
		"cadence-days": 1, "maturation-days": 0, "output-unit": "issues",
		"implementation-unit": "merged PRs",
		"output-scope":        "Retained eligible File Diet proposal issues discovered through tracking markers and named producer references. Deleted or undiscovered outputs are not reconstructed.",
		"output-coverage":     ledger.Coverage, "workflow-name": "Daily File Diet",
	})
	refs := func(kind, parent string, items []fileDietEvidenceRef) {
		seen := map[string]bool{}
		for _, item := range items {
			id := fileDietDigest([]any{item.Label, item.URL, nil})
			if seen[id] {
				continue
			}
			seen[id] = true
			fields := model.Row{"label": item.Label, "url": item.URL}
			if parent != "" {
				fields["parent-id"] = parent
			}
			add(kind, id, fields)
		}
	}
	refs("intent-source", "", ledger.Intent.Sources)
	seen := map[string]bool{}
	for _, requirement := range ledger.Intent.Requirements {
		if !seen[requirement] {
			add("requirement", fileDietDigest(requirement), model.Row{"label": requirement})
			seen[requirement] = true
		}
	}
	for _, output := range ledger.Outputs {
		add("output", output.ID, model.Row{"label": output.Label, "title": output.Title,
			"url": output.URL, "at": output.CreatedAt, "state": output.State, "origin": output.Origin})
		for _, id := range output.ImplementationIDs {
			add("output-implementation", id, model.Row{"parent-id": output.ID, "related-id": id})
		}
		refs("output-evidence", output.ID, output.Evidence)
	}
	outputURL := map[string]string{}
	for _, output := range ledger.Outputs {
		outputURL[output.ID] = output.URL
	}
	for _, event := range ledger.Events {
		var link string
		for _, ref := range event.Evidence {
			if strings.Contains(strings.ToLower(ref.Label), "implement") ||
				strings.Contains(strings.ToLower(ref.Label), "pull") ||
				strings.Contains(ref.Label, "PR") {
				link = ref.URL
				break
			}
		}
		fields := model.Row{"at": event.At, "accepted-at": event.At, "value": event.Value,
			"label": event.Label, "detail": event.Detail, "ref": event.Reference,
			"origin": event.Attribution}
		if event.Title != "" {
			fields["title"] = event.Title
		}
		if link != "" {
			fields["url"] = link
		}
		add("implementation", event.ID, fields)
		refs("implementation-evidence", event.ID, event.Evidence)
		for _, edge := range event.Lineage {
			add("implementation-lineage", fileDietDigest(edge), model.Row{
				"parent-id": event.ID, "related-id": edge.OutputID,
				"label": edge.Relation, "at": edge.At, "url": outputURL[edge.OutputID],
			})
		}
	}
	for _, observation := range ledger.Observations {
		id := fileDietDigest([]string{observation.At, observation.Ref})
		add("snapshot", id, model.Row{"at": observation.At, "value": observation.Value,
			"ref": observation.Ref, "url": observation.URL, "items-available": true})
		refs("snapshot-item", id, observation.Items)
	}
	checks := []struct{ name, status, detail string }{
		{"Structural change measured", "not-verified", "No linked implementation was measured; output creation alone does not establish a structural result."},
		{"Behavior preserved", "not-verified", "This evidence does not independently establish unchanged behavior for every implementation."},
		{"All quality requirements met", "not-verified", "Required coverage and the size of every new file are not fully verified by this structural measure."},
	}
	if len(ledger.Events) > 0 {
		checks[0].status = "verified"
		checks[0].detail = "File counts were checked before and after linked merges, including replacement files and zero or negative changes."
	}
	for _, check := range checks {
		add("quality-check", fileDietDigest(check.name), model.Row{
			"label": check.name, "status": check.status, "detail": check.detail})
	}
	add("coverage", fileDietDigest([]string{proof.Adoption.At, proof.Until}), model.Row{
		"at": proof.Adoption.At, "end-at": proof.Until, "coverage-status": ledger.Coverage})
	seen = map[string]bool{}
	for _, limitation := range ledger.Limitations {
		if !seen[limitation] {
			add("limitation", fileDietDigest(limitation), model.Row{
				"label": "Evidence limitation", "detail": limitation})
			seen[limitation] = true
		}
	}
	add("limitation", "repository-state", model.Row{
		"label":  "Independent repository state",
		"detail": "These snapshots include everyone's changes. They are sparse observations, not continuous monitoring; changes between cutoffs and their causes are unknown.",
	})
	packet := model.Row{
		"schemaVersion": 1, "id": "operational-value-evidence:" +
			url.QueryEscape(strings.ToLower(proof.Repository)+":"+dailyFileDietWorkflow+":"+metric.ID+":"+metric.Version),
		"repository": proof.Repository, "workflow": dailyFileDietWorkflow,
		"sourcePath": fileDietSourcePath, "observedAt": proof.ObservedAt,
		"windowStart": proof.Adoption.At, "windowEnd": proof.Until,
		"contractDigest": contract, "facts": facts,
	}
	// All selected contributions must be tied to a retained eligible output,
	// including negative and zero net changes.
	accepted := map[string]bool{}
	for _, output := range ledger.Outputs {
		for _, id := range output.ImplementationIDs {
			accepted[id] = true
		}
	}
	for _, event := range ledger.Events {
		if !accepted[event.ID] || !fileDietTime(event.At).Before(fileDietTime(proof.Until)) ||
			fileDietTime(event.At).Before(fileDietTime(proof.Adoption.At)) {
			return nil, errors.New("File Diet contribution lacks eligible output trail")
		}
	}
	if len(facts) > 20000 || len(fileDietEncodeJSON(packet)) > 8<<20 {
		return nil, errors.New("File Diet evidence packet exceeds publication byte budget")
	}
	return packet, nil
}
