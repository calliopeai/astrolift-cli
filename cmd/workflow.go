package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// workflow is the third workflow-authoring path (spec 40 §5.5): the CLI
// produces and round-trips the same declarative §5.4 manifest TOML the
// visual builder and GraphQL surface use. All server-touching commands route
// through the server's RBAC (WORKFLOW_*); the CLI carries no auth logic of
// its own. `push`/`register` is deferred with repo-registration (§8).

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Author and round-trip workflow manifests (spec 40 §5.5)",
	Long: `Produce and round-trip the declarative §5.4 workflow manifest TOML —
the third workflow-authoring path alongside the visual builder and the
GraphQL surface.

  init      scaffold a starter workflow.toml (client-side; no server)
  validate  check a manifest's shape locally, or --server for authoritative
            validation via previewWorkflowManifest (WORKFLOW_READ)
  pull      export an existing/global definition's TOML via
            exportWorkflowManifest (WORKFLOW_READ) — alias: export
  import    persist a manifest as an org definition (--preview to dry-run)

Tier-2 configured workflows (spec 40 §2/§3):

  definitions / definition / clone   browse and copy the visible catalogue
  list / create                      the org's configured Workflows
  run / runs                         start runs and watch them

` + "`astro workflow push`/`register` is deferred with repo-registration (spec 40 §8).",
}

// ---- §5.4 manifest shape ---------------------------------------------------

// workflowManifest is the §5.4 declarative workflow manifest: a [workflow]
// table plus an ordered array of [[stage]] tables (array index = stage order).
// Shapes mirror the backend serializer (workflows.manifest); the CLI does a
// local shape check and defers authoritative parsing to the server.
type workflowManifest struct {
	Workflow workflowDef     `toml:"workflow"`
	Stages   []workflowStage `toml:"stage"`
}

type workflowDef struct {
	Slug        string `toml:"slug"`
	Name        string `toml:"name"`
	Pattern     string `toml:"pattern"`
	Description string `toml:"description"`
}

type workflowStage struct {
	Kind      string      `toml:"kind"`
	Role      string      `toml:"role"`
	Agent     string      `toml:"agent"`
	Skills    []string    `toml:"skills"`
	OnFailure string      `toml:"on_failure"`
	Timeout   int         `toml:"timeout"`
	FanOut    interface{} `toml:"fan_out"` // tri-state: 0/N (int) or "dynamic"
	Prompt    string      `toml:"prompt"`
	Approvers []string    `toml:"approvers"`
}

// Valid value sets mirror the backend models (WorkflowDefinition.PatternKind,
// WorkflowStage.StageKind / OnFailure). Kept in lockstep with the server's
// authoritative validation, which remains the source of truth (--server).
var (
	workflowPatterns = map[string]bool{
		"single": true, "chained": true, "fan_out": true,
		"supervisor_worker": true, "review_loop": true, "advisor": true,
	}
	workflowStageKinds = map[string]bool{
		"agent_dispatch": true, "human_gate": true, "checkpoint": true, "aggregation": true,
	}
	workflowOnFailure = map[string]bool{
		"fail": true, "retry": true, "skip": true, "escalate": true,
	}
)

// validateWorkflowManifestShape does a local, server-free shape check. It is
// intentionally a subset of the server's parse_workflow_manifest — enough to
// catch obvious authoring mistakes offline; `validate --server` is authoritative.
func validateWorkflowManifestShape(m *workflowManifest) error {
	if m.Workflow.Slug == "" {
		return fmt.Errorf("[workflow] slug is required")
	}
	if m.Workflow.Name == "" {
		return fmt.Errorf("[workflow] name is required")
	}
	if m.Workflow.Pattern != "" && !workflowPatterns[m.Workflow.Pattern] {
		return fmt.Errorf("workflow.pattern %q is invalid (one of: chained, single, fan_out, supervisor_worker, review_loop, advisor)", m.Workflow.Pattern)
	}
	for i, s := range m.Stages {
		if s.Kind == "" {
			return fmt.Errorf("stage[%d].kind is required", i)
		}
		if !workflowStageKinds[s.Kind] {
			return fmt.Errorf("stage[%d].kind %q is invalid (one of: agent_dispatch, human_gate, checkpoint, aggregation)", i, s.Kind)
		}
		if s.OnFailure != "" && !workflowOnFailure[s.OnFailure] {
			return fmt.Errorf("stage[%d].on_failure %q is invalid (one of: fail, retry, skip, escalate)", i, s.OnFailure)
		}
		if fo, ok := s.FanOut.(string); ok && fo != "dynamic" {
			return fmt.Errorf("stage[%d].fan_out string must be \"dynamic\", got %q", i, fo)
		}
	}
	return nil
}

// ---- init ------------------------------------------------------------------

var (
	workflowInitPattern string
	workflowInitOut     string
)

var workflowInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold a starter workflow manifest TOML",
	Long: `Write a starter §5.4 workflow manifest for the chosen --pattern.

Patterns: chained (default), single, review_loop, fan_out, supervisor_worker.
This is a purely client-side template — it never contacts the server. Edit the
result, then run ` + "`astro workflow validate`" + ` (add --server for the
authoritative check).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		tmpl, ok := workflowTemplates[workflowInitPattern]
		if !ok {
			return fmt.Errorf("unknown --pattern %q (one of: chained, single, review_loop, fan_out, supervisor_worker)", workflowInitPattern)
		}
		if _, err := os.Stat(workflowInitOut); err == nil {
			return fmt.Errorf("%s already exists", workflowInitOut)
		}
		if err := os.WriteFile(workflowInitOut, []byte(tmpl), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", workflowInitOut, err)
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Created %s (pattern: %s)\n", workflowInitOut, workflowInitPattern)
		fmt.Fprintf(out, "Edit it, then run `astro workflow validate %s`.\n", workflowInitOut)
		return nil
	},
}

// ---- validate --------------------------------------------------------------

var workflowValidateServer bool

var workflowValidateCmd = &cobra.Command{
	Use:   "validate <file.toml>",
	Short: "Validate a workflow manifest (local shape; --server for authoritative)",
	Long: `Parse a §5.4 workflow manifest and check its shape.

Without flags this is a local, offline check (BurntSushi/toml parse + a shape
subset of the server's rules). With --server it runs the authoritative
validation via the previewWorkflowManifest GraphQL query (WORKFLOW_READ) and
prints the parsed structure plus any structured parse error.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkflowValidate(cmd, args[0])
	},
}

func runWorkflowValidate(cmd *cobra.Command, file string) error {
	raw, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("reading %s: %w", file, err)
	}
	if workflowValidateServer {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runWorkflowValidateServer(cmd, cmd.Context(), client, string(raw))
	}
	return runWorkflowValidateLocal(cmd, string(raw), file)
}

func runWorkflowValidateLocal(cmd *cobra.Command, content, file string) error {
	var m workflowManifest
	if _, err := toml.Decode(content, &m); err != nil {
		return fmt.Errorf("parsing %s: %w", file, err)
	}
	if err := validateWorkflowManifestShape(&m); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, &m)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "OK (local shape check): %s\n", file)
	fmt.Fprintf(out, "  workflow: %s (%s), pattern=%s, %d stage(s)\n",
		m.Workflow.Name, m.Workflow.Slug, defaultStr(m.Workflow.Pattern, "single"), len(m.Stages))
	for i, s := range m.Stages {
		fmt.Fprintf(out, "  [%d] %s %s\n", i, s.Kind, stageLabel(s))
	}
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Run with --server for authoritative validation.")
	return nil
}

const previewWorkflowManifestQuery = `query($toml: String!) {
  previewWorkflowManifest(toml: $toml) {
    ok
    error
    errorPath
    errorLine
    errorColumn
    definition { slug name pattern description }
    stages { order kind role agent skills onFailure timeout fanOut prompt approvers }
  }
}`

type workflowPreviewDef struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Pattern     string `json:"pattern"`
	Description string `json:"description"`
}

type workflowPreviewStage struct {
	Order     int      `json:"order"`
	Kind      string   `json:"kind"`
	Role      string   `json:"role"`
	Agent     *string  `json:"agent"`
	Skills    []string `json:"skills"`
	OnFailure string   `json:"onFailure"`
	Timeout   int      `json:"timeout"`
	FanOut    string   `json:"fanOut"`
	Prompt    *string  `json:"prompt"`
	Approvers []string `json:"approvers"`
}

type workflowManifestPreview struct {
	Ok          bool                   `json:"ok"`
	Error       *string                `json:"error"`
	ErrorPath   *string                `json:"errorPath"`
	ErrorLine   *int                   `json:"errorLine"`
	ErrorColumn *int                   `json:"errorColumn"`
	Definition  *workflowPreviewDef    `json:"definition"`
	Stages      []workflowPreviewStage `json:"stages"`
}

func runWorkflowValidateServer(cmd *cobra.Command, ctx context.Context, client *api.Client, content string) error {
	var resp struct {
		Preview workflowManifestPreview `json:"previewWorkflowManifest"`
	}
	vars := map[string]interface{}{"toml": content}
	if err := client.GraphQL(ctx, previewWorkflowManifestQuery, vars, &resp); err != nil {
		return fmt.Errorf("previewWorkflowManifest: %w", err)
	}
	p := resp.Preview
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, &p)
	}
	if !p.Ok {
		return fmt.Errorf("server validation failed%s: %s", previewErrorLocation(p), previewErrorMessage(p))
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "OK (server validation):")
	if p.Definition != nil {
		fmt.Fprintf(out, "  workflow: %s (%s), pattern=%s\n",
			p.Definition.Name, p.Definition.Slug, p.Definition.Pattern)
	}
	for _, s := range p.Stages {
		agent := ""
		if s.Agent != nil && *s.Agent != "" {
			agent = " agent=" + *s.Agent
		}
		fmt.Fprintf(out, "  [%d] %s role=%s%s on_failure=%s timeout=%d fan_out=%s\n",
			s.Order, s.Kind, s.Role, agent, s.OnFailure, s.Timeout, s.FanOut)
	}
	return nil
}

