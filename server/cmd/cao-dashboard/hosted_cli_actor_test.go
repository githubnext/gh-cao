package main

import (
	"strings"
	"testing"
)

func TestValidateHostedCLIActorAcceptsGitHubLogins(t *testing.T) {
	for name, raw := range map[string]string{
		"plain":          "octocat",
		"trims space":    "  octocat\n",
		"hyphen":         "octo-cat",
		"single char":    "a",
		"bot suffix":     "dependabot[bot]",
		"max length":     strings.Repeat("a", 39),
		"bot max length": strings.Repeat("a", 39) + "[bot]",
		"numeric":        "42",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := validateHostedCLIActor(raw)
			if err != nil {
				t.Fatalf("validateHostedCLIActor(%q) error = %v", raw, err)
			}
			if want := strings.TrimSpace(raw); got != want {
				t.Fatalf("validateHostedCLIActor(%q) = %q, want %q", raw, got, want)
			}
		})
	}
}

func TestValidateHostedCLIActorRejectsInvalidLogins(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":           "",
		"whitespace only": "  \t",
		"leading hyphen":  "-octocat",
		"underscore":      "octo_cat",
		"space inside":    "octo cat",
		"too long":        strings.Repeat("a", 40),
		"slash":           "octo/cat",
		"newline inside":  "octo\ncat",
		"bare bot suffix": "[bot]",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := validateHostedCLIActor(raw)
			if err == nil {
				t.Fatalf("validateHostedCLIActor(%q) = %q, want error", raw, got)
			}
			if got != "" {
				t.Fatalf("validateHostedCLIActor(%q) returned %q alongside an error", raw, got)
			}
			if !strings.Contains(err.Error(), "--actor must be a valid GitHub login") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestHostedCLIQueueCancelRejectsInvalidActorBeforeStorage(t *testing.T) {
	out, err := runHostedCLIQueueTestCommand(t, "cancel", "1", "--actor", "not a login")
	if err == nil || !strings.Contains(err.Error(), "--actor must be a valid GitHub login") {
		t.Fatalf("cancel error = %v, output %q", err, out)
	}
}
