// Organization skill catalog and skill-repository sources (#148).
//
// Thin wrappers over the server's existing skill GraphQL: skills / skill /
// toolDefs for the catalog, orgSkillRepos and register/update/remove for
// repository sources, and importSkillsFromRepo for a TOML library. The
// server resolves imports and enforces permissions; nothing here re-implements
// either. `astro onboard --only skills` remains the separate local installer
// for bundled developer-workspace skills.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

type catalogSkill struct {
	ID           string `json:"id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Content      string `json:"content,omitempty"`
	SkillVersion int    `json:"skillVersion"`
	IsGlobal     bool   `json:"isGlobal"`
	IsActive     bool   `json:"isActive"`
	SourceKind   string `json:"sourceKind"`
	SourceRef    string `json:"sourceRef"`
	IsImported   bool   `json:"isImported"`
}

type catalogTool struct {
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Adapter         string `json:"adapter"`
	HandlerRef      string `json:"handlerRef"`
	CapabilityGroup string `json:"capabilityGroup"`
	IsBuiltin       bool   `json:"isBuiltin"`
}

type orgSkillRepo struct {
	ID                 string  `json:"id"`
	Alias              string  `json:"alias"`
	RepoFullName       string  `json:"repoFullName"`
	SourceKind         string  `json:"sourceKind"`
	DefaultRef         string  `json:"defaultRef"`
	DisplayName        string  `json:"displayName"`
	IsActive           bool    `json:"isActive"`
	IsPrivate          bool    `json:"isPrivate"`
	SourceConnectionID *string `json:"sourceConnectionId"`
}

const skillFields = `id slug name description skillVersion isGlobal isActive sourceKind sourceRef isImported`
const skillRepoFields = `id alias repoFullName sourceKind defaultRef displayName isActive isPrivate sourceConnectionId`

var (
	skillListGlobal      bool
	skillImportBranch    string
	skillImportManifest  string
	skillRepoRef         string
	skillRepoDisplayName string
	skillRepoSourceKind  string
	skillRepoConnection  string
	skillRepoRepo        string
	skillRepoActive      string
	skillRepoDetach      bool
	skillRepoYes         bool
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Organization skill catalog and skill-repository sources",
	Long: `Inspect the skills agents can use in the working organization and manage
the repositories they are imported from.

The catalog lists the organization's own skills (manual or imported from a
repository) together with the platform's global skills. "astro onboard --only
skills" is separate: it installs bundled skills into a local developer
workspace and does not touch this catalog.`,
}

var skillListCmd = &cobra.Command{
	Use: "list", Aliases: []string{"ls"},
	Short: "List the organization's skills plus the global catalog",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runSkillList(cmd, cmd.Context(), client, cfg)
	},
}

var skillInspectCmd = &cobra.Command{
	Use:   "inspect <slug>",
	Short: "Show one skill, its source and its attached tools",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runSkillInspect(cmd, cmd.Context(), client, cfg, args[0])
	},
}

var skillImportCmd = &cobra.Command{
	Use:   "import <github-repo>",
	Short: "Import skills and tools from a repository's astrolift.toml",
	Long: `Import every [skills.*] and [tools.*] table from a GitHub repository's
astrolift.toml into the organization catalog.

This writes the catalog: matching slugs are updated in place, and a skill's
version increases only when its content changes. The server has no preview
for imports. An invalid manifest (for example an unknown tool adapter)
imports nothing.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runSkillImport(cmd, cmd.Context(), client, cfg, args[0])
	},
}

var skillRepoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Manage the organization's skill-repository sources",
}

var skillRepoListCmd = &cobra.Command{
	Use: "list", Aliases: []string{"ls"},
	Short: "List registered skill repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runSkillRepoList(cmd, cmd.Context(), client, cfg)
	},
}

var skillRepoRegisterCmd = &cobra.Command{
	Use:   "register <alias> <owner/repo>",
	Short: "Register a skill repository under an alias",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runSkillRepoRegister(cmd, cmd.Context(), client, cfg, args[0], args[1])
	},
}

var skillRepoUpdateCmd = &cobra.Command{
	Use:   "update <alias>",
	Short: "Change a registered skill repository",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runSkillRepoUpdate(cmd, cmd.Context(), client, cfg, args[0])
	},
}

