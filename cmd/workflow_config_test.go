package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// ---- definitions -------------------------------------------------------------

func definitionsData() map[string]interface{} {
	return map[string]interface{}{
		"workflowDefinitions": []map[string]interface{}{
			{"guid": "d-1", "name": "OODA", "slug": "ooda", "patternKind": "chained",
				"isEnabled": true, "isGlobal": true, "organizationGuid": nil, "stageCount": 4},
			{"guid": "d-2", "name": "My Review", "slug": "my-review", "patternKind": "review_loop",
				"isEnabled": false, "isGlobal": false, "organizationGuid": "org-1", "stageCount": 2},
		},
	}
}

func TestWorkflowDefinitionsRendersTable(t *testing.T) {
	workflowDefsGlobal, workflowDefsOrg = false, false

	var captured gqlRequest
	srv := gqlServer(t, definitionsData(), &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowDefinitions(cmd, context.Background(), client); err != nil {
		t.Fatalf("definitions: %v", err)
	}
	if !strings.Contains(captured.Query, "workflowDefinitions") {
		t.Errorf("did not call workflowDefinitions:\n%s", captured.Query)
	}
	got := out.String()
	for _, want := range []string{
		"SLUG", "PATTERN", "SCOPE",
		"ooda", "chained", "global",
		"my-review", "review_loop", "org",
		"2 definition(s) shown.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("definitions table missing %q:\n%s", want, got)
		}
	}
}

func TestWorkflowDefinitionsScopeFilters(t *testing.T) {
	cases := []struct {
		name           string
		global, org    bool
		want, wantGone string
	}{
		{"--global keeps platform templates", true, false, "ooda", "my-review"},
		{"--org-only keeps org definitions", false, true, "my-review", "ooda"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workflowDefsGlobal, workflowDefsOrg = tc.global, tc.org
			defer func() { workflowDefsGlobal, workflowDefsOrg = false, false }()

			srv := gqlServer(t, definitionsData(), nil)
			defer srv.Close()

			client := api.NewClient(srv.URL, "tok", false)
			cmd, out, _ := workflowTestCmd()
			if err := runWorkflowDefinitions(cmd, context.Background(), client); err != nil {
				t.Fatalf("definitions: %v", err)
			}
			got := out.String()
			if !strings.Contains(got, tc.want) {
				t.Errorf("filtered table missing %q:\n%s", tc.want, got)
			}
			if strings.Contains(got, tc.wantGone) {
				t.Errorf("filtered table still shows %q:\n%s", tc.wantGone, got)
			}
		})
	}
}

func TestWorkflowDefinitionsFlagsMutuallyExclusive(t *testing.T) {
	workflowDefsGlobal, workflowDefsOrg = true, true
	defer func() { workflowDefsGlobal, workflowDefsOrg = false, false }()

	cmd, _, _ := workflowTestCmd()
	err := runWorkflowDefinitions(cmd, context.Background(), api.NewClient("http://unused", "tok", false))
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually-exclusive error, got %v", err)
	}
}

// ---- definition detail ---------------------------------------------------------

