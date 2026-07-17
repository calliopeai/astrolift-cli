package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// groupsTestCmd builds a command carrying the persistent + local flags the
// org/team/project/scm/alert/status run-funcs inspect, with stdout captured.
func groupsTestCmd() (*cobra.Command, *bytes.Buffer) {
	root := &cobra.Command{}
	root.PersistentFlags().Bool("no-prompt", false, "")
	c := &cobra.Command{}
	c.Flags().String("org", "", "")
	c.Flags().String("team", "", "")
	c.Flags().Bool("no-prompt", false, "")
	c.Flags().Bool("debug", false, "")
	c.Flags().Bool("json", false, "")
	root.AddCommand(c)
	out := &bytes.Buffer{}
	c.SetOut(out)
	return c, out
}

// ---- team list -------------------------------------------------------------

func TestRunTeamListTable(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftTeams": []map[string]interface{}{
			{"id": "t-1", "slug": "core", "name": "Core Team"},
			{"id": "t-2", "slug": "data", "name": "Data Team"},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := groupsTestCmd()
	if err := runTeamList(cmd, context.Background(), client); err != nil {
		t.Fatalf("runTeamList: %v", err)
	}
	for _, want := range []string{"core", "Core Team", "t-1", "data", "Data Team"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("team list missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunTeamListEmpty(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{"astroliftTeams": []map[string]interface{}{}}, nil)
	defer srv.Close()
	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := groupsTestCmd()
	if err := runTeamList(cmd, context.Background(), client); err != nil {
		t.Fatalf("runTeamList: %v", err)
	}
	if !strings.Contains(out.String(), "No teams found.") {
		t.Errorf("expected empty message, got:\n%s", out.String())
	}
}

// ---- team create -----------------------------------------------------------

func TestRunTeamCreateSendsMutation(t *testing.T) {
	teamCreateName = "Platform"
	teamCreateDesc = "the platform team"
	defer func() { teamCreateName = ""; teamCreateDesc = "" }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": []map[string]interface{}{{"id": "org-1", "slug": "acme", "name": "Acme"}},
		"createTeam": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"id": "t-9", "slug": "platform", "name": "Platform"},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := groupsTestCmd()
	if err := runTeamCreate(cmd, context.Background(), client, &config.Config{DefaultOrg: "acme"}, "platform"); err != nil {
		t.Fatalf("runTeamCreate: %v", err)
	}
	input, _ := captured.Variables["input"].(map[string]interface{})
	if input == nil || input["organizationId"] != "org-1" || input["slug"] != "platform" ||
		input["name"] != "Platform" || input["description"] != "the platform team" {
		t.Errorf("create team input wrong: %+v", captured.Variables["input"])
	}
	if !strings.Contains(out.String(), "Team created: platform (Platform) in org acme") {
		t.Errorf("create team output wrong:\n%s", out.String())
	}
}

func TestRunTeamCreateSurfacesError(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": []map[string]interface{}{{"id": "org-1", "slug": "acme", "name": "Acme"}},
		"createTeam": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{"code": "conflict", "message": "slug already taken", "field": "slug"},
			},
			"data": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := groupsTestCmd()
	err := runTeamCreate(cmd, context.Background(), client, &config.Config{DefaultOrg: "acme"}, "dup")
	if err == nil || !strings.Contains(err.Error(), "slug already taken") {
		t.Fatalf("expected mutation error surfaced, got %v", err)
	}
}

// ---- project list ----------------------------------------------------------

func TestRunProjectListTable(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftProjects": []map[string]interface{}{
			{"id": "p-1", "slug": "web", "name": "Web", "team": map[string]interface{}{"slug": "core", "name": "Core"}},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := groupsTestCmd()
	if err := runProjectList(cmd, context.Background(), client); err != nil {
		t.Fatalf("runProjectList: %v", err)
	}
	for _, want := range []string{"web", "Web", "core", "p-1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("project list missing %q:\n%s", want, out.String())
		}
	}
}

// ---- project create --------------------------------------------------------

func TestRunProjectCreateResolvesTeam(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftTeams": []map[string]interface{}{
			{"id": "t-1", "slug": "core", "name": "Core"},
			{"id": "t-2", "slug": "data", "name": "Data"},
		},
		"createProject": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"id": "p-9", "slug": "api", "name": "api",
				"team": map[string]interface{}{"slug": "data", "name": "Data"},
			},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := groupsTestCmd()
	_ = cmd.Flags().Set("team", "data")
	if err := runProjectCreate(cmd, context.Background(), client, "api"); err != nil {
		t.Fatalf("runProjectCreate: %v", err)
	}
	input, _ := captured.Variables["input"].(map[string]interface{})
	if input == nil || input["teamId"] != "t-2" || input["slug"] != "api" || input["name"] != "api" {
		t.Errorf("create project input wrong: %+v", captured.Variables["input"])
	}
	if !strings.Contains(out.String(), "Project created: api (api) in team data") {
		t.Errorf("create project output wrong:\n%s", out.String())
	}
}

func TestRunProjectCreateRequiresTeam(t *testing.T) {
	client := api.NewClient("http://unused", "tok", false)
	cmd, _ := groupsTestCmd()
	err := runProjectCreate(cmd, context.Background(), client, "api")
	if err == nil || !strings.Contains(err.Error(), "--team <slug> is required") {
		t.Fatalf("expected --team required error, got %v", err)
	}
}

