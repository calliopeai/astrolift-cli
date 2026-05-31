// Package cmd — `astro agent ...` subcommand tree.
//
// Provides terminal-side access to the agent dispatch layer:
//   - run     — launch a WorkflowInstance from a WorkflowDefinition slug
//   - ls      — list Tasks / WorkflowInstances
//   - logs    — stream or fetch logs for a Task
//   - cancel  — cancel a Task
//   - inspect — print the full Task record
//
// All commands use the DRF REST API surface under /api/cli/v1/workflows/
// and /api/cli/v1/tasks/, which is only available when
// astrolift_agent_dispatch is installed on the target server.
//
// Issue: calliopeai/astrolift#61
package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ---- top-level group -------------------------------------------------------

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Interact with the agent dispatch layer",
	Long: `Commands for running and monitoring agent tasks via the
Astrolift dispatch layer.

Requires astrolift_agent_dispatch to be installed on the target server.
All commands authenticate via the active server credentials
(see 'astro auth login') or ASTROLIFT_DEPLOY_TOKEN.`,
}

// ---- flags -----------------------------------------------------------------

var (
	agentRunInput      string
	agentRunDispatcher string
	agentRunWait       bool

	agentListStatus     string
	agentListWorkflow   string
	agentListDispatcher string
	agentListLimit      int
	agentListJSON       bool

	agentLogsFollow bool
	agentLogsTail   int

	agentCancelYes bool

	agentInspectJSON bool
)

// ---- response shapes -------------------------------------------------------

type workflowRunResp struct {
	InstanceID string `json:"instance_id"`
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	PollingURL string `json:"polling_url"`
}

type taskRow struct {
	ID           string `json:"id"`
	ShortID      string `json:"short_id"`
	Workflow     string `json:"workflow"`
	Stage        string `json:"stage"`
	Status       string `json:"status"`
	AgentVariant string `json:"agent_variant"`
	Dispatcher   string `json:"dispatcher"`
	Duration     string `json:"duration"`
}

type tasksListResp struct {
	Results []taskRow `json:"results"`
	Count   int       `json:"count"`
}

type taskDetail struct {
	ID           string                   `json:"id"`
	ShortID      string                   `json:"short_id"`
	Workflow     string                   `json:"workflow"`
	Status       string                   `json:"status"`
	Stage        string                   `json:"stage"`
	AgentVariant string                   `json:"agent_variant"`
	Dispatcher   string                   `json:"dispatcher"`
	StartedAt    string                   `json:"started_at"`
	EndedAt      string                   `json:"ended_at"`
	NoVNCURL     string                   `json:"novnc_url,omitempty"`
	Metering     map[string]interface{}   `json:"metering,omitempty"`
	Stages       []map[string]interface{} `json:"stage_chain,omitempty"`
	Brief        string                   `json:"brief_key,omitempty"`
}

// ---- astro agent run -------------------------------------------------------

