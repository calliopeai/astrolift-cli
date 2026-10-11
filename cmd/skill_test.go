package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

func resetSkillFlags() {
	skillListGlobal = false
	skillImportBranch = "main"
	skillImportManifest = ""
	skillRepoRef = "main"
	skillRepoDisplayName = ""
	skillRepoSourceKind = "github"
	skillRepoConnection = ""
	skillRepoRepo = ""
	skillRepoActive = ""
	skillRepoDetach = false
	skillRepoYes = false
}

var skillOrgs = map[string]interface{}{
	"astroliftOrganizations": []map[string]string{{"id": "org-guid", "slug": "acme", "name": "Acme"}},
}

func skillCatalog() []map[string]interface{} {
	return []map[string]interface{}{
		{"id": "g-1", "slug": "review", "name": "Global review", "skillVersion": 1, "isGlobal": true, "isActive": true, "sourceKind": "builtin"},
		{"id": "o-1", "slug": "review", "name": "Our review", "skillVersion": 3, "isGlobal": false, "isActive": true, "sourceKind": "repo_import", "sourceRef": "acme/skills@main", "isImported": true},
		{"id": "o-2", "slug": "deploy", "name": "Deploy", "skillVersion": 1, "isGlobal": false, "isActive": false, "sourceKind": "manual"},
	}
}

func TestSkillListShowsOriginAndScopesToTheWorkingOrg(t *testing.T) {
	resetSkillFlags()
	t.Cleanup(resetSkillFlags)
	var requests []gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		requests = append(requests, req)
		if strings.Contains(req.Query, "astroliftOrganizations") {
			return skillOrgs
		}
		return map[string]interface{}{"skills": skillCatalog()}
	})
	defer srv.Close()
	cmd, out := appTestCmd()
	client := api.NewClient(srv.URL, "token", false)
	if err := runSkillList(cmd, context.Background(), client, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	last := requests[len(requests)-1]
	if last.Variables["orgId"] != "org-guid" || last.Variables["isGlobal"] != false || last.Organization != "org-guid" {
		t.Fatalf("catalog read not scoped to the working org: %#v org=%q", last.Variables, last.Organization)
	}
	for _, want := range []string{"global", "repo_import acme/skills@main", "manual", "v3"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestSkillInspectPrefersTheOrgSkillAndListsItsTools(t *testing.T) {
	resetSkillFlags()
	t.Cleanup(resetSkillFlags)
	var inspected string
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		switch {
		case strings.Contains(req.Query, "astroliftOrganizations"):
			return skillOrgs
		case strings.Contains(req.Query, "toolDefs"):
			inspected, _ = req.Variables["id"].(string)
			skill := skillCatalog()[1]
			skill["content"] = "Review pull requests."
			return map[string]interface{}{
				"skill":    skill,
				"toolDefs": []map[string]interface{}{{"slug": "lint", "adapter": "http_endpoint", "handlerRef": "https://ci.example/lint"}},
			}
		default:
			return map[string]interface{}{"skills": skillCatalog()}
		}
	})
	defer srv.Close()
	cmd, out := appTestCmd()
	if err := runSkillInspect(cmd, context.Background(), api.NewClient(srv.URL, "token", false), &config.Config{}, "review"); err != nil {
		t.Fatal(err)
	}
	if inspected != "o-1" {
		t.Fatalf("inspect read %q, want the org skill o-1 over the global one", inspected)
	}
	for _, want := range []string{"Our review (review) v3", "lint  http_endpoint https://ci.example/lint", "Review pull requests."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("inspect output lacks %q:\n%s", want, out.String())
		}
	}
	if err := runSkillInspect(cmd, context.Background(), api.NewClient(srv.URL, "token", false), &config.Config{}, "missing"); err == nil {
		t.Fatal("an unknown slug must be refused")
	}
}

