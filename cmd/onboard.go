package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Every piece an agent needs to work against an install already existed:
// the MCP gateway at /api/mcp/v1/, `alft_at_` API tokens that authenticate
// both it and this CLI, a skills catalogue, and offline guides in this
// binary. Nothing assembled them, so onboarding meant reading three
// references and hand-editing JSON, and an agent had no way to discover any
// of it. This command is that assembly and nothing more -- it introduces no
// new credential type and talks to no network.

const (
	componentMCP    = "mcp"
	componentSkills = "skills"
	componentDocs   = "docs"
)

var onboardComponents = []string{componentMCP, componentSkills, componentDocs}

// Targets are the agents whose config layout differs. "both" is the common
// case on a developer machine where Claude Code and Codex share a checkout.
var onboardTargets = []string{"claude", "codex", "both"}

var (
	onboardOnly   []string
	onboardTarget string
	onboardDir    string
	onboardDryRun bool
	onboardForce  bool
)

// mcpServerName is the key under "mcpServers". Matches the MCP contract's
// SERVER_NAME so tool names an agent sees line up with the reference docs.
const mcpServerName = "astrolift"

var onboardCmd = &cobra.Command{
	Use:   "onboard",
	Short: "Set an AI agent up against this install: MCP config, skills, and offline docs",
	Long: `Wire an AI coding agent (Claude Code, Codex, or any MCP client) up to
this Astrolift install.

Writes, for whichever components you select:

  mcp     an MCP server entry pointing at this install's gateway, with the
          API URL filled in from your current server
  skills  the Astrolift skill catalogue bundled into this binary
  docs    the offline guides, as agent-readable context

Selection: everything by default. --only narrows it, and --dry-run reports
what would be written without touching the filesystem. --json makes the
whole report machine-readable, so an agent can run this itself and read the
result rather than parsing prose.

Authentication is reported, never performed. Two ways in:

  headless   export ASTROLIFT_TOKEN=alft_at_...   (mint one under Settings
             > API tokens; the same token authenticates this CLI and the
             MCP gateway, so an agent needs nothing else)
  browser    astro auth login                     (device flow; an agent
             with no browser can relay the printed URL to a human, or use
             --json to hand off the URL and code as structured output)`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		selected, err := resolveOnboardComponents(onboardOnly)
		if err != nil {
			return err
		}
		target, err := resolveOnboardTarget(onboardTarget)
		if err != nil {
			return err
		}

		dir := onboardDir
		if dir == "" {
			dir, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("resolving working directory: %w", err)
			}
		}

		report := onboardReport{
			Directory:  dir,
			Target:     target,
			Components: selected,
			DryRun:     onboardDryRun,
		}

		// Auth is reported first because it is the only step this command
		// cannot do for you, and the one that makes the rest work.
		report.Auth = describeAuthState()

		apiURL, err := onboardAPIURL()
		if err != nil && contains(selected, componentMCP) {
			// An MCP entry without a URL is worse than no entry: the agent
			// gets a server it can never reach and a confusing error later.
			return err
		}
		report.APIURL = apiURL

		for _, component := range selected {
			switch component {
			case componentMCP:
				written, err := writeMCPConfig(dir, target, apiURL, onboardDryRun, onboardForce)
				if err != nil {
					return err
				}
				report.Written = append(report.Written, written...)
			case componentSkills:
				written, err := installSkills(dir, target, onboardDryRun, onboardForce)
				if err != nil {
					return err
				}
				report.Written = append(report.Written, written...)
			case componentDocs:
				written, err := installAgentDocs(dir, onboardDryRun, onboardForce)
				if err != nil {
					return err
				}
				report.Written = append(report.Written, written...)
			}
		}

		if viper.GetBool("output_json") {
			return renderJSON(cmd, report)
		}
		return report.render(cmd)
	},
}

type onboardReport struct {
	Directory  string      `json:"directory"`
	Target     string      `json:"target"`
	Components []string    `json:"components"`
	DryRun     bool        `json:"dry_run"`
	APIURL     string      `json:"api_url"`
	Auth       authState   `json:"auth"`
	Written    []writtenFn `json:"written"`
}

