package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "App lifecycle and sub-resource management",
	Long: `Commands for working with Astrolift apps: init, register, deploy,
rollback, promote, plus sub-resource management (secrets, services,
domains, tokens, members, jobs, events, audit).`,
}

// ---- lifecycle ----

var appInitCmd = &cobra.Command{
	Use:   "init [path]",
	Short: "Scaffold a new astrolift.toml in the current directory",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "astrolift.toml"
		if len(args) == 1 {
			path = args[0]
		}
		return scaffoldManifest(path, cmd)
	},
}

var appRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register the local astrolift.toml as a new app on the platform",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, _, err := loadActiveClient(cmd.Context(), false)
		if err != nil {
			return err
		}
		return notImplemented(cmd, "app register")
	},
}

var appDeployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Trigger a deploy for the current app",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, _, err := loadActiveClient(cmd.Context(), false)
		if err != nil {
			return err
		}
		return notImplemented(cmd, "app deploy")
	},
}

var appRollbackCmd = &cobra.Command{
	Use:   "rollback [deployment-id]",
	Short: "Roll back to a previous deployment",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Backend policy: backend/astrolift_lifecycle/rollback.py
		// (resolve_target with explicit-target-id override OR
		// last running deployment).
		return notImplemented(cmd, "app rollback")
	},
}

var appPromoteCmd = &cobra.Command{
	Use:   "promote",
	Short: "Promote a deployment from one environment to another",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Backend policy: spec 14 §15 + portability_surfacing.py
		// (#65) for promote-check.
		return notImplemented(cmd, "app promote")
	},
}

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List apps in the current org / project",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, _, err := loadActiveClient(cmd.Context(), false)
		if err != nil {
			return err
		}
		return notImplemented(cmd, "app list")
	},
}

var appShowCmd = &cobra.Command{
	Use:   "show [app-slug]",
	Short: "Show app details",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return notImplemented(cmd, "app show")
	},
}

// ---- sub-resources ----
//
// Each sub-resource gets its own subcommand group. Bodies follow
// the pattern: load creds → call GraphQL → render.

func newSubResourceCmd(name, summary string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: summary,
		Long: fmt.Sprintf(`%s.

Subcommands typically include: list, show, create, update, delete.`, summary),
	}
}

var appSecretsCmd = newSubResourceCmd("secrets", "Manage app secrets (#5)")
var appServicesCmd = newSubResourceCmd("services", "Manage bound managed services")
var appDomainsCmd = newSubResourceCmd("domains", "Manage custom domains")
var appTokensCmd = newSubResourceCmd("tokens", "Manage deploy tokens")
var appMembersCmd = newSubResourceCmd("members", "Manage app team members")
var appJobsCmd = newSubResourceCmd("jobs", "Manage scheduled jobs")
var appEventsCmd = newSubResourceCmd("events", "Show app event log (#12)")
var appAuditCmd = newSubResourceCmd("audit", "Show app audit log (#12)")
var appLogsCmd = &cobra.Command{
	Use:   "logs [workload]",
	Short: "Stream logs from one or more workloads (#6)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Calls the streaming endpoint via api.Client.Stream;
		// reads SSE lines, prints to stdout.
		return notImplemented(cmd, "app logs")
	},
}
var appExecCmd = &cobra.Command{
	Use:   "exec [workload] -- <command...>",
	Short: "Run a one-shot command in a workload's pod (#6)",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Backend policy: command_run.py (#142) +
		// command_run_output.py (#275). Exec uses the
		// CommandRunWorkflow synchronously then prints output.
		return notImplemented(cmd, "app exec")
	},
}

var appPreviewsCmd = newSubResourceCmd("previews", "Manage preview environments (#95)")

func init() {
	appCmd.AddCommand(
		appInitCmd, appRegisterCmd, appDeployCmd,
		appRollbackCmd, appPromoteCmd,
		appListCmd, appShowCmd,
		appSecretsCmd, appServicesCmd, appDomainsCmd,
		appTokensCmd, appMembersCmd, appJobsCmd,
		appEventsCmd, appAuditCmd,
		appLogsCmd, appExecCmd,
		appPreviewsCmd,
	)
	rootCmd.AddCommand(appCmd)
}

// notImplemented surfaces the gap rather than silently no-op'ing.
// The command tree is structured so adding the body is a focused
// change (see the GraphQL schema in astrolift-app for the queries).
func notImplemented(cmd *cobra.Command, name string) error {
	return fmt.Errorf(
		"`astro %s` is structured but not yet wired to the API; "+
			"see astrolift-cli/cmd for the command tree",
		name,
	)
}