func TestWorkflowDefinitionShowsStages(t *testing.T) {
	fanOut := 3
	srv := gqlServer(t, map[string]interface{}{
		"workflowDefinition": map[string]interface{}{
			"guid": "d-1", "name": "OODA", "slug": "ooda", "description": "Observe...",
			"patternKind": "chained", "isEnabled": true, "isGlobal": true,
			"organizationGuid": nil, "stageCount": 2,
		},
		"workflowStages": []map[string]interface{}{
			{"order": 0, "kind": "agent_dispatch", "onFailure": "retry", "timeoutSeconds": 600,
				"fanOutCount": fanOut, "agentDefinitionName": "observer",
				"environmentSpecSlug": "observer-prod", "outputKey": "observations"},
			{"order": 1, "kind": "human_gate", "onFailure": "fail", "timeoutSeconds": 86400,
				"fanOutCount": nil, "agentDefinitionName": nil},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowDefinition(cmd, context.Background(), client, "ooda"); err != nil {
		t.Fatalf("definition: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"OODA (ooda)", "Pattern:     chained", "Scope:       global",
		"[0] agent_dispatch on_failure=retry timeout=600s fan_out=3 agent=observer",
		"environment_spec=observer-prod", "output_key=observations",
		"[1] human_gate on_failure=fail timeout=86400s",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("definition detail missing %q:\n%s", want, got)
		}
	}
}

func TestWorkflowDefinitionNotFound(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"workflowDefinition": nil,
		"workflowStages":     []interface{}{},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _, _ := workflowTestCmd()
	err := runWorkflowDefinition(cmd, context.Background(), client, "ghost")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

// ---- clone ---------------------------------------------------------------------

func TestWorkflowCloneHappyPath(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"cloneWorkflowDefinition": map[string]interface{}{
			"ok": true, "errors": []interface{}{}, "slug": "ooda-copy",
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowClone(cmd, context.Background(), client, "ooda"); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if !strings.Contains(captured.Query, "cloneWorkflowDefinition(slug: $slug)") {
		t.Errorf("did not call cloneWorkflowDefinition:\n%s", captured.Query)
	}
	if captured.Variables["slug"] != "ooda" {
		t.Errorf("slug var = %v, want ooda", captured.Variables["slug"])
	}
	if !strings.Contains(out.String(), "Cloned definition: ooda-copy") {
		t.Errorf("missing created slug:\n%s", out.String())
	}
}

func TestWorkflowCloneSurfacesErrorEnvelope(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"cloneWorkflowDefinition": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{"field": "slug", "messages": []interface{}{`Definition "ghost" not visible`}},
			},
			"slug": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _, _ := workflowTestCmd()
	err := runWorkflowClone(cmd, context.Background(), client, "ghost")
	if err == nil {
		t.Fatal("expected error when ok=false")
	}
	if !strings.Contains(err.Error(), "slug") || !strings.Contains(err.Error(), "not visible") {
		t.Errorf("error did not surface the validation envelope: %v", err)
	}
}

// ---- list ----------------------------------------------------------------------

func TestWorkflowListRendersTable(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"workflows": []map[string]interface{}{
			{"guid": "w-1", "name": "Nightly OODA", "slug": "nightly-ooda",
				"definitionSlug": "ooda", "triggerKind": "schedule", "isEnabled": true, "runCount": 7},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowList(cmd, context.Background(), client); err != nil {
		t.Fatalf("list: %v", err)
	}
	got := out.String()
	for _, want := range []string{"nightly-ooda", "ooda", "schedule", "yes", "7", "1 workflow(s) shown."} {
		if !strings.Contains(got, want) {
			t.Errorf("list table missing %q:\n%s", want, got)
		}
	}
}

// ---- create --------------------------------------------------------------------

func TestWorkflowCreateSendsBindingsAndInputs(t *testing.T) {
	workflowCreateDefinition = "ooda"
	workflowCreateName = "Nightly OODA"
	workflowCreateSlug = "nightly-ooda"
	workflowCreateBinds = []string{"0=guid-observer", "2=guid-actor"}
	workflowCreateInputs = []string{"repo=myorg/api", "depth=full"}
	workflowCreateTrigger = "schedule"
	workflowCreateCron = "0 9 * * 1"
	defer func() {
		workflowCreateDefinition, workflowCreateName, workflowCreateSlug = "", "", ""
		workflowCreateBinds, workflowCreateInputs = nil, nil
		workflowCreateTrigger, workflowCreateCron = "manual", ""
	}()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"createWorkflow": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"workflow": map[string]interface{}{
				"guid": "w-1", "name": "Nightly OODA", "slug": "nightly-ooda",
				"triggerKind": "schedule", "isEnabled": true,
			},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowCreate(cmd, context.Background(), client); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Repeated --bind flags became the stage_bindings shape the backend
	// validates: {"<order>": {"agent_workload_id": guid}}, string order keys.
	bindings, ok := captured.Variables["stageBindings"].(map[string]interface{})
	if !ok {
		t.Fatalf("stageBindings var missing or wrong type: %v", captured.Variables["stageBindings"])
	}
	for order, guid := range map[string]string{"0": "guid-observer", "2": "guid-actor"} {
		b, ok := bindings[order].(map[string]interface{})
		if !ok || b["agent_workload_id"] != guid {
			t.Errorf("stageBindings[%q] = %v, want {agent_workload_id: %s}", order, bindings[order], guid)
		}
	}
	inputs, ok := captured.Variables["inputs"].(map[string]interface{})
	if !ok || inputs["repo"] != "myorg/api" || inputs["depth"] != "full" {
		t.Errorf("inputs var = %v", captured.Variables["inputs"])
	}
	if captured.Variables["triggerKind"] != "schedule" || captured.Variables["scheduleCron"] != "0 9 * * 1" {
		t.Errorf("trigger vars wrong: %v / %v", captured.Variables["triggerKind"], captured.Variables["scheduleCron"])
	}
	if !strings.Contains(out.String(), "Created workflow: Nightly OODA (nightly-ooda)") {
		t.Errorf("missing created line:\n%s", out.String())
	}
}

func TestWorkflowCreateRejectsMalformedBind(t *testing.T) {
	cases := []struct {
		bind    string
		wantSub string
	}{
		{"noequals", "must be <stageOrder>=<agentWorkloadGuid>"},
		{"=guid", "must be <stageOrder>=<agentWorkloadGuid>"},
		{"one=", "must be <stageOrder>=<agentWorkloadGuid>"},
		{"abc=guid", "stage order must be an integer"},
	}
	for _, tc := range cases {
		if _, err := parseStageBindings([]string{tc.bind}); err == nil || !strings.Contains(err.Error(), tc.wantSub) {
			t.Errorf("parseStageBindings(%q) = %v, want error containing %q", tc.bind, err, tc.wantSub)
		}
	}
}

func TestWorkflowCreateSurfacesUnboundStageError(t *testing.T) {
	workflowCreateDefinition = "ooda"
	workflowCreateName = "Nightly"
	workflowCreateTrigger = "manual"
	defer func() {
		workflowCreateDefinition, workflowCreateName = "", ""
	}()

	srv := gqlServer(t, map[string]interface{}{
		"createWorkflow": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{"field": "stage_bindings",
					"messages": []interface{}{"Unbound agent_dispatch stage(s): stage 0 (observer)"}},
			},
			"workflow": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _, _ := workflowTestCmd()
	err := runWorkflowCreate(cmd, context.Background(), client)
	if err == nil || !strings.Contains(err.Error(), "Unbound agent_dispatch") {
		t.Fatalf("expected unbound-stage error surfaced, got %v", err)
	}
}

// ---- run -----------------------------------------------------------------------

func TestWorkflowRunResolvesSlugAndPrintsRunID(t *testing.T) {
	workflowRunConfigInputs = []string{"branch=main"}
	defer func() { workflowRunConfigInputs = nil }()

	// One handler serves both the workflow(slug) resolution and runWorkflow.
	var mu sync.Mutex
	var runVars map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		data := map[string]interface{}{}
		if strings.Contains(req.Query, "runWorkflow(") {
			mu.Lock()
			runVars = req.Variables
			mu.Unlock()
			data["runWorkflow"] = map[string]interface{}{
				"ok": true, "errors": []interface{}{},
				"runId": "ConfiguredWorkflowRun-99", "workflowRunId": "99",
			}
		} else {
			data["workflow"] = map[string]interface{}{
				"guid": "w-guid-1", "name": "Nightly OODA", "slug": "nightly-ooda",
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowRunConfig(cmd, context.Background(), client, "nightly-ooda"); err != nil {
		t.Fatalf("run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if runVars["workflowId"] != "w-guid-1" {
		t.Errorf("workflowId var = %v, want the resolved guid", runVars["workflowId"])
	}
	inputs, ok := runVars["inputs"].(map[string]interface{})
	if !ok || inputs["branch"] != "main" {
		t.Errorf("inputs var = %v", runVars["inputs"])
	}
	got := out.String()
	if !strings.Contains(got, "Run ID:         ConfiguredWorkflowRun-99") ||
		!strings.Contains(got, "WorkflowRun ID: 99") {
		t.Errorf("run ids not extracted:\n%s", got)
	}
}

func TestWorkflowRunUnknownSlug(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{"workflow": nil}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _, _ := workflowTestCmd()
	err := runWorkflowRunConfig(cmd, context.Background(), client, "ghost")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

// ---- runs / --watch --------------------------------------------------------------

func runRow(guid, state string, completed bool) map[string]interface{} {
	var completedAt interface{}
	if completed {
		completedAt = "2026-07-01T10:05:00+00:00"
	}
	return map[string]interface{}{
		"guid": guid, "currentState": state, "temporalWorkflowId": "t-" + guid,
		"startedAt": "2026-07-01T10:00:00+00:00", "completedAt": completedAt,
		"isCompleted": completed,
	}
}

func TestWorkflowRunsRendersTable(t *testing.T) {
	workflowRunsWatch = false

	srv := gqlServer(t, map[string]interface{}{
		"workflow": map[string]interface{}{"guid": "w-1", "name": "N", "slug": "nightly-ooda"},
		"workflowRuns": []interface{}{
			runRow("run-2", "acting", false),
			runRow("run-1", "completed", true),
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowRuns(cmd, context.Background(), client, "nightly-ooda"); err != nil {
		t.Fatalf("runs: %v", err)
	}
	got := out.String()
	for _, want := range []string{"RUN ID", "run-2", "acting", "run-1", "completed", "2026-07-01T10:05:00"} {
		if !strings.Contains(got, want) {
			t.Errorf("runs table missing %q:\n%s", want, got)
		}
	}
}

func TestWorkflowRunsWatchStopsOnTerminal(t *testing.T) {
	workflowRunsWatch = true
	defer func() { workflowRunsWatch = false }()

	prev := workflowRunsPollIntervalForTest
	workflowRunsPollIntervalForTest = 10 * time.Millisecond
	defer func() { workflowRunsPollIntervalForTest = prev }()

	// The newest run advances observing → acting → completed across polls;
	// --watch must print each transition once and exit on the terminal state.
	var mu sync.Mutex
	states := []struct {
		state    string
		terminal bool
	}{
		{"observing", false},
		{"acting", false},
		{"completed", true},
	}
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		data := map[string]interface{}{}
		if strings.Contains(req.Query, "workflowRuns(") {
			mu.Lock()
			s := states[idx]
			if idx < len(states)-1 {
				idx++
			}
			mu.Unlock()
			data["workflowRuns"] = []interface{}{runRow("run-9", s.state, s.terminal)}
		} else {
			data["workflow"] = map[string]interface{}{"guid": "w-1", "name": "N", "slug": "nightly-ooda"}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()

	done := make(chan error, 1)
	go func() { done <- runWorkflowRuns(cmd, context.Background(), client, "nightly-ooda") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runs --watch: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not terminate on the terminal state")
	}

	got := out.String()
	for _, want := range []string{"Watching run run-9", "→ acting", "→ completed", "Final state: completed"} {
		if !strings.Contains(got, want) {
			t.Errorf("watch output missing %q:\n%s", want, got)
		}
	}
}

func TestWorkflowRunsWatchAlreadyTerminal(t *testing.T) {
	workflowRunsWatch = true
	defer func() { workflowRunsWatch = false }()

	srv := gqlServer(t, map[string]interface{}{
		"workflow":     map[string]interface{}{"guid": "w-1", "name": "N", "slug": "nightly-ooda"},
		"workflowRuns": []interface{}{runRow("run-1", "completed", true)},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowRuns(cmd, context.Background(), client, "nightly-ooda"); err != nil {
		t.Fatalf("runs --watch (terminal): %v", err)
	}
	if !strings.Contains(out.String(), "already terminal: completed") {
		t.Errorf("missing already-terminal notice:\n%s", out.String())
	}
}

// ---- import ------------------------------------------------------------------

func TestWorkflowImportPreviewDoesNotPersist(t *testing.T) {
	workflowImportPreview = true
	defer func() { workflowImportPreview = false }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"importWorkflowManifest": map[string]interface{}{
			"ok": true, "errors": []interface{}{}, "createdSlug": nil,
			"manifest": map[string]interface{}{
				"definition": map[string]interface{}{
					"slug": "feature-dev", "name": "Feature Dev", "pattern": "chained", "description": "",
				},
				"stages": []interface{}{
					map[string]interface{}{"order": 0, "kind": "agent_dispatch", "role": "implementer",
						"agent": nil, "skills": []interface{}{}, "onFailure": "fail", "timeout": 600,
						"fanOut": "0", "prompt": nil, "approvers": []interface{}{}},
				},
			},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowImport(cmd, context.Background(), client, "[workflow]\nslug=\"feature-dev\"\n"); err != nil {
		t.Fatalf("import --preview: %v", err)
	}
	if captured.Variables["preview"] != true {
		t.Errorf("preview var = %v, want true", captured.Variables["preview"])
	}
	got := out.String()
	if !strings.Contains(got, "Preview OK — nothing persisted.") ||
		!strings.Contains(got, "Feature Dev (feature-dev)") ||
		!strings.Contains(got, "1 stage(s)") {
		t.Errorf("preview output wrong:\n%s", got)
	}
}

func TestWorkflowImportPersistsAndPrintsSlug(t *testing.T) {
	workflowImportPreview = false

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"importWorkflowManifest": map[string]interface{}{
			"ok": true, "errors": []interface{}{}, "createdSlug": "feature-dev", "manifest": nil,
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowImport(cmd, context.Background(), client, "[workflow]\nslug=\"feature-dev\"\n"); err != nil {
		t.Fatalf("import: %v", err)
	}
	if captured.Variables["preview"] != false {
		t.Errorf("preview var = %v, want false", captured.Variables["preview"])
	}
	if !strings.Contains(out.String(), "Imported workflow definition: feature-dev") {
		t.Errorf("missing created slug:\n%s", out.String())
	}
}

func TestWorkflowImportSurfacesErrorEnvelope(t *testing.T) {
	workflowImportPreview = false

	srv := gqlServer(t, map[string]interface{}{
		"importWorkflowManifest": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{"field": "toml",
					"messages": []interface{}{"stage[0].kind must be one of [...]"}},
			},
			"createdSlug": nil, "manifest": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _, _ := workflowTestCmd()
	err := runWorkflowImport(cmd, context.Background(), client, "bad")
	if err == nil || !strings.Contains(err.Error(), "stage[0].kind") {
		t.Fatalf("expected envelope error surfaced, got %v", err)
	}
}

// ---- delete / definition-delete -----------------------------------------------

func TestWorkflowDeleteRefusesWithoutYes(t *testing.T) {
	workflowDeleteYes = false

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _, _ := workflowTestCmd()
	err := runWorkflowDelete(cmd, context.Background(), client, "nightly-report")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected refusal mentioning --yes, got: %v", err)
	}
	if captured.Query != "" {
		t.Errorf("a request was sent without --yes:\n%s", captured.Query)
	}
}

func TestWorkflowDeleteHappyPath(t *testing.T) {
	workflowDeleteYes = true
	defer func() { workflowDeleteYes = false }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"deleteWorkflow": map[string]interface{}{"ok": true, "errors": []interface{}{}},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runWorkflowDelete(cmd, context.Background(), client, "nightly-report"); err != nil {
		t.Fatalf("runWorkflowDelete: %v", err)
	}
	if !strings.Contains(captured.Query, "deleteWorkflow(slug: $slug)") {
		t.Errorf("query did not call deleteWorkflow:\n%s", captured.Query)
	}
	if captured.Variables["slug"] != "nightly-report" {
		t.Errorf("slug var = %v, want nightly-report", captured.Variables["slug"])
	}
	if !strings.Contains(out.String(), "Deleted workflow: nightly-report") {
		t.Errorf("output wrong:\n%s", out.String())
	}
}

func TestWorkflowDefinitionDeleteSurfacesProtectError(t *testing.T) {
	workflowDefDeleteYes = true
	defer func() { workflowDefDeleteYes = false }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"deleteWorkflowDefinition": map[string]interface{}{
			"ok": false,
			"errors": []map[string]interface{}{
				{"field": "slug", "messages": []string{
					`Cannot delete workflow definition "ooda" — 2 configured Workflow(s) still use it. Delete those first.`,
				}},
			},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _, _ := workflowTestCmd()
	err := runWorkflowDefinitionDelete(cmd, context.Background(), client, "ooda")
	if err == nil || !strings.Contains(err.Error(), "still use it") {
		t.Fatalf("expected PROTECT refusal surfaced, got: %v", err)
	}
	if !strings.Contains(captured.Query, "deleteWorkflowDefinition(slug: $slug)") {
		t.Errorf("query did not call deleteWorkflowDefinition:\n%s", captured.Query)
	}
}
