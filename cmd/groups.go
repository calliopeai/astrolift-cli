// Structural command groups for org/team/project/operator and
// the org-scoped + operator-scoped resources. These follow the
// same pattern as cmd/app.go: structured tree + GraphQL-wired
// resolvers (cmd/app_lifecycle.go is the template).
//
// The read-mostly resources (org/team/project/scm/alert/status)
// resolve through the same /app/gql/config/ surface every other
// command uses; the server gates tenant scope and permissions.
// Each command's RunE is a thin wrapper that loads the active
// client, then delegates to a testable inner func taking a client.

package cmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// ---- #8: org / team / project / operator scaffolding ----

var orgCmd = &cobra.Command{
	Use:   "org",
	Short: "Org-scoped operations (#8 + #9)",
}
var orgListCmd = &cobra.Command{
	Use: "list", Aliases: []string{"ls"}, Short: "List orgs you belong to",
	RunE: runOrgList,
}
var orgShowCmd = &cobra.Command{
	Use: "show [slug]", Short: "Show org details (defaults to the working org)",
	Args: cobra.MaximumNArgs(1), RunE: runOrgShow,
}

var teamCmd = &cobra.Command{
	Use:   "team",
	Short: "Team management (#8)",
}
var teamListCmd = &cobra.Command{
	Use: "list", Aliases: []string{"ls"}, Short: "List teams",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runTeamList(cmd, cmd.Context(), client)
	},
}
var teamCreateCmd = &cobra.Command{
	Use: "create <slug>", Short: "Create a team in the working org",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runTeamCreate(cmd, cmd.Context(), client, cfg, args[0])
	},
}

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Project management (#8)",
}
var projectListCmd = &cobra.Command{
	Use: "list", Aliases: []string{"ls"}, Short: "List projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runProjectList(cmd, cmd.Context(), client)
	},
}
var projectCreateCmd = &cobra.Command{
	Use: "create <slug>", Short: "Create a project under a team (--team <slug>)",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runProjectCreate(cmd, cmd.Context(), client, args[0])
	},
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
var scmListCmd = &cobra.Command{
	Use: "list", Aliases: []string{"ls"}, Short: "List configured SCM connections",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runScmList(cmd, cmd.Context(), client)
	},
}
var scmDisconnectCmd = &cobra.Command{
	Use: "disconnect <connection-id>", Aliases: []string{"rm", "remove"},
	Short: "Disconnect (remove) an SCM connection by id (from `scm list`)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runScmDisconnect(cmd, cmd.Context(), client, args[0])
	},
}

// ---- #14: alert rule commands ----

var alertListAll bool

var alertCmd = &cobra.Command{Use: "alert", Short: "Alert rule management (#14)"}
var alertListCmd = &cobra.Command{
	Use: "list", Aliases: []string{"ls"}, Short: "List alert rules",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAlertList(cmd, cmd.Context(), client)
	},
}

// ---- #15: cli config + platform status + docs ----

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show platform status (#15)",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, entry, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		apiURL := ""
		if entry != nil {
			apiURL = entry.APIURL
		}
		return runStatus(cmd, cmd.Context(), client, apiURL)
	},
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
	teamCreateCmd.Flags().StringVar(&teamCreateName, "name", "", "Display name (defaults to the slug)")
	teamCreateCmd.Flags().StringVar(&teamCreateDesc, "description", "", "Optional description")
	projectCmd.AddCommand(projectListCmd, projectCreateCmd)
	projectCreateCmd.Flags().StringVar(&projectCreateName, "name", "", "Display name (defaults to the slug)")
	projectCreateCmd.Flags().StringVar(&projectCreateDesc, "description", "", "Optional description")
	operatorCmd.AddCommand(operatorClusterCmd, operatorProviderCmd, operatorFederationCmd)
	scmCmd.AddCommand(scmListCmd, scmDisconnectCmd)
	alertCmd.AddCommand(alertListCmd)
	alertListCmd.Flags().BoolVar(&alertListAll, "all", false, "Include inactive rules")

	rootCmd.AddCommand(orgCmd, teamCmd, projectCmd, operatorCmd,
		scmCmd, alertCmd, statusCmd, docsCmd)
}

// ---- team list / create (astroliftTeams / createTeam) ----------------------

type teamRef struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

func listTeams(ctx context.Context, client *api.Client) ([]teamRef, error) {
	var resp struct {
		Teams []teamRef `json:"astroliftTeams"`
	}
	if err := client.GraphQL(ctx, `query { astroliftTeams { id slug name } }`, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Teams, nil
}

func runTeamList(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	teams, err := listTeams(ctx, client)
	if err != nil {
		return fmt.Errorf("listing teams: %w", err)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, teams)
	}
	if len(teams) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No teams found.")
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tNAME\tID")
	for _, t := range teams {
		fmt.Fprintf(w, "%s\t%s\t%s\n", t.Slug, t.Name, t.ID)
	}
	return w.Flush()
}

