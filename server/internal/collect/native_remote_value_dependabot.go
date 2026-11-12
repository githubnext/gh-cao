package collect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

const (
	dependabotValueRepository  = "github/gh-aw"
	dependabotValueWorkflow    = "dependabot-update-planner"
	dependabotValueCollector   = "dependabot/operational-value/dependabot-update-planner.mjs"
	dependabotActionsCollector = ".github/value-collectors/dependabot-update-planner.mjs"
	dependabotValueAdoption    = "2026-09-17T08:48:00Z"
	dependabotValueVersion     = "dependabot-lifecycle-v1"
)

var dependabotValueRequirements = []remoteValueRequirement{
	{"github", "core", "github.dependabot-alert-lifecycle.v1", dependabotValueRepository, "invocation"},
	{"github", "search", "github.dependabot-pull-request-lifecycle.v1", dependabotValueRepository, "invocation"},
}

type dependabotValueMetric struct {
	id, name, unit, formula string
}

var dependabotValueMetrics = []dependabotValueMetric{
	{"open-security-alert-count", "Open security alerts", "alerts", "Dependabot security alerts open at the cutoff"},
	{"open-high-critical-alert-count", "Open critical/high security alerts", "alerts", "Dependabot security alerts with critical or high severity open at the cutoff"},
	{"open-dependabot-pr-count", "Open Dependabot pull requests", "pull-requests", "Dependabot-authored dependency pull requests open at the cutoff"},
}

func reviewedDependabotValue(entry remoteValueEntry) error {
	if entry.Repository != dependabotValueRepository || entry.Workflow != dependabotValueWorkflow ||
		(entry.Collector != dependabotValueCollector && entry.Collector != dependabotActionsCollector) || entry.APIRequests != 111 ||
		entry.Acquisition == nil || len(entry.Acquisition.Requirements) != len(dependabotValueRequirements) {
		return errors.New("unsupported reviewed Dependabot Value registration")
	}
	required := map[string]bool{}
	for _, requirement := range dependabotValueRequirements {
		required[remoteValueRequirementKey(requirement)] = true
	}
	for _, requirement := range entry.Acquisition.Requirements {
		key := remoteValueRequirementKey(requirement)
		if !required[key] {
			return errors.New("unreviewed or duplicate Dependabot Value acquisition requirement")
		}
		delete(required, key)
	}
	return nil
}

func reviewedRemoteValue(entry remoteValueEntry) error {
	if entry.Workflow == dependabotValueWorkflow || entry.Collector == dependabotValueCollector ||
		entry.Collector == dependabotActionsCollector {
		return reviewedDependabotValue(entry)
	}
	return reviewedDailyFileDiet(entry)
}

