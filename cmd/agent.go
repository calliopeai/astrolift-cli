// Package cmd — `astro agent ...` subcommand tree.
//
// Provides terminal-side access to the agent dispatch layer:
//   - run     — launch an agent WorkflowDefinition's stages via Temporal
//   - ls      — list the org's AgentTasks
//   - logs    — stream or fetch logs for a Task (see note below)
//   - cancel  — cancel an AgentTask
//   - inspect — print the full AgentTask record
//
// These commands talk to the control-plane GraphQL API via
// client.GraphQL (the same surface org.go / agent_envspec.go use), not a
// REST surface — the earlier `/api/cli/v1/workflows/` and
// `/api/cli/v1/tasks/` routes were never built and 404 to the SPA shell.
//
// GraphQL operations (field names per backend/schema.graphql):
//   - run     → runWorkflowDefinition(workflowSlug, triggerPayload) → { ok, errors{field, messages}, workflowRunId, temporalWorkflowId }
//   - ls      → agentTasks(orgId, status) → [AstroliftAgentTask]
//   - inspect → agentTask(id) → AstroliftAgentTask
//   - cancel  → cancelTask(id) → { ok, errors{code, message} }
//
// --wait polls astroliftWorkflowInstance(workflowId) for the returned
// temporalWorkflowId until its status is terminal.
//
// `logs` has no GraphQL or operator-facing REST surface: the only log
// route on the platform is the dispatcher-facing ingest endpoint
// (/api/dispatch/v1/tasks/<id>/logs/, a worker push). It is left wired to
// the old paths and is UNVERIFIED — see the command's comment.
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
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// ---- top-level group -------------------------------------------------------

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Interact with the agent dispatch layer",
	Long: `Commands for running and monitoring agent tasks via the
Astrolift control-plane GraphQL API.

All commands authenticate via the active server credentials
(see 'astro auth login') or ASTROLIFT_DEPLOY_TOKEN.`,
}

// ---- flags -----------------------------------------------------------------

var (
	agentRunInput string
	agentRunWait  bool

	agentListStatus string
	agentListLimit  int
	agentListJSON   bool

	agentLogsFollow bool
	agentLogsTail   int

	agentCancelYes bool

	agentInspectJSON bool
)

// ---- GraphQL operations ----------------------------------------------------

// runWorkflowMutation launches an agent WorkflowDefinition's stages durably
// via Temporal. The resolver takes only workflowSlug + triggerPayload (there
// is no dispatcher arg — routing is platform-side), and returns the
// WorkflowRun mirror pk plus the Temporal workflow id.
const runWorkflowMutation = `mutation($workflowSlug: String!, $triggerPayload: JSON) {
  runWorkflowDefinition(workflowSlug: $workflowSlug, triggerPayload: $triggerPayload) {
    ok
    errors { field messages }
    workflowRunId
    temporalWorkflowId
  }
}`

// workflowInstanceQuery is the cheap single-instance status poll used by
// --wait. It is keyed by the Temporal workflow id (an exact describe, not a
// visibility LIKE), so it resolves on standard SQL visibility.
const workflowInstanceQuery = `query($workflowId: String!) {
  astroliftWorkflowInstance(workflowId: $workflowId) {
    workflowId
    status
  }
}`

const agentTasksQuery = `query($orgId: ID!, $status: String) {
  agentTasks(orgId: $orgId, status: $status) {
    id
    status
    callbackUrl
    result
    createdAt
    startedAt
    finishedAt
    vncEnabled
    vncUrl
    snapshotUrl
  }
}`

const agentTaskQuery = `query($id: ID!) {
  agentTask(id: $id) {
    id
    status
    callbackUrl
    result
    createdAt
    startedAt
    finishedAt
    vncEnabled
    vncUrl
    snapshotUrl
  }
}`

const cancelTaskMutation = `mutation($id: ID!) {
  cancelTask(id: $id) {
    ok
    errors { code message field }
  }
}`

// ---- response shapes (GraphQL camelCase) -----------------------------------

type runWorkflowResult struct {
	Ok     bool `json:"ok"`
	Errors []struct {
		Field    string   `json:"field"`
		Messages []string `json:"messages"`
	} `json:"errors"`
	WorkflowRunID      string `json:"workflowRunId"`
	TemporalWorkflowID string `json:"temporalWorkflowId"`
}

