package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestAuthPolicyRefusesUnconfirmedChangesAndSetsTheSelectedKey(t *testing.T) {
	var mutations []gqlRequest
	policy := map[string]any{
		"key": "REQUIRE_LOCAL_AUTHENTICATOR", "group": "Password sign-in",
		"kind": "bool", "value": "false", "default": "false", "editable": true,
		"warning": "Changes password sign-in.",
	}
	server := gqlServerFunc(t, func(req gqlRequest) map[string]any {
		if strings.Contains(req.Query, "astroliftPlatformSettings") {
			return map[string]any{"astroliftPlatformSettings": []map[string]any{
				policy, {"key": "OTHER_SETTING", "group": "Agent pods", "editable": true},
			}}
		}
		mutations = append(mutations, req)
		return map[string]any{"setPlatformSetting": map[string]any{
			"ok": true, "data": map[string]any{"setting": map[string]any{
				"key": policy["key"], "value": "true",
			}},
		}}
	})
	defer server.Close()
	withConfigHome(t, server.URL)
	viper.Set("token", "cli-policy-test")
	t.Cleanup(func() { viper.Set("token", "") })

	for _, args := range [][]string{{"set", "REQUIRE_LOCAL_AUTHENTICATOR", "true"}, {"set", "OTHER_SETTING", "true", "--yes"}} {
		cmd := newAuthPolicyCommand()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("expected %v to be refused", args)
		}
	}
	if len(mutations) != 0 {
		t.Fatal("an unconfirmed or unrelated setting reached the mutation")
	}

	cmd := newAuthPolicyCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"set", "REQUIRE_LOCAL_AUTHENTICATOR", "true", "--yes"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(mutations) != 1 || mutations[0].Variables["confirmed"] != true ||
		mutations[0].Variables["value"] != "true" ||
		mutations[0].Variables["key"] != "REQUIRE_LOCAL_AUTHENTICATOR" {
		t.Fatalf("unexpected mutations: %+v", mutations)
	}
	if !strings.Contains(out.String(), "REQUIRE_LOCAL_AUTHENTICATOR = true") {
		t.Fatalf("unexpected response: %q", out)
	}
}
