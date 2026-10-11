package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

func resetRoleFlags() {
	roleListSystem, roleListCustom = false, false
	roleSearch, roleName, roleScopeLevel, roleDescription, roleFrom = "", "", "", "", ""
	rolePermissions = nil
	roleBindingsRole, roleBindingsHolder = "", ""
	roleGrantUser, roleGrantGroup, roleGrantTeam, roleGrantProject, roleGrantExpires = "", "", "", "", ""
	roleRevokeConfirmed = false
}

var testOrg = orgRef{ID: "org-guid", Slug: "acme", Name: "Acme"}

func rolesFixture() []map[string]interface{} {
	return []map[string]interface{}{
		{"id": "sys-1", "slug": "deployer", "name": "Deployer", "scopeLevel": "PROJECT", "permissions": []string{"app.deploy"}, "isSystem": true},
		{"id": "own-1", "slug": "deployer", "name": "Our deployer", "scopeLevel": "PROJECT", "permissions": []string{"app.deploy", "app.read_logs"}, "isSystem": false, "bindingsCount": 2},
	}
}

func roleServer(t *testing.T, writes *[]gqlRequest, answer func(gqlRequest) map[string]interface{}) *api.Client {
	t.Helper()
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		switch {
		case strings.Contains(req.Query, "astroliftRolesPage"):
			return map[string]interface{}{"astroliftRolesPage": map[string]interface{}{"items": rolesFixture()}}
		case strings.Contains(req.Query, "astroliftMembersPage"):
			return map[string]interface{}{"astroliftMembersPage": map[string]interface{}{"items": []map[string]interface{}{
				{"user": map[string]string{"id": "41", "username": "ana-old"}},
				{"user": map[string]string{"id": "42", "username": "ana"}},
			}}}
		case strings.Contains(req.Query, "astroliftProjects"):
			return map[string]interface{}{"astroliftProjects": []map[string]interface{}{{"id": "proj-guid", "slug": "checkout", "name": "Checkout"}}}
		case strings.Contains(req.Query, "astroliftTeams"):
			return map[string]interface{}{"astroliftTeams": []map[string]interface{}{{"id": "team-guid", "slug": "platform", "name": "Platform"}}}
		case strings.Contains(req.Query, "astroliftApp("):
			return map[string]interface{}{"astroliftApp": map[string]interface{}{"id": "app-guid", "slug": "web"}}
		}
		*writes = append(*writes, req)
		return answer(req)
	})
	t.Cleanup(srv.Close)
	return api.NewClient(srv.URL, "tok", false)
}

func grantOK(req gqlRequest) map[string]interface{} {
	return map[string]interface{}{"grantRole": map[string]interface{}{
		"ok": true, "errors": []interface{}{},
		"data": map[string]interface{}{
			"id": "bind-1", "groupExternalId": "", "scopeKind": "PROJECT", "scopeId": "7",
			"sourceScopeLabel": "Project checkout", "grantedAt": "2026-10-11T00:00:00Z",
			"user": map[string]string{"id": "42", "username": "ana"}, "role": map[string]string{"slug": "deployer", "name": "Our deployer"},
		},
	}}
}

func TestRoleGrantResolvesTheOrgRoleUsernameAndProjectScope(t *testing.T) {
	resetRoleFlags()
	t.Cleanup(resetRoleFlags)
	roleGrantUser, roleGrantProject = "ana", "checkout"
	var writes []gqlRequest
	client := roleServer(t, &writes, grantOK)
	cmd, out := appTestCmd()

	if err := runRoleGrant(cmd, context.Background(), client, testOrg, "deployer"); err != nil {
		t.Fatal(err)
	}
	input := writes[0].Variables["input"].(map[string]interface{})
	if input["roleId"] != "own-1" {
		t.Errorf("roleId = %v, want the org's custom role over the system role of the same slug", input["roleId"])
	}
	if input["userId"] != "42" || input["scopeKind"] != "PROJECT" || input["scopeGuid"] != "proj-guid" {
		t.Errorf("grant input: %#v", input)
	}
	if _, ok := input["groupExternalId"]; ok {
		t.Error("a user grant must not also name a group")
	}
	if !strings.Contains(out.String(), "Granted deployer to ana at Project checkout (binding bind-1)") {
		t.Errorf("output: %s", out.String())
	}
}

func TestRoleGrantDefaultsToTheOrgAndAcceptsAGroupAndTheGlobalAppFlag(t *testing.T) {
	resetRoleFlags()
	t.Cleanup(resetRoleFlags)
	roleGrantGroup = "okta-admins"
	var writes []gqlRequest
	client := roleServer(t, &writes, grantOK)

	cmd, _ := appTestCmd()
	if err := runRoleGrant(cmd, context.Background(), client, testOrg, "own-1"); err != nil {
		t.Fatal(err)
	}
	input := writes[0].Variables["input"].(map[string]interface{})
	if input["scopeKind"] != "ORG" || input["scopeGuid"] != "org-guid" || input["groupExternalId"] != "okta-admins" {
		t.Errorf("org grant input: %#v", input)
	}

	appCmd, _ := appTestCmd()
	if err := appCmd.Flags().Set("app", "web"); err != nil {
		t.Fatal(err)
	}
	if err := runRoleGrant(appCmd, context.Background(), client, testOrg, "deployer"); err != nil {
		t.Fatal(err)
	}
	input = writes[1].Variables["input"].(map[string]interface{})
	if input["scopeKind"] != "APP" || input["scopeGuid"] != "app-guid" {
		t.Errorf("app grant input: %#v", input)
	}
}

