package cmd

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/viper"
)

const legacyProjectGUID = "9e4b7c12-3d58-4a06-b1f9-5c2e8a7d6b30"

// placedApp is storefront's placement as astroliftApp and the move report it.
// A nil projectID is an app with no project.
func placedApp(teamSlug, projectSlug string, projectID interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id": testAppGUID, "slug": "storefront",
		"teamSlug": teamSlug, "projectSlug": projectSlug, "projectId": projectID,
	}
}

func movedTo(teamSlug, projectSlug, projectID string) map[string]interface{} {
	return map[string]interface{}{"ok": true, "errors": []interface{}{}, "data": placedApp(teamSlug, projectSlug, projectID)}
}

// appMoveServer answers the app read with app, the project lookup with
// twoTeamProjects, and assignAstroliftAppToProject with result.
func appMoveServer(t *testing.T, app, result map[string]interface{}, seen *[]gqlRequest) *api.Client {
	t.Helper()
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		*seen = append(*seen, req)
		switch {
		case strings.Contains(req.Query, "astroliftApp("):
			return map[string]interface{}{"astroliftApp": app}
		case strings.Contains(req.Query, "astroliftProjects"):
			return map[string]interface{}{"astroliftProjects": twoTeamProjects()}
		}
		return map[string]interface{}{"assignAstroliftAppToProject": result}
	})
	t.Cleanup(srv.Close)
	return api.NewClient(srv.URL, "cli-credential", false)
}

func TestAppMoveSendsTheSlugAndTheResolvedProject(t *testing.T) {
	var seen []gqlRequest
	client := appMoveServer(t, placedApp("core", "legacy", legacyProjectGUID), movedTo("core", "web", webProjectGUID), &seen)

	cmd, out := appTestCmd()
	if err := runAppMove(cmd, context.Background(), client, "storefront", "web"); err != nil {
		t.Fatalf("runAppMove: %v", err)
	}
	req, ok := sentMutation(seen, "assignAstroliftAppToProject")
	if !ok {
		t.Fatal("assignAstroliftAppToProject was not sent")
	}
	want := map[string]interface{}{"appSlug": "storefront", "projectGuid": webProjectGUID}
	if input, _ := req.Variables["input"].(map[string]interface{}); !reflect.DeepEqual(input, want) {
		t.Errorf("assignAstroliftAppToProject input = %v, want %v", input, want)
	}
	// Both the read and the move must select what the report prints.
	for _, req := range seen {
		if strings.Contains(req.Query, "astroliftProjects") {
			continue
		}
		if !strings.Contains(req.Query, "id slug teamSlug projectSlug projectId") {
			t.Errorf("does not select the app's placement:\n%s", req.Query)
		}
	}
	if got := out.String(); got != "Moved storefront from project legacy (team core) to project web (team core)\n" {
		t.Errorf("output = %q", got)
	}
}

func TestAppMoveToAProjectInAnotherTeamNamesBothTeams(t *testing.T) {
	var seen []gqlRequest
	client := appMoveServer(t, placedApp("core", "legacy", legacyProjectGUID), movedTo("data", "api", dataAPIProjectGUID), &seen)

	cmd, out := appTestCmd()
	if err := runAppMove(cmd, context.Background(), client, "storefront", dataAPIProjectGUID); err != nil {
		t.Fatalf("runAppMove: %v", err)
	}
	req, _ := sentMutation(seen, "assignAstroliftAppToProject")
	if input, _ := req.Variables["input"].(map[string]interface{}); input["projectGuid"] != dataAPIProjectGUID {
		t.Errorf("assignAstroliftAppToProject input = %v", input)
	}
	if got := out.String(); got != "Moved storefront from project legacy (team core) to project api (team data)\n" {
		t.Errorf("output = %q", got)
	}
}

func TestAppMoveFromNoProject(t *testing.T) {
	var seen []gqlRequest
	client := appMoveServer(t, placedApp("core", "", nil), movedTo("core", "web", webProjectGUID), &seen)

	cmd, out := appTestCmd()
	if err := runAppMove(cmd, context.Background(), client, "storefront", "web"); err != nil {
		t.Fatalf("runAppMove: %v", err)
	}
	if got := out.String(); got != "Moved storefront from no project (team core) to project web (team core)\n" {
		t.Errorf("output = %q", got)
	}
}

func TestAppMoveIntoItsOwnProjectSaysSo(t *testing.T) {
	var seen []gqlRequest
	client := appMoveServer(t, placedApp("core", "web", webProjectGUID), movedTo("core", "web", webProjectGUID), &seen)

	cmd, out := appTestCmd()
	if err := runAppMove(cmd, context.Background(), client, "storefront", "web"); err != nil {
		t.Fatalf("runAppMove: %v", err)
	}
	if got := out.String(); got != "storefront is already in project web (team core); nothing changed.\n" {
		t.Errorf("output = %q", got)
	}
}

