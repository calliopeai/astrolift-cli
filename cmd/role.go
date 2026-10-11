// Package cmd -- `astro role ...`: roles and who holds them
// (calliopeai/astrolift-app#1865).
//
// Roles are the platform's permission bundles; a binding gives one role to a
// member or an IdP group at an org, team, project or app scope. Teams are the
// local groups, so `astro team` already covers group membership. The server
// owns every check (manage-members permission, the caller's own org, the
// target being a member); these commands resolve slugs and usernames to the
// ids the API takes and print what it returns.
package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

var (
	roleListSystem      bool
	roleListCustom      bool
	roleSearch          string
	roleName            string
	roleScopeLevel      string
	roleDescription     string
	rolePermissions     []string
	roleFrom            string
	roleBindingsRole    string
	roleBindingsHolder  string
	roleGrantUser       string
	roleGrantGroup      string
	roleGrantTeam       string
	roleGrantProject    string
	roleGrantExpires    string
	roleRevokeConfirmed bool
)

var guidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ---- GraphQL ----------------------------------------------------------------

const rolesPageQuery = `query($search: String, $filter: AstroliftRolesListFilter) {
  astroliftRolesPage(search: $search, limit: 200, filter: $filter) {
    items { id slug name description scopeLevel permissions isSystem bindingsCount }
    nextCursor
  }
}`

const roleBindingsPageQuery = `query($filter: AstroliftRoleBindingsListFilter) {
  astroliftRoleBindingsPage(limit: 200, filter: $filter) {
    items {
      id groupExternalId scopeKind scopeId sourceScopeLabel grantedAt expiresAt
      user { id username }
      role { slug name }
    }
    nextCursor
  }
}`

const createRoleMutation = `mutation($input: CreateRoleInput!) {
  createRole(input: $input) { ok errors { code message field } data { id slug name scopeLevel permissions isSystem } }
}`

const updateRoleMutation = `mutation($input: UpdateRoleInput!) {
  updateRole(input: $input) { ok errors { code message field } data { id slug name scopeLevel permissions isSystem } }
}`

const grantRoleMutation = `mutation($input: GrantRoleInput!) {
  grantRole(input: $input) {
    ok errors { code message field }
    data { id groupExternalId scopeKind scopeId sourceScopeLabel grantedAt expiresAt user { id username } role { slug name } }
  }
}`

const revokeRoleBindingMutation = `mutation($input: RevokeRoleBindingInput!) {
  revokeRoleBinding(input: $input) { ok errors { code message field } }
}`

const roleMembersQuery = `query($search: String) {
  astroliftMembersPage(search: $search, limit: 50) { items { user { id username } } }
}`

const roleAppIDQuery = `query($slug: String!) { astroliftApp(slug: $slug) { id slug } }`

// ---- shapes -------------------------------------------------------------------

