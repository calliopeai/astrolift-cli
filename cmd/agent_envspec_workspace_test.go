package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestEnvSpecWorkspaceFlagOnlySendsExplicitValues(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		for _, value := range []string{"", "true", "false"} {
			t.Run(operation+"/"+map[string]string{"": "omitted", "true": "enabled", "false": "disabled"}[value], func(t *testing.T) {
				flag := agentEnvSpecUpsertCmd.Flags().Lookup("box-workspace")
				flag.Changed = false
				envSpecBoxWorkspace = false
				envSpecAgentType = "codex"
				t.Cleanup(func() { flag.Changed = false; envSpecBoxWorkspace = false; envSpecAgentType = "" })
				if value != "" {
					if err := agentEnvSpecUpsertCmd.Flags().Set("box-workspace", value); err != nil {
						t.Fatal(err)
					}
				}
				var captured gqlRequest
				srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
					if strings.Contains(req.Query, "astroliftOrganizations") {
						return map[string]interface{}{"astroliftOrganizations": []map[string]string{{"id": "org-guid", "slug": "org", "name": "Org"}}}
					}
					if req.Organization != "org-guid" {
						t.Errorf("spec lookup/write did not bind the resolved organization: %#v", req)
					}
					if strings.Contains(req.Query, "query(") {
						if operation == "create" {
							return map[string]interface{}{"agentEnvironmentSpec": nil}
						}
						return map[string]interface{}{"agentEnvironmentSpec": map[string]interface{}{"slug": "dev"}}
					}
					captured = req
					return map[string]interface{}{operation + "AgentEnvironmentSpec": map[string]interface{}{"ok": true, "data": map[string]interface{}{"slug": "dev"}}}
				})
				defer srv.Close()
				appWireCredentials(t, srv.URL)
				// The upsert command owns the flag; exercise its actual request path.
				originalContext := agentEnvSpecUpsertCmd.Context()
				agentEnvSpecUpsertCmd.SetContext(context.Background())
				agentEnvSpecUpsertCmd.SetOut(&bytes.Buffer{})
				t.Cleanup(func() {
					agentEnvSpecUpsertCmd.SetContext(originalContext)
					agentEnvSpecUpsertCmd.SetOut(nil)
				})
				if err := runEnvSpecUpsert(agentEnvSpecUpsertCmd, []string{"dev"}); err != nil {
					t.Fatal(err)
				}
				input := captured.Variables["input"].(map[string]interface{})
				sent, exists := input["boxWorkspace"]
				if exists != (value != "") || (exists && sent != (value == "true")) {
					t.Fatalf("workspace selection lost: %#v", input)
				}
				if !strings.Contains(captured.Query, operation+"AgentEnvironmentSpec") {
					t.Fatalf("wrong mutation: %s", captured.Query)
				}
			})
		}
	}
}
