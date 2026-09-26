package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

func resetWorkflowGateFlags() {
	workflowGateRun = ""
	workflowGateDecision = ""
	workflowGateNote = ""
}

func pendingGateRow(overrides map[string]interface{}) map[string]interface{} {
	row := map[string]interface{}{
		"executionId":    "72",
		"workflowId":     "WorkflowDefinitionRunWorkflow-320",
		"runGuid":        "run-guid-1",
		"definitionSlug": "outreach-review",
		"definitionName": "Outreach Review",
		"stageRole":      "outreach review",
		"stageApprovers": []string{"team:gtm"},
		"startedAt":      "2026-09-20T10:00:00Z",
	}
	for k, v := range overrides {
		row[k] = v
	}
	return row
}

// ---- astro workflow gates ---------------------------------------------------

func TestWorkflowGatesListsPendingGates(t *testing.T) {
	resetWorkflowGateFlags()
	defer resetWorkflowGateFlags()

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		return map[string]interface{}{
			"pendingHumanGates": []map[string]interface{}{pendingGateRow(nil)},
		}, nil
	})

	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowGates(cmd, context.Background(), api.NewClient(srv.URL, "tok", false)); err != nil {
		t.Fatalf("gates: %v", err)
	}

	got := out.String()
	for _, want := range []string{"outreach-review", "outreach review", "team:gtm", "run-guid-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("gates output missing %q:\n%s", want, got)
		}
	}
}

func TestWorkflowGatesEmptyIsAClearMessage(t *testing.T) {
	resetWorkflowGateFlags()
	defer resetWorkflowGateFlags()

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		return map[string]interface{}{"pendingHumanGates": []map[string]interface{}{}}, nil
	})

	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowGates(cmd, context.Background(), api.NewClient(srv.URL, "tok", false)); err != nil {
		t.Fatalf("gates: %v", err)
	}
	if !strings.Contains(out.String(), "No pending gates.") {
		t.Errorf("expected the empty-state message, got:\n%s", out.String())
	}
}

func TestWorkflowGatesJSONRoundTrips(t *testing.T) {
	resetWorkflowGateFlags()
	defer resetWorkflowGateFlags()

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		return map[string]interface{}{
			"pendingHumanGates": []map[string]interface{}{pendingGateRow(nil)},
		}, nil
	})

	cmd, out, _ := workflowTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("setting --json: %v", err)
	}
	if err := runWorkflowGates(cmd, context.Background(), api.NewClient(srv.URL, "tok", false)); err != nil {
		t.Fatalf("gates --json: %v", err)
	}
	var got []pendingHumanGateRow
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding --json output: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0].ExecutionID != "72" || got[0].WorkflowID != "WorkflowDefinitionRunWorkflow-320" {
		t.Errorf("json lost the gate row: %+v", got)
	}
}

// ---- astro workflow gate -----------------------------------------------------

func TestWorkflowGateDecidesTheNewestMatchingDefinition(t *testing.T) {
	resetWorkflowGateFlags()
	defer resetWorkflowGateFlags()
	workflowGateDecision = "approve"
	workflowGateNote = "ship it"

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "pendingHumanGates"):
			// Newest first, as the server returns them: two runs of the
			// same definition, the first (newest) must win with no --run.
			return map[string]interface{}{
				"pendingHumanGates": []map[string]interface{}{
					pendingGateRow(map[string]interface{}{"executionId": "72", "runGuid": "run-newest"}),
					pendingGateRow(map[string]interface{}{"executionId": "9", "runGuid": "run-older"}),
				},
			}, nil
		default:
			return map[string]interface{}{"signalWorkflowInstance": okEnvelope()}, nil
		}
	})

	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowGate(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "outreach-review"); err != nil {
		t.Fatalf("gate: %v", err)
	}

	signals := srv.sent("signalWorkflowInstance")
	if len(signals) != 1 {
		t.Fatalf("expected one signal, got %d", len(signals))
	}
	if got := signals[0].Variables["workflowId"]; got != "WorkflowDefinitionRunWorkflow-320" {
		t.Errorf("workflowId = %v", got)
	}
	payload, ok := signals[0].Variables["payload"].(map[string]interface{})
	if !ok {
		t.Fatalf("payload variable missing or wrong shape: %v", signals[0].Variables["payload"])
	}
	if payload["execution_id"] != "72" {
		t.Errorf("execution_id = %v, want the newest run's 72 (not run-older's 9)", payload["execution_id"])
	}
	if payload["decision"] != "approved" {
		t.Errorf("decision = %v, want approved", payload["decision"])
	}
	if payload["note"] != "ship it" {
		t.Errorf("note = %v", payload["note"])
	}
	if !strings.Contains(out.String(), "run-newest") {
		t.Errorf("expected the decided run's guid in output:\n%s", out.String())
	}
}

