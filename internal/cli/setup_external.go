package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"
)

type setupExternalSource struct {
	ID                       string    `json:"id"`
	Event                    string    `json:"event"`
	ResourceURLPrefix        string    `json:"resourceUrlPrefix"`
	Scope                    string    `json:"scope"`
	CollectionID             string    `json:"collectionId,omitempty"`
	SecretEnv                string    `json:"secretEnv"`
	DestinationRepositoryIDs []int64   `json:"destinationRepositoryIds"`
	ExpiresAt                time.Time `json:"expiresAt"`
}

type setupExternalOutbound struct {
	VerifiedVersion string   `json:"verifiedVersion"`
	Names           []string `json:"names"`
	DefaultSecrets  []string `json:"defaultSecrets"`
	Instructions    string   `json:"instructions"`
}

type setupExternalReport struct {
	Provider            string                `json:"provider"`
	Status              string                `json:"status"`
	WebhookURL          string                `json:"webhookUrl"`
	ExternalSources     []setupExternalSource `json:"externalSources"`
	Instructions        []string              `json:"instructions"`
	OutboundSafeOutputs setupExternalOutbound `json:"outboundSafeOutputs"`
}

func setupExternalCommand(provider string) *cobra.Command {
	cmd := operatorCommand(provider, "Prepare offline "+provider+" webhook enrollment and a manual setup guide", cobra.NoArgs, []operatorFlag{
		textFlag("gateway-url", "Deployed HTTPS gateway origin/base URL (optional deployment path; no credentials, query or fragment)"),
		textFlag("source-id", "Unique receiver route ID (1-64 ASCII letters, digits, dots, underscores or hyphens)"),
		textFlag("resource-url-prefix", "Canonical provider HTTPS issue REST/resource prefix ending /"),
		textFlag("scope", "Exact Jira project key, Linear organization ID, or Azure DevOps project ID"),
		{name: "destination-repository-id", description: "Exact positive safe-integer GitHub destination repository ID (repeatable; maximum 64)", repeat: true},
		textFlag("expires-at", "Future enrollment expiry in RFC3339 format"),
		textFlag("secret-env", "Receive-only deployment secret variable name (default CAO_GATEWAY_SOURCE_<ID>)"),
		jsonFlag,
		textFlag("output", "Create a new secret-free JSON report (0600; never overwrite files or symlinks)"),
		boolFlag("dry-run", "Prepare the same guide/report without creating an output file"),
	}, nil)
	cmd.Long = `Prepare external webhook enrollment natively in Go, entirely offline.

Print a human service-setup guide with enrollment JSON, or --json for a secret-free report containing
the webhook URL, externalSources enrollment fragment and manual instructions.
--output creates that JSON report exclusively with mode 0600; --dry-run never
writes it. Supply every authority binding explicitly; ambient credentials and
configuration are never read. This command does not contact GitHub or providers,
edit policy, create webhooks, deploy, verify access or activate authority.

Provision a separate 32-512 byte receive-only secret through the provider UI
and deployment secret manager. Secret values must never
be CLI arguments, output, policy, logs or committed files. Outbound gh-aw safe-output
credentials are separate and are not configured by this command. Review enrollment,
managed discovery and destination authority independently before restarting the
receiver; workflow declarations alone do not enroll sources.`
	if provider == "ado" {
		cmd.Aliases = []string{"azure-devops"}
		cmd.Flags().String("collection-id", "", "Exact Azure DevOps collection ID")
		_ = cmd.MarkFlagRequired("collection-id")
	}
	for _, flag := range []string{"gateway-url", "source-id", "resource-url-prefix", "scope", "destination-repository-id", "expires-at"} {
		_ = cmd.MarkFlagRequired(flag)
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runSetupExternal(cmd, provider)
	}
	return cmd
}

var setupExternalID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var setupExternalSecret = regexp.MustCompile(`^CAO_GATEWAY_SOURCE_[A-Z0-9_]+$`)
var setupExternalDecimal = regexp.MustCompile(`^[1-9][0-9]*$`)
var setupExternalDNS = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)
var setupExternalJiraPath = regexp.MustCompile(`^/rest/api/[23]/issue/[1-9][0-9]*$`)
var setupExternalLinearPath = regexp.MustCompile(`^/[A-Za-z0-9_-]+/issue/[A-Z][A-Z0-9_-]*-[1-9][0-9]*$`)
var setupExternalAzurePath = regexp.MustCompile(`^/[A-Za-z0-9_-]+(/([A-Za-z0-9_.-]|%[0-9a-fA-F]{2})+)?/_apis/wit/work[Ii]tems/[1-9][0-9]*$`)
var setupExternalAzureHost = regexp.MustCompile(`^[a-z0-9-]+\.visualstudio\.com$`)

