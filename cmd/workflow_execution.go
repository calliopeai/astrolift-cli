package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const workflowExecutionFields = `guid recordId organizationGuid definitionSlug status temporalWorkflowId temporalRunId
startedAt endedAt isTerminal failure taskCleanup observationError`
const workflowExecutionQuery = `query($id: ID!) { workflowExecution(executionId: $id) { ` + workflowExecutionFields + ` } }`
const controlWorkflowExecutionMutation = `mutation($id: ID!, $workflow: String!, $run: String!, $action: String!, $reason: String!) {
  controlWorkflowExecution(executionId: $id, workflowId: $workflow, runId: $run, action: $action, reason: $reason) {
    ok requested errors { field messages } execution { ` + workflowExecutionFields + ` }
  }
}`

var workflowExecutionGUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var workflowExecutionNumber = regexp.MustCompile(`^[1-9][0-9]*$`)
var workflowExecutionPollInterval = 5 * time.Second

type workflowExecutionCleanup struct {
	Status    string `json:"status"`
	Remaining int    `json:"remaining"`
	Retryable bool   `json:"retryable"`
	Errors    []struct {
		TaskGUID string `json:"task_guid"`
		Message  string `json:"message"`
		Pending  bool   `json:"pending"`
	} `json:"errors"`
}

type workflowExecution struct {
	GUID               string                   `json:"guid"`
	RecordID           string                   `json:"recordId"`
	OrganizationGUID   string                   `json:"organizationGuid"`
	DefinitionSlug     string                   `json:"definitionSlug"`
	Status             string                   `json:"status"`
	TemporalWorkflowID string                   `json:"temporalWorkflowId"`
	TemporalRunID      string                   `json:"temporalRunId"`
	StartedAt          *string                  `json:"startedAt"`
	EndedAt            *string                  `json:"endedAt"`
	IsTerminal         bool                     `json:"isTerminal"`
	Failure            json.RawMessage          `json:"failure"`
	TaskCleanup        workflowExecutionCleanup `json:"taskCleanup"`
	ObservationError   string                   `json:"observationError"`
}

type workflowExecutionOptions struct {
	WorkflowID, RunID     string
	Watch, Yes, Terminate bool
	Reason                string
}

func validWorkflowExecutionID(id string) bool {
	if workflowExecutionGUID.MatchString(id) {
		return true
	}
	if !workflowExecutionNumber.MatchString(id) {
		return false
	}
	number, err := strconv.ParseInt(id, 10, 64)
	return err == nil && number > 0
}

func validateWorkflowExecution(row *workflowExecution, id, organization string, opts workflowExecutionOptions) error {
	if row == nil {
		return fmt.Errorf("workflow execution %q not found", id)
	}
	if !workflowExecutionGUID.MatchString(row.GUID) || !workflowExecutionNumber.MatchString(row.RecordID) || !validWorkflowExecutionID(row.RecordID) ||
		(!strings.EqualFold(row.GUID, id) && row.RecordID != id) || organization == "" ||
		!strings.EqualFold(row.OrganizationGUID, organization) {
		return fmt.Errorf("astrolift returned a different or invalid workflow execution identity")
	}
	if (opts.WorkflowID != "" && opts.WorkflowID != row.TemporalWorkflowID) ||
		(opts.RunID != "" && opts.RunID != row.TemporalRunID) {
		return fmt.Errorf("the Temporal execution identity changed; the original execution was retained")
	}
	switch row.Status {
	case "running", "completed", "failed", "cancelled", "terminated", "timed_out":
	default:
		return fmt.Errorf("invalid workflow execution status %q", row.Status)
	}
	if row.IsTerminal != (row.Status != "running") || row.TaskCleanup.Remaining < 0 {
		return fmt.Errorf("inconsistent workflow execution status")
	}
	if row.ObservationError == "" && (row.TemporalWorkflowID == "" || row.TemporalRunID == "") {
		return fmt.Errorf("verified execution has no complete Temporal identity")
	}
	switch row.TaskCleanup.Status {
	case "not_requested", "pending", "failed":
	case "completed", "not_required":
		if row.TaskCleanup.Remaining != 0 {
			return fmt.Errorf("inconsistent workflow cleanup status")
		}
	default:
		return fmt.Errorf("invalid workflow cleanup status %q", row.TaskCleanup.Status)
	}
	return nil
}

