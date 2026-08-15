// Package cmd — `astro workflow run-manifest`.
//
// Collapses the local→remote path for a locally-authored workflow manifest
// into one call. Today that path is three commands plus a GUID lookup per
// agent stage:
//
//	astro workflow import <file.toml>                     # persist as a definition
//	astro workflow create --definition <slug> \            # bind each agent stage
//	    --bind <order>=<agentWorkloadGuid> ...
//	astro workflow run <workflow-slug>                     # start it
//
// The binding step is the awkward one: the manifest already names the agent
// each dispatch stage wants, but --bind takes a workload GUID, so a caller
// had to resolve every name to a GUID out-of-band before it could run its
// own file.
//
// This imports the manifest, resolves each agent_dispatch stage's declared
// agent against the org's registered agent workloads, and runs the result.
// --bind still overrides any stage, so a manifest naming an agent that does
// not exist locally stays runnable.
//
// GraphQL operations (field names per backend/schema.graphql):
//   - importWorkflowManifest(toml, preview) → createdSlug + parsed manifest
//   - agentWorkloads(orgId)                 → name/slug → workload GUID
//   - createWorkflow(...)                   → the configured Workflow
//   - runWorkflow(workflowId, inputs)       → the run
//
// Issue: calliopeai/astrolift-cli#59
package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// agentDispatchStageKind is the stage kind that requires an agent binding.
const agentDispatchStageKind = "agent_dispatch"

// ---- flags -----------------------------------------------------------------

var (
	runManifestName    string
	runManifestSlug    string
	runManifestBinds   []string
	runManifestInputs  []string
	runManifestDryRun  bool
	runManifestNoRun   bool
	runManifestTrigger string
)

// ---- astro workflow run-manifest -------------------------------------------

