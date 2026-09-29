package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

const executionTestGUID = "019930ef-735d-7000-8000-000000000091"
const executionTestOrg = "019930ef-735d-7000-8000-000000000002"

func executionRow() map[string]interface{} {
	return map[string]interface{}{
		"guid": executionTestGUID, "recordId": "91", "organizationGuid": executionTestOrg,
		"definitionSlug": "review", "status": "running", "isTerminal": false,
		"temporalWorkflowId": "definition-run-91", "temporalRunId": "original-incarnation",
		"startedAt": "2026-09-12T09:00:00Z", "endedAt": nil, "failure": nil,
		"taskCleanup":      map[string]interface{}{"status": "not_requested", "remaining": 1, "retryable": false, "errors": []interface{}{}},
		"observationError": "",
	}
}

func executionTestClient(url string) *api.Client {
	client := api.NewClient(url, "test-token", false)
	client.SetOrg(executionTestOrg)
	return client
}

func TestWorkflowExecutionReadsExactRecordWithSelectedOrganization(t *testing.T) {
	for _, id := range []string{"91", executionTestGUID} {
		t.Run(id, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("X-Astrolift-Organization") != executionTestOrg || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("request lost its selected organization or authentication")
				}
				var req gqlRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.Variables["id"] != id || !strings.Contains(req.Query, "workflowExecution(executionId:") || strings.Contains(req.Query, "workflowRuns") {
					t.Errorf("expected a direct execution lookup: %+v", req)
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"workflowExecution": executionRow()}})
			}))
			defer srv.Close()
			cmd, out, _ := workflowTestCmd()
			_ = cmd.Flags().Set("json", "true")
			if err := runWorkflowExecution(cmd, context.Background(), executionTestClient(srv.URL), id, workflowExecutionOptions{}); err != nil {
				t.Fatal(err)
			}
			var row workflowExecution
			if err := json.Unmarshal(out.Bytes(), &row); err != nil || row.GUID != executionTestGUID || requests != 1 {
				t.Fatalf("unexpected execution output %s: %v", out, err)
			}
		})
	}
}

func TestWorkflowExecutionRejectsInvalidInputBeforeIO(t *testing.T) {
	srv := newRunControlServer(t, func(gqlRequest) (map[string]interface{}, []string) {
		t.Error("invalid execution request reached server")
		return nil, nil
	})
	for _, id := range []string{"", "latest", "0", "-1", "01", "１２", "9223372036854775808", "run-name"} {
		if _, err := fetchWorkflowExecution(context.Background(), executionTestClient(srv.URL), id, workflowExecutionOptions{}); err == nil {
			t.Errorf("accepted invalid ID %q", id)
		}
	}
	if _, err := fetchWorkflowExecution(context.Background(), api.NewClient(srv.URL, "tok", false), "91", workflowExecutionOptions{}); err == nil {
		t.Error("accepted unscoped lookup")
	}
	cmd, _, _ := workflowTestCmd()
	for _, opts := range []workflowExecutionOptions{{}, {Yes: true, Terminate: true}, {Yes: true, Terminate: true, Reason: "  "}, {Yes: true, Reason: "unexpected"}} {
		if err := runWorkflowExecutionControl(cmd, context.Background(), executionTestClient(srv.URL), "91", "cancel", opts); err == nil {
			t.Errorf("accepted invalid control options %+v", opts)
		}
	}
}

func TestWorkflowExecutionRefusesMismatchedOrUnavailableObservation(t *testing.T) {
	cases := []struct {
		name   string
		change func(map[string]interface{})
		opts   workflowExecutionOptions
	}{
		{"different record", func(r map[string]interface{}) { r["recordId"] = "92" }, workflowExecutionOptions{}},
		{"different organization", func(r map[string]interface{}) { r["organizationGuid"] = "other" }, workflowExecutionOptions{}},
		{"invalid GUID", func(r map[string]interface{}) { r["guid"] = "bad" }, workflowExecutionOptions{}},
		{"different workflow", func(r map[string]interface{}) {}, workflowExecutionOptions{WorkflowID: "original"}},
		{"different incarnation", func(r map[string]interface{}) {}, workflowExecutionOptions{RunID: "original"}},
		{"unavailable", func(r map[string]interface{}) { r["observationError"] = "Temporal unavailable" }, workflowExecutionOptions{}},
		{"missing execution ID", func(r map[string]interface{}) { r["temporalRunId"] = nil }, workflowExecutionOptions{}},
		{"inconsistent status", func(r map[string]interface{}) { r["isTerminal"] = true }, workflowExecutionOptions{}},
		{"unsupported status", func(r map[string]interface{}) { r["status"] = "continued_as_new" }, workflowExecutionOptions{}},
		{"inconsistent cleanup", func(r map[string]interface{}) {
			r["taskCleanup"] = map[string]interface{}{"status": "completed", "remaining": 1}
		}, workflowExecutionOptions{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := executionRow()
			tc.change(row)
			srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
				if strings.Contains(req.Query, "mutation") {
					t.Error("unverified observation reached mutation")
				}
				return map[string]interface{}{"workflowExecution": row}, nil
			})
			cmd, out, _ := workflowTestCmd()
			tc.opts.Yes = true
			if err := runWorkflowExecutionControl(cmd, context.Background(), executionTestClient(srv.URL), "91", "cancel", tc.opts); err == nil || out.Len() != 0 {
				t.Fatalf("must refuse before control: err=%v output=%s", err, out)
			}
		})
	}
}

