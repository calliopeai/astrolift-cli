package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const workflowExecutionStagesQuery = `query($id: ID!, $limit: Int!, $after: String) {
  workflowExecutionStages(executionId: $id, limit: $limit, after: $after) {
    executionGuid recordId organizationGuid temporalWorkflowId temporalRunId
    stages { nextCursor items {
      guid executionId stageGuid stageOrder stageKind stageRole stageApprovers
      status attemptNumber humanGateState humanGateNote startedAt endedAt errorMessage
      agentRunGuid childWorkflowRunGuid childWorkflowDefinitionSlug childWorkflowStatus
    } }
  }
}`

type exactWorkflowStage struct {
	workflowStageExecutionRow
	GUID                        string  `json:"guid"`
	ExecutionID                 string  `json:"executionId"`
	StageGUID                   string  `json:"stageGuid"`
	AgentRunGUID                *string `json:"agentRunGuid"`
	ChildWorkflowRunGUID        *string `json:"childWorkflowRunGuid"`
	ChildWorkflowDefinitionSlug *string `json:"childWorkflowDefinitionSlug"`
	ChildWorkflowStatus         *string `json:"childWorkflowStatus"`
}

type exactWorkflowStagesPage struct {
	ExecutionGUID      string `json:"executionGuid"`
	RecordID           string `json:"recordId"`
	OrganizationGUID   string `json:"organizationGuid"`
	TemporalWorkflowID string `json:"temporalWorkflowId"`
	TemporalRunID      string `json:"temporalRunId"`
	Stages             *struct {
		Items      []exactWorkflowStage `json:"items"`
		NextCursor json.RawMessage      `json:"nextCursor"`
	} `json:"stages"`
}

func validateExactStage(stage exactWorkflowStage) error {
	if !workflowExecutionGUID.MatchString(stage.GUID) || !workflowExecutionGUID.MatchString(stage.StageGUID) ||
		!workflowExecutionNumber.MatchString(stage.ExecutionID) || !validWorkflowExecutionID(stage.ExecutionID) ||
		stage.StageOrder < 0 || stage.AttemptNumber < 1 || stage.StageKind == "" {
		return fmt.Errorf("invalid recorded stage identity or attempt")
	}
	switch stage.Status {
	case "pending", "running", "completed", "failed", "skipped", "escalated", "cancelled":
	default:
		return fmt.Errorf("invalid recorded stage status %q", stage.Status)
	}
	for _, guid := range []*string{stage.AgentRunGUID, stage.ChildWorkflowRunGUID} {
		if guid != nil && !workflowExecutionGUID.MatchString(*guid) {
			return fmt.Errorf("invalid linked execution identity")
		}
	}
	return nil
}

func fetchExactWorkflowStages(ctx context.Context, client *api.Client, execution *workflowExecution) ([]exactWorkflowStage, error) {
	stages := make([]exactWorkflowStage, 0)
	seenStages, seenCursors := map[string]bool{}, map[string]bool{}
	var after *string
	for {
		var response struct {
			Page *exactWorkflowStagesPage `json:"workflowExecutionStages"`
		}
		err := client.GraphQL(ctx, workflowExecutionStagesQuery, map[string]interface{}{
			"id": execution.GUID, "limit": 100, "after": after,
		}, &response)
		if err != nil {
			return nil, fmt.Errorf("reading execution stages: %w", err)
		}
		page := response.Page
		if page == nil || !strings.EqualFold(page.ExecutionGUID, execution.GUID) || page.RecordID != execution.RecordID ||
			!strings.EqualFold(page.OrganizationGUID, execution.OrganizationGUID) ||
			page.TemporalWorkflowID != execution.TemporalWorkflowID || page.TemporalRunID != execution.TemporalRunID {
			return nil, fmt.Errorf("stage history no longer belongs to the original execution; inspect it again")
		}
		if page.Stages == nil || page.Stages.Items == nil || len(page.Stages.Items) > 100 || len(page.Stages.NextCursor) == 0 {
			return nil, fmt.Errorf("astrolift returned an invalid stage page")
		}
		for _, stage := range page.Stages.Items {
			if err := validateExactStage(stage); err != nil {
				return nil, err
			}
			key := strings.ToLower(stage.GUID)
			if seenStages[key] {
				return nil, fmt.Errorf("stage history repeated an attempt; inspect it again")
			}
			seenStages[key] = true
			stages = append(stages, stage)
		}
		var next *string
		if err := json.Unmarshal(page.Stages.NextCursor, &next); err != nil {
			return nil, fmt.Errorf("astrolift returned an invalid stage cursor")
		}
		if next == nil {
			return stages, nil
		}
		if *next == "" || len(*next) > 4096 || len(page.Stages.Items) == 0 || seenCursors[*next] {
			return nil, fmt.Errorf("stage pagination made no progress; inspect it again")
		}
		seenCursors[*next] = true
		after = next
	}
}

