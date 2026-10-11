package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/permissioncatalog"
	"github.com/spf13/cobra"
)

// PermsDiagnoseResult contains account information, never write authority.
type PermsDiagnoseResult struct {
	User          permissionIdentity    `json:"user"`
	Organization  orgRef                `json:"organization"`
	AccountGrants accountGrantSummary   `json:"accountGrants"`
	GrantSources  []permissionEntry     `json:"grantSources"`
	AppContext    *appPermissionSummary `json:"appContext,omitempty"`
	Diagnosis     *permissionDiagnosis  `json:"diagnosis,omitempty"`
}

type permissionEntry struct {
	Slug       string   `json:"slug"`
	Resource   string   `json:"resource"`
	Action     string   `json:"action"`
	GrantedVia []string `json:"grantedVia"`
}

type appPermissionSummary struct {
	ID             string   `json:"id"`
	Slug           string   `json:"slug"`
	Interpretation string   `json:"interpretation"`
	Permissions    []string `json:"viewerPermissions"`
}

type permissionTraceStep struct {
	Check  string `json:"check"`
	Result bool   `json:"result"`
	Detail string `json:"detail"`
}

type permissionDiagnosis struct {
	Interpretation string                `json:"interpretation"`
	UserID         string                `json:"userId"`
	Username       string                `json:"username"`
	Permission     string                `json:"permission"`
	Granted        bool                  `json:"granted"`
	IsSuperuser    bool                  `json:"isSuperuser"`
	Steps          []permissionTraceStep `json:"steps"`
}

const diagnosePermissionsQuery = `query($userId: ID!) {
  astroliftMyPermissions
  effectivePermissions(userId: $userId) { slug resource action grantedVia }
}`

const appPermissionsQuery = `query($slug: String!) {
  astroliftApp(slug: $slug) { id slug viewerPermissions }
}`

const permissionDiagnosisQuery = `query($userId: ID!, $permission: String!, $scopeType: String, $scopeId: String) {
  permissionDiagnose(userId: $userId, permission: $permission, scopeType: $scopeType, scopeId: $scopeId) {
    userId username permission granted isSuperuser steps { check result detail }
  }
}`

var permsCmd = &cobra.Command{
	Use:   "perms",
	Short: "Permission diagnostics and inspection",
	Long:  "Inspect informational account grants and permission traces in the selected organization.",
}

