// Package cmd -- `astro app access ...`: who may enter an app behind central
// auth (astrolift-app#2132).
//
// Enforced at the Envoy edge: a user outside the list signs in and gets a
// no-access page, never the app. An app whose astrolift.toml declares
// [ingress.access] is managed there; these commands show it and the server
// refuses to change it, so the repo stays the source of truth.
//
//   - show  -> astroliftAppAccess(appSlug)
//   - allow / deny / clear -> setAppAccess(input) with the whole new list,
//     after astroliftAppAccessPreview reports who would lose access
package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

var (
	appAccessGroups []string
	appAccessUsers  []string
	appAccessYes    bool
)

const appAccessQuery = `query($appSlug: String!) {
  astroliftAppAccess(appSlug: $appSlug) { appSlug groups users restricted managedByManifest enforcedOn }
}`

const appAccessPreviewQuery = `query($appSlug: String!, $groups: [String!]!, $users: [String!]!) {
  astroliftAppAccessPreview(appSlug: $appSlug, groups: $groups, users: $users) { allowed total losing }
}`

const setAppAccessMutation = `mutation($input: SetAppAccessInput!) {
  setAppAccess(input: $input) {
    ok
    errors { code message field }
    data { appSlug groups users restricted managedByManifest enforcedOn }
  }
}`

type appAccess struct {
	AppSlug           string   `json:"appSlug"`
	Groups            []string `json:"groups"`
	Users             []string `json:"users"`
	Restricted        bool     `json:"restricted"`
	ManagedByManifest bool     `json:"managedByManifest"`
	EnforcedOn        []string `json:"enforcedOn"`
}

type appAccessPreview struct {
	Allowed *int     `json:"allowed"`
	Total   *int     `json:"total"`
	Losing  []string `json:"losing"`
}

func fetchAppAccess(ctx context.Context, client *api.Client, slug string) (*appAccess, error) {
	var resp struct {
		Access *appAccess `json:"astroliftAppAccess"`
	}
	if err := client.GraphQL(ctx, appAccessQuery, map[string]interface{}{"appSlug": slug}, &resp); err != nil {
		return nil, fmt.Errorf("reading access for %s: %w", slug, err)
	}
	if resp.Access == nil {
		return nil, fmt.Errorf("app %q not found", slug)
	}
	return resp.Access, nil
}

func renderAppAccess(cmd *cobra.Command, a *appAccess) error {
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, a)
	}
	out := cmd.OutOrStdout()
	if !a.Restricted {
		fmt.Fprintf(out, "%s: open to every signed-in user of the cluster's central auth.\n", a.AppSlug)
	} else {
		fmt.Fprintf(out, "%s: only these may enter.\n  groups: %s\n  users:  %s\n", a.AppSlug,
			dashIfEmpty(strings.Join(a.Groups, ", ")), dashIfEmpty(strings.Join(a.Users, ", ")))
	}
	if a.ManagedByManifest {
		fmt.Fprintln(out, "Set by [ingress.access] in astrolift.toml; change it there.")
	}
	if len(a.EnforcedOn) == 0 {
		fmt.Fprintln(out, "Not enforced yet: none of this app's clusters is on the Envoy edge.")
	}
	return nil
}

