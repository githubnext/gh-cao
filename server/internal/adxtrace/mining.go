package adxtrace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

const episodeBindingVersion = "selected-run-attempt-v1"
const outcomeLabelVersion = mining.OutcomeRuleVersion
const maxMiningBytes = 2 << 20

type miningProjection struct {
	Version, File, State, Delivered string
	SemanticDigest                  string
	Revision, FactsRevision         int64
	FreshUntil                      time.Time
}

type miningEvidence struct {
	CanonicalNamespace, CanonicalRunID, TraceVersion, State string
	CanonicalRevision                                       int64
	CapturedAt, FreshUntil                                  time.Time
	Gaps                                                    []string
	Tables                                                  map[string][]model.Row
}

func canonicalRun(r *record) string { return "github:run:" + r.Repository + ":" + r.RunID }

func projectMining(snapshot sessionexport.MiningSnapshot, r *record, trace Evidence, now time.Time, interval time.Duration) (miningEvidence, string, error) {
	e := miningEvidence{CanonicalNamespace: snapshot.Namespace, CanonicalRunID: canonicalRun(r),
		CanonicalRevision: snapshot.Revision, TraceVersion: r.Version, State: "ready",
		CapturedAt: now.UTC(), FreshUntil: now.Add(3 * interval).UTC(),
		Gaps: []string{"selected-scope-partial"}, Tables: map[string][]model.Row{}}
	input, err := json.Marshal(snapshot)
	if err != nil || len(input) > sessionexport.MaxBytes || len(snapshot.Audits)+len(snapshot.Outputs) > sessionexport.MaxRows {
		return e, "", sessionexport.ErrTooLarge
	}
	if snapshot.Namespace == "" || snapshot.Revision < 0 {
		return e, "", errors.New("mining source has no valid canonical identity")
	}
	if snapshot.Run == nil {
		if len(snapshot.Audits) != 0 || len(snapshot.Outputs) != 0 {
			return e, "", errors.New("mining facts lack a selected run")
		}
		e.State = "unavailable"
		e.Gaps = append(e.Gaps, "selected-attempt-unavailable")
	} else {
		if scalar(snapshot.Run["id"]) != canonicalRun(r) || integer(snapshot.Run["attempt"]) != int(r.Attempt) ||
			scalar(snapshot.Run["repositoryId"]) != "repository:"+model.EncodeCoordinate(r.Repository) {
			return e, "", errors.New("mining source does not match the exact repository/run/attempt")
		}
		audits := map[string]model.Row{}
		proofs := traceOutputProofs(trace)
		for _, audit := range snapshot.Audits {
			id := scalar(audit["id"])
			if id == "" || audits[id] != nil || scalar(audit["runId"]) != canonicalRun(r) || scalar(audit["source"]) != mining.Source {
				return e, "", errors.New("invalid or duplicate mining Audit identity")
			}
			if _, err := observationTime(audit["observedAt"]); err != nil {
				return e, "", err
			}
			audits[id] = audit
		}
		for _, audit := range snapshot.Audits {
			kind := scalar(audit["type"])
			if kind == "outcome.observed" {
				continue
			}
			if kind != "episode.membership" && kind != "episode.root" {
				return e, "", errors.New("unsupported mining Audit type")
			}
			if scalar(audit["scope"]) != "selected_runs" || scalar(audit["evidenceState"]) != "partial" || scalar(audit["correlationId"]) == "" {
				return e, "", errors.New("episode source scope or membership is unavailable")
			}
			hop, parent := scalar(audit["name"]), scalar(audit["code"])
			if kind == "episode.membership" {
				if integer(audit["claimRunAttempt"]) != int(r.Attempt) {
					return e, "", errors.New("episode belongs to another or unknown attempt")
				}
				for _, native := range trace.Tables["TraceEvents"] {
					if episode := scalar(native["EpisodeId"]); episode != "" {
						if episode != scalar(audit["correlationId"]) ||
							scalar(native["HopId"]) == hop && parent != "" && scalar(native["ParentHopId"]) != "" && scalar(native["ParentHopId"]) != parent {
							return e, "", errors.New("conflicting native and mined episode context")
						}
					}
				}
			} else {
				membership := audits[strings.TrimSuffix(scalar(audit["id"]), ":root")+":membership"]
				if membership == nil || scalar(membership["type"]) != "episode.membership" ||
					scalar(membership["correlationId"]) != scalar(audit["correlationId"]) || integer(membership["claimRunAttempt"]) != int(r.Attempt) {
					return e, "", errors.New("episode root lacks matching exact-attempt membership")
				}
			}
			observed, _ := observationTime(audit["observedAt"])
			e.Tables["TraceEpisodes"] = append(e.Tables["TraceEpisodes"], model.Row{
				"CanonicalNamespace": snapshot.Namespace, "CanonicalRunId": canonicalRun(r), "Repository": r.Repository,
				"AuditId": scalar(audit["id"]), "Kind": kind, "EpisodeId": scalar(audit["correlationId"]),
				"HopId": hop, "ParentHopId": parent, "ReferencedRunId": scalar(audit["claimRunId"]),
				"ReferencedRepository": scalar(audit["targetRepository"]), "ReferencedWorkflow": scalar(audit["targetWorkflowPath"]),
				"ObservedAt": observed, "Scope": "selected_runs", "EvidenceState": "partial", "BindingVersion": episodeBindingVersion,
			})
		}
		if len(e.Tables["TraceEpisodes"]) == 0 {
			e.Gaps = append(e.Gaps, "episode-membership-unavailable")
		}
		seen := map[string]bool{}
		for _, output := range snapshot.Outputs {
			id, kind := scalar(output["id"]), scalar(output["safeOutputType"])
			url := scalar(output["safeOutputUrl"])
			if url == "" {
				url = scalar(output["correlationId"])
			}
			target, err := mining.ParseTarget(url)
			if err != nil || id == "" || seen[id] || scalar(output["runId"]) != canonicalRun(r) ||
				(kind != "create_issue" && kind != "create_pull_request") || target.PullRequest != (kind == "create_pull_request") {
				return e, "", errors.New("invalid exact create-output reference")
			}
			if pull, ok := output["isPullRequest"].(bool); ok && pull != target.PullRequest {
				return e, "", errors.New("output category contradicts its URL")
			}
			if correlation := scalar(output["correlationId"]); correlation != "" && correlation != url {
				return e, "", errors.New("conflicting retained output URLs")
			}
			seen[id] = true
			row := model.Row{"CanonicalNamespace": snapshot.Namespace, "CanonicalRunId": canonicalRun(r), "Repository": r.Repository,
				"OutputId": id, "OutputUrl": url, "OutputType": kind, "AuditId": "", "ProducingAttempt": 0,
				"Status": "unknown", "Signal": "missing_resolution_evidence", "ObjectState": "", "ObjectUpdatedAt": "",
				"ObservedAt": "", "AcquisitionSource": "", "Scope": "selected_outputs", "EvidenceState": "partial", "LabelRuleVersion": outcomeLabelVersion}
			audit := audits["audit:server-mining:outcome:"+model.EncodeCoordinate(id)]
			if audit != nil && scalar(audit["payloadRef"]) != "" && scalar(audit["name"]) != "" {
				if scalar(audit["type"]) != "outcome.observed" || scalar(audit["correlationId"]) != id ||
					scalar(audit["name"]) != outcomeLabelVersion ||
					scalar(audit["payloadRef"]) != url ||
					scalar(audit["targetRepository"]) != target.Repository || scalar(audit["scope"]) != "selected_outputs" ||
					scalar(audit["evidenceState"]) != "partial" ||
					!slices.Contains([]string{"github-api", "github-webhook"}, scalar(audit["summary"])) || !validDisposition(audit, target.PullRequest) {
					return e, "", errors.New("outcome Audit does not bind to the exact retained output")
				}
				updated, err := observationTime(audit["timestamp"])
				if err != nil {
					return e, "", err
				}
				observed, _ := observationTime(audit["observedAt"])
				row["AuditId"], row["Status"], row["Signal"], row["ObjectState"] = scalar(audit["id"]), scalar(audit["status"]), scalar(audit["code"]), scalar(audit["decision"])
				row["ObjectUpdatedAt"], row["ObservedAt"], row["AcquisitionSource"] = updated, observed, scalar(audit["summary"])
			} else {
				e.Gaps = append(e.Gaps, "output-disposition-unavailable")
			}
			if proofs[kind+"\x00"+url] == 1 {
				row["ProducingAttempt"] = r.Attempt
			} else {
				e.Gaps = append(e.Gaps, "output-attempt-unattested")
			}
			e.Tables["TraceOutcomes"] = append(e.Tables["TraceOutcomes"], row)
		}
		for _, audit := range snapshot.Audits {
			if scalar(audit["type"]) == "outcome.observed" && !seen[scalar(audit["correlationId"])] {
				return e, "", errors.New("outcome has no retained create-output denominator")
			}
		}
	}
	slices.Sort(e.Gaps)
	e.Gaps = slices.Compact(e.Gaps)
	for table, rows := range e.Tables {
		key := "AuditId"
		if table == "TraceOutcomes" {
			key = "OutputId"
		}
		slices.SortFunc(rows, func(a, b model.Row) int { return strings.Compare(scalar(a[key]), scalar(b[key])) })
	}
	// Unrelated database revisions and polling clocks are not evidence changes.
	semantic, err := json.Marshal(struct {
		Namespace, Run, Trace, State string
		Gaps                         []string
		Tables                       map[string][]model.Row
	}{e.CanonicalNamespace, e.CanonicalRunID, e.TraceVersion, e.State, e.Gaps, e.Tables})
	if err != nil {
		return e, "", err
	}
	return e, digest(semantic), nil
}