// workflowInstance is the AstroliftWorkflowInstance status summary (--wait).
type workflowInstance struct {
	WorkflowID string `json:"workflowId"`
	Status     string `json:"status"`
}

// agentTask mirrors the AstroliftAgentTask GraphQL type.
type agentTask struct {
	ID          string      `json:"id"`
	Status      string      `json:"status"`
	CallbackURL string      `json:"callbackUrl"`
	Result      interface{} `json:"result"`
	CreatedAt   string      `json:"createdAt"`
	StartedAt   *string     `json:"startedAt"`
	FinishedAt  *string     `json:"finishedAt"`
	VNCEnabled  bool        `json:"vncEnabled"`
	VNCURL      string      `json:"vncUrl"`
	SnapshotURL *string     `json:"snapshotUrl"`
}

// noneMutationResult is the NoneTypeMutationResult envelope (cancelTask). Its
// errors carry a `message` (MutationError), not the `messages` list that the
// ValidationError-based results use.
type noneMutationResult struct {
	Ok     bool `json:"ok"`
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field"`
	} `json:"errors"`
}

// terminalWorkflowStatuses are the statuses --wait treats as final. Temporal
// reports closed workflows as COMPLETED/FAILED/CANCELED/TERMINATED/TIMED_OUT
// (compared case-insensitively).
var terminalWorkflowStatuses = map[string]bool{
	"completed":  true,
	"failed":     true,
	"canceled":   true,
	"cancelled":  true,
	"terminated": true,
	"timed_out":  true,
	"timedout":   true,
}

// ---- astro agent run -------------------------------------------------------

var agentRunCmd = &cobra.Command{
	Use:   "run <workflow-slug>",
	Short: "Launch an agent WorkflowDefinition's stages via Temporal",
	Long: `Calls the runWorkflowDefinition GraphQL mutation, which creates the
WorkflowInstance + WorkflowRun mirror rows and enqueues the durable stage
executor. Prints the WorkflowRun id and Temporal workflow id.

The --input flag accepts a JSON string or a @filename to read from a file;
it is passed through as the workflow's triggerPayload.

With --wait, blocks until the Temporal workflow reaches a terminal state,
polling astroliftWorkflowInstance and surfacing status transitions.

Exit codes: 0 success, 1 dispatch/run failure.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAgentRun(cmd, cmd.Context(), client, cfg, args[0])
	},
}

func runAgentRun(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, workflowSlug string) error {
	vars := map[string]interface{}{"workflowSlug": workflowSlug}
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
		vars["triggerPayload"] = payload
	}

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var resp struct {
		Result runWorkflowResult `json:"runWorkflowDefinition"`
	}
	if err := client.GraphQL(runCtx, runWorkflowMutation, vars, &resp); err != nil {
		return fmt.Errorf("running workflow: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("run failed: %s", firstValidationError(resp.Result.Errors))
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "WorkflowRun ID:    %s\n", resp.Result.WorkflowRunID)
	fmt.Fprintf(out, "Temporal workflow: %s\n", resp.Result.TemporalWorkflowID)

	if !agentRunWait {
		return nil
	}

	workflowID := resp.Result.TemporalWorkflowID
	if workflowID == "" {
		return fmt.Errorf("cannot --wait: server returned no temporalWorkflowId")
	}

	fmt.Fprintln(out, "Waiting for terminal state...")
	pollCtx, pollCancel := context.WithTimeout(ctx, 30*time.Minute)
	defer pollCancel()

	last := ""
	for {
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("timed out waiting for terminal state (last status: %s)", last)
		case <-time.After(5 * time.Second):
		}

		var pollResp struct {
			Instance *workflowInstance `json:"astroliftWorkflowInstance"`
		}
		if err := client.GraphQL(pollCtx, workflowInstanceQuery,
			map[string]interface{}{"workflowId": workflowID}, &pollResp); err != nil {
			return fmt.Errorf("polling workflow instance: %w", err)
		}
		// describe can momentarily return null before the workflow is visible;
		// keep polling rather than treating it as terminal.
		if pollResp.Instance == nil {
			continue
		}
		status := pollResp.Instance.Status
		if status != last {
			fmt.Fprintf(out, "  → %s\n", status)
			last = status
		}
		if terminalWorkflowStatuses[strings.ToLower(status)] {
			fmt.Fprintf(out, "Final status: %s\n", status)
			if !strings.EqualFold(status, "completed") {
				return fmt.Errorf("workflow ended in %q", status)
			}
			return nil
		}
	}
}

// ---- astro agent ls --------------------------------------------------------

var agentListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List the org's AgentTasks",
	Long: `Lists AgentTasks for the working org (newest first) via the
agentTasks GraphQL query. Use --status to filter to a single status
(e.g. running, queued, completed, failed). Output is a table unless
--json is given.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAgentList(cmd, cmd.Context(), client, cfg)
	},
}

