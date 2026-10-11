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

// DNS config is write-only in this API; do not synthesize or round-trip it.
const managedDomainFields = `id zone organizationSlug dnsDriver defaultFor isWildcardManaged
  createdAt provisionState provisionNameservers provisionValidationRecords delegationCheck
  provisionClusterId verificationState challengeRecordName challengeRecordValue verifiedAt`
const managedDomainsListQuery = `query { astroliftManagedDomains { ` + managedDomainFields + ` } }`
const managedDomainCreateMutation = `mutation($input: CreateManagedDomainInput!) {
  createManagedDomain(input: $input) { ok errors { code message field } data { ` + managedDomainFields + ` } }
}`
const managedDomainUpdateMutation = `mutation($input: UpdateManagedDomainInput!) {
  updateManagedDomain(input: $input) { ok errors { code message field } data { ` + managedDomainFields + ` } }
}`
const managedDomainVerifyMutation = `mutation($input: VerifyManagedDomainInput!) {
  verifyManagedDomain(input: $input) { ok errors { code message field } data { zone verified message } }
}`

type managedDomain struct {
	ID                         string          `json:"id"`
	Version                    int             `json:"version,omitempty"`
	Zone                       string          `json:"zone"`
	OrganizationSlug           *string         `json:"organizationSlug"`
	DNSDriver                  string          `json:"dnsDriver"`
	DefaultFor                 string          `json:"defaultFor"`
	IsWildcardManaged          bool            `json:"isWildcardManaged"`
	CreatedAt                  string          `json:"createdAt"`
	ProvisionState             string          `json:"provisionState"`
	ProvisionNameservers       json.RawMessage `json:"provisionNameservers"`
	ProvisionValidationRecords json.RawMessage `json:"provisionValidationRecords"`
	DelegationCheck            json.RawMessage `json:"delegationCheck"`
	ProvisionClusterID         *string         `json:"provisionClusterId"`
	VerificationState          string          `json:"verificationState"`
	ChallengeRecordName        string          `json:"challengeRecordName"`
	ChallengeRecordValue       string          `json:"challengeRecordValue"`
	VerifiedAt                 *string         `json:"verifiedAt"`
}

type managedDomainResult struct {
	OK     bool           `json:"ok"`
	Errors mutationErrors `json:"errors"`
	Data   *managedDomain `json:"data"`
}

func newManagedDomainsCommand() *cobra.Command {
	group := &cobra.Command{
		Use: "domains", Short: "Manage DNS zones for tenant apps and previews",
		Long: `Manage organization-owned and visible platform-shared DNS zones.
Uses the selected server and organization. Shared writes require platform-operator
permission; organization-owned writes require the server's domain permission.
Provisioning, DNS proof of control and domain selection are enforced by the API.`,
	}
	list := &cobra.Command{
		Use: "list", Short: "Audit managed zones, their defaults and provisioning state", Args: cobra.NoArgs,
		RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
			return runManagedDomainsList(cmd, ctx, client)
		}),
	}
	create := &cobra.Command{
		Use: "create <zone>", Short: "Register a managed zone (defaults to tenant apps)", Args: cobra.ExactArgs(1),
		RunE: managedDomainWriteRunE(false),
	}
	create.Flags().String("dns-driver", "", "DNS provider driver (required)")
	create.Flags().String("default-for", "tenant_apps", "Default use: tenant_apps, preview_envs, both, none")
	create.Flags().Bool("wildcard", false, "Manage wildcard DNS/TLS")
	create.Flags().Bool("shared", false, "Create a platform-shared zone (requires platform-operator permission)")
	create.Flags().String("dns-config", "", "JSON object file for driver config (never printed)")
	update := &cobra.Command{
		Use: "update <id>", Short: "Change only the supplied managed-zone settings", Args: cobra.ExactArgs(1),
		RunE: managedDomainWriteRunE(true),
	}
	update.Flags().String("default-for", "", "Default use: tenant_apps, preview_envs, both, none")
	update.Flags().Bool("wildcard", false, "Manage wildcard DNS/TLS; use --wildcard=false to disable")
	update.Flags().String("dns-config", "", "JSON object file replacing the entire driver config (never printed)")
	verify := &cobra.Command{
		Use: "verify <zone>", Short: "Verify the published TXT proof of control",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return err
			}
			if strings.TrimSpace(args[0]) == "" {
				return fmt.Errorf("a nonempty zone is required")
			}
			return nil
		},
		RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
			return runManagedDomainVerify(cmd, ctx, client, args[0])
		}),
	}
	group.AddCommand(list, create, update, verify)
	addManagedDomainDiagnosticCommands(group)
	return group
}

// Parse local inputs before resolving credentials or making selection requests.
func managedDomainWriteRunE(update bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		input, err := managedDomainInput(cmd, args[0], update)
		if err != nil {
			return err
		}
		return workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
			return runManagedDomainWrite(cmd, ctx, client, input, update)
		})(cmd, args)
	}
}