var (
	teamCreateName string
	teamCreateDesc string
)

func runTeamCreate(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, slug string) error {
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	name := teamCreateName
	if strings.TrimSpace(name) == "" {
		name = slug
	}
	input := map[string]interface{}{
		"organizationId": org.ID,
		"name":           name,
		"slug":           slug,
	}
	if teamCreateDesc != "" {
		input["description"] = teamCreateDesc
	}

	mutation := `mutation($input: CreateTeamInput!) {
  createTeam(input: $input) {
    ok
    errors { code message field }
    data { id slug name }
  }
}`
	var resp struct {
		Result struct {
			Ok     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *teamRef        `json:"data"`
		} `json:"createTeam"`
	}
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("creating team: %w", err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		return fmt.Errorf("create team failed: %s", firstDeployError(resp.Result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Result.Data)
	}
	t := resp.Result.Data
	fmt.Fprintf(cmd.OutOrStdout(), "Team created: %s (%s) in org %s\n", t.Slug, t.Name, org.Slug)
	return nil
}

// ---- project list / create (astroliftProjects / createProject) -------------

type projectRef struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Team struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"team"`
}

func runProjectList(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	var resp struct {
		Projects []projectRef `json:"astroliftProjects"`
	}
	query := `query { astroliftProjects { id slug name team { slug name } } }`
	if err := client.GraphQL(ctx, query, nil, &resp); err != nil {
		return fmt.Errorf("listing projects: %w", err)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Projects)
	}
	if len(resp.Projects) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No projects found.")
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tNAME\tTEAM\tID")
	for _, p := range resp.Projects {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Slug, p.Name, dashIfEmpty(p.Team.Slug), p.ID)
	}
	return w.Flush()
}

var (
	projectCreateName string
	projectCreateDesc string
)

func runProjectCreate(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	teamSlug, _ := cmd.Flags().GetString("team")
	teamSlug = strings.TrimSpace(teamSlug)
	if teamSlug == "" {
		return fmt.Errorf("--team <slug> is required (the team to create the project under; see `astro team list`)")
	}
	teams, err := listTeams(ctx, client)
	if err != nil {
		return fmt.Errorf("resolving team: %w", err)
	}
	teamID := ""
	for _, t := range teams {
		if t.Slug == teamSlug || t.ID == teamSlug {
			teamID = t.ID
			break
		}
	}
	if teamID == "" {
		return fmt.Errorf("team %q not found (see `astro team list`)", teamSlug)
	}

	name := projectCreateName
	if strings.TrimSpace(name) == "" {
		name = slug
	}
	input := map[string]interface{}{
		"teamId": teamID,
		"name":   name,
		"slug":   slug,
	}
	if projectCreateDesc != "" {
		input["description"] = projectCreateDesc
	}

	mutation := `mutation($input: CreateProjectInput!) {
  createProject(input: $input) {
    ok
    errors { code message field }
    data { id slug name team { slug name } }
  }
}`
	var resp struct {
		Result struct {
			Ok     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *projectRef     `json:"data"`
		} `json:"createProject"`
	}
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("creating project: %w", err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		return fmt.Errorf("create project failed: %s", firstDeployError(resp.Result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Result.Data)
	}
	p := resp.Result.Data
	fmt.Fprintf(cmd.OutOrStdout(), "Project created: %s (%s) in team %s\n", p.Slug, p.Name, teamSlug)
	return nil
}

// ---- scm list (astroliftSourceConnections) ---------------------------------

func runScmList(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	type scmConn struct {
		ID           string `json:"id"`
		Kind         string `json:"kind"`
		Name         string `json:"name"`
		DisplayName  string `json:"displayName"`
		AccountLogin string `json:"accountLogin"`
		IsActive     bool   `json:"isActive"`
		IsPersonal   bool   `json:"isPersonal"`
	}
	var resp struct {
		Connections []scmConn `json:"astroliftSourceConnections"`
	}
	query := `query { astroliftSourceConnections { id kind name displayName accountLogin isActive isPersonal } }`
	if err := client.GraphQL(ctx, query, nil, &resp); err != nil {
		return fmt.Errorf("listing SCM connections: %w", err)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Connections)
	}
	if len(resp.Connections) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No SCM connections configured.")
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KIND\tACCOUNT\tNAME\tSCOPE\tACTIVE\tID")
	for _, c := range resp.Connections {
		scope := "org"
		if c.IsPersonal {
			scope = "personal"
		}
		name := c.DisplayName
		if name == "" {
			name = c.Name
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			c.Kind, dashIfEmpty(c.AccountLogin), dashIfEmpty(name), scope, yesNo(c.IsActive), c.ID)
	}
	return w.Flush()
}

// ---- scm disconnect (disconnectSource) -------------------------------------

func runScmDisconnect(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
	mutation := `mutation($input: DisconnectSourceInput!) {
  disconnectSource(input: $input) {
    ok
    errors { code message field }
    data { id }
  }
}`
	var resp struct {
		Result struct {
			Ok     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"disconnectSource"`
	}
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": map[string]interface{}{"id": id}}, &resp); err != nil {
		return fmt.Errorf("disconnecting SCM connection: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("disconnect failed: %s", firstDeployError(resp.Result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Result.Data)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Disconnected SCM connection %s\n", id)
	return nil
}

