package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func executionStage(index int) map[string]interface{} {
	return map[string]interface{}{
		"guid": fmt.Sprintf("019930ef-735d-7000-8000-%012d", index), "executionId": fmt.Sprint(index),
		"stageGuid": "019930ef-735d-7000-8000-000000000100", "stageOrder": 1, "stageKind": "human_gate",
		"stageRole": "Reviewer", "stageApprovers": []string{"maintainers"}, "status": "running",
		"attemptNumber": index, "humanGateState": "pending", "humanGateNote": "", "errorMessage": "",
	}
}

func executionStagePage(items []interface{}, next interface{}) map[string]interface{} {
	return map[string]interface{}{
		"executionGuid": executionTestGUID, "recordId": "91", "organizationGuid": executionTestOrg,
		"temporalWorkflowId": "definition-run-91", "temporalRunId": "original-incarnation",
		"stages": map[string]interface{}{"items": items, "nextCursor": next},
	}
}

func TestWorkflowExecutionStagesConsumesAllPagesAndKeepsRecordedHistoryWhenTemporalUnavailable(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprint(asJSON), func(t *testing.T) {
			pages := 0
			srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
				if !strings.Contains(req.Query, "workflowExecutionStages") {
					row := executionRow()
					row["observationError"] = "Temporal unavailable"
					return map[string]interface{}{"workflowExecution": row}, nil
				}
				pages++
				if req.Variables["id"] != executionTestGUID || req.Variables["limit"] != float64(100) {
					t.Errorf("stage read lost adopted execution: %+v", req)
				}
				var next interface{}
				items := []interface{}{}
				if pages == 1 {
					if req.Variables["after"] != nil {
						t.Error("first page must have no cursor")
					}
					for i := 1; i <= 100; i++ {
						items = append(items, executionStage(i))
					}
					next = "second-page"
				} else {
					if req.Variables["after"] != "second-page" || pages != 2 {
						t.Errorf("unexpected cursor or page count: %+v", req)
					}
					stage := executionStage(101)
					stage["humanGateState"], stage["humanGateNote"], stage["errorMessage"] = "rejected", "Needs revision", "approval rejected"
					stage["childWorkflowRunGuid"] = executionTestGUID
					items = append(items, stage)
				}
				return map[string]interface{}{"workflowExecutionStages": executionStagePage(items, next)}, nil
			})
			cmd, out, _ := workflowTestCmd()
			_ = cmd.Flags().Set("json", fmt.Sprint(asJSON))
			err := runWorkflowExecutionStages(cmd, context.Background(), executionTestClient(srv.URL), "91", workflowExecutionOptions{RunID: "original-incarnation"})
			if err != nil || pages != 2 {
				t.Fatalf("inspection failed: %v (%d pages)", err, pages)
			}
			if asJSON {
				var reply struct {
					Execution workflowExecution
					Stages    []exactWorkflowStage
				}
				if err := json.Unmarshal(out.Bytes(), &reply); err != nil || len(reply.Stages) != 101 || reply.Execution.ObservationError == "" || reply.Stages[100].HumanGateNote != "Needs revision" {
					t.Fatalf("lost recorded stage details: %v: %s", err, out)
				}
			} else {
				for _, text := range []string{"Temporal unavailable", "Resource cleanup", "maintainers", "Needs revision", "approval rejected", "child workflow:", "attempt 101"} {
					if !strings.Contains(out.String(), text) {
						t.Errorf("missing %q in stage output", text)
					}
				}
			}
		})
	}
}

func TestWorkflowExecutionStagesRefusesChangedIdentityOnEveryPage(t *testing.T) {
	for _, field := range []string{"executionGuid", "recordId", "organizationGuid", "temporalWorkflowId", "temporalRunId"} {
		for _, onPage := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/page%d", field, onPage), func(t *testing.T) {
				pages := 0
				srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
					if !strings.Contains(req.Query, "workflowExecutionStages") {
						return map[string]interface{}{"workflowExecution": executionRow()}, nil
					}
					pages++
					page := executionStagePage([]interface{}{executionStage(pages)}, "next")
					if pages == onPage {
						page[field] = "different"
					}
					return map[string]interface{}{"workflowExecutionStages": page}, nil
				})
				cmd, out, _ := workflowTestCmd()
				err := runWorkflowExecutionStages(cmd, context.Background(), executionTestClient(srv.URL), "91", workflowExecutionOptions{})
				if err == nil || out.Len() != 0 || pages != onPage {
					t.Fatalf("accepted changed execution: %v pages=%d output=%s", err, pages, out)
				}
			})
		}
	}
}

func TestWorkflowExecutionStagesRejectsBrokenPaginationWithoutPartialOutput(t *testing.T) {
	for _, problem := range []string{"missing execution", "missing page", "missing items", "missing cursor", "bad cursor", "empty cursor", "empty continuing page", "repeated cursor", "repeated stage", "invalid stage", "transport"} {
		t.Run(problem, func(t *testing.T) {
			pages := 0
			srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
				if !strings.Contains(req.Query, "workflowExecutionStages") {
					return map[string]interface{}{"workflowExecution": executionRow()}, nil
				}
				pages++
				page := executionStagePage([]interface{}{executionStage(pages)}, "next")
				content := page["stages"].(map[string]interface{})
				switch problem {
				case "missing execution":
					page = nil
				case "missing page":
					delete(page, "stages")
				case "missing items":
					delete(content, "items")
				case "missing cursor":
					delete(content, "nextCursor")
				case "bad cursor":
					content["nextCursor"] = 4
				case "empty cursor":
					content["nextCursor"] = ""
				case "empty continuing page":
					content["items"] = []interface{}{}
				case "repeated cursor":
				case "repeated stage":
					content["items"] = []interface{}{executionStage(1)}
				case "invalid stage":
					content["items"] = []interface{}{map[string]interface{}{"guid": "wrong"}}
				case "transport":
					return nil, []string{"not permitted"}
				}
				return map[string]interface{}{"workflowExecutionStages": page}, nil
			})
			cmd, out, _ := workflowTestCmd()
			err := runWorkflowExecutionStages(cmd, context.Background(), executionTestClient(srv.URL), "91", workflowExecutionOptions{})
			if err == nil || out.Len() != 0 || pages > 2 {
				t.Fatalf("accepted broken page: err=%v pages=%d output=%s", err, pages, out)
			}
		})
	}
}

func TestWorkflowExecutionStagesEmptyAndCancelledReads(t *testing.T) {
	srv := newRunControlServer(t, func(req gqlRequest) (map[string]interface{}, []string) {
		if strings.Contains(req.Query, "workflowExecutionStages") {
			return map[string]interface{}{"workflowExecutionStages": executionStagePage([]interface{}{}, nil)}, nil
		}
		return map[string]interface{}{"workflowExecution": executionRow()}, nil
	})
	cmd, out, _ := workflowTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runWorkflowExecutionStages(cmd, context.Background(), executionTestClient(srv.URL), "91", workflowExecutionOptions{}); err != nil || !strings.Contains(out.String(), `"stages": []`) {
		t.Fatalf("empty history failed: %v: %s", err, out)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := runWorkflowExecutionStages(cmd, ctx, executionTestClient(srv.URL), "91", workflowExecutionOptions{}); err == nil {
		t.Error("ignored cancelled inspection")
	}
	read := newWorkflowExecutionStagesCommand()
	if read.Flags().Lookup("yes") != nil || read.Flags().Lookup("watch") != nil || read.Flags().Lookup("run-id") == nil || read.Flags().Lookup("workflow-id") == nil {
		t.Error("stage inspection must expose only original identity flags")
	}
}
