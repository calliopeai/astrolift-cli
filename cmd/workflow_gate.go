package cmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const pendingHumanGatesQuery = `query PendingHumanGates($limit: Int!, $after: String) {
 pendingHumanGatesPage(limit: $limit, after: $after) {
  items { executionGuid stageGuid runGuid definitionSlug definitionName stageRole stageApprovers startedAt
   temporalExecution { namespace workflowId runId } }
  nextCursor
 }
}`
const humanGateFields = `executionGuid stageExecutionGuid stageGuid stageStatus runStatus requestState
 requestedDecision recordedDecision note decidedByMe observationError temporalExecution { namespace workflowId runId }`
const humanGateStatusQuery = `query HumanGateDecision($executionId: ID!, $stageExecutionId: ID!) {
 humanGateDecision(executionId: $executionId, stageExecutionId: $stageExecutionId) { ` + humanGateFields + ` }
}`
const humanGateDecisionMutation = `mutation DecideHumanGate($executionId: ID!, $stageExecutionId: ID!, $temporalRunId: String!, $decision: String!, $confirmed: Boolean!, $note: String!) {
 decideHumanGate(executionId: $executionId, stageExecutionId: $stageExecutionId, temporalRunId: $temporalRunId,
  decision: $decision, confirmed: $confirmed, note: $note) {
   ok errors { field messages } gate { ` + humanGateFields + ` }
 }
}`

type gateTemporalIdentity struct {
	Namespace  string `json:"namespace"`
	WorkflowID string `json:"workflowId"`
	RunID      string `json:"runId"`
}
type pendingHumanGateRow struct {
	ExecutionGUID     string                `json:"executionGuid"`
	StageGUID         string                `json:"stageGuid"`
	RunGUID           string                `json:"runGuid"`
	DefinitionSlug    string                `json:"definitionSlug"`
	DefinitionName    string                `json:"definitionName"`
	StageRole         string                `json:"stageRole"`
	StageApprovers    []string              `json:"stageApprovers"`
	StartedAt         *string               `json:"startedAt"`
	TemporalExecution *gateTemporalIdentity `json:"temporalExecution"`
}
type humanGateState struct {
	ExecutionGUID      string                `json:"executionGuid"`
	StageExecutionGUID string                `json:"stageExecutionGuid"`
	StageGUID          string                `json:"stageGuid"`
	TemporalExecution  *gateTemporalIdentity `json:"temporalExecution"`
	StageStatus        string                `json:"stageStatus"`
	RunStatus          string                `json:"runStatus"`
	RequestState       string                `json:"requestState"`
	RequestedDecision  *string               `json:"requestedDecision"`
	RecordedDecision   *string               `json:"recordedDecision"`
	Note               string                `json:"note"`
	DecidedByMe        *bool                 `json:"decidedByMe"`
	ObservationError   string                `json:"observationError"`
}
type gateTarget struct {
	RunGUID            string `json:"runGuid"`
	StageExecutionGUID string `json:"stageExecutionGuid"`
	TemporalRunID      string `json:"temporalRunId,omitempty"`
}
type gateReview struct {
	gateTarget
	Decision  string
	Note      string
	Confirmed bool
}
type humanGateReceipt struct {
	OK          bool             `json:"ok"`
	Errors      validationErrors `json:"errors"`
	Gate        *humanGateState  `json:"gate"`
	Target      gateTarget       `json:"target"`
	ClientError string           `json:"clientError,omitempty"`
}

func fetchPendingHumanGates(ctx context.Context, client *api.Client) ([]pendingHumanGateRow, error) {
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows := []pendingHumanGateRow{}
	var cursor *string
	seen := map[string]bool{}
	for {
		var resp struct {
			Page *struct {
				Items      []pendingHumanGateRow `json:"items"`
				NextCursor *string               `json:"nextCursor"`
			} `json:"pendingHumanGatesPage"`
		}
		if err := client.GraphQL(listCtx, pendingHumanGatesQuery,
			map[string]interface{}{"limit": 200, "after": cursor}, &resp); err != nil {
			return nil, fmt.Errorf("pendingHumanGatesPage: %w; this install must support recoverable human gates", err)
		}
		if resp.Page == nil {
			return nil, fmt.Errorf("server did not return pendingHumanGatesPage")
		}
		rows = append(rows, resp.Page.Items...)
		cursor = resp.Page.NextCursor
		if cursor == nil {
			return rows, nil
		}
		if *cursor == "" || seen[*cursor] {
			return nil, fmt.Errorf("server repeated an invalid pending-gate cursor; refresh the list")
		}
		seen[*cursor] = true
	}
}

