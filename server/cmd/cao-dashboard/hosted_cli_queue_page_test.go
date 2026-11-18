package main

import (
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestHostedCLIQueuePageResponseIncludesContinuationWhenMoreRemain(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	page := operational.CLICommandCursorPage{
		Commands: []operational.CLICommand{
			{ID: 9, Status: operational.CLICommandStatus("queued"), CreatedAt: created},
			{ID: 7, Status: operational.CLICommandStatus("running"), CreatedAt: created},
		},
		HasMore:         true,
		Revision:        12,
		RecoveryBlocked: true,
	}
	response := hostedCLIQueuePageResponse(2, page)

	if response["limit"] != 2 || response["hasMore"] != true || response["revision"] != int64(12) || response["recoveryBlocked"] != true {
		t.Fatalf("unexpected page metadata: %+v", response)
	}
	if summaries, ok := response["commands"].([]map[string]any); !ok || len(summaries) != 2 || summaries[0]["id"] != int64(9) {
		t.Fatalf("unexpected command summaries: %+v", response["commands"])
	}
	token, ok := response["nextContinuationToken"].(string)
	if !ok {
		t.Fatalf("missing continuation token: %+v", response)
	}
	beforeID, err := parseHostedCLIContinuationToken(token)
	if err != nil || beforeID != 7 {
		t.Fatalf("token resumes before %d, %v; want the last command ID 7", beforeID, err)
	}
}

func TestHostedCLIQueuePageResponseOmitsContinuationOnFinalPage(t *testing.T) {
	page := operational.CLICommandCursorPage{
		Commands: []operational.CLICommand{{ID: 3, Status: operational.CLICommandStatus("queued")}},
	}
	response := hostedCLIQueuePageResponse(20, page)
	if _, present := response["nextContinuationToken"]; present {
		t.Fatalf("final page must not carry a continuation token: %+v", response)
	}
	if response["hasMore"] != false || response["recoveryBlocked"] != false {
		t.Fatalf("unexpected page metadata: %+v", response)
	}
}

func TestHostedCLIQueuePageResponseHandlesEmptyPage(t *testing.T) {
	for name, hasMore := range map[string]bool{"exhausted": false, "inconsistent has-more": true} {
		response := hostedCLIQueuePageResponse(5, operational.CLICommandCursorPage{HasMore: hasMore})
		summaries, ok := response["commands"].([]map[string]any)
		if !ok || len(summaries) != 0 {
			t.Fatalf("%s: commands = %#v, want an empty non-nil list", name, response["commands"])
		}
		if _, present := response["nextContinuationToken"]; present {
			t.Fatalf("%s: an empty page has no command to resume before: %+v", name, response)
		}
	}
}
