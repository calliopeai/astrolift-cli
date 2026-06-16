// Package cmd — `astro agent env-spec ...`: CRUD for AgentEnvironmentSpec
// (the dispatch recipe: image, the config repo + manifest_path a Brief is
// assembled from, secret refs, env). Calls the control-plane GraphQL API.
//
// upsert is idempotent: it queries the spec by slug, then updates (slug-keyed,
// org from the token) or creates (needs an org — resolved via resolveOrg, so
// --org / `astro org use` / the picker all apply).
package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

const createSpecMutation = `mutation($input: CreateAgentEnvironmentSpecInput!, $orgId: ID!) {
  createAgentEnvironmentSpec(input: $input, orgId: $orgId) { ok errors { message } data { slug } }
}`

const updateSpecMutation = `mutation($slug: String!, $input: UpdateAgentEnvironmentSpecInput!) {
  updateAgentEnvironmentSpec(slug: $slug, input: $input) { ok errors { message } data { slug } }
}`

const deleteSpecMutation = `mutation($slug: String!) {
  deleteAgentEnvironmentSpec(slug: $slug) { ok errors { message } }
}`

type specMutationResult struct {
	Ok     bool `json:"ok"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func specMutErr(r specMutationResult, op string) error {
	if r.Ok {
		return nil
	}
	if len(r.Errors) > 0 {
		return fmt.Errorf("%s failed: %s", op, r.Errors[0].Message)
	}
	return fmt.Errorf("%s failed", op)
}

func parsePairs(items []string, what string) (map[string]string, error) {
	out := map[string]string{}
	for _, raw := range items {
		k, v, ok := strings.Cut(raw, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("--%s must be KEY=VALUE, got %q", what, raw)
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, nil
}

var (
	envSpecName         string
	envSpecAgentType    string
	envSpecImageTag     string
	envSpecRuntime      string
	envSpecToolPreset   string
	envSpecConfigRepo   string
	envSpecConfigBranch string
	envSpecManifestPath string
	envSpecAllowInstall bool
	envSpecVNC          bool
	envSpecEnv          []string
	envSpecSecret       []string
)

var agentEnvSpecCmd = &cobra.Command{
	Use:     "env-spec",
	Aliases: []string{"envspec", "spec"},
	Short:   "Manage agent environment specs (image + config repo/manifest + secrets)",
}

var agentEnvSpecUpsertCmd = &cobra.Command{
	Use:   "upsert <slug>",
	Short: "Create or update an agent environment spec (idempotent)",
	Args:  cobra.ExactArgs(1),
	RunE:  runEnvSpecUpsert,
}

var agentEnvSpecLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List agent environment specs in the working org",
	RunE:  runEnvSpecLs,
}

var agentEnvSpecRmCmd = &cobra.Command{
	Use:   "rm <slug>",
	Short: "Delete an agent environment spec",
	Args:  cobra.ExactArgs(1),
	RunE:  runEnvSpecRm,
}

func runEnvSpecUpsert(cmd *cobra.Command, args []string) error {
	slug := args[0]
	ctx := cmd.Context()
	client, cfg, _, err := loadActiveClient(ctx, boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	if envSpecAgentType != "claude" && envSpecAgentType != "codex" {
		return fmt.Errorf("--agent-type must be claude or codex, got %q", envSpecAgentType)
	}
	envVars, err := parsePairs(envSpecEnv, "env")
	if err != nil {
		return err
	}
	secretPairs, err := parsePairs(envSpecSecret, "secret")
	if err != nil {
		return err
	}
	secretRefs := make([]map[string]string, 0, len(secretPairs))
	for envVar, uri := range secretPairs {
		secretRefs = append(secretRefs, map[string]string{"env_var": envVar, "uri": uri})
	}
	name := envSpecName
	if name == "" {
		name = slug
	}

	common := map[string]interface{}{
		"name":               name,
		"agentType":          envSpecAgentType,
		"imageTag":           envSpecImageTag,
		"runtime":            envSpecRuntime,
		"toolPreset":         envSpecToolPreset,
		"allowInstall":       envSpecAllowInstall,
		"vncEnabled":         envSpecVNC,
		"configRepo":         envSpecConfigRepo,
		"configBranch":       envSpecConfigBranch,
		"configManifestPath": envSpecManifestPath,
		"secretRefs":         secretRefs,
		"envVars":            envVars,
	}

	// Already exists? -> update (slug-keyed, org from token).
	var existing struct {
		AgentEnvironmentSpec *struct {
			Slug string `json:"slug"`
		} `json:"agentEnvironmentSpec"`
	}
	if err := client.GraphQL(ctx,
		`query($slug:String!){ agentEnvironmentSpec(slug:$slug){ slug } }`,
		map[string]interface{}{"slug": slug}, &existing); err != nil {
		return err
	}

	if existing.AgentEnvironmentSpec != nil {
		var resp struct {
			Result specMutationResult `json:"updateAgentEnvironmentSpec"`
		}
		if err := client.GraphQL(ctx, updateSpecMutation,
			map[string]interface{}{"slug": slug, "input": common}, &resp); err != nil {
			return err
		}
		if err := specMutErr(resp.Result, "update"); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Updated env-spec %s\n", slug)
		return nil
	}

	// Create -> needs an org.
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	createInput := map[string]interface{}{"slug": slug}
	for k, v := range common {
		createInput[k] = v
	}
	var resp struct {
		Result specMutationResult `json:"createAgentEnvironmentSpec"`
	}
	if err := client.GraphQL(ctx, createSpecMutation,
		map[string]interface{}{"input": createInput, "orgId": org.ID}, &resp); err != nil {
		return err
	}
	if err := specMutErr(resp.Result, "create"); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Created env-spec %s in org %s\n", slug, org.Slug)
	return nil
}

func runEnvSpecLs(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cfg, _, err := loadActiveClient(ctx, boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	type specRow struct {
		Slug               string `json:"slug"`
		AgentType          string `json:"agentType"`
		ImageTag           string `json:"imageTag"`
		ConfigRepo         string `json:"configRepo"`
		ConfigManifestPath string `json:"configManifestPath"`
	}
	var resp struct {
		AgentEnvironmentSpecs []specRow `json:"agentEnvironmentSpecs"`
	}
	if err := client.GraphQL(ctx,
		`query($orgId:ID!){ agentEnvironmentSpecs(orgId:$orgId){ slug agentType imageTag configRepo configManifestPath } }`,
		map[string]interface{}{"orgId": org.ID}, &resp); err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.AgentEnvironmentSpecs)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tTYPE\tIMAGE\tCONFIG")
	for _, s := range resp.AgentEnvironmentSpecs {
		img := s.ImageTag
		if img == "" {
			img = "-"
		}
		cfgRef := s.ConfigRepo
		if s.ConfigManifestPath != "" {
			cfgRef += ":" + s.ConfigManifestPath
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.Slug, s.AgentType, img, cfgRef)
	}
	return w.Flush()
}

func runEnvSpecRm(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, _, _, err := loadActiveClient(ctx, boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	var resp struct {
		Result specMutationResult `json:"deleteAgentEnvironmentSpec"`
	}
	if err := client.GraphQL(ctx, deleteSpecMutation,
		map[string]interface{}{"slug": args[0]}, &resp); err != nil {
		return err
	}
	if err := specMutErr(resp.Result, "delete"); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deleted env-spec %s\n", args[0])
	return nil
}

func init() {
	f := agentEnvSpecUpsertCmd.Flags()
	f.StringVar(&envSpecName, "name", "", "Display name (defaults to slug)")
	f.StringVar(&envSpecAgentType, "agent-type", "", "Agent runtime: claude|codex (required)")
	f.StringVar(&envSpecImageTag, "image-tag", "", "Explicit image URI (wins over --runtime)")
	f.StringVar(&envSpecRuntime, "runtime", "", "Catalog runtime short-name (used if --image-tag blank)")
	f.StringVar(&envSpecToolPreset, "tool-preset", "", "Named tool bundle")
	f.StringVar(&envSpecConfigRepo, "config-repo", "", `"owner/repo" of the config repo`)
	f.StringVar(&envSpecConfigBranch, "config-branch", "main", "Config repo branch")
	f.StringVar(&envSpecManifestPath, "manifest-path", "", "Repo-relative path to the agent's astrolift.toml (or its dir)")
	f.BoolVar(&envSpecAllowInstall, "allow-install", false, "Allow runtime package installs")
	f.BoolVar(&envSpecVNC, "vnc", false, "Enable VNC for this spec")
	f.StringArrayVar(&envSpecEnv, "env", nil, "Non-secret env var KEY=VALUE (repeatable)")
	f.StringArrayVar(&envSpecSecret, "secret", nil, "Secret ref ENV_VAR=secret-store-name (repeatable)")
	_ = agentEnvSpecUpsertCmd.MarkFlagRequired("agent-type")

	agentEnvSpecCmd.AddCommand(agentEnvSpecUpsertCmd, agentEnvSpecLsCmd, agentEnvSpecRmCmd)
	agentCmd.AddCommand(agentEnvSpecCmd)
}