var skillRepoRemoveCmd = &cobra.Command{
	Use: "remove <alias>", Aliases: []string{"rm"},
	Short: "Remove a registered skill repository (imported skills stay)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runSkillRepoRemove(cmd, cmd.Context(), client, cfg, args[0])
	},
}

func skillOrigin(s catalogSkill) string {
	switch {
	case s.IsGlobal:
		return "global"
	case s.SourceRef != "":
		return s.SourceKind + " " + s.SourceRef
	default:
		return s.SourceKind
	}
}

func listCatalogSkills(ctx context.Context, client *api.Client, orgID string, globalOnly bool) ([]catalogSkill, error) {
	var resp struct {
		Skills []catalogSkill `json:"skills"`
	}
	query := `query($orgId: ID!, $isGlobal: Boolean!) { skills(orgId: $orgId, isGlobal: $isGlobal) { ` + skillFields + ` } }`
	if err := client.GraphQL(ctx, query, map[string]interface{}{"orgId": orgID, "isGlobal": globalOnly}, &resp); err != nil {
		return nil, fmt.Errorf("listing skills: %w", err)
	}
	return resp.Skills, nil
}

func runSkillList(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config) error {
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	skills, err := listCatalogSkills(ctx, client, org.ID, skillListGlobal)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, skills)
	}
	if len(skills) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No skills in this catalog.")
		return nil
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tVERSION\tACTIVE\tORIGIN\tNAME")
	for _, s := range skills {
		fmt.Fprintf(tw, "%s\tv%d\t%t\t%s\t%s\n", s.Slug, s.SkillVersion, s.IsActive, skillOrigin(s), s.Name)
	}
	return tw.Flush()
}

func runSkillInspect(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, slug string) error {
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	skills, err := listCatalogSkills(ctx, client, org.ID, false)
	if err != nil {
		return err
	}
	// An org skill shadows a global one with the same slug, as on the server.
	var match *catalogSkill
	for i := range skills {
		if skills[i].Slug == slug && (match == nil || match.IsGlobal) {
			match = &skills[i]
		}
	}
	if match == nil {
		return fmt.Errorf("skill %q not found in this organization's catalog (see `astro skill list`)", slug)
	}
	var resp struct {
		Skill *catalogSkill `json:"skill"`
		Tools []catalogTool `json:"toolDefs"`
	}
	query := `query($id: ID!) {
  skill(id: $id) { ` + skillFields + ` content }
  toolDefs(skillId: $id) { slug name description adapter handlerRef capabilityGroup isBuiltin }
}`
	if err := client.GraphQL(ctx, query, map[string]interface{}{"id": match.ID}, &resp); err != nil {
		return fmt.Errorf("reading skill %s: %w", slug, err)
	}
	if resp.Skill == nil {
		return fmt.Errorf("skill %q is no longer readable", slug)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, struct {
			catalogSkill
			Tools []catalogTool `json:"tools"`
		}{*resp.Skill, resp.Tools})
	}
	out := cmd.OutOrStdout()
	s := resp.Skill
	fmt.Fprintf(out, "%s (%s) v%d\n", s.Name, s.Slug, s.SkillVersion)
	fmt.Fprintf(out, "Origin:  %s\n", skillOrigin(*s))
	fmt.Fprintf(out, "Active:  %t\n", s.IsActive)
	if s.Description != "" {
		fmt.Fprintf(out, "About:   %s\n", s.Description)
	}
	if len(resp.Tools) == 0 {
		fmt.Fprintln(out, "Tools:   none")
	} else {
		fmt.Fprintln(out, "Tools:")
		for _, t := range resp.Tools {
			fmt.Fprintf(out, "  %s  %s %s\n", t.Slug, t.Adapter, t.HandlerRef)
		}
	}
	if s.Content != "" {
		fmt.Fprintf(out, "\n%s\n", strings.TrimRight(s.Content, "\n"))
	}
	return nil
}

