package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"
)

func setupADXCommand() *cobra.Command {
	cmd := operatorCommand("adx", "Prepare secret-free settings for an existing ADX snapshot destination", cobra.NoArgs, []operatorFlag{
		textFlag("endpoint", "Existing public-cloud HTTPS Kusto query endpoint (not ingest-)"),
		textFlag("database", "Existing ADX database identifier"),
		textFlag("tenant-id", "Microsoft Entra tenant GUID"),
		textFlag("client-id", "Existing Microsoft Entra application GUID"),
		{name: "repository-id", description: "Already collected exact canonical repository ID (repeatable; not OWNER/REPO discovery)", repeat: true},
		boolFlag("data-approved", "Acknowledge corpus/use and destination meet applicable terms, privacy and governance"),
		boolFlag("non-personal-data", "Alternative approval that the dataset is non-personal and non-regulated"),
		boolFlag("single-replica", "Confirm exactly one exporter with durable operational state and a persistent worker lifecycle"),
		textFlag("output", "Create a new deployment environment JSON file (mode 0600; never overwrite)"),
		boolFlag("dry-run", "Print settings without creating the output file"),
	}, nil)
	cmd.Long = `Prepare optional ADX snapshot export settings natively in Go, offline.

Supply an existing destination, Entra application identity and exact canonical
repository IDs already collected in this CAO namespace. Explicit --data-approved
(or --non-personal-data) and --single-replica acknowledgments are required.
These record operator decisions, not an assessment of the data or deployment.

Print a JSON object of environment variable names and string values. --output
also saves that object to a new private file; --dry-run never writes a file.
No ambient CAO_ADX_* settings or credentials are read. CAO_ADX_CLIENT_SECRET
is intentionally omitted: inject it separately from your deployment secret
manager, never a flag, policy file or committed environment document.

Review and apply the generated KQL separately with cao-dashboard adx schema
before deploying these settings. This command does not contact Azure/GitHub,
provision resources, apply schema, verify access, deploy/restart the server,
change rollout/collection policy, or enable trace capture, mining or training.
The prepared snapshot-only profile explicitly disables traces and mining.
Local SQLite, Pages, volatile state and per-request workers are unsupported.
Use an existing single-replica hosted PostgreSQL deployment with durable
operational coordination/state; normal hosted authorization still applies.`
	cmd.Flags().Duration("interval", 30*time.Second, "Retry/poll interval (1s to 1h)")
	cmd.Flags().Int64("max-storage-bytes", 50_000_000_000, "Uncompressed owned-table budget (1 MiB to 1 TiB)")
	for _, name := range []string{"endpoint", "database", "tenant-id", "client-id", "repository-id"} {
		_ = cmd.MarkFlagRequired(name)
	}
	cmd.RunE = runSetupADX
	return cmd
}

var setupADXIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,99}$`)
var setupADXGUID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var setupADXHostname = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*\.kusto\.windows\.net$`)

func setupADXEnvironment(cmd *cobra.Command) (map[string]string, error) {
	endpoint := nativeSetupFlag(cmd, "endpoint")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") || u.RawPath != "" ||
		!setupADXHostname.MatchString(u.Host) || strings.HasPrefix(u.Host, "ingest-") {
		return nil, errors.New("--endpoint requires a public-cloud HTTPS Kusto query endpoint without credentials, port, path or query")
	}
	if !setupADXIdentifier.MatchString(nativeSetupFlag(cmd, "database")) {
		return nil, errors.New("--database requires an ASCII letter followed by letters, digits or underscores (maximum 100 characters)")
	}
	for _, name := range []string{"tenant-id", "client-id"} {
		if !setupADXGUID.MatchString(nativeSetupFlag(cmd, name)) {
			return nil, fmt.Errorf("--%s requires a GUID", name)
		}
	}
	approved := nativeSetupBool(cmd, "data-approved")
	nonPersonal := nativeSetupBool(cmd, "non-personal-data")
	if !approved && !nonPersonal {
		return nil, errors.New("explicit --data-approved or --non-personal-data acknowledgment is required")
	}
	if !nativeSetupBool(cmd, "single-replica") {
		return nil, errors.New("explicit --single-replica acknowledgment is required")
	}
	ids, _ := cmd.Flags().GetStringArray("repository-id")
	if len(ids) == 0 || len(ids) > 1000 {
		return nil, errors.New("supply 1-1000 exact canonical --repository-id values")
	}
	ids = slices.Clone(ids)
	for _, id := range ids {
		if id == "" || len(id) > 512 || strings.TrimSpace(id) != id ||
			strings.ContainsAny(id, ",*?") || strings.ContainsFunc(id, unicode.IsControl) {
			return nil, errors.New("--repository-id requires a nonempty exact canonical ID of at most 512 bytes, without wildcards, commas, controls or surrounding whitespace")
		}
	}
	slices.Sort(ids)
	if len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return nil, errors.New("duplicate --repository-id values are not allowed")
	}
	interval, _ := cmd.Flags().GetDuration("interval")
	if interval < time.Second || interval > time.Hour {
		return nil, errors.New("--interval must be between 1s and 1h")
	}
	budget, _ := cmd.Flags().GetInt64("max-storage-bytes")
	if budget < 1<<20 || budget > 1<<40 {
		return nil, errors.New("--max-storage-bytes must be between 1 MiB and 1 TiB")
	}
	return map[string]string{
		"CAO_ADX_ENABLED":              "true",
		"CAO_ADX_ENDPOINT":             strings.TrimSuffix(endpoint, "/"),
		"CAO_ADX_DATABASE":             nativeSetupFlag(cmd, "database"),
		"CAO_ADX_TENANT_ID":            nativeSetupFlag(cmd, "tenant-id"),
		"CAO_ADX_CLIENT_ID":            nativeSetupFlag(cmd, "client-id"),
		"CAO_ADX_REPOSITORY_IDS":       strings.Join(ids, ","),
		"CAO_ADX_DATA_APPROVED":        strconv.FormatBool(approved),
		"CAO_ADX_NON_PERSONAL_DATA":    strconv.FormatBool(nonPersonal),
		"CAO_ADX_SINGLE_REPLICA":       "true",
		"CAO_ADX_INTERVAL":             interval.String(),
		"CAO_ADX_MAX_STORAGE_BYTES":    strconv.FormatInt(budget, 10),
		"CAO_ADX_TRACES_ENABLED":       "false",
		"CAO_ADX_TRACE_MINING_ENABLED": "false",
	}, nil
}

func runSetupADX(cmd *cobra.Command, _ []string) error {
	settings, err := setupADXEnvironment(cmd)
	if err != nil {
		return err
	}
	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	path := nativeSetupFlag(cmd, "output")
	if path != "" && !nativeSetupBool(cmd, "dry-run") {
		if err := setupADXWriteFile(path, content); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "Saved ADX deployment settings to", path); err != nil {
			return err
		}
	}
	if _, err := cmd.OutOrStdout().Write(content); err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.ErrOrStderr(), "Settings prepared only; nothing deployed or verified. Review/apply `cao-dashboard adx schema`, inject CAO_ADX_CLIENT_SECRET through the deployment secret manager, then apply these environment settings to the approved single-replica hosted deployment. Traces and mining remain disabled.")
	return err
}

func setupADXWriteFile(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create ADX settings file (existing files and symlinks are never overwritten): %w", err)
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		removeErr := os.Remove(path)
		return fmt.Errorf("write ADX settings file: %w", errors.Join(err, removeErr))
	}
	return nil
}