func runAgentList(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config) error {
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}

	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	vars := map[string]interface{}{"orgId": org.ID}
	if agentListStatus != "" {
		vars["status"] = agentListStatus
	}

	var resp struct {
		AgentTasks []agentTask `json:"agentTasks"`
	}
	if err := client.GraphQL(listCtx, agentTasksQuery, vars, &resp); err != nil {
		return fmt.Errorf("listing tasks: %w", err)
	}

	tasks := resp.AgentTasks
	// The resolver caps at 200 and has no limit arg; apply --limit client-side.
	if agentListLimit > 0 && len(tasks) > agentListLimit {
		tasks = tasks[:agentListLimit]
	}

	out := cmd.OutOrStdout()
	if agentListJSON {
		return renderJSON(cmd, tasks)
	}

	if len(tasks) == 0 {
		fmt.Fprintln(out, "No tasks found.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TASK ID\tSTATUS\tVNC\tCREATED\tSTARTED\tFINISHED")
	for _, t := range tasks {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			t.ID, t.Status, yesNo(t.VNCEnabled),
			shortTime(&t.CreatedAt), shortTime(t.StartedAt), shortTime(t.FinishedAt),
		)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d task(s) shown.\n", len(tasks))
	return nil
}

// ---- astro agent logs ------------------------------------------------------

var agentLogsCmd = &cobra.Command{
	Use:   "logs <task-id>",
	Short: "Stream or fetch logs for a Task (unverified surface)",
	Long: `Streams live logs for a running Task (-f / --follow), or fetches the
last N lines for a completed task (--tail <n>).

NOTE: the platform exposes no GraphQL or operator-facing REST log surface.
The only log route is the dispatcher-facing ingest endpoint
(/api/dispatch/v1/tasks/<id>/logs/, a worker push). The paths below are
UNVERIFIED and likely 404 until an operator-facing log read API exists.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, _ := cmd.Flags().GetBool("debug")
		client, _, _, err := loadActiveClient(cmd.Context(), debug)
		if err != nil {
			return err
		}

		taskID := args[0]

		// UNVERIFIED: no operator-facing log read surface exists on the
		// control plane today (the only /api/dispatch/v1/.../logs/ route is a
		// worker-facing ingest/push endpoint). Left wired to the historical
		// paths so the command compiles and is ready to point at a real log
		// API when one ships; expect a 404 until then.
		if agentLogsFollow {
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
				switch {
				case strings.HasPrefix(line, "data: "):
					fmt.Fprintln(out, strings.TrimPrefix(line, "data: "))
				case strings.HasPrefix(line, "event: done"),
					strings.HasPrefix(line, "event: terminal"):
					return nil
				}
			}
			return scanner.Err()
		}

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
	Short: "Cancel an AgentTask",
	Long: `Cancels an AgentTask via the cancelTask GraphQL mutation. Only
DRAFT / QUEUED / PROVISIONING tasks cancel directly; a RUNNING task needs a
stop signal to the Dispatcher and is rejected as a precondition failure.

Prompts for confirmation unless --yes is given.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAgentCancel(cmd, cmd.Context(), client, cfg, args[0])
	},
}

