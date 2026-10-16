// Package cmd -- `astro app manifest push|pull`: settle an app's staged
// manifest draft without the web Config page (SteadyMD DV-9250, #159).
//
// An app registered with --manifest-raw, or edited on the platform, holds a
// staged draft, and the repo resync reports it as diverged until the draft
// is pushed to the repo or discarded by pulling the repo's manifest.
//
// GraphQL operations (field names per backend/schema.graphql):
//   - lookup → astroliftApp(slug) { id }; both mutations are keyed by id
//   - push   → pushManifestToRepo(input: PushManifestToRepoInput!)
//   - pull   → syncManifestFromRepo(input: SyncManifestFromRepoInput!)
package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

var appManifestYes bool

// ---- GraphQL operations -----------------------------------------------------

const appIDBySlugQuery = `query($slug: String!) { astroliftApp(slug: $slug) { id } }`

const pushManifestToRepoMutation = `mutation($input: PushManifestToRepoInput!) {
  pushManifestToRepo(input: $input) {
    ok
    errors { code message field }
    data { id prUrl branchName note }
  }
}`

const syncManifestFromRepoMutation = `mutation($input: SyncManifestFromRepoInput!) {
  syncManifestFromRepo(input: $input) {
    ok
    errors { code message field }
    data { id syncState rawManifestStaged }
  }
}`

type manifestPushResult struct {
	ID         string `json:"id"`
	PrURL      string `json:"prUrl"`
	BranchName string `json:"branchName"`
	Note       string `json:"note"`
}

// manifestPullResult decodes rawManifestStaged only to tell whether staged
// [env] secrets were kept; --json leaves the manifest text out of CI logs.
type manifestPullResult struct {
	ID                string `json:"id"`
	SyncState         string `json:"syncState"`
	RawManifestStaged string `json:"rawManifestStaged"`
}

// ---- astro app manifest -----------------------------------------------------

var appManifestCmd = &cobra.Command{
	Use:   "manifest",
	Short: "Push an app's staged manifest draft to its repo, or replace it with the repo's",
}

var appManifestPushCmd = &cobra.Command{
	Use:   "push [app]",
	Short: "Open a pull request with the app's staged manifest draft",
	Long: `Writes the app's staged manifest draft to a branch of its source repo and
opens a pull request against the default branch (pushManifestToRepo), then
prints the pull request URL.

App secrets live as values in the manifest's top-level [env] table. The pull
request never carries them: the file it writes keeps the [env] table already
on the default branch, and the secrets stay staged on the platform. A draft
whose only changes are in [env] has nothing to push.

The draft stays staged after the push. Once the pull request merges, run
'astro app manifest pull' to land the merged manifest; it keeps the staged
[env] secrets. Prompts for confirmation unless --yes is given.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAppManifestCommand(cmd, firstArg(args), runAppManifestPush)
	},
}

var appManifestPullCmd = &cobra.Command{
	Use:   "pull [app]",
	Short: "Replace the app's staged manifest draft with the manifest in its repo",
	Long: `Fetches the manifest from the app's source repo and applies it
(syncManifestFromRepo). Staged edits outside the top-level [env] table are
discarded. App secrets staged in [env] are made again on the pulled manifest
on control planes with astrolift-app #2410; older ones discard them too.

Prompts for confirmation unless --yes is given.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAppManifestCommand(cmd, firstArg(args), runAppManifestPull)
	},
}

func runAppManifestCommand(cmd *cobra.Command, appArg string, run func(*cobra.Command, context.Context, *api.Client, string) error) error {
	slug, err := resolveAppSlug(cmd, appArg)
	if err != nil {
		return err
	}
	client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	return run(cmd, cmd.Context(), client, slug)
}

// lookupAppID resolves a slug to the app GUID the manifest mutations take.
func lookupAppID(ctx context.Context, client *api.Client, slug string) (string, error) {
	var resp struct {
		App *struct {
			ID string `json:"id"`
		} `json:"astroliftApp"`
	}
	if err := client.GraphQL(ctx, appIDBySlugQuery, map[string]interface{}{"slug": slug}, &resp); err != nil {
		return "", fmt.Errorf("looking up app %q: %w", slug, err)
	}
	if resp.App == nil || resp.App.ID == "" {
		return "", fmt.Errorf("app %q not found in the current org", slug)
	}
	return resp.App.ID, nil
}

