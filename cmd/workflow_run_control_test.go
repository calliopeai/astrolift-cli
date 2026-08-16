package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// ---- harness ---------------------------------------------------------------

// runControlServer answers the resolve → list → mutate → re-list sequence a
// run-control command walks. The responder sees each request in order and
// returns the data payload (and optional GraphQL errors) for it; every
// request is recorded so a test can assert what was, and was not, sent.
type runControlServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []gqlRequest
}

func newRunControlServer(
	t *testing.T,
	respond func(req gqlRequest) (map[string]interface{}, []string),
) *runControlServer {
	t.Helper()
	s := &runControlServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req gqlRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.mu.Unlock()

		data, errs := respond(req)
		envelope := map[string]interface{}{"data": data}
		if len(errs) > 0 {
			messages := make([]map[string]interface{}, len(errs))
			for i, m := range errs {
				messages[i] = map[string]interface{}{"message": m}
			}
			envelope["errors"] = messages
		}
		_ = json.NewEncoder(w).Encode(envelope)
	}))
	t.Cleanup(s.Close)
	return s
}

// sent reports the requests whose operation text contains op.
func (s *runControlServer) sent(op string) []gqlRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []gqlRequest
	for _, r := range s.requests {
		if strings.Contains(r.Query, op) {
			out = append(out, r)
		}
	}
	return out
}

func resetRunControlFlags() {
	workflowRunCancelRun = ""
	workflowRunCancelTerminate = false
	workflowRunCancelReason = ""
	workflowRunCancelYes = false
	workflowRunShowRun = ""
}

// runningRun / completedRun are the two run shapes the commands branch on.
func runningRun(guid string) map[string]interface{} {
	return map[string]interface{}{
		"guid": guid, "currentState": "running",
		"temporalWorkflowId": "WorkflowDefinitionRunWorkflow-7", "temporalRunId": "temporal-run-7",
		"startedAt": "2026-08-15T10:00:00Z", "completedAt": nil, "isCompleted": false,
	}
}

func completedRun(guid, state string) map[string]interface{} {
	return map[string]interface{}{
		"guid": guid, "currentState": state,
		"temporalWorkflowId": "WorkflowDefinitionRunWorkflow-7", "temporalRunId": "temporal-run-7",
		"startedAt": "2026-08-15T10:00:00Z", "completedAt": "2026-08-15T10:04:00Z", "isCompleted": true,
	}
}

func workflowRow() map[string]interface{} {
	return map[string]interface{}{"guid": "wf-guid-1", "name": "Feature Dev", "slug": "feature-dev"}
}

func okEnvelope() map[string]interface{} {
	return map[string]interface{}{"ok": true, "errors": []interface{}{}}
}

// ---- run-cancel: refusals that never touch the server ----------------------

func TestWorkflowRunCancelRefusesWithoutYes(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()

	srv := newRunControlServer(t, func(gqlRequest) (map[string]interface{}, []string) {
		t.Error("run-cancel contacted the server without --yes")
		return nil, nil
	})

	cmd, _, _ := workflowTestCmd()
	err := runWorkflowRunCancel(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "feature-dev")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected a --yes refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("refusal should say nothing changed, got %q", err)
	}
}

