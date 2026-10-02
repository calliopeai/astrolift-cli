package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReviewedAgentAliasUsesOriginalDefinitionRequest(t *testing.T) {
	c, out, _, file := definitionTestCommand(t)
	agentRunWait = false
	agentRunInput = ""
	defer func() { agentRunInput = ""; agentRunWait = false }()
	starts := 0
	var stored map[string]interface{}
	srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
		if strings.Contains(q.Query, "runWorkflowDefinition(") {
			t.Error("legacy unsafe dispatch used")
		}
		data := definitionTestData()
		if strings.Contains(q.Query, "mutation StartWorkflowDefinition") {
			starts++
			input := q.Variables["input"].(map[string]interface{})
			stored = definitionTestStart(input["requestId"].(string), "submitted")
			data["startWorkflowDefinition"] = map[string]interface{}{"ok": true, "data": stored}
		}
		if strings.Contains(q.Query, "query WorkflowDefinitionStartRequest") {
			data["workflowDefinitionStartRequest"] = stored
		}
		definitionTestResponse(t, w, data)
	})
	defer srv.Close()
	client := definitionTestClient(srv)
	if err := runAgentRun(c, c.Context(), client, nil, definitionTestID); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// Even an invalid legacy input is ignored for read-only recovery.
	agentRunInput = "PRIVATE_LITERAL_NEVER_READ"
	if err := runAgentRun(c, c.Context(), client, nil, definitionTestID); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 || !bytes.Equal(before, after) || strings.Contains(out.String(), agentRunInput) {
		t.Fatal("recovery resubmitted, replaced identity or disclosed input")
	}
}

func TestReviewedAgentAliasAcceptsFileButRefusesLiteralInputs(t *testing.T) {
	for _, scenario := range []string{"literal", "file"} {
		t.Run(scenario, func(t *testing.T) {
			c, _, _, _ := definitionTestCommand(t)
			agentRunWait = false
			defer func() { agentRunInput = "" }()
			inputFile := filepath.Join(t.TempDir(), "inputs.json")
			if err := os.WriteFile(inputFile, []byte(`{"message":"PRIVATE_INPUT"}`), 0600); err != nil {
				t.Fatal(err)
			}
			agentRunInput = `{"message":"PRIVATE_INPUT"}`
			if scenario == "file" {
				agentRunInput = "@" + inputFile
			}
			starts := 0
			srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
				data := definitionTestData()
				data["workflowDefinitionById"].(map[string]interface{})["inputContract"].(map[string]interface{})["acceptsInputs"] = true
				if strings.Contains(q.Query, "mutation StartWorkflowDefinition") {
					starts++
					input := q.Variables["input"].(map[string]interface{})
					if input["inputs"].(map[string]interface{})["message"] != "PRIVATE_INPUT" {
						t.Error("legacy file value lost")
					}
					data["startWorkflowDefinition"] = map[string]interface{}{"ok": true, "data": definitionTestStart(input["requestId"].(string), "submitted")}
				}
				definitionTestResponse(t, w, data)
			})
			defer srv.Close()
			err := runAgentRun(c, c.Context(), definitionTestClient(srv), nil, definitionTestID)
			if scenario == "literal" && (err == nil || starts != 0) {
				t.Fatal("literal input dispatched")
			}
			if scenario == "file" && (err != nil || starts != 1) {
				t.Fatalf("file alias failed: %v", err)
			}
		})
	}
}