type roleRow struct {
	ID            string   `json:"id"`
	Slug          string   `json:"slug"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	ScopeLevel    string   `json:"scopeLevel"`
	Permissions   []string `json:"permissions"`
	IsSystem      bool     `json:"isSystem"`
	BindingsCount *int     `json:"bindingsCount,omitempty"`
}

type roleBindingRow struct {
	ID               string  `json:"id"`
	GroupExternalID  string  `json:"groupExternalId"`
	ScopeKind        string  `json:"scopeKind"`
	ScopeID          string  `json:"scopeId"`
	SourceScopeLabel string  `json:"sourceScopeLabel"`
	GrantedAt        string  `json:"grantedAt"`
	ExpiresAt        *string `json:"expiresAt"`
	User             *struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"user"`
	Role struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"role"`
}

func (b roleBindingRow) holder() string {
	if b.User != nil {
		return b.User.Username
	}
	return "group:" + b.GroupExternalID
}

// ---- commands -------------------------------------------------------------------

var roleCmd = &cobra.Command{
	Use:   "role",
	Short: "Roles and role bindings (who can do what, where)",
	Long: `Roles bundle permissions; a binding gives a role to a member or an IdP group
at the organization, or at one team, project or app.

  astro role list --custom
  astro role show deploy-operator
  astro role create release-manager --scope-level PROJECT \
      --permission app.deploy --permission app.read_logs
  astro role grant release-manager --user ana --project checkout
  astro role bindings --holder ana
  astro role revoke <binding-id> --yes

Teams are the local groups: manage their members with 'astro team'.`,
}

var roleListCmd = &cobra.Command{
	Use: "list", Aliases: []string{"ls"}, Short: "List roles (system and this organization's custom roles)",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withRoleClient(cmd, func(ctx context.Context, client *api.Client, org orgRef) error {
			return runRoleList(cmd, ctx, client)
		})
	},
}

var roleShowCmd = &cobra.Command{
	Use: "show <role>", Short: "Show one role and its permissions", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withRoleClient(cmd, func(ctx context.Context, client *api.Client, org orgRef) error {
			return runRoleShow(cmd, ctx, client, args[0])
		})
	},
}

var roleCreateCmd = &cobra.Command{
	Use: "create <slug>", Short: "Create a custom role", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withRoleClient(cmd, func(ctx context.Context, client *api.Client, org orgRef) error {
			return runRoleCreate(cmd, ctx, client, args[0])
		})
	},
}

var roleUpdateCmd = &cobra.Command{
	Use: "update <role>", Short: "Rename, redescribe or replace the permissions of a custom role", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withRoleClient(cmd, func(ctx context.Context, client *api.Client, org orgRef) error {
			return runRoleUpdate(cmd, ctx, client, args[0])
		})
	},
}

var roleBindingsCmd = &cobra.Command{
	Use: "bindings", Short: "List role bindings", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withRoleClient(cmd, func(ctx context.Context, client *api.Client, org orgRef) error {
			return runRoleBindings(cmd, ctx, client)
		})
	},
}

var roleGrantCmd = &cobra.Command{
	Use:   "grant <role>",
	Short: "Give a role to a member or an IdP group",
	Long: `Binds <role> to exactly one of --user <username> or --group <external id>.
The scope is the working organization unless one of --team, --project or --app
(slug or id) names a narrower one. --expires takes an RFC 3339 time.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withRoleClient(cmd, func(ctx context.Context, client *api.Client, org orgRef) error {
			return runRoleGrant(cmd, ctx, client, org, args[0])
		})
	},
}

var roleRevokeCmd = &cobra.Command{
	Use: "revoke <binding-id>", Short: "Remove a role binding (needs --yes)", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withRoleClient(cmd, func(ctx context.Context, client *api.Client, org orgRef) error {
			return runRoleRevoke(cmd, ctx, client, args[0])
		})
	},
}

// withRoleClient loads the active client bound to the working organization.
func withRoleClient(cmd *cobra.Command, run func(context.Context, *api.Client, orgRef) error) error {
	client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	org, err := resolveOrg(cmd, cmd.Context(), client, cfg)
	if err != nil {
		return err
	}
	return run(cmd.Context(), client, org)
}

// ---- implementations ---------------------------------------------------------------

func listRoles(ctx context.Context, client *api.Client, search string, filter map[string]interface{}) ([]roleRow, error) {
	vars := map[string]interface{}{}
	if search != "" {
		vars["search"] = search
	}
	if len(filter) > 0 {
		vars["filter"] = filter
	}
	var resp struct {
		Page struct {
			Items []roleRow `json:"items"`
		} `json:"astroliftRolesPage"`
	}
	if err := client.GraphQL(ctx, rolesPageQuery, vars, &resp); err != nil {
		return nil, fmt.Errorf("listing roles: %w", err)
	}
	return resp.Page.Items, nil
}

// resolveRole finds one role by id or exact slug, preferring the
// organization's custom role over a system role of the same slug.
func resolveRole(ctx context.Context, client *api.Client, selector string) (roleRow, error) {
	selector = strings.TrimSpace(selector)
	rows, err := listRoles(ctx, client, selector, nil)
	if err != nil {
		return roleRow{}, err
	}
	var match *roleRow
	for i := range rows {
		if rows[i].ID == selector || rows[i].Slug == selector {
			if match == nil || (match.IsSystem && !rows[i].IsSystem) {
				match = &rows[i]
			}
		}
	}
	if match == nil {
		return roleRow{}, fmt.Errorf("no role %q", selector)
	}
	return *match, nil
}

