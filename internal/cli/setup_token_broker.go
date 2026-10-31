package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

const tokenBrokerEnvPrefix = "CAO_TOKEN_BROKER_"

var (
	tokenBrokerID          = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	tokenBrokerHost        = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*$`)
	tokenBrokerVaultHost   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,22}[a-z0-9]\.vault\.azure\.net$`)
	tokenBrokerVaultPath   = regexp.MustCompile(`^/secrets/[a-zA-Z0-9-]{1,127}(/[a-zA-Z0-9-]{1,127})?$`)
	tokenBrokerRevision    = regexp.MustCompile(`^[a-f0-9]{40}$`)
	tokenBrokerWorkflowRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9][A-Za-z0-9._-]*/\.github/workflows/[A-Za-z0-9_-][A-Za-z0-9._-]*\.ya?ml@refs/(heads|tags)/[A-Za-z0-9_-]+([./][A-Za-z0-9_-]+)*$`)
)

func setupTokenBrokerCommand() *cobra.Command {
	cmd := operatorCommand("token-broker", "Prepare offline settings for an existing dedicated organization App token broker", cobra.NoArgs, []operatorFlag{
		textFlag("url", "Exact public HTTPS /api/token-broker endpoint and OIDC audience"),
		textFlag("app-id", "Existing dedicated organization App positive decimal ID"),
		textFlag("installation-id", "Existing dedicated App installation positive decimal ID"),
		textFlag("owner-id", "Exact organization owner positive decimal ID"),
		textFlag("key-vault-secret-url", "Existing Azure Key Vault HTTPS secret URL (not its value)"),
		textFlag("managed-identity-client-id", "Optional existing user-assigned managed identity GUID"),
		boolFlag("single-replica", "Acknowledge exactly one deployment replica; no overlapping replicas"),
		boolFlag("single-process", "Acknowledge one persistent process and volatile cleanup state lost on crashes"),
		textFlag("output", "Create a new environment JSON file (0600; never overwrite files or symlinks)"),
		boolFlag("dry-run", "Print settings without creating any file"),
	}, nil)
	cmd.Long = `Prepare secret-free token-broker environment settings natively in Go, offline.

Use an EXISTING dedicated organization GitHub App with copilot_requests: read,
its exact installation and owner IDs, an existing Key Vault secret containing
the App key, and an existing managed identity permitted to read that secret.
Supply the exact public HTTPS /api/token-broker endpoint; no redirects,
credentials, ports, queries or fragments are accepted.

Explicit --single-replica and --single-process acknowledgments are required.
The revocation registry is volatile process-local state: crashes can lose
cleanup until upstream token expiry (at most one hour). Durable operational
storage does not make broker cleanup durable. No overlapping replicas.

Print a JSON object of environment names and string values. --output also
creates a new private file; --dry-run never writes files. Ambient settings
are not read. No private keys, tokens or secrets are accepted or exported.
This does not contact GitHub/Azure, provision an App or Azure resources,
write GitHub secrets, change policy/scope, or enable workflow allowlists.

