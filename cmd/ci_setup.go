// Package cmd — `astro ci setup <app-slug>` app-onboarding autowire.
//
// Pushes the CI config to an app's source repo by wrapping three backend
// mutations, run in order and stopped on the first failure so a re-run (all
// three are idempotent) can continue where it left off:
//
//  1. installAstroliftSourceWebhook  — install/refresh the push-event webhook
//  2. pushAstroliftCiSecretsToRepo   — push the CI secrets (deploy token, API URL)
//  3. pushAstroliftCiWorkflowToRepo  — push the rendered CI workflow file
//
// Each returns an Astrolift MutationResult envelope ({ ok, errors, data }); the
// errors are MutationError { code, message, field }. The working org travels as
// the X-Astrolift-Organization header via workflowOrgScopedRunE, like the tier-2
// workflow commands.
package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

var (
	ciSetupSkipWebhook  bool
	ciSetupSkipSecrets  bool
	ciSetupSkipWorkflow bool
)

// mutationErrors is the [MutationError!] list the CI-setup envelopes carry; its
// underlying type matches firstMutationError's parameter.
type mutationErrors = []struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field"`
}

// ---- GraphQL operations (field/arg names per backend/schema.graphql) --------

const installSourceWebhookMutation = `mutation($input: InstallSourceWebhookInput!) {
  installAstroliftSourceWebhook(input: $input) {
    ok
    errors { code message field }
    data { status hookId receiverUrl }
  }
}`

const pushCiSecretsMutation = `mutation($input: PushCiSecretsToRepoInput!) {
  pushAstroliftCiSecretsToRepo(input: $input) {
    ok
    errors { code message field }
    data { secretNames rotatedTokenLast4 repo }
  }
}`

const pushCiWorkflowMutation = `mutation($input: PushCiWorkflowToRepoInput!) {
  pushAstroliftCiWorkflowToRepo(input: $input) {
    ok
    errors { code message field }
    data { status commitSha prUrl }
  }
}`

// ---- astro ci setup --------------------------------------------------------

