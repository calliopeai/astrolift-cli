package cmd

import (
	"context"
	"fmt"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const enableWorkflowDefinitionMutation = `mutation($slug: String!) {
  updateWorkflowDefinition(slug: $slug, isEnabled: true) { ok errors { field messages } }
}`

var workflowDefinitionEnableCmd = &cobra.Command{
	Use:   "definition-enable <slug>",
	Short: "Enable a reviewed organization workflow definition",
	Long: `Enables an organization-owned workflow definition through the platform's
permission-checked update API. Imports start disabled so they can be reviewed
with 'workflow definition <slug>' before activation. Global definitions are
read-only. This command does not start a run.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runWorkflowDefinitionEnable(cmd, ctx, client, args[0])
	}),
}

func enableWorkflowDefinition(ctx context.Context, client *api.Client, slug string) error {
	if err := execWorkflowMutationResult(ctx, client, enableWorkflowDefinitionMutation, "updateWorkflowDefinition", slug); err != nil {
		return fmt.Errorf("enabling definition %q: %w", slug, err)
	}
	return nil
}

func runWorkflowDefinitionEnable(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	if err := enableWorkflowDefinition(ctx, client, slug); err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, map[string]interface{}{"slug": slug, "isEnabled": true})
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Enabled workflow definition: %s\n", slug)
	return err
}

func init() { workflowCmd.AddCommand(workflowDefinitionEnableCmd) }
