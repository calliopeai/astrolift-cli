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

// Three projects in two teams. A project slug is unique per team only, so
// "api" names one project in core and another in data (platform #2479).
const (
	projectTestOrgGUID = "0d9c4e27-6a1b-4f83-9c52-7e1a3b6d8f40"
	webProjectGUID     = "5b0e3c9a-2f41-4d7e-8c16-0a9b7d3e4f21"
	coreAPIProjectGUID = "8d2f6a1c-7b30-4e95-a4c8-1f6e2d9b0c57"
	dataAPIProjectGUID = "c47a9e05-1d6b-4f38-9e2a-6b5c8d0f3a19"
)

func twoTeamProjects() []map[string]interface{} {
	core := map[string]interface{}{"slug": "core", "name": "Core"}
	data := map[string]interface{}{"slug": "data", "name": "Data"}
	return []map[string]interface{}{
		{"id": webProjectGUID, "slug": "web", "name": "Web", "team": core},
		{"id": coreAPIProjectGUID, "slug": "api", "name": "API", "team": core},
		{"id": dataAPIProjectGUID, "slug": "api", "name": "Data API", "team": data},
	}
}

// projectsAnswer answers the project lookup with twoTeamProjects. Like the
// platform, it sends each project's team only when the query selects it.
func projectsAnswer(req gqlRequest) map[string]interface{} {
	projects := twoTeamProjects()
	if !strings.Contains(req.Query, "team { slug name }") {
		for _, p := range projects {
			delete(p, "team")
		}
	}
	return map[string]interface{}{"astroliftProjects": projects}
}

// projectMutationServer answers the project lookup with twoTeamProjects and
// the named mutation with result, recording every request.
func projectMutationServer(t *testing.T, mutation string, result map[string]interface{}, seen *[]gqlRequest) *api.Client {
	t.Helper()
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		*seen = append(*seen, req)
		if strings.Contains(req.Query, "astroliftProjects") {
			return projectsAnswer(req)
		}
		return map[string]interface{}{mutation: result}
	})
	t.Cleanup(srv.Close)
	// Not "tok": the client redacts its credential from responses.
	return api.NewClient(srv.URL, "cli-credential", false)
}

// projectCommandServer stands behind a whole `astro` run: one organization,
// twoTeamProjects, and result for the named mutation, every request recorded.
func projectCommandServer(t *testing.T, mutation string, result map[string]interface{}, seen *[]gqlRequest) {
	t.Helper()
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		*seen = append(*seen, req)
		switch {
		case strings.Contains(req.Query, "astroliftOrganizations"):
			return map[string]interface{}{"astroliftOrganizations": []map[string]interface{}{
				{"id": projectTestOrgGUID, "slug": "acme", "name": "Acme"},
			}}
		case strings.Contains(req.Query, "astroliftProjects"):
			return projectsAnswer(req)
		}
		return map[string]interface{}{mutation: result}
	})
	t.Cleanup(srv.Close)
	withConfigHome(t, srv.URL)
	viper.Set("token", "cli-credential")
	t.Cleanup(func() { viper.Set("token", "") })
}

// sentMutation returns the request that carried the named mutation.
func sentMutation(seen []gqlRequest, mutation string) (gqlRequest, bool) {
	for _, req := range seen {
		if strings.Contains(req.Query, mutation+"(") {
			return req, true
		}
	}
	return gqlRequest{}, false
}

// refusedResult is a mutation envelope refused with one error. The platform
// sends a null field when the error names none.
func refusedResult(code, field, message string) map[string]interface{} {
	var named interface{}
	if field != "" {
		named = field
	}
	return map[string]interface{}{
		"ok": false, "data": nil,
		"errors": []map[string]interface{}{{"code": code, "field": named, "message": message}},
	}
}

func renamedProject(slug, name string) map[string]interface{} {
	return map[string]interface{}{
		"ok": true, "errors": []interface{}{},
		"data": map[string]interface{}{
			"id": webProjectGUID, "slug": slug, "name": name,
			"team": map[string]interface{}{"slug": "core", "name": "Core"},
		},
	}
}