// writtenFn is one filesystem outcome. Skipped entries carry a reason so a
// --dry-run report and a real run read the same way.
type writtenFn struct {
	Path    string `json:"path"`
	Action  string `json:"action"` // "write" | "skip"
	Detail  string `json:"detail,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
}

type authState struct {
	// Authenticated is a statement about configuration, not a probe: this
	// command makes no network call, so it reports whether a credential is
	// present, never whether the server would accept it.
	Authenticated bool   `json:"authenticated"`
	Source        string `json:"source"`
	Hint          string `json:"hint,omitempty"`
	Server        string `json:"server,omitempty"`
}

func describeAuthState() authState {
	if token := strings.TrimSpace(viper.GetString("token")); token != "" {
		return authState{Authenticated: true, Source: "ASTROLIFT_TOKEN or --token"}
	}
	if token := strings.TrimSpace(os.Getenv("ASTROLIFT_DEPLOY_TOKEN")); token != "" {
		return authState{
			Authenticated: true,
			Source:        "ASTROLIFT_DEPLOY_TOKEN",
			Hint: "a deploy token is app-scoped; an agent doing org-level work " +
				"wants an API token in ASTROLIFT_TOKEN instead",
		}
	}
	cfg, err := config.Load()
	if err != nil || (cfg.CurrentServer == "" && strings.TrimSpace(viper.GetString("server")) == "") {
		return authState{
			Source: "none",
			Hint: "run `astro server add <slug> <api-url>`, then either " +
				"export ASTROLIFT_TOKEN=alft_at_... or run `astro auth login`",
		}
	}
	serverSlug, _, err := selectedServer(cfg, nil)
	if err != nil {
		return authState{Source: "none", Hint: err.Error()}
	}
	if _, err := config.LoadCredentials(serverSlug); err != nil {
		return authState{
			Source: "none",
			Server: serverSlug,
			Hint: "export ASTROLIFT_TOKEN=alft_at_... for an unattended agent, " +
				"or run `astro auth login` for a browser device flow",
		}
	}
	return authState{Authenticated: true, Source: "stored credentials", Server: serverSlug}
}

// onboardAPIURL resolves the install's API URL the same way every other
// command does, so the MCP entry points where the CLI already points.
func onboardAPIURL() (string, error) {
	if override := strings.TrimSpace(viper.GetString("api_url")); override != "" && strings.TrimSpace(viper.GetString("server")) == "" {
		return override, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	if cfg.CurrentServer == "" && strings.TrimSpace(viper.GetString("server")) == "" {
		return "", errors.New(
			"no current server, so there is no API URL to point an MCP client at. " +
				"run `astro server add <slug> <api-url>` first, or pass --api-url",
		)
	}
	_, entry, err := selectedServer(cfg, nil)
	if err != nil {
		return "", err
	}
	return entry.APIURL, nil
}

func resolveOnboardComponents(only []string) ([]string, error) {
	if len(only) == 0 {
		return append([]string(nil), onboardComponents...), nil
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range only {
		for _, piece := range strings.Split(raw, ",") {
			name := strings.ToLower(strings.TrimSpace(piece))
			if name == "" {
				continue
			}
			if !contains(onboardComponents, name) {
				return nil, fmt.Errorf(
					"unknown component %q; pick from %s",
					name, strings.Join(onboardComponents, ", "),
				)
			}
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("--only was given with no components")
	}
	// Stable order regardless of how they were typed, so reports diff.
	sort.Slice(out, func(i, j int) bool {
		return indexOf(onboardComponents, out[i]) < indexOf(onboardComponents, out[j])
	})
	return out, nil
}

func resolveOnboardTarget(target string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(target))
	if name == "" {
		return "both", nil
	}
	if !contains(onboardTargets, name) {
		return "", fmt.Errorf(
			"unknown target %q; pick from %s",
			name, strings.Join(onboardTargets, ", "),
		)
	}
	return name, nil
}

func (r onboardReport) render(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	if r.DryRun {
		fmt.Fprintf(out, "Dry run: nothing written.\n\n")
	}

	if r.Auth.Authenticated {
		fmt.Fprintf(out, "Auth: %s\n", r.Auth.Source)
	} else {
		fmt.Fprintf(out, "Auth: NOT configured\n")
	}
	if r.Auth.Hint != "" {
		fmt.Fprintf(out, "      %s\n", r.Auth.Hint)
	}
	if r.APIURL != "" {
		fmt.Fprintf(out, "Install: %s\n", r.APIURL)
	}
	fmt.Fprintln(out)

	for _, w := range r.Written {
		verb := "wrote"
		if r.DryRun {
			verb = "would write"
		}
		if w.Skipped {
			verb = "skipped"
		}
		fmt.Fprintf(out, "  %-12s %s", verb, w.Path)
		if w.Detail != "" {
			fmt.Fprintf(out, "  (%s)", w.Detail)
		}
		fmt.Fprintln(out)
	}

	if !r.Auth.Authenticated {
		fmt.Fprintf(out, "\nNext: authenticate, then re-run to confirm.\n")
	}
	return nil
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func indexOf(haystack []string, needle string) int {
	for i, item := range haystack {
		if item == needle {
			return i
		}
	}
	return len(haystack)
}

// writeFileReport writes body unless the path exists and force is unset,
// and never writes at all on a dry run. Centralised so every component
// reports identically -- a component that quietly clobbered an operator's
// config would be the worst possible bug in an onboarding tool.
func writeFileReport(target string, body []byte, dryRun, force bool) (writtenFn, error) {
	if _, err := os.Stat(target); err == nil && !force {
		return writtenFn{
			Path:    target,
			Action:  "skip",
			Skipped: true,
			Detail:  "exists; pass --force to replace",
		}, nil
	} else if err != nil && !os.IsNotExist(err) {
		return writtenFn{}, fmt.Errorf("checking %s: %w", target, err)
	}

	if dryRun {
		return writtenFn{Path: target, Action: "write"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return writtenFn{}, fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, body, 0o644); err != nil {
		return writtenFn{}, fmt.Errorf("writing %s: %w", target, err)
	}
	return writtenFn{Path: target, Action: "write"}, nil
}

func init() {
	onboardCmd.Flags().StringSliceVar(&onboardOnly, "only", nil,
		"components to set up: "+strings.Join(onboardComponents, ", ")+" (default: all)")
	onboardCmd.Flags().StringVar(&onboardTarget, "target", "both",
		"agent layout to write for: "+strings.Join(onboardTargets, ", "))
	onboardCmd.Flags().StringVar(&onboardDir, "dir", "",
		"directory to set up (default: the working directory)")
	onboardCmd.Flags().BoolVar(&onboardDryRun, "dry-run", false,
		"report what would be written without writing it")
	onboardCmd.Flags().BoolVar(&onboardForce, "force", false,
		"replace files that already exist")
	rootCmd.AddCommand(onboardCmd)
}
