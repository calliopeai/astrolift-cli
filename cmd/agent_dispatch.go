// Package cmd — `astro agent dispatch <agent-slug>`.
//
// Ad-hoc "Once" dispatch of a registered agent Workload(kind=agent) by slug.
// This is the agent-slug seam (runAstroliftAgent) — distinct from
// `astro agent run <workflow-slug>` (runWorkflowDefinition), which dispatches a
// WorkflowDefinition's stages. An emr-bug-triage-style agent is a Workload, so
// it dispatches here, not through `run`.
//
// The backend resolves the agent Workload org-scoped, creates an AgentTask
// (DRAFT → QUEUED), optionally pins an AgentEnvironmentSpec, and enqueues the
// Temporal dispatch pipeline. --input is the opaque triggerPayload folded into
// the task's brief context; a bounded backfill is expressed as a payload the
// agent loops on (e.g. {"mode":"backfill","batches":N,"batch_size":M}) — there
// is no separate backfill primitive.
//
// GraphQL: runAstroliftAgent(input: RunAstroliftAgentInput!) → { ok, errors{message}, data{ id status } }
// --wait polls agentTask(id) to a terminal status; --tail streams its logs
// via the same agentTaskLogs poll `astro agent logs -f` uses.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

var (
	agentDispatchInput   string
	agentDispatchEnvSpec string
	agentDispatchTimeout int
	agentDispatchWait    bool
	agentDispatchTail    bool
	agentDispatchJSON    bool
)

// ---- GraphQL ---------------------------------------------------------------

const runAstroliftAgentMutation = `mutation($input: RunAstroliftAgentInput!) {
  runAstroliftAgent(input: $input) {
    ok
    errors { message }
    data { id status }
  }
}`

// envSpecIDQuery resolves an env-spec slug to its GUID so --env-spec can take a
// slug (what the operator knows) while the mutation wants environmentSpecId.
const envSpecIDQuery = `query($slug: String!) {
  agentEnvironmentSpec(slug: $slug) { id }
}`

type runAgentResult struct {
	Ok     bool `json:"ok"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Data *struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
}

// agentDispatchPollIntervalForTest is the cadence --wait re-queries the task
// status. A var (not const) so tests can shorten it.
var agentDispatchPollIntervalForTest = 5 * time.Second

// terminalAgentTaskStatuses are the AgentTask statuses --wait treats as final
// (compared case-insensitively). Mirrors the model's terminal set.
var terminalAgentTaskStatuses = map[string]bool{
	"completed": true,
	"failed":    true,
	"cancelled": true,
	"canceled":  true,
	"timed_out": true,
	"timedout":  true,
	"error":     true,
}

// ---- command ---------------------------------------------------------------

var agentDispatchCmd = &cobra.Command{
	Use:   "dispatch <agent-slug>",
	Short: "Dispatch a registered agent (Workload) by slug",
	Long: `Dispatches a Once run of a registered agent Workload(kind=agent) via
the runAstroliftAgent mutation. Prints the created AgentTask id and status.

--input accepts a JSON string or @filename; it is passed through as the
task's triggerPayload. A single smoke-test run is just a small payload
(or none); a bounded backfill is a payload the agent loops on, e.g.
  --input '{"mode":"backfill","batches":5,"batch_size":10}'

--env-spec pins an AgentEnvironmentSpec (by slug) to launch into — its
image + secret packet. Omit to use the workload's own image/runtime.

With --wait, blocks until the task reaches a terminal state, polling
agentTask. --tail additionally streams the task's logs while waiting.

Exit codes: 0 success, 1 dispatch/run failure.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAgentDispatch(cmd, cmd.Context(), client, args[0])
	},
}

func runAgentDispatch(cmd *cobra.Command, ctx context.Context, client *api.Client, agentSlug string) error {
	input := map[string]interface{}{"agentSlug": agentSlug}

	if agentDispatchInput != "" {
		payload, err := parseJSONInput(agentDispatchInput)
		if err != nil {
			return err
		}
		input["triggerPayload"] = payload
	}

	if agentDispatchEnvSpec != "" {
		id, err := resolveEnvSpecID(ctx, client, agentDispatchEnvSpec)
		if err != nil {
			return err
		}
		input["environmentSpecId"] = id
	}

	if agentDispatchTimeout > 0 {
		input["timeoutSeconds"] = agentDispatchTimeout
	}

	dispatchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var resp struct {
		Result runAgentResult `json:"runAstroliftAgent"`
	}
	if err := client.GraphQL(dispatchCtx, runAstroliftAgentMutation,
		map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("dispatching agent: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("dispatch failed: %s", firstMessageError(resp.Result.Errors))
	}
	if resp.Result.Data == nil {
		return fmt.Errorf("dispatch returned no task")
	}

	task := resp.Result.Data
	out := cmd.OutOrStdout()
	if agentDispatchJSON && !agentDispatchWait && !agentDispatchTail {
		return renderJSON(cmd, task)
	}
	fmt.Fprintf(out, "AgentTask ID: %s\n", task.ID)
	fmt.Fprintf(out, "Status:       %s\n", task.Status)

	if !agentDispatchWait && !agentDispatchTail {
		return nil
	}

	// --tail implies --wait: we stream logs until the task is terminal.
	if agentDispatchTail {
		return tailAgentTask(cmd, ctx, client, task.ID)
	}
	return waitAgentTask(cmd, ctx, client, task.ID)
}

// waitAgentTask polls agentTask(id) until the status is terminal.
func waitAgentTask(cmd *cobra.Command, ctx context.Context, client *api.Client, taskID string) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Waiting for terminal state...")

	pollCtx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()

	last := ""
	for {
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("timed out waiting for terminal state (last status: %s)", last)
		case <-time.After(agentDispatchPollIntervalForTest):
		}

		status, err := fetchAgentTaskStatus(pollCtx, client, taskID)
		if err != nil {
			return err
		}
		if status == "" {
			continue
		}
		if status != last {
			fmt.Fprintf(out, "  → %s\n", status)
			last = status
		}
		if terminalAgentTaskStatuses[strings.ToLower(status)] {
			return finalizeAgentTask(out, status)
		}
	}
}