var workflowRunManifestCmd = &cobra.Command{
	Use:   "run-manifest <file.toml>",
	Short: "Import, bind, and run a local workflow manifest in one call",
	Long: `Imports a §5.4 workflow manifest, binds every agent_dispatch stage to
its declared agent, and starts a run — the one-call form of
'workflow import' + 'workflow create --bind ...' + 'workflow run'.

Each agent_dispatch stage's declared agent is resolved against the org's
registered agent workloads by slug, then by name. Override any stage with
--bind <stageOrder>=<agentWorkloadGuid>, which also covers a stage whose
manifest names an agent that is not registered here.

Use --dry-run to see the resolved bindings without importing anything, and
--no-run to import and configure but not start a run.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		org, err := resolveOrg(cmd, cmd.Context(), client, cfg)
		if err != nil {
			return err
		}
		client.SetOrg(org.ID)
		raw, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("reading %s: %w", args[0], err)
		}
		return runWorkflowRunManifest(cmd, cmd.Context(), client, cfg, org.ID, string(raw))
	},
}

// stageBinding records how one agent_dispatch stage got its agent, so the
// summary can show whether a binding came from the manifest or an override.
type stageBinding struct {
	Order  int
	Agent  string
	GUID   string
	Source string
}

func runWorkflowRunManifest(
	cmd *cobra.Command, ctx context.Context, client *api.Client,
	cfg *config.Config, orgID string, content string,
) error {
	overrides, err := parseStageBindings(runManifestBinds)
	if err != nil {
		return err
	}
	inputs, err := parseKeyValues("--input", runManifestInputs)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	// Preview first, always. On a dry run this is the whole job; otherwise it
	// validates the manifest and yields the stage list needed to resolve
	// bindings before anything is persisted.
	preview, err := importManifest(ctx, client, content, true)
	if err != nil {
		return err
	}
	if preview.Definition == nil {
		return fmt.Errorf("manifest preview returned no definition")
	}

	bindings, err := resolveStageBindings(ctx, client, orgID, preview.Stages, overrides)
	if err != nil {
		return err
	}

	if runManifestDryRun {
		fmt.Fprintf(out, "Manifest OK: %s (%s), %d stage(s) — nothing persisted.\n\n",
			preview.Definition.Name, preview.Definition.Slug, len(preview.Stages))
		printStageBindings(out, bindings)
		return nil
	}

	// Persist the definition.
	imported, err := importManifest(ctx, client, content, false)
	if err != nil {
		return err
	}
	if imported.CreatedSlug == "" {
		return fmt.Errorf("import succeeded but returned no definition slug")
	}
	fmt.Fprintf(out, "Imported definition: %s\n", imported.CreatedSlug)

	name := runManifestName
	if name == "" {
		name = preview.Definition.Name
	}

	wf, err := createConfiguredWorkflow(ctx, client, name, imported.CreatedSlug, runManifestSlug,
		bindingsToStagePayload(bindings), inputs, runManifestTrigger)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Configured workflow:  %s (%s)\n", wf.Name, wf.Slug)
	if len(bindings) > 0 {
		fmt.Fprintln(out)
		printStageBindings(out, bindings)
	}

	if runManifestNoRun {
		fmt.Fprintf(out, "\nRun it with `astro workflow run %s`.\n", wf.Slug)
		return nil
	}

	runID, workflowRunID, err := startConfiguredWorkflow(ctx, client, wf.GUID, inputs)
	if err != nil {
		return err
	}
	fmt.Fprintln(out)
	if runID != "" {
		fmt.Fprintf(out, "Run ID:         %s\n", runID)
	}
	if workflowRunID != "" {
		fmt.Fprintf(out, "WorkflowRun ID: %s\n", workflowRunID)
	}
	fmt.Fprintf(out, "\nWatch it with `astro workflow runs %s --watch`.\n", wf.Slug)
	return nil
}

// ---- steps -----------------------------------------------------------------

type manifestImport struct {
	CreatedSlug string
	Definition  *workflowPreviewDef
	Stages      []workflowPreviewStage
}

func importManifest(ctx context.Context, client *api.Client, content string, preview bool) (*manifestImport, error) {
	impCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Result struct {
			Ok          bool             `json:"ok"`
			Errors      validationErrors `json:"errors"`
			CreatedSlug *string          `json:"createdSlug"`
			Manifest    *struct {
				Definition *workflowPreviewDef    `json:"definition"`
				Stages     []workflowPreviewStage `json:"stages"`
			} `json:"manifest"`
		} `json:"importWorkflowManifest"`
	}
	vars := map[string]interface{}{"toml": content, "preview": preview}
	if err := client.GraphQL(impCtx, importWorkflowManifestMutation, vars, &resp); err != nil {
		return nil, fmt.Errorf("importing manifest: %w", err)
	}
	if !resp.Result.Ok {
		verb := "import"
		if preview {
			verb = "validation"
		}
		return nil, fmt.Errorf("manifest %s failed: %s", verb, firstValidationError(resp.Result.Errors))
	}

	res := &manifestImport{}
	if resp.Result.CreatedSlug != nil {
		res.CreatedSlug = *resp.Result.CreatedSlug
	}
	if m := resp.Result.Manifest; m != nil {
		res.Definition, res.Stages = m.Definition, m.Stages
	}
	return res, nil
}

// resolveStageBindings maps every agent_dispatch stage to an agent workload
// GUID. An explicit --bind wins; otherwise the stage's declared agent is
// matched against the org's registered workloads by slug, then by name.
//
// The workload list is fetched only when some stage actually needs it, so a
// manifest that binds everything explicitly (or has no agent stages) costs
// no extra round trip.
func resolveStageBindings(
	ctx context.Context, client *api.Client, orgID string,
	stages []workflowPreviewStage, overrides map[string]interface{},
) ([]stageBinding, error) {
	needsLookup := false
	for _, st := range stages {
		if st.Kind != agentDispatchStageKind {
			continue
		}
		if _, overridden := overrides[strconv.Itoa(st.Order)]; !overridden {
			needsLookup = true
			break
		}
	}

	var workloads []agentWorkload
	if needsLookup {
		var err error
		if workloads, err = listAgentWorkloads(ctx, client, orgID); err != nil {
			return nil, err
		}
	}
	return resolveBindingsWithWorkloads(stages, overrides, workloads)
}

// resolveBindingsWithWorkloads is the resolution itself, separated from the
// fetch so the binding rules can be exercised without a control plane.
func resolveBindingsWithWorkloads(
	stages []workflowPreviewStage, overrides map[string]interface{}, workloads []agentWorkload,
) ([]stageBinding, error) {
	var bindings []stageBinding

	for _, st := range stages {
		if st.Kind != agentDispatchStageKind {
			continue
		}
		key := strconv.Itoa(st.Order)

		if ov, ok := overrides[key]; ok {
			guid := ""
			if m, ok := ov.(map[string]interface{}); ok {
				guid, _ = m["agent_workload_id"].(string)
			}
			bindings = append(bindings, stageBinding{
				Order: st.Order, Agent: stageAgentName(st), GUID: guid, Source: "--bind",
			})
			continue
		}

		declared := stageAgentName(st)
		if declared == "" {
			return nil, fmt.Errorf(
				"stage %d is an %s stage but names no agent; bind it with --bind %d=<agentWorkloadGuid>",
				st.Order, agentDispatchStageKind, st.Order)
		}

		guid, err := matchAgentWorkload(declared, workloads, st.Order)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, stageBinding{
			Order: st.Order, Agent: declared, GUID: guid, Source: "manifest",
		})
	}

	// An override for a stage the manifest does not have is almost always a
	// typo'd order; failing beats silently configuring the wrong workflow.
	agentStages := make(map[string]bool, len(stages))
	for _, st := range stages {
		if st.Kind == agentDispatchStageKind {
			agentStages[strconv.Itoa(st.Order)] = true
		}
	}
	var stray []string
	for key := range overrides {
		if !agentStages[key] {
			stray = append(stray, key)
		}
	}
	if len(stray) > 0 {
		sort.Strings(stray)
		return nil, fmt.Errorf("--bind given for stage(s) %s, which are not %s stages in this manifest",
			strings.Join(stray, ", "), agentDispatchStageKind)
	}

	return bindings, nil
}

func stageAgentName(st workflowPreviewStage) string {
	if st.Agent == nil {
		return ""
	}
	return strings.TrimSpace(*st.Agent)
}

func listAgentWorkloads(ctx context.Context, client *api.Client, orgID string) ([]agentWorkload, error) {
	lsCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		AgentWorkloads []agentWorkload `json:"agentWorkloads"`
	}
	if err := client.GraphQL(lsCtx, agentWorkloadsQuery, map[string]interface{}{"orgId": orgID}, &resp); err != nil {
		return nil, fmt.Errorf("listing agent workloads: %w", err)
	}
	return resp.AgentWorkloads, nil
}

// matchAgentWorkload resolves a manifest's agent reference to a workload
// GUID, preferring an exact slug match over a name match. An unresolvable
// reference lists the registered agents, since the usual cause is a manifest
// written against a different org.
func matchAgentWorkload(declared string, workloads []agentWorkload, order int) (string, error) {
	for _, w := range workloads {
		if w.Slug == declared {
			return w.ID, nil
		}
	}
	for _, w := range workloads {
		if strings.EqualFold(w.Name, declared) {
			return w.ID, nil
		}
	}

	available := make([]string, 0, len(workloads))
	for _, w := range workloads {
		available = append(available, w.Slug)
	}
	sort.Strings(available)
	hint := strings.Join(available, ", ")
	if hint == "" {
		hint = "none registered — see `astro agent register-repo`"
	}
	return "", fmt.Errorf(
		"stage %d names agent %q, which is not a registered agent workload in this org (available: %s); "+
			"override with --bind %d=<agentWorkloadGuid>",
		order, declared, hint, order)
}

func bindingsToStagePayload(bindings []stageBinding) map[string]interface{} {
	if len(bindings) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(bindings))
	for _, b := range bindings {
		out[strconv.Itoa(b.Order)] = map[string]interface{}{"agent_workload_id": b.GUID}
	}
	return out
}

func createConfiguredWorkflow(
	ctx context.Context, client *api.Client,
	name, definitionSlug, slug string,
	stageBindings map[string]interface{}, inputs map[string]interface{}, trigger string,
) (*configuredWorkflow, error) {
	createCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	vars := map[string]interface{}{
		"name":           name,
		"definitionSlug": definitionSlug,
		"triggerKind":    trigger,
	}
	if slug != "" {
		vars["slug"] = slug
	}
	if stageBindings != nil {
		vars["stageBindings"] = stageBindings
	}
	if inputs != nil {
		vars["inputs"] = inputs
	}

	var resp struct {
		Result struct {
			Ok       bool                `json:"ok"`
			Errors   validationErrors    `json:"errors"`
			Workflow *configuredWorkflow `json:"workflow"`
		} `json:"createWorkflow"`
	}
	if err := client.GraphQL(createCtx, createWorkflowMutation, vars, &resp); err != nil {
		return nil, fmt.Errorf("creating workflow: %w", err)
	}
	if !resp.Result.Ok {
		return nil, fmt.Errorf("create failed: %s", firstValidationError(resp.Result.Errors))
	}
	if resp.Result.Workflow == nil {
		return nil, fmt.Errorf("create succeeded but returned no workflow")
	}
	return resp.Result.Workflow, nil
}

func startConfiguredWorkflow(
	ctx context.Context, client *api.Client, workflowGUID string, inputs map[string]interface{},
) (string, string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	vars := map[string]interface{}{"workflowId": workflowGUID}
	if inputs != nil {
		vars["inputs"] = inputs
	}

	var resp struct {
		Result struct {
			Ok            bool             `json:"ok"`
			Errors        validationErrors `json:"errors"`
			RunID         *string          `json:"runId"`
			WorkflowRunID *string          `json:"workflowRunId"`
		} `json:"runWorkflow"`
	}
	if err := client.GraphQL(runCtx, runConfiguredWorkflowMutation, vars, &resp); err != nil {
		return "", "", fmt.Errorf("running workflow: %w", err)
	}
	if !resp.Result.Ok {
		return "", "", fmt.Errorf("run failed: %s", firstValidationError(resp.Result.Errors))
	}
	runID, workflowRunID := "", ""
	if resp.Result.RunID != nil {
		runID = *resp.Result.RunID
	}
	if resp.Result.WorkflowRunID != nil {
		workflowRunID = *resp.Result.WorkflowRunID
	}
	return runID, workflowRunID, nil
}

func printStageBindings(out interface{ Write([]byte) (int, error) }, bindings []stageBinding) {
	if len(bindings) == 0 {
		fmt.Fprintln(out, "No agent_dispatch stages to bind.")
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STAGE\tAGENT\tWORKLOAD GUID\tBOUND BY")
	for _, b := range bindings {
		agent := b.Agent
		if agent == "" {
			agent = "-"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", b.Order, agent, b.GUID, b.Source)
	}
	_ = w.Flush()
}

// ---- init ------------------------------------------------------------------

func init() {
	workflowRunManifestCmd.Flags().StringVar(&runManifestName, "name", "", "Configured workflow name (defaults to the manifest's name)")
	workflowRunManifestCmd.Flags().StringVar(&runManifestSlug, "slug", "", "Configured workflow slug (server-generated when omitted)")
	workflowRunManifestCmd.Flags().StringArrayVar(&runManifestBinds, "bind", nil, "Override a stage's agent: <stageOrder>=<agentWorkloadGuid> (repeatable)")
	workflowRunManifestCmd.Flags().StringArrayVar(&runManifestInputs, "input", nil, "Workflow input: <key>=<value> (repeatable)")
	workflowRunManifestCmd.Flags().StringVar(&runManifestTrigger, "trigger", "manual", "Trigger kind for the configured workflow")
	workflowRunManifestCmd.Flags().BoolVar(&runManifestDryRun, "dry-run", false, "Validate and show resolved bindings without persisting anything")
	workflowRunManifestCmd.Flags().BoolVar(&runManifestNoRun, "no-run", false, "Import and configure, but do not start a run")

	workflowCmd.AddCommand(workflowRunManifestCmd)
}