func fetchWorkflowExecution(ctx context.Context, client *api.Client, id string, opts workflowExecutionOptions) (*workflowExecution, error) {
	if !validWorkflowExecutionID(id) {
		return nil, fmt.Errorf("use the WorkflowRun ID printed on dispatch or the execution GUID")
	}
	if client.Org() == "" {
		return nil, fmt.Errorf("select an organization before inspecting an execution")
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var response struct {
		Execution *workflowExecution `json:"workflowExecution"`
	}
	if err := client.GraphQL(readCtx, workflowExecutionQuery, map[string]interface{}{"id": id}, &response); err != nil {
		return nil, fmt.Errorf("reading workflow execution: %w", err)
	}
	if err := validateWorkflowExecution(response.Execution, id, client.Org(), opts); err != nil {
		return nil, err
	}
	return response.Execution, nil
}

func printWorkflowExecution(cmd *cobra.Command, row *workflowExecution) error {
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, row)
	}
	text := fmt.Sprintf("Execution:       %s (%s)\nDefinition:      %s\nStatus:          %s\nResource cleanup: %s (%d remaining)\n",
		row.RecordID, row.GUID, row.DefinitionSlug, row.Status, row.TaskCleanup.Status, row.TaskCleanup.Remaining)
	text += fmt.Sprintf("Temporal:        %s / %s\n", row.TemporalWorkflowID, row.TemporalRunID)
	if row.ObservationError != "" {
		text += fmt.Sprintf("Observation:     %s\n", row.ObservationError)
	}
	for _, issue := range row.TaskCleanup.Errors {
		text += fmt.Sprintf("  %s: %s\n", issue.TaskGUID, issue.Message)
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), text)
	return err
}

func runWorkflowExecution(cmd *cobra.Command, ctx context.Context, client *api.Client, id string, opts workflowExecutionOptions) error {
	row, err := fetchWorkflowExecution(ctx, client, id, opts)
	if err != nil {
		return err
	}
	if !opts.Watch {
		return printWorkflowExecution(cmd, row)
	}
	watchCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	var last string
	for {
		settled := row.ObservationError == "" && row.IsTerminal &&
			(row.TaskCleanup.Status == "completed" || row.TaskCleanup.Status == "not_required")
		current, _ := json.Marshal(row)
		if !boolFlag(cmd, "json") && string(current) != last {
			if err := printWorkflowExecution(cmd, row); err != nil {
				return err
			}
			last = string(current)
		}
		if settled {
			if boolFlag(cmd, "json") {
				return printWorkflowExecution(cmd, row)
			}
			return nil
		}
		select {
		case <-watchCtx.Done():
			return fmt.Errorf("watching execution %s: %w", row.RecordID, watchCtx.Err())
		case <-time.After(workflowExecutionPollInterval):
		}
		if opts.WorkflowID == "" {
			opts.WorkflowID = row.TemporalWorkflowID
		}
		if opts.RunID == "" {
			opts.RunID = row.TemporalRunID
		}
		observed, err := fetchWorkflowExecution(watchCtx, client, row.GUID, opts)
		if err != nil {
			row.ObservationError = err.Error()
			continue
		}
		if observed.RecordID != row.RecordID {
			return fmt.Errorf("workflow execution record changed")
		}
		row = observed
	}
}

