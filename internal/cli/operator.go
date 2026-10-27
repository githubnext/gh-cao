package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type operatorFlag struct {
	name, short, description string
	repeat, boolean          bool
}

func textFlag(name, description string) operatorFlag {
	return operatorFlag{name: name, description: description}
}

func boolFlag(name, description string) operatorFlag {
	return operatorFlag{name: name, description: description, boolean: true}
}

var (
	databaseFlag  = textFlag("database", "Existing SQLite snapshot (default .cao/gh-aw-logs.sqlite)")
	dashboardFlag = textFlag("dashboard", "Dashboard Language document")
	jsonFlag      = boolFlag("json", "Write machine-readable JSON")
	paramFlag     = operatorFlag{name: "param", description: "Query parameter NAME=VALUE (repeatable)", repeat: true}
)

func addOperatorCommands(root *cobra.Command, execute runtimeExecutor, github ghExecutor) {
	download := operatorCommand("download", "Download the deployed CAO evidence snapshot", cobra.NoArgs, []operatorFlag{
		textFlag("url", "Dashboard payload-hashes.json URL"),
		textFlag("output", "Local output directory (default .cao)"),
		textFlag("manifest-sha256", "Trusted SHA-256 digest of the manifest"),
	}, execute)
	pages := operatorCommand("pages [PAGE_ID]", "List or describe agent-facing dashboard pages", cobra.MaximumNArgs(1), []operatorFlag{dashboardFlag, jsonFlag}, execute)
	queries := operatorCommand("queries", "List reviewed dashboard queries", cobra.NoArgs, []operatorFlag{dashboardFlag, jsonFlag}, execute)
	info := operatorCommand("query-info QUERY_ID", "Describe one reviewed dashboard query", cobra.ExactArgs(1), []operatorFlag{dashboardFlag, jsonFlag}, execute)
	prompt := operatorCommand("prompt QUERY_ID", "Render a reviewed query as an agent prompt", cobra.ExactArgs(1), []operatorFlag{dashboardFlag, databaseFlag, paramFlag}, execute)
	query := operatorCommand("query [QUERY_ID]", "Query the local canonical snapshot", cobra.MaximumNArgs(1), []operatorFlag{
		databaseFlag, dashboardFlag, paramFlag,
		textFlag("collection", "Canonical collection name"), textFlag("id", "Record or named query identifier"),
		{name: "where", description: "Canonical FIELD=VALUE predicate (repeatable)", repeat: true},
		textFlag("limit", "Maximum result count"), boolFlag("stdin", "Read a Dashboard Language query from stdin"),
	}, execute)
	query.MarkFlagsMutuallyExclusive("stdin", "collection")
	query.MarkFlagsMutuallyExclusive("stdin", "id")
	query.MarkFlagsMutuallyExclusive("stdin", "where")
	query.MarkFlagsMutuallyExclusive("stdin", "limit")
	mcp := operatorCommand("mcp", "Serve read-only local CAO queries over MCP", cobra.NoArgs, []operatorFlag{
		databaseFlag, dashboardFlag, textFlag("host", "Bind host (default 127.0.0.1)"), textFlag("port", "Bind port (default 8765)"),
	}, execute)
	upgrade := operatorCommand("upgrade-gh-aw VERSION", "Upgrade gh-aw and its policy pin to an exact release", cobra.ExactArgs(1), nil, execute)
	gh := &cobra.Command{
		Use: "gh", Short: "Inspect workflow runs and safe-output issues or pull requests", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("gh requires runs, issues, or prs")
		},
	}
	ghFlags := []operatorFlag{
		databaseFlag,
		{name: "repo", short: "R", description: "Exact execution or target repository OWNER/REPO"},
		{name: "workflow", short: "w", description: "Producing workflow name, path, file, or ID"},
		{name: "limit", short: "L", description: "Maximum results, newest first (default 30)"},
		textFlag("since", "Include results at or after ISO 8601 time"), textFlag("until", "Include results at or before ISO 8601 time"),
	}
	gh.AddCommand(
		operatorCommand("runs", "List workflow runs", cobra.NoArgs, appendFlags(ghFlags, operatorFlag{name: "status", short: "s", description: "Run status or conclusion"}), execute),
		operatorCommand("issues", "List safe-output issues", cobra.NoArgs, ghFlags, execute),
		operatorCommand("prs", "List safe-output pull requests", cobra.NoArgs, ghFlags, execute),
	)
	computation := &cobra.Command{
		Use: "computation", Short: "Compute advisory intelligence from local evidence", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("computation requires intelligence or runtime-health")
		},
	}
	computationFlags := []operatorFlag{databaseFlag, textFlag("inventory", "Campaign inventory JSON"), textFlag("campaign", "Limit evidence to a campaign slug")}
	intelligence := operatorCommand("intelligence", "Compute portfolio intelligence and advisory decisions", cobra.NoArgs, appendFlags(computationFlags,
		textFlag("previous", "Previous intelligence result"), textFlag("feedback", "Versioned decision feedback"),
	), execute)
	health := operatorCommand("runtime-health", "Compute runtime health from canonical evidence", cobra.NoArgs, appendFlags(computationFlags,
		boolFlag("diagnose", "Diagnose the selected campaign"),
	), execute)
	health.PreRunE = func(cmd *cobra.Command, _ []string) error {
		diagnose, _ := cmd.Flags().GetBool("diagnose")
		campaign, _ := cmd.Flags().GetString("campaign")
		if diagnose && campaign == "" {
			return fmt.Errorf("--diagnose requires --campaign SLUG")
		}
		return nil
	}
	computation.AddCommand(intelligence, health)
	value := &cobra.Command{
		Use: "value", Short: "Publish evidence or manage reviewed recurring Value collection", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("value requires publish, enable, or disable")
		},
	}
	publish := operatorCommand("publish OWNER/REPO WORKFLOW", "Publish already measured evidence to a CAO server", cobra.ExactArgs(2), []operatorFlag{
		textFlag("server", "CAO server base URL (HTTPS except loopback)"),
		textFlag("input", "Prepared schema-v2 Operational Value JSONL envelope (maximum 8 MiB)"),
	}, execute)
	publish.Long = `Publish one already measured Operational Value evidence envelope unchanged.

This opt-in command does not evaluate, acquire, discover, or schedule evidence.
Prepare the measurement separately after defining its contract with the skill.
WORKFLOW is the exact lowercase workflow filename stem, not a path.

Hosted publication requires an administrator OAuth session with server step-up:
set CAO_SERVER_SESSION to the cao_session cookie value and CAO_SERVER_CSRF to
its CSRF token. CAO_SERVER_TOKEN bearer authentication is allowed only for a
local numeric loopback or localhost server. Never pass credentials as arguments.
Success is JSON; stale or conflicting captures fail without retrying.`
	_ = publish.MarkFlagRequired("server")
	_ = publish.MarkFlagRequired("input")
	enable := operatorCommand("enable OWNER/REPO WORKFLOW", "Stage an exact recurring Value collector opt-in in local policy", cobra.ExactArgs(2), []operatorFlag{
		textFlag("collector", "Existing reviewed repository-relative .mjs collector (no symlinks)"),
		textFlag("api-requests", "Reviewed maximum uncached GitHub API requests per remote collection (1-1000)"),
		boolFlag("if-absent", "Register only when absent; preserve existing registrations and explicit disables"),
	}, execute)
	enable.Long = "Stage local reviewed policy only. The repository must already be explicitly enrolled in Activity. Review and commit the collector and cao.json, then deploy/push to enable scheduled collection. Never pushes, enrolls repositories, activates target workflows, or uploads executable code."
	_ = enable.MarkFlagRequired("collector")
	disable := operatorCommand("disable OWNER/REPO WORKFLOW", "Stage local revocation of recurring Value collection; retain history", cobra.ExactArgs(2), nil, execute)
	disable.Long = "Persist an explicit disable for the exact workflow without deleting evidence. Automatic skill registration respects it. Review, commit, and deploy/push cao.json before scheduled collection stops."
	value.AddCommand(publish, enable, disable)
	root.AddCommand(setupCommand(execute, github), download, pages, queries, info, prompt, query, mcp, upgrade, gh, computation,
		value,
		passthroughCommand("update [REF] [GH_AW_OPTIONS...]", "Update installed CAO packages without widening rollout", execute),
	)
}

