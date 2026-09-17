package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

// Exercise the actual import -> configure -> activate -> launch HTTP path.
// The server assigns a collision-safe slug and refuses disabled definitions,
// matching the deployed API instead of accepting every run unconditionally.
func TestRunManifestActivation(t *testing.T) {
	for _, tc := range []struct {
		name               string
		dry, noRun, refuse bool
		want               []string
	}{
		{"launch", false, false, false, []string{"preview", "import", "create", "enable", "run"}},
		{"dry run", true, false, false, []string{"preview"}},
		{"configure only", false, true, false, []string{"preview", "import", "create"}},
		{"permission refused", false, false, true, []string{"preview", "import", "create", "enable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runManifestDryRun, runManifestNoRun = tc.dry, tc.noRun
			t.Cleanup(func() { runManifestDryRun, runManifestNoRun = false, false })
			var calls []string
			enabled := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Astrolift-Organization") != "org-owned" {
					t.Error("missing selected tenant")
				}
				var req gqlRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				data := map[string]interface{}{}
				switch {
				case strings.Contains(req.Query, "importWorkflowManifest"):
					preview, _ := req.Variables["preview"].(bool)
					call, slug := "import", "review-copy-2"
					if preview {
						call, slug = "preview", ""
					}
					calls = append(calls, call)
					data["importWorkflowManifest"] = map[string]interface{}{"ok": true, "createdSlug": slug, "manifest": map[string]interface{}{
						"definition": map[string]interface{}{"slug": "review", "name": "Review"},
						"stages":     []map[string]interface{}{{"order": 0, "kind": "human_gate"}},
					}}
				case strings.Contains(req.Query, "createWorkflow"):
					calls = append(calls, "create")
					if req.Variables["definitionSlug"] != "review-copy-2" {
						t.Error("configured the wrong definition")
					}
					data["createWorkflow"] = map[string]interface{}{"ok": true, "workflow": map[string]interface{}{"guid": "wf-id", "slug": "review-config", "name": "Review"}}
				case strings.Contains(req.Query, "updateWorkflowDefinition"):
					calls = append(calls, "enable")
					if req.Variables["slug"] != "review-copy-2" || !strings.Contains(req.Query, "isEnabled: true") {
						t.Error("must enable only the actual new import")
					}
					enabled = !tc.refuse
					if tc.refuse {
						_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []map[string]interface{}{{"message": "permission denied"}}})
						return
					}
					data["updateWorkflowDefinition"] = map[string]interface{}{"ok": true}
				case strings.Contains(req.Query, "runWorkflow"):
					calls = append(calls, "run")
					if req.Variables["workflowId"] != "wf-id" {
						t.Error("wrong configured workflow")
					}
					data["runWorkflow"] = map[string]interface{}{"ok": enabled, "runId": "temporal-1", "workflowRunId": "run-1", "errors": []map[string]interface{}{{"field": "definition", "messages": []string{"Workflow definition is disabled"}}}}
				default:
					t.Errorf("unexpected query: %s", req.Query)
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
			}))
			defer srv.Close()
			client := api.NewClient(srv.URL, "token", false)
			client.SetOrg("org-owned")
			cmd, out, _ := workflowTestCmd()
			err := runWorkflowRunManifest(cmd, context.Background(), client, &config.Config{}, "org-owned", "manifest")
			if tc.refuse {
				if err == nil || !strings.Contains(err.Error(), "permission denied") {
					t.Fatalf("expected permission failure, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("launch: %v", err)
			}
			if !reflect.DeepEqual(calls, tc.want) {
				t.Errorf("calls %v, want %v", calls, tc.want)
			}
			if tc.noRun && !strings.Contains(out.String(), "definition-enable review-copy-2") {
				t.Error("missing activation instructions for disabled import")
			}
			if !tc.dry && !tc.noRun && !tc.refuse && !strings.Contains(out.String(), "WorkflowRun ID: run-1") {
				t.Error("missing run identifier")
			}
		})
	}
}

func TestWorkflowDefinitionEnable(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ok, machine bool
	}{
		{"human", true, false}, {"json", true, true}, {"validation refused", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured gqlRequest
			srv := gqlServer(t, map[string]interface{}{"updateWorkflowDefinition": map[string]interface{}{
				"ok": tc.ok, "errors": []map[string]interface{}{{"field": "definition", "messages": []string{"global definitions are read-only"}}},
			}}, &captured)
			defer srv.Close()
			cmd, out, _ := workflowTestCmd()
			if tc.machine {
				_ = cmd.Flags().Set("json", "true")
			}
			err := runWorkflowDefinitionEnable(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "reviewed")
			if captured.Variables["slug"] != "reviewed" || !strings.Contains(captured.Query, "isEnabled: true") {
				t.Error("wrong activation request")
			}
			if !tc.ok {
				if err == nil || !strings.Contains(err.Error(), "read-only") || out.Len() != 0 {
					t.Fatalf("refusal lost: %v, %q", err, out.String())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.machine {
				var result map[string]interface{}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result["slug"] != "reviewed" || result["isEnabled"] != true {
					t.Fatalf("bad result: %v", result)
				}
			} else if !strings.Contains(out.String(), "Enabled workflow definition: reviewed") {
				t.Error("missing acknowledgement")
			}
		})
	}
}
