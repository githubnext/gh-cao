package mining

import (
	"testing"
	"time"
)

func TestOutputLifecycleDeliveryAndAuthoritativeReversal(t *testing.T) {
	delivered := testInstant()
	row, err := OutputLifecycleRow(
		"run:1:safe-output:1",
		"repository:octo%2Fapi",
		"workflow:octo%2Fapi%3A.github%2Fworkflows%2Freview.md",
		"github:run:octo/api:1",
		"create_issue",
		"Review finding",
		"",
		"https://github.com/octo/api/issues/7",
		delivered,
		delivered.Add(time.Minute),
		"safe-output",
	)
	if err != nil {
		t.Fatal(err)
	}
	if row["deliveryState"] != "delivered" || row["adoptionState"] != "pending" ||
		row["targetRepository"] != "octo/api" || row["ruleVersion"] != AdoptionRegistryVersion {
		t.Fatalf("initial lifecycle = %+v", row)
	}
	acceptedAt := delivered.Add(2 * time.Hour)
	accepted, err := ApplyOutcomeToLifecycle(row, Outcome{
		Repository: "octo/api", Number: 7, URL: "https://github.com/octo/api/issues/7",
		State: "closed", StateReason: "completed", CloseActor: "User",
		DispositionAt: &acceptedAt, UpdatedAt: acceptedAt,
		ObservedAt: acceptedAt.Add(time.Minute), Source: "github-api",
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted["adoptionState"] != "accepted" || accepted["survivalState"] != "observed" {
		t.Fatalf("accepted lifecycle = %+v", accepted)
	}
	reopenedAt := acceptedAt.Add(24 * time.Hour)
	reopened, err := ApplyOutcomeToLifecycle(accepted, Outcome{
		Repository: "octo/api", Number: 7, URL: "https://github.com/octo/api/issues/7",
		State: "open", UpdatedAt: reopenedAt,
		ObservedAt: reopenedAt.Add(time.Minute), Source: "github-api",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reopened["adoptionState"] != "pending" || reopened["reversalSignal"] != "ISS_REOPENED" ||
		reopened["survivalState"] != "reversed" {
		t.Fatalf("reopened lifecycle = %+v", reopened)
	}
}

func TestOutputLifecycleDeliveryRulesCoverFallbackInfraAndUnknown(t *testing.T) {
	at := testInstant()
	for outputType, status := range map[string]string{
		"add_comment":       "accepted",
		"report_incomplete": "not_applicable",
		"custom_output":     "unknown",
	} {
		row, err := OutputLifecycleRow("source:"+outputType, "repository:1", "workflow:1", "run:1",
			outputType, "", "", "", at, at, "safe-output")
		if err != nil {
			t.Fatal(err)
		}
		if row["adoptionState"] != status {
			t.Fatalf("%s adoption = %+v", outputType, row)
		}
	}
}