func appendFlags(base []operatorFlag, extra ...operatorFlag) []operatorFlag {
	return append(append([]operatorFlag(nil), base...), extra...)
}

func operatorCommand(use, short string, args cobra.PositionalArgs, flags []operatorFlag, execute runtimeExecutor) *cobra.Command {
	cmd := &cobra.Command{Use: use, Short: short, Args: args}
	for _, flag := range flags {
		if flag.boolean {
			cmd.Flags().BoolP(flag.name, flag.short, false, flag.description)
		} else if flag.repeat {
			cmd.Flags().StringArrayP(flag.name, flag.short, nil, flag.description)
		} else {
			cmd.Flags().StringP(flag.name, flag.short, "", flag.description)
			value := cmd.Flags().Lookup(flag.name)
			value.Value = &singleValue{Value: value.Value, name: flag.name}
		}
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		arguments := commandWords(cmd)
		arguments = append(arguments, args...)
		// Send explicit values only; each native handler owns its defaults.
		cmd.Flags().Visit(func(flag *pflag.Flag) {
			if flag.Name == "help" {
				return
			}
			if flag.Value.Type() == "bool" {
				if flag.Value.String() == "true" {
					arguments = append(arguments, "--"+flag.Name)
				}
			} else if flag.Value.Type() == "stringArray" {
				values, _ := cmd.Flags().GetStringArray(flag.Name)
				for _, value := range values {
					arguments = append(arguments, "--"+flag.Name, value)
				}
			} else {
				arguments = append(arguments, "--"+flag.Name, flag.Value.String())
			}
		})
		return execute(cmd, arguments)
	}
	return cmd
}

// gh-aw update options evolve independently; retain their exact order and values.
func passthroughCommand(use, short string, execute runtimeExecutor) *cobra.Command {
	cmd := &cobra.Command{Use: use, Short: short, DisableFlagParsing: true}
	cmd.Long = short + ".\n\nOptions are passed unchanged to gh-aw after native CAO validation.\nUse --campaign SLUG with update."
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		for _, arg := range args {
			if arg == "--" {
				break
			}
			if arg == "--help" || arg == "-h" {
				return cmd.Help()
			}
		}
		return execute(cmd, append(commandWords(cmd), args...))
	}
	return cmd
}

func commandWords(cmd *cobra.Command) []string {
	var words []string
	for current := cmd; current.Parent() != nil; current = current.Parent() {
		words = append([]string{current.Name()}, words...)
	}
	return words
}

type singleValue struct {
	pflag.Value
	name string
	set  bool
}

func (v *singleValue) Set(value string) error {
	if v.set {
		return fmt.Errorf("option --%s may only be specified once", v.name)
	}
	v.set = true
	return v.Value.Set(value)
}
