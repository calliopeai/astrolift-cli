package cmd

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const orgAuthPolicyFields = "slug allowStepUpPin trustedDomainSignup"

type organizationAuthPolicy struct {
	Slug                string `json:"slug"`
	AllowStepUpPin      bool   `json:"allowStepUpPin"`
	TrustedDomainSignup bool   `json:"trustedDomainSignup"`
}

func authOrganizationClient(cmd *cobra.Command) (*api.Client, orgRef, error) {
	client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return nil, orgRef{}, err
	}
	org, err := resolveOrg(cmd, cmd.Context(), client, cfg)
	return client, org, err
}

func writeOrganizationAuthPolicy(cmd *cobra.Command, policy organizationAuthPolicy) error {
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, policy)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\nallow-step-up-pin = %t\ntrusted-domain-signup = %t\n",
		policy.Slug, policy.AllowStepUpPin, policy.TrustedDomainSignup)
	return err
}

func newOrganizationAuthPolicyCommand() *cobra.Command {
	parent := &cobra.Command{Use: "organization", Short: "Manage authentication policies in the selected organization"}
	parent.AddCommand(
		&cobra.Command{
			Use: "list", Short: "Show the selected organization's authentication policies", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				client, org, err := authOrganizationClient(cmd)
				if err != nil {
					return err
				}
				var reply struct {
					Policy *organizationAuthPolicy `json:"astroliftOrganization"`
				}
				query := `query($slug: String!) { astroliftOrganization(slug: $slug) { ` + orgAuthPolicyFields + ` } }`
				if err := client.GraphQL(cmd.Context(), query, map[string]any{"slug": org.Slug}, &reply); err != nil {
					return err
				}
				if reply.Policy == nil {
					return fmt.Errorf("organization authentication policy is unavailable")
				}
				return writeOrganizationAuthPolicy(cmd, *reply.Policy)
			},
		},
		&cobra.Command{
			Use:   "set <allow-step-up-pin|trusted-domain-signup> <true|false>",
			Short: "Change an organization authentication policy", Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				field := map[string]string{"allow-step-up-pin": "allowStepUpPin", "trusted-domain-signup": "trustedDomainSignup"}[args[0]]
				if field == "" || (args[1] != "true" && args[1] != "false") {
					return fmt.Errorf("choose allow-step-up-pin or trusted-domain-signup and true or false")
				}
				value := args[1] == "true"
				client, org, err := authOrganizationClient(cmd)
				if err != nil {
					return err
				}
				result, err := runAuthMutation(cmd.Context(), client, "updateOrganization",
					"UpdateOrganizationInput", orgAuthPolicyFields, map[string]any{"id": org.ID, field: value})
				if err != nil {
					return err
				}
				var policy organizationAuthPolicy
				if err := json.Unmarshal(result, &policy); err != nil {
					return err
				}
				return writeOrganizationAuthPolicy(cmd, policy)
			},
		},
		newAuthDomainsCommand(),
	)
	return parent
}

func newAuthDomainsCommand() *cobra.Command {
	domains := &cobra.Command{Use: "domains", Short: "Manage trusted sign-in domains for the organization"}
	type domainRow struct {
		ID             string  `json:"id"`
		Domain         string  `json:"domain"`
		DefaultRole    *string `json:"defaultRoleSlug"`
		RequiresReview bool    `json:"requiresReview"`
	}
	const selection = "id domain defaultRoleSlug requiresReview"
	list := &cobra.Command{
		Use: "list", Short: "List trusted domains", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, err := authOrganizationClient(cmd)
			if err != nil {
				return err
			}
			reply := struct {
				Domains []domainRow `json:"astroliftOrganizationAllowlistDomains"`
			}{Domains: []domainRow{}}
			if err := client.GraphQL(cmd.Context(),
				"query { astroliftOrganizationAllowlistDomains { "+selection+" } }", nil, &reply); err != nil {
				return err
			}
			if boolFlag(cmd, "json") {
				return renderJSON(cmd, reply.Domains)
			}
			out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(out, "ID\tDOMAIN\tDEFAULT ROLE\tREVIEW")
			for _, row := range reply.Domains {
				role := ""
				if row.DefaultRole != nil {
					role = *row.DefaultRole
				}
				fmt.Fprintf(out, "%s\t%s\t%s\t%t\n", row.ID, row.Domain, role, row.RequiresReview)
			}
			return out.Flush()
		},
	}
	add := &cobra.Command{
		Use: "add <domain>", Short: "Add a trusted domain", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authOrganizationClient(cmd)
			if err != nil {
				return err
			}
			input := map[string]any{"domain": args[0], "requiresReview": boolFlag(cmd, "requires-review")}
			if role, _ := cmd.Flags().GetString("default-role"); role != "" {
				input["defaultRoleSlug"] = role
			}
			result, err := runAuthMutation(cmd.Context(), client, "addOrganizationAllowlistDomain",
				"AddOrganizationAllowlistDomainInput", selection, input)
			if err != nil {
				return err
			}
			if boolFlag(cmd, "json") {
				return renderJSON(cmd, result)
			}
			var row domainRow
			if err := json.Unmarshal(result, &row); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s (%s)\n", row.Domain, row.ID)
			return err
		},
	}
	add.Flags().String("default-role", "", "Role slug for new members from the domain")
	add.Flags().Bool("requires-review", false, "Require a reviewer before new members get access")
	remove := &cobra.Command{
		Use: "remove <id>", Short: "Remove a trusted domain", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !boolFlag(cmd, "yes") {
				return fmt.Errorf("pass --yes to remove the trusted domain")
			}
			client, _, err := authOrganizationClient(cmd)
			if err != nil {
				return err
			}
			result, err := runAuthMutation(cmd.Context(), client, "removeOrganizationAllowlistDomain",
				"RemoveOrganizationAllowlistDomainInput", "id deleted", map[string]any{"id": args[0]})
			if err != nil {
				return err
			}
			if boolFlag(cmd, "json") {
				return renderJSON(cmd, result)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), args[0])
			return err
		},
	}
	remove.Flags().Bool("yes", false, "Confirm removing the trusted domain")
	domains.AddCommand(list, add, remove)
	return domains
}
