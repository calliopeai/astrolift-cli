package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// Project rename and delete (platform #2479), through the updateProject and
// softDeleteProject mutations the console's Projects page uses.

var (
	projectRenameSlug string
	projectRenameName string
	projectDeleteYes  bool
)

const projectLookupQuery = `query { astroliftProjects { id slug name team { slug name } } }`

const updateProjectMutation = `mutation UpdateProject($input: UpdateProjectInput!) {
  updateProject(input: $input) {
    ok
    errors { code message field }
    data { id slug name team { slug name } }
  }
}`

const softDeleteProjectMutation = `mutation SoftDeleteProject($input: SoftDeleteByGuidInput!) {
  softDeleteProject(input: $input) {
    ok
    errors { code message field }
    data { id deleted }
  }
}`

var projectRenameCmd = &cobra.Command{
	Use:   "rename <project>",
	Short: "Change a project's slug, display name, or both",
	Long: `Renames a project through updateProject. Name the project by its slug or
id, as astro project list shows them; a slug that more than one team uses
needs the id.

--slug sets a new slug, which must be free in the project's team, and --name
sets a new display name. Give either or both. The platform checks the slug,
and a refusal is printed with the field it names.

Commands and settings that name the old slug, such as --project and
default_project, need the new one.

Examples:
  astro project rename web --slug storefront
  astro project rename web --name Storefront`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runProjectRename(cmd, ctx, client, args[0], projectRenameSlug, projectRenameName)
	}),
}

var projectDeleteCmd = &cobra.Command{
	Use:   "delete <project>",
	Short: "Delete a project that has nothing left in it",
	Long: `Deletes a project through softDeleteProject. Name the project by its slug
or id, as astro project list shows them; a slug that more than one team uses
needs the id.

The platform refuses while the project still has apps, managed resources or
shared secret bundles. The command then prints the platform's reason and
exits 1. Empty the project first: move each app to another project with
astro app move or deregister it, deprovision each resource with
astro project resources remove, and delete its secret bundles.

There is no interactive prompt: --yes is the confirmation (CI-safe).
Without it, nothing is sent and nothing is changed.

Examples:
  astro project delete legacy --yes`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !projectDeleteYes {
			return fmt.Errorf("refusing to delete project %q without --yes (nothing was changed)", args[0])
		}
		return workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
			return runProjectDelete(cmd, ctx, client, args[0])
		})(cmd, args)
	},
}

func init() {
	projectRenameCmd.Flags().StringVar(&projectRenameSlug, "slug", "", "New project slug")
	projectRenameCmd.Flags().StringVar(&projectRenameName, "name", "", "New display name")
	projectDeleteCmd.Flags().BoolVarP(&projectDeleteYes, "yes", "y", false, "Confirm the deletion (required to proceed)")
	projectCmd.AddCommand(projectRenameCmd, projectDeleteCmd)
}

// resolveProjectSelector finds one project in the working organization by id
// or slug. A project slug is unique within its team, not across the
// organization, so a slug that several teams use is refused, not guessed.
func resolveProjectSelector(ctx context.Context, client *api.Client, selector string) (projectRef, error) {
	want := strings.TrimSpace(selector)
	if want == "" {
		return projectRef{}, fmt.Errorf("give the project's slug or id (see `astro project list`)")
	}
	var resp struct {
		Projects []projectRef `json:"astroliftProjects"`
	}
	if err := client.GraphQL(ctx, projectLookupQuery, nil, &resp); err != nil {
		return projectRef{}, fmt.Errorf("resolving project: %w", err)
	}
	var matches []projectRef
	for _, p := range resp.Projects {
		if p.ID == want {
			return p, nil
		}
		if p.Slug == want {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return projectRef{}, fmt.Errorf("project %q not found in the working organization (see `astro project list`)", want)
	case 1:
		return matches[0], nil
	}
	teams := make([]string, 0, len(matches))
	for _, p := range matches {
		teams = append(teams, p.Team.Slug)
	}
	return projectRef{}, fmt.Errorf("project slug %q is used in teams %s; give the project's id (see `astro project list`)",
		want, strings.Join(teams, ", "))
}

func runProjectRename(cmd *cobra.Command, ctx context.Context, client *api.Client, selector, newSlug, newName string) error {
	newSlug, newName = strings.TrimSpace(newSlug), strings.TrimSpace(newName)
	if newSlug == "" && newName == "" {
		return fmt.Errorf("give the new slug as --slug, the new display name as --name, or both")
	}
	project, err := resolveProjectSelector(ctx, client, selector)
	if err != nil {
		return err
	}

	// Only the fields given are sent; the platform leaves the others as they are.
	input := map[string]interface{}{"id": project.ID}
	if newSlug != "" {
		input["slug"] = newSlug
	}
	if newName != "" {
		input["name"] = newName
	}
	var resp struct {
		Result struct {
			Ok     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *projectRef     `json:"data"`
		} `json:"updateProject"`
	}
	if err := client.GraphQL(ctx, updateProjectMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("renaming project %s: %w", project.Slug, err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		return fmt.Errorf("project %s was not renamed: %s", project.Slug, firstDeployError(resp.Result.Errors))
	}

	renamed := resp.Result.Data
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, renamed)
	}
	out := cmd.OutOrStdout()
	if renamed.Slug == project.Slug && renamed.Name == project.Name {
		fmt.Fprintf(out, "Project %s (%s) already had that slug and name; nothing changed.\n", project.Slug, project.Name)
		return nil
	}
	fmt.Fprintf(out, "Project renamed: %s (%s) -> %s (%s) in team %s\n",
		project.Slug, project.Name, renamed.Slug, renamed.Name, renamed.Team.Slug)
	return nil
}

func runProjectDelete(cmd *cobra.Command, ctx context.Context, client *api.Client, selector string) error {
	project, err := resolveProjectSelector(ctx, client, selector)
	if err != nil {
		return err
	}
	var resp struct {
		Result struct {
			Ok     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *struct {
				ID      string `json:"id"`
				Deleted bool   `json:"deleted"`
			} `json:"data"`
		} `json:"softDeleteProject"`
	}
	vars := map[string]interface{}{"input": map[string]interface{}{"id": project.ID}}
	if err := client.GraphQL(ctx, softDeleteProjectMutation, vars, &resp); err != nil {
		return fmt.Errorf("deleting project %s: %w", project.Slug, err)
	}
	// The refusal while apps, managed resources or secret bundles remain is
	// PRECONDITION with the counts and the steps in its message, so the
	// message is printed as the platform wrote it.
	if !resp.Result.Ok || resp.Result.Data == nil {
		return fmt.Errorf("project %s was not deleted: %s", project.Slug, firstDeployError(resp.Result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Result.Data)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deleted project %s (%s) in team %s\n", project.Slug, project.Name, project.Team.Slug)
	return nil
}