func TestRoleGrantRefusesAmbiguousOrMissingTargetsBeforeWriting(t *testing.T) {
	cases := []struct {
		name string
		set  func()
		want string
	}{
		{"neither principal", func() {}, "exactly one of --user or --group"},
		{"both principals", func() { roleGrantUser, roleGrantGroup = "ana", "g" }, "exactly one of --user or --group"},
		{"two scopes", func() { roleGrantUser, roleGrantTeam, roleGrantProject = "ana", "platform", "checkout" }, "at most one"},
		{"bad expiry", func() { roleGrantUser, roleGrantExpires = "ana", "tomorrow" }, "RFC 3339"},
		{"unknown member", func() { roleGrantUser = "nobody" }, `no member "nobody"`},
		{"unknown team", func() { roleGrantUser, roleGrantTeam = "ana", "ghosts" }, `no team "ghosts"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetRoleFlags()
			t.Cleanup(resetRoleFlags)
			tc.set()
			var writes []gqlRequest
			client := roleServer(t, &writes, grantOK)
			cmd, _ := appTestCmd()
			err := runRoleGrant(cmd, context.Background(), client, testOrg, "deployer")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
			if len(writes) != 0 {
				t.Fatalf("no write may happen, got %d", len(writes))
			}
		})
	}
}

func TestRoleGrantSurfacesTheServerRefusal(t *testing.T) {
	resetRoleFlags()
	t.Cleanup(resetRoleFlags)
	roleGrantUser = "ana"
	var writes []gqlRequest
	client := roleServer(t, &writes, func(gqlRequest) map[string]interface{} {
		return map[string]interface{}{"grantRole": map[string]interface{}{
			"ok": false, "data": nil, "errors": []map[string]string{{"code": "PERMISSION_DENIED", "message": "org.manage_members is required"}},
		}}
	})
	cmd, _ := appTestCmd()
	err := runRoleGrant(cmd, context.Background(), client, testOrg, "deployer")
	if err == nil || !strings.Contains(err.Error(), "org.manage_members is required") {
		t.Fatalf("expected the server message, got %v", err)
	}
}

func TestRoleCreateFromCopiesPermissionsAndUpdateRefusesSystemRoles(t *testing.T) {
	resetRoleFlags()
	t.Cleanup(resetRoleFlags)
	roleScopeLevel, roleFrom = "project", "deployer"
	var writes []gqlRequest
	client := roleServer(t, &writes, func(req gqlRequest) map[string]interface{} {
		return map[string]interface{}{"createRole": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"id": "new", "slug": "release", "name": "release", "scopeLevel": "PROJECT", "permissions": []string{"app.deploy", "app.read_logs"}},
		}}
	})
	cmd, _ := appTestCmd()
	if err := runRoleCreate(cmd, context.Background(), client, "release"); err != nil {
		t.Fatal(err)
	}
	input := writes[0].Variables["input"].(map[string]interface{})
	perms, _ := json.Marshal(input["permissions"])
	if input["scopeLevel"] != "PROJECT" || input["duplicatedFromId"] != "own-1" || string(perms) != `["app.deploy","app.read_logs"]` {
		t.Errorf("create input: %#v", input)
	}

	resetRoleFlags()
	err := runRoleUpdate(cmd, context.Background(), roleServerSystemOnly(t), "deployer")
	if err == nil || !strings.Contains(err.Error(), "system role") {
		t.Fatalf("expected a system-role refusal, got %v", err)
	}
}

func roleServerSystemOnly(t *testing.T) *api.Client {
	t.Helper()
	srv := gqlServerFunc(t, func(gqlRequest) map[string]interface{} {
		return map[string]interface{}{"astroliftRolesPage": map[string]interface{}{"items": rolesFixture()[:1]}}
	})
	t.Cleanup(srv.Close)
	return api.NewClient(srv.URL, "tok", false)
}

func TestRoleRevokeNeedsYesAndBindingsListHolders(t *testing.T) {
	resetRoleFlags()
	t.Cleanup(resetRoleFlags)
	var writes []gqlRequest
	client := roleServer(t, &writes, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "revokeRoleBinding") {
			return map[string]interface{}{"revokeRoleBinding": map[string]interface{}{"ok": true, "errors": []interface{}{}}}
		}
		return map[string]interface{}{"astroliftRoleBindingsPage": map[string]interface{}{"items": []map[string]interface{}{
			{"id": "b1", "groupExternalId": "okta-admins", "scopeKind": "ORG", "scopeId": "1", "sourceScopeLabel": "Organization", "grantedAt": "x", "user": nil, "role": map[string]string{"slug": "admin"}},
		}}}
	})
	cmd, out := appTestCmd()
	if err := runRoleRevoke(cmd, context.Background(), client, "b1"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("revoke must require --yes, got %v", err)
	}
	if len(writes) != 0 {
		t.Fatal("no write without --yes")
	}
	roleRevokeConfirmed = true
	if err := runRoleRevoke(cmd, context.Background(), client, "b1"); err != nil {
		t.Fatal(err)
	}
	roleBindingsHolder = "group:okta-admins"
	if err := runRoleBindings(cmd, context.Background(), client); err != nil {
		t.Fatal(err)
	}
	filter := writes[len(writes)-1].Variables["filter"].(map[string]interface{})
	if holders := filter["holder"].([]interface{}); holders[0] != "group:okta-admins" {
		t.Errorf("holder filter: %#v", filter)
	}
	if !strings.Contains(out.String(), "group:okta-admins") || !strings.Contains(out.String(), "Revoked binding b1.") {
		t.Errorf("output: %s", out.String())
	}
}