func TestReviewedAgentWaitPinsEngineAndEmitsOneMetadataObject(t *testing.T) {
	for _, scenario := range []string{"completed", "failed", "different-engine", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			c, out, _, file := definitionTestCommand(t)
			if err := c.Flags().Set("json", "true"); err != nil {
				t.Fatal(err)
			}
			agentRunWait = true
			agentRunInput = ""
			oldPoll := agentDefinitionPollInterval
			agentDefinitionPollInterval = time.Millisecond
			defer func() { agentRunWait = false; agentDefinitionPollInterval = oldPoll }()
			starts := 0
			var stored map[string]interface{}
			srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
				data := definitionTestData()
				if strings.Contains(q.Query, "mutation StartWorkflowDefinition") {
					starts++
					input := q.Variables["input"].(map[string]interface{})
					stored = definitionTestStart(input["requestId"].(string), "submitted")
					data["startWorkflowDefinition"] = map[string]interface{}{"ok": true, "data": stored}
				}
				if strings.Contains(q.Query, "query WorkflowDefinitionStartRequest") {
					data["workflowDefinitionStartRequest"] = stored
				}
				if strings.Contains(q.Query, "query ReviewedAgentDefinitionExecution") {
					if q.Variables["id"] != definitionTestExecution || strings.Contains(q.Query, "failure ") {
						t.Error("watch requested wrong execution or failure body")
					}
					if scenario == "unavailable" {
						w.WriteHeader(503)
						return
					}
					status := "completed"
					if scenario == "failed" {
						status = "failed"
					}
					row := map[string]interface{}{"guid": definitionTestExecution, "recordId": "17", "organizationGuid": definitionTestOrg, "status": status, "temporalWorkflowId": "WorkflowDefinitionRunWorkflow-17", "temporalRunId": "opaque-engine-run-id", "isTerminal": true, "observationError": "", "taskCleanup": map[string]interface{}{"status": "pending", "remaining": 1, "retryable": true}, "failure": "PRIVATE_FAILURE_BODY"}
					if scenario == "different-engine" {
						row["temporalRunId"] = "newer-engine-run"
					}
					data["workflowExecution"] = row
				}
				definitionTestResponse(t, w, data)
			})
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := runAgentRun(c, ctx, definitionTestClient(srv), nil, definitionTestID)
			if scenario == "completed" && err != nil {
				t.Fatal(err)
			}
			if scenario != "completed" && err == nil {
				t.Fatal("unknown/failed/substituted outcome accepted")
			}
			if starts != 1 {
				t.Fatal("watch dispatched another start")
			}
			if _, err := readReviewedRequest(file); err != nil {
				t.Fatal("original identity lost")
			}
			if strings.Contains(out.String(), "PRIVATE_FAILURE_BODY") {
				t.Fatal("failure body disclosed")
			}
			if scenario == "completed" || scenario == "failed" {
				d := json.NewDecoder(bytes.NewReader(out.Bytes()))
				var result map[string]interface{}
				if err := d.Decode(&result); err != nil {
					t.Fatal(err)
				}
				var extra any
				if err := d.Decode(&extra); err != io.EOF {
					t.Fatal("wait emitted multiple JSON objects")
				}
				if result["execution"].(map[string]interface{})["cleanupStatus"] != "pending" {
					t.Fatal("closure claimed cleanup complete")
				}
			}
		})
	}
}

func TestReviewedAgentWaitNeverStartsForAnAbsentExistingRequest(t *testing.T) {
	c, _, _, file := definitionTestCommand(t)
	agentRunWait = true
	defer func() { agentRunWait = false }()
	starts := 0
	srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
		if strings.Contains(q.Query, "mutation") {
			starts++
		}
		definitionTestResponse(t, w, definitionTestData())
	})
	defer srv.Close()
	r := reviewedStartRequest{Format: 1, Kind: "workflow-definition", Server: srv.URL, OrganizationID: definitionTestOrg, ActorUserID: 42, TargetID: definitionTestID, RequestID: uuid.NewString(), Revision: definitionTestRevision, InputSchemaDigest: definitionTestDigest}
	if err := createReviewedRequest(file, r); err != nil {
		t.Fatal(err)
	}
	if err := runAgentRun(c, c.Context(), definitionTestClient(srv), nil, definitionTestID); err == nil {
		t.Fatal("absent original request treated as failed dispatch")
	}
	if starts != 0 {
		t.Fatal("missing record resubmitted")
	}
}