func deletedProject(id string) map[string]interface{} {
	return map[string]interface{}{
		"ok": true, "errors": []interface{}{},
		"data": map[string]interface{}{"id": id, "deleted": true},
	}
}

// ---- project rename ----------------------------------------------------------

func TestProjectRenameSendsTheResolvedIDWithSlugAndName(t *testing.T) {
	var seen []gqlRequest
	client := projectMutationServer(t, "updateProject", renamedProject("storefront", "Storefront"), &seen)

	cmd, out := groupsTestCmd()
	if err := runProjectRename(cmd, context.Background(), client, "web", " storefront ", "Storefront"); err != nil {
		t.Fatalf("runProjectRename: %v", err)
	}
	req, ok := sentMutation(seen, "updateProject")
	if !ok {
		t.Fatal("updateProject was not sent")
	}
	input, _ := req.Variables["input"].(map[string]interface{})
	want := map[string]interface{}{"id": webProjectGUID, "slug": "storefront", "name": "Storefront"}
	if !reflect.DeepEqual(input, want) {
		t.Errorf("updateProject input = %v, want %v", input, want)
	}
	if !strings.Contains(req.Query, "data { id slug name team { slug name } }") {
		t.Errorf("updateProject does not select the renamed project and its team:\n%s", req.Query)
	}
	if got := out.String(); got != "Project renamed: web (Web) -> storefront (Storefront) in team core\n" {
		t.Errorf("output = %q", got)
	}
}

func TestProjectRenameSendsOnlyTheFieldsGiven(t *testing.T) {
	for _, tc := range []struct {
		name, slug, display string
		want                map[string]interface{}
		after               map[string]interface{}
		output              string
	}{
		{
			"slug only", "storefront", "",
			map[string]interface{}{"id": webProjectGUID, "slug": "storefront"},
			renamedProject("storefront", "Web"),
			"Project renamed: web (Web) -> storefront (Web) in team core\n",
		},
		{
			"name only", "", "Storefront",
			map[string]interface{}{"id": webProjectGUID, "name": "Storefront"},
			renamedProject("web", "Storefront"),
			"Project renamed: web (Web) -> web (Storefront) in team core\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen []gqlRequest
			client := projectMutationServer(t, "updateProject", tc.after, &seen)
			cmd, out := groupsTestCmd()
			if err := runProjectRename(cmd, context.Background(), client, "web", tc.slug, tc.display); err != nil {
				t.Fatalf("runProjectRename: %v", err)
			}
			req, _ := sentMutation(seen, "updateProject")
			if input, _ := req.Variables["input"].(map[string]interface{}); !reflect.DeepEqual(input, tc.want) {
				t.Errorf("updateProject input = %v, want %v", input, tc.want)
			}
			if got := out.String(); got != tc.output {
				t.Errorf("output = %q, want %q", got, tc.output)
			}
		})
	}
}

func TestProjectRenameRunsInTheWorkingOrganization(t *testing.T) {
	var seen []gqlRequest
	projectCommandServer(t, "updateProject", renamedProject("storefront", "Storefront"), &seen)
	t.Cleanup(func() { projectRenameSlug, projectRenameName = "", "" })

	out, err := runCmd(t, "project", "rename", "web", "--slug", "storefront", "--name", "Storefront")
	if err != nil {
		t.Fatalf("project rename: %v (%s)", err, out)
	}
	req, ok := sentMutation(seen, "updateProject")
	if !ok || req.Organization != projectTestOrgGUID {
		t.Fatalf("updateProject sent=%v organization=%q", ok, req.Organization)
	}
	want := map[string]interface{}{"id": webProjectGUID, "slug": "storefront", "name": "Storefront"}
	if input, _ := req.Variables["input"].(map[string]interface{}); !reflect.DeepEqual(input, want) {
		t.Errorf("updateProject input = %v, want %v", input, want)
	}
}

