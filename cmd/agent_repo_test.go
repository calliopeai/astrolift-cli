package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

func TestAgentRegisterRepoHappyPath(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"registerAgentRepo": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"agents": []interface{}{
					map[string]interface{}{
						"manifestPath": "astrolift.toml",
						"slug":         "sample-agent",
						"appId":        "guid-1",
						"workloadSlug": "agent",
						"created":      true,
						"skillNotes":   []interface{}{"shell resolved from catalogue"},
					},
				},
			},
		},
	}, &captured)
	defer srv.Close()

	prev := agentRepoProjectID
	agentRepoProjectID = "proj-guid"
	defer func() { agentRepoProjectID = prev }()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runAgentRegisterRepo(cmd, context.Background(), client, "calliopeai/astrolift-sample-agent"); err != nil {
		t.Fatalf("register-repo: %v", err)
	}
	if !strings.Contains(captured.Query, "registerAgentRepo(input: $input)") {
		t.Errorf("did not call registerAgentRepo:\n%s", captured.Query)
	}
	input, _ := captured.Variables["input"].(map[string]interface{})
	if input["projectId"] != "proj-guid" || input["sourceRepo"] != "calliopeai/astrolift-sample-agent" {
		t.Errorf("input vars = %v", input)
	}
	if !strings.Contains(out.String(), "Agent sample-agent (workload agent, created)") ||
		!strings.Contains(out.String(), "note: shell resolved from catalogue") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
}

func TestAgentRegisterRepoSurfacesErrorEnvelope(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"registerAgentRepo": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{"field": "sourceRepo", "message": "repo not reachable"},
			},
			"data": nil,
		},
	}, nil)
	defer srv.Close()

	prev := agentRepoProjectID
	agentRepoProjectID = "proj-guid"
	defer func() { agentRepoProjectID = prev }()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _, _ := workflowTestCmd()
	err := runAgentRegisterRepo(cmd, context.Background(), client, "ghost/repo")
	if err == nil || !strings.Contains(err.Error(), "repo not reachable") {
		t.Fatalf("expected error envelope surfaced, got: %v", err)
	}
}
