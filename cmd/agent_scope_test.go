package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestAgentCommandsHonorOrganizationBeforeTaskRequests(t *testing.T) {
	oldYes := agentCancelYes
	agentCancelYes = true
	t.Cleanup(func() { agentCancelYes = oldYes })
	for _, command := range []*cobra.Command{agentInspectCmd, agentLogsCmd, agentSendCmd, agentCancelCmd, agentDispatchCmd} {
		for _, organization := range []string{"selected-org", "missing-org"} {
			t.Run(command.Name()+"/"+organization, func(t *testing.T) {
				operations := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request gqlRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					if r.Header.Get("Authorization") != "Bearer selected-token" {
						t.Error("wrong server credentials")
					}
					data := map[string]interface{}{}
					if strings.Contains(request.Query, "astroliftOrganizations") {
						data["astroliftOrganizations"] = []map[string]string{{"id": "default-id", "slug": "org"}, {"id": "selected-id", "slug": "selected-org"}}
					} else {
						operations++
						if r.Header.Get("X-Astrolift-Organization") != "selected-id" {
							t.Error("task operation did not use selected organization")
						}
						data = map[string]interface{}{
							"agentTask":          map[string]string{"id": "task", "status": "failed"},
							"agentTaskLogs":      []string{"fixture log"},
							"sendAgentTaskInput": map[string]interface{}{"ok": true},
							"cancelTask":         map[string]interface{}{"ok": true},
							"runAstroliftAgent":  map[string]interface{}{"ok": true, "data": map[string]string{"id": "task", "status": "pending"}},
						}
					}
					if err := json.NewEncoder(w).Encode(map[string]interface{}{"data": data}); err != nil {
						t.Error(err)
					}
				}))
				defer server.Close()
				serverSelectionFixture(t, server.URL)
				invocation, _ := agentTestCmd()
				invocation.SetContext(context.Background())
				if err := invocation.Flags().Set("org", organization); err != nil {
					t.Fatal(err)
				}
				args := []string{"task"}
				if command == agentSendCmd {
					args = append(args, "continue")
				}
				err := command.RunE(invocation, args)
				if organization == "missing-org" {
					if err == nil || operations != 0 {
						t.Fatalf("unknown organization reached task operation: err=%v requests=%d", err, operations)
					}
				} else if err != nil || operations != 1 {
					t.Fatalf("selected organization failed: err=%v requests=%d", err, operations)
				}
			})
		}
	}
}
