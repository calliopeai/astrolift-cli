package cmd

import (
	"context"
	"fmt"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// astro app move (platform #2479) re-parents an app through
// assignAstroliftAppToProject, the mutation behind the console's Settings >
// Assign project. transferApp moves apps too, but the console never calls it
// and it asks for app.transfer plus app.create on the destination.

var appMoveProject string

const appPlacementQuery = `query($slug: String!) {
  astroliftApp(slug: $slug) { id slug teamSlug projectSlug projectId }
}`

const assignAppToProjectMutation = `mutation AssignAstroliftAppToProject($input: AssignAppToProjectInput!) {
  assignAstroliftAppToProject(input: $input) {
    ok
    errors { code message field }
    data { id slug teamSlug projectSlug projectId }
  }
}`

// appPlacement is where an app sits. An app with no project has an empty
// projectSlug and a null projectId.
type appPlacement struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	TeamSlug    string `json:"teamSlug"`
	ProjectSlug string `json:"projectSlug"`
	ProjectID   string `json:"projectId"`
}

var appMoveCmd = &cobra.Command{
	Use:   "move <app-slug> --project <project>",
	Short: "Move an app to another project",
	Long: `Moves an app to another project in the organization through
assignAstroliftAppToProject, as Settings > Assign project does in the
console. --project names the destination by slug or id, as astro project
list shows them; a slug that more than one team uses needs the id.

The app joins the destination project's team, so a project in another team
moves the app to that team as well. The move needs app.update on the app and
a role that reaches the destination project. The command prints the project
and team the app left and the ones it is in now.

Moving its apps out is how a project is emptied before astro project delete.

Examples:
  astro app move storefront --project web`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runAppMove(cmd, ctx, client, args[0], appMoveProject)
	}),
}

func init() {
	appMoveCmd.Flags().StringVar(&appMoveProject, "project", "", "Destination project slug or id")
	_ = appMoveCmd.MarkFlagRequired("project")
	appCmd.AddCommand(appMoveCmd)
}

func runAppMove(cmd *cobra.Command, ctx context.Context, client *api.Client, slug, projectSelector string) error {
	var show struct {
		App *appPlacement `json:"astroliftApp"`
	}
	if err := client.GraphQL(ctx, appPlacementQuery, map[string]interface{}{"slug": slug}, &show); err != nil {
		return fmt.Errorf("look up app %q: %w", slug, err)
	}
	if show.App == nil || show.App.ID == "" {
		return fmt.Errorf("app %q not found in the current org", slug)
	}
	before := *show.App

	project, err := resolveProjectSelector(ctx, client, projectSelector)
	if err != nil {
		return err
	}

	input := map[string]interface{}{"appSlug": slug, "projectGuid": project.ID}
	var resp struct {
		Result struct {
			Ok     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *appPlacement   `json:"data"`
		} `json:"assignAstroliftAppToProject"`
	}
	if err := client.GraphQL(ctx, assignAppToProjectMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("moving app %s: %w", slug, err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		return fmt.Errorf("app %s was not moved: %s", slug, firstDeployError(resp.Result.Errors))
	}

	after := resp.Result.Data
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, after)
	}
	out := cmd.OutOrStdout()
	if after.ProjectID == before.ProjectID && after.TeamSlug == before.TeamSlug {
		fmt.Fprintf(out, "%s is already in project %s (team %s); nothing changed.\n", slug, after.ProjectSlug, after.TeamSlug)
		return nil
	}
	from := "no project"
	if before.ProjectSlug != "" {
		from = "project " + before.ProjectSlug
	}
	fmt.Fprintf(out, "Moved %s from %s (team %s) to project %s (team %s)\n",
		slug, from, before.TeamSlug, after.ProjectSlug, after.TeamSlug)
	return nil
}
