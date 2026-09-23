package cmd

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

var (
	applyFile   string
	applyDryRun bool
)

// applySpec is one [[env_spec]] table. Only the keys present in the file are
// managed; a key the file leaves out keeps whatever value the spec has.
type applySpec struct {
	Slug         string            `toml:"slug"`
	Name         string            `toml:"name"`
	AgentType    string            `toml:"agent_type"`
	ImageTag     string            `toml:"image_tag"`
	Runtime      string            `toml:"runtime"`
	ToolPreset   string            `toml:"tool_preset"`
	AllowInstall bool              `toml:"allow_install"`
	VNC          bool              `toml:"vnc"`
	ManagedModel bool              `toml:"managed_model"`
	RunAsNonRoot bool              `toml:"run_as_non_root"`
	Env          map[string]string `toml:"env"`
	Secrets      map[string]string `toml:"secrets"`
	ConfigRepo   string            `toml:"config_repo"`
	ConfigBranch string            `toml:"config_branch"`
	ManifestPath string            `toml:"manifest_path"`
}

type applyFileShape struct {
	EnvSpec []applySpec `toml:"env_spec"`
}

// applyFields maps each file key to its GraphQL input field.
var applyFields = []struct{ key, field string }{
	{"name", "name"},
	{"agent_type", "agentType"},
	{"image_tag", "imageTag"},
	{"runtime", "runtime"},
	{"tool_preset", "toolPreset"},
	{"allow_install", "allowInstall"},
	{"vnc", "vncEnabled"},
	{"managed_model", "managedModel"},
	{"run_as_non_root", "runAsNonRoot"},
	{"env", "envVars"},
	{"secrets", "secretRefs"},
	{"config_repo", "configRepo"},
	{"config_branch", "configBranch"},
	{"manifest_path", "configManifestPath"},
}

// currentSpec is the live spec as the server reports it, keyed by GraphQL field.
type currentSpec map[string]interface{}

type specChange struct {
	Field    string
	Old, New interface{}
}

type specPlan struct {
	Slug    string
	Create  bool
	Changes []specChange
	Input   map[string]interface{}
}

func (p specPlan) noop() bool { return !p.Create && len(p.Changes) == 0 }

// desiredValue renders a file key as the value the GraphQL input expects.
func desiredValue(s applySpec, key string) interface{} {
	switch key {
	case "name":
		return s.Name
	case "agent_type":
		return s.AgentType
	case "image_tag":
		return s.ImageTag
	case "runtime":
		return s.Runtime
	case "tool_preset":
		return s.ToolPreset
	case "allow_install":
		return s.AllowInstall
	case "vnc":
		return s.VNC
	case "managed_model":
		return s.ManagedModel
	case "run_as_non_root":
		return s.RunAsNonRoot
	case "env":
		out := map[string]interface{}{}
		for k, v := range s.Env {
			out[k] = v
		}
		return out
	case "secrets":
		names := make([]string, 0, len(s.Secrets))
		for name := range s.Secrets {
			names = append(names, name)
		}
		sort.Strings(names)
		refs := make([]interface{}, 0, len(names))
		for _, name := range names {
			refs = append(refs, map[string]interface{}{"env_var": name, "uri": s.Secrets[name]})
		}
		return refs
	case "config_repo":
		return s.ConfigRepo
	case "config_branch":
		return s.ConfigBranch
	case "manifest_path":
		return s.ManifestPath
	}
	return nil
}

// normalizeRefs orders secret refs by env var so server order never reads
// as a change.
func normalizeRefs(v interface{}) interface{} {
	items, ok := v.([]interface{})
	if !ok {
		return v
	}
	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		ref, ok := item.(map[string]interface{})
		if !ok {
			return v
		}
		out = append(out, map[string]interface{}{"env_var": ref["env_var"], "uri": ref["uri"]})
	}
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i].(map[string]interface{})["env_var"]) < fmt.Sprint(out[j].(map[string]interface{})["env_var"])
	})
	return out
}

// planSpec compares the file's declared keys with the live spec.
func planSpec(s applySpec, defined map[string]bool, current currentSpec) specPlan {
	plan := specPlan{Slug: s.Slug, Create: current == nil, Input: map[string]interface{}{}}
	for _, f := range applyFields {
		if !defined[f.key] {
			continue
		}
		want := desiredValue(s, f.key)
		if plan.Create {
			plan.Input[f.field] = want
			continue
		}
		have := current[f.field]
		if f.key == "secrets" {
			have = normalizeRefs(have)
		}
		if f.key == "env" && have == nil {
			have = map[string]interface{}{}
		}
		if !reflect.DeepEqual(have, want) {
			plan.Changes = append(plan.Changes, specChange{Field: f.key, Old: have, New: want})
			plan.Input[f.field] = want
		}
	}
	return plan
}

func loadApplyFile(path string) ([]applySpec, []map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var shape applyFileShape
	meta, err := toml.Decode(string(raw), &shape)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		return nil, nil, fmt.Errorf("%s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	if len(shape.EnvSpec) == 0 {
		return nil, nil, fmt.Errorf("%s declares no [[env_spec]]", path)
	}
	seen := map[string]bool{}
	defined := make([]map[string]bool, len(shape.EnvSpec))
	for i, s := range shape.EnvSpec {
		if s.Slug == "" {
			return nil, nil, fmt.Errorf("%s: env_spec #%d has no slug", path, i+1)
		}
		if seen[s.Slug] {
			return nil, nil, fmt.Errorf("%s: env_spec %q is declared twice", path, s.Slug)
		}
		seen[s.Slug] = true
		defined[i] = map[string]bool{}
		for _, f := range applyFields {
			if isDefinedAt(meta, i, f.key) {
				defined[i][f.key] = true
			}
		}
	}
	return shape.EnvSpec, defined, nil
}