func TestReviewedAgentWaitPreservesUnconfirmedJSONReceipt(t *testing.T) {
	for _, status := range []string{"reserved", "uncertain", "refused"} {
		t.Run(status, func(t *testing.T) {
			c, out, _, _ := definitionTestCommand(t)
			if err := c.Flags().Set("json", "true"); err != nil {
				t.Fatal(err)
			}
			agentRunWait, agentRunInput = true, ""
			defer func() { agentRunWait = false }()
			polls := 0
			srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
				data := definitionTestData()
				if strings.Contains(q.Query, "mutation StartWorkflowDefinition") {
					input := q.Variables["input"].(map[string]interface{})
					data["startWorkflowDefinition"] = map[string]interface{}{"ok": true, "data": definitionTestStart(input["requestId"].(string), status)}
				}
				if strings.Contains(q.Query, "query ReviewedAgentDefinitionExecution") {
					polls++
				}
				definitionTestResponse(t, w, data)
			})
			defer srv.Close()
			if err := runAgentRun(c, c.Context(), definitionTestClient(srv), nil, definitionTestID); err == nil {
				t.Fatal("unconfirmed submission accepted")
			}
			decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
			var result map[string]interface{}
			if err := decoder.Decode(&result); err != nil {
				t.Fatalf("known receipt hidden: %v", err)
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatal("multiple JSON receipts emitted")
			}
			start := result["start"].(map[string]interface{})
			if start["executionId"] != definitionTestExecution || start["dispatchStatus"] != status || polls != 0 {
				t.Fatal("original uncertainty identity lost or polled as submitted")
			}
		})
	}
}

func TestReviewedAgentWaitIgnoresReplacementRequestFile(t *testing.T) {
	c, out, _, filename := definitionTestCommand(t)
	if err := c.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	agentRunWait, agentRunInput = true, ""
	defer func() { agentRunWait = false }()
	polls, recoveries := 0, 0
	srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
		data := definitionTestData()
		if strings.Contains(q.Query, "mutation StartWorkflowDefinition") {
			input := q.Variables["input"].(map[string]interface{})
			original := input["requestId"].(string)
			r, err := readReviewedRequest(filename)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			r.RequestID = uuid.NewString()
			if err := os.Remove(filename); err != nil {
				t.Error(err)
			}
			if err := createReviewedRequest(filename, *r); err != nil {
				t.Error(err)
			}
			data["startWorkflowDefinition"] = map[string]interface{}{"ok": true, "data": definitionTestStart(original, "submitted")}
		}
		if strings.Contains(q.Query, "query WorkflowDefinitionStartRequest") {
			recoveries++
			r := definitionTestStart(q.Variables["requestId"].(string), "submitted")
			r["executionId"] = "00000000-0000-4000-8000-000000000999"
			r["temporalRunId"] = "replacement-engine-run"
			data["workflowDefinitionStartRequest"] = r
		}
		if strings.Contains(q.Query, "query ReviewedAgentDefinitionExecution") {
			polls++
			if q.Variables["id"] != definitionTestExecution {
				t.Error("replacement execution followed")
			}
			data["workflowExecution"] = map[string]interface{}{"guid": definitionTestExecution, "recordId": "17", "organizationGuid": definitionTestOrg, "status": "completed", "temporalWorkflowId": "WorkflowDefinitionRunWorkflow-17", "temporalRunId": "opaque-engine-run-id", "isTerminal": true, "observationError": "", "taskCleanup": map[string]interface{}{"status": "pending", "remaining": 1, "retryable": true}}
		}
		definitionTestResponse(t, w, data)
	})
	defer srv.Close()
	if err := runAgentRun(c, c.Context(), definitionTestClient(srv), nil, definitionTestID); err != nil {
		t.Fatal(err)
	}
	if polls != 1 || recoveries != 0 || !strings.Contains(out.String(), "opaque-engine-run-id") || strings.Contains(out.String(), "replacement-engine-run") {
		t.Fatal("watch identity was derived from replaced request metadata")
	}
}