func runAgentCancel(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, taskID string) error {
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

	cancelCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Result noneMutationResult `json:"cancelTask"`
	}
	if err := client.GraphQL(cancelCtx, cancelTaskMutation,
		map[string]interface{}{"id": taskID}, &resp); err != nil {
		return fmt.Errorf("cancelling task: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("cancel failed: %s", firstMutationError(resp.Result.Errors))
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Task %s cancelled.\n", taskID)
	return nil
}

// ---- astro agent inspect ---------------------------------------------------

var agentInspectCmd = &cobra.Command{
	Use:   "inspect <task-id>",
	Short: "Print the full AgentTask record",
	Long: `Fetches one AgentTask by GUID via the agentTask GraphQL query and
prints its record: status, timestamps, VNC relay path, and terminal result
payload (if any). Use --json for the raw record.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAgentInspect(cmd, cmd.Context(), client, cfg, args[0])
	},
}

func runAgentInspect(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, taskID string) error {
	inspectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		AgentTask *agentTask `json:"agentTask"`
	}
	if err := client.GraphQL(inspectCtx, agentTaskQuery,
		map[string]interface{}{"id": taskID}, &resp); err != nil {
		return fmt.Errorf("fetching task: %w", err)
	}
	if resp.AgentTask == nil {
		return fmt.Errorf("task %s not found", taskID)
	}
	t := resp.AgentTask

	out := cmd.OutOrStdout()
	if agentInspectJSON {
		return renderJSON(cmd, t)
	}

	fmt.Fprintf(out, "Task ID:       %s\n", t.ID)
	fmt.Fprintf(out, "Status:        %s\n", t.Status)
	fmt.Fprintf(out, "VNC enabled:   %s\n", yesNo(t.VNCEnabled))
	if t.CreatedAt != "" {
		fmt.Fprintf(out, "Created at:    %s\n", t.CreatedAt)
	}
	if t.StartedAt != nil && *t.StartedAt != "" {
		fmt.Fprintf(out, "Started at:    %s\n", *t.StartedAt)
	}
	if t.FinishedAt != nil && *t.FinishedAt != "" {
		fmt.Fprintf(out, "Finished at:   %s\n", *t.FinishedAt)
	}
	if t.CallbackURL != "" {
		fmt.Fprintf(out, "Callback URL:  %s\n", t.CallbackURL)
	}
	if t.VNCURL != "" {
		fmt.Fprintf(out, "VNC URL:       %s\n", t.VNCURL)
	}
	if t.SnapshotURL != nil && *t.SnapshotURL != "" {
		fmt.Fprintf(out, "Snapshot URL:  %s\n", *t.SnapshotURL)
	}
	if t.Result != nil {
		enc, err := json.MarshalIndent(t.Result, "", "  ")
		if err == nil {
			fmt.Fprintf(out, "\nResult:\n%s\n", enc)
		}
	}
	return nil
}

// ---- helpers ---------------------------------------------------------------

// firstValidationError renders the first {field, messages} error for display.
func firstValidationError(errs []struct {
	Field    string   `json:"field"`
	Messages []string `json:"messages"`
}) string {
	if len(errs) == 0 {
		return "unknown error"
	}
	e := errs[0]
	msg := strings.Join(e.Messages, "; ")
	if e.Field != "" {
		return fmt.Sprintf("%s: %s", e.Field, msg)
	}
	return msg
}

// firstMutationError renders the first MutationError {code, message} for display.
func firstMutationError(errs []struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field"`
}) string {
	if len(errs) == 0 {
		return "unknown error"
	}
	return errs[0].Message
}

// yesNo renders a bool as a compact yes/no.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// shortTime trims an ISO-8601 timestamp to its minute, dropping the timezone
// suffix, for a compact table column. Empty/nil → "-".
func shortTime(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	v := *s
	if i := strings.IndexByte(v, '.'); i >= 0 {
		return v[:i]
	}
	// Trim an explicit offset / Z if there are no sub-seconds.
	if i := strings.IndexByte(v, '+'); i >= 0 {
		return v[:i]
	}
	if strings.HasSuffix(v, "Z") {
		return strings.TrimSuffix(v, "Z")
	}
	return v
}

// ---- init ------------------------------------------------------------------

func init() {
	// run
	agentRunCmd.Flags().StringVar(&agentRunInput, "input", "", "Workflow trigger payload as a JSON string or @file.json")
	agentRunCmd.Flags().BoolVar(&agentRunWait, "wait", false, "Block until the workflow reaches a terminal state")

	// ls
	agentListCmd.Flags().StringVar(&agentListStatus, "status", "", "Filter by a single status (e.g. running|queued|completed|failed)")
	agentListCmd.Flags().IntVar(&agentListLimit, "limit", 20, "Maximum number of tasks to show")
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