// confirmManifestAction asks before a push or pull. --yes skips the prompt;
// with prompts disabled, --yes is required.
func confirmManifestAction(cmd *cobra.Command, question string) (bool, error) {
	if appManifestYes {
		return true, nil
	}
	noPrompt, _ := cmd.Flags().GetBool("no-prompt")
	if !noPrompt {
		noPrompt, _ = cmd.Root().PersistentFlags().GetBool("no-prompt")
	}
	if noPrompt {
		return false, fmt.Errorf("pass --yes to %s when prompts are disabled (nothing was changed)", cmd.Name())
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", question)
	var answer string
	_, _ = fmt.Fscan(cmd.InOrStdin(), &answer)
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
		return false, nil
	}
	return true, nil
}

func runAppManifestPush(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	id, err := lookupAppID(ctx, client, slug)
	if err != nil {
		return err
	}
	ok, err := confirmManifestAction(cmd, fmt.Sprintf("Open a pull request with the staged manifest draft for %s?", slug))
	if err != nil || !ok {
		return err
	}
	var resp struct {
		Result struct {
			Ok     bool                `json:"ok"`
			Errors []mutationError     `json:"errors"`
			Data   *manifestPushResult `json:"data"`
		} `json:"pushManifestToRepo"`
	}
	if err := client.GraphQL(ctx, pushManifestToRepoMutation,
		map[string]interface{}{"input": map[string]interface{}{"id": id}}, &resp); err != nil {
		return fmt.Errorf("pushing the manifest draft for %s: %w", slug, err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		return fmt.Errorf("pushing the manifest draft for %s: %s", slug, firstDeployError(resp.Result.Errors))
	}
	d := resp.Result.Data
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, d)
	}
	out := cmd.OutOrStdout()
	switch d.Note {
	case "nothing_to_push":
		fmt.Fprintf(out, "Nothing to push: %s has no staged manifest draft.\n", slug)
	case "env_only":
		fmt.Fprintf(out, "Nothing to push: the draft for %s only changes app secrets in the top-level [env] table.\n", slug)
		fmt.Fprintln(out, "App secrets stay on the platform and are never written to the repo.")
	default:
		fmt.Fprintf(out, "Opened a pull request for %s: %s\n", slug, d.PrURL)
		fmt.Fprintf(out, "  branch: %s\n", d.BranchName)
		fmt.Fprintln(out, "App secrets in the top-level [env] table are not in it; they stay staged on the platform.")
		fmt.Fprintf(out, "After it merges, run 'astro app manifest pull %s' to land the merged manifest.\n", slug)
	}
	return nil
}

func runAppManifestPull(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	id, err := lookupAppID(ctx, client, slug)
	if err != nil {
		return err
	}
	ok, err := confirmManifestAction(cmd, fmt.Sprintf("Replace the staged manifest draft for %s with the manifest in its repo? Staged edits outside [env] are discarded.", slug))
	if err != nil || !ok {
		return err
	}
	var resp struct {
		Result struct {
			Ok     bool                `json:"ok"`
			Errors []mutationError     `json:"errors"`
			Data   *manifestPullResult `json:"data"`
		} `json:"syncManifestFromRepo"`
	}
	if err := client.GraphQL(ctx, syncManifestFromRepoMutation,
		map[string]interface{}{"input": map[string]interface{}{"id": id}}, &resp); err != nil {
		return fmt.Errorf("pulling the manifest for %s: %w", slug, err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		return fmt.Errorf("pulling the manifest for %s: %s", slug, firstDeployError(resp.Result.Errors))
	}
	d := resp.Result.Data
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, map[string]string{"id": d.ID, "syncState": d.SyncState})
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Pulled the manifest for %s from its repo (sync state: %s).\n", slug, dashIfEmpty(d.SyncState))
	if d.RawManifestStaged != "" {
		fmt.Fprintln(out, "App secrets staged in the top-level [env] table were kept on the pulled manifest.")
	}
	return nil
}

func init() {
	for _, c := range []*cobra.Command{appManifestPushCmd, appManifestPullCmd} {
		c.Flags().BoolVarP(&appManifestYes, "yes", "y", false, "Skip the confirmation prompt")
	}
	appManifestCmd.AddCommand(appManifestPushCmd, appManifestPullCmd)
}
