// Package cmd: `astro workflow gates` / `astro workflow gate`.
//
// CLI parity for #1820: today the only way to decide a pending human_gate is
// the raw signalWorkflowInstance mutation, keyed on the gate's integer
// executionId rather than the guid workflowStageExecutions otherwise uses
// everywhere else (#1786's footgun). These two verbs read the org-wide
// pendingHumanGates query (the same one the web "pending gates" list reads,
// and the same authorization: WORKFLOW_TRIGGER, narrowed to the gates the
// caller may decide) so a CLI-side approver never resolves an executionId by
// hand, and never risks sending the wrong id shape.
//
// GraphQL operations (field names per backend/schema.graphql):
//   - pendingHumanGates(limit)                 → [PendingHumanGate]
//   - signalWorkflowInstance(workflowId, ...)   → MutationResult
//
// Issue: calliopeai/astrolift-app#1820
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
	workflowGateRun      string
	workflowGateDecision string
	workflowGateNote     string
)

// ---- GraphQL operations ----------------------------------------------------

const pendingHumanGatesQuery = `query($limit: Int!) {
  pendingHumanGates(limit: $limit) {
    executionId workflowId runGuid definitionSlug definitionName
    stageRole stageApprovers startedAt
  }
}`

// pendingGatesFetchLimit is comfortably above the "8+ pending gates" the
// issue reports for one org today; --json on `gates` is the escape hatch if
// an install ever needs more in one page.
const pendingGatesFetchLimit = 200

// ---- response shapes (GraphQL camelCase) -----------------------------------

// pendingHumanGateRow mirrors PendingHumanGateType. executionId is the
// stage-execution pk as a string, what signalWorkflowInstance's payload
// takes (#1786: a guid is silently dropped); workflowId is the Temporal
// workflow id its own workflowId argument takes; runGuid is the astrolift
// WorkflowRun guid the web observe page's ?run= deep link uses (#2068).
type pendingHumanGateRow struct {
	ExecutionID    string   `json:"executionId"`
	WorkflowID     string   `json:"workflowId"`
	RunGUID        string   `json:"runGuid"`
	DefinitionSlug string   `json:"definitionSlug"`
	DefinitionName string   `json:"definitionName"`
	StageRole      string   `json:"stageRole"`
	StageApprovers []string `json:"stageApprovers"`
	StartedAt      *string  `json:"startedAt"`
}

func fetchPendingHumanGates(ctx context.Context, client *api.Client) ([]pendingHumanGateRow, error) {
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var resp struct {
		Gates []pendingHumanGateRow `json:"pendingHumanGates"`
	}
	if err := client.GraphQL(listCtx, pendingHumanGatesQuery,
		map[string]interface{}{"limit": pendingGatesFetchLimit}, &resp); err != nil {
		return nil, fmt.Errorf("pendingHumanGates: %w", err)
	}
	return resp.Gates, nil
}

// ---- astro workflow gates ---------------------------------------------------

var workflowGatesCmd = &cobra.Command{
	Use:   "gates",
	Short: "List pending human gates across the organization",
	Long: `Lists every open human_gate stage execution across the org's workflow
runs via the pendingHumanGates GraphQL query, the same query the web
"pending gates" list reads, and the same authorization the decide path
applies (WORKFLOW_TRIGGER, narrowed to the gates the caller may decide; a
gate naming specific approver addresses shows only to them).

Newest first. Decide one with ` + "`astro workflow gate <definition-slug> --run <guid> --decision approve|reject`" + `.`,
	Args: cobra.NoArgs,
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runWorkflowGates(cmd, ctx, client)
	}),
}

func runWorkflowGates(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	gates, err := fetchPendingHumanGates(ctx, client)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, gates)
	}
	out := cmd.OutOrStdout()
	if len(gates) == 0 {
		_, _ = fmt.Fprintln(out, "No pending gates.")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "DEFINITION\tROLE\tAPPROVERS\tSTARTED\tRUN")
	for _, g := range gates {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			g.DefinitionSlug, dashIfEmpty(g.StageRole), dashIfEmpty(strings.Join(g.StageApprovers, ", ")),
			shortTime(g.StartedAt), g.RunGUID)
	}
	return w.Flush()
}

// ---- astro workflow gate ----------------------------------------------------