func TestWorkflowRunCancelValidatesTerminateFlags(t *testing.T) {
	cases := []struct {
		name      string
		terminate bool
		reason    string
		want      string
	}{
		{"--terminate without a reason", true, "", "--terminate requires --reason"},
		{"--terminate with a blank reason", true, "   ", "--terminate requires --reason"},
		{"--reason without --terminate", false, "wedged", "--reason only applies with --terminate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetRunControlFlags()
			defer resetRunControlFlags()
			workflowRunCancelTerminate = tc.terminate
			workflowRunCancelReason = tc.reason
			// --yes is set so the flag validation, not the confirmation, is
			// what the assertion proves.
			workflowRunCancelYes = true

			srv := newRunControlServer(t, func(gqlRequest) (map[string]interface{}, []string) {
				t.Error("flag validation must happen before any request")
				return nil, nil
			})
			cmd, _, _ := workflowTestCmd()
			err := runWorkflowRunCancel(cmd, context.Background(),
				api.NewClient(srv.URL, "tok", false), "feature-dev")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}

// ---- run-cancel: the terminal-run refusal ----------------------------------

func TestWorkflowRunCancelRefusesATerminalRun(t *testing.T) {
	for _, state := range []string{"completed", "failed"} {
		t.Run(state, func(t *testing.T) {
			resetRunControlFlags()
			defer resetRunControlFlags()
			workflowRunCancelYes = true

			srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
				switch {
				case strings.Contains(req.Query, "workflowRuns"):
					return map[string]interface{}{
						"workflowRuns": []map[string]interface{}{completedRun("run-1", state)},
					}, nil
				default:
					return map[string]interface{}{"workflow": workflowRow()}, nil
				}
			})

			cmd, _, _ := workflowTestCmd()
			err := runWorkflowRunCancel(cmd, context.Background(),
				api.NewClient(srv.URL, "tok", false), "feature-dev")
			if err == nil || !strings.Contains(err.Error(), "already "+state) {
				t.Fatalf("expected an already-%s refusal, got %v", state, err)
			}
			// The refusal must be local: nothing may reach the mutation.
			if got := srv.sent("cancelWorkflowInstance"); len(got) != 0 {
				t.Errorf("cancel was sent for a terminal run: %+v", got)
			}
		})
	}
}

func TestWorkflowRunCancelRefusesARunWithNoTemporalID(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()
	workflowRunCancelYes = true

	run := runningRun("run-1")
	run["temporalWorkflowId"] = nil

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		if strings.Contains(req.Query, "workflowRuns") {
			return map[string]interface{}{"workflowRuns": []map[string]interface{}{run}}, nil
		}
		return map[string]interface{}{"workflow": workflowRow()}, nil
	})

	cmd, _, _ := workflowTestCmd()
	err := runWorkflowRunCancel(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev")
	if err == nil || !strings.Contains(err.Error(), "no Temporal workflow id") {
		t.Fatalf("expected a missing-temporal-id refusal, got %v", err)
	}
	if got := srv.sent("cancelWorkflowInstance"); len(got) != 0 {
		t.Errorf("cancel was sent without a temporal id: %+v", got)
	}
}

// ---- run-cancel: the happy paths -------------------------------------------

func TestWorkflowRunCancelSendsMutationAndReportsTheFreshState(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()
	workflowRunCancelYes = true

	listCalls := 0
	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "cancelWorkflowInstance"):
			return map[string]interface{}{"cancelWorkflowInstance": okEnvelope()}, nil
		case strings.Contains(req.Query, "workflowRuns"):
			listCalls++
			// The re-read after the mutation must be what is reported, so
			// the second list answers with the post-cancel state.
			if listCalls == 1 {
				return map[string]interface{}{
					"workflowRuns": []map[string]interface{}{runningRun("run-1")},
				}, nil
			}
			return map[string]interface{}{
				"workflowRuns": []map[string]interface{}{completedRun("run-1", "cancelled")},
			}, nil
		default:
			return map[string]interface{}{"workflow": workflowRow()}, nil
		}
	})

	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowRunCancel(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev"); err != nil {
		t.Fatalf("run-cancel: %v", err)
	}

	mutations := srv.sent("cancelWorkflowInstance")
	if len(mutations) != 1 {
		t.Fatalf("expected exactly one cancel, got %d", len(mutations))
	}
	if got := mutations[0].Variables["workflowId"]; got != "WorkflowDefinitionRunWorkflow-7" {
		t.Errorf("cancel keyed on %v, want the run's temporal workflow id", got)
	}
	if _, ok := mutations[0].Variables["reason"]; ok {
		t.Error("a cooperative cancel must not send a reason")
	}
	if listCalls != 2 {
		t.Errorf("expected a re-read after the mutation, got %d list calls", listCalls)
	}

	got := out.String()
	for _, want := range []string{"Cancel requested", "run-1", "WorkflowDefinitionRunWorkflow-7", "cancelled"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// A run that came back terminal is done; don't tell the operator to wait.
	if strings.Contains(got, "asynchronously") {
		t.Errorf("terminal run should not print the async caveat:\n%s", got)
	}
}