var agentRunCmd = &cobra.Command{
	Use:   "run <workflow-slug>",
	Short: "Launch a WorkflowInstance from a WorkflowDefinition slug",
	Long: `Posts to /api/cli/v1/workflows/<slug>/run/ and either:
  * prints the WorkflowInstance ID and first Task ID then returns (default)
  * blocks until the WorkflowInstance reaches a terminal state and streams
    stage transitions to stdout (--wait)

The --input flag accepts a JSON string or a @filename to read from a file.
The --dispatcher flag targets a specific DispatcherInstance; omit to let
the platform auto-route to the best available dispatcher.

Exit codes: 0 success, 1 dispatch failure, 2 config error.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, _ := cmd.Flags().GetBool("debug")
		client, _, _, err := loadActiveClient(cmd.Context(), debug)
		if err != nil {
			return err
		}

		workflowSlug := args[0]

		body := map[string]interface{}{}
		if agentRunInput != "" {
			raw := agentRunInput
			if strings.HasPrefix(raw, "@") {
				data, err := os.ReadFile(raw[1:])
				if err != nil {
					return fmt.Errorf("reading input file: %w", err)
				}
				raw = string(data)
			}
			var payload interface{}
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				return fmt.Errorf("--input is not valid JSON: %w", err)
			}
			body["input"] = payload
		}
		if agentRunDispatcher != "" {
			body["dispatcher"] = agentRunDispatcher
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
		defer cancel()

		var resp workflowRunResp
		path := fmt.Sprintf("/api/cli/v1/workflows/%s/run/", workflowSlug)
		if err := client.Post(ctx, path, body, &resp); err != nil {
			return fmt.Errorf("dispatching workflow: %w", err)
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "WorkflowInstance: %s\n", resp.InstanceID)
		fmt.Fprintf(out, "Task ID:          %s\n", resp.TaskID)
		fmt.Fprintf(out, "Status:           %s\n", resp.Status)

		if !agentRunWait {
			return nil
		}

		// --wait: poll for terminal state and surface status transitions
		fmt.Fprintln(out, "Waiting for terminal state...")
		pollCtx, pollCancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
		defer pollCancel()

		last := resp.Status
		for {
			select {
			case <-pollCtx.Done():
				return fmt.Errorf("timed out waiting for terminal state (last status: %s)", last)
			case <-time.After(5 * time.Second):
			}

			var detail taskDetail
			taskPath := fmt.Sprintf("/api/cli/v1/tasks/%s/", resp.TaskID)
			if err := client.Get(pollCtx, taskPath, &detail); err != nil {
				return fmt.Errorf("polling task: %w", err)
			}
			if detail.Status != last {
				fmt.Fprintf(out, "  → %s\n", detail.Status)
				last = detail.Status
			}
			switch detail.Status {
			case "completed", "succeeded", "failed", "cancelled", "timed_out":
				fmt.Fprintf(out, "Final status: %s\n", detail.Status)
				if detail.Status == "failed" {
					return fmt.Errorf("task ended in %q", detail.Status)
				}
				return nil
			}
		}
	},
}

// ---- astro agent ls --------------------------------------------------------

var agentListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List Tasks / WorkflowInstances",
	Long: `Lists Tasks for the current org/team context.

Defaults to showing running and queued tasks. Use --status to filter.
Output is a table unless --json is given.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, _ := cmd.Flags().GetBool("debug")
		client, _, _, err := loadActiveClient(cmd.Context(), debug)
		if err != nil {
			return err
		}

		params := []string{}
		if agentListStatus != "" {
			params = append(params, "status="+agentListStatus)
		} else {
			// Default: running + queued
			params = append(params, "status=running,queued")
		}
		if agentListWorkflow != "" {
			params = append(params, "workflow="+agentListWorkflow)
		}
		if agentListDispatcher != "" {
			params = append(params, "dispatcher="+agentListDispatcher)
		}
		params = append(params, fmt.Sprintf("limit=%d", agentListLimit))

		path := "/api/cli/v1/tasks/?" + strings.Join(params, "&")

		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		var resp tasksListResp
		if err := client.Get(ctx, path, &resp); err != nil {
			return fmt.Errorf("listing tasks: %w", err)
		}

		out := cmd.OutOrStdout()
		if agentListJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(resp.Results)
		}

		if len(resp.Results) == 0 {
			fmt.Fprintln(out, "No tasks found.")
			return nil
		}

		fmt.Fprintf(out, "%-12s  %-20s  %-14s  %-14s  %-14s  %-20s  %s\n",
			"TASK ID", "WORKFLOW", "STAGE", "STATUS", "VARIANT", "DISPATCHER", "DURATION",
		)
		fmt.Fprintln(out, strings.Repeat("-", 108))
		for _, t := range resp.Results {
			id := t.ShortID
			if id == "" {
				id = t.ID
				if len(id) > 12 {
					id = id[:12]
				}
			}
			fmt.Fprintf(out, "%-12s  %-20s  %-14s  %-14s  %-14s  %-20s  %s\n",
				id, agentTruncate(t.Workflow, 20), agentTruncate(t.Stage, 14),
				t.Status, agentTruncate(t.AgentVariant, 14),
				agentTruncate(t.Dispatcher, 20), t.Duration,
			)
		}
		fmt.Fprintf(out, "\n%d task(s) shown.\n", len(resp.Results))
		return nil
	},
}

