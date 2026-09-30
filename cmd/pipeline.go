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
	"encoding/json"
	"fmt"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
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

// resolvePipelineID maps a pipeline name or guid to its guid. The pipeline
// mutations (triggerPipelineRun) and the runs query key on the pipeline's
// guid — the astrolift-app resolvers filter on `pipeline__guid` — so a
// human-supplied name is resolved here by listing the org's pipelines and
// matching on either field.
func resolvePipelineID(ctx context.Context, client *api.Client, nameOrID string) (string, error) {
	query := `query FindPipeline($limit: Int!) { astroliftPipelines(limit: $limit) { id name } }`
	var resp struct {
		Pipelines []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"astroliftPipelines"`
	}
	if err := client.GraphQL(ctx, query, map[string]interface{}{"limit": 200}, &resp); err != nil {
		return "", fmt.Errorf("looking up pipeline: %w", err)
	}
	for _, p := range resp.Pipelines {
		if p.ID == nameOrID || p.Name == nameOrID {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("pipeline %q not found", nameOrID)
}

// ---- list ------------------------------------------------------------------

var (
	pipelineListLimit int
	pipelineListJSON  bool
)

var pipelineListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pipelines for the current org",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client, _, _, err := loadActiveClient(ctx, false)
		if err != nil {
			return err
		}
		return runPipelineList(cmd, ctx, client)
	},
}

func runPipelineList(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	// astroliftPipelines(limit: Int! = 100) — limit is a non-null arg.
	query := `
query ListPipelines($limit: Int!) {
  astroliftPipelines(limit: $limit) {
    id name repoUrl defaultBranch tomlPath createdAt
  }
}`
	var resp struct {
		Pipelines []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			RepoURL       string `json:"repoUrl"`
			DefaultBranch string `json:"defaultBranch"`
			TomlPath      string `json:"tomlPath"`
			CreatedAt     string `json:"createdAt"`
		} `json:"astroliftPipelines"`
	}
	if err := client.GraphQL(ctx, query, map[string]interface{}{"limit": pipelineListLimit}, &resp); err != nil {
		return fmt.Errorf("listing pipelines: %w", err)
	}

	if pipelineListJSON {
		b, _ := json.MarshalIndent(resp.Pipelines, "", "  ")
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		return nil
	}

	if len(resp.Pipelines) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No pipelines found. Create one with `astro pipeline` or register a TOML file.")
		return nil
	}

	for _, p := range resp.Pipelines {
		fmt.Fprintf(cmd.OutOrStdout(), "  %-30s  %s (branch: %s)\n", p.Name, p.RepoURL, p.DefaultBranch)
	}
	return nil
}

// ---- run -------------------------------------------------------------------

var pipelineRunBranch string

var pipelineRunCmd = &cobra.Command{
	Use:   "run <pipeline-name>",
	Short: "Trigger a manual pipeline run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client, _, _, err := loadActiveClient(ctx, false)
		if err != nil {
			return err
		}
		return runPipelineRun(cmd, ctx, client, args[0])
	},
}

func runPipelineRun(cmd *cobra.Command, ctx context.Context, client *api.Client, pipelineName string) error {
	pipelineID, err := resolvePipelineID(ctx, client, pipelineName)
	if err != nil {
		return err
	}

	// triggerPipelineRun(pipelineId: GUID!, ref: String = null) — positional
	// args, not an input object. There is no TriggerPipelineRunInput type.
	mutation := `
mutation TriggerPipelineRun($pipelineId: GUID!, $ref: String) {
  triggerPipelineRun(pipelineId: $pipelineId, ref: $ref) {
    ok
    errors { code message }
    data { id runNumber status }
  }
}`
	vars := map[string]interface{}{"pipelineId": pipelineID}
	if pipelineRunBranch != "" {
		vars["ref"] = pipelineRunBranch
	}

	var mutResp struct {
		TriggerPipelineRun struct {
			Ok     bool `json:"ok"`
			Errors []struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"errors"`
			Data *struct {
				ID        string `json:"id"`
				RunNumber int    `json:"runNumber"`
				Status    string `json:"status"`
			} `json:"data"`
		} `json:"triggerPipelineRun"`
	}
	if err := client.GraphQL(ctx, mutation, vars, &mutResp); err != nil {
		return fmt.Errorf("triggering run: %w", err)
	}

	if !mutResp.TriggerPipelineRun.Ok {
		for _, e := range mutResp.TriggerPipelineRun.Errors {
			fmt.Fprintf(cmd.ErrOrStderr(), "error: %s: %s\n", e.Code, e.Message)
		}
		return fmt.Errorf("trigger failed")
	}

	run := mutResp.TriggerPipelineRun.Data
	fmt.Fprintf(cmd.OutOrStdout(), "Triggered run #%d (id: %s) — status: %s\n", run.RunNumber, run.ID, run.Status)
	return nil
}