func TestWorkflowRunCancelStillPendingPrintsTheAsyncCaveat(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()
	workflowRunCancelYes = true

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "cancelWorkflowInstance"):
			return map[string]interface{}{"cancelWorkflowInstance": okEnvelope()}, nil
		case strings.Contains(req.Query, "workflowRuns"):
			return map[string]interface{}{
				"workflowRuns": []map[string]interface{}{runningRun("run-1")},
			}, nil
		default:
			return map[string]interface{}{"workflow": workflowRow()}, nil
		}
	})

	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowRunCancel(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev"); err != nil {
		t.Fatalf("run-cancel: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "State:             running") {
		t.Errorf("expected the still-running state, got:\n%s", got)
	}
	if !strings.Contains(got, "delivered asynchronously") {
		t.Errorf("a cancel that has not landed must say so:\n%s", got)
	}
}

func TestWorkflowRunCancelTerminateSendsTheReason(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()
	workflowRunCancelYes = true
	workflowRunCancelTerminate = true
	workflowRunCancelReason = "  wedged on stage 3  "

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "terminateWorkflowInstance"):
			return map[string]interface{}{"terminateWorkflowInstance": okEnvelope()}, nil
		case strings.Contains(req.Query, "workflowRuns"):
			return map[string]interface{}{
				"workflowRuns": []map[string]interface{}{runningRun("run-1")},
			}, nil
		default:
			return map[string]interface{}{"workflow": workflowRow()}, nil
		}
	})

	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowRunCancel(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev"); err != nil {
		t.Fatalf("run-cancel --terminate: %v", err)
	}
	if got := srv.sent("cancelWorkflowInstance("); len(got) != 0 {
		t.Errorf("--terminate must not call the cooperative cancel: %+v", got)
	}
	mutations := srv.sent("terminateWorkflowInstance")
	if len(mutations) != 1 {
		t.Fatalf("expected exactly one terminate, got %d", len(mutations))
	}
	if got := mutations[0].Variables["reason"]; got != "wedged on stage 3" {
		t.Errorf("reason sent as %q, want it trimmed", got)
	}
	if got := out.String(); !strings.Contains(got, "Terminate requested") {
		t.Errorf("output should name the terminate path:\n%s", got)
	}
}

func TestWorkflowRunCancelJSONReportsTheRequestAndState(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()
	workflowRunCancelYes = true

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "cancelWorkflowInstance"):
			return map[string]interface{}{"cancelWorkflowInstance": okEnvelope()}, nil
		case strings.Contains(req.Query, "workflowRuns"):
			return map[string]interface{}{
				"workflowRuns": []map[string]interface{}{runningRun("run-1")},
			}, nil
		default:
			return map[string]interface{}{"workflow": workflowRow()}, nil
		}
	})

	cmd, out, _ := workflowTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("setting --json: %v", err)
	}
	if err := runWorkflowRunCancel(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev"); err != nil {
		t.Fatalf("run-cancel --json: %v", err)
	}

	var got workflowRunCancelResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding --json output: %v\n%s", err, out.String())
	}
	if got.Mode != "cancel" || !got.Requested {
		t.Errorf("expected a requested cancel, got %+v", got)
	}
	if got.Run != "run-1" || got.TemporalWorkflowID != "WorkflowDefinitionRunWorkflow-7" {
		t.Errorf("json lost the run identity: %+v", got)
	}
	// The IDE reads state to decide whether to keep polling.
	if got.State != "running" || got.IsCompleted {
		t.Errorf("json must carry the observed state, got %+v", got)
	}
	if got.Reason != "" {
		t.Errorf("a cancel carries no reason, got %q", got.Reason)
	}
}

// ---- run-cancel: RBAC + run selection --------------------------------------

