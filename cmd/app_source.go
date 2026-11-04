package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

var appSetSourceRepo string

const appSourceQuery = `query($slug: String!) {
  astroliftApp(slug: $slug) { id slug version sourceRepo }
}`

const setAppSourceMutation = `mutation SetAppSource($input: SetAppSourceInput!) {
  setAppSource(input: $input) {
    ok
    errors { code message field }
    data {
      previousSourceRepo
      app { id slug sourceRepo sourceUrl autowire { connected ciWorkflow webhook secrets detail } }
    }
  }
}`

type appAutowire struct {
	Connected  bool   `json:"connected"`
	CIWorkflow string `json:"ciWorkflow"`
	Webhook    string `json:"webhook"`
	Secrets    string `json:"secrets"`
	Detail     string `json:"detail"`
}

type appSourceChange struct {
	PreviousSourceRepo string `json:"previousSourceRepo"`
	App                struct {
		ID         string       `json:"id"`
		Slug       string       `json:"slug"`
		SourceRepo string       `json:"sourceRepo"`
		SourceURL  string       `json:"sourceUrl"`
		Autowire   *appAutowire `json:"autowire"`
	} `json:"app"`
}

var appSetSourceCmd = &cobra.Command{
	Use:   "set-source <app-slug> --source-repo <owner/name>",
	Short: "Point an app at another GitHub repository, or its new name",
	Long: `Points an app at its GitHub repository's new name after a rename, or at
a different repository.

The repository must be in the organization's GitHub App installation on
its owner, and the app stores the name as GitHub lists it. A source URL
that named the old repository follows it. Autowire then runs against the
repository: the CI workflow and the CI push role follow it, and the CI
secrets are pushed there, which rotates the deploy token. The command
prints each step's result.

The platform also follows a rename by itself when the organization's
GitHub App delivers repository events.

Examples:
  astro app set-source fossil --source-repo ConflictHQ/fossilrepos`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runAppSetSource(cmd, ctx, client, args[0], appSetSourceRepo)
	}),
}

func runAppSetSource(cmd *cobra.Command, ctx context.Context, client *api.Client, slug, sourceRepo string) error {
	repo := strings.TrimSpace(sourceRepo)
	if repo == "" {
		return fmt.Errorf("give the repository as --source-repo owner/name")
	}

	var show struct {
		App *struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"astroliftApp"`
	}
	if err := client.GraphQL(ctx, appSourceQuery, map[string]interface{}{"slug": slug}, &show); err != nil {
		return fmt.Errorf("look up app %q: %w", slug, err)
	}
	if show.App == nil || show.App.ID == "" {
		return fmt.Errorf("app %q not found in the current org", slug)
	}

	input := map[string]interface{}{
		"id":         show.App.ID,
		"sourceRepo": repo,
		// Refused if the app changed since it was read above.
		"ifMatchVersion": show.App.Version,
	}
	var resp struct {
		Result struct {
			Ok     bool             `json:"ok"`
			Errors []mutationError  `json:"errors"`
			Data   *appSourceChange `json:"data"`
		} `json:"setAppSource"`
	}
	if err := client.GraphQL(ctx, setAppSourceMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("setAppSource: %w", err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		for _, e := range resp.Result.Errors {
			if e.Code == "VERSION_MISMATCH" {
				return fmt.Errorf("%s changed while this command ran; nothing was saved, run it again", slug)
			}
		}
		return fmt.Errorf("setAppSource failed: %s", firstDeployError(resp.Result.Errors))
	}

	change := resp.Result.Data
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, change)
	}
	out := cmd.OutOrStdout()
	if change.App.SourceRepo == change.PreviousSourceRepo {
		fmt.Fprintf(out, "%s already points at %s; nothing changed.\n", slug, change.App.SourceRepo)
		return nil
	}
	fmt.Fprintf(out, "%s: source %s -> %s\n", slug, change.PreviousSourceRepo, change.App.SourceRepo)
	printSourceAutowire(out, slug, change.App.Autowire)
	return nil
}

// printSourceAutowire reports the wiring that ran against the new repository.
func printSourceAutowire(w io.Writer, slug string, a *appAutowire) {
	if a == nil {
		return
	}
	if !a.Connected {
		fmt.Fprintln(w, "Nothing was wired: no organization source connection can write to the repository.")
		fmt.Fprintf(w, "Connect one, then run: astro ci setup %s\n", slug)
		return
	}
	fmt.Fprintf(w, "Autowire: CI workflow %s, webhook %s, secrets %s\n", a.CIWorkflow, a.Webhook, a.Secrets)
	if a.Detail != "" {
		fmt.Fprintf(w, "  %s\n", a.Detail)
	}
	if a.CIWorkflow != "ok" || a.Webhook != "ok" || a.Secrets != "ok" {
		fmt.Fprintf(w, "Retry the wiring with: astro ci setup %s\n", slug)
	}
}

func init() {
	appSetSourceCmd.Flags().StringVar(&appSetSourceRepo, "source-repo", "", "The GitHub repository, as owner/name")
	_ = appSetSourceCmd.MarkFlagRequired("source-repo")
	appCmd.AddCommand(appSetSourceCmd)
}
