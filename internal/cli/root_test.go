package cli

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"root help", nil, "Configure and operate Central Agentic Ops"},
		{"help flag", []string{"--help"}, "gh cao [command]"},
		{"version flag", []string{"--version"}, "gh cao version test\n"},
		{"version command", []string{"version"}, "gh cao version test\n"},
		{"doctor help", []string{"doctor", "--help"}, "gh cao doctor"},
		{"init help", []string{"init", "--help"}, "does not install runtime files, configure Pages or credentials"},
		{"setup help", []string{"setup", "--help"}, "No prior init or installer"},
		{"evidence help", []string{"evidence", "--help"}, "gh cao evidence"},
		{"release evidence help", []string{"evidence", "release", "--help"}, "gh cao evidence release"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRootCommand("test", func(context.Context) (string, error) {
				t.Fatal("help and version must not invoke gh")
				return "", nil
			})
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(tt.args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stdout.String(), tt.want) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), tt.want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %s", &stderr)
			}
		})
	}
}

func TestCommandReferenceCoversRegisteredOperatorCommands(t *testing.T) {
	data, err := os.ReadFile("../../docs/cao-cli.md")
	if err != nil {
		t.Fatal(err)
	}
	_, reference, found := strings.Cut(string(data), "| Surface | Commands | Execution |")
	if !found {
		t.Fatal("command reference table missing")
	}
	reference, _, found = strings.Cut(reference, "\n\n")
	if !found {
		t.Fatal("command reference table end missing")
	}
	documented := map[string]bool{}
	for _, match := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(reference, -1) {
		documented[strings.Fields(match[1])[0]] = true
	}
	for _, command := range NewRootCommand("test").Commands() {
		if command.Hidden {
			continue
		}
		if !documented[command.Name()] {
			t.Errorf("registered operator command %q missing from command reference", command.Name())
		}
	}
	root := NewRootCommand("test")
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, summary := range []string{
		"Optionally initialize local policy only, without runtime or remote setup",
		"Prepare monitoring runtime, policy, scope, and authentication",
	} {
		if !strings.Contains(output.String(), summary) {
			t.Errorf("root help missing %q", summary)
		}
	}
}

func TestInvalidCommands(t *testing.T) {
	for _, args := range [][]string{
		{"setup", "extra"},
		{"--unknown"},
		{"version", "extra"},
		{"doctor", "extra"},
		{"doctor", "--unknown"},
		{"evidence", "release"},
		{"evidence", "release", "extra"},
		{"evidence", "release", "--unknown"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := newRootCommand("test", func(context.Context) (string, error) {
				t.Fatal("invalid input must not invoke gh")
				return "", nil
			})
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetArgs(args)
			if err := root.ExecuteContext(context.Background()); err == nil {
				t.Fatal("expected an error for unsupported input")
			}
			if output.Len() != 0 {
				t.Fatalf("error must be rendered once by the entry point, got %q", output.String())
			}
		})
	}
}
