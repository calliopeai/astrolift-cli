// Structural command groups for org/team/project/operator and
// the org-scoped + operator-scoped resources. These follow the
// same pattern as cmd/app.go: structured tree + notImplemented
// stubs that surface the gap explicitly.
//
// Wiring each subcommand body is mechanical (load API client →
// GraphQL/REST call → render); the structure here is what's
// needed for the help-text + doc surface.

package cmd

import "github.com/spf13/cobra"

// ---- #8: org / team / project / operator scaffolding ----

var orgCmd = &cobra.Command{
	Use:   "org",
	Short: "Org-scoped operations (#8 + #9)",
}
var orgListCmd = &cobra.Command{
	Use: "list", Short: "List orgs you belong to",
	RunE: stub("org list"),
}
var orgShowCmd = &cobra.Command{
	Use: "show [slug]", Short: "Show org details",
	Args: cobra.MaximumNArgs(1), RunE: stub("org show"),
}

var teamCmd = &cobra.Command{
	Use:   "team",
	Short: "Team management (#8)",
}
var teamListCmd = &cobra.Command{Use: "list", Short: "List teams", RunE: stub("team list")}
var teamCreateCmd = &cobra.Command{
	Use: "create <slug>", Short: "Create a team",
	Args: cobra.ExactArgs(1), RunE: stub("team create"),
}

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Project management (#8)",
}
var projectListCmd = &cobra.Command{Use: "list", Short: "List projects", RunE: stub("project list")}
var projectCreateCmd = &cobra.Command{
	Use: "create <slug>", Short: "Create a project",
	Args: cobra.ExactArgs(1), RunE: stub("project create"),
}

// ---- #9: org-scoped resources ----

var orgSecretBundlesCmd = &cobra.Command{Use: "secret-bundles", Short: "Manage org secret bundles (#13)"}
var orgWebhooksCmd = &cobra.Command{Use: "webhooks", Short: "Manage org webhooks"}
var orgTokensCmd = &cobra.Command{Use: "tokens", Short: "Manage org service tokens"}
var orgEventsCmd = &cobra.Command{Use: "events", Short: "Org event log"}
var orgAuditCmd = &cobra.Command{Use: "audit", Short: "Org audit log"}
var orgAlertsCmd = &cobra.Command{Use: "alerts", Short: "Org alert rules (#14)"}
var orgCostCmd = &cobra.Command{Use: "cost", Short: "Org cost summary"}

// ---- #10: operator commands ----

var operatorCmd = &cobra.Command{
	Use:   "operator",
	Short: "Operator-scoped commands (cluster + provider mgmt)",
	Long: `Operator commands are admin-only. They configure the platform
itself rather than tenant apps. Includes cluster registration,
provider plugin management, federation trust handshakes (#32),
and platform observability profiles.`,
}
var operatorClusterCmd = &cobra.Command{Use: "cluster", Short: "Cluster CRUD (operator)"}
var operatorProviderCmd = &cobra.Command{Use: "provider", Short: "Provider plugin mgmt (operator)"}
var operatorFederationCmd = &cobra.Command{Use: "federation", Short: "Cross-install federation (#32)"}

// ---- #11: SCM webhook commands ----

var scmCmd = &cobra.Command{Use: "scm", Short: "Source-control webhook commands (#11)"}
var scmListCmd = &cobra.Command{Use: "list", Short: "List configured SCM webhooks", RunE: stub("scm list")}

// ---- #14: alert rule commands ----

var alertCmd = &cobra.Command{Use: "alert", Short: "Alert rule management (#14)"}
var alertListCmd = &cobra.Command{Use: "list", Short: "List alert rules", RunE: stub("alert list")}

// ---- #15: cli config + platform status + docs ----

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show platform status (#15)",
	RunE:  stub("status"),
}

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Open the platform docs in your browser (#15)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return openBrowser("https://docs.astrolift.app")
	},
}

func init() {
	orgCmd.AddCommand(orgListCmd, orgShowCmd, orgSecretBundlesCmd,
		orgWebhooksCmd, orgTokensCmd, orgEventsCmd, orgAuditCmd,
		orgAlertsCmd, orgCostCmd)
	teamCmd.AddCommand(teamListCmd, teamCreateCmd)
	projectCmd.AddCommand(projectListCmd, projectCreateCmd)
	operatorCmd.AddCommand(operatorClusterCmd, operatorProviderCmd, operatorFederationCmd)
	scmCmd.AddCommand(scmListCmd)
	alertCmd.AddCommand(alertListCmd)

	rootCmd.AddCommand(orgCmd, teamCmd, projectCmd, operatorCmd,
		scmCmd, alertCmd, statusCmd, docsCmd)
}

// stub returns a RunE that surfaces the not-implemented gap.
// Distinct from notImplemented in cmd/app.go to avoid an import
// cycle if these split into separate files later.
func stub(name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		return notImplemented(cmd, name)
	}
}