func managedDomainInput(cmd *cobra.Command, target string, update bool) (map[string]interface{}, error) {
	if strings.TrimSpace(target) == "" {
		return nil, fmt.Errorf("a nonempty zone or domain id is required")
	}
	input := map[string]interface{}{}
	if update {
		input["id"] = target
		if !cmd.Flags().Changed("default-for") && !cmd.Flags().Changed("wildcard") && !cmd.Flags().Changed("dns-config") {
			return nil, fmt.Errorf("supply --default-for, --wildcard or --dns-config to update")
		}
	} else {
		driver, _ := cmd.Flags().GetString("dns-driver")
		if strings.TrimSpace(driver) == "" {
			return nil, fmt.Errorf("--dns-driver is required")
		}
		input["zone"] = target
		input["dnsDriver"] = driver
		input["organizationScoped"] = !boolFlag(cmd, "shared")
	}
	if !update || cmd.Flags().Changed("default-for") {
		value, _ := cmd.Flags().GetString("default-for")
		switch value {
		case "tenant_apps", "preview_envs", "both", "none":
			input["defaultFor"] = value
		default:
			return nil, fmt.Errorf("--default-for must be tenant_apps, preview_envs, both or none")
		}
	}
	if !update || cmd.Flags().Changed("wildcard") {
		input["isWildcardManaged"] = boolFlag(cmd, "wildcard")
	}
	if cmd.Flags().Changed("dns-config") {
		path, _ := cmd.Flags().GetString("dns-config")
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading --dns-config file: %w", err)
		}
		var config map[string]json.RawMessage
		if err := json.Unmarshal(raw, &config); err != nil || config == nil {
			// Driver config may contain credentials: never echo its raw content.
			return nil, fmt.Errorf("--dns-config must contain a JSON object")
		}
		input["dnsConfig"] = config
	}
	return input, nil
}

func runManagedDomainsList(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	var response struct {
		Domains []managedDomain `json:"astroliftManagedDomains"`
	}
	if err := client.GraphQL(ctx, managedDomainsListQuery, nil, &response); err != nil {
		return fmt.Errorf("listing managed domains: %w", err)
	}
	if response.Domains == nil {
		response.Domains = []managedDomain{}
	}
	if len(response.Domains) >= 200 {
		fmt.Fprintln(cmd.ErrOrStderr(), "note: the managed-domain API returns at most 200 rows; this audit may be incomplete")
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, response.Domains)
	}
	if len(response.Domains) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No managed domains visible in the selected organization.")
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tZONE\tOWNER\tDNS DRIVER\tDEFAULT FOR\tWILDCARD\tVERIFICATION\tPROVISIONING")
	for _, domain := range response.Domains {
		owner := "platform-shared"
		if domain.OrganizationSlug != nil {
			owner = *domain.OrganizationSlug
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", domain.ID, domain.Zone, owner,
			domain.DNSDriver, domain.DefaultFor, yesNo(domain.IsWildcardManaged),
			dashIfEmpty(domain.VerificationState), dashIfEmpty(domain.ProvisionState))
	}
	return w.Flush()
}

func runManagedDomainWrite(cmd *cobra.Command, ctx context.Context, client *api.Client, input map[string]interface{}, update bool) error {
	query, field := managedDomainCreateMutation, "createManagedDomain"
	if update {
		query, field = managedDomainUpdateMutation, "updateManagedDomain"
	}
	var response map[string]managedDomainResult
	if err := client.GraphQL(ctx, query, map[string]interface{}{"input": input}, &response); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	result := response[field]
	if !result.OK {
		return fmt.Errorf("%s: %s", field, firstMutationError(result.Errors))
	}
	if result.Data == nil {
		return fmt.Errorf("%s returned no domain", field)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, result.Data)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s), default_for=%s, verification=%s, provisioning=%s\n", field,
		result.Data.Zone, result.Data.ID, result.Data.DefaultFor, result.Data.VerificationState,
		dashIfEmpty(result.Data.ProvisionState))
	if result.Data.VerificationState == "pending" {
		fmt.Fprintf(cmd.OutOrStdout(), "Publish TXT %s = %s, then run `astro operator domains verify %s`.\n",
			result.Data.ChallengeRecordName, result.Data.ChallengeRecordValue, result.Data.Zone)
	}
	return nil
}

func runManagedDomainVerify(cmd *cobra.Command, ctx context.Context, client *api.Client, zone string) error {
	if strings.TrimSpace(zone) == "" {
		return fmt.Errorf("a nonempty zone is required")
	}
	var response struct {
		Result struct {
			OK     bool           `json:"ok"`
			Errors mutationErrors `json:"errors"`
			Data   *struct {
				Zone     string `json:"zone"`
				Verified bool   `json:"verified"`
				Message  string `json:"message"`
			} `json:"data"`
		} `json:"verifyManagedDomain"`
	}
	if err := client.GraphQL(ctx, managedDomainVerifyMutation, map[string]interface{}{"input": map[string]interface{}{"zone": zone}}, &response); err != nil {
		return fmt.Errorf("verifyManagedDomain: %w", err)
	}
	result := response.Result
	if !result.OK {
		return fmt.Errorf("verifyManagedDomain: %s", firstMutationError(result.Errors))
	}
	if result.Data == nil {
		return fmt.Errorf("verifyManagedDomain returned no verification result")
	}
	if boolFlag(cmd, "json") {
		if err := renderJSON(cmd, result.Data); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: verified=%s: %s\n", result.Data.Zone, yesNo(result.Data.Verified), result.Data.Message)
	}
	if !result.Data.Verified {
		return fmt.Errorf("domain proof of control is still pending: %s", result.Data.Message)
	}
	return nil
}

func init() {
	operatorCmd.AddCommand(newManagedDomainsCommand())
}
