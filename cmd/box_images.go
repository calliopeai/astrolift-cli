package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// boxImage is one container image the working org's environment specs run
// (#86), with the specs that name it, so a client can offer a dropdown
// instead of asking for a free reference.
type boxImage struct {
	Image    string   `json:"image"`
	EnvSpecs []string `json:"envSpecs"`
}

var boxImagesCmd = &cobra.Command{
	Use:   "images",
	Short: "List the images this organization's environment specs run",
	Long: `Lists the container images named by the working organization's agent
environment specs, one row per image with the specs that use it. Pick one with
"astro box ensure --env-spec <slug>". "--image" takes any other reference.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runBoxImages(cmd, cmd.Context(), client, cfg)
	},
}

func runBoxImages(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config) error {
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	var resp struct {
		Specs []struct {
			Slug     string `json:"slug"`
			ImageTag string `json:"imageTag"`
		} `json:"agentEnvironmentSpecs"`
	}
	if err := client.GraphQL(ctx, `query($orgId:ID!){ agentEnvironmentSpecs(orgId:$orgId){ slug imageTag } }`,
		map[string]interface{}{"orgId": org.ID}, &resp); err != nil {
		return fmt.Errorf("listing environment specs: %w", err)
	}
	bySpec := map[string][]string{}
	for _, s := range resp.Specs {
		image := strings.TrimSpace(s.ImageTag)
		if image == "" {
			continue // a spec without an image runs the runtime default, not a choosable image
		}
		bySpec[image] = append(bySpec[image], s.Slug)
	}
	images := make([]boxImage, 0, len(bySpec))
	for image, specs := range bySpec {
		sort.Strings(specs)
		images = append(images, boxImage{Image: image, EnvSpecs: specs})
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Image < images[j].Image })
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, images)
	}
	if len(images) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No environment spec names an image yet.")
		return nil
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "IMAGE\tENV SPECS")
	for _, image := range images {
		fmt.Fprintf(tw, "%s\t%s\n", image.Image, strings.Join(image.EnvSpecs, ", "))
	}
	return tw.Flush()
}
