package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

var agentDefinitionPollInterval = 5 * time.Second

// Keep the historical command as an alias without restoring slug-only writes.
// Existing request files still take the metadata-only recovery path, including
// when a legacy input flag is present or its old file no longer exists.
func runReviewedAgentDefinition(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
	if !definitionGUIDValid(id) {
		return errors.New("agent run requires an exact definition GUID; use workflow definitions --json and workflow definition-review, or agent task run for an AgentTask")
	}
	filename, _ := cmd.Flags().GetString("request-file")
	if filename == "" {
		return errors.New("--request-file is required; keep the original file for recovery")
	}
	_, statErr := os.Lstat(filename)
	if errors.Is(statErr, os.ErrNotExist) && agentRunInput != "" {
		if !strings.HasPrefix(agentRunInput, "@") || len(agentRunInput) < 2 {
			return errors.New("literal --input is no longer accepted; use --inputs-file with reviewed JSON inputs")
		}
		inputFilename, _ := cmd.Flags().GetString("inputs-file")
		if inputFilename != "" && inputFilename != agentRunInput[1:] {
			return errors.New("--input and --inputs-file select different files")
		}
		if err := cmd.Flags().Set("inputs-file", agentRunInput[1:]); err != nil {
			return err
		}
	}
	if !agentRunWait {
		return runDefinitionStart(cmd, ctx, client, id)
	}
	out := cmd.OutOrStdout()
	var receiptOutput bytes.Buffer
	if boolFlag(cmd, "json") {
		cmd.SetOut(&receiptOutput)
	}
	// Pin the verified dispatch/recovery receipt, never re-read a mutable file
	// to choose the execution followed by this invocation.
	start, err := runDefinitionStartWithReceipt(cmd, ctx, client, id)
	cmd.SetOut(out)
	if err != nil {
		if receiptOutput.Len() > 0 {
			if _, copyErr := io.Copy(out, &receiptOutput); copyErr != nil {
				return copyErr
			}
		}
		return err
	}
	if start == nil || start.TemporalRunID == nil || *start.TemporalRunID == "" || start.DispatchStatus != "submitted" {
		return errors.New("the original engine submission is unconfirmed; reconcile the same request")
	}
	return waitAgentDefinition(cmd, ctx, client, start, filename)
}

const agentDefinitionExecutionQuery = `query ReviewedAgentDefinitionExecution($id: ID!) { workflowExecution(executionId: $id) { guid recordId organizationGuid status temporalWorkflowId temporalRunId isTerminal observationError taskCleanup { status remaining retryable } } }`

func waitAgentDefinition(cmd *cobra.Command, ctx context.Context, client *api.Client, start *reviewedDefinitionStart, filename string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	opts := workflowExecutionOptions{WorkflowID: start.TemporalWorkflowID, RunID: *start.TemporalRunID}
	last := ""
	for {
		var resp struct {
			Execution *workflowExecution `json:"workflowExecution"`
		}
		readCtx, stopRead := context.WithTimeout(ctx, 30*time.Second)
		err := client.GraphQL(readCtx, agentDefinitionExecutionQuery, map[string]interface{}{"id": start.ExecutionID}, &resp)
		stopRead()
		if err == nil && resp.Execution != nil {
			row := resp.Execution
			if err := validateWorkflowExecution(row, start.ExecutionID, client.Org(), opts); err != nil {
				return errors.New("original execution identity or state changed; no replacement execution was followed")
			}
			if row.ObservationError == "" {
				if !boolFlag(cmd, "json") && last != row.Status {
					fmt.Fprintf(cmd.OutOrStdout(), "Execution %s: %s; cleanup %s\n", row.GUID, row.Status, row.TaskCleanup.Status)
					last = row.Status
				}
				if row.IsTerminal {
					if boolFlag(cmd, "json") {
						metadata := map[string]interface{}{"guid": row.GUID, "status": row.Status, "temporalWorkflowId": row.TemporalWorkflowID, "temporalRunId": row.TemporalRunID, "cleanupStatus": row.TaskCleanup.Status, "cleanupRemaining": row.TaskCleanup.Remaining}
						if err := renderJSON(cmd, map[string]interface{}{"start": start, "execution": metadata, "requestFile": filename}); err != nil {
							return err
						}
					}
					if row.Status != "completed" {
						return errors.New("the original workflow finished unsuccessfully; inspect its recorded execution")
					}
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("waiting ended with the original execution outcome unconfirmed; reconcile the saved request, without resubmitting inputs")
		case <-time.After(agentDefinitionPollInterval):
		}
	}
}

func init() {
	agentRunCmd.Flags().String("inputs-file", "", "Reviewed JSON inputs file; omitted for a no-input definition")
	agentRunCmd.Flags().String("request-file", "", "Private file retaining this definition start's request identity (required)")
	agentRunCmd.Flags().String("expected-revision", "", "Revision from prior exact definition review (paired with schema digest)")
	agentRunCmd.Flags().String("expected-input-schema-digest", "", "Digest from prior exact input review (paired with revision)")
	agentRunCmd.Flags().Bool("yes", false, "Confirm dispatch of the exact reviewed definition")
}