func setupExternalClean(text string) bool {
	return !strings.ContainsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

// Mirror the receiver's canonical URL and provider shapes without importing the
// separate server module's internal packages. Authority inputs are never normalized.
func setupExternalURL(text string) (*url.URL, bool) {
	u, err := url.Parse(text)
	if err != nil || len(text) > 2048 || !setupExternalClean(text) ||
		u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(text, "\\?#") ||
		u.Host != strings.ToLower(u.Host) || u.String() != text {
		return nil, false
	}
	host := u.Hostname()
	if net.ParseIP(host) == nil && (!setupExternalDNS.MatchString(host) || len(host) > 253) {
		return nil, false
	}
	if u.Host != host && u.Host != "["+host+"]" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, false
		}
	}
	cleaned := path.Clean(u.Path)
	if u.Path != "" && u.Path != cleaned && (!strings.HasSuffix(u.Path, "/") || strings.TrimSuffix(u.Path, "/") != cleaned) {
		return nil, false
	}
	// A path is not allowed to conceal separators, dot segments or controls.
	if strings.ContainsAny(u.Path, "\\") || strings.ContainsFunc(u.Path, unicode.IsControl) {
		return nil, false
	}
	return u, true
}

func setupExternalPrefix(provider, prefix string) bool {
	if !strings.HasSuffix(prefix, "/") {
		return false
	}
	suffix := "1"
	if provider == "linear" {
		suffix = "APP-1"
	}
	u, ok := setupExternalURL(prefix + suffix)
	if !ok || u.Port() == "443" {
		return false
	}
	switch provider {
	case "jira":
		return setupExternalJiraPath.MatchString(u.EscapedPath())
	case "linear":
		return u.Host == "linear.app" && setupExternalLinearPath.MatchString(u.EscapedPath())
	case "ado":
		return (u.Host == "dev.azure.com" || setupExternalAzureHost.MatchString(u.Host)) &&
			setupExternalAzurePath.MatchString(u.EscapedPath())
	default:
		return false
	}
}