func TestAppMovePrintsTheRefusalWithItsField(t *testing.T) {
	var seen []gqlRequest
	denied := "you do not have access to the target project"
	client := appMoveServer(t, placedApp("core", "legacy", legacyProjectGUID),
		refusedResult("PERMISSION_DENIED", "projectGuid", denied), &seen)

	cmd, out := appTestCmd()
	err := runAppMove(cmd, context.Background(), client, "storefront", "web")
	if err == nil || err.Error() != "app storefront was not moved: projectGuid: "+denied {
		t.Fatalf("err = %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("printed on a refusal:\n%s", out.String())
	}
}

func TestAppMoveUnknownAppSendsNoMove(t *testing.T) {
	var seen []gqlRequest
	client := appMoveServer(t, nil, movedTo("core", "web", webProjectGUID), &seen)

	cmd, _ := appTestCmd()
	err := runAppMove(cmd, context.Background(), client, "nope", "web")
	if err == nil || !strings.Contains(err.Error(), `app "nope" not found`) {
		t.Fatalf("err = %v", err)
	}
	if _, sent := sentMutation(seen, "assignAstroliftAppToProject"); sent {
		t.Fatal("assignAstroliftAppToProject was sent for an app that was not found")
	}
}

func TestAppMoveToASlugTwoTeamsUseIsRefused(t *testing.T) {
	var seen []gqlRequest
	client := appMoveServer(t, placedApp("core", "legacy", legacyProjectGUID), movedTo("core", "api", coreAPIProjectGUID), &seen)

	cmd, out := appTestCmd()
	err := runAppMove(cmd, context.Background(), client, "storefront", "api")
	if err == nil || !strings.Contains(err.Error(), `project slug "api" is used in teams core, data`) {
		t.Fatalf("err = %v", err)
	}
	if _, sent := sentMutation(seen, "assignAstroliftAppToProject"); sent {
		t.Fatal("assignAstroliftAppToProject was sent for a slug two teams use")
	}
	if out.Len() != 0 {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestAppMoveNeedsADestination(t *testing.T) {
	cmd, _ := appTestCmd()
	// A nil client panics if anything is sent.
	err := runAppMove(cmd, context.Background(), nil, "storefront", " ")
	if err == nil || !strings.Contains(err.Error(), "--project <slug-or-id>") {
		t.Fatalf("err = %v", err)
	}
}

func TestAppMoveJSON(t *testing.T) {
	var seen []gqlRequest
	client := appMoveServer(t, placedApp("core", "legacy", legacyProjectGUID), movedTo("data", "api", dataAPIProjectGUID), &seen)

	cmd, out := appTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runAppMove(cmd, context.Background(), client, "storefront", dataAPIProjectGUID); err != nil {
		t.Fatalf("runAppMove: %v", err)
	}
	var got appPlacement
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if got.Slug != "storefront" || got.TeamSlug != "data" || got.ProjectSlug != "api" || got.ProjectID != dataAPIProjectGUID {
		t.Errorf("JSON = %+v", got)
	}
}

func TestAppMoveNeedsTheSlugAndTheProject(t *testing.T) {
	if err := appMoveCmd.Args(appMoveCmd, []string{"storefront"}); err != nil {
		t.Fatal(err)
	}
	if err := appMoveCmd.Args(appMoveCmd, nil); err == nil {
		t.Error("app move accepted no app slug")
	}
	flag := appMoveCmd.Flags().Lookup("project")
	if flag == nil || flag.Annotations["cobra_annotation_bash_completion_one_required_flag"] == nil {
		t.Error("--project is not a required flag")
	}
}

// app move declares its own --project over the global one. A whole run proves
// the run func receives it and the calls carry the working organization.
func TestAppMoveTakesTheDestinationFromItsProjectFlag(t *testing.T) {
	var seen []gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		seen = append(seen, req)
		switch {
		case strings.Contains(req.Query, "astroliftOrganizations"):
			return map[string]interface{}{"astroliftOrganizations": []map[string]interface{}{
				{"id": projectTestOrgGUID, "slug": "acme", "name": "Acme"},
			}}
		case strings.Contains(req.Query, "astroliftApp("):
			return map[string]interface{}{"astroliftApp": placedApp("core", "legacy", legacyProjectGUID)}
		case strings.Contains(req.Query, "astroliftProjects"):
			return map[string]interface{}{"astroliftProjects": twoTeamProjects()}
		}
		return map[string]interface{}{"assignAstroliftAppToProject": movedTo("core", "web", webProjectGUID)}
	})
	t.Cleanup(srv.Close)
	withConfigHome(t, srv.URL)
	viper.Set("token", "cli-credential")
	t.Cleanup(func() { viper.Set("token", ""); appMoveProject = "" })

	out, err := runCmd(t, "app", "move", "storefront", "--project", "web")
	if err != nil {
		t.Fatalf("app move: %v (%s)", err, out)
	}
	req, ok := sentMutation(seen, "assignAstroliftAppToProject")
	if !ok {
		t.Fatal("assignAstroliftAppToProject was not sent")
	}
	if input, _ := req.Variables["input"].(map[string]interface{}); input["projectGuid"] != webProjectGUID {
		t.Errorf("assignAstroliftAppToProject input = %v", input)
	}
	if req.Organization != projectTestOrgGUID {
		t.Errorf("assignAstroliftAppToProject organization = %q", req.Organization)
	}
	if !strings.Contains(out, "Moved storefront from project legacy (team core) to project web (team core)") {
		t.Errorf("output:\n%s", out)
	}
}