func runRoleList(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	if roleListSystem && roleListCustom {
		return fmt.Errorf("use --system or --custom, not both")
	}
	filter := map[string]interface{}{}
	if roleListSystem || roleListCustom {
		filter["isSystem"] = roleListSystem
	}
	rows, err := listRoles(ctx, client, strings.TrimSpace(roleSearch), filter)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No roles matched.")
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tNAME\tSCOPE\tKIND\tPERMISSIONS\tBINDINGS")
	for _, r := range rows {
		kind, bindings := "custom", "-"
		if r.IsSystem {
			kind = "system"
		}
		if r.BindingsCount != nil {
			bindings = fmt.Sprint(*r.BindingsCount)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", r.Slug, r.Name, r.ScopeLevel, kind, len(r.Permissions), bindings)
	}
	return w.Flush()
}

func runRoleShow(cmd *cobra.Command, ctx context.Context, client *api.Client, selector string) error {
	role, err := resolveRole(ctx, client, selector)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, role)
	}
	out := cmd.OutOrStdout()
	kind := "custom"
	if role.IsSystem {
		kind = "system"
	}
	fmt.Fprintf(out, "%s (%s), %s role at %s scope\n", role.Name, role.Slug, kind, role.ScopeLevel)
	if role.Description != "" {
		fmt.Fprintln(out, role.Description)
	}
	fmt.Fprintf(out, "\nPermissions (%d):\n", len(role.Permissions))
	for _, p := range role.Permissions {
		fmt.Fprintf(out, "  %s\n", p)
	}
	return nil
}

func runRoleCreate(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	level := strings.ToUpper(strings.TrimSpace(roleScopeLevel))
	if level == "" {
		return fmt.Errorf("--scope-level is required (ORG, TEAM, PROJECT or APP)")
	}
	input := map[string]interface{}{
		"slug":        strings.TrimSpace(slug),
		"name":        firstNonEmpty(strings.TrimSpace(roleName), strings.TrimSpace(slug)),
		"scopeLevel":  level,
		"permissions": cleanPermissions(rolePermissions),
		"description": roleDescription,
	}
	if from := strings.TrimSpace(roleFrom); from != "" {
		source, err := resolveRole(ctx, client, from)
		if err != nil {
			return err
		}
		input["duplicatedFromId"] = source.ID
		if len(rolePermissions) == 0 {
			input["permissions"] = source.Permissions
		}
	}
	if len(input["permissions"].([]string)) == 0 {
		return fmt.Errorf("give at least one --permission, or --from an existing role")
	}
	return writeRole(cmd, ctx, client, createRoleMutation, "createRole", input)
}

func runRoleUpdate(cmd *cobra.Command, ctx context.Context, client *api.Client, selector string) error {
	role, err := resolveRole(ctx, client, selector)
	if err != nil {
		return err
	}
	if role.IsSystem {
		return fmt.Errorf("%s is a system role; duplicate it with 'astro role create <slug> --from %s' instead", role.Slug, role.Slug)
	}
	input := map[string]interface{}{"id": role.ID}
	if cmd.Flags().Changed("name") {
		input["name"] = roleName
	}
	if cmd.Flags().Changed("description") {
		input["description"] = roleDescription
	}
	if cmd.Flags().Changed("permission") {
		input["permissions"] = cleanPermissions(rolePermissions)
	}
	if len(input) == 1 {
		return fmt.Errorf("nothing to change: pass --name, --description or --permission")
	}
	return writeRole(cmd, ctx, client, updateRoleMutation, "updateRole", input)
}

