package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

func TestAgentInspectStartupDiagnostic(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%v", asJSON), func(t *testing.T) {
			prior := agentInspectJSON
			agentInspectJSON = asJSON
			defer func() { agentInspectJSON = prior }()
			var captured gqlRequest
			srv := gqlServer(t, map[string]interface{}{"agentTask": map[string]interface{}{
				"id": "waiting-task", "status": "provisioning",
				"startupDiagnostic": map[string]interface{}{
					"phase": "Pending", "reason": "Unschedulable", "message": "2 Insufficient cpu",
					"podName": "owned-pod", "observedAt": "2026-09-30T00:00:00Z",
				},
			}}, &captured)
			defer srv.Close()
			command, out := agentTestCmd()
			if err := runAgentInspect(command, context.Background(), api.NewClient(srv.URL, "token", false), &config.Config{}, "waiting-task"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(captured.Query, "startupDiagnostic {") || captured.Variables["id"] != "waiting-task" {
				t.Fatalf("missing exact task diagnostic projection: %+v", captured)
			}
			if !strings.Contains(out.String(), "2 Insufficient cpu") || !strings.Contains(out.String(), "provisioning") {
				t.Fatalf("lost startup state: %s", out.String())
			}
		})
	}
}

func TestStartupDiagnosticCompatibilityDoesNotHideFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []string
		calls    int
	}{
		{"old server", []string{`Cannot query field "startupDiagnostic" on type "AstroliftAgentBox".`}, 2},
		{"permission", []string{"permission denied: agent_box.view"}, 1},
		{"other field", []string{`Cannot query field "agentBox" on type "Query".`}, 1},
		{"mixed", []string{`Cannot query field "startupDiagnostic" on type "AstroliftAgentBox".`, "permission denied"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req gqlRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.Variables["slug"] != "owned-box" || r.Header.Get("Authorization") != "Bearer token" {
					t.Errorf("scope or credentials changed in fallback")
				}
				if calls == 1 {
					messages := make([]map[string]string, len(tc.messages))
					for i, message := range tc.messages {
						messages[i] = map[string]string{"message": message}
					}
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": messages})
					return
				}
				if strings.Contains(req.Query, "startupDiagnostic") {
					t.Error("fallback did not omit unsupported field")
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"agentBox": boxPayload("owned-box", "running")}})
			}))
			defer srv.Close()
			box, err := getBox(context.Background(), api.NewClient(srv.URL, "token", false), "owned-box")
			if calls != tc.calls {
				t.Fatalf("requests=%d, want %d", calls, tc.calls)
			}
			if tc.calls == 2 {
				if err != nil || box == nil || box.Slug != "owned-box" || box.StartupDiagnostic != nil {
					t.Fatalf("legacy read failed: %v, %v", box, err)
				}
			} else if err == nil {
				t.Fatal("real failure was hidden")
			}
		})
	}
}

func TestBoxWaitShowsDiagnosticAndRecovers(t *testing.T) {
	prior := boxWaitPollInterval
	boxWaitPollInterval = time.Millisecond
	defer func() { boxWaitPollInterval = prior }()
	polls := 0
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		polls++
		box := boxPayload("owned-box", "provisioning")
		if polls < 3 {
			box["startupDiagnostic"] = map[string]interface{}{"phase": "Pending", "reason": "Unschedulable", "message": "Insufficient cpu"}
		} else {
			box["status"] = "running"
			box["startupDiagnostic"] = map[string]interface{}{"phase": "Running", "reason": "", "message": ""}
		}
		return map[string]interface{}{"agentBox": box}
	})
	defer srv.Close()
	command, out := agentTestCmd()
	command.SetErr(out)
	box, err := waitForBox(command, context.Background(), api.NewClient(srv.URL, "token", false), &agentBox{Slug: "owned-box", Status: "provisioning"})
	if err != nil || box.Status != "running" {
		t.Fatalf("recovery failed: %v, %v", box, err)
	}
	if strings.Count(out.String(), "Unschedulable: Insufficient cpu") != 1 {
		t.Fatalf("diagnostic missing or repeated: %s", out.String())
	}
	if box.StartupDiagnostic.summary() != "" {
		t.Fatal("ready observation displays stale reason")
	}
}
