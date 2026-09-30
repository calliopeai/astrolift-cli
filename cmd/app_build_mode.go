package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type updateAppResult struct {
	UpdateApp struct {
		Ok     bool `json:"ok"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data *struct {
			ID        string `json:"id"`
			Slug      string `json:"slug"`
			BuildMode string `json:"buildMode"`
		} `json:"data"`
	} `json:"updateApp"`
}

// appShowIDResult is the sliver of the show query this command needs: the guid,
// because updateApp is keyed by id while operators think in slugs.
type appShowIDResult struct {
	AstroliftApp *struct {
		ID        string `json:"id"`
		Slug      string `json:"slug"`
		BuildMode string `json:"buildMode"`
	} `json:"astroliftApp"`
}

var (
	appSetBuildModeJSON     bool
	appSetBuildModeStrategy string
)

// Updating both axes prevents a CI-pushed app from retaining its old builder.
func buildModeUpdateInput(id, mode, strategyOverride string) (map[string]interface{}, error) {
	if !oneOf(mode, validBuildModes) {
		return nil, fmt.Errorf("mode must be one of %s", strings.Join(validBuildModes, ", "))
	}
	input := map[string]interface{}{"id": id, "buildMode": mode}

	switch {
	case strategyOverride != "":
		if !oneOf(strategyOverride, validBuildStrategies) {
			return nil, fmt.Errorf("--build-strategy must be one of %s",
				strings.Join(validBuildStrategies, ", "))
		}
		input["buildStrategy"] = strategyOverride
	case mode == "platform_build":
		input["buildStrategy"] = "dockerfile"
	default:
		// ci_pushed and none both mean "the platform does not build".
		input["buildStrategy"] = "off"
	}
	return input, nil
}

var appSetBuildModeCmd = &cobra.Command{
	Use:   "set-build-mode <app-slug> <mode>",
	Short: "Change who publishes an app's container image",
	Long: `Flips an already-registered app's build mode.

  ci_pushed       your CI builds and pushes the image; the platform only
                  rolls out the tag passed to 'astro app deploy'
  platform_build  the platform fetches the source and builds in-cluster
  none            no image; for apps whose workloads are all external

The build mode and strategy change together. A Dockerfile path or build
context saved at registration is preserved when switching modes.

Use this when the platform builder cannot produce the image (a source repo it
cannot clone, no builder on the cluster) and your CI already can. The image
must exist in the registry before the next deploy under ci_pushed.

Examples:
  astro app set-build-mode my-app ci_pushed
  astro app set-build-mode my-app platform_build`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, mode := args[0], args[1]
		input, err := buildModeUpdateInput("", mode, appSetBuildModeStrategy)
		if err != nil {
			return err
		}

		client, cfg, _, err := loadActiveClient(cmd.Context(), false)
		if err != nil {
			return err
		}

		if _, err := resolveOrg(cmd, cmd.Context(), client, cfg); err != nil {
			return err
		}

		// Resolve slug → guid. updateApp takes an id, and asking the operator
		// for a guid they would have to look up first is not an interface.
		var show appShowIDResult
		if err := client.GraphQL(cmd.Context(),
			`query($slug: String!) { astroliftApp(slug: $slug) { id slug buildMode } }`,
			map[string]interface{}{"slug": slug}, &show); err != nil {
			return fmt.Errorf("look up app %q: %w", slug, err)
		}
		if show.AstroliftApp == nil {
			return fmt.Errorf("app %q not found in the current org", slug)
		}
		was := show.AstroliftApp.BuildMode

		const m = `
mutation SetBuildMode($input: UpdateAppInput!) {
  updateApp(input: $input) {
    ok
    errors { message }
    data { id slug buildMode }
  }
}`
		input["id"] = show.AstroliftApp.ID
		vars := map[string]interface{}{"input": input}
		var result updateAppResult
		if err := client.GraphQL(cmd.Context(), m, vars, &result); err != nil {
			return fmt.Errorf("updateApp: %w", err)
		}
		if !result.UpdateApp.Ok {
			msgs := make([]string, 0, len(result.UpdateApp.Errors))
			for _, e := range result.UpdateApp.Errors {
				msgs = append(msgs, e.Message)
			}
			if len(msgs) > 0 {
				return fmt.Errorf("updateApp failed: %s", msgs[0])
			}
			return fmt.Errorf("updateApp failed (no error detail returned)")
		}

		app := result.UpdateApp.Data
		if app == nil {
			return fmt.Errorf("updateApp succeeded but the server returned no app record")
		}
		if appSetBuildModeJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(app)
		}
		out := cmd.OutOrStdout()
		if was == mode {
			fmt.Fprintf(out, "%s: build mode %s, strategy %s\n", app.Slug, app.BuildMode, input["buildStrategy"])
			return nil
		}
		fmt.Fprintf(out, "%s: build mode %s -> %s\n", app.Slug, was, app.BuildMode)
		if app.BuildMode == "ci_pushed" {
			fmt.Fprintln(out, "")
			fmt.Fprintln(out, "Next deploy rolls out the tag you pass; push the image first.")
		}
		return nil
	},
}

func init() {
	appSetBuildModeCmd.Flags().BoolVar(&appSetBuildModeJSON, "json", false, "Output as JSON")
	appSetBuildModeCmd.Flags().StringVar(&appSetBuildModeStrategy, "build-strategy", "", "Override the strategy set alongside the mode: dockerfile, buildpacks, nixpacks, off")
	appCmd.AddCommand(appSetBuildModeCmd)
}
