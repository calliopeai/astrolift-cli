package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const myIdentityQuery = `query { astroliftMyProfile { userId username email } }`
const myAccountPermissionsQuery = `query { astroliftMyPermissions }`

const accountGrantNotice = "Account grants held somewhere in the selected organization; not permission to act on a target or a report of this credential's limits."
const diagnosisNotice = "Account diagnostic only: this trace does not establish the current credential's authority or evaluate a mutation's environment and approval requirements."

type permissionIdentity struct {
	UserID   int    `json:"userId"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

type accountGrantSummary struct {
	Interpretation string   `json:"interpretation"`
	Permissions    []string `json:"permissions"`
}

type whoamiResult struct {
	Server struct {
		Name   string `json:"name"`
		APIURL string `json:"apiUrl"`
	} `json:"server"`
	Organization  orgRef               `json:"organization"`
	User          permissionIdentity   `json:"user"`
	AccountGrants *accountGrantSummary `json:"accountGrants,omitempty"`
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the verified current user, server and organization",
	Long: `Read your identity from the selected server in the selected organization.

--permissions also lists account grants held somewhere in that organization.
This summary is informational: it does not approve an action on a target or
describe bearer-token limits, environment policies or approval requirements.
Credentials are never included in the output.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return permissionCommandError(cmd, runWhoami(cmd))
	},
}

func runWhoami(cmd *cobra.Command) error {
	client, cfg, entry, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	org, err := resolveOrg(cmd, cmd.Context(), client, cfg)
	if err != nil {
		return err
	}
	org = redactPermissionOrg(client, org)
	user, err := fetchPermissionIdentity(cmd, client)
	if err != nil {
		return err
	}
	result := whoamiResult{Organization: org, User: *user}
	if cfg.CurrentServer == "" && strings.TrimSpace(viper.GetString("server")) == "" {
		result.Server.Name = entry.DisplayName
	} else {
		result.Server.Name, _, err = selectedServer(cfg, nil)
		if err != nil {
			return err
		}
	}
	result.Server.APIURL = client.RedactDiagnostic(publicServerURL(entry.APIURL))
	result.Server.Name = client.RedactDiagnostic(result.Server.Name)
	if boolFlag(cmd, "permissions") {
		permissions, err := fetchAccountPermissions(cmd, client)
		if err != nil {
			return err
		}
		result.AccountGrants = &accountGrantSummary{Interpretation: accountGrantNotice, Permissions: permissions}
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Server: %s (%s)\nOrg:    %s (%s)\nUser:   %s (id %d)\nEmail:  %s\n",
		result.Server.Name, result.Server.APIURL, org.Slug, org.ID, user.Username, user.UserID, user.Email)
	if result.AccountGrants != nil {
		printAccountPermissions(cmd, result.AccountGrants)
	}
	return nil
}

func redactPermissionOrg(client *api.Client, org orgRef) orgRef {
	org.ID = client.RedactDiagnostic(org.ID)
	org.Name = client.RedactDiagnostic(org.Name)
	org.Slug = client.RedactDiagnostic(org.Slug)
	return org
}

func fetchPermissionIdentity(cmd *cobra.Command, client *api.Client) (*permissionIdentity, error) {
	var response struct {
		Profile *permissionIdentity `json:"astroliftMyProfile"`
	}
	if err := client.GraphQL(cmd.Context(), myIdentityQuery, nil, &response); err != nil {
		return nil, fmt.Errorf("verifying current user: %w", err)
	}
	if response.Profile == nil || response.Profile.UserID <= 0 || strings.TrimSpace(response.Profile.Username) == "" {
		return nil, fmt.Errorf("server returned no verified current user in the selected organization")
	}
	response.Profile.Username = client.RedactDiagnostic(response.Profile.Username)
	response.Profile.Email = client.RedactDiagnostic(response.Profile.Email)
	return response.Profile, nil
}

func fetchAccountPermissions(cmd *cobra.Command, client *api.Client) ([]string, error) {
	var response struct {
		Permissions []string `json:"astroliftMyPermissions"`
	}
	if err := client.GraphQL(cmd.Context(), myAccountPermissionsQuery, nil, &response); err != nil {
		return nil, fmt.Errorf("fetching account grants: %w", err)
	}
	if response.Permissions == nil {
		return nil, fmt.Errorf("server returned no account grant summary")
	}
	for i, permission := range response.Permissions {
		response.Permissions[i] = client.RedactDiagnostic(permission)
	}
	return response.Permissions, nil
}

func printAccountPermissions(cmd *cobra.Command, grants *accountGrantSummary) {
	fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", grants.Interpretation)
	for _, permission := range grants.Permissions {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", permission)
	}
	if len(grants.Permissions) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "  (none)")
	}
}

// Only these inspection commands add machine-readable errors. Other command
// output and exit behavior stays unchanged; errors still go to stderr.
func permissionCommandError(cmd *cobra.Command, err error) error {
	if err == nil || !boolFlag(cmd, "json") {
		return err
	}
	var graphql *api.GraphQLResponseError
	var httpError *api.HTTPError
	var payload any = map[string]string{"message": err.Error()}
	if errors.As(err, &graphql) {
		payload = graphql
	} else if errors.As(err, &httpError) {
		payload = httpError
	}
	encoder := json.NewEncoder(cmd.ErrOrStderr())
	encoder.SetIndent("", "  ")
	if outputErr := encoder.Encode(map[string]any{"error": payload}); outputErr != nil {
		return fmt.Errorf("writing diagnostic error: %w (original error: %v)", outputErr, err)
	}
	return &reportedPermissionError{err}
}

type reportedPermissionError struct{ error }

func (e *reportedPermissionError) Unwrap() error         { return e.error }
func (e *reportedPermissionError) AlreadyReported() bool { return true }

func publicServerURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "(invalid server URL)"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func init() {
	whoamiCmd.Flags().Bool("permissions", false, "Include informational account grants, not target or credential authority")
	rootCmd.AddCommand(whoamiCmd)
}