func observationTime(value any) (string, error) {
	if t, ok := value.(time.Time); ok && !t.IsZero() {
		return t.UTC().Format(time.RFC3339Nano), nil
	}
	t, err := time.Parse(time.RFC3339Nano, scalar(value))
	if err != nil || t.IsZero() {
		return "", errors.New("mining source clock is missing or invalid")
	}
	return t.UTC().Format(time.RFC3339Nano), nil
}

func validDisposition(audit model.Row, pull bool) bool {
	state, status, signal := scalar(audit["decision"]), scalar(audit["status"]), scalar(audit["code"])
	if state == "open" {
		return status == "pending" && signal == "open"
	}
	if state != "closed" {
		return false
	}
	if pull {
		return status == "accepted" && signal == "merged" || status == "rejected" && signal == "closed_without_merge"
	}
	return status == "accepted" && signal == "completed" || status == "rejected" && signal == "closed_not_planned" ||
		status == "lifecycle" && signal == "lifecycle" || status == "unknown" && signal == "missing_resolution_evidence"
}

// A Run-level output link alone does not identify its producing attempt.
// Only an approved, correlated, successful creation result can attest it.
func traceOutputProofs(trace Evidence) map[string]int {
	proofs := map[string]int{}
	events, err := reconstruct(trace)
	if err != nil {
		return proofs
	}
	calls := map[string]string{}
	seen := map[string]bool{}
	for _, event := range events {
		data, _ := event["data"].(map[string]any)
		call := scalar(data["toolCallId"])
		switch scalar(event["type"]) {
		case "tool.execution_start":
			if call != "" && slices.Contains([]string{"create_issue", "create_pull_request"}, scalar(data["toolName"])) {
				if seen[call] {
					return map[string]int{}
				}
				seen[call] = true
				calls[call] = scalar(data["toolName"])
			}
		case "tool.execution_complete":
			kind := calls[call]
			if call == "" || kind == "" || data["success"] != true || data["error"] != nil {
				continue
			}
			delete(calls, call)
			urls := map[string]bool{}
			for _, key := range []string{"output", "result"} {
				result, ok := data[key].(map[string]any)
				if ok {
					for _, field := range []string{"url", "html_url"} {
						if url := scalar(result[field]); url != "" {
							urls[url] = true
						}
					}
				}
			}
			if len(urls) == 1 {
				for url := range urls {
					proofs[kind+"\x00"+url]++
				}
			}
		}
	}
	return proofs
}