Hosted OAuth/HTTPS with a process listener remains a separate prerequisite.
Review the mounted control-plane.web.host target profile with replicas: 1;
there is no CAO_SINGLE_REPLICA environment setting. Separately review exact
control-plane.token-broker.copilot-requests.workflows IDs, workflow refs and
40-hex revision pins. Missing/empty allowlists are not production-ready.
Apply settings through the deployment manager, then run
gh cao doctor token-broker --settings PATH --policy PATH --json.
Runtime managed identity, App grants, OIDC, inference/renewal/invalidation
and cleanup verification remain explicit operator work.`
	for _, name := range []string{"url", "app-id", "installation-id", "owner-id", "key-vault-secret-url"} {
		_ = cmd.MarkFlagRequired(name)
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		settings := map[string]string{
			tokenBrokerEnvPrefix + "ENABLED":              "true",
			tokenBrokerEnvPrefix + "SINGLE_PROCESS":       "true",
			tokenBrokerEnvPrefix + "URL":                  nativeSetupFlag(cmd, "url"),
			tokenBrokerEnvPrefix + "APP_ID":               nativeSetupFlag(cmd, "app-id"),
			tokenBrokerEnvPrefix + "INSTALLATION_ID":      nativeSetupFlag(cmd, "installation-id"),
			tokenBrokerEnvPrefix + "OWNER_ID":             nativeSetupFlag(cmd, "owner-id"),
			tokenBrokerEnvPrefix + "KEY_VAULT_SECRET_URL": nativeSetupFlag(cmd, "key-vault-secret-url"),
		}
		if cmd.Flags().Changed("managed-identity-client-id") {
			value := nativeSetupFlag(cmd, "managed-identity-client-id")
			if !setupADXGUID.MatchString(value) {
				return errors.New("--managed-identity-client-id requires a GUID")
			}
			settings[tokenBrokerEnvPrefix+"MANAGED_IDENTITY_CLIENT_ID"] = value
		}
		if !nativeSetupBool(cmd, "single-replica") || !nativeSetupBool(cmd, "single-process") {
			return errors.New("explicit --single-replica and --single-process volatile-state acknowledgments are required")
		}
		if err := validateTokenBrokerSettings(settings); err != nil {
			return err
		}
		content, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return errors.New("cannot encode token broker settings")
		}
		content = append(content, '\n')
		if path := nativeSetupFlag(cmd, "output"); path != "" && !nativeSetupBool(cmd, "dry-run") {
			if err := writeTokenBrokerSettings(path, content); err != nil {
				return err
			}
		}
		if _, err := cmd.OutOrStdout().Write(content); err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.ErrOrStderr(), "Settings prepared only; nothing deployed or verified. Import the existing dedicated App key into Key Vault and grant the deployment managed identity secret-read access separately. Review the mounted hosted OAuth/HTTPS process-listener policy with target.replicas=1 and exact broker workflow allowlist revision pins; policy and scope are unchanged. Apply settings through your deployment manager; run gh cao doctor token-broker --settings PATH --policy PATH --json. Verify App permissions/installation, OIDC, inference/renewal/invalidation and crash cleanup explicitly before production; volatile cleanup can be lost until upstream expiry (at most one hour).")
		return err
	}
	return cmd
}

func tokenBrokerSafeURL(value string, vault bool) bool {
	u, err := url.Parse(value)
	if err != nil || len(value) > 2048 || u.Scheme != "https" || u.Opaque != "" ||
		u.User != nil || u.Host != u.Hostname() || u.Port() != "" || u.RawPath != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(value, "\\#") ||
		strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return false
	}
	if vault {
		return tokenBrokerVaultHost.MatchString(strings.ToLower(u.Hostname())) &&
			!strings.Contains(u.Hostname(), "--") && tokenBrokerVaultPath.MatchString(u.Path)
	}
	return tokenBrokerHost.MatchString(u.Hostname()) && u.Path == "/api/token-broker"
}

func validateTokenBrokerSettings(settings map[string]string) error {
	for _, name := range []string{"APP_ID", "INSTALLATION_ID", "OWNER_ID"} {
		value := settings[tokenBrokerEnvPrefix+name]
		if !tokenBrokerID.MatchString(value) {
			return errors.New("token broker requires exact positive decimal App, installation and owner IDs")
		}
		if name != "OWNER_ID" {
			if _, err := strconv.ParseInt(value, 10, 64); err != nil {
				return errors.New("token broker App and installation IDs must fit positive signed 64-bit integers")
			}
		}
	}
	if !tokenBrokerSafeURL(settings[tokenBrokerEnvPrefix+"URL"], false) {
		return errors.New("token broker URL requires the exact public HTTPS /api/token-broker endpoint without credentials, port, query or fragment")
	}
	if !tokenBrokerSafeURL(settings[tokenBrokerEnvPrefix+"KEY_VAULT_SECRET_URL"], true) {
		return errors.New("token broker requires a safe Azure Key Vault HTTPS secret URL without credentials, port, query or fragment")
	}
	if value := settings[tokenBrokerEnvPrefix+"MANAGED_IDENTITY_CLIENT_ID"]; value != "" && !setupADXGUID.MatchString(value) {
		return errors.New("token broker managed identity client ID must be a GUID")
	}
	if settings[tokenBrokerEnvPrefix+"SINGLE_PROCESS"] != "true" {
		return errors.New("token broker requires explicit single-process volatile-state acknowledgment")
	}
	return nil
}

func writeTokenBrokerSettings(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("cannot create token broker settings file; existing files and symlinks are never overwritten")
	}
	_, writeErr := file.Write(content)
	syncErr := file.Sync()
	closeErr := file.Close()
	if errors.Join(writeErr, syncErr, closeErr) != nil {
		_ = os.Remove(path)
		return errors.New("cannot save token broker settings file")
	}
	return nil
}