func runWorkflowExecutionControl(cmd *cobra.Command, ctx context.Context, client *api.Client, id, action string, opts workflowExecutionOptions) error {
	if !opts.Yes {
		return fmt.Errorf("--yes is required to request execution control")
	}
	if opts.Terminate {
		action = "terminate"
	}
	if action == "terminate" && strings.TrimSpace(opts.Reason) == "" {
		return fmt.Errorf("--terminate requires --reason")
	}
	if action != "terminate" && opts.Reason != "" {
		return fmt.Errorf("--reason applies only to termination")
	}
	row, err := fetchWorkflowExecution(ctx, client, id, opts)
	if err != nil {
		return err
	}
	if row.ObservationError != "" {
		return fmt.Errorf("cannot verify execution: %s", row.ObservationError)
	}
	if row.TemporalWorkflowID == "" || row.TemporalRunID == "" {
		return fmt.Errorf("execution has no complete Temporal identity")
	}
	opts.WorkflowID, opts.RunID = row.TemporalWorkflowID, row.TemporalRunID
	var response struct {
		Result struct {
			OK        bool               `json:"ok"`
			Requested bool               `json:"requested"`
			Errors    validationErrors   `json:"errors"`
			Execution *workflowExecution `json:"execution"`
		} `json:"controlWorkflowExecution"`
	}
	client.SetTimeout(120 * time.Second)
	controlCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	err = client.GraphQL(controlCtx, controlWorkflowExecutionMutation, map[string]interface{}{
		"id": row.GUID, "workflow": row.TemporalWorkflowID, "run": row.TemporalRunID,
		"action": action, "reason": opts.Reason,
	}, &response)
	if err != nil {
		return workflowRunControlError(action, err)
	}
	result := response.Result
	if !result.OK {
		return fmt.Errorf("execution control failed: %s", firstValidationError(result.Errors))
	}
	if !result.Requested {
		return fmt.Errorf("astrolift did not acknowledge this execution control request")
	}
	if err := validateWorkflowExecution(result.Execution, row.GUID, client.Org(), opts); err != nil {
		return err
	}
	if result.Execution.RecordID != row.RecordID {
		return fmt.Errorf("astrolift acknowledged a different execution record")
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, map[string]interface{}{"requested": true, "action": action, "execution": result.Execution})
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s acknowledged for execution %s.\n", action, row.RecordID); err != nil {
		return err
	}
	return printWorkflowExecution(cmd, result.Execution)
}

func newWorkflowExecutionCommand(action string) *cobra.Command {
	opts := workflowExecutionOptions{}
	use, short := "execution <id>", "Inspect an exact configured or definition workflow execution"
	if action == "cancel" {
		use, short = "execution-stop <id>", "Stop an exact workflow execution"
	}
	if action == "cleanup" {
		use, short = "execution-cleanup <id>", "Retry owned resource cleanup for a closed execution"
	}
	cmd := &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(1),
		Long: short + `. Use the WorkflowRun ID printed by 'workflow run' or 'agent run', or its execution GUID.
The original server and organization are selected with the normal --server and --org flags.
Execution closure and resource cleanup are separate states. A control acknowledgement does not prove cleanup has finished.`,
		RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
			if action == "" {
				return runWorkflowExecution(cmd, ctx, client, args[0], opts)
			}
			return runWorkflowExecutionControl(cmd, ctx, client, args[0], action, opts)
		}),
	}
	cmd.Flags().StringVar(&opts.WorkflowID, "workflow-id", "", "Require this original Temporal workflow ID")
	cmd.Flags().StringVar(&opts.RunID, "run-id", "", "Require this original Temporal execution ID")
	if action == "" {
		cmd.Flags().BoolVar(&opts.Watch, "watch", false, "Watch this exact execution until it closes and resource cleanup settles; --json prints only the final record")
	} else {
		cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Confirm the control request")
	}
	if action == "cancel" {
		cmd.Flags().BoolVar(&opts.Terminate, "terminate", false, "Hard terminate instead of cooperative cancellation")
		cmd.Flags().StringVar(&opts.Reason, "reason", "", "Required explanation for termination")
	}
	return cmd
}

func init() {
	workflowCmd.AddCommand(newWorkflowExecutionCommand(""), newWorkflowExecutionCommand("cancel"), newWorkflowExecutionCommand("cleanup"))
}
