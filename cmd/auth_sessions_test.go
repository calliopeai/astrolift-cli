package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestAuthSessionsUsesTheAccountSessionAPI(t *testing.T) {
	var mutations []gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]any {
		if strings.Contains(req.Query, "astroliftActiveSessions") {
			return map[string]any{"astroliftActiveSessions": []map[string]any{
				{"id": webProjectGUID, "clientKind": "web", "label": "Work browser"},
			}}
		}
		mutations = append(mutations, req)
		return map[string]any{"revokeAstroliftSession": map[string]any{
			"ok": true, "data": map[string]any{"id": webProjectGUID, "revoked": true},
		}}
	})
	defer srv.Close()
	withConfigHome(t, srv.URL)
	viper.Set("token", "cli-session-test")
	t.Cleanup(func() { viper.Set("token", "") })

	list := newAuthSessionsCommand()
	out := new(bytes.Buffer)
	list.SetOut(out)
	list.SetArgs([]string{"list"})
	if err := list.Execute(); err != nil || !strings.Contains(out.String(), "Work browser") {
		t.Fatalf("list output: %q, error: %v", out, err)
	}
	all := newAuthSessionsCommand()
	all.SetArgs([]string{"revoke-all"})
	if err := all.Execute(); err == nil || len(mutations) != 0 {
		t.Fatal("unconfirmed bulk revoke reached the API")
	}

	revoke := newAuthSessionsCommand()
	revoke.SetArgs([]string{"revoke", webProjectGUID})
	if err := revoke.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(mutations) != 1 || !strings.Contains(mutations[0].Query, "revokeAstroliftSession(") {
		t.Fatalf("unexpected requests: %+v", mutations)
	}
	if input := mutations[0].Variables["input"].(map[string]any); input["sessionId"] != webProjectGUID {
		t.Fatalf("unexpected revoke input: %+v", input)
	}
}
