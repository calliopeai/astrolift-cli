// Package cmd -- `astro auth-users ...`: the users of a cluster's central
// auth (astrolift-app#2131).
//
// Adding a login to the apps behind central auth used to mean the AWS CLI
// against the install's Cognito pool, with the pool id in hand. These act on
// the cluster's identity provider through the control plane instead, with the
// cluster.users permission (token scope manage:auth-users, which
// `astro auth login --scope clusters` asks for).
//
// Passwords are never a flag, so they never land in shell history: create and
// set-password read one from --password-stdin or a hidden prompt, and create
// without one asks the provider to email a temporary password itself. Nothing
// the server returns carries a password, so nothing here can print one.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	authUsersSearch        string
	authUsersPasswordStdin bool
	authUsersSetPassword   bool
	authUsersPermanent     bool
	authUsersGroups        []string
	authUsersAdd           []string
	authUsersRemove        []string
	authUsersYes           bool
)

const authUsersListQuery = `query($clusterId: GUID!, $search: String) {
  astroliftClusterAuthUsers(clusterId: $clusterId, search: $search) {
    supported reason provider reachNote groups
    users { username email enabled status createdAt groups }
  }
}`

const authUserChangeFields = `ok errors { code message field }`

var authUserMutations = map[string]string{
	"createClusterAuthUser":        `mutation($input: CreateClusterAuthUserInput!) { createClusterAuthUser(input: $input) { ` + authUserChangeFields + ` data { username email } } }`,
	"setClusterAuthUserPassword":   `mutation($input: SetClusterAuthUserPasswordInput!) { setClusterAuthUserPassword(input: $input) { ` + authUserChangeFields + ` } }`,
	"resetClusterAuthUserPassword": `mutation($input: ClusterAuthUserRefInput!) { resetClusterAuthUserPassword(input: $input) { ` + authUserChangeFields + ` } }`,
	"setClusterAuthUserEnabled":    `mutation($input: SetClusterAuthUserEnabledInput!) { setClusterAuthUserEnabled(input: $input) { ` + authUserChangeFields + ` } }`,
	"deleteClusterAuthUser":        `mutation($input: ClusterAuthUserRefInput!) { deleteClusterAuthUser(input: $input) { ` + authUserChangeFields + ` } }`,
	"setClusterAuthUserGroups":     `mutation($input: SetClusterAuthUserGroupsInput!) { setClusterAuthUserGroups(input: $input) { ` + authUserChangeFields + ` } }`,
	"createClusterAuthGroup":       `mutation($input: CreateClusterAuthGroupInput!) { createClusterAuthGroup(input: $input) { ` + authUserChangeFields + ` } }`,
}

type authUser struct {
	Username  string   `json:"username"`
	Email     string   `json:"email"`
	Enabled   bool     `json:"enabled"`
	Status    string   `json:"status"`
	CreatedAt *string  `json:"createdAt"`
	Groups    []string `json:"groups"`
}

type authUsersView struct {
	Supported bool       `json:"supported"`
	Reason    string     `json:"reason"`
	Provider  string     `json:"provider"`
	ReachNote string     `json:"reachNote"`
	Groups    []string   `json:"groups"`
	Users     []authUser `json:"users"`
}

type authUserResult struct {
	OK     bool            `json:"ok"`
	Errors []mutationError `json:"errors"`
}

// ---- plumbing ----------------------------------------------------------------

// authUsersClient resolves the active client and the cluster's id from its slug.
func authUsersClient(cmd *cobra.Command, clusterSlug string) (*api.Client, string, error) {
	client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return nil, "", err
	}
	cluster, err := fetchClusterBySlug(cmd.Context(), client, clusterSlug)
	if err != nil {
		return nil, "", err
	}
	return client, cluster.ID, nil
}

func runAuthUserMutation(ctx context.Context, client *api.Client, name string, input map[string]interface{}) error {
	var resp map[string]authUserResult
	if err := client.GraphQL(ctx, authUserMutations[name], map[string]interface{}{"input": input}, &resp); err != nil {
		return err
	}
	result := resp[name]
	if !result.OK {
		return fmt.Errorf("%s", firstDeployError(result.Errors))
	}
	return nil
}

// readAuthUserPassword reads a password from stdin or a hidden prompt. It is
// never taken from a flag value.
func readAuthUserPassword(cmd *cobra.Command, prompt string) (string, error) {
	if authUsersPasswordStdin {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("reading password from stdin: %w", err)
		}
		if v := strings.TrimRight(string(data), "\r\n"); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("empty password read from stdin")
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("no password provided: pass --password-stdin or run in a terminal")
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), prompt)
	b, err := term.ReadPassword(fd)
	_, _ = fmt.Fprintln(cmd.OutOrStdout())
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	if v := strings.TrimRight(string(b), "\r\n"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("empty password")
}

