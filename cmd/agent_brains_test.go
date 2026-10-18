package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

func TestDispatchSendsBrainSlugs(t *testing.T) {
	resetDispatchFlags()
	defer resetDispatchFlags()
	agentDispatchBrains = []string{"handbook", " runbooks "}
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"runAstroliftAgent": map[string]interface{}{"ok": true, "data": map[string]interface{}{"id": "task-1", "status": "queued"}}}, &captured)
	defer srv.Close()
	cmd, _ := agentTestCmd()
	if err := runAgentDispatch(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "triage"); err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(captured.Variables["input"].(map[string]interface{})["brainSlugs"])
	if got != "[handbook runbooks]" {
		t.Fatalf("brainSlugs = %s", got)
	}
}

func TestDispatchBrainsRefusedBeforeAnyCall(t *testing.T) {
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = fmt.Sprintf("b%d", i)
	}
	for name, brains := range map[string][]string{"duplicate": {"a", "a"}, "eleven": eleven, "blank": {" "}} {
		t.Run(name, func(t *testing.T) {
			resetDispatchFlags()
			defer resetDispatchFlags()
			agentDispatchBrains = brains
			var calls int32
			srv := gqlServerFunc(t, func(gqlRequest) map[string]interface{} {
				atomic.AddInt32(&calls, 1)
				return nil
			})
			defer srv.Close()
			cmd, _ := agentTestCmd()
			if err := runAgentDispatch(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "triage"); err == nil {
				t.Fatal("expected refusal")
			}
			if calls != 0 {
				t.Fatalf("%d calls before refusing", calls)
			}
		})
	}
}

func TestDispatchBrainsOnOlderServer(t *testing.T) {
	resetDispatchFlags()
	defer resetDispatchFlags()
	agentDispatchBrains = []string{"handbook"}
	srv := schemaErrorServer(t, map[string]string{"runAstroliftAgent": `Variable '$input' got invalid value; Field 'brainSlugs' is not defined by type 'RunAstroliftAgentInput'.`}, nil)
	defer srv.Close()
	cmd, _ := agentTestCmd()
	err := runAgentDispatch(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "triage")
	if err == nil || !strings.Contains(err.Error(), "older than platform #2432") {
		t.Fatalf("got %v", err)
	}
}

func TestAgentInspectListsBrains(t *testing.T) {
	agentInspectJSON = false
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"agentTask": map[string]interface{}{
		"id": "task-1", "status": "completed",
		"brains": []interface{}{
			map[string]interface{}{"slug": "handbook", "source": "default", "revokedAt": nil, "revokedReason": ""},
			map[string]interface{}{"slug": "runbooks", "source": "requested", "revokedAt": "2026-10-16T10:05:00Z", "revokedReason": "run_ended"},
		},
	}}, &captured)
	defer srv.Close()
	cmd, out := agentTestCmd()
	if err := runAgentInspect(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "task-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(captured.Query, "brains { slug source revokedAt revokedReason }") {
		t.Fatalf("inspect did not ask for brains: %s", captured.Query)
	}
	for _, want := range []string{"handbook (default)", "runbooks (requested) revoked 2026-10-16T10:05:00Z: run_ended"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q:\n%s", want, out.String())
		}
	}
}

func TestAgentInspectOnServerWithoutBrains(t *testing.T) {
	agentInspectJSON = false
	var queries []string
	srv := schemaErrorServer(t, map[string]string{"brains {": `Cannot query field "brains" on type "AstroliftAgentTask".`},
		func(query string) { queries = append(queries, query) })
	defer srv.Close()
	cmd, out := agentTestCmd()
	if err := runAgentInspect(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "task-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "task-1") || strings.Contains(out.String(), "Brains:") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
	if len(queries) < 2 {
		t.Fatalf("no fallback query: %v", queries)
	}
}

func TestAgentShowBrains(t *testing.T) {
	for name, tc := range map[string]struct {
		allowed interface{}
		want    string
	}{
		"any":  {nil, "Brains allowed: any brain the launcher can read"},
		"none": {[]interface{}{}, "Brains allowed: none"},
		"list": {[]interface{}{"runbooks", "ledger"}, "Brains allowed: runbooks, ledger"},
	} {
		t.Run(name, func(t *testing.T) {
			agentShowJSON = false
			var captured gqlRequest
			srv := gqlServer(t, map[string]interface{}{
				"astroliftOrganizations": []map[string]interface{}{{"id": "org-1", "name": "Acme", "slug": "acme"}},
				"agent": map[string]interface{}{
					"id": "w-1", "name": "Triage", "slug": "triage", "appSlug": "ops", "runMode": "once",
					"brains": []interface{}{"handbook"}, "brainsAllowed": tc.allowed,
				},
			}, &captured)
			defer srv.Close()
			cmd, out := agentTestCmd()
			_ = cmd.Flags().Set("org", "acme")
			if err := runAgentShow(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "triage"); err != nil {
				t.Fatal(err)
			}
			if captured.Variables["slug"] != "triage" || captured.Variables["orgId"] != "org-1" {
				t.Fatalf("variables = %v", captured.Variables)
			}
			for _, want := range []string{"Brains:         handbook", tc.want} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %q:\n%s", want, out.String())
				}
			}
		})
	}
}

func TestAgentShowOnServerWithoutBrains(t *testing.T) {
	agentShowJSON = false
	srv := schemaErrorServer(t, map[string]string{"brainsAllowed": `Cannot query field "brains" on type "AstroliftAgentDetail".`}, nil)
	defer srv.Close()
	cmd, out := agentTestCmd()
	_ = cmd.Flags().Set("org", "acme")
	if err := runAgentShow(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "triage"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "older than platform #2432") || !strings.Contains(out.String(), "Triage") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

// schemaErrorServer answers a query containing a key with that GraphQL schema
// error and anything else with a fixed task, agent and organization.
func schemaErrorServer(t *testing.T, refuse map[string]string, seen func(string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if seen != nil {
			seen(req.Query)
		}
		for key, message := range refuse {
			if strings.Contains(req.Query, key) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []map[string]string{{"message": message}}})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
			"astroliftOrganizations": []map[string]interface{}{{"id": "org-1", "name": "Acme", "slug": "acme"}},
			"agentTask":              map[string]interface{}{"id": "task-1", "status": "completed"},
			"agent":                  map[string]interface{}{"id": "w-1", "name": "Triage", "slug": "triage", "runMode": "once"},
		}})
	}))
}