// ---- alert list (astroliftAlertRules) --------------------------------------

func runAlertList(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	type alertRule struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Target     string `json:"target"`
		TargetID   string `json:"targetId"`
		Severity   string `json:"severity"`
		IsActive   bool   `json:"isActive"`
		ActiveMute *struct {
			TTLUntil string `json:"ttlUntil"`
		} `json:"activeMute"`
	}
	var resp struct {
		Rules []alertRule `json:"astroliftAlertRules"`
	}
	query := `query($activeOnly: Boolean!) {
  astroliftAlertRules(activeOnly: $activeOnly) {
    id name target targetId severity isActive activeMute { ttlUntil }
  }
}`
	vars := map[string]interface{}{"activeOnly": !alertListAll}
	if err := client.GraphQL(ctx, query, vars, &resp); err != nil {
		return fmt.Errorf("listing alert rules: %w", err)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Rules)
	}
	if len(resp.Rules) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No alert rules found.")
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSEVERITY\tTARGET\tSTATE\tID")
	for _, r := range resp.Rules {
		state := "active"
		if !r.IsActive {
			state = "inactive"
		}
		if r.ActiveMute != nil {
			state += " (muted)"
		}
		target := r.Target
		if r.TargetID != "" {
			target = fmt.Sprintf("%s:%s", r.Target, r.TargetID)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Severity, target, state, r.ID)
	}
	return w.Flush()
}

// ---- status (astroliftServerInfo) ------------------------------------------

func runStatus(cmd *cobra.Command, ctx context.Context, client *api.Client, apiURL string) error {
	type serverInfo struct {
		Version      string   `json:"version"`
		APIVersion   string   `json:"apiVersion"`
		InstallID    string   `json:"installId"`
		InstallSlug  string   `json:"installSlug"`
		InstallLabel *string  `json:"installLabel"`
		Region       *string  `json:"region"`
		ServerTime   string   `json:"serverTime"`
		Capabilities []string `json:"capabilities"`
		AuthMethods  []string `json:"authMethods"`
	}
	var resp struct {
		Info *serverInfo `json:"astroliftServerInfo"`
	}
	query := `query {
  astroliftServerInfo {
    version apiVersion installId installSlug installLabel region serverTime capabilities authMethods
  }
}`
	if err := client.GraphQL(ctx, query, nil, &resp); err != nil {
		return fmt.Errorf("fetching platform status: %w", err)
	}
	if resp.Info == nil {
		return fmt.Errorf("platform returned no server info")
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Info)
	}
	info := resp.Info
	out := cmd.OutOrStdout()
	label := info.InstallSlug
	if info.InstallLabel != nil && *info.InstallLabel != "" {
		label = fmt.Sprintf("%s (%s)", *info.InstallLabel, info.InstallSlug)
	}
	if apiURL != "" {
		fmt.Fprintf(out, "API:          %s\n", apiURL)
	}
	fmt.Fprintf(out, "Install:      %s\n", label)
	if info.Region != nil && *info.Region != "" {
		fmt.Fprintf(out, "Region:       %s\n", *info.Region)
	}
	fmt.Fprintf(out, "Version:      %s (api %s)\n", info.Version, info.APIVersion)
	fmt.Fprintf(out, "Server time:  %s\n", info.ServerTime)
	if len(info.AuthMethods) > 0 {
		fmt.Fprintf(out, "Auth:         %s\n", strings.Join(info.AuthMethods, ", "))
	}
	if len(info.Capabilities) > 0 {
		fmt.Fprintf(out, "Capabilities: %s\n", strings.Join(info.Capabilities, ", "))
	}
	return nil
}
