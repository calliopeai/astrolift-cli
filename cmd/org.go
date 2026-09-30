// Package cmd — organization context: list, switch (`use`), show the
// resolved working org, and a shared resolver used by org-scoped commands.
//
// Org resolution precedence (resolveOrg):
//  1. --org flag (slug or id)            — per-command override
//  2. config DefaultOrg (`astro org use`) — persisted working org
//  3. the only org, if exactly one        — single-org instances "just work"
//  4. interactive picker                   — multi-org + a TTY + prompts on
//  5. error                                — multi-org, non-interactive
package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

type orgRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func boolFlag(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

func listOrgs(ctx context.Context, client *api.Client) ([]orgRef, error) {
	var resp struct {
		Organizations []orgRef `json:"astroliftOrganizations"`
	}
	if err := client.GraphQL(ctx, `query { astroliftOrganizations { id name slug } }`, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Organizations, nil
}

// stdinIsInteractive reports whether stdin is a terminal (for prompts).
func stdinIsInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// resolveOrg resolves the working organization to its (id, slug, name).
func resolveOrg(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config) (orgRef, error) {
	org, err := resolveOrgSelection(cmd, ctx, client, cfg)
	if err == nil {
		client.SetOrg(org.ID)
	}
	return org, err
}

func resolveOrgSelection(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config) (orgRef, error) {
	want := strings.TrimSpace(orgFlagValue(cmd))
	if want == "" {
		want = strings.TrimSpace(cfg.DefaultOrg)
	}
	orgs, err := listOrgs(ctx, client)
	if err != nil {
		return orgRef{}, err
	}
	if len(orgs) == 0 {
		return orgRef{}, errors.New("no organizations available for this account")
	}
	if want != "" {
		for _, o := range orgs {
			if o.Slug == want || o.ID == want {
				return o, nil
			}
		}
		return orgRef{}, fmt.Errorf("organization %q not found (see `astro org list`)", want)
	}
	if len(orgs) == 1 {
		return orgs[0], nil
	}
	if boolFlag(cmd, "no-prompt") || !stdinIsInteractive() {
		return orgRef{}, errors.New(
			"multiple organizations available; pass --org <slug> or set one with `astro org use <slug>`")
	}
	return pickOrg(cmd, orgs)
}

func orgFlagValue(cmd *cobra.Command) string {
	v, _ := cmd.Flags().GetString("org")
	return v
}

func pickOrg(cmd *cobra.Command, orgs []orgRef) (orgRef, error) {
	out := cmd.ErrOrStderr()
	fmt.Fprintln(out, "Select an organization:")
	for i, o := range orgs {
		fmt.Fprintf(out, "  [%d] %s (%s)\n", i+1, o.Slug, o.Name)
	}
	fmt.Fprint(out, "> ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return orgRef{}, fmt.Errorf("reading selection: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(orgs) {
		return orgRef{}, fmt.Errorf("invalid selection %q", strings.TrimSpace(line))
	}
	return orgs[n-1], nil
}

// ---- astro org list / use / current / show -------------------------------

func runOrgList(cmd *cobra.Command, args []string) error {
	client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	orgs, err := listOrgs(cmd.Context(), client)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, orgs)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ACTIVE\tSLUG\tNAME\tID")
	for _, o := range orgs {
		active := ""
		if o.Slug == cfg.DefaultOrg {
			active = "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", active, o.Slug, o.Name, o.ID)
	}
	return w.Flush()
}

func runOrgUse(cmd *cobra.Command, args []string) error {
	client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	orgs, err := listOrgs(cmd.Context(), client)
	if err != nil {
		return err
	}
	want := args[0]
	for _, o := range orgs {
		if o.Slug == want || o.ID == want {
			cfg.DefaultOrg = o.Slug
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Default organization set to %s (%s)\n", o.Slug, o.Name)
			return nil
		}
	}
	return fmt.Errorf("organization %q not found (see `astro org list`)", want)
}

func runOrgCurrent(cmd *cobra.Command, args []string) error {
	client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	org, err := resolveOrg(cmd, cmd.Context(), client, cfg)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, org)
	}
	src := "single org"
	if orgFlagValue(cmd) != "" {
		src = "--org flag"
	} else if cfg.DefaultOrg != "" {
		src = "default (astro org use)"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s (%s)  [%s]\n", org.Slug, org.Name, src)
	return nil
}

func runOrgShow(cmd *cobra.Command, args []string) error {
	client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	if len(args) == 1 {
		_ = cmd.Flags().Set("org", args[0])
	}
	org, err := resolveOrg(cmd, cmd.Context(), client, cfg)
	if err != nil {
		return err
	}
	return renderJSON(cmd, org)
}

var orgUseCmd = &cobra.Command{
	Use:   "use <slug>",
	Short: "Set the default working organization (persisted in config)",
	Args:  cobra.ExactArgs(1),
	RunE:  runOrgUse,
}

var orgCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the resolved working organization and where it came from",
	RunE:  runOrgCurrent,
}

func init() {
	orgCmd.AddCommand(orgUseCmd, orgCurrentCmd)
}
