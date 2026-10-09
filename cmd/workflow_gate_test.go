package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const gateRunGUID = "11111111-1111-4111-8111-111111111111"
const gateStageGUID = "22222222-2222-4222-8222-222222222222"
const gateTemporalRun = "33333333-3333-4333-8333-333333333333"

func gateTestReview() gateReview {
	return gateReview{gateTarget: gateTarget{RunGUID: gateRunGUID, StageExecutionGUID: gateStageGUID, TemporalRunID: gateTemporalRun}, Decision: "approved", Note: "User chose this", Confirmed: true}
}
func gateStatePayload(state string) map[string]interface{} {
	recorded := interface{}(nil)
	if state == "recorded" {
		recorded = "approved"
	}
	return map[string]interface{}{"executionGuid": gateRunGUID, "stageExecutionGuid": gateStageGUID, "stageGuid": gateStageGUID,
		"temporalExecution": map[string]interface{}{"namespace": "default", "workflowId": "collection-child", "runId": gateTemporalRun},
		"requestState":      state, "stageStatus": "running", "runStatus": "running", "requestedDecision": "approved",
		"recordedDecision": recorded, "note": "User chose this", "decidedByMe": true, "observationError": ""}
}
func gateResultPayload(state string, ok bool) map[string]interface{} {
	return map[string]interface{}{"ok": ok, "gate": gateStatePayload(state), "errors": []interface{}{}}
}
func pendingGateRow() map[string]interface{} {
	return map[string]interface{}{"executionGuid": gateStageGUID, "stageGuid": gateStageGUID, "runGuid": gateRunGUID,
		"definitionSlug": "review", "stageRole": "editor", "stageApprovers": []string{"approver@example.test"},
		"temporalExecution": map[string]interface{}{"namespace": "default", "workflowId": "collection-child", "runId": gateTemporalRun}}
}
func TestWorkflowGatesTraversesEmptyFilteredPages(t *testing.T) {
	calls := 0
	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		calls++
		if !strings.Contains(req.Query, "pendingHumanGatesPage") {
			t.Fatalf("unexpected lookup %s", req.Query)
		}
		items := []map[string]interface{}{}
		var next interface{} = "next"
		if calls == 1 && req.Variables["after"] != nil {
			t.Fatalf("initial cursor %v", req.Variables)
		}
		if calls == 2 {
			if req.Variables["after"] != "next" {
				t.Fatalf("lost cursor %v", req.Variables)
			}
			items = append(items, pendingGateRow())
			next = nil
		}
		return map[string]interface{}{"pendingHumanGatesPage": map[string]interface{}{"items": items, "nextCursor": next}}, nil
	})
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowGates(cmd, context.Background(), api.NewClient(srv.URL, "tok", false)); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{gateRunGUID, gateStageGUID, gateTemporalRun, "editor"} {
		if !strings.Contains(out.String(), value) {
			t.Fatalf("missing %s: %s", value, out.String())
		}
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}
func TestWorkflowGatesJSONIncludesPublicIdentitiesAndLegacyUnboundRows(t *testing.T) {
	first, legacy := pendingGateRow(), pendingGateRow()
	legacy["temporalExecution"] = nil
	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		return map[string]interface{}{
			"pendingHumanGatesPage": map[string]interface{}{"items": []interface{}{first, legacy}, "nextCursor": nil}}, nil
	})
	cmd, out, _ := workflowTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runWorkflowGates(cmd, context.Background(), api.NewClient(srv.URL, "tok", false)); err != nil {
		t.Fatal(err)
	}
	var rows []pendingHumanGateRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ExecutionGUID != gateStageGUID || rows[1].TemporalExecution != nil {
		t.Fatalf("rows=%+v", rows)
	}
}
func TestWorkflowGatesRejectsRepeatedCursor(t *testing.T) {
	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		return map[string]interface{}{
			"pendingHumanGatesPage": map[string]interface{}{"items": []interface{}{}, "nextCursor": "same"}}, nil
	})
	cmd, _, _ := workflowTestCmd()
	if err := runWorkflowGates(cmd, context.Background(), api.NewClient(srv.URL, "tok", false)); err == nil {
		t.Fatal("accepted cursor loop")
	}
	if len(srv.sent("pendingHumanGatesPage")) != 2 {
		t.Fatal("unbounded pagination")
	}
}
func TestWorkflowGateInvalidReviewNeverLoadsCredentials(t *testing.T) {
	base := []string{"--run", gateRunGUID, "--stage", gateStageGUID, "--temporal-run", gateTemporalRun, "--decision", "approve"}
	for _, args := range [][]string{{"review"}, {"--decision", "approve", "--yes"}, base, {"--run", gateRunGUID, "--stage", "72", "--decision", "approve", "--yes"}} {
		root := &cobra.Command{Use: "astro", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newWorkflowGateCmd())
		root.SetArgs(append([]string{"gate"}, args...))
		if err := root.Execute(); err == nil || strings.Contains(err.Error(), "credentials") || strings.Contains(err.Error(), "login") {
			t.Fatalf("args=%v err=%v", args, err)
		}
	}
}
func TestWorkflowGateValidatedBeforeNetwork(t *testing.T) {
	cases := map[string]func(*gateReview){"no run": func(r *gateReview) { r.RunGUID = "" }, "no stage": func(r *gateReview) { r.StageExecutionGUID = "" },
		"no incarnation": func(r *gateReview) { r.TemporalRunID = "" }, "unconfirmed": func(r *gateReview) { r.Confirmed = false },
		"invalid decision": func(r *gateReview) { r.Decision = "maybe" }, "oversize": func(r *gateReview) { r.Note = strings.Repeat("é", 4097) }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
				t.Fatal("invalid review reached network")
				return nil, nil
			})
			review := gateTestReview()
			change(&review)
			cmd, _, _ := workflowTestCmd()
			if err := runWorkflowGate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), review); err == nil {
				t.Fatal("accepted invalid review")
			}
		})
	}
}
func TestWorkflowGateUsesExactDecisionAndDistinguishesRequestedFromRecorded(t *testing.T) {
	for _, state := range []string{"requested", "recorded"} {
		t.Run(state, func(t *testing.T) {
			srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
				return map[string]interface{}{"decideHumanGate": gateResultPayload(state, true)}, nil
			})
			cmd, out, _ := workflowTestCmd()
			review := gateTestReview()
			if err := runWorkflowGate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), review); err != nil {
				t.Fatal(err)
			}
			sent := srv.sent("decideHumanGate")
			if len(sent) != 1 || sent[0].Variables["executionId"] != gateRunGUID || sent[0].Variables["stageExecutionId"] != gateStageGUID || sent[0].Variables["temporalRunId"] != gateTemporalRun || sent[0].Variables["confirmed"] != true || sent[0].Variables["decision"] != "approved" {
				t.Fatalf("wrong request %+v", sent)
			}
			if len(srv.sent("signalWorkflowInstance")) != 0 || len(srv.sent("pendingHumanGates")) != 0 {
				t.Fatal("unexpected signal or newest lookup")
			}
			if !strings.Contains(out.String(), "Decision state: "+state) || (state == "requested" && strings.Contains(out.String(), "Recorded decision:")) {
				t.Fatal(out.String())
			}
		})
	}
}
func TestWorkflowGateFailureKeepsReceiptAndRecoveryIdentity(t *testing.T) {
	result := gateResultPayload("unknown", false)
	result["errors"] = []interface{}{map[string]interface{}{"field": "decision", "messages": []string{"delivery unknown"}}}
	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		return map[string]interface{}{"decideHumanGate": result}, nil
	})
	cmd, out, _ := workflowTestCmd()
	_ = cmd.Flags().Set("json", "true")
	err := runWorkflowGate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), gateTestReview())
	if err == nil || !strings.Contains(err.Error(), "gate-status --run "+gateRunGUID+" --stage "+gateStageGUID) {
		t.Fatalf("err=%v", err)
	}
	var got humanGateReceipt
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Gate == nil || got.Gate.RequestState != "unknown" || got.Target.TemporalRunID != gateTemporalRun || len(got.Errors) != 1 {
		t.Fatalf("receipt=%+v", got)
	}
	if len(srv.sent("decideHumanGate")) != 1 {
		t.Fatal("retried mutation")
	}
}
func TestWorkflowGateTransportFailureNeverFallsBack(t *testing.T) {
	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		return nil, []string{"Cannot query field decideHumanGate"}
	})
	cmd, out, _ := workflowTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runWorkflowGate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), gateTestReview()); err == nil {
		t.Fatal("old server accepted")
	}
	var got humanGateReceipt
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Gate != nil || got.Target.RunGUID != gateRunGUID || got.ClientError == "" {
		t.Fatalf("lost recovery target %+v", got)
	}
	if len(srv.sent("signalWorkflowInstance")) != 0 {
		t.Fatal("signal fallback")
	}
}
func TestWorkflowGateRejectsMismatchedReceipts(t *testing.T) {
	changes := map[string]func(map[string]interface{}){"run": func(g map[string]interface{}) { g["executionGuid"] = gateStageGUID },
		"stage": func(g map[string]interface{}) { g["stageExecutionGuid"] = gateRunGUID }, "incarnation": func(g map[string]interface{}) { g["temporalExecution"] = nil },
		"caller": func(g map[string]interface{}) { g["decidedByMe"] = false }, "decision": func(g map[string]interface{}) { g["requestedDecision"] = "rejected" },
		"note": func(g map[string]interface{}) { g["note"] = "other" }, "state": func(g map[string]interface{}) { g["requestState"] = "invented" },
		"outcome": func(g map[string]interface{}) { g["recordedDecision"] = "rejected" }}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			result := gateResultPayload("recorded", true)
			change(result["gate"].(map[string]interface{}))
			srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
				return map[string]interface{}{"decideHumanGate": result}, nil
			})
			cmd, _, _ := workflowTestCmd()
			if err := runWorkflowGate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), gateTestReview()); err == nil {
				t.Fatal("accepted wrong receipt")
			}
		})
	}
}
func TestWorkflowGateStatusOnlyReadsExactTarget(t *testing.T) {
	for _, state := range []string{"not_requested", "requested", "recorded", "closed", "unbound", "unknown"} {
		t.Run(state, func(t *testing.T) {
			srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
				return map[string]interface{}{"humanGateDecision": gateStatePayload(state)}, nil
			})
			cmd, out, _ := workflowTestCmd()
			_ = cmd.Flags().Set("json", "true")
			target := gateTestReview().gateTarget
			target.TemporalRunID = ""
			err := runWorkflowGateStatus(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), target)
			if (err != nil) != (state == "unknown") {
				t.Fatalf("state=%s err=%v", state, err)
			}
			var got humanGateReceipt
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Gate.RequestState != state {
				t.Fatal(out.String())
			}
			sent := srv.sent("humanGateDecision(")
			if len(sent) != 1 || sent[0].Variables["executionId"] != gateRunGUID || sent[0].Variables["stageExecutionId"] != gateStageGUID || len(srv.sent("mutation")) != 0 {
				t.Fatalf("wrong recovery request %+v", sent)
			}
		})
	}
}
func TestNormalizeGateDecisionAcceptsBothTenses(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"approve", "approved"}, {"Approved", "approved"}, {"REJECT", "rejected"}, {"rejected", "rejected"}} {
		got, err := normalizeGateDecision(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("%s: %s %v", tc.in, got, err)
		}
	}
}
func TestWorkflowGateCommandsAreRegistered(t *testing.T) {
	for _, name := range []string{"gates", "gate", "gate-status"} {
		found := false
		for _, c := range workflowCmd.Commands() {
			if c.Name() == name {
				found = true
				if c.Short == "" || c.Long == "" {
					t.Fatalf("missing help for %s", name)
				}
			}
		}
		if !found {
			t.Fatalf("missing %s", name)
		}
	}
}
