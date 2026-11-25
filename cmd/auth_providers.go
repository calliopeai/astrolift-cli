package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const identityProviderFields = `id name kind clientId oidcDiscoveryUrl metadataUrl version isActive config`

type identityProviderRow struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Kind             string          `json:"kind"`
	ClientID         string          `json:"clientId"`
	OIDCDiscoveryURL string          `json:"oidcDiscoveryUrl"`
	MetadataURL      string          `json:"metadataUrl"`
	Version          int             `json:"version"`
	Active           bool            `json:"isActive"`
	Config           json.RawMessage `json:"config"`
}

func identityProvidersClient(cmd *cobra.Command) (*api.Client, error) {
	client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return nil, err
	}
	if _, err := resolveOrg(cmd, cmd.Context(), client, cfg); err != nil {
		return nil, err
	}
	return client, nil
}

func runAuthMutation(ctx context.Context, client *api.Client, field, inputType, selection string, input map[string]any) (json.RawMessage, error) {
	query := fmt.Sprintf(`mutation($input: %s!) { %s(input: $input) { ok errors { code message field } data { %s } } }`, inputType, field, selection)
	var result map[string]struct {
		OK     bool            `json:"ok"`
		Errors []mutationError `json:"errors"`
		Data   json.RawMessage `json:"data"`
	}
	if err := client.GraphQL(ctx, query, map[string]any{"input": input}, &result); err != nil {
		return nil, err
	}
	receipt := result[field]
	if !receipt.OK {
		return nil, fmt.Errorf("%s: %s", field, firstDeployError(receipt.Errors))
	}
	if len(receipt.Data) == 0 || string(receipt.Data) == "null" {
		return nil, fmt.Errorf("%s: the server did not return the updated record", field)
	}
	return receipt.Data, nil
}

func providerMutation(cmd *cobra.Command, field, inputType, selection string, input map[string]any) error {
	client, err := identityProvidersClient(cmd)
	if err != nil {
		return err
	}
	result, err := runAuthMutation(cmd.Context(), client, field, inputType, selection, input)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, result)
	}
	var row identityProviderRow
	if err := json.Unmarshal(result, &row); err != nil {
		return err
	}
	if row.Name == "" {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), row.ID)
	} else {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s (%s)\n", row.Name, row.ID)
	}
	return err
}

var providerConnectionFlags = []struct {
	flag, field, help string
}{
	{"name", "displayName", "Provider display name"},
	{"oidc-discovery-url", "oidcDiscoveryUrl", "OIDC discovery URL"},
	{"metadata-url", "metadataUrl", "Provider metadata URL"},
	{"client-id", "clientId", "OAuth client ID"},
	{"client-secret-ref", "clientSecretRef", "Reference to the client secret in the secrets backend"},
}

func providerConnectionInput(cmd *cobra.Command) (map[string]any, error) {
	input := make(map[string]any)
	for _, option := range providerConnectionFlags {
		if cmd.Flags().Changed(option.flag) {
			value, _ := cmd.Flags().GetString(option.flag)
			input[option.field] = strings.TrimSpace(value)
		}
	}
	if cmd.Flags().Changed("config-file") {
		path, _ := cmd.Flags().GetString("config-file")
		payload, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read provider configuration: %w", err)
		}
		var config map[string]json.RawMessage
		if err := json.Unmarshal(payload, &config); err != nil || config == nil {
			return nil, fmt.Errorf("--config-file must contain a JSON object")
		}
		input["config"] = config
	}
	return input, nil
}

type browserSSOConnection struct {
	Reference    string `json:"reference"`
	DiscoveryURL string `json:"discoveryUrl"`
	Protocol     string `json:"protocol"`
	MetadataURL  string `json:"metadataUrl"`
	ClientID     string `json:"clientId"`
}

