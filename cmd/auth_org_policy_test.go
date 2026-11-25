package cmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestOrganizationAuthPolicyUsesTheSelectedOrganizationAndExistingMutations(t *testing.T) {
	requests := make(chan map[string]any, 2)
	server := gqlServerFunc(t, func(req gqlRequest) map[string]any {
		switch {
		case strings.Contains(req.Query, "astroliftOrganizations"):
			return map[string]any{"astroliftOrganizations": []map[string]any{
				{"id": projectTestOrgGUID, "slug": "workforce", "name": "Workforce"},
			}}
		case strings.Contains(req.Query, "updateOrganization"):
			requests <- req.Variables["input"].(map[string]any)
			return map[string]any{"updateOrganization": map[string]any{
				"ok": true, "data": map[string]any{"slug": "workforce", "allowStepUpPin": true},
			}}
		default:
			requests <- req.Variables["input"].(map[string]any)
			return map[string]any{"addOrganizationAllowlistDomain": map[string]any{
				"ok": true, "data": map[string]any{"id": webProjectGUID, "domain": "example.test"},
			}}
		}
	})
	defer server.Close()
	withConfigHome(t, server.URL)
	viper.Set("token", "manager-credential")
	t.Cleanup(func() { viper.Set("token", "") })

	run := func(args ...string) {
		t.Helper()
		cmd := newOrganizationAuthPolicyCommand()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	run("set", "allow-step-up-pin", "true")
	if got, want := <-requests, (map[string]any{"id": projectTestOrgGUID, "allowStepUpPin": true}); !reflect.DeepEqual(got, want) {
		t.Fatalf("organization change = %v, want %v", got, want)
	}
	run("domains", "add", "example.test", "--default-role", "developer", "--requires-review")
	if got, want := <-requests, (map[string]any{"domain": "example.test", "defaultRoleSlug": "developer", "requiresReview": true}); !reflect.DeepEqual(got, want) {
		t.Fatalf("domain change = %v, want %v", got, want)
	}
}
