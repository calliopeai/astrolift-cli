package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// PermsDiagnoseResult is the shape returned by the myPermissions GraphQL query.
type PermsDiagnoseResult struct {
	Username    string   `json:"username"`
	Email       string   `json:"email"`
	OrgSlug     string   `json:"orgSlug"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	Missing     []string `json:"missingCommon"`
	AppContext  *struct {
		Permissions []string `json:"permissions"`
	} `json:"appContext,omitempty"`
}

// diagnosePermissionsQuery is the GraphQL query for permission diagnostics.
// The backend myPermissions resolver returns the full effective permission set
// for the calling user. Falls back gracefully if the resolver isn't available.
const diagnosePermissionsQuery = `
query DiagnosePermissions($appSlug: String) {
  myPermissions(appSlug: $appSlug) {
    username
    email
    orgSlug
    roles
    permissions
    missingCommon
    appContext {
      permissions
    }
  }
}`

// permsCmd is the top-level `astro perms` command group.
var permsCmd = &cobra.Command{
	Use:   "perms",
	Short: "Permission diagnostics and inspection",
	Long: `Inspect and diagnose permissions for the current authenticated user.

These commands help operators and developers understand what actions
they can perform on the platform and why specific operations might fail.`,
}

// permsDiagnoseCmd implements `astro perms diagnose`.
var permsDiagnoseCmd = &cobra.Command{
	Use:   "diagnose [app-slug]",
	Short: "Show effective permissions for the current user (and optionally an app)",
	Long: `Fetch and display the effective permission set for the current
authenticated user across the platform.

When an app slug is provided, also shows app-scoped permissions.

Useful for diagnosing 'permission denied' errors: run this command
to see exactly which permissions you hold, then cross-reference against
the operation that failed.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		debug, _ := cmd.Flags().GetBool("debug")
		client, _, _, err := loadActiveClient(ctx, debug)
		if err != nil {
			return err
		}

		appSlug := ""
		if len(args) == 1 {
			appSlug = args[0]
		}

		variables := map[string]interface{}{}
		if appSlug != "" {
			variables["appSlug"] = appSlug
		}

		var gqlResp struct {
			MyPermissions *PermsDiagnoseResult `json:"myPermissions"`
		}
		if err := client.GraphQL(ctx, diagnosePermissionsQuery, variables, &gqlResp); err != nil {
			return fmt.Errorf("fetching permissions: %w", err)
		}
		if gqlResp.MyPermissions == nil {
			return fmt.Errorf("myPermissions query returned no data — is this platform version supported?")
		}
		result := gqlResp.MyPermissions

		asJSON, _ := cmd.Flags().GetBool("json")
		if asJSON {
			return renderJSON(cmd, result)
		}

		// Human-readable output
		fmt.Fprintf(cmd.OutOrStdout(), "User:   %s\n", result.Username)
		fmt.Fprintf(cmd.OutOrStdout(), "Email:  %s\n", result.Email)
		fmt.Fprintf(cmd.OutOrStdout(), "Org:    %s\n", result.OrgSlug)
		fmt.Fprintln(cmd.OutOrStdout())

		if len(result.Roles) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Roles:")
			for _, r := range result.Roles {
				fmt.Fprintf(cmd.OutOrStdout(), "  • %s\n", r)
			}
			fmt.Fprintln(cmd.OutOrStdout())
		}

		if len(result.Permissions) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Effective permissions:")
			for _, p := range result.Permissions {
				fmt.Fprintf(cmd.OutOrStdout(), "  ✓ %s\n", p)
			}
			fmt.Fprintln(cmd.OutOrStdout())
		}

		if appSlug != "" && result.AppContext != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "App-scoped (%s):\n", appSlug)
			if len(result.AppContext.Permissions) > 0 {
				for _, p := range result.AppContext.Permissions {
					fmt.Fprintf(cmd.OutOrStdout(), "  ✓ %s\n", p)
				}
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "  (no app-scoped permissions — are you a member?)")
			}
			fmt.Fprintln(cmd.OutOrStdout())
		}

		if len(result.Missing) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Common permissions you do NOT hold:")
			for _, m := range result.Missing {
				fmt.Fprintf(cmd.OutOrStdout(), "  ✗ %s\n", m)
			}
			fmt.Fprintln(cmd.OutOrStdout())
			fmt.Fprintf(cmd.OutOrStdout(),
				"Hint: ask an org admin to grant you the missing permissions.\n"+
					"Run `astro perms diagnose --json` for the full machine-readable report.\n")
		}

		return nil
	},
}

// permsListCmd implements `astro perms list` — raw list of all platform permissions.
var permsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all platform permission strings",
	Long: `Print every permission string defined by the platform.
Useful when writing custom roles or debugging access control.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// All known permission strings — mirrors core.permissions.Permission in the backend.
		// Keep this in sync with backend/core/permissions.py.
		permissions := []string{
			// Org-level
			"org.read", "org.update", "org.delete",
			"org.member.invite", "org.member.remove", "org.member.update_role",
			"org.team.create", "org.team.delete",
			"org.project.create",
			// App-level
			"app.read", "app.create", "app.update", "app.delete",
			"app.deploy", "app.rollback", "app.promote",
			"app.approve_deploy",
			"app.read_logs", "app.read_metrics",
			"app.secret.read", "app.secret.write",
			"app.exec",
			// Cluster-level
			"cluster.register", "cluster.update", "cluster.delete",
			"cluster.observe",
			// Billing
			"billing.read", "billing.update",
			// Audit
			"audit_log.read", "audit_log.export",
		}

		asJSON, _ := cmd.Flags().GetBool("json")
		if asJSON {
			return renderJSON(cmd, map[string]any{"permissions": permissions})
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Platform permissions:")
		for _, p := range permissions {
			parts := strings.SplitN(p, ".", 2)
			if len(parts) == 2 {
				fmt.Fprintf(cmd.OutOrStdout(), "  %-40s  (%s scope)\n", p, parts[0])
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", p)
			}
		}
		return nil
	},
}

func init() {
	permsCmd.AddCommand(permsDiagnoseCmd, permsListCmd)
	rootCmd.AddCommand(permsCmd)
}

// renderJSON prints v as indented JSON to cmd's stdout.
func renderJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