func runSkillImport(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, repo string) error {
	if _, err := resolveOrg(cmd, ctx, client, cfg); err != nil {
		return err
	}
	var resp struct {
		Result struct {
			Ok     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *struct {
				ImportedSkills []string `json:"importedSkills"`
				ImportedTools  []string `json:"importedTools"`
				SourceRef      string   `json:"sourceRef"`
			} `json:"data"`
		} `json:"importSkillsFromRepo"`
	}
	mutation := `mutation($repoUrl: String!, $branch: String!, $manifestPath: String!) {
  importSkillsFromRepo(repoUrl: $repoUrl, branch: $branch, manifestPath: $manifestPath) {
    ok
    errors { code message field }
    data { importedSkills importedTools sourceRef }
  }
}`
	vars := map[string]interface{}{"repoUrl": repo, "branch": skillImportBranch, "manifestPath": skillImportManifest}
	if err := client.GraphQL(ctx, mutation, vars, &resp); err != nil {
		return fmt.Errorf("importing skills: %w", err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		return fmt.Errorf("import refused: %s", firstDeployError(resp.Result.Errors))
	}
	data := resp.Result.Data
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, data)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Imported from %s\n", data.SourceRef)
	fmt.Fprintf(out, "Skills: %s\n", joinOrNone(data.ImportedSkills))
	fmt.Fprintf(out, "Tools:  %s\n", joinOrNone(data.ImportedTools))
	return nil
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func listSkillRepos(ctx context.Context, client *api.Client, orgID string) ([]orgSkillRepo, error) {
	var resp struct {
		Repos []orgSkillRepo `json:"orgSkillRepos"`
	}
	query := `query($orgId: ID!) { orgSkillRepos(orgId: $orgId) { ` + skillRepoFields + ` } }`
	if err := client.GraphQL(ctx, query, map[string]interface{}{"orgId": orgID}, &resp); err != nil {
		return nil, fmt.Errorf("listing skill repositories: %w", err)
	}
	return resp.Repos, nil
}

func findSkillRepo(ctx context.Context, client *api.Client, orgID, alias string) (orgSkillRepo, error) {
	repos, err := listSkillRepos(ctx, client, orgID)
	if err != nil {
		return orgSkillRepo{}, err
	}
	for _, r := range repos {
		if r.Alias == alias {
			return r, nil
		}
	}
	return orgSkillRepo{}, fmt.Errorf("skill repository %q not found (see `astro skill repo list`)", alias)
}

func runSkillRepoList(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config) error {
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	repos, err := listSkillRepos(ctx, client, org.ID)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, repos)
	}
	if len(repos) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No skill repositories registered.")
		return nil
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ALIAS\tREPOSITORY\tREF\tACTIVE\tPRIVATE")
	for _, r := range repos {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%t\n", r.Alias, r.RepoFullName, r.DefaultRef, r.IsActive, r.IsPrivate)
	}
	return tw.Flush()
}

// runSkillRepoMutation runs one repository mutation. declarations and args are
// the GraphQL variable declarations and field arguments, e.g.
// "$input: RemoveOrgSkillRepoInput!" and "input: $input".
func runSkillRepoMutation(ctx context.Context, client *api.Client, field, declarations, args string, vars map[string]interface{}) (*orgSkillRepo, error) {
	var resp map[string]struct {
		Ok     bool            `json:"ok"`
		Errors []mutationError `json:"errors"`
		Data   *orgSkillRepo   `json:"data"`
	}
	mutation := fmt.Sprintf(`mutation(%s) {
  %s(%s) {
    ok
    errors { code message field }
    data { %s }
  }
}`, declarations, field, args, skillRepoFields)
	if err := client.GraphQL(ctx, mutation, vars, &resp); err != nil {
		return nil, err
	}
	result := resp[field]
	if !result.Ok || result.Data == nil {
		return nil, errors.New(firstDeployError(result.Errors))
	}
	return result.Data, nil
}

func renderSkillRepo(cmd *cobra.Command, verb string, repo *orgSkillRepo) error {
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, repo)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s skill repository %s (%s @ %s)\n", verb, repo.Alias, repo.RepoFullName, repo.DefaultRef)
	return nil
}

func runSkillRepoRegister(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, alias, repo string) error {
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	input := map[string]interface{}{
		"alias": alias, "repoFullName": repo, "sourceKind": skillRepoSourceKind, "defaultRef": skillRepoRef,
	}
	if skillRepoDisplayName != "" {
		input["displayName"] = skillRepoDisplayName
	}
	if skillRepoConnection != "" {
		input["sourceConnectionId"] = skillRepoConnection
	}
	created, err := runSkillRepoMutation(ctx, client, "registerOrgSkillRepo",
		"$input: RegisterOrgSkillRepoInput!, $orgId: ID!", "input: $input, orgId: $orgId",
		map[string]interface{}{"input": input, "orgId": org.ID})
	if err != nil {
		return fmt.Errorf("registering skill repository %s: %w", alias, err)
	}
	return renderSkillRepo(cmd, "Registered", created)
}