func TestWorkflowGateHonorsRunFlag(t *testing.T) {
	resetWorkflowGateFlags()
	defer resetWorkflowGateFlags()
	workflowGateDecision = "reject"
	workflowGateRun = "run-older"

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "pendingHumanGates"):
			return map[string]interface{}{
				"pendingHumanGates": []map[string]interface{}{
					pendingGateRow(map[string]interface{}{"executionId": "72", "runGuid": "run-newest"}),
					pendingGateRow(map[string]interface{}{"executionId": "9", "runGuid": "run-older"}),
				},
			}, nil
		default:
			return map[string]interface{}{"signalWorkflowInstance": okEnvelope()}, nil
		}
	})

	cmd, _, _ := workflowTestCmd()
	if err := runWorkflowGate(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "outreach-review"); err != nil {
		t.Fatalf("gate --run run-older: %v", err)
	}

	signals := srv.sent("signalWorkflowInstance")
	payload := signals[0].Variables["payload"].(map[string]interface{})
	if payload["execution_id"] != "9" {
		t.Errorf("execution_id = %v, want run-older's 9", payload["execution_id"])
	}
	if payload["decision"] != "rejected" {
		t.Errorf("decision = %v, want rejected", payload["decision"])
	}
}

func TestWorkflowGateNoMatchIsAClearError(t *testing.T) {
	resetWorkflowGateFlags()
	defer resetWorkflowGateFlags()
	workflowGateDecision = "approve"

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		return map[string]interface{}{
			"pendingHumanGates": []map[string]interface{}{pendingGateRow(nil)},
		}, nil
	})

	cmd, _, _ := workflowTestCmd()
	err := runWorkflowGate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "no-such-workflow")
	if err == nil || !strings.Contains(err.Error(), "no pending human gate") {
		t.Fatalf("expected a no-match error, got %v", err)
	}
	if len(srv.sent("signalWorkflowInstance")) != 0 {
		t.Error("must not signal when no gate matched")
	}
}

func TestWorkflowGateRunFlagNarrowsToNothingIsAClearError(t *testing.T) {
	resetWorkflowGateFlags()
	defer resetWorkflowGateFlags()
	workflowGateDecision = "approve"
	workflowGateRun = "no-such-run"

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		return map[string]interface{}{
			"pendingHumanGates": []map[string]interface{}{pendingGateRow(nil)},
		}, nil
	})

	cmd, _, _ := workflowTestCmd()
	err := runWorkflowGate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "outreach-review")
	if err == nil || !strings.Contains(err.Error(), `run "no-such-run"`) {
		t.Fatalf("expected a run-specific no-match error, got %v", err)
	}
}

func TestWorkflowGateInvalidDecisionIsRejectedLocally(t *testing.T) {
	resetWorkflowGateFlags()
	defer resetWorkflowGateFlags()
	workflowGateDecision = "maybe"

	called := false
	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		called = true
		return map[string]interface{}{"pendingHumanGates": []map[string]interface{}{}}, nil
	})

	cmd, _, _ := workflowTestCmd()
	err := runWorkflowGate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "outreach-review")
	if err == nil || !strings.Contains(err.Error(), "--decision must be approve or reject") {
		t.Fatalf("expected a local validation error, got %v", err)
	}
	if called {
		t.Error("an invalid --decision must never reach the server")
	}
}

func TestNormalizeGateDecisionAcceptsBothTenses(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"approve", "approved"},
		{"Approved", "approved"},
		{"REJECT", "rejected"},
		{"rejected", "rejected"},
	} {
		got, err := normalizeGateDecision(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("normalizeGateDecision(%q) = %q, %v; want %q, nil", tc.in, got, err, tc.want)
		}
	}
	if _, err := normalizeGateDecision("maybe"); err == nil {
		t.Error("expected an error for an unrecognized decision")
	}
}

func TestWorkflowGateMutationFailureSurfaces(t *testing.T) {
	resetWorkflowGateFlags()
	defer resetWorkflowGateFlags()
	workflowGateDecision = "approve"

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "pendingHumanGates"):
			return map[string]interface{}{
				"pendingHumanGates": []map[string]interface{}{pendingGateRow(nil)},
			}, nil
		default:
			return map[string]interface{}{
				"signalWorkflowInstance": map[string]interface{}{
					"ok": false,
					"errors": []map[string]interface{}{
						{"field": "payload", "messages": []string{"only one of this gate's named approvers may decide it"}},
					},
				},
			}, nil
		}
	})

	cmd, _, _ := workflowTestCmd()
	err := runWorkflowGate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "outreach-review")
	if err == nil || !strings.Contains(err.Error(), "named approvers may decide it") {
		t.Fatalf("expected the server refusal surfaced, got %v", err)
	}
}

// ---- registration ------------------------------------------------------------

func TestWorkflowGateCommandsAreRegistered(t *testing.T) {
	want := map[string]bool{"gates": false, "gate": false}
	for _, c := range workflowCmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
			if c.Short == "" || c.Long == "" {
				t.Errorf("%s is missing help text", c.Name())
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("astro workflow %s is not registered", name)
		}
	}
}
