package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const scheduleWorkflowGUID = "403c35c3-0091-42c2-a7f9-28dfb7f0be48"

func scheduleReceipt(active, confirmed bool, state string) map[string]interface{} {
	return map[string]interface{}{"workflowId": scheduleWorkflowGUID, "scheduleId": "workflow-" + scheduleWorkflowGUID, "configurationVersion": 7, "desiredRevision": strings.Repeat("a", 64), "desiredActive": active, "confirmed": confirmed, "observedState": state, "observedAt": "2026-10-05T08:00:00Z", "errorCode": "", "message": "", "engineCreatedAt": nil, "engineUpdatedAt": nil, "actionCount": nil}
}
func scheduleResult(ok, saved, active, confirmed bool, state string) map[string]interface{} {
	return map[string]interface{}{"ok": ok, "configurationSaved": saved, "errors": []interface{}{}, "schedule": scheduleReceipt(active, confirmed, state)}
}

func TestScheduleReviewRequiresExplicitVersionAndActivation(t *testing.T) {
	cases := []struct {
		args  []string
		valid bool
	}{
		{[]string{}, false}, {[]string{"--expected-version=7"}, false}, {[]string{"--expected-active=false"}, false},
		{[]string{"--expected-version=0", "--expected-active=true"}, false},
		{[]string{"--expected-version=2147483648", "--expected-active=true"}, false},
		{[]string{"--expected-version=7", "--expected-active=false"}, true},
		{[]string{"--expected-version=7", "--expected-active=true"}, true},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			group := newWorkflowScheduleCmd()
			cmd, _, err := group.Find([]string{"reconcile"})
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			vars, err := workflowScheduleReview(cmd, scheduleWorkflowGUID)
			if (err == nil) != tc.valid {
				t.Fatalf("vars=%v err=%v", vars, err)
			}
			if tc.valid && (vars["workflowId"] != scheduleWorkflowGUID || vars["expectedVersion"] != 7) {
				t.Fatalf("wrong reviewed variables: %v", vars)
			}
		})
	}
}

func TestScheduleInvalidInvocationFailsBeforeLoadingCredentials(t *testing.T) {
	for _, args := range [][]string{{"schedule", "inspect", "a-slug"}, {"schedule", "reconcile", scheduleWorkflowGUID}, {"update", scheduleWorkflowGUID}, {"update", scheduleWorkflowGUID, "--trigger=event"}} {
		root := &cobra.Command{Use: "astro", SilenceUsage: true, SilenceErrors: true}
		root.AddCommand(newWorkflowScheduleCmd(), newWorkflowUpdateCmd())
		root.SetArgs(args)
		if err := root.Execute(); err == nil || strings.Contains(err.Error(), "credentials") || strings.Contains(err.Error(), "login") {
			t.Fatalf("args=%v local validation=%v", args, err)
		}
	}
}

