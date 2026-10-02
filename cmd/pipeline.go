// Package cmd — `astro pipeline ...` subcommand tree (#102).
//
// Provides CLI access to the Astrolift Pipelines surface:
//   - list    — list pipelines for the current org
//   - run     — trigger a manual pipeline run
//   - runs    — list pipeline runs
//   - cancel  — cancel a running pipeline run
//   - logs    — stream logs for a pipeline run (stub)
//
// Issue: calliopeai/astrolift#102
package cmd

import (
	"context"
	"fmt"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// ---- top-level group -------------------------------------------------------

var pipelineCmd = &cobra.Command{
	Use:   "pipeline",
	Short: "Manage Astrolift Pipelines (CI/CD)",
	Long: `List, trigger, monitor, and cancel pipeline runs.

Pipelines are declared as TOML files in your source repository and
triggered by webhooks, schedules, or manual dispatch.`,
}

// resolvePipelineID walks the server cursor catalogue and rejects ambiguous
// names. Exact GUIDs never depend on a capped list or name substitution.
func resolvePipelineID(ctx context.Context, client *api.Client, selector string) (string, error) {
	if _, err := uuid.Parse(selector); err == nil {
		return selector, nil
	}
	query := `query FindPipeline($search: String!, $limit: Int!, $after: String) { astroliftPipelinesPage(search: $search, limit: $limit, after: $after) { items { id name } nextCursor } }`
	var after *string
	seen := map[string]bool{}
	found := ""
	for page := 0; page < 1000; page++ {
		var resp struct {
			Page *struct {
				Items []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"items"`
				NextCursor *string `json:"nextCursor"`
			} `json:"astroliftPipelinesPage"`
		}
		if err := client.GraphQL(ctx, query, map[string]interface{}{"search": selector, "limit": 50, "after": after}, &resp); err != nil {
			return "", fmt.Errorf("looking up pipeline: %w", err)
		}
		if resp.Page == nil {
			return "", fmt.Errorf("pipeline catalogue was not returned")
		}
		for _, p := range resp.Page.Items {
			if p.Name == selector {
				if found != "" && found != p.ID {
					return "", fmt.Errorf("pipeline name %q is ambiguous; select its exact GUID", selector)
				}
				found = p.ID
			}
		}
		if resp.Page.NextCursor == nil {
			if found == "" {
				return "", fmt.Errorf("pipeline %q not found", selector)
			}
			if _, err := uuid.Parse(found); err != nil {
				return "", fmt.Errorf("pipeline catalogue returned an invalid GUID")
			}
			return found, nil
		}
		if *resp.Page.NextCursor == "" || seen[*resp.Page.NextCursor] {
			return "", fmt.Errorf("pipeline catalogue cursor repeated; selection remains unconfirmed")
		}
		seen[*resp.Page.NextCursor] = true
		after = resp.Page.NextCursor
	}
	return "", fmt.Errorf("pipeline catalogue exceeded the traversal limit; use an exact GUID")
}

// ---- list ------------------------------------------------------------------

var (
	pipelineListLimit int
	pipelineListJSON  bool
)

var pipelineListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pipelines for the current org",
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runPipelineList(cmd, ctx, client)
	}),
}

func runPipelineList(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	return listReviewedPipelines(cmd, ctx, client)
}

// ---- run -------------------------------------------------------------------

var pipelineRunBranch string

var pipelineRunCmd = &cobra.Command{
	Use:   "run <pipeline-name-or-guid>",
	Short: "Start a reviewed pipeline with a persistent recovery request",
	Args:  cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runPipelineRun(cmd, ctx, client, args[0])
	}),
}

func runPipelineRun(cmd *cobra.Command, ctx context.Context, client *api.Client, pipelineName string) error {
	return reviewedPipelineStart(cmd, ctx, client, pipelineName)
}

// ---- runs ------------------------------------------------------------------

var (
	pipelineRunsLimit    int
	pipelineRunsPipeline string
)

var pipelineRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List pipeline runs",
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runPipelineRuns(cmd, ctx, client)
	}),
}

func runPipelineRuns(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	return listReviewedPipelineRuns(cmd, ctx, client)
}

// ---- cancel ----------------------------------------------------------------

var pipelineCancelCmd = &cobra.Command{
	Use:   "cancel <run-id>",
	Short: "Request cancellation of an exact reviewed engine run",
	Args:  cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runPipelineCancel(cmd, ctx, client, args[0])
	}),
}

func runPipelineCancel(cmd *cobra.Command, ctx context.Context, client *api.Client, runID string) error {
	return reviewedPipelineCancel(cmd, ctx, client, runID)
}

// ---- logs ------------------------------------------------------------------

var pipelineLogsCmd = &cobra.Command{
	Use:   "logs <run-id>",
	Short: "Stream logs for a pipeline run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return notImplemented(cmd,
			"pipeline logs — log streaming requires the backend to expose "+
				"AstroliftStepRun.logExcerpt via GraphQL (the model field exists "+
				"but is not surfaced); use the web UI Logs tab until it is")
	},
}

// ---- init ------------------------------------------------------------------

func init() {
	pipelineListCmd.Flags().IntVar(&pipelineListLimit, "limit", 50, "Maximum number of pipelines to list")
	pipelineListCmd.Flags().BoolVar(&pipelineListJSON, "json", false, "Output as JSON")

	pipelineRunCmd.Flags().StringVar(&pipelineRunBranch, "branch", "", "Branch to run (default: pipeline's default_branch)")

	pipelineRunsCmd.Flags().IntVar(&pipelineRunsLimit, "limit", 20, "Maximum number of runs to show")
	pipelineRunsCmd.Flags().StringVar(&pipelineRunsPipeline, "pipeline", "", "Pipeline name or ID whose runs to list (required)")
	_ = pipelineRunsCmd.MarkFlagRequired("pipeline")

	pipelineCmd.AddCommand(
		pipelineListCmd,
		pipelineRunCmd,
		pipelineRunsCmd,
		pipelineCancelCmd,
		pipelineLogsCmd,
	)
	rootCmd.AddCommand(pipelineCmd)
}