// ---- astro agent logs ------------------------------------------------------

var agentLogsCmd = &cobra.Command{
	Use:   "logs <task-id>",
	Short: "Stream or fetch logs for a Task",
	Long: `Streams live logs for a running Task (-f / --follow) using the SSE
endpoint, or fetches the last N lines for a completed task (--tail <n>).

The stream exits automatically when the Task reaches a terminal state.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, _ := cmd.Flags().GetBool("debug")
		client, _, _, err := loadActiveClient(cmd.Context(), debug)
		if err != nil {
			return err
		}

		taskID := args[0]

		if agentLogsFollow {
			// SSE stream — no timeout; exits on terminal state or ctrl-c
			path := fmt.Sprintf("/api/dispatch/v1/tasks/%s/logs/stream", taskID)
			stream, err := client.Stream(cmd.Context(), path)
			if err != nil {
				return fmt.Errorf("opening log stream: %w", err)
			}
			defer stream.Close()

			out := cmd.OutOrStdout()
			scanner := bufio.NewScanner(stream)
			for scanner.Scan() {
				line := scanner.Text()
				// SSE lines arrive as "data: <payload>" or "event: <type>"
				switch {
				case strings.HasPrefix(line, "data: "):
					fmt.Fprintln(out, strings.TrimPrefix(line, "data: "))
				case strings.HasPrefix(line, "event: done"),
					strings.HasPrefix(line, "event: terminal"):
					// Server signals task has reached terminal state
					return nil
				}
			}
			return scanner.Err()
		}

		// Non-follow: fetch tail lines
		path := fmt.Sprintf("/api/cli/v1/tasks/%s/logs/?tail=%d", taskID, agentLogsTail)
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		var resp struct {
			Lines []string `json:"lines"`
		}
		if err := client.Get(ctx, path, &resp); err != nil {
			return fmt.Errorf("fetching logs: %w", err)
		}

		out := cmd.OutOrStdout()
		for _, line := range resp.Lines {
			fmt.Fprintln(out, line)
		}
		return nil
	},
}

// ---- astro agent cancel ----------------------------------------------------

var agentCancelCmd = &cobra.Command{
	Use:   "cancel <task-id>",
	Short: "Cancel a running or queued Task",
	Long: `Cancels a Task in DRAFT, QUEUED, or PROVISIONING state.
For RUNNING tasks, sends a stop signal to the Dispatch Service.

Prompts for confirmation unless --yes is given.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, _ := cmd.Flags().GetBool("debug")
		client, _, _, err := loadActiveClient(cmd.Context(), debug)
		if err != nil {
			return err
		}

		taskID := args[0]

		if !agentCancelYes {
			noPrompt, _ := cmd.Root().PersistentFlags().GetBool("no-prompt")
			if !noPrompt {
				fmt.Fprintf(cmd.OutOrStdout(), "Cancel task %s? [y/N] ", taskID)
				var answer string
				fmt.Fscan(cmd.InOrStdin(), &answer)
				if strings.ToLower(strings.TrimSpace(answer)) != "y" {
					fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
					return nil
				}
			}
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		path := fmt.Sprintf("/api/cli/v1/tasks/%s/cancel/", taskID)
		if err := client.Post(ctx, path, nil, nil); err != nil {
			return fmt.Errorf("cancelling task: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Task %s cancelled.\n", taskID)
		return nil
	},
}

// ---- astro agent inspect ---------------------------------------------------

var agentInspectCmd = &cobra.Command{
	Use:   "inspect <task-id>",
	Short: "Print the full Task record",
	Long: `Prints the full Task record: status, Brief key, AgentDefinition,
DispatcherInstance, stage execution chain, metering summary (if terminal),
and noVNC URL (if RUNNING + VNC variant).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, _ := cmd.Flags().GetBool("debug")
		client, _, _, err := loadActiveClient(cmd.Context(), debug)
		if err != nil {
			return err
		}

		taskID := args[0]
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		path := fmt.Sprintf("/api/cli/v1/tasks/%s/", taskID)
		var detail taskDetail
		if err := client.Get(ctx, path, &detail); err != nil {
			return fmt.Errorf("fetching task: %w", err)
		}

		out := cmd.OutOrStdout()

		if agentInspectJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(detail)
		}

		fmt.Fprintf(out, "Task ID:       %s\n", detail.ID)
		fmt.Fprintf(out, "Workflow:      %s\n", detail.Workflow)
		fmt.Fprintf(out, "Status:        %s\n", detail.Status)
		fmt.Fprintf(out, "Stage:         %s\n", detail.Stage)
		fmt.Fprintf(out, "Agent variant: %s\n", detail.AgentVariant)
		fmt.Fprintf(out, "Dispatcher:    %s\n", detail.Dispatcher)
		if detail.Brief != "" {
			fmt.Fprintf(out, "Brief key:     %s\n", detail.Brief)
		}
		if detail.StartedAt != "" {
			fmt.Fprintf(out, "Started at:    %s\n", detail.StartedAt)
		}
		if detail.EndedAt != "" {
			fmt.Fprintf(out, "Ended at:      %s\n", detail.EndedAt)
		}
		if detail.NoVNCURL != "" {
			fmt.Fprintf(out, "noVNC URL:     %s\n", detail.NoVNCURL)
		}

		if len(detail.Stages) > 0 {
			fmt.Fprintln(out, "\nStage chain:")
			for i, s := range detail.Stages {
				name, _ := s["name"].(string)
				status, _ := s["status"].(string)
				fmt.Fprintf(out, "  %d. %-30s %s\n", i+1, name, status)
			}
		}

		if len(detail.Metering) > 0 {
			fmt.Fprintln(out, "\nMetering:")
			for k, v := range detail.Metering {
				fmt.Fprintf(out, "  %-20s %v\n", k+":", v)
			}
		}
		return nil
	},
}

// ---- helpers ---------------------------------------------------------------

// agentTruncate caps s at n runes, appending "…" if truncated.
func agentTruncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string(runes[:n-1]) + "…"
}

// ---- init ------------------------------------------------------------------

func init() {
	// run
	agentRunCmd.Flags().StringVar(&agentRunInput, "input", "", "Workflow input as a JSON string or @file.json")
	agentRunCmd.Flags().StringVar(&agentRunDispatcher, "dispatcher", "", "Target a specific DispatcherInstance slug (default: auto-route)")
	agentRunCmd.Flags().BoolVar(&agentRunWait, "wait", false, "Block until WorkflowInstance reaches terminal state")

	// ls
	agentListCmd.Flags().StringVar(&agentListStatus, "status", "", "Filter by status: running|queued|completed|failed (default: running+queued)")
	agentListCmd.Flags().StringVar(&agentListWorkflow, "workflow", "", "Filter by workflow slug")
	agentListCmd.Flags().StringVar(&agentListDispatcher, "dispatcher", "", "Filter by dispatcher slug")
	agentListCmd.Flags().IntVar(&agentListLimit, "limit", 20, "Maximum number of tasks to return")
	agentListCmd.Flags().BoolVar(&agentListJSON, "json", false, "Output as JSON")

	// logs
	agentLogsCmd.Flags().BoolVarP(&agentLogsFollow, "follow", "f", false, "Stream live logs (exits on terminal state)")
	agentLogsCmd.Flags().IntVar(&agentLogsTail, "tail", 100, "Number of lines to fetch for completed tasks")

	// cancel
	agentCancelCmd.Flags().BoolVarP(&agentCancelYes, "yes", "y", false, "Skip confirmation prompt")

	// inspect
	agentInspectCmd.Flags().BoolVar(&agentInspectJSON, "json", false, "Output the full task record as JSON")

	agentCmd.AddCommand(agentRunCmd, agentListCmd, agentLogsCmd, agentCancelCmd, agentInspectCmd)
	rootCmd.AddCommand(agentCmd)
}
