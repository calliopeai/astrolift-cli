// Package cmd — `astro workflow run-cancel` / `astro workflow run-show`.
//
// Control and inspect a workflow run that is already in flight. `runs`
// answers "what state is it in"; these answer "stop it" and "which stage is
// it sitting on, and is that stage waiting on a human".
//
// Both key on the run's Temporal workflow id, which `workflowRuns` already
// returns — the control plane's instance mutations and its stage-execution
// reader are both keyed that way, not by the tier-3 run guid. There is no
// by-guid run resolver, so both verbs take the workflow slug and select a run
// within it (`--run <guid>`, default the newest).
//
// GraphQL operations (field names per backend/schema.graphql):
//   - workflow(slug)                            → the configured Workflow guid
//   - workflowRuns(workflowId)                  → run guid + temporal ids + state
//   - cancelWorkflowInstance(workflowId)        → MutationResult
//   - terminateWorkflowInstance(workflowId, reason) → MutationResult
//   - workflowStageExecutions(workflowId, runId)    → per-stage rows
//
// Server-side both mutations are gated per run ownership: the elevated
// platform-operator pair reaches every run, otherwise WORKFLOW_TRIGGER scoped
// to the caller's org, with a foreign org's run answered by the same
// not-found envelope as a nonexistent one. A denial arrives as a GraphQL
// error naming the permission, which these commands translate into an
// explicit "not permitted" rather than passing a transport error up.
//
// Issue: calliopeai/astrolift-cli#69
package cmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

var (
	workflowRunCancelRun       string
	workflowRunCancelTerminate bool
	workflowRunCancelReason    string
	workflowRunCancelYes       bool

	workflowRunShowRun string
)

// workflowTriggerPermission is the RBAC permission the control plane requires
// for run control when the caller is not a platform operator. A denial is
// rendered by core.permissions as "<reason>: <permission>", so the value is
// what identifies the failure in an otherwise opaque GraphQL error.
const workflowTriggerPermission = "workflow.trigger"

// ---- GraphQL operations ----------------------------------------------------

// workflowRunsControlQuery is workflowRunsQuery plus temporalRunId, which
// workflowStageExecutions needs as its second key.
const workflowRunsControlQuery = `query($workflowId: ID!) {
  workflowRuns(workflowId: $workflowId) {
    guid currentState temporalWorkflowId temporalRunId startedAt completedAt isCompleted
  }
}`

const cancelWorkflowInstanceMutation = `mutation($workflowId: String!) {
  cancelWorkflowInstance(workflowId: $workflowId) {
    ok
    errors { field messages }
  }
}`

const terminateWorkflowInstanceMutation = `mutation($workflowId: String!, $reason: String!) {
  terminateWorkflowInstance(workflowId: $workflowId, reason: $reason) {
    ok
    errors { field messages }
  }
}`

const workflowStageExecutionsQuery = `query($workflowId: String!, $runId: String!) {
  workflowStageExecutions(workflowId: $workflowId, runId: $runId) {
    stageOrder stageKind stageRole stageApprovers
    status attemptNumber humanGateState humanGateNote
    startedAt endedAt errorMessage
  }
}`

// ---- response shapes (GraphQL camelCase) -----------------------------------

// workflowRunDetail is a run row carrying both Temporal ids. The tier-3
// guid identifies the run to a human; the pair identifies it to the
// control plane's instance mutations and stage reader.
type workflowRunDetail struct {
	configuredWorkflowRun
	TemporalRunID *string `json:"temporalRunId"`
}

// workflowStageExecutionRow mirrors WorkflowStageExecutionType. humanGateState
// is empty for every non-gate stage kind; on a human_gate stage it is pending
// (open, no decision), approved, rejected (a gate that runs out its timeout is
// recorded as a rejection noted "gate timed out"), or closed.
type workflowStageExecutionRow struct {
	StageOrder     int      `json:"stageOrder"`
	StageKind      string   `json:"stageKind"`
	StageRole      string   `json:"stageRole"`
	StageApprovers []string `json:"stageApprovers"`
	Status         string   `json:"status"`
	AttemptNumber  int      `json:"attemptNumber"`
	HumanGateState string   `json:"humanGateState"`
	HumanGateNote  string   `json:"humanGateNote"`
	StartedAt      *string  `json:"startedAt"`
	EndedAt        *string  `json:"endedAt"`
	ErrorMessage   string   `json:"errorMessage"`
}