func TestWorkflowRunCancelSurfacesAPermissionDenial(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()
	workflowRunCancelYes = true

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "cancelWorkflowInstance"):
			// core.permissions renders PermissionDenied as
			// "<reason>: <permission>" and it arrives as a GraphQL error,
			// not as an ok=false envelope.
			return nil, []string{"no role binding grants this permission: workflow.trigger"}
		case strings.Contains(req.Query, "workflowRuns"):
			return map[string]interface{}{
				"workflowRuns": []map[string]interface{}{runningRun("run-1")},
			}, nil
		default:
			return map[string]interface{}{"workflow": workflowRow()}, nil
		}
	})

	cmd, _, _ := workflowTestCmd()
	err := runWorkflowRunCancel(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev")
	if err == nil {
		t.Fatal("expected the denial to fail the command")
	}
	if !strings.Contains(err.Error(), "not permitted") {
		t.Errorf("denial must read as not permitted, got %q", err)
	}
	if !strings.Contains(err.Error(), "workflow.trigger") {
		t.Errorf("denial should name the permission, got %q", err)
	}
}

func TestWorkflowRunCancelNotFoundEnvelopeIsReported(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()
	workflowRunCancelYes = true

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "cancelWorkflowInstance"):
			// A foreign org's run, a legacy org-less run, and a nonexistent
			// id all answer with this one envelope (no existence oracle).
			return map[string]interface{}{"cancelWorkflowInstance": map[string]interface{}{
				"ok": false,
				"errors": []map[string]interface{}{
					{"field": "workflow_id", "messages": []string{"workflow instance not found"}},
				},
			}}, nil
		case strings.Contains(req.Query, "workflowRuns"):
			return map[string]interface{}{
				"workflowRuns": []map[string]interface{}{runningRun("run-1")},
			}, nil
		default:
			return map[string]interface{}{"workflow": workflowRow()}, nil
		}
	})

	cmd, _, _ := workflowTestCmd()
	err := runWorkflowRunCancel(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev")
	if err == nil || !strings.Contains(err.Error(), "workflow instance not found") {
		t.Fatalf("expected the not-found envelope surfaced, got %v", err)
	}
}

func TestWorkflowRunCancelSelectsTheRequestedRun(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()
	workflowRunCancelYes = true
	workflowRunCancelRun = "run-older"

	older := runningRun("run-older")
	older["temporalWorkflowId"] = "WorkflowDefinitionRunWorkflow-2"

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "cancelWorkflowInstance"):
			return map[string]interface{}{"cancelWorkflowInstance": okEnvelope()}, nil
		case strings.Contains(req.Query, "workflowRuns"):
			return map[string]interface{}{"workflowRuns": []map[string]interface{}{
				runningRun("run-newest"), older,
			}}, nil
		default:
			return map[string]interface{}{"workflow": workflowRow()}, nil
		}
	})

	cmd, _, _ := workflowTestCmd()
	if err := runWorkflowRunCancel(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev"); err != nil {
		t.Fatalf("run-cancel --run: %v", err)
	}
	mutations := srv.sent("cancelWorkflowInstance")
	if len(mutations) != 1 {
		t.Fatalf("expected one cancel, got %d", len(mutations))
	}
	// Without --run this would have stopped run-newest instead.
	if got := mutations[0].Variables["workflowId"]; got != "WorkflowDefinitionRunWorkflow-2" {
		t.Errorf("--run selected the wrong run: keyed on %v", got)
	}
}

func TestWorkflowRunCancelUnknownRunGUIDFails(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()
	workflowRunCancelYes = true
	workflowRunCancelRun = "run-does-not-exist"

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		if strings.Contains(req.Query, "workflowRuns") {
			return map[string]interface{}{
				"workflowRuns": []map[string]interface{}{runningRun("run-1")},
			}, nil
		}
		return map[string]interface{}{"workflow": workflowRow()}, nil
	})

	cmd, _, _ := workflowTestCmd()
	err := runWorkflowRunCancel(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev")
	if err == nil || !strings.Contains(err.Error(), "not found on this workflow") {
		t.Fatalf("expected an unknown-run error, got %v", err)
	}
	if got := srv.sent("cancelWorkflowInstance"); len(got) != 0 {
		t.Errorf("nothing may be cancelled when the run does not resolve: %+v", got)
	}
}

// ---- run-show ---------------------------------------------------------------

