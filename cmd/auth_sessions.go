package cmd

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type browserSession struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	ClientKind string  `json:"clientKind"`
	LastSeenAt *string `json:"lastSeenAt"`
	ExpiresAt  *string `json:"expiresAt"`
}

func newAuthSessionsCommand() *cobra.Command {
	parent := &cobra.Command{Use: "sessions", Short: "Manage your browser sessions"}
	list := &cobra.Command{
		Use: "list", Short: "List your browser sessions", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
			if err != nil {
				return err
			}
			reply := struct {
				Sessions []browserSession `json:"astroliftActiveSessions"`
			}{Sessions: []browserSession{}}
			const query = `query { astroliftActiveSessions { id label clientKind lastSeenAt expiresAt } }`
			if err := client.GraphQL(cmd.Context(), query, nil, &reply); err != nil {
				return err
			}
			if boolFlag(cmd, "json") {
				return renderJSON(cmd, reply.Sessions)
			}
			out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(out, "ID\tLABEL\tCLIENT\tLAST SEEN\tEXPIRES")
			timeOrEmpty := func(value *string) string {
				if value == nil {
					return ""
				}
				return *value
			}
			for _, session := range reply.Sessions {
				fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n",
					session.ID, session.Label, session.ClientKind, timeOrEmpty(session.LastSeenAt), timeOrEmpty(session.ExpiresAt))
			}
			return out.Flush()
		},
	}
	revoke := &cobra.Command{
		Use: "revoke <id>", Short: "Revoke a browser session by ID", Args: cobra.ExactArgs(1),
		RunE: runAuthSessionRevoke,
	}
	revokeAll := &cobra.Command{
		Use: "revoke-all", Short: "Revoke all your browser sessions", Args: cobra.NoArgs,
		RunE: runAuthSessionRevoke,
	}
	revokeAll.Flags().Bool("yes", false, "Confirm revoking all browser sessions")
	parent.AddCommand(list, revoke, revokeAll)
	return parent
}

func runAuthSessionRevoke(cmd *cobra.Command, args []string) error {
	all := cmd.Name() == "revoke-all"
	if all && !boolFlag(cmd, "yes") {
		return fmt.Errorf("pass --yes to revoke all browser sessions")
	}
	client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if all {
		raw, err = runAuthMutation(cmd.Context(), client, "logoutAllSessions", "LogoutAllSessionsInput", "revokedCount", map[string]any{"keepCurrent": false})
	} else {
		raw, err = runAuthMutation(cmd.Context(), client, "revokeAstroliftSession", "RevokeAstroliftSessionInput", "id revoked", map[string]any{"sessionId": args[0]})
	}
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, raw)
	}
	var result struct {
		RevokedCount int  `json:"revokedCount"`
		Revoked      bool `json:"revoked"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if all {
		_, err = fmt.Fprintf(out, "Revoked %d browser session(s).\n", result.RevokedCount)
	} else if result.Revoked {
		_, err = fmt.Fprintln(out, "Browser session revoked.")
	} else {
		_, err = fmt.Fprintln(out, "Browser session was already revoked.")
	}
	return err
}

func init() {
	authCmd.AddCommand(newAuthSessionsCommand())
}