func browserSSOConnections(cmd *cobra.Command) ([]browserSSOConnection, error) {
	client, err := identityProvidersClient(cmd)
	if err != nil {
		return nil, err
	}
	reply := struct {
		Connections []browserSSOConnection `json:"astroliftBrowserSsoConnections"`
	}{Connections: []browserSSOConnection{}}
	const query = `query { astroliftBrowserSsoConnections { reference discoveryUrl clientId protocol metadataUrl } }`
	if err := client.GraphQL(cmd.Context(), query, nil, &reply); err != nil {
		return nil, err
	}
	return reply.Connections, nil
}

func applyIdentityProviderConnection(cmd *cobra.Command, input map[string]any) error {
	if !cmd.Flags().Changed("connection") {
		return nil
	}
	for _, flag := range []string{"oidc-discovery-url", "client-id", "client-secret-ref"} {
		if cmd.Flags().Changed(flag) {
			return fmt.Errorf("--connection supplies --%s", flag)
		}
	}
	reference, _ := cmd.Flags().GetString("connection")
	connections, err := browserSSOConnections(cmd)
	if err != nil {
		return err
	}
	for _, connection := range connections {
		if connection.Reference == reference {
			if kind, ok := input["kind"].(string); ok {
				protocol := kind
				switch kind {
				case "oidc", "auth0", "cognito", "okta", "azure_ad", "google":
					protocol = "oidc"
				}
				if protocol != connection.Protocol {
					return fmt.Errorf("connection %q uses %s, which is incompatible with provider kind %q", reference, connection.Protocol, kind)
				}
			}
			input["oidcDiscoveryUrl"] = connection.DiscoveryURL
			if connection.Protocol != "oidc" {
				if cmd.Flags().Changed("metadata-url") {
					return fmt.Errorf("--connection supplies --metadata-url for a %s connection", connection.Protocol)
				}
				input["metadataUrl"] = connection.MetadataURL
			}
			input["clientId"] = connection.ClientID
			input["clientSecretRef"] = connection.Reference
			return nil
		}
	}
	return fmt.Errorf("approved browser SSO connection %q was not found in the selected organization", reference)
}

func listIdentityProviders(cmd *cobra.Command, client *api.Client) ([]identityProviderRow, error) {
	var result struct {
		Providers []identityProviderRow `json:"astroliftIdentityProviders"`
	}
	query := `query { astroliftIdentityProviders { ` + identityProviderFields + ` } }`
	err := client.GraphQL(cmd.Context(), query, nil, &result)
	return result.Providers, err
}

func applyIdentityProviderMFA(cmd *cobra.Command, id string, input map[string]any) error {
	if !cmd.Flags().Changed("require-mfa") {
		return nil
	}
	config, _ := input["config"].(map[string]json.RawMessage)
	if config == nil && id != "" {
		client, err := identityProvidersClient(cmd)
		if err != nil {
			return err
		}
		providers, err := listIdentityProviders(cmd, client)
		if err != nil {
			return err
		}
		for _, provider := range providers {
			if provider.ID == id {
				if err := json.Unmarshal(provider.Config, &config); err != nil {
					return fmt.Errorf("read provider configuration: %w", err)
				}
				if config == nil {
					config = make(map[string]json.RawMessage)
				}
				break
			}
		}
		if config == nil {
			return fmt.Errorf("provider %q was not found in the selected organization", id)
		}
	}
	if config == nil {
		config = make(map[string]json.RawMessage)
	}
	if boolFlag(cmd, "require-mfa") {
		config["require_mfa"] = json.RawMessage("true")
	} else {
		delete(config, "require_mfa")
	}
	input["config"] = config
	return nil
}

