package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// resetDispatchFlags clears the package-level dispatch flags between tests.
func resetDispatchFlags() {
	agentDispatchInput = ""
	agentDispatchEnvSpec = ""
	agentDispatchTimeout = 0
	agentDispatchWait = false
	agentDispatchTail = false
	agentDispatchJSON = false
}

func TestAgentDispatchSendsMutationAndParsesTask(t *testing.T) {
	resetDispatchFlags()
	agentDispatchInput = `{"mode":"backfill","batches":3,"batch_size":10}`
	defer resetDispatchFlags()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"runAstroliftAgent": map[string]interface{}{
			"ok":     true,
			"errors": []interface{}{},
			"data":   map[string]interface{}{"id": "task-777", "status": "queued"},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	if err := runAgentDispatch(cmd, context.Background(), client, "emr-bug-triage"); err != nil {
		t.Fatalf("runAgentDispatch: %v", err)
	}

	if !strings.Contains(captured.Query, "runAstroliftAgent(input: $input)") {
		t.Errorf("query did not call runAstroliftAgent:\n%s", captured.Query)
	}
	input, ok := captured.Variables["input"].(map[string]interface{})
	if !ok {
		t.Fatalf("input var not an object: %v", captured.Variables["input"])
	}
	if input["agentSlug"] != "emr-bug-triage" {
		t.Errorf("agentSlug = %v, want emr-bug-triage", input["agentSlug"])
	}
	payload, ok := input["triggerPayload"].(map[string]interface{})
	if !ok || payload["mode"] != "backfill" || payload["batches"].(float64) != 3 {
		t.Errorf("triggerPayload = %v, want backfill/3", input["triggerPayload"])
	}
	got := out.String()
	if !strings.Contains(got, "task-777") || !strings.Contains(got, "queued") {
		t.Errorf("output missing task id/status:\n%s", got)
	}
}

func TestAgentDispatchNoPayloadOmitsTriggerPayload(t *testing.T) {
	resetDispatchFlags()
	defer resetDispatchFlags()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"runAstroliftAgent": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"id": "task-1", "status": "queued"},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := agentTestCmd()
	if err := runAgentDispatch(cmd, context.Background(), client, "flow-test"); err != nil {
		t.Fatalf("runAgentDispatch: %v", err)
	}
	input := captured.Variables["input"].(map[string]interface{})
	if _, present := input["triggerPayload"]; present {
		t.Errorf("triggerPayload should be omitted when --input is empty, got %v", input["triggerPayload"])
	}
}

func TestAgentDispatchResolvesEnvSpecSlugToID(t *testing.T) {
	resetDispatchFlags()
	agentDispatchEnvSpec = "emr-bug-triage"
	defer resetDispatchFlags()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"agentEnvironmentSpec": map[string]interface{}{"id": "spec-guid-9"},
		"runAstroliftAgent": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"id": "task-1", "status": "queued"},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := agentTestCmd()
	if err := runAgentDispatch(cmd, context.Background(), client, "emr-bug-triage"); err != nil {
		t.Fatalf("runAgentDispatch: %v", err)
	}
	// captured is the last request (the mutation); its input carries the GUID.
	input := captured.Variables["input"].(map[string]interface{})
	if input["environmentSpecId"] != "spec-guid-9" {
		t.Errorf("environmentSpecId = %v, want spec-guid-9", input["environmentSpecId"])
	}
}

func TestAgentDispatchSurfacesError(t *testing.T) {
	resetDispatchFlags()
	defer resetDispatchFlags()

	srv := gqlServer(t, map[string]interface{}{
		"runAstroliftAgent": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{"message": "agent not found"},
			},
			"data": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := agentTestCmd()
	err := runAgentDispatch(cmd, context.Background(), client, "nope")
	if err == nil || !strings.Contains(err.Error(), "agent not found") {
		t.Fatalf("expected 'agent not found' error, got %v", err)
	}
}

func TestAgentDispatchWaitPollsToTerminal(t *testing.T) {
	resetDispatchFlags()
	agentDispatchWait = true
	defer resetDispatchFlags()

	prev := agentDispatchPollIntervalForTest
	agentDispatchPollIntervalForTest = 5 * time.Millisecond
	defer func() { agentDispatchPollIntervalForTest = prev }()

	// The mutation and the status poll hit the same handler; return both keys.
	srv := gqlServer(t, map[string]interface{}{
		"runAstroliftAgent": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"id": "task-1", "status": "queued"},
		},
		"agentTask": map[string]interface{}{
			"id": "task-1", "status": "completed", "callbackUrl": "",
			"result": nil, "createdAt": "2026-07-23T00:00:00+00:00",
			"startedAt": nil, "finishedAt": nil,
			"vncEnabled": false, "vncUrl": "", "snapshotUrl": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	if err := runAgentDispatch(cmd, context.Background(), client, "emr-bug-triage"); err != nil {
		t.Fatalf("runAgentDispatch --wait: %v", err)
	}
	if !strings.Contains(out.String(), "Final status: completed") {
		t.Errorf("did not reach terminal completed:\n%s", out.String())
	}
}

func TestAgentDispatchWaitNonCompletedIsError(t *testing.T) {
	resetDispatchFlags()
	agentDispatchWait = true
	defer resetDispatchFlags()

	prev := agentDispatchPollIntervalForTest
	agentDispatchPollIntervalForTest = 5 * time.Millisecond
	defer func() { agentDispatchPollIntervalForTest = prev }()

	srv := gqlServer(t, map[string]interface{}{
		"runAstroliftAgent": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"id": "task-1", "status": "queued"},
		},
		"agentTask": map[string]interface{}{
			"id": "task-1", "status": "failed", "callbackUrl": "",
			"result": nil, "createdAt": "2026-07-23T00:00:00+00:00",
			"startedAt": nil, "finishedAt": nil,
			"vncEnabled": false, "vncUrl": "", "snapshotUrl": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := agentTestCmd()
	err := runAgentDispatch(cmd, context.Background(), client, "emr-bug-triage")
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("expected error for terminal 'failed', got %v", err)
	}
}

func TestParseJSONInputLiteralFileAndInvalid(t *testing.T) {
	// literal
	v, err := parseJSONInput(`{"a":1}`)
	if err != nil {
		t.Fatalf("literal: %v", err)
	}
	if v.(map[string]interface{})["a"].(float64) != 1 {
		t.Errorf("literal parse wrong: %v", v)
	}

	// @file
	dir := t.TempDir()
	f := filepath.Join(dir, "p.json")
	if err := os.WriteFile(f, []byte(`{"b":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err = parseJSONInput("@" + f)
	if err != nil {
		t.Fatalf("@file: %v", err)
	}
	if v.(map[string]interface{})["b"].(float64) != 2 {
		t.Errorf("@file parse wrong: %v", v)
	}

	// invalid
	if _, err := parseJSONInput("{not json"); err == nil {
		t.Error("expected error for invalid JSON")
	}
}