// tailAgentTask streams the task's logs (polling agentTaskLogs) and stops when
// the task reaches a terminal status. Reuses agentTaskLogsQuery.
func tailAgentTask(cmd *cobra.Command, ctx context.Context, client *api.Client, taskID string) error {
	out := cmd.OutOrStdout()

	printed := 0
	last := ""
	for {
		// print any newly-appended log lines
		var logResp struct {
			Lines []string `json:"agentTaskLogs"`
		}
		logCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := client.GraphQL(logCtx, agentTaskLogsQuery,
			map[string]interface{}{"id": taskID, "tail": 500}, &logResp)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("polling logs: %w", err)
		}
		if len(logResp.Lines) < printed {
			printed = 0
		}
		for _, line := range logResp.Lines[printed:] {
			fmt.Fprintln(out, line)
		}
		printed = len(logResp.Lines)

		// check terminal status
		status, err := fetchAgentTaskStatus(ctx, client, taskID)
		if err != nil {
			return err
		}
		if status != "" && status != last {
			last = status
		}
		if terminalAgentTaskStatuses[strings.ToLower(status)] {
			return finalizeAgentTask(out, status)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(agentLogsPollIntervalForTest):
		}
	}
}

func finalizeAgentTask(out io.Writer, status string) error {
	fmt.Fprintf(out, "Final status: %s\n", status)
	if !strings.EqualFold(status, "completed") {
		return fmt.Errorf("task ended in %q", status)
	}
	return nil
}

// fetchAgentTaskStatus reads a single AgentTask's status via agentTaskQuery.
func fetchAgentTaskStatus(ctx context.Context, client *api.Client, taskID string) (string, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		AgentTask *agentTask `json:"agentTask"`
	}
	if err := client.GraphQL(fetchCtx, agentTaskQuery,
		map[string]interface{}{"id": taskID}, &resp); err != nil {
		return "", fmt.Errorf("polling task: %w", err)
	}
	if resp.AgentTask == nil {
		return "", nil
	}
	return resp.AgentTask.Status, nil
}

// resolveEnvSpecID looks up an env-spec's GUID from its slug.
func resolveEnvSpecID(ctx context.Context, client *api.Client, slug string) (string, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Spec *struct {
			ID string `json:"id"`
		} `json:"agentEnvironmentSpec"`
	}
	if err := client.GraphQL(fetchCtx, envSpecIDQuery,
		map[string]interface{}{"slug": slug}, &resp); err != nil {
		return "", fmt.Errorf("resolving env-spec %q: %w", slug, err)
	}
	if resp.Spec == nil || resp.Spec.ID == "" {
		return "", fmt.Errorf("env-spec %q not found", slug)
	}
	return resp.Spec.ID, nil
}

// ---- helpers ---------------------------------------------------------------

// parseJSONInput reads a --input value: a literal JSON string, or @file.json.
func parseJSONInput(raw string) (interface{}, error) {
	if strings.HasPrefix(raw, "@") {
		data, err := os.ReadFile(raw[1:])
		if err != nil {
			return nil, fmt.Errorf("reading input file: %w", err)
		}
		raw = string(data)
	}
	var payload interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("--input is not valid JSON: %w", err)
	}
	return payload, nil
}

// firstMessageError renders the first {message} error for display.
func firstMessageError(errs []struct {
	Message string `json:"message"`
}) string {
	if len(errs) == 0 {
		return "unknown error"
	}
	return errs[0].Message
}

// ---- init ------------------------------------------------------------------

func init() {
	agentDispatchCmd.Flags().StringVar(&agentDispatchInput, "input", "", "Trigger payload as a JSON string or @file.json")
	agentDispatchCmd.Flags().StringVar(&agentDispatchEnvSpec, "env-spec", "", "Env-spec slug to pin (image + secret packet)")
	agentDispatchCmd.Flags().IntVar(&agentDispatchTimeout, "timeout", 0, "Run timeout in seconds (0 = server default)")
	agentDispatchCmd.Flags().BoolVar(&agentDispatchWait, "wait", false, "Block until the task reaches a terminal state")
	agentDispatchCmd.Flags().BoolVar(&agentDispatchTail, "tail", false, "Stream task logs while waiting (implies --wait)")
	agentDispatchCmd.Flags().BoolVar(&agentDispatchJSON, "json", false, "Output the created task as JSON")

	agentCmd.AddCommand(agentDispatchCmd)
}