// gateStages is a run mid-flight: a finished agent stage, then a human gate
// sitting open on two approvers.
func gateStages() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"stageOrder": 0, "stageKind": "agent_dispatch", "stageRole": "implementer",
			"stageApprovers": []string{}, "status": "completed", "attemptNumber": 2,
			"humanGateState": "", "humanGateNote": "",
			"startedAt": "2026-08-15T10:00:00Z", "endedAt": "2026-08-15T10:02:00Z",
			"errorMessage": "",
		},
		{
			"stageOrder": 1, "stageKind": "human_gate", "stageRole": "reviewer",
			"stageApprovers": []string{"team-leads", "sre"}, "status": "running", "attemptNumber": 1,
			"humanGateState": "pending", "humanGateNote": "",
			"startedAt": "2026-08-15T10:02:00Z", "endedAt": nil,
			"errorMessage": "",
		},
	}
}

func TestWorkflowRunShowRendersStagesAndTheOpenGate(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "workflowStageExecutions"):
			return map[string]interface{}{"workflowStageExecutions": gateStages()}, nil
		case strings.Contains(req.Query, "workflowRuns"):
			return map[string]interface{}{
				"workflowRuns": []map[string]interface{}{runningRun("run-1")},
			}, nil
		default:
			return map[string]interface{}{"workflow": workflowRow()}, nil
		}
	})

	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowRunShow(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev"); err != nil {
		t.Fatalf("run-show: %v", err)
	}

	stageQueries := srv.sent("workflowStageExecutions")
	if len(stageQueries) != 1 {
		t.Fatalf("expected one stage query, got %d", len(stageQueries))
	}
	// Both Temporal ids key the reader; the run guid does not.
	if got := stageQueries[0].Variables["workflowId"]; got != "WorkflowDefinitionRunWorkflow-7" {
		t.Errorf("stage query workflowId = %v", got)
	}
	if got := stageQueries[0].Variables["runId"]; got != "temporal-run-7" {
		t.Errorf("stage query runId = %v", got)
	}

	got := out.String()
	for _, want := range []string{
		"Run:       run-1",
		"State:     running",
		"STAGE", "KIND", "ROLE", "GATE",
		"agent_dispatch", "implementer",
		"human_gate", "reviewer", "pending",
		"waiting on approval from team-leads, sre",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("run-show output missing %q:\n%s", want, got)
		}
	}
	// A non-gate stage must not be labelled with a gate state.
	if strings.Contains(got, "agent_dispatch  implementer  completed  2  pending") {
		t.Errorf("agent stage carries a gate state:\n%s", got)
	}
}

func TestWorkflowRunShowJSONCarriesTheGateShape(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "workflowStageExecutions"):
			return map[string]interface{}{"workflowStageExecutions": gateStages()}, nil
		case strings.Contains(req.Query, "workflowRuns"):
			return map[string]interface{}{
				"workflowRuns": []map[string]interface{}{runningRun("run-1")},
			}, nil
		default:
			return map[string]interface{}{"workflow": workflowRow()}, nil
		}
	})

	cmd, out, _ := workflowTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("setting --json: %v", err)
	}
	if err := runWorkflowRunShow(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev"); err != nil {
		t.Fatalf("run-show --json: %v", err)
	}

	var got workflowRunShowResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding --json output: %v\n%s", err, out.String())
	}
	if got.Run.GUID != "run-1" || got.Run.CurrentState != "running" {
		t.Errorf("json lost the run header: %+v", got.Run)
	}
	if len(got.Stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(got.Stages))
	}
	gate := got.Stages[1]
	// This is the whole point of the verb: the IDE reads "waiting, on whom"
	// off the platform instead of inferring it.
	if gate.HumanGateState != "pending" {
		t.Errorf("gate state = %q, want pending", gate.HumanGateState)
	}
	if strings.Join(gate.StageApprovers, ",") != "team-leads,sre" {
		t.Errorf("gate approvers = %v", gate.StageApprovers)
	}
	if gate.StageRole != "reviewer" || gate.StageKind != "human_gate" {
		t.Errorf("gate stage lost kind/role: %+v", gate)
	}
	if got.Stages[0].HumanGateState != "" {
		t.Errorf("agent stage must carry no gate state, got %q", got.Stages[0].HumanGateState)
	}
}