// ---- commands ----------------------------------------------------------------

var authUsersCmd = &cobra.Command{
	Use:     "auth-users",
	Aliases: []string{"auth-user"},
	Short:   "Manage who can sign in to the apps behind a cluster's central auth",
	Long: `The users of a cluster's central auth identity provider (Amazon Cognito
first): list, create, disable, enable, delete, set or reset a password, and
groups.

A user of the pool can sign in to every app on the cluster that has no access
rule of its own; restrict an app with 'astro app access'.

Needs the cluster.users permission. With a device login, use
'astro auth login --scope clusters'.`,
}

var authUsersListCmd = &cobra.Command{
	Use:   "list <cluster>",
	Short: "List a cluster's sign-in users and groups",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, clusterID, err := authUsersClient(cmd, args[0])
		if err != nil {
			return err
		}
		var resp struct {
			View *authUsersView `json:"astroliftClusterAuthUsers"`
		}
		vars := map[string]interface{}{"clusterId": clusterID, "search": authUsersSearch}
		if err := client.GraphQL(cmd.Context(), authUsersListQuery, vars, &resp); err != nil {
			return fmt.Errorf("listing users on %s: %w", args[0], err)
		}
		if resp.View == nil {
			return fmt.Errorf("cluster %q not found", args[0])
		}
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, resp.View)
		}
		out := cmd.OutOrStdout()
		if !resp.View.Supported {
			_, _ = fmt.Fprintf(out, "Users are not managed here: %s\n", resp.View.Reason)
			return nil
		}
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "EMAIL\tUSERNAME\tENABLED\tSTATUS\tGROUPS")
		for _, u := range resp.View.Users {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%t\t%s\t%s\n", dashIfEmpty(u.Email), u.Username, u.Enabled,
				dashIfEmpty(u.Status), dashIfEmpty(strings.Join(u.Groups, ",")))
		}
		if err := w.Flush(); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "\n%d user(s) in %s. Groups: %s\n%s\n", len(resp.View.Users), resp.View.Provider,
			dashIfEmpty(strings.Join(resp.View.Groups, ", ")), resp.View.ReachNote)
		return nil
	},
}