var permsDiagnoseCmd = &cobra.Command{
	Use:   "diagnose [app-slug]",
	Short: "Inspect account grants and optionally diagnose one permission",
	Long: `Show your account grants in the selected organization and their sources.
An optional app slug adds an informational app grant summary.

--permission <slug> requests the server's account-level diagnostic trace.
An app slug checks that app's actual GUID; otherwise --scope-type and --scope-id
may name ORG, TEAM, PROJECT, APP or AGENT. Without a target the diagnostic checks
the selected organization context.

These summaries and traces do not establish the current credential's authority,
or evaluate a mutation's environment and approval requirements. They never
approve, preflight or execute another operation. A successfully retrieved trace
exits zero even when its account-level verdict is denied.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return permissionCommandError(cmd, runPermsDiagnose(cmd, args))
	},
}

func runPermsDiagnose(cmd *cobra.Command, args []string) error {
	permission, _ := cmd.Flags().GetString("permission")
	scopeType, _ := cmd.Flags().GetString("scope-type")
	scopeID, _ := cmd.Flags().GetString("scope-id")
	permission, scopeID = strings.TrimSpace(permission), strings.TrimSpace(scopeID)
	scopeType = strings.ToUpper(strings.TrimSpace(scopeType))
	if cmd.Flags().Changed("permission") && permission == "" {
		return fmt.Errorf("--permission requires a nonempty permission slug")
	}
	if (scopeType == "") != (scopeID == "") {
		return fmt.Errorf("--scope-type and --scope-id must be supplied together")
	}
	if scopeType != "" {
		switch scopeType {
		case "ORG", "TEAM", "PROJECT", "APP", "AGENT":
		default:
			return fmt.Errorf("--scope-type must be ORG, TEAM, PROJECT, APP or AGENT")
		}
		if permission == "" {
			return fmt.Errorf("--scope-type and --scope-id require --permission")
		}
		if len(args) != 0 {
			return fmt.Errorf("pass an app slug or explicit scope flags, not both")
		}
	}
	client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	org, err := resolveOrg(cmd, cmd.Context(), client, cfg)
	if err != nil {
		return err
	}
	user, err := fetchPermissionIdentity(cmd, client)
	if err != nil {
		return err
	}
	var response struct {
		Permissions []string          `json:"astroliftMyPermissions"`
		Sources     []permissionEntry `json:"effectivePermissions"`
	}
	userID := fmt.Sprint(user.UserID)
	if err := client.GraphQL(cmd.Context(), diagnosePermissionsQuery,
		map[string]interface{}{"userId": userID}, &response); err != nil {
		return fmt.Errorf("fetching account grants: %w", err)
	}
	if response.Permissions == nil || response.Sources == nil {
		return fmt.Errorf("server returned no account grant report")
	}
	result := PermsDiagnoseResult{
		User: *user, Organization: org,
		AccountGrants: accountGrantSummary{Interpretation: accountGrantNotice, Permissions: response.Permissions},
		GrantSources:  response.Sources,
	}
	if len(args) == 1 {
		var response struct {
			App *appPermissionSummary `json:"astroliftApp"`
		}
		if err := client.GraphQL(cmd.Context(), appPermissionsQuery,
			map[string]interface{}{"slug": args[0]}, &response); err != nil {
			return fmt.Errorf("fetching app grant summary: %w", err)
		}
		if response.App == nil || response.App.ID == "" || response.App.Slug != args[0] || response.App.Permissions == nil {
			return fmt.Errorf("app %q has no visible verified grant summary in the selected organization", args[0])
		}
		response.App.Interpretation = "Account grants on this app; not the current credential's authority or an environment-specific action check."
		result.AppContext = response.App
		if permission != "" {
			scopeType, scopeID = "APP", response.App.ID
		}
	}
	if permission != "" {
		variables := map[string]interface{}{"userId": userID, "permission": permission}
		if scopeType != "" {
			variables["scopeType"], variables["scopeId"] = scopeType, scopeID
		}
		var response struct {
			Diagnosis *permissionDiagnosis `json:"permissionDiagnose"`
		}
		if err := client.GraphQL(cmd.Context(), permissionDiagnosisQuery, variables, &response); err != nil {
			return fmt.Errorf("fetching account diagnostic: %w", err)
		}
		if response.Diagnosis == nil || response.Diagnosis.UserID != userID || response.Diagnosis.Permission != permission || response.Diagnosis.Steps == nil {
			return fmt.Errorf("server returned no matching account diagnostic")
		}
		response.Diagnosis.Interpretation = diagnosisNotice
		result.Diagnosis = response.Diagnosis
	}
	// The server can reflect request text in traces. Remove the current
	// credential even from otherwise permitted informational fields.
	redactPermissionReport(client, &result)
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "User: %s (id %d)\nOrg:  %s (%s)\n", result.User.Username, result.User.UserID, result.Organization.Slug, result.Organization.ID)
	printAccountPermissions(cmd, &result.AccountGrants)
	fmt.Fprintln(cmd.OutOrStdout(), "\nGrant sources (account-level):")
	for _, source := range result.GrantSources {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s: %s\n", source.Slug, strings.Join(source.GrantedVia, ", "))
	}
	if result.AppContext != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "\nApp %s (%s): %s\n", result.AppContext.Slug, result.AppContext.ID, result.AppContext.Interpretation)
		for _, slug := range result.AppContext.Permissions {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", slug)
		}
	}
	if result.Diagnosis != nil {
		diagnosis := result.Diagnosis
		fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n%s: granted=%t\n", diagnosis.Interpretation, diagnosis.Permission, diagnosis.Granted)
		for _, step := range diagnosis.Steps {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s: %t — %s\n", step.Check, step.Result, step.Detail)
		}
	}
	return nil
}

func redactPermissionReport(client *api.Client, result *PermsDiagnoseResult) {
	result.Organization = redactPermissionOrg(client, result.Organization)
	redactStrings := func(values []string) {
		for i, value := range values {
			values[i] = client.RedactDiagnostic(value)
		}
	}
	redactStrings(result.AccountGrants.Permissions)
	for i := range result.GrantSources {
		source := &result.GrantSources[i]
		source.Slug = client.RedactDiagnostic(source.Slug)
		source.Resource = client.RedactDiagnostic(source.Resource)
		source.Action = client.RedactDiagnostic(source.Action)
		redactStrings(source.GrantedVia)
	}
	if result.AppContext != nil {
		result.AppContext.ID = client.RedactDiagnostic(result.AppContext.ID)
		result.AppContext.Slug = client.RedactDiagnostic(result.AppContext.Slug)
		redactStrings(result.AppContext.Permissions)
	}
	if result.Diagnosis != nil {
		diagnosis := result.Diagnosis
		diagnosis.Username = client.RedactDiagnostic(diagnosis.Username)
		diagnosis.Permission = client.RedactDiagnostic(diagnosis.Permission)
		for i := range diagnosis.Steps {
			step := &diagnosis.Steps[i]
			step.Check = client.RedactDiagnostic(step.Check)
			step.Detail = client.RedactDiagnostic(step.Detail)
		}
	}
}

// permsListCmd lists bundled definitions, independently of login or server.
var permsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List bundled permission definitions from a pinned backend revision",
	Long: `Print the CLI's offline permission-definition snapshot and source revision.
The selected server may expose a different catalogue. These definitions are not
account grants, token scopes or permission to act on any target. Current target,
credential, environment and policy checks remain the server's responsibility.
No server connection or authentication is performed. JSON retains permissions[]
and adds catalogueKind, source, interpretation and requiresTargetCheck metadata.`,
	Args: cobra.NoArgs,
	// This offline catalogue must not inherit the root's optional network
	// release check, even in a stamped release with a GitHub credential.
	PersistentPostRun: func(_ *cobra.Command, _ []string) {},
	RunE: func(cmd *cobra.Command, args []string) error {
		catalogue, err := permissioncatalog.Read()
		if err != nil {
			return err
		}
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, catalogue)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Bundled permission definitions (%s@%s):\n", catalogue.Source.Repository, catalogue.Source.Revision)
		fmt.Fprintln(cmd.OutOrStdout(), catalogue.Interpretation)
		for _, permission := range catalogue.Permissions {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", permission)
		}
		return nil
	},
}

func init() {
	permsDiagnoseCmd.Flags().String("permission", "", "Request an informational account diagnostic for one permission slug")
	permsDiagnoseCmd.Flags().String("scope-type", "", "Diagnostic target: ORG, TEAM, PROJECT, APP or AGENT (requires --scope-id)")
	permsDiagnoseCmd.Flags().String("scope-id", "", "Actual target GUID (requires --scope-type and --permission)")
	permsCmd.AddCommand(permsDiagnoseCmd, permsListCmd)
	rootCmd.AddCommand(permsCmd)
}

// renderJSON prints v as indented JSON to cmd's stdout.
func renderJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