func TestWorkflowRunShowReportsADecidedGate(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()

	expired := []map[string]interface{}{{
		"stageOrder": 0, "stageKind": "human_gate", "stageRole": "reviewer",
		"stageApprovers": []string{"team-leads"}, "status": "failed", "attemptNumber": 1,
		"humanGateState": "rejected", "humanGateNote": "gate timed out",
		"startedAt": "2026-08-15T10:00:00Z", "endedAt": "2026-08-16T10:00:00Z",
		"errorMessage": "human gate rejected",
	}}

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		switch {
		case strings.Contains(req.Query, "workflowStageExecutions"):
			return map[string]interface{}{"workflowStageExecutions": expired}, nil
		case strings.Contains(req.Query, "workflowRuns"):
			return map[string]interface{}{
				"workflowRuns": []map[string]interface{}{completedRun("run-1", "failed")},
			}, nil
		default:
			return map[string]interface{}{"workflow": workflowRow()}, nil
		}
	})

	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowRunShow(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev"); err != nil {
		t.Fatalf("run-show: %v", err)
	}
	got := out.String()
	// A timed-out gate is recorded as a rejection; the note is what
	// distinguishes it from a human saying no.
	if !strings.Contains(got, "gate rejected: gate timed out") {
		t.Errorf("expected the gate note rendered:\n%s", got)
	}
	if !strings.Contains(got, "error: human gate rejected") {
		t.Errorf("expected the stage error rendered:\n%s", got)
	}
	if strings.Contains(got, "waiting on approval") {
		t.Errorf("a decided gate must not read as waiting:\n%s", got)
	}
}

func TestWorkflowRunShowWithoutATemporalRunIDSkipsTheStageQuery(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()

	pending := runningRun("run-1")
	pending["temporalRunId"] = nil

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		if strings.Contains(req.Query, "workflowRuns") {
			return map[string]interface{}{"workflowRuns": []map[string]interface{}{pending}}, nil
		}
		return map[string]interface{}{"workflow": workflowRow()}, nil
	})

	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowRunShow(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev"); err != nil {
		t.Fatalf("run-show: %v", err)
	}
	// Both ids key the reader, so a half-keyed query would just error.
	if got := srv.sent("workflowStageExecutions"); len(got) != 0 {
		t.Errorf("stage query sent without a run id: %+v", got)
	}
	if got := out.String(); !strings.Contains(got, "no Temporal run id yet") {
		t.Errorf("expected the missing-run-id explanation:\n%s", got)
	}
}

func TestWorkflowRunShowRequestsTheTemporalRunID(t *testing.T) {
	// workflowRunsQuery (used by `runs`) does not select temporalRunId, and
	// without it run-show can never reach the stage reader.
	if !strings.Contains(workflowRunsControlQuery, "temporalRunId") {
		t.Fatalf("run-control run query must select temporalRunId:\n%s", workflowRunsControlQuery)
	}
	for _, field := range []string{"stageRole", "stageApprovers", "humanGateState", "humanGateNote"} {
		if !strings.Contains(workflowStageExecutionsQuery, field) {
			t.Errorf("stage query must select %s:\n%s", field, workflowStageExecutionsQuery)
		}
	}
}

func TestWorkflowRunShowNoRunsIsAClearError(t *testing.T) {
	resetRunControlFlags()
	defer resetRunControlFlags()

	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		if strings.Contains(req.Query, "workflowRuns") {
			return map[string]interface{}{"workflowRuns": []map[string]interface{}{}}, nil
		}
		return map[string]interface{}{"workflow": workflowRow()}, nil
	})

	cmd, _, _ := workflowTestCmd()
	err := runWorkflowRunShow(cmd, context.Background(),
		api.NewClient(srv.URL, "tok", false), "feature-dev")
	if err == nil || !strings.Contains(err.Error(), "no runs yet") {
		t.Fatalf("expected a no-runs error, got %v", err)
	}
}

// ---- registration ------------------------------------------------------------

func TestWorkflowRunControlCommandsAreRegistered(t *testing.T) {
	want := map[string]bool{"run-cancel": false, "run-show": false}
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