func previewErrorMessage(p workflowManifestPreview) string {
	if p.Error != nil && *p.Error != "" {
		return *p.Error
	}
	return "invalid manifest"
}

func previewErrorLocation(p workflowManifestPreview) string {
	loc := ""
	if p.ErrorPath != nil && *p.ErrorPath != "" {
		loc = " at " + *p.ErrorPath
	}
	if p.ErrorLine != nil {
		loc += fmt.Sprintf(" (line %d", *p.ErrorLine)
		if p.ErrorColumn != nil {
			loc += fmt.Sprintf(", col %d", *p.ErrorColumn)
		}
		loc += ")"
	}
	return loc
}

// ---- pull ------------------------------------------------------------------

var workflowPullOut string

var workflowPullCmd = &cobra.Command{
	Use:     "pull <slug>",
	Aliases: []string{"export"},
	Short:   "Export a workflow definition's TOML (exportWorkflowManifest)",
	Long: `Fetch a visible WorkflowDefinition — the caller's org UNION the global
catalogue — and emit its canonical §5.4 TOML, to stdout or to -o <file>.

This is the way to export a global/catalogue definition (e.g. ooda, rasd,
research) as a starting point for your own workflow. WORKFLOW_READ-gated
server-side. Pass --org <slug-or-id> to scope to a specific organization's
definitions (otherwise only globals are visible to a token).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		// Only scope to an org when one is explicitly requested; without it
		// the server returns the global catalogue, which is the common case
		// for "export a global as a starting point".
		if strings.TrimSpace(orgFlagValue(cmd)) != "" {
			org, err := resolveOrg(cmd, cmd.Context(), client, cfg)
			if err != nil {
				return err
			}
			client.SetOrg(org.ID)
		}
		return runWorkflowPull(cmd, cmd.Context(), client, args[0])
	},
}

const exportWorkflowManifestQuery = `query($slug: String!) {
  exportWorkflowManifest(definitionSlug: $slug) {
    ok
    toml
    error
  }
}`

func runWorkflowPull(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	var resp struct {
		Export struct {
			Ok    bool    `json:"ok"`
			Toml  *string `json:"toml"`
			Error *string `json:"error"`
		} `json:"exportWorkflowManifest"`
	}
	vars := map[string]interface{}{"slug": slug}
	if err := client.GraphQL(ctx, exportWorkflowManifestQuery, vars, &resp); err != nil {
		return fmt.Errorf("exportWorkflowManifest: %w", err)
	}
	e := resp.Export
	if !e.Ok {
		msg := fmt.Sprintf("no visible workflow definition with slug %q", slug)
		if e.Error != nil && *e.Error != "" {
			msg = *e.Error
		}
		return fmt.Errorf("pull failed: %s", msg)
	}
	body := ""
	if e.Toml != nil {
		body = *e.Toml
	}
	if workflowPullOut != "" {
		if err := os.WriteFile(workflowPullOut, []byte(body), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", workflowPullOut, err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s (%d bytes)\n", workflowPullOut, len(body))
		return nil
	}
	out := cmd.OutOrStdout()
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	fmt.Fprint(out, body)
	return nil
}

// ---- helpers ---------------------------------------------------------------

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func stageLabel(s workflowStage) string {
	parts := make([]string, 0, 2)
	if s.Role != "" {
		parts = append(parts, "role="+s.Role)
	}
	if s.Agent != "" {
		parts = append(parts, "agent="+s.Agent)
	}
	return strings.Join(parts, " ")
}

func init() {
	workflowInitCmd.Flags().StringVar(&workflowInitPattern, "pattern", "chained", "Starter pattern: chained, single, review_loop, fan_out, supervisor_worker")
	workflowInitCmd.Flags().StringVarP(&workflowInitOut, "out", "o", "workflow.toml", "Output file for the scaffolded manifest")

	workflowValidateCmd.Flags().BoolVar(&workflowValidateServer, "server", false, "Validate via the server (previewWorkflowManifest) instead of local shape checks")

	workflowPullCmd.Flags().StringVarP(&workflowPullOut, "out", "o", "", "Write the exported TOML to a file instead of stdout")

	workflowCmd.AddCommand(workflowInitCmd, workflowValidateCmd, workflowPullCmd)
	rootCmd.AddCommand(workflowCmd)
}
