package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// gqlRequest is the inbound GraphQL envelope the test server inspects.
type gqlRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

// gqlServer stands up an httptest server that captures the last GraphQL
// request and replies with the supplied data map. The capture pointer lets a
// test assert on the operation text + variables the command actually sent.
func gqlServer(t *testing.T, data map[string]interface{}, capture *gqlRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if capture != nil {
			if err := json.Unmarshal(body, capture); err != nil {
				t.Errorf("decoding request body: %v", err)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
	}))
}

// agentTestCmd builds a command carrying the persistent flags the agent
// run-funcs and resolveOrg inspect, with a buffer captured for stdout.
func agentTestCmd() (*cobra.Command, *bytes.Buffer) {
	root := &cobra.Command{}
	root.PersistentFlags().Bool("no-prompt", false, "")
	c := &cobra.Command{}
	c.Flags().String("org", "", "")
	c.Flags().Bool("no-prompt", false, "")
	c.Flags().Bool("debug", false, "")
	c.Flags().Bool("json", false, "")
	root.AddCommand(c)
	out := &bytes.Buffer{}
	c.SetOut(out)
	return c, out
}

func TestAgentRunSendsMutationAndParsesIDs(t *testing.T) {
	agentRunInput = `{"pr":12}`
	agentRunWait = false
	defer func() { agentRunInput = "" }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"runWorkflowDefinition": map[string]interface{}{
			"ok":                 true,
			"errors":             []interface{}{},
			"workflowRunId":      "42",
			"temporalWorkflowId": "WorkflowDefinitionRunWorkflow-42",
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	if err := runAgentRun(cmd, context.Background(), client, &config.Config{}, "triage-pr"); err != nil {
		t.Fatalf("runAgentRun: %v", err)
	}

	// Sent the right operation + variables.
	if !strings.Contains(captured.Query, "runWorkflowDefinition(workflowSlug: $workflowSlug, triggerPayload: $triggerPayload)") {
		t.Errorf("query did not call runWorkflowDefinition with expected args:\n%s", captured.Query)
	}
	if captured.Variables["workflowSlug"] != "triage-pr" {
		t.Errorf("workflowSlug var = %v, want triage-pr", captured.Variables["workflowSlug"])
	}
	payload, ok := captured.Variables["triggerPayload"].(map[string]interface{})
	if !ok || payload["pr"].(float64) != 12 {
		t.Errorf("triggerPayload var = %v, want {pr:12}", captured.Variables["triggerPayload"])
	}

	// Parsed both ids from the camelCase result.
	got := out.String()
	if !strings.Contains(got, "WorkflowRun ID:    42") {
		t.Errorf("output missing workflowRunId:\n%s", got)
	}
	if !strings.Contains(got, "Temporal workflow: WorkflowDefinitionRunWorkflow-42") {
		t.Errorf("output missing temporalWorkflowId:\n%s", got)
	}
}

func TestAgentRunSurfacesValidationError(t *testing.T) {
	agentRunInput = ""
	agentRunWait = false

	srv := gqlServer(t, map[string]interface{}{
		"runWorkflowDefinition": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{
					"field":    "workflowSlug",
					"messages": []interface{}{`Workflow "nope" not found or disabled`},
				},
			},
			"workflowRunId":      nil,
			"temporalWorkflowId": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := agentTestCmd()
	err := runAgentRun(cmd, context.Background(), client, &config.Config{}, "nope")
	if err == nil {
		t.Fatal("expected error when ok=false")
	}
	if !strings.Contains(err.Error(), "workflowSlug") || !strings.Contains(err.Error(), "not found") {
		t.Errorf("error did not surface the validation message: %v", err)
	}
}

func TestAgentListDecodesTasksAndAppliesLimit(t *testing.T) {
	agentListStatus = "running"
	agentListLimit = 1
	agentListJSON = false
	defer func() { agentListStatus = ""; agentListLimit = 20 }()

	started := "2026-06-17T10:00:00.123456+00:00"
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		// resolveOrg's listOrgs call and the agentTasks call hit the same
		// handler; return both keys so either query resolves.
		"astroliftOrganizations": []map[string]interface{}{
			{"id": "org-1", "name": "Acme", "slug": "acme"},
		},
		"agentTasks": []map[string]interface{}{
			{
				"id": "task-aaa", "status": "running", "callbackUrl": "",
				"result": nil, "createdAt": "2026-06-17T09:59:00+00:00",
				"startedAt": started, "finishedAt": nil,
				"vncEnabled": true, "vncUrl": "/app/vnc/task-aaa", "snapshotUrl": nil,
			},
			{
				"id": "task-bbb", "status": "running", "callbackUrl": "",
				"result": nil, "createdAt": "2026-06-17T09:58:00+00:00",
				"startedAt": nil, "finishedAt": nil,
				"vncEnabled": false, "vncUrl": "", "snapshotUrl": nil,
			},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	_ = cmd.Flags().Set("org", "acme")
	if err := runAgentList(cmd, context.Background(), client, &config.Config{}); err != nil {
		t.Fatalf("runAgentList: %v", err)
	}

	got := out.String()
	// First row decoded from camelCase fields and minute-trimmed.
	if !strings.Contains(got, "task-aaa") || !strings.Contains(got, "running") {
		t.Errorf("first task not rendered:\n%s", got)
	}
	if !strings.Contains(got, "2026-06-17T10:00:00") || strings.Contains(got, ".123456") {
		t.Errorf("startedAt not minute-trimmed:\n%s", got)
	}
	// --limit=1 dropped the second row.
	if strings.Contains(got, "task-bbb") {
		t.Errorf("limit not applied client-side, second row present:\n%s", got)
	}
	if !strings.Contains(got, "1 task(s) shown.") {
		t.Errorf("count footer wrong:\n%s", got)
	}
}

func TestAgentListJSONOutput(t *testing.T) {
	agentListStatus = ""
	agentListLimit = 20
	agentListJSON = true
	defer func() { agentListJSON = false }()

	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": []map[string]interface{}{
			{"id": "org-1", "name": "Acme", "slug": "acme"},
		},
		"agentTasks": []map[string]interface{}{
			{
				"id": "task-aaa", "status": "completed", "callbackUrl": "",
				"result": map[string]interface{}{"exit": 0}, "createdAt": "2026-06-17T09:00:00+00:00",
				"startedAt": nil, "finishedAt": nil, "vncEnabled": false,
				"vncUrl": "", "snapshotUrl": nil,
			},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	_ = cmd.Flags().Set("org", "acme")
	if err := runAgentList(cmd, context.Background(), client, &config.Config{}); err != nil {
		t.Fatalf("runAgentList: %v", err)
	}

	var rows []agentTask
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("output is not valid JSON array: %v\n%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].ID != "task-aaa" || rows[0].Status != "completed" {
		t.Errorf("decoded rows wrong: %+v", rows)
	}
}

func TestAgentInspectNotFound(t *testing.T) {
	agentInspectJSON = false
	srv := gqlServer(t, map[string]interface{}{"agentTask": nil}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := agentTestCmd()
	err := runAgentInspect(cmd, context.Background(), client, &config.Config{}, "missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestAgentInspectRendersRecord(t *testing.T) {
	agentInspectJSON = false
	started := "2026-06-17T10:00:00+00:00"
	srv := gqlServer(t, map[string]interface{}{
		"agentTask": map[string]interface{}{
			"id": "task-aaa", "status": "running", "callbackUrl": "https://cb",
			"result": nil, "createdAt": "2026-06-17T09:59:00+00:00",
			"startedAt": started, "finishedAt": nil,
			"vncEnabled": true, "vncUrl": "/app/vnc/task-aaa", "snapshotUrl": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	if err := runAgentInspect(cmd, context.Background(), client, &config.Config{}, "task-aaa"); err != nil {
		t.Fatalf("runAgentInspect: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Task ID:", "task-aaa", "Status:", "running", "VNC URL:", "/app/vnc/task-aaa"} {
		if !strings.Contains(got, want) {
			t.Errorf("inspect output missing %q:\n%s", want, got)
		}
	}
}

func TestAgentCancelSurfacesMutationError(t *testing.T) {
	agentCancelYes = true
	defer func() { agentCancelYes = false }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"cancelTask": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{
					"code":    "precondition",
					"message": "AgentTask cannot transition 'running' → 'cancelled'",
					"field":   nil,
				},
			},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := agentTestCmd()
	err := runAgentCancel(cmd, context.Background(), client, &config.Config{}, "task-aaa")
	if err == nil {
		t.Fatal("expected error when ok=false")
	}
	// MutationError carries `message` (not the ValidationError `messages` list).
	if !strings.Contains(err.Error(), "cannot transition") {
		t.Errorf("error did not surface MutationError.message: %v", err)
	}
	if captured.Variables["id"] != "task-aaa" {
		t.Errorf("id var = %v, want task-aaa", captured.Variables["id"])
	}
}

func TestAgentCancelSuccess(t *testing.T) {
	agentCancelYes = true
	defer func() { agentCancelYes = false }()

	srv := gqlServer(t, map[string]interface{}{
		"cancelTask": map[string]interface{}{"ok": true, "errors": []interface{}{}},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	if err := runAgentCancel(cmd, context.Background(), client, &config.Config{}, "task-aaa"); err != nil {
		t.Fatalf("runAgentCancel: %v", err)
	}
	if !strings.Contains(out.String(), "Task task-aaa cancelled.") {
		t.Errorf("missing success line:\n%s", out.String())
	}
}