var ciSetupCmd = &cobra.Command{
	Use:   "setup <app-slug>",
	Short: "Autowire an app's source repo: webhook, CI secrets, CI workflow",
	Long: `Pushes the CI config to <app-slug>'s source repo in one pass, wrapping
three idempotent backend mutations in order:

  1. installAstroliftSourceWebhook  — install/refresh the push-event webhook
  2. pushAstroliftCiSecretsToRepo   — push the CI secrets
  3. pushAstroliftCiWorkflowToRepo  — push the rendered CI workflow

The sequence stops on the first failure (a workflow is never pushed if the
webhook failed); what succeeded is printed so a re-run continues. The working
org is sent as the tenant header. --skip-webhook / --skip-secrets /
--skip-workflow run a subset.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runCiSetup(cmd, ctx, client, args[0])
	}),
}

func runCiSetup(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string) error {
	out := cmd.OutOrStdout()
	vars := map[string]interface{}{"input": map[string]interface{}{"appSlug": appSlug}}

	// 1. Webhook -----------------------------------------------------------
	if ciSetupSkipWebhook {
		fmt.Fprintln(out, "Webhook: skipped (--skip-webhook)")
	} else {
		var resp struct {
			Result struct {
				Ok     bool           `json:"ok"`
				Errors mutationErrors `json:"errors"`
				Data   *struct {
					Status      string `json:"status"`
					HookID      string `json:"hookId"`
					ReceiverURL string `json:"receiverUrl"`
				} `json:"data"`
			} `json:"installAstroliftSourceWebhook"`
		}
		if err := ciSetupMutate(ctx, client, installSourceWebhookMutation, vars, &resp); err != nil {
			return fmt.Errorf("installing source webhook: %w", err)
		}
		if !resp.Result.Ok {
			return fmt.Errorf("webhook install failed: %s", firstMutationError(resp.Result.Errors))
		}
		if d := resp.Result.Data; d != nil {
			fmt.Fprintf(out, "Webhook: %s (hook %s)\n", d.Status, d.HookID)
			fmt.Fprintf(out, "  receiver: %s\n", d.ReceiverURL)
		} else {
			fmt.Fprintln(out, "Webhook: installed")
		}
	}

	// 2. Secrets -----------------------------------------------------------
	if ciSetupSkipSecrets {
		fmt.Fprintln(out, "Secrets: skipped (--skip-secrets)")
	} else {
		var resp struct {
			Result struct {
				Ok     bool           `json:"ok"`
				Errors mutationErrors `json:"errors"`
				Data   *struct {
					SecretNames       []string `json:"secretNames"`
					RotatedTokenLast4 string   `json:"rotatedTokenLast4"`
					Repo              string   `json:"repo"`
				} `json:"data"`
			} `json:"pushAstroliftCiSecretsToRepo"`
		}
		if err := ciSetupMutate(ctx, client, pushCiSecretsMutation, vars, &resp); err != nil {
			return fmt.Errorf("pushing CI secrets: %w", err)
		}
		if !resp.Result.Ok {
			return fmt.Errorf("secrets push failed: %s", firstMutationError(resp.Result.Errors))
		}
		if d := resp.Result.Data; d != nil {
			fmt.Fprintf(out, "Secrets: pushed to %s\n", d.Repo)
			fmt.Fprintf(out, "  %s (deploy token ****%s)\n",
				strings.Join(d.SecretNames, ", "), d.RotatedTokenLast4)
		} else {
			fmt.Fprintln(out, "Secrets: pushed")
		}
	}

	// 3. Workflow ----------------------------------------------------------
	if ciSetupSkipWorkflow {
		fmt.Fprintln(out, "Workflow: skipped (--skip-workflow)")
	} else {
		var resp struct {
			Result struct {
				Ok     bool           `json:"ok"`
				Errors mutationErrors `json:"errors"`
				Data   *struct {
					Status    string  `json:"status"`
					CommitSha *string `json:"commitSha"`
					PrURL     *string `json:"prUrl"`
				} `json:"data"`
			} `json:"pushAstroliftCiWorkflowToRepo"`
		}
		if err := ciSetupMutate(ctx, client, pushCiWorkflowMutation, vars, &resp); err != nil {
			return fmt.Errorf("pushing CI workflow: %w", err)
		}
		if !resp.Result.Ok {
			return fmt.Errorf("workflow push failed: %s", firstMutationError(resp.Result.Errors))
		}
		if d := resp.Result.Data; d != nil {
			fmt.Fprintf(out, "Workflow: %s\n", d.Status)
			// The workflow lands as created/updated on the deploy branch
			// (commit_sha), or as a PR if the branch is protected (pr_url).
			if d.PrURL != nil && *d.PrURL != "" {
				fmt.Fprintf(out, "  PR: %s\n", *d.PrURL)
			} else if d.CommitSha != nil && *d.CommitSha != "" {
				fmt.Fprintf(out, "  commit: %s\n", *d.CommitSha)
			}
		} else {
			fmt.Fprintln(out, "Workflow: pushed")
		}
	}

	fmt.Fprintf(out, "\nCI setup complete for %s.\n", appSlug)
	return nil
}

// ciSetupMutate runs one CI-setup mutation with a 60s timeout.
func ciSetupMutate(ctx context.Context, client *api.Client, mutation string, vars map[string]interface{}, target interface{}) error {
	mutCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return client.GraphQL(mutCtx, mutation, vars, target)
}

func init() {
	ciSetupCmd.Flags().BoolVar(&ciSetupSkipWebhook, "skip-webhook", false, "Skip installing the source webhook")
	ciSetupCmd.Flags().BoolVar(&ciSetupSkipSecrets, "skip-secrets", false, "Skip pushing the CI secrets")
	ciSetupCmd.Flags().BoolVar(&ciSetupSkipWorkflow, "skip-workflow", false, "Skip pushing the CI workflow")
	ciCmd.AddCommand(ciSetupCmd)
}
