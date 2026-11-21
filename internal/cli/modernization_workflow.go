package cli

import (
	"strings"

	"gopkg.in/yaml.v3"
)

const modernizationFenceScript = `# CAO_MODERNIZATION_REVIEWED_SHA_V1
if [ -n "$EXPECTED_CONTROL_SHA" ] && [ "$EXPECTED_CONTROL_SHA" != "$ACTUAL_CONTROL_SHA" ]; then
  echo "Reviewed modernization control revision changed; request a fresh preview." >&2
  exit 1
fi`

func modernizationWorkflowFenced(content []byte, compiled bool) bool {
	source := string(content)
	job := "pre_activation"
	if !compiled {
		job = "pre-activation"
		if !strings.HasPrefix(source, "---\n") {
			return false
		}
		var found bool
		source, _, found = strings.Cut(strings.TrimPrefix(source, "---\n"), "\n---")
		if !found {
			return false
		}
	}
	var document struct {
		Jobs map[string]struct {
			Steps []struct {
				Run, If, Shell   string
				WorkingDirectory string            `yaml:"working-directory"`
				ContinueOnError  any               `yaml:"continue-on-error"`
				Env              map[string]string `yaml:"env"`
			}
		}
	}
	if yaml.Unmarshal([]byte(source), &document) != nil {
		return false
	}
	for _, step := range document.Jobs[job].Steps {
		if strings.TrimSpace(step.Run) == modernizationFenceScript &&
			step.Env["EXPECTED_CONTROL_SHA"] == "${{ inputs.expected_control_sha }}" &&
			step.Env["ACTUAL_CONTROL_SHA"] == "${{ github.workflow_sha }}" &&
			step.If == "" && step.ContinueOnError == nil && step.WorkingDirectory == "" &&
			(step.Shell == "" || step.Shell == "bash") {
			return true
		}
	}
	return false
}