var workflowGateCmd = &cobra.Command{
	Use:   "gate <definition-slug>",
	Short: "Approve or reject a pending human gate",
	Long: `Decides a pending human_gate stage execution via the signalWorkflowInstance
GraphQL mutation. Resolves the gate off the same pendingHumanGates query
` + "`astro workflow gates`" + ` reads (org-scoped, WORKFLOW_TRIGGER), so this
never needs the executionId or the guid/integer distinction #1786 documents;
the CLI resolves executionId itself.

--run <guid> selects a specific run's gate (the run guid from
` + "`astro workflow gates`" + `); by default the newest pending gate for the
definition. --decision accepts approve/approved or reject/rejected.
--note is optional and stored on the decision record.

Who may decide is the server's call, same as everywhere else: a refusal
comes back as a mutation error naming why.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runWorkflowGate(cmd, ctx, client, args[0])
	}),
}

// normalizeGateDecision accepts either tense (the issue text used
// approve/reject; the backend and #2068's UI use approved/rejected) and
// maps to the wire value signalWorkflowInstance's payload requires.
func normalizeGateDecision(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "approve", "approved":
		return "approved", nil
	case "reject", "rejected":
		return "rejected", nil
	default:
		return "", fmt.Errorf("--decision must be approve or reject, got %q", raw)
	}
}

func runWorkflowGate(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	decision, err := normalizeGateDecision(workflowGateDecision)
	if err != nil {
		return err
	}

	gates, err := fetchPendingHumanGates(ctx, client)
	if err != nil {
		return err
	}
	var match *pendingHumanGateRow
	for i := range gates {
		if gates[i].DefinitionSlug != slug {
			continue
		}
		if workflowGateRun != "" && gates[i].RunGUID != workflowGateRun {
			continue
		}
		// pendingHumanGates is newest-first; the first match is the newest
		// pending gate for this definition (or this exact run, with --run).
		match = &gates[i]
		break
	}
	if match == nil {
		if workflowGateRun != "" {
			return fmt.Errorf("no pending human gate for workflow %q on run %q (see `astro workflow gates`)",
				slug, workflowGateRun)
		}
		return fmt.Errorf("no pending human gate for workflow %q (see `astro workflow gates`)", slug)
	}

	mutateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var resp struct {
		Result struct {
			OK     bool             `json:"ok"`
			Errors validationErrors `json:"errors"`
		} `json:"signalWorkflowInstance"`
	}
	vars := map[string]interface{}{
		"workflowId": match.WorkflowID,
		"signalName": "human_gate_decision",
		"payload": map[string]interface{}{
			"execution_id": match.ExecutionID,
			"decision":     decision,
			"note":         workflowGateNote,
		},
	}
	if err := client.GraphQL(mutateCtx, signalWorkflowInstanceMutation, vars, &resp); err != nil {
		return workflowRunControlError("decide", err)
	}
	if !resp.Result.OK {
		return fmt.Errorf("decide failed: %s", firstValidationError(resp.Result.Errors))
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, map[string]interface{}{
			"workflow": slug, "run": match.RunGUID, "decision": decision, "note": workflowGateNote,
		})
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Decision recorded: %s (%s)\n", decision, slug)
	_, _ = fmt.Fprintf(out, "Run:        %s\n", match.RunGUID)
	if workflowGateNote != "" {
		_, _ = fmt.Fprintf(out, "Note:       %s\n", workflowGateNote)
	}
	return nil
}

// signalWorkflowInstanceMutation is shared with a raw-signal escape hatch
// nowhere else in this package yet, but matches the field names
// workflow_run_control.go documents for the control plane's signal path.
const signalWorkflowInstanceMutation = `mutation($workflowId: String!, $signalName: String!, $payload: JSON) {
  signalWorkflowInstance(workflowId: $workflowId, signalName: $signalName, payload: $payload) {
    ok
    errors { field messages }
  }
}`

// ---- init ------------------------------------------------------------------

func init() {
	workflowGateCmd.Flags().StringVar(&workflowGateRun, "run", "", "Run guid to decide (default: the newest pending gate for this workflow)")
	workflowGateCmd.Flags().StringVar(&workflowGateDecision, "decision", "", "approve or reject (required)")
	workflowGateCmd.Flags().StringVar(&workflowGateNote, "note", "", "Note for the decision record (optional)")
	_ = workflowGateCmd.MarkFlagRequired("decision")

	workflowCmd.AddCommand(workflowGatesCmd, workflowGateCmd)
}
