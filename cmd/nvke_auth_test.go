package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNVKESignInRequiresExplicitServerSupportBeforeStarting(t *testing.T) {
	for _, kind := range []string{"cli-nvke", "cli-nvke-read"} {
		for _, supported := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", kind, supported), func(t *testing.T) {
				starts := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "" {
						t.Error("scope preflight forwarded credentials")
					}
					if r.URL.Path == "/app/gql/config/public/" {
						caps := []string{}
						if supported {
							caps = append(caps, "identity.nvke_scopes")
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"astroliftServerInfo": map[string]any{"capabilities": caps}}})
						return
					}
					starts++
					if r.URL.Path != "/api/cli/v1/auth/start" {
						t.Error(r.URL.Path)
					}
					var input map[string]string
					_ = json.NewDecoder(r.Body).Decode(&input)
					if input["client_kind"] != kind {
						t.Error("wrong device profile")
					}
					fmt.Fprint(w, `{"session_id":"owned-session","login_url":"https://example.test/approve"}`)
				}))
				defer server.Close()
				_, err := startScopedLogin(context.Background(), server.URL, kind)
				if supported {
					if err != nil || starts != 1 {
						t.Fatalf("starts=%d err=%v", starts, err)
					}
				} else if err == nil || starts != 0 {
					t.Fatalf("old server started an incompatible profile: starts=%d err=%v", starts, err)
				}
			})
		}
	}
}

func TestNVKESignInHelpExplainsScopeAndSensitiveOperations(t *testing.T) {
	text := strings.Join(strings.Fields(authLoginCmd.Long), " ")
	for _, phrase := range []string{"--scope nvke-read", "--scope nvke", "workload credentials", "approved organization", "identity.nvke_scopes", "no app access"} {
		if !strings.Contains(text, phrase) {
			t.Errorf("help is missing %q", phrase)
		}
	}
}
