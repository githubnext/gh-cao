package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func nativeOperator(gh ghExecutor) runtimeExecutor {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("an operator command is required")
		}
		switch args[0] {
		case "setup":
			return nativeSetup(cmd, gh)
		case "setup-auth":
			return nativeSetupAuth(cmd, gh)
		case "download":
			return nativeDownload(cmd, gh)
		case "upgrade-gh-aw":
			return nativeUpgrade(cmd, args[1], gh)
		case "update":
			return nativeUpdate(cmd, args[1:], gh)
		case "pages", "queries", "query-info":
			return nativeCatalogCommand(cmd, cmd.Flags().Args())
		case "query":
			return nativeQuery(cmd, cmd.Flags().Args())
		case "prompt":
			return nativePrompt(cmd, args[1])
		case "gh":
			return nativeGh(cmd)
		case "computation":
			return nativeComputation(cmd)
		case "api-cost":
			return nativeAPICost(cmd)
		case "mcp":
			return nativeMCP(cmd)
		case "value":
			if cmd.Name() == "enable" || cmd.Name() == "disable" {
				return nativeValueConfigure(cmd, cmd.Flags().Args(), cmd.Name() == "enable")
			}
			return nativeValuePublish(cmd, cmd.Flags().Args())
		default:
			return fmt.Errorf("unknown native operator command: %s", args[0])
		}
	}
}