func readMining(root *os.Root, m *miningProjection) (miningEvidence, error) {
	var e miningEvidence
	content, err := readBounded(root, m.File, maxMiningBytes)
	if err != nil {
		return e, err
	}
	if digest(content)+".mining" != m.File {
		return e, errors.New("mining spool integrity failure")
	}
	if err := decode(content, &e); err != nil {
		return e, err
	}
	return e, nil
}

func invalidateMining(state *ledger, r *record) []string {
	m := r.Mining
	if m == nil || m.State == "invalidated" {
		return nil
	}
	var files []string
	if m.File != "" {
		files = append(files, m.File)
	}
	state.Revision++
	m.Version = digest([]byte(m.Version + ":invalidated:" + strconv.FormatInt(state.Revision, 10)))
	m.File, m.State, m.Delivered = "", "invalidated", ""
	m.Revision, m.FactsRevision = state.Revision, state.Revision
	return files
}

func (w *Worker) stepMining(ctx context.Context, root *os.Root, state *ledger) (bool, error) {
	ids := make([]string, 0, len(state.Records))
	for id := range state.Records {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		r := state.Records[id]
		if r.Mining != nil && r.Mining.Delivered == "" {
			if err := w.deliverMining(ctx, root, state, r); err != nil {
				return false, err
			}
			r.Mining.Delivered = strconv.FormatInt(r.Mining.Revision, 10)
			return true, nil
		}
	}
	var next *record
	for _, id := range ids {
		r := state.Records[id]
		if r.State != "eligible" || r.Delivered == "" || !w.selected(state, r) || !r.MiningCheckedAt.IsZero() && w.now().Sub(r.MiningCheckedAt) < w.spool.config.MiningInterval {
			continue
		}
		if next == nil || r.MiningCheckedAt.Before(next.MiningCheckedAt) {
			next = r
		}
	}
	if next == nil {
		return false, nil
	}
	snapshot, err := w.mining.ReadMining(ctx, next.Repository, next.RunID, next.Attempt)
	if err != nil {
		return false, err
	}
	trace, err := readEvidence(root, next, w.spool.config.MaxTraceBytes)
	if err != nil {
		return false, err
	}
	e, version, err := projectMining(snapshot, next, trace, w.now(), w.spool.config.MiningInterval)
	if err == nil && (state.MiningNamespace != "" && state.MiningNamespace != snapshot.Namespace || snapshot.Revision < next.MiningSourceRevision) {
		err = errors.New("canonical mining namespace or revision changed incompatibly")
	}
	if err == nil && next.Mining != nil && next.Mining.File != "" {
		old, readErr := readMining(root, next.Mining)
		if readErr != nil {
			return false, readErr
		}
		err = checkOutcomeProgress(old, e)
	}
	if err == nil {
		content, marshalErr := json.Marshal(e.Tables)
		if marshalErr != nil {
			return false, marshalErr
		}
		for _, pattern := range builtInSecrets {
			if pattern.Match(content) {
				err = errors.New("mining metadata contains unapproved sensitive identifiers")
			}
		}
		for _, pattern := range w.spool.config.Policy.RedactPatterns {
			matched, matchErr := regexp.Match(pattern, content)
			if matchErr != nil {
				return false, matchErr
			}
			if matched {
				err = errors.New("mining metadata requires incompatible identifier redaction")
			}
		}
	}
	if err != nil {
		files := invalidateDatasets(state, next.ID)
		files = append(files, invalidateMining(state, next)...)
		next.MiningCheckedAt = w.now()
		if saveErr := w.spool.save(root, state); saveErr != nil {
			return false, saveErr
		}
		if removeErr := removeMiningFiles(root, files); removeErr != nil {
			return false, removeErr
		}
		return false, err
	}
	state.MiningNamespace = snapshot.Namespace
	next.MiningCheckedAt, next.MiningSourceRevision = w.now(), snapshot.Revision
	var remove []string
	old := next.Mining
	state.Revision++
	m := &miningProjection{Version: digest([]byte(version + "\x00" + strconv.FormatInt(state.Revision, 10))),
		SemanticDigest: version, State: e.State, Revision: state.Revision, FactsRevision: state.Revision, FreshUntil: e.FreshUntil}
	if old != nil {
		if old.File != "" {
			remove = append(remove, old.File)
		}
		if old.SemanticDigest == version && old.State == e.State {
			m.Version = old.Version
			m.FactsRevision = old.FactsRevision
		} else {
			remove = append(remove, invalidateDatasets(state, next.ID)...)
		}
	} else {
		remove = append(remove, invalidateDatasets(state, next.ID)...)
	}
	content, err := json.Marshal(e)
	if err != nil {
		return false, err
	}
	if len(content) > maxMiningBytes {
		return false, sessionexport.ErrTooLarge
	}
	if err := w.spool.reserve(root, int64(len(content))); err != nil {
		return false, err
	}
	m.File = digest(content) + ".mining"
	if err := atomicWrite(root, m.File, content); err != nil {
		return false, err
	}
	next.Mining = m
	if err := w.spool.save(root, state); err != nil {
		return false, err
	}
	retired := slices.DeleteFunc(remove, func(file string) bool { return file == m.File })
	return true, removeMiningFiles(root, retired)
}

