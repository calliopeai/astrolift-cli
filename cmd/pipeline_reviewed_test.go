package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const reviewedPipelineTestOrg = "00000000-0000-4000-8000-000000000010"
const reviewedPipelineTestID = "00000000-0000-4000-8000-000000000020"
const reviewedPipelineTestRunID = "00000000-0000-4000-8000-000000000030"

func reviewedPipelineTestCommand(t *testing.T) (*cobra.Command, string) {
	t.Helper()
	c, _ := pipelineTestCmd()
	c.SetContext(context.Background())
	c.Flags().Bool("yes", true, "")
	c.Flags().Bool("json", false, "")
	c.Flags().String("branch", "", "")
	file := filepath.Join(t.TempDir(), "request.json")
	c.Flags().String("request-file", file, "")
	return c, file
}

func reviewedPipelineTestData() map[string]interface{} {
	return map[string]interface{}{
		"astroliftMyProfile":   map[string]interface{}{"userId": 42, "username": "reviewer"},
		"astroliftPipeline":    map[string]interface{}{"id": reviewedPipelineTestID, "name": "deploy", "organizationId": reviewedPipelineTestOrg, "version": 7, "defaultBranch": "main"},
		"pipelineStartRequest": nil,
	}
}

func reviewedPipelineTestRun(requestID string) map[string]interface{} {
	return map[string]interface{}{"id": reviewedPipelineTestRunID, "runNumber": 3, "version": 2, "pipelineId": reviewedPipelineTestID, "organizationId": reviewedPipelineTestOrg, "pipelineVersion": 7, "requestId": requestID, "triggerRef": "main", "status": "pending", "temporalWorkflowId": "pipeline-run-" + reviewedPipelineTestRunID, "temporalRunId": "engine-run-1", "dispatchStatus": "submitted", "cancellationStatus": "none", "cleanupStatus": "not_required"}
}

func pipelineReviewServer(t *testing.T, fn func(gqlRequest, http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("request decode: %v", err)
			w.WriteHeader(400)
			return
		}
		request.Organization = r.Header.Get("X-Astrolift-Org")
		fn(request, w)
	}))
}

func writePipelineTestResponse(t *testing.T, w http.ResponseWriter, data map[string]interface{}) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]interface{}{"data": data}); err != nil {
		t.Errorf("response: %v", err)
	}
}

