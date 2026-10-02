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
//   - run     → reviewed startWorkflowDefinition and actor-scoped request recovery
//   - ls      → agentTasks(orgId, status) → [AstroliftAgentTask]
//   - inspect → agentTask(id) → AstroliftAgentTask
//   - cancel  → cancelTask(id) → { ok, errors{code, message} }
//   - logs    → agentTaskLogs(id, tail) → [String!]
//
// --wait reads the exact execution GUID and recorded engine IDs until closure.
//
// `logs` reads the task's pod logs via the agentTaskLogs query (tenant-
// scoped server-side). There is no SSE log stream on the control plane,
// so --follow polls the query on an interval and prints newly-appended
// lines rather than holding a long-lived connection.
//
// Issue: calliopeai/astrolift#61
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
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

const agentTasksQuery = `query($orgId: ID!, $status: String) {
  agentTasks(orgId: $orgId, status: $status) {
    id
    status
    callbackUrl
    callbackStatus
    callbackAttempts
    callbackLastError
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
    callbackStatus
    callbackAttempts
    callbackLastError
    result
    failureMessage
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

// agentTaskLogsQuery fetches the last N stdout/stderr lines for a task's
// pod. Tenant-scoped server-side (the active session's org), so it takes
// only the task id + a tail count. Returns a flat list of message lines;
// an empty list means "no logs yet" (or the task has no readable pod),
// never an error.
const agentTaskLogsQuery = `query($id: ID!, $tail: Int!) {
  agentTaskLogs(id: $id, tail: $tail)
}`

// ---- response shapes (GraphQL camelCase) -----------------------------------

// agentTask mirrors the AstroliftAgentTask GraphQL type.
type agentTask struct {
	StartupDiagnostic *startupDiagnostic `json:"startupDiagnostic,omitempty"`
	ID                string             `json:"id"`
	Status            string             `json:"status"`
	CallbackURL       string             `json:"callbackUrl"`
	CallbackStatus    *string            `json:"callbackStatus"`
	CallbackAttempts  *int               `json:"callbackAttempts"`
	CallbackLastError *string            `json:"callbackLastError"`
	Result            interface{}        `json:"result"`
	FailureMessage    *string            `json:"failureMessage,omitempty"`
	CreatedAt         string             `json:"createdAt"`
	StartedAt         *string            `json:"startedAt"`
	FinishedAt        *string            `json:"finishedAt"`
	VNCEnabled        bool               `json:"vncEnabled"`
	VNCURL            string             `json:"vncUrl"`
	SnapshotURL       *string            `json:"snapshotUrl"`
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
	Use:   "run <definition-guid>",
	Short: "Start an agent workflow through the reviewed Definition contract",
	Long: `Alias for workflow definition-start. Review an exact definition GUID, provide
--yes and retain --request-file for recovery. Inputs use --inputs-file; legacy
--input @file.json selects the same file. Literal inputs on argv are refused.
An existing request file performs read-only recovery without resubmitting inputs.

--wait observes the exact execution and recorded Temporal workflow/run IDs until
closure. It keeps closure and task cleanup distinct and never follows a newer run.
Use agent task run for an AgentTask, or workflow run for a configured workflow.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runAgentRun(cmd, ctx, client, nil, args[0])
	}),
}

