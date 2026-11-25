package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

const ssoApprovalFields = "reference organizationSlug protocol config version active installationWide"

type ssoApproval struct {
	Reference        string         `json:"reference"`
	Organization     string         `json:"organizationSlug"`
	Protocol         string         `json:"protocol"`
	Config           map[string]any `json:"config"`
	Version          int            `json:"version"`
	Active           bool           `json:"active"`
	InstallationWide bool           `json:"installationWide"`
}

func listSSOApprovals(cmd *cobra.Command, client *api.Client) ([]ssoApproval, error) {
	rows := struct {
		Approvals []ssoApproval `json:"astroliftBrowserSsoApprovals"`
	}{Approvals: []ssoApproval{}}
	err := client.GraphQL(cmd.Context(), "query { astroliftBrowserSsoApprovals { "+ssoApprovalFields+" } }", nil, &rows)
	return rows.Approvals, err
}

func readSSOCredential(cmd *cobra.Command, path string) (string, error) {
	var source io.Reader = cmd.InOrStdin()
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("read connection credential: %w", err)
		}
		defer file.Close()
		source = file
	}
	data, err := io.ReadAll(io.LimitReader(source, 32_769))
	if err != nil {
		return "", fmt.Errorf("read connection credential: %w", err)
	}
	credential := strings.TrimRight(string(data), "\r\n")
	if len(data) > 32_768 || strings.TrimSpace(credential) == "" {
		return "", fmt.Errorf("connection credential must contain between 1 and 32768 bytes")
	}
	return credential, nil
}

func approvalInput(cmd *cobra.Command, client *api.Client, reference string) (map[string]any, error) {
	organization, _ := cmd.Flags().GetString("organization")
	protocol, _ := cmd.Flags().GetString("protocol")
	configPath, _ := cmd.Flags().GetString("config-file")
	secretPath, _ := cmd.Flags().GetString("credential-file")
	version, _ := cmd.Flags().GetInt("if-version")
	installationWide, _ := cmd.Flags().GetBool("installation-wide")
	var settings map[string]any
	input := map[string]any{"reference": reference}
	if cmd.Flags().Changed("if-version") {
		if version < 1 {
			return nil, fmt.Errorf("--if-version must be positive")
		}
		rows, err := listSSOApprovals(cmd, client)
		if err != nil {
			return nil, err
		}
		var existing *ssoApproval
		for i := range rows {
			if rows[i].Reference == reference {
				existing = &rows[i]
				break
			}
		}
		if existing == nil || existing.Version != version {
			return nil, fmt.Errorf("connection is missing or changed; review connections approvals")
		}
		if organization == "" {
			organization = existing.Organization
		}
		if protocol == "" {
			protocol = existing.Protocol
		}
		settings = existing.Config
		if !cmd.Flags().Changed("installation-wide") {
			installationWide = existing.InstallationWide
		}
		input["version"] = version
	} else if organization == "" || protocol == "" || configPath == "" || secretPath == "" {
		return nil, fmt.Errorf("approval requires --organization, --protocol, --config-file, and --credential-file")
	}
	if configPath != "" {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("read public connection settings: %w", err)
		}
		settings = nil
		if json.Unmarshal(raw, &settings) != nil || settings == nil {
			return nil, fmt.Errorf("--config-file must contain a JSON object")
		}
	}
	input["organizationSlug"] = organization
	input["protocol"] = protocol
	input["config"] = settings
	input["installationWide"] = installationWide
	if secretPath != "" {
		credential, err := readSSOCredential(cmd, secretPath)
		if err != nil {
			return nil, err
		}
		input["credential"] = credential
	}
	return input, nil
}

func addBrowserSSOApprovalCommands(connections *cobra.Command) {
	list := &cobra.Command{
		Use: "approvals", Short: "List connections approved through Astrolift (platform operator)",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
			if err != nil {
				return err
			}
			rows, err := listSSOApprovals(cmd, client)
			if err != nil {
				return err
			}
			if boolFlag(cmd, "json") {
				return renderJSON(cmd, rows)
			}
			out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(out, "REFERENCE\tORGANIZATION\tPROTOCOL\tVERSION\tACTIVE")
			for _, row := range rows {
				fmt.Fprintf(out, "%s\t%s\t%s\t%d\t%t\n", row.Reference, row.Organization, row.Protocol, row.Version, row.Active)
			}
			return out.Flush()
		},
	}
	approve := &cobra.Command{
		Use: "approve <reference>", Short: "Approve, edit, or rotate a browser SSO connection (platform operator)",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
			if err != nil {
				return err
			}
			input, err := approvalInput(cmd, client, args[0])
			if err != nil {
				return err
			}
			result, err := runAuthMutation(cmd.Context(), client, "saveBrowserSsoApproval", "BrowserSsoApprovalInput", ssoApprovalFields, input)
			if err != nil {
				return err
			}
			if boolFlag(cmd, "json") {
				return renderJSON(cmd, result)
			}
			var row ssoApproval
			if err := json.Unmarshal(result, &row); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s (version %d)\n", row.Reference, row.Version)
			return err
		},
	}
	approve.Flags().String("organization", "", "Organization slug owning the connection")
	approve.Flags().String("protocol", "", "oidc, saml, or github")
	approve.Flags().String("config-file", "", "JSON file of public connection settings")
	approve.Flags().String("credential-file", "", "Client secret or PEM certificate file (- for standard input)")
	approve.Flags().Int("if-version", 0, "Current version required when editing or rotating an existing connection")
	approve.Flags().Bool("installation-wide", false, "Allow installation-wide sign-in for the primary sign-in organization")

	remove := &cobra.Command{
		Use: "remove-approval <reference>", Short: "Remove an inactive SSO connection approval (platform operator)",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			version, _ := cmd.Flags().GetInt("if-version")
			if version < 1 || !boolFlag(cmd, "yes") {
				return fmt.Errorf("pass --if-version with the current version and --yes to remove the connection")
			}
			client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
			if err != nil {
				return err
			}
			const mutation = `mutation($reference: String!, $version: Int!) {
				removeBrowserSsoApproval(reference: $reference, version: $version) { ok errors { code message field } }
			}`
			var result struct {
				Removed struct {
					OK     bool            `json:"ok"`
					Errors []mutationError `json:"errors"`
				} `json:"removeBrowserSsoApproval"`
			}
			err = client.GraphQL(cmd.Context(), mutation, map[string]any{"reference": args[0], "version": version}, &result)
			if err != nil {
				return err
			}
			if !result.Removed.OK {
				return fmt.Errorf("removeBrowserSsoApproval: %s", firstDeployError(result.Removed.Errors))
			}
			if boolFlag(cmd, "json") {
				return renderJSON(cmd, map[string]string{"reference": args[0]})
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), args[0])
			return err
		},
	}
	remove.Flags().Int("if-version", 0, "Current version from connections approvals")
	remove.Flags().Bool("yes", false, "Confirm removing the approval")
	connections.AddCommand(list, approve, remove)
}
