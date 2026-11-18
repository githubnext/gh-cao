package operational

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCLICommandValidation(t *testing.T) {
	for _, options := range []CLICommandQueueOptions{
		{MaxConcurrency: 33}, {MaxJobs: -1}, {MaxChunkBytes: 16385},
		{RetentionDays: 6}, {Timeout: time.Hour},
	} {
		if _, err := options.Resolve(); !errors.Is(err, ErrCLICommandInput) {
			t.Fatalf("invalid limits accepted: %+v %v", options, err)
		}
	}
	options, err := (CLICommandQueueOptions{}).Resolve()
	if err != nil || options.MaxConcurrency != 4 || options.Timeout != 15*time.Minute || options.RetentionDays != 30 {
		t.Fatalf("defaults: %+v %v", options, err)
	}
	for _, text := range []string{"ghp_example", "Bearer example", "password=example", "hello\x00world", "\xff"} {
		if err := ValidateCLICommandText(text); !errors.Is(err, ErrCLICommandInput) {
			t.Fatalf("unsafe input accepted")
		}
	}
	for _, invocation := range []CLICommandInvocation{
		{ActionID: "compile", Command: "gh aw compile", Values: map[string]string{"access-token": "value"}},
		{ActionID: "compile", Command: "gh aw compile", Input: new(strings.Repeat("x", 100001))},
		{ActionID: "compile", Command: "gh aw compile --token value"},
		{ActionID: "compile", Command: "gh aw compile", Values: map[string]string{"value": "\x00"}},
	} {
		if _, err := invocation.Encode(options.MaxInputBytes); !errors.Is(err, ErrCLICommandInput) {
			t.Fatalf("unsafe invocation accepted")
		}
	}
	if _, err := (CLICommandInvocation{ActionID: "compile", Command: "gh aw compile"}).Encode(1); !errors.Is(err, ErrCLICommandInput) {
		t.Fatalf("payload budget: %v", err)
	}
	for _, filter := range []CLICommandFilter{{Limit: 0}, {Limit: 501}, {Limit: 1, Offset: -1}, {Limit: 1, Statuses: []CLICommandStatus{"unknown"}}} {
		if err := filter.Validate(); !errors.Is(err, ErrCLICommandInput) {
			t.Fatalf("invalid filter accepted: %v", err)
		}
	}
	for _, key := range []string{"contains spaces", "ghp_example", strings.Repeat("k", 129)} {
		if err := ValidateCLICommandKey(key); !errors.Is(err, ErrCLICommandInput) {
			t.Fatalf("invalid idempotency accepted")
		}
	}
}

func TestCLICommandsRequireDurability(t *testing.T) {
	for _, required := range []bool{true, false} {
		f := serviceFeature{name: "cli commands", cap: Capability{Scope: ScopeProcess}, present: true, required: required}
		if got := classifyFeatureRejection(f, Requirements{SingleProcess: true, AllowVolatile: true}); got != featureRejectionStageInvalidGuarantees {
			t.Fatalf("volatile commands accepted: %s", got)
		}
	}
	if got := classifyFeatureRejection(serviceFeature{name: "cli commands", required: true}, Requirements{}); got != featureRejectionStageUnsupported {
		t.Fatalf("unsupported commands accepted: %s", got)
	}
}