func runAgentRun(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, id string) error {
	return runReviewedAgentDefinition(cmd, ctx, client, id)
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
	if err := queryAgentCallbackState(listCtx, client, agentTasksQuery, vars, &resp); err != nil {
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
	Short: "Fetch (or poll) the logs for an agent Task",
	Long: `Fetches the last N stdout/stderr lines for a Task's pod via the
agentTaskLogs GraphQL query (--tail <n>, default 100).

The control plane has no SSE log stream, so --follow (-f) instead polls
the query on a fixed interval and prints only newly-appended lines until
interrupted (Ctrl-C). An empty result means the task has produced no logs
yet (or has no readable pod); it is not an error.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, err := loadScopedAgentClient(cmd)
		if err != nil {
			return err
		}
		return runAgentLogs(cmd, cmd.Context(), client, args[0])
	},
}

// agentLogsPollIntervalForTest is the cadence --follow re-queries
// agentTaskLogs. The control plane exposes no push stream; this is a
// deliberate poll. It is a var (not a const) so tests can shorten it.
var agentLogsPollIntervalForTest = 3 * time.Second

func runAgentLogs(cmd *cobra.Command, ctx context.Context, client *api.Client, taskID string) error {
	out := cmd.OutOrStdout()

	fetch := func(fetchCtx context.Context) ([]string, error) {
		var resp struct {
			Lines []string `json:"agentTaskLogs"`
		}
		if err := client.GraphQL(fetchCtx, agentTaskLogsQuery,
			map[string]interface{}{"id": taskID, "tail": agentLogsTail}, &resp); err != nil {
			return nil, err
		}
		return resp.Lines, nil
	}

	if !agentLogsFollow {
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		lines, err := fetch(fetchCtx)
		if err != nil {
			return fmt.Errorf("fetching logs: %w", err)
		}
		if len(lines) == 0 {
			// Silence and exit 0 is indistinguishable from "the agent
			// printed nothing", and the moment an operator needs this
			// most is a failed task (#1712). Say that nothing came back
			// and where to look next; the control plane logs which of
			// the several empty cases it actually took.
			fmt.Fprintf(cmd.ErrOrStderr(),
				"no log lines returned for task %s.\n"+
					"The agent may have printed nothing, or its pod may be gone — an agent Job\n"+
					"is garbage-collected an hour after it settles, taking the pod's logs with it.\n"+
					"`astro agent inspect %s` shows the task's recorded result and failure.\n",
				taskID, taskID)
		}
		for _, line := range lines {
			fmt.Fprintln(out, line)
		}
		return nil
	}

	var tail agentLogTail
	for {
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		lines, err := fetch(fetchCtx)
		cancel()
		if err != nil {
			// A transient error mid-follow shouldn't kill the session; the
			// context being done is the real exit signal, handled below.
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("polling logs: %w", err)
		}
		for _, line := range tail.append(lines) {
			fmt.Fprintln(out, line)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(agentLogsPollIntervalForTest):
		}
	}
}

// ---- astro agent cancel ----------------------------------------------------

var agentCancelCmd = &cobra.Command{
	Use:   "cancel <task-id>",
	Short: "Cancel an AgentTask",
	Long: `Cancels an AgentTask via the cancelTask GraphQL mutation. For a
RUNNING or PROVISIONING Kubernetes task, the control plane deletes the Job and
its pod before recording the task as CANCELLED.

Prompts for confirmation unless --yes is given.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := loadScopedAgentClient(cmd)
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
			if _, err := fmt.Fscan(cmd.InOrStdin(), &answer); err != nil {
				return fmt.Errorf("reading confirmation: %w", err)
			}
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
		client, cfg, err := loadScopedAgentClient(cmd)
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
	if err := queryAgentCallbackState(inspectCtx, client, agentTaskQuery,
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
	if t.CallbackStatus != nil {
		fmt.Fprintf(out, "Callback:      %s\n", *t.CallbackStatus)
		if t.CallbackAttempts != nil {
			fmt.Fprintf(out, "Attempts:      %d\n", *t.CallbackAttempts)
		}
		if t.CallbackLastError != nil && *t.CallbackLastError != "" {
			fmt.Fprintf(out, "Callback error: %s\n", *t.CallbackLastError)
		}
	}
	if diagnostic := t.StartupDiagnostic.summary(); diagnostic != "" {
		fmt.Fprintf(out, "Startup:       %s\n", diagnostic)
	}
	if t.FailureMessage != nil && strings.TrimSpace(*t.FailureMessage) != "" {
		if _, err := fmt.Fprintf(out, "Failure:       %s\n", *t.FailureMessage); err != nil {
			return err
		}
	}
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
	agentRunCmd.Flags().StringVar(&agentRunInput, "input", "", "Legacy @file.json alias for --inputs-file; literal JSON is refused")
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
