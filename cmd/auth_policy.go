package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const authPolicyFields = `key group kind value default editable warning helpText readOnlyReason choices`

type authPolicy struct {
	Key      string   `json:"key"`
	Group    string   `json:"group"`
	Kind     string   `json:"kind"`
	Value    string   `json:"value"`
	Default  string   `json:"default"`
	Editable bool     `json:"editable"`
	Warning  string   `json:"warning"`
	HelpText string   `json:"helpText"`
	ReadOnly string   `json:"readOnlyReason"`
	Choices  []string `json:"choices"`
}

func listAuthPolicy(cmd *cobra.Command, client *api.Client) ([]authPolicy, error) {
	var reply struct {
		Settings []authPolicy `json:"astroliftPlatformSettings"`
	}
	query := `query { astroliftPlatformSettings { ` + authPolicyFields + ` } }`
	if err := client.GraphQL(cmd.Context(), query, nil, &reply); err != nil {
		return nil, err
	}
	settings := make([]authPolicy, 0)
	for _, setting := range reply.Settings {
		switch setting.Group {
		case "Password sign-in", "Sessions", "Step-up auth", "Device attestation", "Deploy tokens":
			settings = append(settings, setting)
		}
	}
	return settings, nil
}

func runAuthPolicy(cmd *cobra.Command, args []string) error {
	client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	settings, err := listAuthPolicy(cmd, client)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if cmd.Name() == "list" {
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, settings)
		}
		table := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(table, "SETTING\tVALUE\tDEFAULT\tGROUP")
		for _, setting := range settings {
			value := setting.Value
			if setting.ReadOnly != "" && value == "" {
				value = "(read only)"
			}
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", setting.Key, value, setting.Default, setting.Group)
		}
		return table.Flush()
	}

	var policy *authPolicy
	for i := range settings {
		if settings[i].Key == args[0] {
			policy = &settings[i]
			break
		}
	}
	if policy == nil {
		return fmt.Errorf("unknown authentication policy %q", args[0])
	}
	if !policy.Editable {
		return fmt.Errorf("%s: %s", policy.Key, policy.ReadOnly)
	}
	confirmed := boolFlag(cmd, "yes")
	if policy.Warning != "" && !confirmed {
		return fmt.Errorf("%s Pass --yes to confirm", policy.Warning)
	}

	field, declaration, arguments := "resetPlatformSetting", "$key: String!, $confirmed: Boolean!", "key: $key, confirmed: $confirmed"
	variables := map[string]any{"key": policy.Key, "confirmed": confirmed}
	if cmd.Name() == "set" {
		field = "setPlatformSetting"
		declaration += ", $value: String!"
		arguments += ", value: $value"
		variables["value"] = args[1]
	}
	query := fmt.Sprintf(`mutation(%s) { %s(%s) { ok errors { code message field } data { setting { %s } } } }`,
		declaration, field, arguments, authPolicyFields)
	var reply map[string]struct {
		OK     bool            `json:"ok"`
		Errors []mutationError `json:"errors"`
		Data   *struct {
			Setting authPolicy `json:"setting"`
		} `json:"data"`
	}
	if err := client.GraphQL(cmd.Context(), query, variables, &reply); err != nil {
		return err
	}
	change := reply[field]
	if !change.OK {
		return fmt.Errorf("%s: %s", policy.Key, firstDeployError(change.Errors))
	}
	if change.Data == nil || change.Data.Setting.Key != policy.Key {
		return fmt.Errorf("the server did not return the updated policy %s", policy.Key)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, change.Data.Setting)
	}
	_, err = fmt.Fprintf(out, "%s = %s\n", policy.Key, change.Data.Setting.Value)
	return err
}

func newAuthPolicyCommand() *cobra.Command {
	parent := &cobra.Command{Use: "policy", Short: "Manage install-wide authentication settings"}
	list := &cobra.Command{Use: "list", Short: "List password, session, step-up, device, and token policies", Args: cobra.NoArgs, RunE: runAuthPolicy}
	set := &cobra.Command{Use: "set <key> <value>", Short: "Change an authentication policy", Args: cobra.ExactArgs(2), RunE: runAuthPolicy}
	reset := &cobra.Command{Use: "reset <key>", Short: "Restore an authentication policy's default", Args: cobra.ExactArgs(1), RunE: runAuthPolicy}
	set.Flags().Bool("yes", false, "Confirm a policy change that requires confirmation")
	reset.Flags().Bool("yes", false, "Confirm a reset that requires confirmation")
	parent.AddCommand(list, set, reset, newOrganizationAuthPolicyCommand())
	return parent
}

func init() {
	authCmd.AddCommand(newAuthPolicyCommand())
}