func runSkillRepoUpdate(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, alias string) error {
	if skillRepoConnection != "" && skillRepoDetach {
		return errors.New("pass either --source-connection or --detach-source-connection, not both")
	}
	input := map[string]interface{}{}
	if skillRepoRepo != "" {
		input["repoFullName"] = skillRepoRepo
	}
	if cmd.Flags().Changed("ref") {
		input["defaultRef"] = skillRepoRef
	}
	if skillRepoDisplayName != "" {
		input["displayName"] = skillRepoDisplayName
	}
	if cmd.Flags().Changed("source-kind") {
		input["sourceKind"] = skillRepoSourceKind
	}
	if skillRepoConnection != "" {
		input["sourceConnectionId"] = skillRepoConnection
	}
	if skillRepoDetach {
		input["detachSourceConnection"] = true
	}
	switch skillRepoActive {
	case "":
	case "true", "false":
		input["isActive"] = skillRepoActive == "true"
	default:
		return errors.New("--active must be true or false")
	}
	if len(input) == 0 {
		return errors.New("nothing to change: pass --repo, --ref, --display-name, --source-kind, --active or a source-connection flag")
	}
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	existing, err := findSkillRepo(ctx, client, org.ID, alias)
	if err != nil {
		return err
	}
	input["id"] = existing.ID
	updated, err := runSkillRepoMutation(ctx, client, "updateOrgSkillRepo",
		"$input: UpdateOrgSkillRepoInput!", "input: $input", map[string]interface{}{"input": input})
	if err != nil {
		return fmt.Errorf("updating skill repository %s: %w", alias, err)
	}
	return renderSkillRepo(cmd, "Updated", updated)
}

func runSkillRepoRemove(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, alias string) error {
	if !skillRepoYes {
		return fmt.Errorf("removing skill repository %s needs --yes; skills already imported from it stay in the catalog", alias)
	}
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	existing, err := findSkillRepo(ctx, client, org.ID, alias)
	if err != nil {
		return err
	}
	removed, err := runSkillRepoMutation(ctx, client, "removeOrgSkillRepo",
		"$input: RemoveOrgSkillRepoInput!", "input: $input",
		map[string]interface{}{"input": map[string]interface{}{"id": existing.ID}})
	if err != nil {
		return fmt.Errorf("removing skill repository %s: %w", alias, err)
	}
	return renderSkillRepo(cmd, "Removed", removed)
}

func init() {
	skillListCmd.Flags().BoolVar(&skillListGlobal, "global", false, "Only the platform's global skills")
	skillImportCmd.Flags().StringVar(&skillImportBranch, "branch", "main", "Branch or ref to import from")
	skillImportCmd.Flags().StringVar(&skillImportManifest, "manifest-path", "", "Repo-relative astrolift.toml path (default: the repo root)")

	for _, c := range []*cobra.Command{skillRepoRegisterCmd, skillRepoUpdateCmd} {
		c.Flags().StringVar(&skillRepoRef, "ref", "main", "Default branch or ref")
		c.Flags().StringVar(&skillRepoDisplayName, "display-name", "", "Human-readable name")
		c.Flags().StringVar(&skillRepoSourceKind, "source-kind", "github", "Source host kind")
		c.Flags().StringVar(&skillRepoConnection, "source-connection", "", "Source connection GUID for a private repository")
	}
	skillRepoUpdateCmd.Flags().StringVar(&skillRepoRepo, "repo", "", "New owner/repo")
	skillRepoUpdateCmd.Flags().StringVar(&skillRepoActive, "active", "", "Set active: true or false")
	skillRepoUpdateCmd.Flags().BoolVar(&skillRepoDetach, "detach-source-connection", false, "Stop using a source connection")
	skillRepoRemoveCmd.Flags().BoolVar(&skillRepoYes, "yes", false, "Confirm the removal")

	skillRepoCmd.AddCommand(skillRepoListCmd, skillRepoRegisterCmd, skillRepoUpdateCmd, skillRepoRemoveCmd)
	skillCmd.AddCommand(skillListCmd, skillInspectCmd, skillImportCmd, skillRepoCmd)
	rootCmd.AddCommand(skillCmd)
}
