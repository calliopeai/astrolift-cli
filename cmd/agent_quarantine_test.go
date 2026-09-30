package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuarantineCommandsPreserveOrganizationAndCredentials(t *testing.T) {
	for _, operation := range []string{"list", "clear", "denied"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request gqlRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				if r.Header.Get("Authorization") != "Bearer selected-token" {
					t.Error("wrong credential")
				}
				data := map[string]interface{}{}
				if strings.Contains(request.Query, "astroliftOrganizations") {
					data["astroliftOrganizations"] = []map[string]string{{"id": "selected-id", "slug": "selected-org"}}
				} else {
					calls++
					if r.Header.Get("X-Astrolift-Organization") != "selected-id" {
						t.Error("lost organization ceiling")
					}
					if operation == "list" {
						data["agentQuarantines"] = []map[string]string{{"id": "quarantine-id", "targetKind": "spec", "targetId": "spec-id", "reason": "review required"}}
					} else {
						if request.Variables["id"] != "quarantine-id" {
							t.Error("wrong target")
						}
						data["clearAgentQuarantine"] = map[string]interface{}{"ok": operation == "clear", "errors": []map[string]string{{"code": "PERMISSION_DENIED", "message": "scope refused"}}}
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
			}))
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			cmd, out := agentTestCmd()
			cmd.SetContext(context.Background())
			_ = cmd.Flags().Set("org", "selected-org")
			_ = cmd.Flags().Set("json", "true")
			cmd.Flags().Bool("yes", true, "")
			var err error
			if operation == "list" {
				err = agentQuarantineListCmd.RunE(cmd, nil)
			} else {
				err = agentQuarantineClearCmd.RunE(cmd, []string{"quarantine-id"})
			}
			if calls != 1 {
				t.Fatalf("requests=%d", calls)
			}
			if operation == "denied" {
				if err == nil || strings.Contains(out.String(), "cleared") {
					t.Fatalf("refusal lost: err=%v output=%s", err, out)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQuarantineClearNeedsExplicitNoninteractiveConsent(t *testing.T) {
	cmd, _ := agentTestCmd()
	cmd.Flags().Bool("yes", false, "")
	_ = cmd.Flags().Set("no-prompt", "true")
	if err := agentQuarantineClearCmd.RunE(cmd, []string{"id"}); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("unexpected result: %v", err)
	}
}