// mergeAccess applies an allow or deny to the current lists.
func mergeAccess(current []string, add, remove []string) []string {
	seen := map[string]bool{}
	var out []string
	drop := map[string]bool{}
	for _, r := range remove {
		drop[strings.TrimSpace(r)] = true
	}
	for _, v := range append(append([]string{}, current...), add...) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] || drop[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func runAppAccessChange(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string, groups, users []string) error {
	var preview struct {
		P *appAccessPreview `json:"astroliftAppAccessPreview"`
	}
	vars := map[string]interface{}{"appSlug": slug, "groups": groups, "users": users}
	if err := client.GraphQL(ctx, appAccessPreviewQuery, vars, &preview); err == nil && preview.P != nil && len(preview.P.Losing) > 0 && !appAccessYes {
		noPrompt, _ := cmd.Root().PersistentFlags().GetBool("no-prompt")
		if noPrompt {
			return fmt.Errorf("%d user(s) would lose access (%s): pass --yes to confirm",
				len(preview.P.Losing), strings.Join(preview.P.Losing, ", "))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%d user(s) would lose access: %s\nContinue? [y/N] ",
			len(preview.P.Losing), strings.Join(preview.P.Losing, ", "))
		var answer string
		_, _ = fmt.Fscan(cmd.InOrStdin(), &answer)
		if !strings.EqualFold(strings.TrimSpace(answer), "y") {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
			return nil
		}
	}

	var resp struct {
		Result struct {
			OK     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *appAccess      `json:"data"`
		} `json:"setAppAccess"`
	}
	input := map[string]interface{}{"appSlug": slug, "groups": groups, "users": users}
	if err := client.GraphQL(ctx, setAppAccessMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("setting access for %s: %w", slug, err)
	}
	if !resp.Result.OK {
		return fmt.Errorf("setting access for %s failed: %s", slug, firstDeployError(resp.Result.Errors))
	}
	return renderAppAccess(cmd, resp.Result.Data)
}

var appAccessCmd = &cobra.Command{
	Use:   "access",
	Short: "Who may enter the app behind central auth",
	Long: `Restrict an app to groups of the cluster's identity provider and to
users by email. Without a rule, every signed-in user may enter. Enforced at
the Envoy edge; a user outside the list gets a no-access page.

Group names are the identity provider's (see 'astro auth-users list').`,
}

func appAccessRun(change func(a *appAccess) (groups, users []string)) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		slug, err := resolveAppSlug(cmd, firstArg(args))
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		current, err := fetchAppAccess(cmd.Context(), client, slug)
		if err != nil {
			return err
		}
		if change == nil {
			return renderAppAccess(cmd, current)
		}
		if current.ManagedByManifest {
			return fmt.Errorf("%s's access is set by [ingress.access] in its astrolift.toml; change it there", slug)
		}
		groups, users := change(current)
		return runAppAccessChange(cmd, cmd.Context(), client, slug, groups, users)
	}
}

var appAccessShowCmd = &cobra.Command{
	Use:   "show [app]",
	Short: "Show who may enter the app",
	Args:  cobra.MaximumNArgs(1),
	RunE:  appAccessRun(nil),
}

var appAccessAllowCmd = &cobra.Command{
	Use:   "allow [app] --group <g> | --user <email>",
	Short: "Let a group or user in (restricts an open app to them)",
	Args:  cobra.MaximumNArgs(1),
	RunE: appAccessRun(func(a *appAccess) ([]string, []string) {
		return mergeAccess(a.Groups, appAccessGroups, nil), mergeAccess(a.Users, lower(appAccessUsers), nil)
	}),
}

var appAccessDenyCmd = &cobra.Command{
	Use:   "deny [app] --group <g> | --user <email>",
	Short: "Remove a group or user from the app's access list",
	Args:  cobra.MaximumNArgs(1),
	RunE: appAccessRun(func(a *appAccess) ([]string, []string) {
		return mergeAccess(a.Groups, nil, appAccessGroups), mergeAccess(a.Users, nil, lower(appAccessUsers))
	}),
}

var appAccessClearCmd = &cobra.Command{
	Use:   "clear [app]",
	Short: "Remove the rule, opening the app to every signed-in user",
	Args:  cobra.MaximumNArgs(1),
	RunE: appAccessRun(func(*appAccess) ([]string, []string) {
		return []string{}, []string{}
	}),
}

func lower(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strings.ToLower(strings.TrimSpace(v)))
	}
	return out
}

func init() {
	for _, c := range []*cobra.Command{appAccessAllowCmd, appAccessDenyCmd} {
		c.Flags().StringSliceVar(&appAccessGroups, "group", nil, "Identity provider group (repeatable)")
		c.Flags().StringSliceVar(&appAccessUsers, "user", nil, "User email (repeatable)")
	}
	for _, c := range []*cobra.Command{appAccessAllowCmd, appAccessDenyCmd, appAccessClearCmd} {
		c.Flags().BoolVar(&appAccessYes, "yes", false, "Don't ask when users would lose access")
	}
	appAccessCmd.AddCommand(appAccessShowCmd, appAccessAllowCmd, appAccessDenyCmd, appAccessClearCmd)
	appCmd.AddCommand(appAccessCmd)
}