func TestRunProjectCreateUnknownTeam(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftTeams": []map[string]interface{}{{"id": "t-1", "slug": "core", "name": "Core"}},
	}, nil)
	defer srv.Close()
	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := groupsTestCmd()
	_ = cmd.Flags().Set("team", "ghost")
	err := runProjectCreate(cmd, context.Background(), client, "api")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected team-not-found error, got %v", err)
	}
}

// ---- scm list --------------------------------------------------------------

func TestRunScmList(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftSourceConnections": []map[string]interface{}{
			{"id": "c-1", "kind": "github", "name": "gh", "displayName": "Acme GitHub",
				"accountLogin": "acme", "isActive": true, "isPersonal": false},
			{"id": "c-2", "kind": "gitlab", "name": "gl", "displayName": "",
				"accountLogin": "me", "isActive": false, "isPersonal": true},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := groupsTestCmd()
	if err := runScmList(cmd, context.Background(), client); err != nil {
		t.Fatalf("runScmList: %v", err)
	}
	for _, want := range []string{"github", "Acme GitHub", "org", "yes", "gitlab", "personal", "no", "gl"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("scm list missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunScmDisconnect(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"disconnectSource": map[string]interface{}{
			"ok": true, "errors": []interface{}{}, "data": map[string]interface{}{"id": "c-1"},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := groupsTestCmd()
	if err := runScmDisconnect(cmd, context.Background(), client, "c-1"); err != nil {
		t.Fatalf("runScmDisconnect: %v", err)
	}
	if !strings.Contains(out.String(), "Disconnected SCM connection c-1") {
		t.Errorf("disconnect missing confirmation:\n%s", out.String())
	}
}

func TestRunScmDisconnectSurfacesError(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"disconnectSource": map[string]interface{}{
			"ok":     false,
			"errors": []interface{}{map[string]interface{}{"code": "NOT_FOUND", "message": "connection not found"}},
			"data":   nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := groupsTestCmd()
	err := runScmDisconnect(cmd, context.Background(), client, "missing")
	if err == nil || !strings.Contains(err.Error(), "connection not found") {
		t.Fatalf("expected disconnect error surfaced, got %v", err)
	}
}

// ---- alert list ------------------------------------------------------------

func TestRunAlertList(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftAlertRules": []map[string]interface{}{
			{"id": "a-1", "name": "High CPU", "target": "app", "targetId": "web",
				"severity": "warning", "isActive": true, "activeMute": nil},
			{"id": "a-2", "name": "Down", "target": "app", "targetId": "api",
				"severity": "critical", "isActive": true,
				"activeMute": map[string]interface{}{"ttlUntil": "2026-07-01T00:00:00Z"}},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := groupsTestCmd()
	if err := runAlertList(cmd, context.Background(), client); err != nil {
		t.Fatalf("runAlertList: %v", err)
	}
	if captured.Variables["activeOnly"] != true {
		t.Errorf("expected activeOnly=true by default, got %v", captured.Variables["activeOnly"])
	}
	for _, want := range []string{"High CPU", "warning", "app:web", "active", "Down", "critical", "muted"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("alert list missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunAlertListAllFlag(t *testing.T) {
	alertListAll = true
	defer func() { alertListAll = false }()
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"astroliftAlertRules": []map[string]interface{}{}}, &captured)
	defer srv.Close()
	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := groupsTestCmd()
	if err := runAlertList(cmd, context.Background(), client); err != nil {
		t.Fatalf("runAlertList: %v", err)
	}
	if captured.Variables["activeOnly"] != false {
		t.Errorf("--all must set activeOnly=false, got %v", captured.Variables["activeOnly"])
	}
}

// ---- status ----------------------------------------------------------------

func TestRunStatus(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftServerInfo": map[string]interface{}{
			"version": "1.4.0", "apiVersion": "v1", "installId": "i-1", "installSlug": "acme-prod",
			"installLabel": "Acme Prod", "region": "us-west-2",
			"serverTime":   "2026-06-30T12:00:00Z",
			"capabilities": []string{"agents", "pipelines"},
			"authMethods":  []string{"oidc", "token"},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := groupsTestCmd()
	if err := runStatus(cmd, context.Background(), client, "https://api.acme.example"); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	for _, want := range []string{
		"https://api.acme.example", "Acme Prod (acme-prod)", "us-west-2",
		"1.4.0 (api v1)", "2026-06-30T12:00:00Z", "oidc, token", "agents, pipelines",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunStatusJSON(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftServerInfo": map[string]interface{}{
			"version": "1.4.0", "apiVersion": "v1", "installId": "i-1", "installSlug": "acme-prod",
			"installLabel": nil, "region": nil, "serverTime": "2026-06-30T12:00:00Z",
			"capabilities": []string{}, "authMethods": []string{"token"},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := groupsTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runStatus(cmd, context.Background(), client, ""); err != nil {
		t.Fatalf("runStatus --json: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if got["installSlug"] != "acme-prod" || got["version"] != "1.4.0" {
		t.Errorf("decoded status wrong: %+v", got)
	}
}