func runWorkflowExecutionStages(cmd *cobra.Command, ctx context.Context, client *api.Client, id string, opts workflowExecutionOptions) error {
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	execution, err := fetchWorkflowExecution(readCtx, client, id, opts)
	if err != nil {
		return err
	}
	stages, err := fetchExactWorkflowStages(readCtx, client, execution)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, struct {
			Execution *workflowExecution   `json:"execution"`
			Stages    []exactWorkflowStage `json:"stages"`
		}{execution, stages})
	}
	if err := printWorkflowExecution(cmd, execution); err != nil {
		return err
	}
	var text strings.Builder
	text.WriteString("\nRecorded stage history. Pending rows may remain after execution closure.\n")
	if len(stages) == 0 {
		text.WriteString("No recorded stages.\n")
	} else {
		sort.SliceStable(stages, func(i, j int) bool {
			if stages[i].StageOrder != stages[j].StageOrder {
				return stages[i].StageOrder < stages[j].StageOrder
			}
			if stages[i].AttemptNumber != stages[j].AttemptNumber {
				return stages[i].AttemptNumber < stages[j].AttemptNumber
			}
			return stages[i].GUID < stages[j].GUID
		})
		w := tabwriter.NewWriter(&text, 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "STAGE\tKIND\tROLE\tSTATUS\tATTEMPT\tGATE\tSTARTED\tFINISHED\tEXECUTION"); err != nil {
			return err
		}
		for _, stage := range stages {
			if _, err := fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n",
				stage.StageOrder, stage.StageKind, dashIfEmpty(stage.StageRole), stage.Status, stage.AttemptNumber,
				dashIfEmpty(stage.HumanGateState), shortTime(stage.StartedAt), shortTime(stage.EndedAt), stage.ExecutionID); err != nil {
				return err
			}
		}
		if err := w.Flush(); err != nil {
			return err
		}
		for _, stage := range stages {
			fmt.Fprintf(&text, "\n  [%d attempt %d] %s\n", stage.StageOrder, stage.AttemptNumber, stage.GUID)
			if note := gateDetail(stage.workflowStageExecutionRow); note != "" {
				fmt.Fprintf(&text, "    %s\n", note)
			}
			if stage.ErrorMessage != "" {
				fmt.Fprintf(&text, "    error: %s\n", stage.ErrorMessage)
			}
			if stage.AgentRunGUID != nil {
				fmt.Fprintf(&text, "    agent run: %s\n", *stage.AgentRunGUID)
			}
			if stage.ChildWorkflowRunGUID != nil {
				fmt.Fprintf(&text, "    child workflow: %s\n", *stage.ChildWorkflowRunGUID)
			}
		}
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), text.String())
	return err
}

func newWorkflowExecutionStagesCommand() *cobra.Command {
	opts := workflowExecutionOptions{}
	cmd := &cobra.Command{
		Use: "execution-stages <id>", Short: "Inspect all recorded stages of an exact workflow execution",
		Long: `Inspect recorded stage attempts, approvals, errors, and linked executions using the WorkflowRun ID printed on dispatch or its execution GUID.
Select the original --server and --org. Pages are read to completion, with the same execution identity checked on every page.
Recorded stages remain readable when Temporal is unavailable. They do not prove execution closure or resource cleanup.`,
		Args: cobra.ExactArgs(1),
		RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
			return runWorkflowExecutionStages(cmd, ctx, client, args[0], opts)
		}),
	}
	cmd.Flags().StringVar(&opts.WorkflowID, "workflow-id", "", "Require this original Temporal workflow ID")
	cmd.Flags().StringVar(&opts.RunID, "run-id", "", "Require this original Temporal execution ID")
	return cmd
}

func init() {
	workflowCmd.AddCommand(newWorkflowExecutionStagesCommand())
}