func TestWorkflowExecutionControlPinsIdentityAndReportsAcknowledgement(t *testing.T) {
	for _, action := range []string{"cancel", "terminate", "cleanup"} {
		t.Run(action, func(t *testing.T) {
			row := executionRow()
			opts := workflowExecutionOptions{Yes: true}
			if action == "terminate" {
				opts.Terminate, opts.Reason = true, "wedged task"
			}
			if action == "cleanup" {
				row["status"], row["isTerminal"], row["endedAt"] = "cancelled", true, "2026-09-12T09:10:00Z"
				row["taskCleanup"] = map[string]interface{}{"status": "pending", "remaining": 1, "retryable": true, "errors": []interface{}{}}
			}
			srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
				if strings.Contains(req.Query, "mutation") {
					if req.Variables["id"] != executionTestGUID || req.Variables["workflow"] != "definition-run-91" || req.Variables["run"] != "original-incarnation" || req.Variables["action"] != action || req.Variables["reason"] != opts.Reason {
						t.Errorf("control did not pin original identity: %+v", req)
					}
					return map[string]interface{}{"controlWorkflowExecution": map[string]interface{}{"ok": true, "requested": true, "execution": row}}, nil
				}
				return map[string]interface{}{"workflowExecution": row}, nil
			})
			cmd, out, _ := workflowTestCmd()
			_ = cmd.Flags().Set("json", "true")
			if err := runWorkflowExecutionControl(cmd, context.Background(), executionTestClient(srv.URL), "91", action, opts); err != nil {
				t.Fatal(err)
			}
			var ack struct {
				Requested bool
				Action    string
				Execution workflowExecution
			}
			if err := json.Unmarshal(out.Bytes(), &ack); err != nil {
				t.Fatal(err)
			}
			if !ack.Requested || ack.Action != action || ack.Execution.TaskCleanup.Remaining != 1 || ack.Execution.IsTerminal != (action == "cleanup") || len(srv.sent("controlWorkflowExecution")) != 1 {
				t.Fatalf("acknowledgement fabricated completion: %s", out)
			}
		})
	}
}

func TestWorkflowExecutionControlRejectsWrongAcknowledgement(t *testing.T) {
	for _, field := range []string{"guid", "recordId", "organizationGuid", "temporalWorkflowId", "temporalRunId", "requested", "ok"} {
		t.Run(field, func(t *testing.T) {
			srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
				row := executionRow()
				if !strings.Contains(req.Query, "mutation") {
					return map[string]interface{}{"workflowExecution": row}, nil
				}
				ack := map[string]interface{}{"ok": true, "requested": true, "execution": row}
				if field == "requested" || field == "ok" {
					ack[field] = false
				} else {
					row[field] = "different"
				}
				return map[string]interface{}{"controlWorkflowExecution": ack}, nil
			})
			cmd, out, _ := workflowTestCmd()
			if err := runWorkflowExecutionControl(cmd, context.Background(), executionTestClient(srv.URL), "91", "cancel", workflowExecutionOptions{Yes: true}); err == nil || out.Len() != 0 {
				t.Fatalf("accepted wrong acknowledgement err=%v output=%s", err, out)
			}
		})
	}
}

func TestWorkflowExecutionWatchWaitsForCleanupAndRejectsReusedIdentity(t *testing.T) {
	previous := workflowExecutionPollInterval
	workflowExecutionPollInterval = time.Millisecond
	defer func() { workflowExecutionPollInterval = previous }()
	calls := 0
	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		calls++
		if calls > 1 && req.Variables["id"] != executionTestGUID {
			t.Errorf("watch did not adopt GUID: %+v", req)
		}
		row := executionRow()
		if calls > 1 {
			row["status"], row["isTerminal"], row["endedAt"] = "cancelled", true, "2026-09-12T09:10:00Z"
			row["taskCleanup"] = map[string]interface{}{"status": "completed", "remaining": 0}
		}
		switch calls {
		case 2:
			row["temporalRunId"] = "new-incarnation"
		case 3:
			return nil, []string{"temporarily unavailable"}
		case 4:
			row["taskCleanup"] = map[string]interface{}{"status": "pending", "remaining": 1, "retryable": true}
		case 5:
			row["observationError"] = "not verified"
		}
		return map[string]interface{}{"workflowExecution": row}, nil
	})
	cmd, out, _ := workflowTestCmd()
	_ = cmd.Flags().Set("json", "true")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runWorkflowExecution(cmd, ctx, executionTestClient(srv.URL), "91", workflowExecutionOptions{Watch: true}); err != nil {
		t.Fatal(err)
	}
	var row workflowExecution
	if err := json.Unmarshal(out.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if calls != 6 || row.TemporalRunID != "original-incarnation" || row.TaskCleanup.Status != "completed" || row.ObservationError != "" {
		t.Fatalf("watch settled early or followed another execution after %d calls: %s", calls, out)
	}
}

func TestWorkflowExecutionWatchCancellation(t *testing.T) {
	srv := newRunControlServer(t, func(gqlRequest) (map[string]interface{}, []string) {
		return map[string]interface{}{"workflowExecution": executionRow()}, nil
	})
	cmd, out, _ := workflowTestCmd()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := runWorkflowExecution(cmd, ctx, executionTestClient(srv.URL), "91", workflowExecutionOptions{Watch: true})
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") || !strings.Contains(out.String(), "Resource cleanup:") {
		t.Fatalf("watch did not stop with its caller: %v %s", err, out)
	}
}
