package adxingest

import (
	"fmt"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

func TestKustoEmulatorOutcomeDenominator(t *testing.T) {
	k := newKustoAcceptance(t)
	k.apply(t, adxtrace.Schema())
	config, source := kustoMiningConfig(t)
	source.change = func(snapshot *sessionexport.MiningSnapshot) {
		canonical := snapshot.Run["id"].(string)
		for i, variant := range []struct {
			pull                 bool
			state, reason, actor string
		}{
			{true, "closed", "", ""},
			{true, "open", "", ""},
			{false, "closed", "not_planned", "Bot"},
			{false, "closed", "not_planned", ""},
			{false, "closed", "not_planned", "User"},
			{false, "closed", "completed", "User"},
		} {
			category, outputType := "issues", "create_issue"
			if variant.pull {
				category, outputType = "pull", "create_pull_request"
			}
			number, output := int64(100+i), fmt.Sprintf("additional-output-%d", i)
			observation := mining.Outcome{Repository: "octo/target", Number: number, PullRequest: variant.pull,
				URL:   fmt.Sprintf("https://github.com/octo/target/%s/%d", category, number),
				State: variant.state, StateReason: variant.reason, CloseActor: variant.actor,
				UpdatedAt: source.updated, ObservedAt: source.observed, Source: "github-api"}
			snapshot.Outputs = append(snapshot.Outputs, model.Row{"id": output, "runId": canonical,
				"safeOutputType": outputType, "isPullRequest": variant.pull, "safeOutputUrl": observation.URL})
			snapshot.Audits = append(snapshot.Audits, observation.Audit(output, canonical))
		}
	}
	worker, err := adxtrace.New(t.Context(), config, "denominator", k.client, source)
	if err != nil {
		t.Fatal(err)
	}
	captureKustoArtifact(t, worker, config, 42, kustoCreationTrace(t, 42))
	drainKustoTraces(t, worker)
	k.assert(t, `cao_TraceOutcomeSummary("denominator",now())
        | project EligibleOutputs, Accepted, Rejected, Pending, Lifecycle, Unknown, UnknownAttempts`,
		[][]any{{7, 2, 2, 1, 1, 1, 6}})
	k.assert(t, `cao_TraceOutcomes() | where OutputType=="create_issue"
        | project Status, Signal, ProducingAttempt | order by Status asc`,
		[][]any{{"accepted", "completed", 0}, {"lifecycle", "lifecycle", 0},
			{"rejected", "closed_not_planned", 0}, {"unknown", "missing_resolution_evidence", 0}})
}