func TestCLICommandInvocationValidateReportsRejectionStage(t *testing.T) {
	secretInput := "token=abc123"
	longInput := strings.Repeat("x", 100001)
	tests := []struct {
		name  string
		input CLICommandInvocation
		stage string
	}{
		{"valid", CLICommandInvocation{ActionID: "compile", Command: "gh aw compile", Values: map[string]string{"mode": "review"}}, ""},
		{"missing action", CLICommandInvocation{Command: "gh aw compile"}, cliRejectShape},
		{"blank command", CLICommandInvocation{ActionID: "a", Command: "  "}, cliRejectShape},
		{"oversized input", CLICommandInvocation{ActionID: "a", Command: "c", Input: &longInput}, cliRejectShape},
		{"secret key", CLICommandInvocation{ActionID: "a", Command: "c", Values: map[string]string{"apiToken": "v"}}, cliRejectSecretKey},
		{"empty secret key value", CLICommandInvocation{ActionID: "a", Command: "c", Values: map[string]string{"apiToken": ""}}, ""},
		{"secret argument", CLICommandInvocation{ActionID: "a", Command: "c", Arguments: map[string]bool{"--auth-token": true}}, cliRejectText},
		{"secret command", CLICommandInvocation{ActionID: "a", Command: "echo ghp_abcdef123456"}, cliRejectText},
		{"secret value", CLICommandInvocation{ActionID: "a", Command: "c", Values: map[string]string{"k": "Bearer abc"}}, cliRejectText},
		{"secret input", CLICommandInvocation{ActionID: "a", Command: "c", Input: &secretInput}, cliRejectText},
		{"dashboard origin", CLICommandInvocation{ActionID: "a", Command: "c", Origin: "#page-maintenance?repository=octo%2Fapp"}, ""},
		{"external origin", CLICommandInvocation{ActionID: "a", Command: "c", Origin: "https://example.com/#page-x"}, cliRejectShape},
		{"script origin", CLICommandInvocation{ActionID: "a", Command: "c", Origin: "#page-x?<script>"}, cliRejectShape},
		{"secret origin", CLICommandInvocation{ActionID: "a", Command: "c", Origin: "#page-x?token=abc123"}, cliRejectText},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stage, err := test.input.validate()
			if stage != test.stage || (test.stage == "") != (err == nil) {
				t.Fatalf("stage=%q err=%v, want stage %q", stage, err, test.stage)
			}
			if err != nil && !errors.Is(err, ErrCLICommandInput) {
				t.Fatalf("error %v is not ErrCLICommandInput", err)
			}
		})
	}
}

func TestCLICommandInvocationEncodeEnforcesSizeBoundary(t *testing.T) {
	invocation := CLICommandInvocation{ActionID: "compile", Command: "gh aw compile"}
	data, err := invocation.Encode(1 << 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invocation.Encode(len(data)); err != nil {
		t.Fatalf("exact-size encoding rejected: %v", err)
	}
	if _, err := invocation.Encode(len(data) - 1); !errors.Is(err, ErrCLICommandInput) {
		t.Fatalf("oversized encoding accepted: %v", err)
	}
}

func TestRedactCLICommandTextProducesStorableOutput(t *testing.T) {
	for _, text := range []string{
		"pushed with ghs_abcdefABCDEF0123456789 ok",
		"Authorization: Bearer abc.def",
		"password=hunter2 and token: xyz",
		"tok[REDACTED]en=leak",
		"bad\x00utf8 \xff",
		"plain output",
	} {
		redacted := RedactCLICommandText(text)
		if err := ValidateCLICommandText(redacted); err != nil {
			t.Fatalf("RedactCLICommandText(%q) = %q is not storable", text, redacted)
		}
	}
	if got := RedactCLICommandText("plain output"); got != "plain output" {
		t.Fatalf("plain output changed: %q", got)
	}
}

func TestCLICommandInvocationValidatesAttribution(t *testing.T) {
	valid := CLICommandInvocation{ActionID: "a", Command: "c", Actor: "mona", ActorID: 101, Repository: "octo/api"}
	if _, err := valid.Encode(4096); err != nil {
		t.Fatal(err)
	}
	for _, invocation := range []CLICommandInvocation{
		{ActionID: "a", Command: "c", Actor: "bad login"},
		{ActionID: "a", Command: "c", Actor: "mona"},
		{ActionID: "a", Command: "c", Actor: "mona", ActorID: -1},
		{ActionID: "a", Command: "c", ActorID: 101},
		{ActionID: "a", Command: "c", ActorID: -1},
		{ActionID: "a", Command: "c", Repository: "octo"},
		{ActionID: "a", Command: "c", Repository: "octo/api/extra"},
	} {
		if _, err := invocation.Encode(4096); !errors.Is(err, ErrCLICommandInput) {
			t.Fatalf("Encode(%+v) error = %v", invocation, err)
		}
	}
}
