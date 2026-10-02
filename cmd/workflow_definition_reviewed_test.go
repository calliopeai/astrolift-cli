package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const definitionTestID = "00000000-0000-4000-8000-000000000100"
const definitionTestOrg = "00000000-0000-4000-8000-000000000200"
const definitionTestExecution = "00000000-0000-4000-8000-000000000300"
const definitionTestStartID = "00000000-0000-4000-8000-000000000400"
const definitionTestRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const definitionTestDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func definitionTestCommand(t *testing.T) (*cobra.Command, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	c, out, errOut := workflowTestCmd()
	c.SetContext(context.Background())
	c.Flags().Bool("yes", true, "")
	c.Flags().String("request-file", filepath.Join(t.TempDir(), "request.json"), "")
	c.Flags().String("inputs-file", "", "")
	c.Flags().String("expected-revision", "", "")
	c.Flags().String("expected-input-schema-digest", "", "")
	file, _ := c.Flags().GetString("request-file")
	return c, out, errOut, file
}

func definitionTestMetadata() map[string]interface{} {
	return map[string]interface{}{
		"guid": definitionTestID, "revision": definitionTestRevision,
		"inputContract": map[string]interface{}{"schema": map[string]interface{}{"type": "object", "additionalProperties": false}, "digest": definitionTestDigest, "supported": true, "acceptsInputs": false, "supportsSimpleForm": true, "fields": []interface{}{}},
		"definition":    map[string]interface{}{"guid": definitionTestID, "name": "Example", "slug": "example", "isEnabled": true, "isGlobal": false, "organizationGuid": definitionTestOrg},
	}
}

func definitionTestStart(requestID, status string) map[string]interface{} {
	r := map[string]interface{}{"id": definitionTestStartID, "requestId": requestID, "definitionId": definitionTestID, "organizationId": definitionTestOrg, "definitionRevision": definitionTestRevision, "inputSchemaDigest": definitionTestDigest, "executionId": definitionTestExecution, "temporalWorkflowId": "WorkflowDefinitionRunWorkflow-17", "temporalRunId": nil, "dispatchStatus": status}
	if status == "submitted" {
		r["temporalRunId"] = "opaque-engine-run-id"
	}
	return r
}

func definitionTestServer(t *testing.T, fn func(gqlRequest, http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q gqlRequest
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&q); err != nil {
			t.Errorf("decode: %v", err)
			w.WriteHeader(400)
			return
		}
		fn(q, w)
	}))
}

func definitionTestResponse(t *testing.T, w http.ResponseWriter, data map[string]interface{}) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]interface{}{"data": data}); err != nil {
		t.Errorf("encode: %v", err)
	}
}

func definitionTestData() map[string]interface{} {
	return map[string]interface{}{"astroliftMyProfile": map[string]interface{}{"userId": 42, "username": "reviewer"}, "workflowDefinitionById": definitionTestMetadata(), "workflowDefinitionStartRequest": nil}
}

func definitionTestClient(srv *httptest.Server) *api.Client {
	c := api.NewClient(srv.URL, "private-bearer", false)
	c.SetOrg(definitionTestOrg)
	return c
}