func prepareSetupExternal(cmd *cobra.Command, provider string, now time.Time) (setupExternalReport, error) {
	var report setupExternalReport
	base := nativeSetupFlag(cmd, "gateway-url")
	u, ok := setupExternalURL(base)
	if !ok || u.RawPath != "" || !setupExternalClean(u.Path) {
		return report, errors.New("--gateway-url requires a canonical HTTPS origin/base URL without credentials, query, fragment or escaped path")
	}
	id := nativeSetupFlag(cmd, "source-id")
	if !setupExternalID.MatchString(id) || id == "." || id == ".." || id == "compiler" || id == "github" {
		return report, errors.New("--source-id requires 1-64 ASCII letters, digits, dots, underscores or hyphens; '.', '..', 'compiler' and 'github' are reserved")
	}
	prefix := nativeSetupFlag(cmd, "resource-url-prefix")
	if len(prefix) > 2048 || !setupExternalPrefix(provider, prefix) {
		return report, errors.New("--resource-url-prefix requires a canonical provider HTTPS prefix ending /: Jira /rest/api/2/issue/ or /rest/api/3/issue/, Linear https://linear.app/WORKSPACE/issue/, or Azure DevOps /ORG[/PROJECT]/_apis/wit/workItems/")
	}
	scope := nativeSetupFlag(cmd, "scope")
	if scope == "" || len(scope) > 256 || !setupExternalClean(scope) {
		return report, errors.New("--scope requires an exact nonempty native scope of at most 256 bytes without whitespace or controls")
	}
	collection := ""
	if provider == "ado" {
		collection = nativeSetupFlag(cmd, "collection-id")
		if collection == "" || len(collection) > 256 || !setupExternalClean(collection) {
			return report, errors.New("--collection-id requires an exact nonempty native collection ID of at most 256 bytes without whitespace or controls")
		}
	}
	secret := nativeSetupFlag(cmd, "secret-env")
	if !cmd.Flags().Changed("secret-env") {
		secret = "CAO_GATEWAY_SOURCE_" + strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToUpper(id))
	}
	if !setupExternalSecret.MatchString(secret) || len(secret) > 128 || strings.HasSuffix(secret, "_PREVIOUS") {
		return report, errors.New("--secret-env must match CAO_GATEWAY_SOURCE_[A-Z0-9_]+, at most 128 bytes, without the reserved _PREVIOUS suffix")
	}
	expiresText := nativeSetupFlag(cmd, "expires-at")
	expires, err := time.Parse(time.RFC3339, expiresText)
	if err != nil || !setupExternalClean(expiresText) || !now.Before(expires) {
		return report, errors.New("--expires-at requires a future RFC3339 timestamp")
	}
	values, _ := cmd.Flags().GetStringArray("destination-repository-id")
	if len(values) == 0 || len(values) > 64 {
		return report, errors.New("supply 1-64 exact --destination-repository-id values")
	}
	ids := make([]int64, 0, len(values))
	seen := make(map[int64]bool)
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || !setupExternalDecimal.MatchString(value) || id > 1<<53-1 || seen[id] {
			return report, errors.New("--destination-repository-id requires unique exact positive safe integers (1-9007199254740991); duplicates are forbidden")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	output := nativeSetupFlag(cmd, "output")
	if cmd.Flags().Changed("output") && (output == "" || strings.TrimSpace(output) != output || strings.ContainsFunc(output, unicode.IsControl)) {
		return report, errors.New("--output requires a nonempty path without surrounding whitespace or controls")
	}
	event := map[string]string{"linear": "linear_issue", "jira": "jira_issue", "ado": "azure_devops_issue"}[provider]
	report = setupExternalReport{
		Provider: provider, Status: "prepared-only",
		WebhookURL: strings.TrimSuffix(base, "/") + "/webhooks/" + id,
		ExternalSources: []setupExternalSource{{
			ID: id, Event: event, ResourceURLPrefix: prefix, Scope: scope, CollectionID: collection,
			SecretEnv: secret, DestinationRepositoryIDs: ids, ExpiresAt: expires,
		}},
		Instructions: []string{
			"Prepared only: nothing deployed, contacted, verified or activated; no policy or provider settings changed.",
			fmt.Sprintf("Provision a separate 32-512 byte receive-only secret directly through the provider UI and deployment secret manager; inject it as %s into the receiver only. Use the same secret for provider authentication. Never put secret values in CLI arguments, output, policy, logs or committed files.", secret),
			"Have an operator independently review and merge the externalSources fragment into gateway policy. Require managed discovery, unique source IDs and secret names, and every destination ID in discovery.repositories. Review current destination installation access, workflow head/lock digest and CAO rollout authority separately. Restart the receiver after source enrollment; this report grants no authority.",
		},
		OutboundSafeOutputs: setupExternalOutbound{VerifiedVersion: "github.com/github/gh-aw@v0.92.0"},
	}
	switch provider {
	case "jira":
		report.Instructions = append(report.Instructions,
			"In Jira administration, configure a webhook for the enrolled project key (JQL project = the exact --scope), targeting webhookUrl. Select jira:issue_created, jira:issue_updated and jira:issue_deleted, with issue and changelog payloads; issue.self must use resourceUrlPrefix and the same decimal issue.id.",
			"Use a Jira webhook configuration that signs raw request bytes with X-Hub-Signature: sha256=<hex> (HMAC-SHA256) and supplies X-Atlassian-Webhook-Identifier for retry identity. Configure its secret directly, not a token in the URL. If the chosen Jira webhook type cannot supply both headers, do not enable it; unsigned delivery is unsupported.",
			fmt.Sprintf("Configure the matching gh-aw trigger jira_issue with its project filter set exactly to %q (the enrolled Jira project key), and choose explicit activity types before compiling the workflow. This declaration does not enroll or authorize the source.", scope))
		report.OutboundSafeOutputs.Names = []string{"jira-create-issue", "jira-update-issue", "jira-add-comment", "jira-add-label"}
		report.OutboundSafeOutputs.DefaultSecrets = []string{"JIRA_USER_EMAIL", "JIRA_API_TOKEN"}
		report.OutboundSafeOutputs.Instructions = "Only for separately approved outbound writes, explicitly declare the required safe-outputs and limits, set safe-outputs.env.JIRA_BASE_URL to your trusted Jira origin (never rely on the compiler's tenant default), and provision the scoped Actions secrets JIRA_USER_EMAIL and JIRA_API_TOKEN. Never reuse the receive-only secret."
	case "linear":
		report.Instructions = append(report.Instructions,
			"In Linear workspace Settings > API > Webhooks, create a webhook targeting webhookUrl, select Issue events (create, update, remove), and restrict it to the enrolled organization ID (--scope). Workspace-qualified data.url must be under resourceUrlPrefix.",
			"Copy the Linear webhook signing secret directly from its provider UI into the deployment secret manager (32-512 bytes); never print it here. Require raw-body HMAC-SHA256 Linear-Signature, Linear-Delivery retry identity, and signed webhookTimestamp within one minute of receiver time; synchronize the receiver clock. Durable accepted/filtered delivery returns HTTP 200.",
			fmt.Sprintf("Configure the matching gh-aw trigger linear_issue with its organization-id filter set exactly to %q (the enrolled Linear organization ID), and choose explicit activity types before compiling the workflow. This declaration does not enroll or authorize the source.", scope))
		report.OutboundSafeOutputs.Names = []string{"linear-create-issue", "linear-update-issue", "linear-add-comment"}
		report.OutboundSafeOutputs.DefaultSecrets = []string{"LINEAR_API_KEY", "LINEAR_TEAM_ID"}
		report.OutboundSafeOutputs.Instructions = "Only for separately approved outbound writes, explicitly declare the required safe-outputs and limits and provision a scoped LINEAR_API_KEY Actions secret (safe-outputs.linear-token may override it). linear-create-issue additionally defaults to the LINEAR_TEAM_ID secret; an explicit team-id may override it, and LINEAR_PROJECT_ID is an optional Actions variable. Never reuse the receive-only secret."
	case "ado":
		report.Instructions = append(report.Instructions,
			"In Azure DevOps Project settings > Service hooks, create Web Hooks subscriptions targeting webhookUrl for workitem.created, workitem.updated and workitem.deleted. Bind the exact project ID (--scope) and collection ID (--collection-id); use the enrolled work item REST resourceUrlPrefix, never a revision/update URL.",
			"Configure HTTPS Basic authentication with username cao and the receive-only secret as the password directly in the service hook UI. The gateway uses body id for delivery identity; Azure hooks do not claim native HMAC. Preserve resourceContainers and work item/change evidence (prefer resourceVersion 2.0 for updates); test delivery after reviewed deployment.",
			fmt.Sprintf("Configure the matching gh-aw trigger azure_devops_issue with its project_id filter set exactly to %q (the enrolled Azure project ID), and choose explicit activity types before compiling the workflow. This declaration does not enroll or authorize the source.", scope))
		report.OutboundSafeOutputs.Names = []string{"ado-create-work-item", "ado-update-work-item", "ado-comment-on-work-item", "ado-assign-work-item", "ado-link-work-items", "ado-upload-workitem-attachment"}
		report.OutboundSafeOutputs.DefaultSecrets = []string{"AZURE_DEVOPS_EXT_PAT"}
		report.OutboundSafeOutputs.Instructions = "Only for separately approved outbound writes, explicitly declare the required safe-outputs and limits and provision the scoped AZURE_DEVOPS_EXT_PAT Actions secret. Explicit safe-outputs.env.SYSTEM_ACCESSTOKEN or AZURE_DEVOPS_EXT_PAT can override the default. Configure the approved organization/project separately. Never reuse the receive-only password."
	}
	report.Instructions = append(report.Instructions,
		"Review and compile matching v2 provider workflow declarations separately; declarations cannot register sources. Restrict native scope and selected activities. Treat external references as private/restricted; URLs grant no provider access. Use separately authenticated provider readers for further data, and independently reviewed safe-output credentials for writes.")
	return report, nil
}