// workflowRunCancelResult is the --json document run-cancel emits. State is
// what the control plane reported immediately after the request, not a final
// state: a cooperative cancel lands asynchronously.
type workflowRunCancelResult struct {
	Workflow           string  `json:"workflow"`
	Run                string  `json:"run"`
	TemporalWorkflowID string  `json:"temporalWorkflowId"`
	Mode               string  `json:"mode"`
	Reason             string  `json:"reason,omitempty"`
	Requested          bool    `json:"requested"`
	State              string  `json:"state"`
	IsCompleted        bool    `json:"isCompleted"`
	CompletedAt        *string `json:"completedAt"`
}

// workflowRunShowResult is the --json document run-show emits.
type workflowRunShowResult struct {
	Workflow string                      `json:"workflow"`
	Run      workflowRunDetail           `json:"run"`
	Stages   []workflowStageExecutionRow `json:"stages"`
}

// ---- astro workflow run-cancel ---------------------------------------------

var workflowRunCancelCmd = &cobra.Command{
	Use:   "run-cancel <workflow-slug>",
	Short: "Stop an in-flight run of a configured Workflow",
	Long: `Stops a run that has not reached a terminal state, via the
cancelWorkflowInstance GraphQL mutation. The cancel is cooperative: Temporal
signals the workflow, which can run its cleanup before exiting, so a run that
owns external resources tears them down. It lands asynchronously — the state
printed afterwards is what the control plane reported immediately after the
request, not a final state.

--terminate escalates to terminateWorkflowInstance: Temporal kills the run at
once and no cleanup runs. Reserve it for a wedged run the cooperative cancel
cannot unstick. It requires --reason, which is stored on the Temporal record
so the next operator sees why.

By default this acts on the newest run; --run <guid> selects one from
` + "`astro workflow runs <slug>`" + `. A run that is already terminal is
refused before anything is sent.

There is no interactive prompt: --yes is the confirmation (CI-safe).
Without it, nothing is changed.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runWorkflowRunCancel(cmd, ctx, client, args[0])
	}),
}

func runWorkflowRunCancel(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	reason := strings.TrimSpace(workflowRunCancelReason)
	if workflowRunCancelTerminate && reason == "" {
		return fmt.Errorf("--terminate requires --reason (the control plane stores it on the Temporal record)")
	}
	if !workflowRunCancelTerminate && reason != "" {
		return fmt.Errorf("--reason only applies with --terminate")
	}
	mode := "cancel"
	if workflowRunCancelTerminate {
		mode = "terminate"
	}
	if !workflowRunCancelYes {
		return fmt.Errorf("refusing to %s a run of workflow %q without --yes (nothing was changed)", mode, slug)
	}

	wf, err := resolveConfiguredWorkflow(ctx, client, slug)
	if err != nil {
		return err
	}
	run, err := selectWorkflowRun(ctx, client, wf.GUID, workflowRunCancelRun)
	if err != nil {
		return err
	}
	if workflowRunTerminal(run.configuredWorkflowRun) {
		return fmt.Errorf("run %s is already %s — nothing to %s", run.GUID, run.CurrentState, mode)
	}
	temporalID := ""
	if run.TemporalWorkflowID != nil {
		temporalID = *run.TemporalWorkflowID
	}
	if temporalID == "" {
		return fmt.Errorf("run %s has no Temporal workflow id yet — it has not reached the executor", run.GUID)
	}

	mutation := cancelWorkflowInstanceMutation
	field := "cancelWorkflowInstance"
	vars := map[string]interface{}{"workflowId": temporalID}
	if workflowRunCancelTerminate {
		mutation = terminateWorkflowInstanceMutation
		field = "terminateWorkflowInstance"
		vars["reason"] = reason
	}

	mutateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	var resp map[string]struct {
		Ok     bool             `json:"ok"`
		Errors validationErrors `json:"errors"`
	}
	err = client.GraphQL(mutateCtx, mutation, vars, &resp)
	cancel()
	if err != nil {
		return workflowRunControlError(mode, err)
	}
	if result := resp[field]; !result.Ok {
		return fmt.Errorf("%s failed: %s", mode, firstValidationError(result.Errors))
	}

	// Re-read the run so the caller gets the state the control plane reports
	// now rather than the pre-request one. A cooperative cancel usually has
	// not landed yet, which is why the text output says so.
	observed := run
	if fresh, err := selectWorkflowRun(ctx, client, wf.GUID, run.GUID); err == nil {
		observed = fresh
	}

	out := cmd.OutOrStdout()
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, workflowRunCancelResult{
			Workflow:           slug,
			Run:                observed.GUID,
			TemporalWorkflowID: temporalID,
			Mode:               mode,
			Reason:             reason,
			Requested:          true,
			State:              observed.CurrentState,
			IsCompleted:        observed.IsCompleted,
			CompletedAt:        observed.CompletedAt,
		})
	}
	verb := "Cancel"
	if workflowRunCancelTerminate {
		verb = "Terminate"
	}
	fmt.Fprintf(out, "%s requested: run %s (temporal: %s)\n", verb, observed.GUID, temporalID)
	if reason != "" {
		fmt.Fprintf(out, "Reason:            %s\n", reason)
	}
	fmt.Fprintf(out, "State:             %s\n", observed.CurrentState)
	if !workflowRunTerminal(observed.configuredWorkflowRun) {
		fmt.Fprintf(out, "\nThe run has not reached a terminal state yet; %s is delivered asynchronously.\n",
			strings.ToLower(verb))
		fmt.Fprintf(out, "Watch it with `astro workflow runs %s --watch`.\n", slug)
	}
	return nil
}

// ---- astro workflow run-show -----------------------------------------------

var workflowRunShowCmd = &cobra.Command{
	Use:   "run-show <workflow-slug>",
	Short: "Show one run of a configured Workflow, stage by stage",
	Long: `Prints a run's header plus its ordered stage executions via the
workflowStageExecutions GraphQL query: each stage's order, kind, role, status,
attempt, and timings.

A human_gate stage also reports its gate state — pending (open, waiting on a
decision), approved, rejected, or closed — and the approvers the stage
declares, so "waiting on approval, and on whom" is read from the platform
rather than inferred. A gate that runs out its timeout is recorded as a
rejection noted "gate timed out". The gate state describes the stage: a run
cancelled while a gate was open leaves that gate open, so read it against the
run state printed above it.

Deciding a gate is not a CLI operation — approvers act through the platform,
where RBAC applies. This is read-only.

By default this shows the newest run; --run <guid> selects one from
` + "`astro workflow runs <slug>`" + `.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runWorkflowRunShow(cmd, ctx, client, args[0])
	}),
}