func checkOutcomeProgress(old, next miningEvidence) error {
	previous := map[string]model.Row{}
	for _, row := range old.Tables["TraceOutcomes"] {
		previous[scalar(row["OutputId"])] = row
	}
	for _, row := range next.Tables["TraceOutcomes"] {
		before := previous[scalar(row["OutputId"])]
		previousTime, oldErr := time.Parse(time.RFC3339Nano, scalar(before["ObjectUpdatedAt"]))
		nextTime, newErr := time.Parse(time.RFC3339Nano, scalar(row["ObjectUpdatedAt"]))
		if oldErr != nil || newErr != nil {
			continue
		}
		if nextTime.Before(previousTime) {
			return errors.New("outcome observation regressed")
		}
		if nextTime.Equal(previousTime) && (row["Status"] != before["Status"] || row["Signal"] != before["Signal"] || row["ObjectState"] != before["ObjectState"]) {
			return errors.New("contradictory same-time outcome observations")
		}
	}
	return nil
}

func removeMiningFiles(root *os.Root, files []string) error {
	for _, file := range files {
		if err := root.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (w *Worker) deliverMining(ctx context.Context, root *os.Root, state *ledger, r *record) error {
	m := r.Mining
	e := miningEvidence{CanonicalNamespace: state.MiningNamespace, CanonicalRunID: canonicalRun(r),
		CanonicalRevision: r.MiningSourceRevision, TraceVersion: r.Version, State: m.State,
		CapturedAt: w.now(), FreshUntil: w.now(), Gaps: []string{"source-invalidated"}, Tables: map[string][]model.Row{}}
	if m.File != "" {
		var err error
		e, err = readMining(root, m)
		if err != nil {
			return err
		}
	}
	if err := w.sink.Capacity(ctx); err != nil {
		return err
	}
	data := map[string][]model.Row{}
	for _, table := range []string{"TraceEpisodes", "TraceOutcomes"} {
		rows := e.Tables[table]
		if len(rows) == 0 {
			continue
		}
		if err := seal(w.spool.namespace, r.ID, m.Version, r.Attempt, m.FactsRevision, table, rows); err != nil {
			return err
		}
		if err := w.writeBatches(ctx, table, rows); err != nil {
			return err
		}
		data[table] = rows
	}
	if len(data) > 0 {
		if err := w.sink.VerifyEvidence(ctx, w.spool.namespace, r.ID, m.Version, data); err != nil {
			return err
		}
	}
	rows := []model.Row{{"CanonicalNamespace": e.CanonicalNamespace, "CanonicalRunId": e.CanonicalRunID,
		"CanonicalRevision": e.CanonicalRevision, "Repository": r.Repository, "TraceVersion": e.TraceVersion,
		"EpisodeCount": len(e.Tables["TraceEpisodes"]), "OutcomeCount": len(e.Tables["TraceOutcomes"]),
		"State": e.State, "Gaps": e.Gaps, "CapturedAt": e.CapturedAt.Format(time.RFC3339Nano), "FreshUntil": e.FreshUntil.Format(time.RFC3339Nano)}}
	if err := seal(w.spool.namespace, r.ID, m.Version, r.Attempt, m.Revision, "TraceMiningManifest", rows); err != nil {
		return err
	}
	if err := w.writeBatches(ctx, "TraceMiningManifest", rows); err != nil {
		return err
	}
	return w.sink.VerifyEvidence(ctx, w.spool.namespace, r.ID, m.Version, map[string][]model.Row{"TraceMiningManifest": rows})
}
