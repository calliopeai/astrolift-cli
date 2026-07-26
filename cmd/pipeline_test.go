package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// pipelineTestCmd returns a bare command with stdout captured. The pipeline
// run-funcs read their inputs from package-level flag vars (set per-test) and
// write to cmd.OutOrStdout().
func pipelineTestCmd() (*cobra.Command, *bytes.Buffer) {
	c := &cobra.Command{}
	out := &bytes.Buffer{}
	c.SetOut(out)
	return c, out
}

func TestRunPipelineListSendsNonNullLimit(t *testing.T) {
	pipelineListLimit = 25
	pipelineListJSON = false
	defer func() { pipelineListLimit = 0; pipelineListJSON = false }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftPipelines": []map[string]interface{}{
			{"id": "p-1", "name": "deploy", "repoUrl": "https://git/x", "defaultBranch": "main", "tomlPath": ".astro/pipeline.toml", "createdAt": "2026-01-01T00:00:00Z"},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := pipelineTestCmd()
	if err := runPipelineList(cmd, context.Background(), client); err != nil {
		t.Fatalf("runPipelineList: %v", err)
	}

	if !strings.Contains(captured.Query, "astroliftPipelines(limit: $limit)") {
		t.Errorf("list query lost limit arg:\n%s", captured.Query)
	}
	// Schema: astroliftPipelines(limit: Int! = 100) — must be declared Int!.
	if !strings.Contains(captured.Query, "$limit: Int!") {
		t.Errorf("list limit must be declared Int! to match schema:\n%s", captured.Query)
	}
	if got := captured.Variables["limit"]; got != float64(25) {
		t.Errorf("limit variable = %v, want 25", got)
	}
	if !strings.Contains(out.String(), "deploy") {
		t.Errorf("expected pipeline name in output:\n%s", out.String())
	}
}

func TestRunPipelineRunSendsPositionalGUIDArgs(t *testing.T) {
	pipelineRunBranch = "feature/x"
	defer func() { pipelineRunBranch = "" }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		// resolvePipelineID lookup
		"astroliftPipelines": []map[string]interface{}{
			{"id": "guid-42", "name": "deploy"},
		},
		// triggerPipelineRun mutation
		"triggerPipelineRun": map[string]interface{}{
			"ok":     true,
			"errors": []interface{}{},
			"data":   map[string]interface{}{"id": "run-1", "runNumber": 7, "status": "QUEUED"},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := pipelineTestCmd()
	if err := runPipelineRun(cmd, context.Background(), client, "deploy"); err != nil {
		t.Fatalf("runPipelineRun: %v", err)
	}

	// The last captured request is the mutation. Schema:
	// triggerPipelineRun(pipelineId: GUID!, ref: String = null) — positional
	// args, NOT an input object (there is no TriggerPipelineRunInput type).
	if !strings.Contains(captured.Query, "triggerPipelineRun(pipelineId: $pipelineId, ref: $ref)") {
		t.Errorf("trigger mutation lost positional args:\n%s", captured.Query)
	}
	if !strings.Contains(captured.Query, "$pipelineId: GUID!") {
		t.Errorf("pipelineId must be declared GUID!:\n%s", captured.Query)
	}
	if strings.Contains(captured.Query, "TriggerPipelineRunInput") || strings.Contains(captured.Query, "input:") {
		t.Errorf("trigger mutation still uses the non-existent input shape:\n%s", captured.Query)
	}
	if got := captured.Variables["pipelineId"]; got != "guid-42" {
		t.Errorf("pipelineId variable = %v, want resolved guid-42", got)
	}
	if got := captured.Variables["ref"]; got != "feature/x" {
		t.Errorf("ref variable = %v, want feature/x", got)
	}
	if !strings.Contains(out.String(), "#7") || !strings.Contains(out.String(), "QUEUED") {
		t.Errorf("expected run summary in output:\n%s", out.String())
	}
}

func TestRunPipelineRunsSendsRequiredStringPipelineID(t *testing.T) {
	pipelineRunsPipeline = "deploy"
	pipelineRunsLimit = 10
	defer func() { pipelineRunsPipeline = ""; pipelineRunsLimit = 0 }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		// resolvePipelineID lookup (name -> guid)
		"astroliftPipelines": []map[string]interface{}{
			{"id": "guid-99", "name": "deploy"},
		},
		"astroliftPipelineRuns": []map[string]interface{}{
			{"id": "r-1", "runNumber": 3, "status": "SUCCESS", "triggerKind": "manual", "triggerRef": "main", "startedAt": nil, "finishedAt": nil},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := pipelineTestCmd()
	if err := runPipelineRuns(cmd, context.Background(), client); err != nil {
		t.Fatalf("runPipelineRuns: %v", err)
	}

	// Schema: astroliftPipelineRuns(pipelineId: String!, limit: Int! = 50).
	if !strings.Contains(captured.Query, "astroliftPipelineRuns(pipelineId: $pipelineId, limit: $limit)") {
		t.Errorf("runs query arg shape wrong:\n%s", captured.Query)
	}
	if !strings.Contains(captured.Query, "$pipelineId: String!") {
		t.Errorf("runs pipelineId must be required String! (was optional ID):\n%s", captured.Query)
	}
	if !strings.Contains(captured.Query, "$limit: Int!") {
		t.Errorf("runs limit must be Int!:\n%s", captured.Query)
	}
	if got := captured.Variables["pipelineId"]; got != "guid-99" {
		t.Errorf("pipelineId variable = %v, want resolved guid-99", got)
	}
	if !strings.Contains(out.String(), "#3") {
		t.Errorf("expected run row in output:\n%s", out.String())
	}
}

func TestRunPipelineCancelSendsRunIDArg(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"cancelPipelineRun": map[string]interface{}{"ok": true, "errors": []interface{}{}},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := pipelineTestCmd()
	if err := runPipelineCancel(cmd, context.Background(), client, "run-guid-5"); err != nil {
		t.Fatalf("runPipelineCancel: %v", err)
	}

	// Schema: cancelPipelineRun(runId: GUID!) — positional runId, not id: ID!.
	if !strings.Contains(captured.Query, "cancelPipelineRun(runId: $runId)") {
		t.Errorf("cancel mutation must use runId arg:\n%s", captured.Query)
	}
	if !strings.Contains(captured.Query, "$runId: GUID!") {
		t.Errorf("cancel runId must be GUID!:\n%s", captured.Query)
	}
	if strings.Contains(captured.Query, "$id: ID!") || strings.Contains(captured.Query, "cancelPipelineRun(id:") {
		t.Errorf("cancel mutation still uses the old id arg:\n%s", captured.Query)
	}
	if got := captured.Variables["runId"]; got != "run-guid-5" {
		t.Errorf("runId variable = %v, want run-guid-5", got)
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Errorf("expected cancel confirmation:\n%s", out.String())
	}
}

func TestResolvePipelineIDNotFound(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftPipelines": []map[string]interface{}{{"id": "guid-1", "name": "other"}},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	if _, err := resolvePipelineID(context.Background(), client, "missing"); err == nil {
		t.Error("expected not-found error for unknown pipeline name/id")
	}
}