func runWorkflowRunShow(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	wf, err := resolveConfiguredWorkflow(ctx, client, slug)
	if err != nil {
		return err
	}
	run, err := selectWorkflowRun(ctx, client, wf.GUID, workflowRunShowRun)
	if err != nil {
		return err
	}

	temporalID, temporalRunID := "", ""
	if run.TemporalWorkflowID != nil {
		temporalID = *run.TemporalWorkflowID
	}
	if run.TemporalRunID != nil {
		temporalRunID = *run.TemporalRunID
	}

	// Both ids key the stage reader. A run that has not been accepted by
	// Temporal yet has no stage rows to read, which is a real state, not an
	// error — report the run and say so.
	stages := []workflowStageExecutionRow{}
	if temporalID != "" && temporalRunID != "" {
		stageCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		var resp struct {
			Stages []workflowStageExecutionRow `json:"workflowStageExecutions"`
		}
		err = client.GraphQL(stageCtx, workflowStageExecutionsQuery,
			map[string]interface{}{"workflowId": temporalID, "runId": temporalRunID}, &resp)
		cancel()
		if err != nil {
			return workflowRunControlError("read stages of", err)
		}
		stages = resp.Stages
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, workflowRunShowResult{Workflow: slug, Run: *run, Stages: stages})
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Workflow:  %s\n", slug)
	fmt.Fprintf(out, "Run:       %s\n", run.GUID)
	fmt.Fprintf(out, "State:     %s\n", run.CurrentState)
	fmt.Fprintf(out, "Started:   %s\n", shortTime(&run.StartedAt))
	fmt.Fprintf(out, "Finished:  %s\n", shortTime(run.CompletedAt))
	if temporalID != "" {
		fmt.Fprintf(out, "Temporal:  %s\n", temporalID)
	}

	fmt.Fprintln(out, "")
	if len(stages) == 0 {
		if temporalID == "" || temporalRunID == "" {
			fmt.Fprintln(out, "No stage detail: the run has no Temporal run id yet.")
			return nil
		}
		fmt.Fprintln(out, "No stages have started yet.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STAGE\tKIND\tROLE\tSTATUS\tATTEMPT\tGATE\tSTARTED\tFINISHED")
	for _, s := range stages {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			s.StageOrder, s.StageKind, dashIfEmpty(s.StageRole), s.Status, s.AttemptNumber,
			dashIfEmpty(s.HumanGateState), shortTime(s.StartedAt), shortTime(s.EndedAt))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	for _, s := range stages {
		if note := gateDetail(s); note != "" {
			fmt.Fprintf(out, "\n  [%d] %s\n", s.StageOrder, note)
		}
		if s.ErrorMessage != "" {
			fmt.Fprintf(out, "\n  [%d] error: %s\n", s.StageOrder, s.ErrorMessage)
		}
	}
	return nil
}

// gateDetail renders the human-readable line for a gate stage: who a pending
// gate waits on, or the note a decided one recorded. Empty for a stage with
// nothing gate-shaped to say.
func gateDetail(s workflowStageExecutionRow) string {
	switch {
	case s.HumanGateState == "pending" && len(s.StageApprovers) > 0:
		return "waiting on approval from " + strings.Join(s.StageApprovers, ", ")
	case s.HumanGateState == "pending":
		return "waiting on approval (the stage declares no approvers)"
	case s.HumanGateState != "" && s.HumanGateNote != "":
		return fmt.Sprintf("gate %s: %s", s.HumanGateState, s.HumanGateNote)
	default:
		return ""
	}
}

// ---- helpers ---------------------------------------------------------------

// selectWorkflowRun resolves one run of a configured Workflow: the run whose
// guid is runGUID, or the newest when runGUID is empty (workflowRuns returns
// them newest-first).
func selectWorkflowRun(ctx context.Context, client *api.Client, workflowGUID, runGUID string) (*workflowRunDetail, error) {
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Runs []workflowRunDetail `json:"workflowRuns"`
	}
	if err := client.GraphQL(listCtx, workflowRunsControlQuery,
		map[string]interface{}{"workflowId": workflowGUID}, &resp); err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}
	if len(resp.Runs) == 0 {
		return nil, fmt.Errorf("workflow has no runs yet")
	}
	if runGUID == "" {
		return &resp.Runs[0], nil
	}
	for i := range resp.Runs {
		if resp.Runs[i].GUID == runGUID {
			return &resp.Runs[i], nil
		}
	}
	return nil, fmt.Errorf("run %q not found on this workflow (see `astro workflow runs`)", runGUID)
}