func newIdentityProvidersCommand() *cobra.Command {
	providers := &cobra.Command{
		Use:   "providers",
		Short: "Manage the organization's identity provider records",
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List identity providers and their versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := identityProvidersClient(cmd)
			if err != nil {
				return err
			}
			found, err := listIdentityProviders(cmd, client)
			if err != nil {
				return err
			}
			if boolFlag(cmd, "json") {
				if found == nil {
					found = []identityProviderRow{}
				}
				return renderJSON(cmd, found)
			}
			out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(out, "ID\tNAME\tKIND\tACTIVE\tVERSION\tENDPOINT")
			for _, provider := range found {
				endpoint := provider.OIDCDiscoveryURL
				if endpoint == "" {
					endpoint = provider.MetadataURL
				}
				fmt.Fprintf(out, "%s\t%s\t%s\t%t\t%d\t%s\n",
					provider.ID, provider.Name, provider.Kind, provider.Active, provider.Version, endpoint)
			}
			return out.Flush()
		},
	}
	connections := &cobra.Command{
		Use:   "connections",
		Short: "List approved browser SSO connections for the organization",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			found, err := browserSSOConnections(cmd)
			if err != nil {
				return err
			}
			if boolFlag(cmd, "json") {
				return renderJSON(cmd, found)
			}
			out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(out, "REFERENCE\tPROTOCOL\tCLIENT ID\tDISCOVERY OR METADATA URL")
			for _, connection := range found {
				fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", connection.Reference, connection.Protocol, connection.ClientID, connection.DiscoveryURL+connection.MetadataURL)
			}
			return out.Flush()
		},
	}
	create := &cobra.Command{
		Use:   "create <kind>",
		Short: "Create an identity provider record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := providerConnectionInput(cmd)
			if err != nil {
				return err
			}
			input["kind"] = args[0]
			if err := applyIdentityProviderConnection(cmd, input); err != nil {
				return err
			}
			if err := applyIdentityProviderMFA(cmd, "", input); err != nil {
				return err
			}
			return providerMutation(cmd, "createIdentityProvider", "CreateIdentityProviderInput", identityProviderFields, input)
		},
	}
	edit := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit the connection settings of an identity provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := providerConnectionInput(cmd)
			if err != nil {
				return err
			}
			if err := applyIdentityProviderConnection(cmd, input); err != nil {
				return err
			}
			if err := applyIdentityProviderMFA(cmd, args[0], input); err != nil {
				return err
			}
			if len(input) == 0 {
				return fmt.Errorf("supply at least one connection setting to edit")
			}
			version, _ := cmd.Flags().GetInt("if-version")
			if version < 0 {
				return fmt.Errorf("--if-version must be non-negative")
			}
			input["id"] = args[0]
			input["ifMatchVersion"] = version
			return providerMutation(cmd, "updateIdentityProvider", "UpdateIdentityProviderInput", identityProviderFields, input)
		},
	}
	edit.Flags().Int("if-version", 0, "Current version from the provider list")
	_ = edit.MarkFlagRequired("if-version")
	for _, cmd := range []*cobra.Command{create, edit} {
		for _, option := range providerConnectionFlags {
			cmd.Flags().String(option.flag, "", option.help)
		}
		cmd.Flags().String("connection", "", "Approved browser SSO connection reference")
		cmd.Flags().String("config-file", "", "JSON object of provider-specific configuration")
		cmd.Flags().Bool("require-mfa", false, "Require a verified MFA claim at browser SSO sign-in")
	}

	activate := &cobra.Command{
		Use:   "activate <id>",
		Short: "Set the organization's primary provider record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !boolFlag(cmd, "yes") {
				return fmt.Errorf("pass --yes to set the primary provider")
			}
			return providerMutation(cmd, "setActiveIdentityProvider", "SetActiveIdentityProviderInput", identityProviderFields, map[string]any{"id": args[0]})
		},
	}
	activate.Flags().Bool("yes", false, "Confirm changing the primary provider")
	remove := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an inactive identity provider record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !boolFlag(cmd, "yes") {
				return fmt.Errorf("pass --yes to delete the provider")
			}
			return providerMutation(cmd, "softDeleteIdentityProvider", "SoftDeleteByGuidInput", "id deleted", map[string]any{"id": args[0]})
		},
	}
	remove.Flags().Bool("yes", false, "Confirm deleting the provider")
	addBrowserSSOApprovalCommands(connections)
	providers.AddCommand(list, connections, create, edit, activate, remove)
	return providers
}

func init() {
	authCmd.AddCommand(newIdentityProvidersCommand())
}