func TestReviewedPipelineStartPersistsKeyAndExactInput(t *testing.T) {
	c, file := reviewedPipelineTestCommand(t)
	var input map[string]interface{}
	srv := pipelineReviewServer(t, func(q gqlRequest, w http.ResponseWriter) {
		data := reviewedPipelineTestData()
		if strings.Contains(q.Query, "mutation StartPipelineRun") {
			var err error
			input = q.Variables["input"].(map[string]interface{})
			saved, readErr := readReviewedRequest(file)
			if readErr != nil {
				t.Errorf("key was not durable before dispatch: %v", readErr)
			} else if saved.RequestID != input["requestId"] {
				t.Errorf("saved key differs from dispatch")
			}
			_, err = uuid.Parse(input["requestId"].(string))
			if err != nil {
				t.Errorf("bad request ID: %v", err)
			}
			data["startPipelineRun"] = map[string]interface{}{"ok": true, "errors": []interface{}{}, "data": reviewedPipelineTestRun(input["requestId"].(string))}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	client.SetOrg(reviewedPipelineTestOrg)
	if err := runPipelineRun(c, c.Context(), client, reviewedPipelineTestID); err != nil {
		t.Fatal(err)
	}
	if input["pipelineId"] != reviewedPipelineTestID || input["expectedVersion"] != float64(7) || input["ref"] != "main" || input["confirmed"] != true {
		t.Fatalf("wrong reviewed input: %v", input)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("request file permissions")
	}
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "token") {
		t.Fatal("credential in request file")
	}
}

func TestReviewedPipelineLostResponseRecoversWithoutNewStartOrKey(t *testing.T) {
	c, file := reviewedPipelineTestCommand(t)
	var stored map[string]interface{}
	starts := 0
	srv := pipelineReviewServer(t, func(q gqlRequest, w http.ResponseWriter) {
		data := reviewedPipelineTestData()
		// Definition changes after the accepted start must not prevent exact recovery.
		if stored != nil {
			data["astroliftPipeline"].(map[string]interface{})["version"] = 99
		}
		if strings.Contains(q.Query, "mutation StartPipelineRun") {
			starts++
			input := q.Variables["input"].(map[string]interface{})
			stored = reviewedPipelineTestRun(input["requestId"].(string))
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if strings.Contains(q.Query, "query PipelineStartRequest") {
			data["pipelineStartRequest"] = stored
		}
		writePipelineTestResponse(t, w, data)
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	client.SetOrg(reviewedPipelineTestOrg)
	if err := runPipelineRun(c, c.Context(), client, reviewedPipelineTestID); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected uncertain response, got %v", err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := runPipelineRun(c, c.Context(), client, reviewedPipelineTestID); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 || string(before) != string(after) {
		t.Fatalf("recovery changed the key or started again: %d", starts)
	}
}

func TestReviewedPipelineSavedScopeChangesNeverDispatch(t *testing.T) {
	for _, field := range []string{"actor", "org", "server", "selector", "branch"} {
		t.Run(field, func(t *testing.T) {
			c, file := reviewedPipelineTestCommand(t)
			mutations := 0
			srv := pipelineReviewServer(t, func(q gqlRequest, w http.ResponseWriter) {
				if strings.Contains(q.Query, "mutation") {
					mutations++
				}
				writePipelineTestResponse(t, w, reviewedPipelineTestData())
			})
			defer srv.Close()
			r := reviewedStartRequest{Format: 1, Kind: "pipeline", Server: srv.URL, OrganizationID: reviewedPipelineTestOrg, ActorUserID: 42, TargetID: reviewedPipelineTestID, TargetName: "deploy", RequestID: uuid.NewString(), Version: 7, Ref: "main"}
			selector := reviewedPipelineTestID
			switch field {
			case "actor":
				r.ActorUserID = 43
			case "org":
				r.OrganizationID = "other-org"
			case "server":
				r.Server = "https://other.example"
			case "selector":
				selector = "other-pipeline"
			case "branch":
				pipelineRunBranch = "different"
				defer func() { pipelineRunBranch = "" }()
				if err := c.Flags().Set("branch", "different"); err != nil {
					t.Fatal(err)
				}
			}
			if err := createReviewedRequest(file, r); err != nil {
				t.Fatal(err)
			}
			client := api.NewClient(srv.URL, "token", false)
			client.SetOrg(reviewedPipelineTestOrg)
			if err := runPipelineRun(c, c.Context(), client, selector); err == nil {
				t.Fatal("expected refusal")
			}
			if mutations != 0 {
				t.Fatal("scope failure submitted a mutation")
			}
		})
	}
}

func TestReviewedPipelineUnavailableRecoveryDoesNotResubmit(t *testing.T) {
	c, file := reviewedPipelineTestCommand(t)
	starts := 0
	srv := pipelineReviewServer(t, func(q gqlRequest, w http.ResponseWriter) {
		if strings.Contains(q.Query, "mutation") {
			starts++
		}
		if strings.Contains(q.Query, "query PipelineStartRequest") {
			w.WriteHeader(503)
			return
		}
		writePipelineTestResponse(t, w, reviewedPipelineTestData())
	})
	defer srv.Close()
	r := reviewedStartRequest{Format: 1, Kind: "pipeline", Server: srv.URL, OrganizationID: reviewedPipelineTestOrg, ActorUserID: 42, TargetID: reviewedPipelineTestID, RequestID: uuid.NewString(), Version: 7, Ref: "main"}
	if err := createReviewedRequest(file, r); err != nil {
		t.Fatal(err)
	}
	client := api.NewClient(srv.URL, "token", false)
	client.SetOrg(reviewedPipelineTestOrg)
	if err := runPipelineRun(c, c.Context(), client, reviewedPipelineTestID); err == nil {
		t.Fatal("expected recovery outage")
	}
	if starts != 0 {
		t.Fatal("read outage resubmitted a start")
	}
}

func TestReviewedPipelineRefusesSubstitutedOrPartialRun(t *testing.T) {
	for _, field := range []string{"nil", "id", "organizationId", "pipelineId", "requestId", "pipelineVersion", "triggerRef", "temporalWorkflowId"} {
		t.Run(field, func(t *testing.T) {
			c, _ := reviewedPipelineTestCommand(t)
			srv := pipelineReviewServer(t, func(q gqlRequest, w http.ResponseWriter) {
				data := reviewedPipelineTestData()
				if strings.Contains(q.Query, "mutation StartPipelineRun") {
					input := q.Variables["input"].(map[string]interface{})
					run := reviewedPipelineTestRun(input["requestId"].(string))
					var payload interface{} = run
					if field == "nil" {
						payload = nil
					} else {
						run[field] = "substituted"
					}
					data["startPipelineRun"] = map[string]interface{}{"ok": true, "errors": []interface{}{}, "data": payload}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer srv.Close()
			client := api.NewClient(srv.URL, "token", false)
			client.SetOrg(reviewedPipelineTestOrg)
			if err := runPipelineRun(c, c.Context(), client, reviewedPipelineTestID); err == nil {
				t.Fatal("accepted substituted/partial response")
			}
		})
	}
}

func TestReviewedPipelineCancelBindsVersionAndEngineDoesNotClaimCompletion(t *testing.T) {
	c, _ := reviewedPipelineTestCommand(t)
	var vars map[string]interface{}
	srv := pipelineReviewServer(t, func(q gqlRequest, w http.ResponseWriter) {
		run := reviewedPipelineTestRun(uuid.NewString())
		data := map[string]interface{}{"astroliftPipelineRun": run}
		if strings.Contains(q.Query, "mutation CancelPipelineRun") {
			vars = q.Variables
			run["version"] = 3
			run["cancellationStatus"] = "acknowledged"
			run["cleanupStatus"] = "pending"
			data["cancelPipelineRun"] = map[string]interface{}{"ok": true, "errors": []interface{}{}, "data": run}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	client.SetOrg(reviewedPipelineTestOrg)
	if err := runPipelineCancel(c, c.Context(), client, reviewedPipelineTestRunID); err != nil {
		t.Fatal(err)
	}
	if vars["runId"] != reviewedPipelineTestRunID || vars["expectedVersion"] != float64(2) || vars["temporalWorkflowId"] != "pipeline-run-"+reviewedPipelineTestRunID || vars["temporalRunId"] != "engine-run-1" || vars["confirmed"] != true {
		t.Fatalf("wrong cancel preconditions: %v", vars)
	}
	if strings.Contains(fmt.Sprint(c.OutOrStdout()), "cancelled") {
		t.Fatal("acknowledgement claimed completion")
	}
}

func TestResolvePipelinePagesRejectAmbiguousNames(t *testing.T) {
	pages := 0
	srv := pipelineReviewServer(t, func(q gqlRequest, w http.ResponseWriter) {
		pages++
		id := reviewedPipelineTestID
		var next interface{} = "second-page"
		if pages == 2 {
			id = reviewedPipelineTestRunID
			next = nil
		}
		writePipelineTestResponse(t, w, map[string]interface{}{"astroliftPipelinesPage": map[string]interface{}{"items": []map[string]interface{}{{"id": id, "name": "deploy"}}, "nextCursor": next}})
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	if _, err := resolvePipelineID(context.Background(), client, "deploy"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous name, got %v", err)
	}
	if pages != 2 {
		t.Fatalf("did not inspect all server pages: %d", pages)
	}
}

func TestReviewedRequestFileExclusiveAndPrivate(t *testing.T) {
	file := filepath.Join(t.TempDir(), "request.json")
	r := reviewedStartRequest{Format: 1, RequestID: uuid.NewString()}
	if err := createReviewedRequest(file, r); err != nil {
		t.Fatal(err)
	}
	if err := createReviewedRequest(file, reviewedStartRequest{Format: 1, RequestID: uuid.NewString()}); err == nil {
		t.Fatal("overwrote existing request")
	}
	stored, err := readReviewedRequest(file)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RequestID != r.RequestID {
		t.Fatal("replaced original identity")
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readReviewedRequest(file); err == nil {
		t.Fatal("accepted world-readable request file")
	}
}

func TestReviewedPipelineUnconfirmedOrChangedStartNeverDispatches(t *testing.T) {
	for _, scenario := range []string{"confirmation", "changed-version", "known-uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			c, file := reviewedPipelineTestCommand(t)
			starts := 0
			requestID := uuid.NewString()
			srv := pipelineReviewServer(t, func(q gqlRequest, w http.ResponseWriter) {
				if strings.Contains(q.Query, "mutation") {
					starts++
				}
				data := reviewedPipelineTestData()
				if scenario == "changed-version" {
					data["astroliftPipeline"].(map[string]interface{})["version"] = 8
				}
				if scenario == "known-uncertain" {
					r := reviewedPipelineTestRun(requestID)
					r["dispatchStatus"] = "uncertain"
					r["temporalRunId"] = nil
					data["pipelineStartRequest"] = r
				}
				writePipelineTestResponse(t, w, data)
			})
			defer srv.Close()
			if scenario == "confirmation" {
				if err := c.Flags().Set("yes", "false"); err != nil {
					t.Fatal(err)
				}
			} else {
				r := reviewedStartRequest{Format: 1, Kind: "pipeline", Server: srv.URL, OrganizationID: reviewedPipelineTestOrg, ActorUserID: 42, TargetID: reviewedPipelineTestID, RequestID: requestID, Version: 7, Ref: "main"}
				if err := createReviewedRequest(file, r); err != nil {
					t.Fatal(err)
				}
			}
			client := api.NewClient(srv.URL, "token", false)
			client.SetOrg(reviewedPipelineTestOrg)
			if err := runPipelineRun(c, c.Context(), client, reviewedPipelineTestID); err == nil {
				t.Fatal("expected refusal/unconfirmed outcome")
			}
			if starts != 0 {
				t.Fatal("unconfirmed or stale request submitted")
			}
		})
	}
}

func TestReviewedPipelineCancelRefusesUnknownOrChangedEngine(t *testing.T) {
	for _, scenario := range []string{"missing-engine", "substituted-response", "uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := reviewedPipelineTestCommand(t)
			mutations := 0
			srv := pipelineReviewServer(t, func(q gqlRequest, w http.ResponseWriter) {
				run := reviewedPipelineTestRun(uuid.NewString())
				if scenario == "missing-engine" {
					run["temporalRunId"] = nil
				}
				data := map[string]interface{}{"astroliftPipelineRun": run}
				if strings.Contains(q.Query, "mutation") {
					mutations++
					if scenario == "substituted-response" {
						run["temporalRunId"] = "different-engine-run"
					}
					if scenario == "uncertain" {
						run["cancellationStatus"] = "uncertain"
					}
					data["cancelPipelineRun"] = map[string]interface{}{"ok": true, "errors": []interface{}{}, "data": run}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer srv.Close()
			client := api.NewClient(srv.URL, "token", false)
			client.SetOrg(reviewedPipelineTestOrg)
			if err := runPipelineCancel(c, c.Context(), client, reviewedPipelineTestRunID); err == nil {
				t.Fatal("accepted unverified/unconfirmed cancellation")
			}
			if scenario == "missing-engine" && mutations != 0 {
				t.Fatal("cancellation submitted before engine review")
			}
		})
	}
}

func TestReviewedPipelinePageJSONRetainsServerContinuation(t *testing.T) {
	c, _ := reviewedPipelineTestCommand(t)
	if err := c.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	c.Flags().String("after", "cursor-A", "")
	c.Flags().String("search", "deploy", "")
	pipelineListLimit = 25
	defer func() { pipelineListLimit = 50 }()
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"astroliftPipelinesPage": map[string]interface{}{"items": []interface{}{}, "nextCursor": "cursor-B", "totalCount": 250}}, &captured)
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	client.SetOrg(reviewedPipelineTestOrg)
	if err := runPipelineList(c, c.Context(), client); err != nil {
		t.Fatal(err)
	}
	if captured.Variables["after"] != "cursor-A" || captured.Variables["search"] != "deploy" || !strings.Contains(captured.Query, "astroliftPipelinesPage") {
		t.Fatalf("lost server paging: %v", captured)
	}
	var page pipelineCursorPage[reviewedPipeline]
	if err := json.Unmarshal([]byte(fmt.Sprint(c.OutOrStdout())), &page); err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == nil || *page.NextCursor != "cursor-B" || page.TotalCount == nil || *page.TotalCount != 250 {
		t.Fatal("truncated page was presented as complete")
	}
}