var authUsersCreateCmd = &cobra.Command{
	Use:   "create <cluster> <email>",
	Short: "Add a sign-in user",
	Long: `Adds a user. Without --set-password or --password-stdin the provider
generates a temporary password and emails the invitation itself.

--permanent skips the change-password prompt at first sign-in; it only
applies with a password you set.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		input := map[string]interface{}{
			"email":     strings.TrimSpace(args[1]),
			"permanent": false,
			"groups":    authUsersGroups,
		}
		if authUsersSetPassword || authUsersPasswordStdin {
			password, err := readAuthUserPassword(cmd, "Password (hidden): ")
			if err != nil {
				return err
			}
			input["password"] = password
			input["permanent"] = authUsersPermanent
		}
		client, clusterID, err := authUsersClient(cmd, args[0])
		if err != nil {
			return err
		}
		input["clusterId"] = clusterID
		if err := runAuthUserMutation(cmd.Context(), client, "createClusterAuthUser", input); err != nil {
			return fmt.Errorf("adding %s: %w", args[1], err)
		}
		if _, set := input["password"]; set {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added %s with the password you set.\n", args[1])
		} else {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added %s. The provider emailed a temporary password.\n", args[1])
		}
		return nil
	},
}

var authUsersSetPasswordCmd = &cobra.Command{
	Use:   "set-password <cluster> <username>",
	Short: "Set a user's password (from a hidden prompt or --password-stdin)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		password, err := readAuthUserPassword(cmd, "New password (hidden): ")
		if err != nil {
			return err
		}
		client, clusterID, err := authUsersClient(cmd, args[0])
		if err != nil {
			return err
		}
		input := map[string]interface{}{
			"clusterId": clusterID, "username": args[1], "password": password, "permanent": authUsersPermanent,
		}
		if err := runAuthUserMutation(cmd.Context(), client, "setClusterAuthUserPassword", input); err != nil {
			return fmt.Errorf("setting the password for %s: %w", args[1], err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Password set for %s.\n", args[1])
		return nil
	},
}

// authUserRefCmd builds a <cluster> <username> command around one mutation.
func authUserRefCmd(use, short, mutation, done string, extra map[string]interface{}) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <cluster> <username>",
		Short: short,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, clusterID, err := authUsersClient(cmd, args[0])
			if err != nil {
				return err
			}
			input := map[string]interface{}{"clusterId": clusterID, "username": args[1]}
			for k, v := range extra {
				input[k] = v
			}
			if err := runAuthUserMutation(cmd.Context(), client, mutation, input); err != nil {
				return fmt.Errorf("%s %s: %w", use, args[1], err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), done+"\n", args[1])
			return nil
		},
	}
}

var (
	authUsersResetCmd = authUserRefCmd("reset-password", "Email the user a reset code",
		"resetClusterAuthUserPassword", "Reset code sent to %s.", nil)
	authUsersDisableCmd = authUserRefCmd("disable", "Stop a user signing in, keeping the account",
		"setClusterAuthUserEnabled", "Disabled %s.", map[string]interface{}{"enabled": false})
	authUsersEnableCmd = authUserRefCmd("enable", "Let a disabled user sign in again",
		"setClusterAuthUserEnabled", "Enabled %s.", map[string]interface{}{"enabled": true})
)

var authUsersDeleteCmd = &cobra.Command{
	Use:   "delete <cluster> <username>",
	Short: "Delete a sign-in user",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !authUsersYes {
			noPrompt, _ := cmd.Root().PersistentFlags().GetBool("no-prompt")
			if noPrompt {
				return fmt.Errorf("delete needs confirmation: pass --yes (--no-prompt is set)")
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Delete %s on %s? They can no longer sign in to any app there. [y/N] ", args[1], args[0])
			var answer string
			_, _ = fmt.Fscan(cmd.InOrStdin(), &answer)
			if !strings.EqualFold(strings.TrimSpace(answer), "y") {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
				return nil
			}
		}
		client, clusterID, err := authUsersClient(cmd, args[0])
		if err != nil {
			return err
		}
		input := map[string]interface{}{"clusterId": clusterID, "username": args[1]}
		if err := runAuthUserMutation(cmd.Context(), client, "deleteClusterAuthUser", input); err != nil {
			return fmt.Errorf("deleting %s: %w", args[1], err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s.\n", args[1])
		return nil
	},
}

var authUsersGroupsCmd = &cobra.Command{
	Use:   "groups <cluster> <username>",
	Short: "Add a user to groups (--add) or remove them (--remove)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(authUsersAdd) == 0 && len(authUsersRemove) == 0 {
			return fmt.Errorf("nothing to change: pass --add and/or --remove")
		}
		client, clusterID, err := authUsersClient(cmd, args[0])
		if err != nil {
			return err
		}
		input := map[string]interface{}{
			"clusterId": clusterID, "username": args[1], "add": authUsersAdd, "remove": authUsersRemove,
		}
		if err := runAuthUserMutation(cmd.Context(), client, "setClusterAuthUserGroups", input); err != nil {
			return fmt.Errorf("changing the groups of %s: %w", args[1], err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Groups of %s updated.\n", args[1])
		return nil
	},
}

var authUsersGroupCreateCmd = &cobra.Command{
	Use:   "group-create <cluster> <name>",
	Short: "Create a group in the cluster's identity provider",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, clusterID, err := authUsersClient(cmd, args[0])
		if err != nil {
			return err
		}
		input := map[string]interface{}{"clusterId": clusterID, "name": args[1]}
		if err := runAuthUserMutation(cmd.Context(), client, "createClusterAuthGroup", input); err != nil {
			return fmt.Errorf("creating group %s: %w", args[1], err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created group %s.\n", args[1])
		return nil
	},
}

func init() {
	authUsersListCmd.Flags().StringVar(&authUsersSearch, "search", "", "Only users whose email starts with this")
	for _, c := range []*cobra.Command{authUsersCreateCmd, authUsersSetPasswordCmd} {
		c.Flags().BoolVar(&authUsersPasswordStdin, "password-stdin", false, "Read the password from stdin")
		c.Flags().BoolVar(&authUsersPermanent, "permanent", false, "Skip the change-password prompt at next sign-in")
	}
	authUsersCreateCmd.Flags().BoolVar(&authUsersSetPassword, "set-password", false, "Prompt for a password instead of emailing a temporary one")
	authUsersCreateCmd.Flags().StringSliceVar(&authUsersGroups, "group", nil, "Add the user to this group (repeatable)")
	authUsersGroupsCmd.Flags().StringSliceVar(&authUsersAdd, "add", nil, "Group to add the user to (repeatable)")
	authUsersGroupsCmd.Flags().StringSliceVar(&authUsersRemove, "remove", nil, "Group to remove the user from (repeatable)")
	authUsersDeleteCmd.Flags().BoolVar(&authUsersYes, "yes", false, "Skip the confirmation")

	authUsersCmd.AddCommand(
		authUsersListCmd, authUsersCreateCmd, authUsersSetPasswordCmd, authUsersResetCmd,
		authUsersDisableCmd, authUsersEnableCmd, authUsersDeleteCmd, authUsersGroupsCmd, authUsersGroupCreateCmd,
	)
	rootCmd.AddCommand(authUsersCmd)
}