func newWorkflowGatesCmd() *cobra.Command {
	return &cobra.Command{Use: "gates", Short: "List eligible pending human gates with exact recovery identities",
		Long: `Read every pendingHumanGatesPage, including empty pages carrying a next cursor.
Closed parent runs are excluded. The server applies workflow trigger authority and
named-approver checks. Role/team approver references retain native trigger-authority
fallback. Inspect the run, gate GUID and captured Temporal identity before deciding.
Legacy unbound gates remain visible but cannot use the recoverable decision API.`,
		Args: cobra.NoArgs, RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
			return runWorkflowGates(cmd, ctx, client)
		})}
}
func runWorkflowGates(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	rows, err := fetchPendingHumanGates(ctx, client)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No pending gates.")
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DEFINITION\tROLE\tAPPROVERS\tRUN\tGATE\tTEMPORAL RUN")
	for _, row := range rows {
		runID := "unbound"
		if row.TemporalExecution != nil {
			runID = row.TemporalExecution.RunID
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", row.DefinitionSlug, dashIfEmpty(row.StageRole),
			dashIfEmpty(strings.Join(row.StageApprovers, ", ")), row.RunGUID, row.ExecutionGUID, runID)
	}
	return w.Flush()
}

func gateTargetFlags(cmd *cobra.Command) gateTarget {
	run, _ := cmd.Flags().GetString("run")
	stage, _ := cmd.Flags().GetString("stage")
	temporal, _ := cmd.Flags().GetString("temporal-run")
	return gateTarget{RunGUID: run, StageExecutionGUID: stage, TemporalRunID: temporal}
}
func validateGateTarget(target gateTarget) error {
	if !definitionGUIDValid(target.RunGUID) || !definitionGUIDValid(target.StageExecutionGUID) {
		return fmt.Errorf("--run and --stage must be exact canonical GUIDs from workflow gates; newest-gate selection is not supported")
	}
	return nil
}
func validateGateReview(review gateReview) error {
	if err := validateGateTarget(review.gateTarget); err != nil {
		return err
	}
	if !definitionGUIDValid(review.TemporalRunID) {
		return fmt.Errorf("--temporal-run must be the captured runId reviewed for this gate")
	}
	if !review.Confirmed {
		return fmt.Errorf("--yes is required to confirm the user's explicit decision")
	}
	if review.Decision != "approved" && review.Decision != "rejected" {
		return fmt.Errorf("--decision must be approve or reject")
	}
	if !utf8.ValidString(review.Note) || utf8.RuneCountInString(review.Note) > 4096 {
		return fmt.Errorf("--note must contain at most 4096 Unicode characters")
	}
	return nil
}
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
func newWorkflowGateCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "gate", Short: "Submit an explicit decision to an exact reviewed human gate",
		Long: `Use --run, --stage and --temporal-run from workflow gates. All identities are
required; this command never selects the newest gate or falls back to a generic
signal. --decision accepts approve/approved or reject/rejected; --yes asserts the
user explicitly chose it. The server records the authenticated caller as approver.
An agent's own assessment is not a human decision.

The result distinguishes requested from recorded. After an uncertain response,
use gate-status with the same run and stage GUIDs before retrying. Conflicting
retries cannot replace an accepted decision. Old servers/workers may refuse this
operation and must be upgraded; there is no signal fallback.`, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := cmd.Flags().GetString("decision")
			decision, err := normalizeGateDecision(raw)
			if err != nil {
				return err
			}
			note, _ := cmd.Flags().GetString("note")
			review := gateReview{gateTarget: gateTargetFlags(cmd), Decision: decision, Note: note, Confirmed: boolFlag(cmd, "yes")}
			if err := validateGateReview(review); err != nil {
				return err
			}
			return workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
				return runWorkflowGate(cmd, ctx, client, review)
			})(cmd, args)
		}}
	cmd.Flags().String("run", "", "Exact workflow run GUID (required)")
	cmd.Flags().String("stage", "", "Exact gate execution GUID (required)")
	cmd.Flags().String("temporal-run", "", "Reviewed temporalExecution.runId of the gate, including child runs (required)")
	cmd.Flags().String("decision", "", "approve or reject (required)")
	cmd.Flags().String("note", "", "Note stored with the decision, at most 4096 characters")
	cmd.Flags().Bool("yes", false, "Confirm the user's explicit decision (required; no prompt)")
	return cmd
}
func newWorkflowGateStatusCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "gate-status", Short: "Recover an exact human-gate decision without resubmitting",
		Long: `Read humanGateDecision for the exact --run and --stage GUIDs. This command
never sends a decision. Requested means durably admitted; recorded means the gate
outcome was persisted, not that the whole workflow succeeded. Unknown means the
engine could not confirm the receipt; retain these IDs. Closed or unbound gates
are reported explicitly. Current permissions and named-approver checks still apply.`, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := gateTargetFlags(cmd)
			if err := validateGateTarget(target); err != nil {
				return err
			}
			return workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
				return runWorkflowGateStatus(cmd, ctx, client, target)
			})(cmd, args)
		}}
	cmd.Flags().String("run", "", "Exact workflow run GUID (required)")
	cmd.Flags().String("stage", "", "Exact gate execution GUID (required)")
	return cmd
}