func TestScheduleInspectUsesExactIDAndPreservesJSON(t *testing.T) {
	receipt := scheduleReceipt(true, false, "unknown")
	receipt["errorCode"] = "engine_disabled"
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"workflowSchedule": receipt}, &captured)
	defer srv.Close()
	cmd, out, _ := workflowTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	err := runWorkflowScheduleInspect(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), scheduleWorkflowGUID)
	if err == nil || !strings.Contains(err.Error(), "engine_disabled") {
		t.Fatalf("error=%v", err)
	}
	if captured.Variables["workflowId"] != scheduleWorkflowGUID || strings.Contains(captured.Query, "slug:") {
		t.Fatalf("wrong lookup: %+v", captured)
	}
	var payload struct {
		Schedule workflowScheduleState `json:"workflowSchedule"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Schedule.ConfigurationVersion != 7 || payload.Schedule.WorkflowID != scheduleWorkflowGUID || payload.Schedule.Confirmed {
		t.Fatalf("lost observation: %+v", payload)
	}
}

func TestScheduleReconcileReturnsPartialResultOnce(t *testing.T) {
	result := scheduleResult(false, false, true, false, "unknown")
	result["schedule"].(map[string]interface{})["errorCode"] = "engine_timeout"
	result["errors"] = []interface{}{map[string]interface{}{"field": "schedule", "messages": []string{"Inspect and reconcile this workflow ID."}}}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"reconcileWorkflowSchedule": result}})
	}))
	defer srv.Close()
	for _, asJSON := range []bool{false, true} {
		cmd, out, _ := workflowTestCmd()
		if err := cmd.Flags().Set("json", fmt.Sprint(asJSON)); err != nil {
			t.Fatal(err)
		}
		err := runWorkflowScheduleReconcile(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), map[string]interface{}{"workflowId": scheduleWorkflowGUID, "expectedVersion": 7, "expectedActive": true})
		if err == nil || !strings.Contains(out.String(), scheduleWorkflowGUID) || !strings.Contains(out.String(), "engine_timeout") {
			t.Fatalf("error=%v output=%s", err, out.String())
		}
		if asJSON {
			var got workflowConfigurationResult
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Ok || got.Schedule.ConfigurationVersion != 7 {
				t.Fatalf("lost failure result: %+v", got)
			}
		}
	}
	if calls != 2 {
		t.Fatalf("mutation was retried: calls=%d", calls)
	}
}

func TestScheduleResultRefusesSuccessWithoutConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result map[string]interface{}
	}{
		{"missing receipt", map[string]interface{}{"ok": true, "configurationSaved": true}},
		{"engine drift", scheduleResult(true, true, true, false, "drifted")},
		{"inactive schedule still active", scheduleResult(true, true, false, true, "active")},
		{"not saved", scheduleResult(true, false, true, true, "active")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, _ := json.Marshal(tc.result)
			var result workflowConfigurationResult
			if err := json.Unmarshal(encoded, &result); err != nil {
				t.Fatal(err)
			}
			cmd, _, _ := workflowTestCmd()
			if err := reportWorkflowConfiguration(cmd, result, "update", scheduleWorkflowGUID); err == nil {
				t.Fatal("claimed success")
			}
		})
	}
}

func TestWorkflowUpdatePreservesExplicitFalseAndEmptyCron(t *testing.T) {
	cmd := newWorkflowUpdateCmd()
	if err := cmd.ParseFlags([]string{"--enabled=false", "--cron=", "--trigger=manual"}); err != nil {
		t.Fatal(err)
	}
	vars, err := workflowUpdateVariables(cmd, scheduleWorkflowGUID)
	if err != nil {
		t.Fatal(err)
	}
	if vars["isEnabled"] != false || vars["scheduleCron"] != "" || vars["triggerKind"] != "manual" {
		t.Fatalf("lost explicit values: %v", vars)
	}
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"updateWorkflow": scheduleResult(true, true, false, true, "missing")}, &captured)
	defer srv.Close()
	rendered, out, _ := workflowTestCmd()
	if err := rendered.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runWorkflowUpdate(rendered, context.Background(), api.NewClient(srv.URL, "tok", false), vars); err != nil {
		t.Fatal(err)
	}
	if captured.Variables["isEnabled"] != false || captured.Variables["workflowId"] != scheduleWorkflowGUID {
		t.Fatalf("wrong mutation: %+v", captured)
	}
	var result workflowConfigurationResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Schedule.Confirmed || result.Schedule.DesiredActive {
		t.Fatal("lost cleanup confirmation")
	}
}

func TestWorkflowCreateExactDraftRetainsFailureIdentity(t *testing.T) {
	oldID, oldEnabled := workflowCreateDefinitionID, workflowCreateEnabled
	defer func() { workflowCreateDefinitionID, workflowCreateEnabled = oldID, oldEnabled; workflowCreateName = "" }()
	workflowCreateDefinitionID = scheduleWorkflowGUID
	workflowCreateEnabled = false
	workflowCreateName = "Prepared"
	vars, err := workflowCreateVariables()
	if err != nil {
		t.Fatal(err)
	}
	if vars["definitionId"] != scheduleWorkflowGUID || vars["isEnabled"] != false {
		t.Fatalf("wrong create variables: %v", vars)
	}
	if _, ok := vars["definitionSlug"]; ok {
		t.Fatal("silently used a slug")
	}
	result := scheduleResult(false, true, true, false, "unknown")
	result["schedule"].(map[string]interface{})["errorCode"] = "engine_disabled"
	srv := gqlServer(t, map[string]interface{}{"createWorkflow": result}, nil)
	defer srv.Close()
	cmd, out, _ := workflowTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runWorkflowCreate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false)); err == nil {
		t.Fatal("claimed success")
	}
	var got workflowConfigurationResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ConfigurationSaved == nil || !*got.ConfigurationSaved || got.Schedule.WorkflowID != scheduleWorkflowGUID {
		t.Fatalf("lost saved identity: %+v", got)
	}
}

func TestWorkflowDeleteExactIDKeepsCleanupUncertainty(t *testing.T) {
	workflowDeleteYes = true
	defer func() { workflowDeleteYes = false }()
	var captured gqlRequest
	result := scheduleResult(false, true, false, false, "unknown")
	result["schedule"].(map[string]interface{})["errorCode"] = "engine_unavailable"
	srv := gqlServer(t, map[string]interface{}{"deleteWorkflow": result}, &captured)
	defer srv.Close()
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowDeleteResult(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), map[string]interface{}{"workflowId": scheduleWorkflowGUID}); err == nil {
		t.Fatal("claimed cleanup success")
	}
	if captured.Variables["workflowId"] != scheduleWorkflowGUID || captured.Variables["slug"] != nil {
		t.Fatalf("wrong identity: %+v", captured)
	}
	if strings.Contains(out.String(), "Deleted workflow:") || strings.Contains(out.String(), "torn down") || !strings.Contains(out.String(), "Configuration saved: true") {
		t.Fatalf("misleading output: %s", out.String())
	}
}

func TestScheduleSchemaMismatchDoesNotFallback(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"errors":[{"message":"Cannot query field 'workflowSchedule' on type 'Query'."}]}`)
	}))
	defer srv.Close()
	cmd, _, _ := workflowTestCmd()
	if err := runWorkflowScheduleInspect(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), scheduleWorkflowGUID); err == nil {
		t.Fatal("silently accepted old schema")
	}
	if calls != 1 {
		t.Fatalf("fallback calls=%d", calls)
	}
}

func TestScheduleIdentityComparisonAcceptsEquivalentGUIDs(t *testing.T) {
	if !sameWorkflowGUID(scheduleWorkflowGUID, strings.ToUpper(scheduleWorkflowGUID)) {
		t.Fatal("rejected an equivalent GUID")
	}
	if sameWorkflowGUID(scheduleWorkflowGUID, "a-slug") || sameWorkflowGUID(scheduleWorkflowGUID, "f6eb659b-4a5a-49a3-a031-e27e43183d0d") {
		t.Fatal("accepted another identity")
	}
}