// workflowRunControlError turns the control plane's RBAC denial into an
// explicit refusal. PermissionDenied surfaces as a GraphQL error ending in
// the permission value rather than as a mutation envelope, so without this a
// caller sees a transport error it has to pattern-match to tell "you may not"
// apart from "the server broke".
func workflowRunControlError(action string, err error) error {
	if strings.Contains(err.Error(), workflowTriggerPermission) {
		return fmt.Errorf("not permitted to %s this run: the control plane requires %s in this organization (%w)",
			action, workflowTriggerPermission, err)
	}
	return fmt.Errorf("%s run: %w", action, err)
}

// ---- init ------------------------------------------------------------------

func init() {
	workflowRunCancelCmd.Flags().StringVar(&workflowRunCancelRun, "run", "", "Run guid to stop (default: the newest run)")
	workflowRunCancelCmd.Flags().BoolVar(&workflowRunCancelTerminate, "terminate", false, "Hard-kill the run instead of cancelling it cooperatively (requires --reason)")
	workflowRunCancelCmd.Flags().StringVar(&workflowRunCancelReason, "reason", "", "Why the run is being terminated (required with --terminate)")
	workflowRunCancelCmd.Flags().BoolVarP(&workflowRunCancelYes, "yes", "y", false, "Confirm stopping the run (required to proceed)")

	workflowRunShowCmd.Flags().StringVar(&workflowRunShowRun, "run", "", "Run guid to show (default: the newest run)")

	workflowCmd.AddCommand(workflowRunCancelCmd, workflowRunShowCmd)
}
