package server

import (
	"errors"
	"os"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/hostedcli"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

// Podman is the only OCI runtime. Apple and Windows Sandbox remain explicit
// local-development profiles; no runtime falls back to another.
func hostedCLIContainerRuntime() string {
	value := strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_CONTAINER_RUNTIME"))
	if value == "" {
		return "podman"
	}
	return value
}

func hostedCLIQueueOptions(localDebug bool) (operational.CLICommandQueueOptions, error) {
	options := operational.CLICommandQueueOptions{}
	if localDebug && hostedCLIContainerRuntime() == "windows-sandbox" {
		options.MaxConcurrency = 1
	}
	return options.Resolve()
}

func newHostedCLIRunnerFromEnv(options HostedCLIExecutorOptions) (*hostedcli.ContainerRunner, error) {
	provider := hostedCLIContainerRuntime()
	image := strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_CONTAINER_IMAGE"))
	switch provider {
	case "podman":
		policy := strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_EGRESS_POLICY_FILE"))
		relay := strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_EGRESS_RELAY"))
		digest := strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_EGRESS_RELAY_SHA256"))
		var egress *hostedcli.ContainerEgress
		// Only explicit local development without any egress setting runs on
		// bridge networking; partial settings fail instead of falling back.
		if !options.LocalDevelopment || policy != "" || relay != "" || digest != "" {
			var err error
			egress, err = hostedcli.LoadContainerEgress(policy, relay, digest)
			if err != nil {
				return nil, err
			}
		}
		lifecycle, err := hostedcli.NewPodmanContainerLifecycle(strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_PODMAN")), hostedcli.ExecRunner{}, egress)
		if err != nil {
			return nil, err
		}
		return hostedcli.NewContainerRunner(image, lifecycle)
	case "apple", "windows-sandbox":
		if !options.LocalDevelopment {
			return nil, errors.New("native hosted CLI runtimes are for local development only and require execute-cli --local-development")
		}
	default:
		return nil, errors.New("CAO_HOSTED_CLI_CONTAINER_RUNTIME must be podman, apple, or windows-sandbox")
	}
	if provider == "apple" {
		lifecycle, err := hostedcli.NewAppleContainerLifecycle(strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_APPLE_CONTAINER")), hostedcli.ExecRunner{})
		if err != nil {
			return nil, err
		}
		return hostedcli.NewContainerRunner(image, lifecycle)
	}
	if image != "" {
		return nil, errors.New("the Windows Sandbox profile uses a pinned Windows tools bundle, not CAO_HOSTED_CLI_CONTAINER_IMAGE")
	}
	return hostedcli.NewWindowsSandboxRunner(hostedcli.WindowsSandboxOptions{
		Binary:       strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_WINDOWS_SANDBOX")),
		ToolsArchive: strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_WINDOWS_BUNDLE")),
		SHA256:       strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_WINDOWS_BUNDLE_SHA256")),
	}, hostedcli.ExecRunner{})
}