func validateGateReceipt(gate *humanGateState, target gateTarget) error {
	if gate == nil {
		return fmt.Errorf("no visible decision receipt for this gate")
	}
	if !sameWorkflowGUID(gate.ExecutionGUID, target.RunGUID) || !sameWorkflowGUID(gate.StageExecutionGUID, target.StageExecutionGUID) {
		return fmt.Errorf("server returned a different gate identity")
	}
	if target.TemporalRunID != "" && (gate.TemporalExecution == nil || !sameWorkflowGUID(gate.TemporalExecution.RunID, target.TemporalRunID)) {
		return fmt.Errorf("server returned a different Temporal incarnation")
	}
	switch gate.RequestState {
	case "not_requested", "requested", "recorded", "closed", "unbound", "refused", "unknown":
		return nil
	default:
		return fmt.Errorf("server returned an unsupported decision state %q", gate.RequestState)
	}
}
func runWorkflowGate(cmd *cobra.Command, ctx context.Context, client *api.Client, review gateReview) error {
	if err := validateGateReview(review); err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var resp struct {
		Result humanGateReceipt `json:"decideHumanGate"`
	}
	err := client.GraphQL(callCtx, humanGateDecisionMutation, map[string]interface{}{
		"executionId": review.RunGUID, "stageExecutionId": review.StageExecutionGUID, "temporalRunId": review.TemporalRunID,
		"decision": review.Decision, "confirmed": true, "note": review.Note,
	}, &resp)
	result := resp.Result
	result.Target = review.gateTarget
	if err != nil {
		return reportGateReceipt(cmd, result, fmt.Errorf("decision delivery is unconfirmed: %w", err))
	}
	if !result.OK {
		return reportGateReceipt(cmd, result, fmt.Errorf("decision refused or unconfirmed: %s", firstValidationError(result.Errors)))
	}
	if err := validateGateReceipt(result.Gate, review.gateTarget); err != nil {
		return reportGateReceipt(cmd, result, err)
	}
	gate := result.Gate
	if gate.RequestedDecision == nil || *gate.RequestedDecision != review.Decision || gate.Note != review.Note || gate.DecidedByMe == nil || !*gate.DecidedByMe {
		return reportGateReceipt(cmd, result, fmt.Errorf("receipt does not confirm this caller's requested decision and note"))
	}
	if gate.RequestState != "requested" && gate.RequestState != "recorded" {
		return reportGateReceipt(cmd, result, fmt.Errorf("decision is %s; inspect this exact gate", gate.RequestState))
	}
	if gate.RequestState == "recorded" && (gate.RecordedDecision == nil || *gate.RecordedDecision != review.Decision) {
		return reportGateReceipt(cmd, result, fmt.Errorf("recorded outcome does not match the requested decision"))
	}
	return reportGateReceipt(cmd, result, nil)
}
func runWorkflowGateStatus(cmd *cobra.Command, ctx context.Context, client *api.Client, target gateTarget) error {
	if err := validateGateTarget(target); err != nil {
		return err
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var resp struct {
		Gate *humanGateState `json:"humanGateDecision"`
	}
	err := client.GraphQL(readCtx, humanGateStatusQuery, map[string]interface{}{"executionId": target.RunGUID, "stageExecutionId": target.StageExecutionGUID}, &resp)
	result := humanGateReceipt{OK: err == nil, Target: target, Gate: resp.Gate}
	if err == nil {
		err = validateGateReceipt(result.Gate, target)
	}
	if err == nil && (result.Gate.RequestState == "unknown" || result.Gate.RequestState == "refused") {
		err = fmt.Errorf("decision observation is %s: %s", result.Gate.RequestState, result.Gate.ObservationError)
	}
	return reportGateReceipt(cmd, result, err)
}
func reportGateReceipt(cmd *cobra.Command, result humanGateReceipt, failure error) error {
	if failure != nil {
		result.OK = false
		result.ClientError = failure.Error()
	}
	if boolFlag(cmd, "json") {
		if err := renderJSON(cmd, result); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Run: %s\nGate: %s\n", result.Target.RunGUID, result.Target.StageExecutionGUID)
		if g := result.Gate; g != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Decision state: %s\nStage: %s\nRun status: %s\n", g.RequestState, g.StageStatus, g.RunStatus)
			if g.RequestedDecision != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Requested decision: %s\n", *g.RequestedDecision)
			}
			if g.RecordedDecision != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Recorded decision: %s\n", *g.RecordedDecision)
			}
			if g.Note != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Note: %s\n", g.Note)
			}
			if g.ObservationError != "" {
				fmt.Fprintln(cmd.OutOrStdout(), g.ObservationError)
			}
		}
	}
	if failure != nil {
		return fmt.Errorf("%w; recover with: astro workflow gate-status --run %s --stage %s", failure, result.Target.RunGUID, result.Target.StageExecutionGUID)
	}
	return nil
}
func init() {
	workflowCmd.AddCommand(newWorkflowGatesCmd(), newWorkflowGateCmd(), newWorkflowGateStatusCmd())
}