func TestReviewedWorkflowDefinitionStartDurableComplexInputs(t *testing.T) {
	c, out, errOut, file := definitionTestCommand(t)
	_ = c.Flags().Set("json", "true")
	inputsFile := filepath.Join(t.TempDir(), "inputs.json")
	marker := "PRIVATE_RUNTIME_MARKER"
	if err := os.WriteFile(inputsFile, []byte(`{"nested":{"values":[9007199254740993,true]},"secret":"secret://agents/`+definitionTestOrg+`/key","message":"`+marker+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_ = c.Flags().Set("inputs-file", inputsFile)
	_ = c.Flags().Set("expected-revision", definitionTestRevision)
	_ = c.Flags().Set("expected-input-schema-digest", definitionTestDigest)
	mutations := 0
	srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
		data := definitionTestData()
		data["workflowDefinitionById"].(map[string]interface{})["inputContract"].(map[string]interface{})["acceptsInputs"] = true
		if strings.Contains(q.Query, "mutation StartWorkflowDefinition") {
			mutations++
			input := q.Variables["input"].(map[string]interface{})
			saved, err := readReviewedRequest(file)
			if err != nil {
				t.Fatalf("identity absent before dispatch: %v", err)
			}
			if input["requestId"] != saved.RequestID || input["definitionId"] != definitionTestID || input["expectedRevision"] != definitionTestRevision || input["expectedInputSchemaDigest"] != definitionTestDigest || input["confirmed"] != true {
				t.Errorf("reviewed input mismatch: %v", input)
			}
			nested := input["inputs"].(map[string]interface{})["nested"].(map[string]interface{})["values"].([]interface{})
			if nested[0].(json.Number).String() != "9007199254740993" {
				t.Error("JSON integer lost precision")
			}
			data["startWorkflowDefinition"] = map[string]interface{}{"ok": true, "data": definitionTestStart(saved.RequestID, "submitted")}
		}
		definitionTestResponse(t, w, data)
	})
	defer srv.Close()
	if err := runDefinitionStart(c, c.Context(), definitionTestClient(srv), definitionTestID); err != nil {
		t.Fatal(err)
	}
	if mutations != 1 || !strings.Contains(out.String(), definitionTestExecution) {
		t.Fatalf("missing accepted identity: %s", out)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{string(b), out.String(), errOut.String()} {
		if strings.Contains(text, marker) || strings.Contains(text, "private-bearer") || strings.Contains(text, "secret://") {
			t.Fatal("runtime inputs or token exposed")
		}
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0600 {
		t.Fatal("recovery file is not private")
	}
}

func TestReviewedWorkflowDefinitionLostResponseRecoveryNeverReadsInputs(t *testing.T) {
	c, out, _, file := definitionTestCommand(t)
	mutations, reviews, recoveries := 0, 0, 0
	requestID := ""
	srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
		data := definitionTestData()
		switch {
		case strings.Contains(q.Query, "mutation StartWorkflowDefinition"):
			mutations++
			requestID = q.Variables["input"].(map[string]interface{})["requestId"].(string)
			w.WriteHeader(503)
			_, _ = w.Write([]byte("PRIVATE_RESPONSE_MARKER"))
			return
		case strings.Contains(q.Query, "query ReviewedWorkflowDefinition"):
			reviews++
		case strings.Contains(q.Query, "query WorkflowDefinitionStartRequest"):
			recoveries++
			if q.Variables["requestId"] != requestID {
				t.Error("recovery replaced request identity")
			}
			data["workflowDefinitionStartRequest"] = definitionTestStart(requestID, "submitted")
		}
		definitionTestResponse(t, w, data)
	})
	defer srv.Close()
	client := definitionTestClient(srv)
	err := runDefinitionStart(c, c.Context(), client, definitionTestID)
	if err == nil || strings.Contains(err.Error(), "PRIVATE_RESPONSE_MARKER") {
		t.Fatalf("unsafe lost response: %v", err)
	}
	before, _ := os.ReadFile(file)
	_ = c.Flags().Set("inputs-file", "/nonexistent/never-open-this.json")
	_ = c.Flags().Set("yes", "false")
	if err := runDefinitionStart(c, c.Context(), client, definitionTestID); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(file)
	if mutations != 1 || reviews != 1 || recoveries != 1 || !bytes.Equal(before, after) || !strings.Contains(out.String(), definitionTestExecution) {
		t.Fatal("recovery resubmitted or changed identity")
	}
}

