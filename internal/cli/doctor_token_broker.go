package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

const tokenBrokerDocumentLimit = 1 << 20

func doctorTokenBrokerCommand() *cobra.Command {
	var settingsPath, localPolicyPath string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use: "token-broker", Short: "Diagnose token broker settings and mounted policy offline",
		Long: `Diagnose offline using a deployment environment JSON object exported by gh cao setup token-broker.
Never source a shell file or read ambient credentials. Reads are bounded to 1 MiB;
duplicate JSON keys, malformed settings and malformed broker allowlists fail closed.
No external gh, Node.js, Azure, GitHub or runtime calls; no files or policy are changed.

Checks enabled/disabled selection, exact IDs and HTTPS URLs, single-process
acknowledgment, broker workflow allowlist shape and revision pins, and locally
visible hosted OAuth/HTTPS process-listener and single-replica policy prerequisites.
An enabled broker with a missing/empty allowlist is not production-ready.
Host profile imports are not resolved; supply a reviewed composed mounted policy
for complete local profile diagnostics. Hosted credentials and allowed-host
deployment settings remain separate prerequisites.

Runtime verification is NOT performed: verify managed identity/Key Vault access,
dedicated App permissions and organization installation, reviewed allowed host,
OIDC, inference/renewal/invalidation and volatile cleanup explicitly.
Diagnostic messages never echo raw settings, credentials, policy fields or errors.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := diagnoseTokenBroker(settingsPath, localPolicyPath)
			if jsonOutput {
				if err := printJSON(cmd, report); err != nil {
					return err
				}
			} else {
				for _, check := range report.Checks {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s: %s\n", strings.ToUpper(check.Status), check.Name, check.Message); err != nil {
						return err
					}
					if check.Remediation != "" {
						if _, err := fmt.Fprintln(cmd.OutOrStdout(), "  "+check.Remediation); err != nil {
							return err
						}
					}
				}
			}
			if !report.OK {
				return errors.New("token broker doctor found local configuration problems; follow the reported remediation")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&settingsPath, "settings", "", "Exported environment JSON file (not a shell file)")
	cmd.Flags().StringVar(&localPolicyPath, "policy", policyPath, "Reviewed mounted CAO policy JSON file")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write safe machine-readable diagnostic results")
	_ = cmd.MarkFlagRequired("settings")
	return cmd
}

func tokenBrokerReadDocument(path string) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > tokenBrokerDocumentLimit {
		return nil, errors.New("document unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("document unavailable")
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("document unavailable")
	}
	content, err := io.ReadAll(io.LimitReader(file, tokenBrokerDocumentLimit+1))
	if err != nil || len(content) > tokenBrokerDocumentLimit {
		return nil, errors.New("document unavailable")
	}
	// Bound nesting before the shared recursive unique-key decoder sees input.
	decoder := json.NewDecoder(bytes.NewReader(content))
	depth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("invalid document")
		}
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter == '{' || delimiter == '[' {
				depth++
			} else {
				depth--
			}
			if depth > 64 {
				return nil, errors.New("invalid document")
			}
		}
	}
	value, err := nativeDecodeJSON(content)
	if err != nil {
		return nil, errors.New("invalid document")
	}
	document, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("invalid document")
	}
	return document, nil
}

func tokenBrokerAddCheck(report *doctorReport, name, status, message, remediation string) {
	report.Checks = append(report.Checks, doctorCheck{name, status, message, remediation})
	if status == "fail" {
		report.OK = false
	}
}

func diagnoseTokenBroker(settingsPath, localPolicyPath string) doctorReport {
	report := doctorReport{Command: "doctor token-broker", OK: true, Checks: []doctorCheck{}}
	add := func(name, status, message, remediation string) {
		tokenBrokerAddCheck(&report, name, status, message, remediation)
	}
	document, err := tokenBrokerReadDocument(settingsPath)
	settings := map[string]string{}
	if err == nil {
		for name, value := range document {
			text, ok := value.(string)
			if !ok || !nativeSecret.MatchString(name) {
				err = errors.New("invalid environment document")
				break
			}
			settings[name] = text
		}
	}
	enabled := false
	if err != nil {
		add("broker-settings", "fail", "Settings must be a readable bounded JSON environment object with string values and unique keys.", "Export a fresh secret-free document with gh cao setup token-broker.")
	} else {
		value := settings[tokenBrokerEnvPrefix+"ENABLED"]
		switch value {
		case "", "false":
			add("broker-enabled", "pass", "Token broker is disabled; no issuance profile is selected.", "")
		case "true":
			enabled = true
			add("broker-enabled", "pass", "Token broker is explicitly enabled in the supplied document.", "")
			if err := validateTokenBrokerSettings(settings); err != nil {
				add("broker-settings", "fail", err.Error(), "Regenerate settings with explicit exact IDs, HTTPS endpoint, Key Vault URL and single-process acknowledgment.")
			} else {
				add("broker-settings", "pass", "Broker identity, endpoint, Key Vault URL and single-process acknowledgment have valid local shape.", "")
			}
			if hosts, supplied := settings["CAO_ALLOWED_HOSTS"]; supplied {
				endpoint, _ := url.Parse(settings[tokenBrokerEnvPrefix+"URL"])
				matches := false
				if endpoint != nil {
					for _, host := range strings.Split(hosts, ",") {
						if strings.EqualFold(strings.TrimSpace(host), endpoint.Hostname()) && endpoint.Hostname() != "" {
							matches = true
						}
					}
				}
				if !matches {
					add("broker-allowed-host", "fail", "Supplied allowed-host settings do not admit the exact broker endpoint host.", "Review CAO_ALLOWED_HOSTS separately; credential reach does not authorize additional hosts.")
				} else {
					add("broker-allowed-host", "pass", "Supplied allowed-host settings include the broker endpoint host.", "Verify these are the settings applied to the running deployment.")
				}
			}
		default:
			add("broker-enabled", "fail", "Broker enabled selection must be exactly true, false or absent.", "Use string environment values from gh cao setup token-broker.")
		}
	}
	policy, policyErr := tokenBrokerReadDocument(localPolicyPath)
	if policyErr != nil {
		status := "fail"
		if _, statErr := os.Stat(localPolicyPath); !enabled && os.IsNotExist(statErr) {
			status = "warn"
		}
		add("broker-policy", status, "Mounted policy is missing, unreadable, oversized or invalid JSON; broker readiness cannot be established.", "Supply the exact reviewed mounted policy via --policy; duplicate keys are forbidden.")
	} else {
		tokenBrokerPolicyCheck(&report, policy, enabled, settings[tokenBrokerEnvPrefix+"OWNER_ID"])
		if enabled {
			tokenBrokerHostCheck(&report, policy)
		}
	}
	add("broker-runtime", "warn", "Runtime verification was not performed; local checks do not establish production readiness.", "Separately verify hosted OAuth/HTTPS, CAO_ALLOWED_HOSTS matching the endpoint, one persistent process/replica with no overlap, managed identity/Key Vault, dedicated App grants/installation, OIDC, inference/renewal/invalidation and crash cleanup (volatile until upstream expiry, at most one hour).")
	return report
}

func tokenBrokerPolicyCheck(report *doctorReport, policy map[string]any, required bool, ownerID string) {
	add := func(status, message string) {
		tokenBrokerAddCheck(report, "broker-policy", status, message, "Review control-plane.token-broker.copilot-requests.workflows in the mounted policy; pin exact immutable IDs, workflow refs and 40-lowercase-hex revisions. This does not grant rollout authority.")
	}
	control, controlOK := policy["control-plane"].(map[string]any)
	if _, exists := policy["control-plane"]; exists && !controlOK {
		add("fail", "Broker policy requires control-plane to be an object.")
		return
	}
	value, exists := control["token-broker"]
	if !exists {
		if required {
			add("fail", "Broker workflow allowlist is missing; enabled broker is not production-ready.")
		}
		return
	}
	broker, ok := value.(map[string]any)
	requests, requestsOK := broker["copilot-requests"].(map[string]any)
	workflows, workflowsOK := requests["workflows"].([]any)
	if !ok || len(broker) != 1 || !requestsOK || len(requests) != 1 || !workflowsOK || len(workflows) > 128 {
		add("fail", "Broker policy requires a strict copilot-requests object containing an array of at most 128 workflow rules.")
		return
	}
	seen := map[[4]string]bool{}
	for _, value := range workflows {
		rule, ok := value.(map[string]any)
		var identity [4]string
		for i, name := range []string{"repository-id", "owner-id", "workflow-ref", "workflow-sha"} {
			identity[i], _ = rule[name].(string)
		}
		if !ok || len(rule) != 4 || !tokenBrokerID.MatchString(identity[0]) ||
			!tokenBrokerID.MatchString(identity[1]) || !tokenBrokerValidWorkflowRef(identity[2]) ||
			!tokenBrokerRevision.MatchString(identity[3]) || seen[identity] ||
			(ownerID != "" && required && identity[1] != ownerID) {
			add("fail", "Broker workflow rules must be unique exact string IDs, valid workflow refs and pinned revisions matching the configured owner.")
			return
		}
		seen[identity] = true
	}
	if len(workflows) == 0 {
		status := "warn"
		if required {
			status = "fail"
		}
		add(status, "Broker workflow allowlist is empty; it denies all issuance and is not production-ready.")
		return
	}
	add("pass", "Broker workflow allowlist has valid local shape; grants, workflow existence and runtime authorization are not verified.")
}

func tokenBrokerValidWorkflowRef(value string) bool {
	if !tokenBrokerWorkflowRef.MatchString(value) || strings.Contains(value, "..") {
		return false
	}
	_, ref, _ := strings.Cut(value, "@")
	for _, segment := range strings.Split(ref, "/") {
		if strings.HasSuffix(segment, ".lock") {
			return false
		}
	}
	return true
}

func tokenBrokerHostCheck(report *doctorReport, policy map[string]any) {
	control, _ := policy["control-plane"].(map[string]any)
	web, _ := control["web"].(map[string]any)
	host, _ := web["host"].(map[string]any)
	target, _ := host["target"].(map[string]any)
	module, _ := target["module"].(string)
	valid := target["replicas"] == json.Number("1")
	switch module {
	case "container":
		for _, field := range []string{"authentication", "listener", "require-https", "trust-platform-proxy", "supports-single-replica"} {
			if _, exists := target[field]; exists {
				valid = false
			}
		}
	case "generic":
		valid = valid && target["authentication"] == "github-oauth" &&
			target["listener"] == "process" && target["supports-single-replica"] == true
		if value, exists := target["require-https"]; exists && value != true {
			valid = false
		}
	default:
		valid = false
	}
	if operationalValue, exists := host["operational-store"]; exists {
		operational, ok := operationalValue.(map[string]any)
		_, legacy := host["redis"]
		valid = valid && ok && !legacy
		switch operational["backend"] {
		case "memory":
			valid = valid && operational["single-process"] == true && operational["allow-volatile"] == true
			if _, exists := operational["redis"]; exists {
				valid = false
			}
			if _, exists := operational["postgres"]; exists {
				valid = false
			}
		case "postgres", "redis":
			for _, name := range []string{"single-process", "allow-volatile"} {
				if value, exists := operational[name]; exists && value != false {
					valid = false
				}
			}
			incompatible := "redis"
			if operational["backend"] == "redis" {
				incompatible = "postgres"
			}
			if _, exists := operational[incompatible]; exists {
				valid = false
			}
		default:
			valid = false
		}
	}
	if _, imports := policy["extends"]; imports {
		tokenBrokerAddCheck(report, "broker-host-profile", "fail", "Host policy imports are unresolved; complete local profile validation requires a composed policy.", "Supply the reviewed composed mounted policy; no imported authority is inferred.")
		return
	}
	if !valid {
		tokenBrokerAddCheck(report, "broker-host-profile", "fail", "Broker requires a reviewed hosted OAuth/HTTPS process-listener target with exactly one replica.", "Review control-plane.web.host.target separately: container with replicas: 1, or explicit compatible generic capabilities; platform/external listeners and overlapping replicas are unsupported.")
		return
	}
	tokenBrokerAddCheck(report, "broker-host-profile", "pass", "Local host target declares compatible hosted OAuth/HTTPS process-listener and single-replica prerequisites.", "Verify actual deployment topology, hosted credentials, operational store and reviewed allowed host separately.")
}

func readDoctorPolicy() (map[string]any, error) {
	policy, err := tokenBrokerReadDocument(policyPath)
	if err != nil {
		return nil, err
	}
	version, ok := policy["version"].(json.Number)
	number, err := version.Float64()
	if !ok || err != nil || number != 1 {
		return nil, errors.New("invalid policy version")
	}
	if value, exists := policy["control-plane"]; exists {
		control, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("invalid control plane")
		}
		for _, key := range []string{"campaigns", "workflows", "operations"} {
			if value, exists := control[key]; exists {
				if _, ok := value.(map[string]any); !ok {
					return nil, errors.New("invalid control plane")
				}
			}
		}
	}
	return policy, nil
}