func TestProjectRenameNeedsANewSlugOrName(t *testing.T) {
	cmd, _ := groupsTestCmd()
	// A nil client panics if anything is sent.
	err := runProjectRename(cmd, context.Background(), nil, "web", " ", "")
	if err == nil || !strings.Contains(err.Error(), "--slug") || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("err = %v", err)
	}
}

func TestProjectRenamePrintsTheRefusalWithItsField(t *testing.T) {
	var seen []gqlRequest
	taken := "a project with slug 'api' already exists in this team"
	client := projectMutationServer(t, "updateProject", refusedResult("CONFLICT", "slug", taken), &seen)

	cmd, out := groupsTestCmd()
	err := runProjectRename(cmd, context.Background(), client, "web", "api", "")
	if err == nil || err.Error() != "project web was not renamed: slug: "+taken {
		t.Fatalf("err = %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("printed on a refusal:\n%s", out.String())
	}
}

func TestProjectRenameUnchangedSaysSo(t *testing.T) {
	var seen []gqlRequest
	client := projectMutationServer(t, "updateProject", renamedProject("web", "Web"), &seen)

	cmd, out := groupsTestCmd()
	if err := runProjectRename(cmd, context.Background(), client, "web", "web", "Web"); err != nil {
		t.Fatalf("runProjectRename: %v", err)
	}
	if got := out.String(); got != "Project web (Web) already had that slug and name; nothing changed.\n" {
		t.Errorf("output = %q", got)
	}
}

func TestProjectRenameJSON(t *testing.T) {
	var seen []gqlRequest
	client := projectMutationServer(t, "updateProject", renamedProject("storefront", "Storefront"), &seen)

	cmd, out := groupsTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runProjectRename(cmd, context.Background(), client, "web", "storefront", ""); err != nil {
		t.Fatalf("runProjectRename: %v", err)
	}
	var got projectRef
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if got.ID != webProjectGUID || got.Slug != "storefront" || got.Name != "Storefront" || got.Team.Slug != "core" {
		t.Errorf("JSON = %+v", got)
	}
}

// ---- project resolution --------------------------------------------------------

func TestProjectSlugTwoTeamsUseIsRefused(t *testing.T) {
	var seen []gqlRequest
	client := projectMutationServer(t, "softDeleteProject", deletedProject(coreAPIProjectGUID), &seen)

	cmd, out := groupsTestCmd()
	err := runProjectDelete(cmd, context.Background(), client, "api")
	if err == nil || !strings.Contains(err.Error(), `project slug "api" is used in teams core, data; give the project's id`) {
		t.Fatalf("err = %v", err)
	}
	if _, sent := sentMutation(seen, "softDeleteProject"); sent {
		t.Fatal("softDeleteProject was sent for a slug two teams use")
	}
	if out.Len() != 0 {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestProjectIDPicksOneOfTheProjectsThatShareASlug(t *testing.T) {
	var seen []gqlRequest
	client := projectMutationServer(t, "softDeleteProject", deletedProject(dataAPIProjectGUID), &seen)

	cmd, out := groupsTestCmd()
	if err := runProjectDelete(cmd, context.Background(), client, " "+dataAPIProjectGUID+" "); err != nil {
		t.Fatalf("runProjectDelete: %v", err)
	}
	req, _ := sentMutation(seen, "softDeleteProject")
	if input, _ := req.Variables["input"].(map[string]interface{}); input["id"] != dataAPIProjectGUID {
		t.Errorf("softDeleteProject input = %v", input)
	}
	if got := out.String(); got != "Deleted project api (Data API) in team data\n" {
		t.Errorf("output = %q", got)
	}
}

func TestProjectNotInTheOrganization(t *testing.T) {
	var seen []gqlRequest
	client := projectMutationServer(t, "updateProject", renamedProject("storefront", "Storefront"), &seen)

	cmd, _ := groupsTestCmd()
	err := runProjectRename(cmd, context.Background(), client, "ghost", "storefront", "")
	if err == nil || !strings.Contains(err.Error(), `project "ghost" not found in the working organization`) {
		t.Fatalf("err = %v", err)
	}
	if _, sent := sentMutation(seen, "updateProject"); sent {
		t.Fatal("updateProject was sent for a project that was not found")
	}
}

// ---- project delete ------------------------------------------------------------

func TestProjectDeleteSendsTheResolvedID(t *testing.T) {
	var seen []gqlRequest
	client := projectMutationServer(t, "softDeleteProject", deletedProject(webProjectGUID), &seen)

	cmd, out := groupsTestCmd()
	if err := runProjectDelete(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runProjectDelete: %v", err)
	}
	req, ok := sentMutation(seen, "softDeleteProject")
	if !ok {
		t.Fatal("softDeleteProject was not sent")
	}
	want := map[string]interface{}{"id": webProjectGUID}
	if input, _ := req.Variables["input"].(map[string]interface{}); !reflect.DeepEqual(input, want) {
		t.Errorf("softDeleteProject input = %v, want %v", input, want)
	}
	if got := out.String(); got != "Deleted project web (Web) in team core\n" {
		t.Errorf("output = %q", got)
	}
}

// The platform refuses while the project still holds apps, managed resources
// or shared secret bundles. Its message carries the counts and the steps, and
// is printed word for word.
func TestProjectDeletePrintsThePlatformRefusal(t *testing.T) {
	var seen []gqlRequest
	reason := "project still has 2 apps, 1 managed resource and 1 secret bundle; move its apps to another project " +
		"or deregister them, deprovision its managed resources and delete its secret bundles before deleting it"
	client := projectMutationServer(t, "softDeleteProject", refusedResult("PRECONDITION", "", reason), &seen)

	cmd, out := groupsTestCmd()
	err := runProjectDelete(cmd, context.Background(), client, "web")
	if err == nil || err.Error() != "project web was not deleted: "+reason {
		t.Fatalf("err = %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("printed on a refusal:\n%s", out.String())
	}
}

func TestProjectDeleteJSON(t *testing.T) {
	var seen []gqlRequest
	client := projectMutationServer(t, "softDeleteProject", deletedProject(webProjectGUID), &seen)

	cmd, out := groupsTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runProjectDelete(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runProjectDelete: %v", err)
	}
	var got struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if got.ID != webProjectGUID || !got.Deleted {
		t.Errorf("JSON = %+v", got)
	}
}

func TestProjectDeleteWithoutYesSendsNothing(t *testing.T) {
	var seen []gqlRequest
	projectCommandServer(t, "softDeleteProject", deletedProject(webProjectGUID), &seen)
	t.Cleanup(func() { projectDeleteYes = false })

	_, err := runCmd(t, "project", "delete", "web")
	if err == nil || err.Error() != `refusing to delete project "web" without --yes (nothing was changed)` {
		t.Fatalf("err = %v", err)
	}
	if len(seen) != 0 {
		t.Fatalf("sent %d requests without --yes", len(seen))
	}
}

func TestProjectDeleteRunsInTheWorkingOrganization(t *testing.T) {
	var seen []gqlRequest
	projectCommandServer(t, "softDeleteProject", deletedProject(webProjectGUID), &seen)
	t.Cleanup(func() { projectDeleteYes = false })

	out, err := runCmd(t, "project", "delete", "web", "--yes")
	if err != nil {
		t.Fatalf("project delete: %v (%s)", err, out)
	}
	// The lookup and the delete both carry the working organization, so the
	// project is resolved in the organization it is deleted from.
	var lookup, deletion gqlRequest
	for _, req := range seen {
		switch {
		case strings.Contains(req.Query, "astroliftProjects"):
			lookup = req
		case strings.Contains(req.Query, "softDeleteProject("):
			deletion = req
		}
	}
	if lookup.Query == "" || lookup.Organization != projectTestOrgGUID {
		t.Errorf("project lookup organization = %q", lookup.Organization)
	}
	if deletion.Query == "" || deletion.Organization != projectTestOrgGUID {
		t.Errorf("softDeleteProject organization = %q", deletion.Organization)
	}
	if !strings.Contains(out, "Deleted project web (Web) in team core") {
		t.Errorf("output:\n%s", out)
	}
}