func writeRole(cmd *cobra.Command, ctx context.Context, client *api.Client, mutation, field string, input map[string]interface{}) error {
	var resp map[string]struct {
		OK     bool            `json:"ok"`
		Errors []mutationError `json:"errors"`
		Data   *roleRow        `json:"data"`
	}
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return err
	}
	result := resp[field]
	if !result.OK || result.Data == nil {
		return fmt.Errorf("%s: %s", field, firstDeployError(result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, result.Data)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Role %s (%s) saved with %d permission(s).\n", result.Data.Name, result.Data.Slug, len(result.Data.Permissions))
	return nil
}

func runRoleBindings(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	filter := map[string]interface{}{}
	if r := strings.TrimSpace(roleBindingsRole); r != "" {
		filter["role"] = []string{r}
	}
	if h := strings.TrimSpace(roleBindingsHolder); h != "" {
		filter["holder"] = []string{h}
	}
	vars := map[string]interface{}{}
	if len(filter) > 0 {
		vars["filter"] = filter
	}
	var resp struct {
		Page struct {
			Items      []roleBindingRow `json:"items"`
			NextCursor *string          `json:"nextCursor"`
		} `json:"astroliftRoleBindingsPage"`
	}
	if err := client.GraphQL(ctx, roleBindingsPageQuery, vars, &resp); err != nil {
		return fmt.Errorf("listing role bindings: %w", err)
	}
	rows := resp.Page.Items
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, rows)
	}
	out := cmd.OutOrStdout()
	if len(rows) == 0 {
		fmt.Fprintln(out, "No role bindings matched.")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tHOLDER\tROLE\tSCOPE\tEXPIRES")
	for _, b := range rows {
		scope := firstNonEmpty(b.SourceScopeLabel, b.ScopeKind+" "+b.ScopeID)
		expires := "-"
		if b.ExpiresAt != nil && *b.ExpiresAt != "" {
			expires = *b.ExpiresAt
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", b.ID, b.holder(), b.Role.Slug, scope, expires)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if resp.Page.NextCursor != nil && *resp.Page.NextCursor != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "note: more bindings exist; narrow with --role or --holder")
	}
	return nil
}

func runRoleGrant(cmd *cobra.Command, ctx context.Context, client *api.Client, org orgRef, selector string) error {
	user, group := strings.TrimSpace(roleGrantUser), strings.TrimSpace(roleGrantGroup)
	if (user == "") == (group == "") {
		return fmt.Errorf("give exactly one of --user or --group")
	}
	scopes := 0
	for _, s := range []string{roleGrantTeam, roleGrantProject, grantAppFlag(cmd)} {
		if strings.TrimSpace(s) != "" {
			scopes++
		}
	}
	if scopes > 1 {
		return fmt.Errorf("give at most one of --team, --project or --app")
	}
	input := map[string]interface{}{}
	if expires := strings.TrimSpace(roleGrantExpires); expires != "" {
		at, err := time.Parse(time.RFC3339, expires)
		if err != nil {
			return fmt.Errorf("--expires must be an RFC 3339 time, e.g. 2026-12-31T00:00:00Z")
		}
		input["expiresAt"] = at.UTC().Format(time.RFC3339)
	}
	role, err := resolveRole(ctx, client, selector)
	if err != nil {
		return err
	}
	input["roleId"] = role.ID
	kind, scopeID, err := resolveGrantScope(cmd, ctx, client, org)
	if err != nil {
		return err
	}
	input["scopeKind"], input["scopeGuid"] = kind, scopeID
	if user != "" {
		id, err := resolveMemberID(ctx, client, user)
		if err != nil {
			return err
		}
		input["userId"] = id
	} else {
		input["groupExternalId"] = group
	}
	var resp struct {
		Grant struct {
			OK     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *roleBindingRow `json:"data"`
		} `json:"grantRole"`
	}
	if err := client.GraphQL(ctx, grantRoleMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return err
	}
	if !resp.Grant.OK || resp.Grant.Data == nil {
		return fmt.Errorf("grantRole: %s", firstDeployError(resp.Grant.Errors))
	}
	b := resp.Grant.Data
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, b)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Granted %s to %s at %s (binding %s).\n",
		b.Role.Slug, b.holder(), firstNonEmpty(b.SourceScopeLabel, b.ScopeKind), b.ID)
	return nil
}

// resolveGrantScope is the working organization unless a narrower scope is named.
func resolveGrantScope(cmd *cobra.Command, ctx context.Context, client *api.Client, org orgRef) (string, string, error) {
	switch {
	case strings.TrimSpace(roleGrantTeam) != "":
		want := strings.TrimSpace(roleGrantTeam)
		var resp struct {
			Teams []teamRef `json:"astroliftTeams"`
		}
		if err := client.GraphQL(ctx, `query { astroliftTeams { id slug name } }`, nil, &resp); err != nil {
			return "", "", err
		}
		for _, t := range resp.Teams {
			if t.ID == want || t.Slug == want {
				return "TEAM", t.ID, nil
			}
		}
		return "", "", fmt.Errorf("no team %q in the working organization", want)
	case strings.TrimSpace(roleGrantProject) != "":
		want := strings.TrimSpace(roleGrantProject)
		var resp struct {
			Projects []projectRef `json:"astroliftProjects"`
		}
		if err := client.GraphQL(ctx, `query { astroliftProjects { id slug name team { slug name } } }`, nil, &resp); err != nil {
			return "", "", err
		}
		for _, p := range resp.Projects {
			if p.ID == want || p.Slug == want {
				return "PROJECT", p.ID, nil
			}
		}
		return "", "", fmt.Errorf("no project %q in the working organization", want)
	case grantAppFlag(cmd) != "":
		want := grantAppFlag(cmd)
		if guidPattern.MatchString(want) {
			return "APP", want, nil
		}
		var resp struct {
			App *struct {
				ID string `json:"id"`
			} `json:"astroliftApp"`
		}
		if err := client.GraphQL(ctx, roleAppIDQuery, map[string]interface{}{"slug": want}, &resp); err != nil {
			return "", "", err
		}
		if resp.App == nil || resp.App.ID == "" {
			return "", "", fmt.Errorf("no app %q in the working organization", want)
		}
		return "APP", resp.App.ID, nil
	default:
		return "ORG", org.ID, nil
	}
}