func TestReviewedWorkflowDefinitionExistingUnknownAndUncertain(t *testing.T) {
	for _, state := range []string{"absent", "reserved", "uncertain", "refused", "submitted"} {
		t.Run(state, func(t *testing.T) {
			c, out, _, file := definitionTestCommand(t)
			_ = c.Flags().Set("json", "true")
			srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
				if strings.Contains(q.Query, "mutation") || strings.Contains(q.Query, "ReviewedWorkflowDefinition(") {
					t.Fatal("existing identity must only recover")
				}
				data := definitionTestData()
				if strings.Contains(q.Query, "query WorkflowDefinitionStartRequest") && state != "absent" {
					data["workflowDefinitionStartRequest"] = definitionTestStart(q.Variables["requestId"].(string), state)
				}
				definitionTestResponse(t, w, data)
			})
			defer srv.Close()
			r := reviewedStartRequest{Format: 1, Kind: "workflow-definition", Server: srv.URL, OrganizationID: definitionTestOrg, ActorUserID: 42, TargetID: definitionTestID, RequestID: definitionTestStartID, Revision: definitionTestRevision, InputSchemaDigest: definitionTestDigest}
			if err := createReviewedRequest(file, r); err != nil {
				t.Fatal(err)
			}
			err := runDefinitionStart(c, c.Context(), definitionTestClient(srv), definitionTestID)
			if (err == nil) != (state == "submitted") {
				t.Fatalf("start returned incorrect certainty: %v", err)
			}
			if out.Len() == 0 {
				t.Fatal("missing recovery metadata")
			}
			out.Reset()
			err = runDefinitionReconcile(c, c.Context(), definitionTestClient(srv))
			if (err == nil) != (state != "absent") {
				t.Fatalf("read recovery should show known uncertainty: %v", err)
			}
		})
	}
}

func TestReviewedWorkflowDefinitionScopeChangesNeverRecoverOrDispatch(t *testing.T) {
	for _, field := range []string{"server", "organization", "actor", "target", "revision", "kind"} {
		t.Run(field, func(t *testing.T) {
			c, _, _, file := definitionTestCommand(t)
			calls := 0
			srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
				if !strings.Contains(q.Query, "astroliftMyProfile") {
					calls++
				}
				definitionTestResponse(t, w, definitionTestData())
			})
			defer srv.Close()
			r := reviewedStartRequest{Format: 1, Kind: "workflow-definition", Server: srv.URL, OrganizationID: definitionTestOrg, ActorUserID: 42, TargetID: definitionTestID, RequestID: definitionTestStartID, Revision: definitionTestRevision, InputSchemaDigest: definitionTestDigest}
			switch field {
			case "server":
				r.Server += "/other"
			case "organization":
				r.OrganizationID = definitionTestID
			case "actor":
				r.ActorUserID = 99
			case "target":
				r.TargetID = definitionTestStartID
			case "revision":
				r.Revision = "missing"
			case "kind":
				r.Kind = "pipeline"
			}
			if err := createReviewedRequest(file, r); err != nil {
				t.Fatal(err)
			}
			if err := runDefinitionStart(c, c.Context(), definitionTestClient(srv), definitionTestID); err == nil {
				t.Fatal("changed scope accepted")
			}
			if calls != 0 {
				t.Fatal("scope mismatch reached recovery/dispatch")
			}
		})
	}
}

func TestReviewedWorkflowDefinitionPreconditionsNoDispatch(t *testing.T) {
	for _, condition := range []string{"disabled", "unsupported", "org", "definition", "revision", "schema", "changed-revision", "changed-schema", "paired-review", "missing-inputs", "no-inputs", "yes", "slug"} {
		t.Run(condition, func(t *testing.T) {
			c, _, _, file := definitionTestCommand(t)
			id := definitionTestID
			switch condition {
			case "changed-revision":
				_ = c.Flags().Set("expected-revision", definitionTestDigest)
				_ = c.Flags().Set("expected-input-schema-digest", definitionTestDigest)
			case "changed-schema":
				_ = c.Flags().Set("expected-revision", definitionTestRevision)
				_ = c.Flags().Set("expected-input-schema-digest", definitionTestRevision)
			case "paired-review":
				_ = c.Flags().Set("expected-revision", definitionTestRevision)
			case "no-inputs":
				_ = c.Flags().Set("inputs-file", "/never-open")
			case "yes":
				_ = c.Flags().Set("yes", "false")
			case "slug":
				id = "example"
			}
			srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
				if strings.Contains(q.Query, "mutation") {
					t.Fatal("invalid precondition dispatched")
				}
				data := definitionTestData()
				m := data["workflowDefinitionById"].(map[string]interface{})
				switch condition {
				case "disabled":
					m["definition"].(map[string]interface{})["isEnabled"] = false
				case "unsupported":
					m["inputContract"].(map[string]interface{})["supported"] = false
				case "org":
					m["definition"].(map[string]interface{})["organizationGuid"] = definitionTestID
				case "definition":
					m["guid"] = definitionTestStartID
				case "revision":
					m["revision"] = "invalid"
				case "schema":
					m["inputContract"].(map[string]interface{})["digest"] = "invalid"
				case "missing-inputs":
					m["inputContract"].(map[string]interface{})["acceptsInputs"] = true
				}
				definitionTestResponse(t, w, data)
			})
			defer srv.Close()
			if err := runDefinitionStart(c, c.Context(), definitionTestClient(srv), id); err == nil {
				t.Fatal("precondition unexpectedly accepted")
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Fatal("precondition allocated a request file")
			}
		})
	}
}