func TestSkillImportSendsTheRepoAndSurfacesServerRefusals(t *testing.T) {
	resetSkillFlags()
	t.Cleanup(resetSkillFlags)
	skillImportBranch = "release"
	skillImportManifest = "skills/astrolift.toml"
	refuse := false
	var sent gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "astroliftOrganizations") {
			return skillOrgs
		}
		sent = req
		if refuse {
			return map[string]interface{}{"importSkillsFromRepo": map[string]interface{}{
				"ok": false, "errors": []map[string]string{{"code": "VALIDATION", "message": "tools.measure: unknown adapter 'http_endpont'"}},
			}}
		}
		return map[string]interface{}{"importSkillsFromRepo": map[string]interface{}{
			"ok": true, "data": map[string]interface{}{"importedSkills": []string{"audit"}, "importedTools": []string{"measure"}, "sourceRef": "acme/skills@release"},
		}}
	})
	defer srv.Close()
	cmd, out := appTestCmd()
	client := api.NewClient(srv.URL, "token", false)
	if err := runSkillImport(cmd, context.Background(), client, &config.Config{}, "https://github.com/acme/skills"); err != nil {
		t.Fatal(err)
	}
	if sent.Variables["repoUrl"] != "https://github.com/acme/skills" || sent.Variables["branch"] != "release" || sent.Variables["manifestPath"] != "skills/astrolift.toml" {
		t.Fatalf("import variables: %#v", sent.Variables)
	}
	if !strings.Contains(out.String(), "Skills: audit") || !strings.Contains(out.String(), "Tools:  measure") {
		t.Fatalf("import output: %s", out.String())
	}
	refuse = true
	err := runSkillImport(cmd, context.Background(), client, &config.Config{}, "https://github.com/acme/skills")
	if err == nil || !strings.Contains(err.Error(), "unknown adapter") {
		t.Fatalf("expected the server's validation message, got %v", err)
	}
}

func TestSkillRepoRegisterUpdateAndRemoveResolveTheAlias(t *testing.T) {
	resetSkillFlags()
	t.Cleanup(resetSkillFlags)
	repo := map[string]interface{}{"id": "repo-guid", "alias": "team", "repoFullName": "acme/skills", "defaultRef": "main", "isActive": true}
	var writes []gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		switch {
		case strings.Contains(req.Query, "astroliftOrganizations"):
			return skillOrgs
		case strings.Contains(req.Query, "orgSkillRepos"):
			return map[string]interface{}{"orgSkillRepos": []interface{}{repo}}
		}
		writes = append(writes, req)
		for _, field := range []string{"registerOrgSkillRepo", "updateOrgSkillRepo", "removeOrgSkillRepo"} {
			if strings.Contains(req.Query, field) {
				return map[string]interface{}{field: map[string]interface{}{"ok": true, "data": repo}}
			}
		}
		return nil
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	cmd, _ := appTestCmd()
	skillRepoConnection = "conn-guid"
	if err := runSkillRepoRegister(cmd, context.Background(), client, &config.Config{}, "team", "acme/skills"); err != nil {
		t.Fatal(err)
	}
	register := writes[0]
	input := register.Variables["input"].(map[string]interface{})
	if register.Variables["orgId"] != "org-guid" || input["alias"] != "team" || input["sourceConnectionId"] != "conn-guid" {
		t.Fatalf("register request: %#v", register.Variables)
	}

	resetSkillFlags()
	updateCmd, _ := appTestCmd()
	updateCmd.Flags().String("ref", "", "")
	if err := updateCmd.Flags().Set("ref", "v2"); err != nil {
		t.Fatal(err)
	}
	skillRepoRef = "v2"
	skillRepoActive = "false"
	if err := runSkillRepoUpdate(updateCmd, context.Background(), client, &config.Config{}, "team"); err != nil {
		t.Fatal(err)
	}
	update := writes[1].Variables["input"].(map[string]interface{})
	if update["id"] != "repo-guid" || update["defaultRef"] != "v2" || update["isActive"] != false {
		t.Fatalf("update input: %#v", update)
	}

	resetSkillFlags()
	if err := runSkillRepoRemove(cmd, context.Background(), client, &config.Config{}, "team"); err == nil {
		t.Fatal("remove must require --yes")
	}
	skillRepoYes = true
	removeCmd, out := appTestCmd()
	removeCmd.Flags().Set("json", "true") //nolint:errcheck
	if err := runSkillRepoRemove(removeCmd, context.Background(), client, &config.Config{}, "team"); err != nil {
		t.Fatal(err)
	}
	remove := writes[len(writes)-1].Variables["input"].(map[string]interface{})
	if remove["id"] != "repo-guid" || !json.Valid(out.Bytes()) {
		t.Fatalf("remove input %#v, output %s", remove, out.String())
	}
}

func TestSkillRepoUpdateRefusesConflictingOrEmptyChanges(t *testing.T) {
	resetSkillFlags()
	t.Cleanup(resetSkillFlags)
	cmd, _ := appTestCmd()
	client := api.NewClient("http://127.0.0.1:1", "token", false)
	if err := runSkillRepoUpdate(cmd, context.Background(), client, &config.Config{}, "team"); err == nil || !strings.Contains(err.Error(), "nothing to change") {
		t.Fatalf("expected nothing-to-change refusal, got %v", err)
	}
	skillRepoConnection, skillRepoDetach = "conn", true
	if err := runSkillRepoUpdate(cmd, context.Background(), client, &config.Config{}, "team"); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected conflicting connection flags refusal, got %v", err)
	}
}