// isDefinedAt reports whether key was set in the i-th [[env_spec]] table.
// toml.MetaData.Keys lists array-of-table entries under the same path, in
// file order, so count occurrences of the table header to find table i.
func isDefinedAt(meta toml.MetaData, i int, key string) bool {
	table := -1
	for _, k := range meta.Keys() {
		if len(k) == 1 && k[0] == "env_spec" {
			table++
			continue
		}
		if table == i && len(k) >= 2 && k[0] == "env_spec" && k[1] == key {
			return true
		}
	}
	return false
}

const applySpecQuery = `query($slug:String!){ agentEnvironmentSpec(slug:$slug){
  slug name agentType imageTag runtime toolPreset allowInstall vncEnabled managedModel
  runAsNonRoot envVars secretRefs configRepo configBranch configManifestPath } }`

func fetchSpec(ctx context.Context, client *api.Client, slug string) (currentSpec, error) {
	var resp struct {
		AgentEnvironmentSpec map[string]interface{} `json:"agentEnvironmentSpec"`
	}
	if err := client.GraphQL(ctx, applySpecQuery, map[string]interface{}{"slug": slug}, &resp); err != nil {
		return nil, err
	}
	if resp.AgentEnvironmentSpec == nil {
		return nil, nil
	}
	return currentSpec(resp.AgentEnvironmentSpec), nil
}

func formatValue(v interface{}) string {
	if v == nil {
		return "(unset)"
	}
	return fmt.Sprintf("%v", v)
}

func runApply(
	cmd *cobra.Command,
	client *api.Client,
	resolveOrgID func() (string, error),
	path string,
	dryRun bool,
) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	out := cmd.OutOrStdout()
	specs, defined, err := loadApplyFile(path)
	if err != nil {
		return err
	}
	plans := make([]specPlan, 0, len(specs))
	for i, s := range specs {
		current, err := fetchSpec(ctx, client, s.Slug)
		if err != nil {
			return fmt.Errorf("reading env-spec %s: %w", s.Slug, err)
		}
		if current == nil && (!defined[i]["agent_type"] || s.AgentType == "") {
			return fmt.Errorf("env-spec %s does not exist; creating it needs agent_type", s.Slug)
		}
		plans = append(plans, planSpec(s, defined[i], current))
	}

	orgID := ""
	for _, p := range plans {
		switch {
		case p.Create:
			fmt.Fprintf(out, "create  env-spec %s\n", p.Slug)
		case p.noop():
			fmt.Fprintf(out, "unchanged env-spec %s\n", p.Slug)
			continue
		default:
			fmt.Fprintf(out, "update  env-spec %s\n", p.Slug)
			for _, c := range p.Changes {
				fmt.Fprintf(out, "  %s: %s -> %s\n", c.Field, formatValue(c.Old), formatValue(c.New))
			}
		}
		if dryRun {
			continue
		}
		var resp struct {
			Create specMutationResult `json:"createAgentEnvironmentSpec"`
			Update specMutationResult `json:"updateAgentEnvironmentSpec"`
		}
		if p.Create {
			if orgID == "" {
				if orgID, err = resolveOrgID(); err != nil {
					return err
				}
			}
			input := map[string]interface{}{"slug": p.Slug}
			for k, v := range p.Input {
				input[k] = v
			}
			if _, ok := input["name"]; !ok {
				input["name"] = p.Slug
			}
			if err := client.GraphQL(ctx, createSpecMutation,
				map[string]interface{}{"input": input, "orgId": orgID}, &resp); err != nil {
				return err
			}
			if err := specMutErr(resp.Create, "create"); err != nil {
				return fmt.Errorf("env-spec %s: %w", p.Slug, err)
			}
			continue
		}
		if err := client.GraphQL(ctx, updateSpecMutation,
			map[string]interface{}{"slug": p.Slug, "input": p.Input}, &resp); err != nil {
			return err
		}
		if err := specMutErr(resp.Update, "update"); err != nil {
			return fmt.Errorf("env-spec %s: %w", p.Slug, err)
		}
	}
	if dryRun {
		fmt.Fprintln(out, "dry run: nothing was changed")
	}
	return nil
}

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Declaratively create or update agent environment specs from a file",
	Long: `Reads [[env_spec]] tables from a TOML file and makes the platform match:
a missing spec is created, a spec that differs is updated field by field
(the diff is printed), and an unchanged spec is left alone, so a second
apply is a no-op. Only keys present in the file are managed.

  [[env_spec]]
  slug = "claude-dev"
  agent_type = "claude"
  runtime = "claude-code"
  run_as_non_root = true
  env = { LOG_LEVEL = "info" }
  secrets = { GITHUB_TOKEN = "agents/claude-dev/github-token" }

Example:
  astro apply -f agents.toml --dry-run
  astro apply -f agents.toml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		resolve := func() (string, error) {
			org, err := resolveOrg(cmd, cmd.Context(), client, cfg)
			if err != nil {
				return "", err
			}
			return org.ID, nil
		}
		return runApply(cmd, client, resolve, applyFile, applyDryRun)
	},
}

func init() {
	applyCmd.Flags().StringVarP(&applyFile, "file", "f", "", "TOML file with [[env_spec]] tables (required)")
	applyCmd.Flags().BoolVar(&applyDryRun, "dry-run", false, "Print the plan without changing anything")
	_ = applyCmd.MarkFlagRequired("file")
	rootCmd.AddCommand(applyCmd)
}