func runSetupExternal(cmd *cobra.Command, provider string) error {
	report, err := prepareSetupExternal(cmd, provider, time.Now())
	if err != nil {
		return err
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if output := nativeSetupFlag(cmd, "output"); output != "" && !nativeSetupBool(cmd, "dry-run") {
		if err := setupExternalWriteFile(output, content); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "Saved preparation report to", output); err != nil {
			return err
		}
	}
	if nativeSetupBool(cmd, "json") {
		_, err = cmd.OutOrStdout().Write(content)
		return err
	}
	enrollment, err := json.MarshalIndent(struct {
		ExternalSources []setupExternalSource `json:"externalSources"`
	}{report.ExternalSources}, "", "  ")
	if err != nil {
		return err
	}
	var guide strings.Builder
	source := report.ExternalSources[0]
	fmt.Fprintf(&guide, "%s webhook setup — prepared only\nWebhook URL: %s\nReceive-only secret variable: %s\nNative scope: %s\nResource URL prefix: %s\nDestination repository IDs: %v\nExpires at: %s\n",
		provider, report.WebhookURL, source.SecretEnv, source.Scope, source.ResourceURLPrefix, source.DestinationRepositoryIDs, source.ExpiresAt.Format(time.RFC3339))
	if source.CollectionID != "" {
		fmt.Fprintf(&guide, "Collection ID: %s\n", source.CollectionID)
	}
	guide.WriteString("\n")
	for i, step := range report.Instructions {
		fmt.Fprintf(&guide, "%d. %s\n\n", i+1, step)
	}
	fmt.Fprintf(&guide, "Separate outbound safe outputs (%s): %s\n%s\n\nUse --json for the full machine-readable report.\n\nEnrollment JSON (merge into reviewed gateway policy; preserve existing entries):\n%s\n",
		report.OutboundSafeOutputs.VerifiedVersion, strings.Join(report.OutboundSafeOutputs.Names, ", "), report.OutboundSafeOutputs.Instructions, enrollment)
	_, err = fmt.Fprint(cmd.OutOrStdout(), guide.String())
	return err
}

func setupExternalWriteFile(filename string, content []byte) error {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create preparation report (existing files and symlinks are never overwritten): %w", err)
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write preparation report: %w", errors.Join(err, os.Remove(filename)))
	}
	return nil
}