func dependabotValueHash(value any) string {
	content, _ := json.Marshal(value)
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func dependabotValueCutoff(at time.Time) time.Time {
	adoption, _ := time.Parse(time.RFC3339, dependabotValueAdoption)
	period := 7 * 24 * time.Hour
	if at.Before(adoption.Add(period)) {
		return time.Time{}
	}
	elapsedNanos := at.Sub(adoption).Nanoseconds()
	periodNanos := period.Nanoseconds()
	return adoption.Add(time.Duration(elapsedNanos / periodNanos * periodNanos))
}

func dependabotValueTime(value string, observed time.Time) (time.Time, error) {
	if value == "" || !strings.HasSuffix(value, "Z") {
		return time.Time{}, errors.New("dependabot lifecycle timestamp missing or not UTC")
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || at.After(observed) {
		return time.Time{}, errors.New("dependabot lifecycle timestamp malformed or in the future")
	}
	return at, nil
}

type dependabotValueLifecycle struct {
	CreatedAt        string  `json:"created_at"`
	FixedAt          *string `json:"fixed_at"`
	DismissedAt      *string `json:"dismissed_at"`
	AutoDismissedAt  *string `json:"auto_dismissed_at"`
	ClosedAt         *string `json:"closed_at"`
	State            string  `json:"state"`
	Number           int     `json:"number"`
	SecurityAdvisory *struct {
		Severity string `json:"severity"`
	} `json:"security_advisory"`
	User *struct {
		Login string `json:"login"`
	} `json:"user"`
	PullRequest json.RawMessage `json:"pull_request"`
}

type dependabotValueEvent struct {
	created, ended time.Time
	severity       string
}

func dependabotValueRecord(record dependabotValueLifecycle, observed time.Time, pull bool) (dependabotValueEvent, error) {
	created, err := dependabotValueTime(record.CreatedAt, observed)
	if err != nil || record.Number <= 0 {
		return dependabotValueEvent{}, errors.New("dependabot lifecycle record lacks an authoritative identity or creation")
	}
	event := dependabotValueEvent{created: created}
	terminal := []*string{record.FixedAt, record.DismissedAt, record.AutoDismissedAt}
	switch {
	case pull:
		if record.User == nil || record.User.Login != "dependabot[bot]" ||
			len(record.PullRequest) == 0 || bytes.Equal(record.PullRequest, []byte("null")) ||
			(record.State != "open" && record.State != "closed") {
			return event, errors.New("search result is not a Dependabot-authored pull request")
		}
		terminal = []*string{record.ClosedAt}
	case record.SecurityAdvisory == nil ||
		(record.SecurityAdvisory.Severity != "critical" && record.SecurityAdvisory.Severity != "high" &&
			record.SecurityAdvisory.Severity != "medium" && record.SecurityAdvisory.Severity != "low") ||
		(record.State != "open" && record.State != "fixed" &&
			record.State != "dismissed" && record.State != "auto_dismissed"):
		return event, errors.New("dependabot alert lacks a valid severity or state")
	default:
		event.severity = record.SecurityAdvisory.Severity
	}
	for _, value := range terminal {
		if value == nil {
			continue
		}
		ended, err := dependabotValueTime(*value, observed)
		if err != nil || ended.Before(created) {
			return event, errors.New("dependabot lifecycle terminal timestamp is invalid")
		}
		if event.ended.IsZero() || ended.Before(event.ended) {
			event.ended = ended
		}
	}
	if (record.State == "open") != event.ended.IsZero() {
		return event, errors.New("dependabot lifecycle state contradicts its terminal timestamp")
	}
	return event, nil
}

func dependabotValueNext(headers http.Header, base *url.URL, endpoint string) (string, error) {
	link := headers.Get("Link")
	if link == "" {
		return "", nil
	}
	current, _ := url.Parse(endpoint)
	next := ""
	parts := []string{}
	start, inURL := 0, false
	for index, char := range link {
		switch char {
		case '<':
			inURL = true
		case '>':
			inURL = false
		case ',':
			if !inURL {
				parts = append(parts, link[start:index])
				start = index + 1
			}
		}
	}
	parts = append(parts, link[start:])
	for _, part := range parts {
		section := strings.SplitN(strings.TrimSpace(part), ";", 2)
		if len(section) != 2 {
			return "", errors.New("malformed Dependabot pagination link")
		}
		if !strings.Contains(section[1], `rel="next"`) {
			continue
		}
		target, err := url.Parse(strings.Trim(section[0], "<> "))
		if err != nil {
			return "", errors.New("dependabot pagination link escaped reviewed endpoint")
		}
		basePath := strings.TrimSuffix(base.Path, "/")
		id := strings.TrimSuffix(strings.TrimPrefix(target.Path, basePath+"/repositories/"), "/dependabot/alerts")
		number, numericErr := strconv.ParseUint(id, 10, 64)
		numericAlias := numericErr == nil && number > 0 &&
			target.Path == basePath+"/repositories/"+id+"/dependabot/alerts"
		if next != "" || target.User != nil || target.Fragment != "" ||
			target.Scheme != base.Scheme || target.Host != base.Host ||
			(target.Path != basePath+"/"+current.Path && !numericAlias) {
			return "", errors.New("dependabot pagination link escaped reviewed endpoint")
		}
		query, err := url.ParseQuery(target.RawQuery)
		if err != nil || len(query["after"]) != 1 || query.Get("after") == "" ||
			query.Get("after") == current.Query().Get("after") {
			return "", errors.New("dependabot pagination link lacks a new cursor")
		}
		expected := current.Query()
		expected.Del("after")
		query.Del("after")
		if query.Encode() != expected.Encode() {
			return "", errors.New("dependabot pagination link changed reviewed selection")
		}
		// GitHub returns numeric aliases; only the cursor is reused on the reviewed named endpoint.
		next = current.Path + "?" + target.RawQuery
	}
	return next, nil
}

func dependabotValueReadAlerts(ctx context.Context, api *remoteValueAPI, observed time.Time) ([]dependabotValueEvent, error) {
	const path = "repos/github/gh-aw/dependabot/alerts"
	events := []dependabotValueEvent{}
	seen := map[int]bool{}
	endpoint := path + "?state=auto_dismissed,dismissed,fixed,open&per_page=100"
	for page := 1; page <= 100; page++ {
		body, headers, status, err := api.request(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("dependabot alerts API returned status %d", status)
		}
		var rows []dependabotValueLifecycle
		if err := json.Unmarshal(body, &rows); err != nil || rows == nil || len(rows) > 100 {
			return nil, errors.New("dependabot alert page is malformed")
		}
		next, err := dependabotValueNext(headers, api.base, endpoint)
		if err != nil {
			return nil, err
		}
		if next != "" && len(rows) != 100 || page == 100 && next != "" {
			return nil, errors.New("dependabot alert pagination is incomplete")
		}
		for _, row := range rows {
			if seen[row.Number] {
				return nil, errors.New("duplicate Dependabot alert")
			}
			seen[row.Number] = true
			event, err := dependabotValueRecord(row, observed, false)
			if err != nil {
				return nil, err
			}
			events = append(events, event)
		}
		if next == "" {
			if len(rows) == 100 {
				return nil, errors.New("full Dependabot alert page lacks a next cursor")
			}
			return events, nil
		}
		endpoint = next
	}
	return nil, errors.New("dependabot alert pagination is incomplete")
}

func dependabotValueReadPulls(ctx context.Context, api *remoteValueAPI, observed, cutoff time.Time) ([]dependabotValueEvent, error) {
	const path = "search/issues"
	query := fmt.Sprintf("repo:%s is:pr author:app/dependabot created:<=%s",
		dependabotValueRepository, cutoff.Format(time.RFC3339))
	events := []dependabotValueEvent{}
	seen := map[int]bool{}
	expected := -1
	for page := 1; page <= 10; page++ {
		endpoint := fmt.Sprintf("%s?q=%s&per_page=100&page=%d", path, url.QueryEscape(query), page)
		body, _, status, err := api.request(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("dependabot PR search returned status %d", status)
		}
		var response struct {
			TotalCount        *int                       `json:"total_count"`
			IncompleteResults *bool                      `json:"incomplete_results"`
			Items             []dependabotValueLifecycle `json:"items"`
		}
		if err := json.Unmarshal(body, &response); err != nil || response.TotalCount == nil ||
			*response.TotalCount < 0 || *response.TotalCount > 1000 ||
			response.IncompleteResults == nil || *response.IncompleteResults ||
			response.Items == nil || len(response.Items) > 100 {
			return nil, errors.New("dependabot PR search is incomplete or malformed")
		}
		if expected < 0 {
			expected = *response.TotalCount
		} else if expected != *response.TotalCount {
			return nil, errors.New("dependabot PR search total changed during pagination")
		}
		if len(response.Items) != min(100, expected-len(events)) {
			return nil, errors.New("dependabot PR search page was truncated")
		}
		for _, row := range response.Items {
			if seen[row.Number] {
				return nil, errors.New("duplicate Dependabot PR search result")
			}
			seen[row.Number] = true
			event, err := dependabotValueRecord(row, observed, true)
			if err != nil || event.created.After(cutoff) {
				return nil, errors.New("dependabot PR search returned an invalid author, lifecycle or cutoff")
			}
			events = append(events, event)
		}
		if len(events) == expected {
			return events, nil
		}
	}
	return nil, errors.New("dependabot PR search exceeded ten pages")
}

func dependabotValueCounts(alerts, pulls []dependabotValueEvent, cutoff time.Time) [3]int {
	var counts [3]int
	for _, alert := range alerts {
		if !alert.created.After(cutoff) && (alert.ended.IsZero() || alert.ended.After(cutoff)) {
			counts[0]++
			if alert.severity == "high" || alert.severity == "critical" {
				counts[1]++
			}
		}
	}
	for _, pull := range pulls {
		if !pull.created.After(cutoff) && (pull.ended.IsZero() || pull.ended.After(cutoff)) {
			counts[2]++
		}
	}
	return counts
}

func dependabotValueContract(metric dependabotValueMetric) (string, map[string]any) {
	fact := map[string]any{
		"fact-kind": "contract", "fact-id": "contract",
		"goal":          "Reduce the open Dependabot security-alert backlog in github/gh-aw.",
		"acceptance":    "Complete alert and Dependabot-authored pull-request lifecycle evidence at a seven-day cutoff.",
		"workflow-name": "Dependabot / Update Planner",
		"metric-id":     metric.id, "metric-name": metric.name, "metric-unit": metric.unit,
		"metric-version": dependabotValueVersion, "metric-formula": metric.formula,
		"metric-direction": "decrease", "measurement-basis": "repository-outcome",
		"result-label":      "open Dependabot lifecycle count",
		"outputs-available": false, "repository-state-available": true, "checks-available": false,
		"snapshot-id": metric.id, "snapshot-name": metric.name, "snapshot-unit": metric.unit,
		"snapshot-version": dependabotValueVersion, "snapshot-formula": metric.formula,
		"snapshot-direction": "decrease", "cadence-days": 7, "maturation-days": 0,
	}
	return dependabotValueHash(fact), fact
}

func dependabotValuePacket(metric dependabotValueMetric, previous model.Row, cutoff, observed time.Time, count int) (model.Row, error) {
	adoption, _ := time.Parse(time.RFC3339, dependabotValueAdoption)
	digest, contract := dependabotValueContract(metric)
	start := adoption
	facts := []any{contract}
	if previous != nil {
		if previous["contractDigest"] != digest ||
			previous["id"] != "operational-value-evidence:"+url.QueryEscape(dependabotValueRepository+":"+dependabotValueWorkflow+":"+metric.id+":"+dependabotValueVersion) ||
			previous["windowStart"] != dependabotValueAdoption {
			return nil, errors.New("retained Dependabot contract or fixed adoption was changed")
		}
		oldFacts, ok := previous["facts"].([]any)
		if !ok {
			return nil, errors.New("retained Dependabot facts are malformed")
		}
		for _, old := range oldFacts {
			fact, ok := old.(map[string]any)
			if !ok {
				return nil, errors.New("retained Dependabot fact is malformed")
			}
			if fact["fact-kind"] != "contract" {
				facts = append(facts, fact)
			}
		}
	}
	at := cutoff.UTC().Format(time.RFC3339)
	snapshotID := "cutoff:" + at
	for _, fact := range facts[1:] {
		row, ok := fact.(map[string]any)
		if !ok {
			return nil, errors.New("retained Dependabot fact is malformed")
		}
		if row["fact-kind"] == "snapshot" && row["fact-id"] == snapshotID {
			return nil, errors.New("retained Dependabot cutoff is already present")
		}
	}
	facts = append(facts, map[string]any{
		"fact-kind": "snapshot", "fact-id": snapshotID, "at": at,
		"value": count, "ref": "cutoff:" + at, "items-available": false,
	})
	coverageStart := cutoff.Add(-7 * 24 * time.Hour)
	if previous != nil {
		previousEnd, err := time.Parse(time.RFC3339Nano, fmt.Sprint(previous["windowEnd"]))
		if err != nil || !previousEnd.Before(cutoff) {
			return nil, errors.New("retained Dependabot cutoff is not before new cutoff")
		}
	}
	facts = append(facts, map[string]any{
		"fact-kind": "coverage", "fact-id": snapshotID,
		"at": coverageStart.Format(time.RFC3339), "end-at": at, "coverage-status": "complete",
	})
	packet := model.Row{
		"schemaVersion": 1,
		"id":            "operational-value-evidence:" + url.QueryEscape(dependabotValueRepository+":"+dependabotValueWorkflow+":"+metric.id+":"+dependabotValueVersion),
		"repository":    dependabotValueRepository, "workflow": dependabotValueWorkflow,
		"sourcePath":  ".github/workflows/" + dependabotValueWorkflow + ".md",
		"observedAt":  observed.UTC().Format(time.RFC3339Nano),
		"windowStart": start.Format(time.RFC3339), "windowEnd": at,
		"contractDigest": digest, "facts": facts,
	}
	encoded, err := json.Marshal(packet)
	if err != nil || len(encoded) > (8<<20)-1024 || len(facts) > 20000 {
		return nil, errors.New("dependabot evidence packet exceeds its byte budget")
	}
	return packet, nil
}

func (p NativeValueService) collectDependabotValue(
	ctx context.Context, repository string, installation int64, entry remoteValueEntry,
	policyDigest [sha256.Size]byte, base *url.URL, cache *remoteValueResponseCache,
	sharedRequirements map[string]remoteValueRequirement,
) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	if err := reviewedDependabotValue(entry); err != nil {
		return err
	}
	if err := p.remoteValueRegistrationCurrent(ctx, repository, installation, entry, policyDigest); err != nil {
		return err
	}
	started := time.Now().UTC()
	var outputBytes int64
	transport := p.RemoteHTTP
	if transport == nil {
		transport = &http.Client{Timeout: 65 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	api := &remoteValueAPI{
		client: transport, quota: p.RemoteQuota, searchQuota: p.RemoteSearchQuota,
		app: p.QuotaApp, installation: installation, repository: repository,
		collector: entry.Collector, base: base, max: entry.APIRequests, cache: cache,
		sharedRequirements: sharedRequirements, authorize: p.nativeValueCurrent,
	}
	defer func() {
		calls, core, search, graphql, hits, preflights, points, unknown, received := api.costUsage()
		costCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		reason := ""
		if resultErr != nil {
			reason = nativeRemoteValueFailure(ctx, resultErr)
		}
		if err := p.Data.AppendValueCollectionCost(costCtx, postgresx.ValueCollectionCost{
			At: started, Installation: installation, Repository: repository, Workflow: entry.Workflow,
			Collector: entry.Collector, APIRequests: calls, CoreRequests: core, SearchRequests: search,
			GraphQLRequests: graphql, CacheHits: hits, PreflightRequests: preflights,
			GraphQLPointsObserved: points, GraphQLRequestsUnverified: unknown,
			TransportBytes: received, OutputBytes: outputBytes,
			WallMS: time.Since(started).Milliseconds(), Succeeded: resultErr == nil, FailureReason: reason,
		}); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
		state := "available"
		switch {
		case resultErr != nil:
			state, reason = "failed", nativeRemoteValueFailure(ctx, resultErr)
		case outputBytes == 4:
			reason = "not-due"
		default:
			reason = "captured"
		}
		if err := p.RecordNativeValueAttempt(costCtx, repository, entry.Workflow, started, state, reason); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	previous, _, err := p.Data.Acquisition().ReadValueBatch(ctx, *p.nativeValueScope, entry.Workflow)
	if errors.Is(err, postgresx.ErrValueParentMissing) {
		return fmt.Errorf("%w: canonical workflow parent has not been projected", ErrRemoteValueDeferred)
	}
	if err != nil {
		return err
	}
	api.token, err = p.Tokens.InstallationToken(ctx, installation)
	if err != nil {
		return err
	}
	observed := time.Now().UTC().Truncate(time.Microsecond)
	packets, skipped, err := acquireNativeDependabotValue(ctx, api, previous, observed)
	if err != nil {
		return err
	}
	if skipped {
		outputBytes = 4
		return nil
	}
	for _, packet := range packets {
		content, err := json.Marshal(packet)
		if err != nil {
			return err
		}
		outputBytes += int64(len(content))
	}
	if outputBytes > (8<<20)-1024 {
		return errors.New("dependabot evidence batch exceeds its byte budget")
	}
	if err := p.remoteValueRegistrationCurrent(ctx, repository, installation, entry, policyDigest); err != nil {
		return err
	}
	_, err = p.Data.Acquisition().PublishValueBatch(ctx, *p.nativeValueScope, previous, packets, nil, time.Now().UTC())
	return err
}

func acquireNativeDependabotValue(ctx context.Context, api *remoteValueAPI, previous []model.Row, observed time.Time) ([]model.Row, bool, error) {
	cutoff := dependabotValueCutoff(observed)
	if cutoff.IsZero() {
		return nil, true, nil
	}
	if len(previous) != 0 && len(previous) != len(dependabotValueMetrics) {
		return nil, false, errors.New("incomplete retained Dependabot metric batch")
	}
	starting := make(map[string]model.Row, len(previous))
	complete := len(previous) == len(dependabotValueMetrics)
	var retainedCutoff string
	for _, packet := range previous {
		if packet == nil {
			return nil, false, errors.New("retained Dependabot metric is missing")
		}
		facts, ok := packet["facts"].([]any)
		if !ok {
			return nil, false, errors.New("retained Dependabot facts are malformed")
		}
		var metricID string
		var contract map[string]any
		var latest, coverage bool
		for _, item := range facts {
			row, ok := item.(map[string]any)
			if !ok {
				return nil, false, errors.New("retained Dependabot fact is malformed")
			}
			switch row["fact-kind"] {
			case "contract":
				if contract != nil {
					return nil, false, errors.New("retained Dependabot contract is duplicated")
				}
				contract = row
				metricID, _ = row["metric-id"].(string)
			case "snapshot":
				if row["at"] == packet["windowEnd"] &&
					row["fact-id"] == "cutoff:"+fmt.Sprint(packet["windowEnd"]) &&
					row["items-available"] == false && row["value"] != nil {
					value, err := strconv.Atoi(fmt.Sprint(row["value"]))
					if err != nil || value < 0 {
						return nil, false, errors.New("retained Dependabot snapshot is not a native count")
					}
					latest = true
				}
			case "coverage":
				coverageEnd, err := time.Parse(time.RFC3339Nano, fmt.Sprint(row["end-at"]))
				coverageStart, startErr := time.Parse(time.RFC3339Nano, fmt.Sprint(row["at"]))
				if err != nil || startErr != nil {
					return nil, false, errors.New("retained Dependabot coverage is malformed")
				}
				if row["end-at"] == packet["windowEnd"] && row["coverage-status"] == "complete" &&
					row["fact-id"] == "cutoff:"+fmt.Sprint(packet["windowEnd"]) &&
					coverageEnd.Sub(coverageStart) == 7*24*time.Hour {
					coverage = true
				}
			}
		}
		if metricID == "" || starting[metricID] != nil {
			return nil, false, errors.New("retained Dependabot metric identity is invalid")
		}
		var known bool
		for _, metric := range dependabotValueMetrics {
			if metric.id == metricID {
				digest, frozen := dependabotValueContract(metric)
				frozenBytes, _ := json.Marshal(frozen)
				retainedBytes, _ := json.Marshal(contract)
				if packet["contractDigest"] != digest || !bytes.Equal(frozenBytes, retainedBytes) ||
					packet["id"] != "operational-value-evidence:"+url.QueryEscape(dependabotValueRepository+":"+dependabotValueWorkflow+":"+metric.id+":"+dependabotValueVersion) ||
					packet["windowStart"] != dependabotValueAdoption {
					return nil, false, errors.New("retained Dependabot contract changed")
				}
				known = true
				break
			}
		}
		if !known {
			return nil, false, errors.New("retained Dependabot metric is not frozen")
		}
		starting[metricID] = packet
		parsed, err := time.Parse(time.RFC3339Nano, fmt.Sprint(packet["windowEnd"]))
		if err != nil || parsed.After(cutoff) ||
			retainedCutoff != "" && retainedCutoff != fmt.Sprint(packet["windowEnd"]) {
			return nil, false, errors.New("retained Dependabot cutoff is invalid")
		}
		retainedCutoff = fmt.Sprint(packet["windowEnd"])
		complete = complete && !parsed.Before(cutoff) && latest && coverage
	}
	if retainedCutoff == cutoff.Format(time.RFC3339) && !complete {
		return nil, false, errors.New("retained Dependabot cutoff is incomplete; immutable evidence cannot be refreshed")
	}
	if complete {
		return nil, true, nil
	}
	alerts, err := dependabotValueReadAlerts(ctx, api, observed)
	if err != nil {
		return nil, false, err
	}
	pulls, err := dependabotValueReadPulls(ctx, api, observed, cutoff)
	if err != nil {
		return nil, false, err
	}
	counts := dependabotValueCounts(alerts, pulls, cutoff)
	packets := make([]model.Row, 0, len(dependabotValueMetrics))
	var size int
	for i, metric := range dependabotValueMetrics {
		packet, err := dependabotValuePacket(metric, starting[metric.id], cutoff, observed, counts[i])
		if err != nil {
			return nil, false, err
		}
		content, _ := json.Marshal(packet)
		size += len(content)
		if size > (8<<20)-1024 {
			return nil, false, errors.New("dependabot metric batch exceeds its byte budget")
		}
		packets = append(packets, packet)
	}
	return packets, false, nil
}