func TestReviewedWorkflowDefinitionPrivacyRefusalAndErrors(t *testing.T) {
	for _, mode := range []string{"identity-error", "review-error", "mutation-error", "refused-record", "malformed-file", "bad-metadata"} {
		t.Run(mode, func(t *testing.T) {
			c, out, errOut, file := definitionTestCommand(t)
			_ = c.Flags().Set("json", "true")
			marker := "PRIVATE_ERROR_MARKER"
			srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
				if (mode == "identity-error" && strings.Contains(q.Query, "astroliftMyProfile")) || (mode == "review-error" && strings.Contains(q.Query, "query ReviewedWorkflowDefinition")) || (mode == "mutation-error" && strings.Contains(q.Query, "mutation")) {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []interface{}{map[string]interface{}{"message": marker}}})
					return
				}
				data := definitionTestData()
				if strings.Contains(q.Query, "mutation") {
					if strings.Contains(q.Query, "dispatchLastError") || strings.Contains(q.Query, "message") {
						t.Error("sensitive error fields selected")
					}
					r := definitionTestStart(q.Variables["input"].(map[string]interface{})["requestId"].(string), "refused")
					r["dispatchLastError"] = marker
					if mode == "bad-metadata" {
						r["definitionId"] = definitionTestOrg
					}
					data["startWorkflowDefinition"] = map[string]interface{}{"ok": false, "errors": []interface{}{map[string]interface{}{"code": marker, "message": marker}}, "data": r}
				}
				definitionTestResponse(t, w, data)
			})
			defer srv.Close()
			if mode == "malformed-file" {
				if err := os.WriteFile(file, []byte(`{"`+marker+`":1}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := runDefinitionStart(c, c.Context(), definitionTestClient(srv), definitionTestID)
			if err == nil {
				t.Fatal("failure accepted")
			}
			if strings.Contains(err.Error()+out.String()+errOut.String(), marker) {
				t.Fatal("private failure content exposed")
			}
		})
	}
}

func TestReviewedWorkflowDefinitionInputFileBoundaries(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"PRIVATE_PARSE_MARKER":}`, `{} {}`, strings.Repeat(" ", 65537)} {
		filename := filepath.Join(t.TempDir(), "inputs.json")
		if err := os.WriteFile(filename, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readDefinitionInputs(filename); err == nil || strings.Contains(err.Error(), "PRIVATE_PARSE_MARKER") {
			t.Fatalf("invalid input or unsafe diagnostic: %v", err)
		}
	}
	filename := filepath.Join(t.TempDir(), "inputs.json")
	if err := os.WriteFile(filename, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(filename, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readDefinitionInputs(link); err == nil {
		t.Fatal("symlink inputs accepted")
	}
}

func TestReviewedWorkflowDefinitionGlobalReviewAndJSON(t *testing.T) {
	c, out, _, _ := definitionTestCommand(t)
	_ = c.Flags().Set("json", "true")
	srv := definitionTestServer(t, func(q gqlRequest, w http.ResponseWriter) {
		data := definitionTestData()
		m := data["workflowDefinitionById"].(map[string]interface{})
		m["definition"].(map[string]interface{})["isGlobal"] = true
		m["definition"].(map[string]interface{})["organizationGuid"] = nil
		m["inputContract"].(map[string]interface{})["supported"] = false
		definitionTestResponse(t, w, data)
	})
	defer srv.Close()
	if err := runDefinitionReview(c, c.Context(), definitionTestClient(srv), definitionTestID); err != nil {
		t.Fatal(err)
	}
	var result reviewedDefinition
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.InputContract.Supported || result.GUID != definitionTestID {
		t.Fatalf("review JSON invalid: %v", err)
	}
}