// resolveMemberID turns a username into the member id grantRole takes.
func resolveMemberID(ctx context.Context, client *api.Client, username string) (string, error) {
	var resp struct {
		Page struct {
			Items []struct {
				User struct {
					ID       string `json:"id"`
					Username string `json:"username"`
				} `json:"user"`
			} `json:"items"`
		} `json:"astroliftMembersPage"`
	}
	if err := client.GraphQL(ctx, roleMembersQuery, map[string]interface{}{"search": username}, &resp); err != nil {
		return "", fmt.Errorf("looking up member %s: %w", username, err)
	}
	for _, m := range resp.Page.Items {
		if strings.EqualFold(m.User.Username, username) {
			return m.User.ID, nil
		}
	}
	return "", fmt.Errorf("no member %q in the working organization", username)
}

func runRoleRevoke(cmd *cobra.Command, ctx context.Context, client *api.Client, bindingID string) error {
	if !roleRevokeConfirmed {
		return fmt.Errorf("revoking binding %s removes access; pass --yes to confirm", bindingID)
	}
	var resp struct {
		Revoke struct {
			OK     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
		} `json:"revokeRoleBinding"`
	}
	vars := map[string]interface{}{"input": map[string]interface{}{"id": strings.TrimSpace(bindingID)}}
	if err := client.GraphQL(ctx, revokeRoleBindingMutation, vars, &resp); err != nil {
		return err
	}
	if !resp.Revoke.OK {
		return fmt.Errorf("revokeRoleBinding: %s", firstDeployError(resp.Revoke.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, map[string]interface{}{"id": bindingID, "revoked": true})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Revoked binding %s.\n", bindingID)
	return nil
}

// grantAppFlag is the global --app, read as the grant's app scope.
func grantAppFlag(cmd *cobra.Command) string {
	v, _ := cmd.Flags().GetString("app")
	return strings.TrimSpace(v)
}

func cleanPermissions(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

func init() {
	roleListCmd.Flags().BoolVar(&roleListSystem, "system", false, "Only the platform's system roles")
	roleListCmd.Flags().BoolVar(&roleListCustom, "custom", false, "Only this organization's custom roles")
	roleListCmd.Flags().StringVar(&roleSearch, "search", "", "Filter by name or slug")
	for _, c := range []*cobra.Command{roleCreateCmd, roleUpdateCmd} {
		c.Flags().StringVar(&roleName, "name", "", "Display name")
		c.Flags().StringVar(&roleDescription, "description", "", "Description")
		c.Flags().StringArrayVar(&rolePermissions, "permission", nil, "Permission string (repeatable or comma-separated); on update, replaces the set")
	}
	roleCreateCmd.Flags().StringVar(&roleScopeLevel, "scope-level", "", "ORG, TEAM, PROJECT or APP (required)")
	roleCreateCmd.Flags().StringVar(&roleFrom, "from", "", "Duplicate an existing role (its permissions unless --permission is given)")
	roleBindingsCmd.Flags().StringVar(&roleBindingsRole, "role", "", "Only bindings of this role")
	roleBindingsCmd.Flags().StringVar(&roleBindingsHolder, "holder", "", "Only this holder: a username, group:<external id>, or me")
	roleGrantCmd.Flags().StringVar(&roleGrantUser, "user", "", "Member username")
	roleGrantCmd.Flags().StringVar(&roleGrantGroup, "group", "", "IdP group external id")
	roleGrantCmd.Flags().StringVar(&roleGrantTeam, "team", "", "Team slug or id (default: the organization)")
	roleGrantCmd.Flags().StringVar(&roleGrantProject, "project", "", "Project slug or id")
	roleGrantCmd.Flags().StringVar(&roleGrantExpires, "expires", "", "Expiry as an RFC 3339 time")
	roleRevokeCmd.Flags().BoolVar(&roleRevokeConfirmed, "yes", false, "Confirm the revocation")

	roleCmd.AddCommand(roleListCmd, roleShowCmd, roleCreateCmd, roleUpdateCmd, roleBindingsCmd, roleGrantCmd, roleRevokeCmd)
	rootCmd.AddCommand(roleCmd)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