// ---- runs ------------------------------------------------------------------

var (
	pipelineRunsLimit    int
	pipelineRunsPipeline string
)

var pipelineRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List pipeline runs",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client, _, _, err := loadActiveClient(ctx, false)
		if err != nil {
			return err
		}
		return runPipelineRuns(cmd, ctx, client)
	},
}

func runPipelineRuns(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	// astroliftPipelineRuns(pipelineId: String!, limit: Int! = 50) — pipelineId
	// is required, so --pipeline is mandatory. Accept a name or guid.
	pipelineID, err := resolvePipelineID(ctx, client, pipelineRunsPipeline)
	if err != nil {
		return err
	}

	query := `
query ListPipelineRuns($pipelineId: String!, $limit: Int!) {
  astroliftPipelineRuns(pipelineId: $pipelineId, limit: $limit) {
    id runNumber status triggerKind triggerRef startedAt finishedAt
  }
}`
	vars := map[string]interface{}{
		"pipelineId": pipelineID,
		"limit":      pipelineRunsLimit,
	}

	var resp struct {
		Runs []struct {
			ID          string  `json:"id"`
			RunNumber   int     `json:"runNumber"`
			Status      string  `json:"status"`
			TriggerKind string  `json:"triggerKind"`
			TriggerRef  string  `json:"triggerRef"`
			StartedAt   *string `json:"startedAt"`
			FinishedAt  *string `json:"finishedAt"`
		} `json:"astroliftPipelineRuns"`
	}
	if err := client.GraphQL(ctx, query, vars, &resp); err != nil {
		return fmt.Errorf("listing runs: %w", err)
	}

	if len(resp.Runs) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No pipeline runs found.")
		return nil
	}

	for _, r := range resp.Runs {
		duration := "pending"
		if r.StartedAt != nil && r.FinishedAt != nil {
			t1, _ := time.Parse(time.RFC3339, *r.StartedAt)
			t2, _ := time.Parse(time.RFC3339, *r.FinishedAt)
			duration = fmt.Sprintf("%ds", int(t2.Sub(t1).Seconds()))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  #%-4d  %-10s  %-12s  %s  (%s)\n",
			r.RunNumber, r.Status, r.TriggerKind, r.TriggerRef, duration)
	}
	return nil
}

// ---- cancel ----------------------------------------------------------------

var pipelineCancelCmd = &cobra.Command{
	Use:   "cancel <run-id>",
	Short: "Cancel a running pipeline run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client, _, _, err := loadActiveClient(ctx, false)
		if err != nil {
			return err
		}
		return runPipelineCancel(cmd, ctx, client, args[0])
	},
}

func runPipelineCancel(cmd *cobra.Command, ctx context.Context, client *api.Client, runID string) error {
	// cancelPipelineRun(runId: GUID!) — positional runId arg.
	mutation := `
mutation CancelPipelineRun($runId: GUID!) {
  cancelPipelineRun(runId: $runId) {
    ok
    errors { code message }
  }
}`
	var resp struct {
		Cancel struct {
			Ok     bool `json:"ok"`
			Errors []struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"errors"`
		} `json:"cancelPipelineRun"`
	}
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"runId": runID}, &resp); err != nil {
		return fmt.Errorf("cancelling run: %w", err)
	}

	if !resp.Cancel.Ok {
		for _, e := range resp.Cancel.Errors {
			fmt.Fprintf(cmd.ErrOrStderr(), "error: %s: %s\n", e.Code, e.Message)
		}
		return fmt.Errorf("cancel failed")
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Run %s cancelled.\n", runID)
	return nil
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
