// Package cmd — `astro workflow` tier-2 configured-workflow surface (spec 40).
//
// Extends the manifest-authoring group (workflow.go: init/validate/pull) with
// the catalogue + configured-workflow + run commands:
//   - definitions — list visible WorkflowDefinitions (org UNION global)
//   - definition  — one definition + its stages
//   - clone       — copy a global/catalogue definition into the org
//   - list        — the org's configured Workflows (tier 2)
//   - create      — configure a Workflow from a visible definition
//   - run         — start a configured Workflow's run (tier 3)
//   - runs        — list a Workflow's runs; --watch polls until terminal
//   - import      — importWorkflowManifest (--preview to validate only)
//   - delete      — soft-delete a configured Workflow (+ schedule teardown)
//   - definition-delete — soft-delete an org-owned WorkflowDefinition
//
// GraphQL operations (field names per backend/schema.graphql):
//   - definitions → workflowDefinitions → [WorkflowDefinitionSummary]
//   - definition  → workflowDefinition(slug) + workflowStages(workflowSlug)
//   - clone       → cloneWorkflowDefinition(slug) → { ok, errors, slug }
//   - list        → workflows → [ConfiguredWorkflow]
//   - create      → createWorkflow(...) → { ok, errors, workflow }
//   - run         → workflow(slug) then runWorkflow(workflowId, inputs)
//   - runs        → workflow(slug) then workflowRuns(workflowId)
//   - import      → importWorkflowManifest(toml, preview)
//   - delete      → deleteWorkflow(slug) → MutationResult
//   - definition-delete → deleteWorkflowDefinition(slug) → MutationResult
//
// All commands are org-scoped: the working org (resolveOrg) is sent as the
// X-Astrolift-Organization header, which the tenant middleware resolves into
// the caller context every resolver filters on.
//
// stage_bindings shape (workflows.models.Workflow, validated on save/run):
//
//	{"<stageOrder>": {"agent_workload_id": "<guid>"}}
//
// keys are stage orders as strings; the model looks up str(order) first.
package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

var (
	workflowDefsGlobal bool
	workflowDefsOrg    bool

	workflowCreateDefinition string
	workflowCreateName       string
	workflowCreateSlug       string
	workflowCreateBinds      []string
	workflowCreateInputs     []string
	workflowCreateTrigger    string
	workflowCreateCron       string

	workflowRunConfigInputs []string

	workflowRunsWatch bool

	workflowImportPreview bool

	workflowDeleteYes    bool
	workflowDefDeleteYes bool
)

// workflowRunsPollIntervalForTest is the cadence --watch re-queries
// workflowRuns. A var (not a const) so tests can shorten it.
var workflowRunsPollIntervalForTest = 5 * time.Second

// ---- GraphQL operations ----------------------------------------------------

const workflowDefinitionsQuery = `query {
  workflowDefinitions {
    guid name slug patternKind isEnabled isGlobal organizationGuid stageCount
  }
}`

// workflowDefinitionDetailQuery fetches the summary and the stage list in one
// round trip; both resolvers key on the same slug.
const workflowDefinitionDetailQuery = `query($slug: String!) {
  workflowDefinition(slug: $slug) {
    guid name slug description patternKind isEnabled isGlobal organizationGuid stageCount
  }
  workflowStages(workflowSlug: $slug) {
    order kind onFailure timeoutSeconds fanOutCount agentDefinitionName
  }
}`

const cloneWorkflowDefinitionMutation = `mutation($slug: String!) {
  cloneWorkflowDefinition(slug: $slug) {
    ok
    errors { field messages }
    slug
  }
}`

const workflowsListQuery = `query {
  workflows {
    guid name slug definitionSlug triggerKind isEnabled runCount
  }
}`

// workflowBySlugQuery resolves a configured Workflow's guid for the commands
// that take a slug but call an id-keyed resolver (run, runs).
const workflowBySlugQuery = `query($slug: String!) {
  workflow(slug: $slug) {
    guid name slug
  }
}`

const createWorkflowMutation = `mutation($name: String!, $definitionSlug: String!, $slug: String, $stageBindings: JSON, $inputs: JSON, $triggerKind: String!, $scheduleCron: String) {
  createWorkflow(name: $name, definitionSlug: $definitionSlug, slug: $slug, stageBindings: $stageBindings, inputs: $inputs, triggerKind: $triggerKind, scheduleCron: $scheduleCron) {
    ok
    errors { field messages }
    workflow { guid name slug triggerKind isEnabled }
  }
}`

const runConfiguredWorkflowMutation = `mutation($workflowId: ID!, $inputs: JSON) {
  runWorkflow(workflowId: $workflowId, inputs: $inputs) {
    ok
    errors { field messages }
    runId
    workflowRunId
  }
}`

const workflowRunsQuery = `query($workflowId: ID!) {
  workflowRuns(workflowId: $workflowId) {
    guid currentState temporalWorkflowId startedAt completedAt isCompleted
  }
}`

const importWorkflowManifestMutation = `mutation($toml: String!, $preview: Boolean!) {
  importWorkflowManifest(toml: $toml, preview: $preview) {
    ok
    errors { field messages }
    createdSlug
    manifest {
      definition { slug name pattern description }
      stages { order kind role agent skills onFailure timeout fanOut prompt approvers }
    }
  }
}`

const deleteWorkflowMutation = `mutation($slug: String!) {
  deleteWorkflow(slug: $slug) {
    ok
    errors { field messages }
  }
}`

const deleteWorkflowDefinitionMutation = `mutation($slug: String!) {
  deleteWorkflowDefinition(slug: $slug) {
    ok
    errors { field messages }
  }
}`

// ---- response shapes (GraphQL camelCase) -----------------------------------

// validationErrors is the ValidationError list every workflow MutationResult
// envelope carries; its underlying type matches firstValidationError's param.
type validationErrors = []struct {
	Field    string   `json:"field"`
	Messages []string `json:"messages"`
}

type workflowDefSummary struct {
	GUID             string  `json:"guid"`
	Name             string  `json:"name"`
	Slug             string  `json:"slug"`
	Description      string  `json:"description"`
	PatternKind      string  `json:"patternKind"`
	IsEnabled        bool    `json:"isEnabled"`
	IsGlobal         bool    `json:"isGlobal"`
	OrganizationGUID *string `json:"organizationGuid"`
	StageCount       int     `json:"stageCount"`
}

// workflowStageRow mirrors WorkflowStageType. The deployed schema exposes no
// stage role — the agent binding name is the human-readable label.
type workflowStageRow struct {
	Order               int     `json:"order"`
	Kind                string  `json:"kind"`
	OnFailure           string  `json:"onFailure"`
	TimeoutSeconds      int     `json:"timeoutSeconds"`
	FanOutCount         *int    `json:"fanOutCount"`
	AgentDefinitionName *string `json:"agentDefinitionName"`
}

type configuredWorkflow struct {
	GUID           string `json:"guid"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	DefinitionSlug string `json:"definitionSlug"`
	TriggerKind    string `json:"triggerKind"`
	IsEnabled      bool   `json:"isEnabled"`
	RunCount       int    `json:"runCount"`
}

type configuredWorkflowRun struct {
	GUID               string  `json:"guid"`
	CurrentState       string  `json:"currentState"`
	TemporalWorkflowID *string `json:"temporalWorkflowId"`
	StartedAt          string  `json:"startedAt"`
	CompletedAt        *string `json:"completedAt"`
	IsCompleted        bool    `json:"isCompleted"`
}

// ---- astro workflow definitions ---------------------------------------------

var workflowDefinitionsCmd = &cobra.Command{
	Use:   "definitions",
	Short: "List visible workflow definitions (org UNION global catalogue)",
	Long: `Lists the WorkflowDefinitions visible to the working org via the
workflowDefinitions GraphQL query: the org's own plus the platform-global
catalogue (organizationGuid null = platform template). --global / --org
narrow to one scope.`,
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
		return runWorkflowDefinitions(cmd, ctx, client)
	}),
}

func runWorkflowDefinitions(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	if workflowDefsGlobal && workflowDefsOrg {
		return fmt.Errorf("--global and --org-only are mutually exclusive")
	}

	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Definitions []workflowDefSummary `json:"workflowDefinitions"`
	}
	if err := client.GraphQL(listCtx, workflowDefinitionsQuery, nil, &resp); err != nil {
		return fmt.Errorf("listing definitions: %w", err)
	}

	defs := make([]workflowDefSummary, 0, len(resp.Definitions))
	for _, d := range resp.Definitions {
		if workflowDefsGlobal && !d.IsGlobal {
			continue
		}
		if workflowDefsOrg && d.IsGlobal {
			continue
		}
		defs = append(defs, d)
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, defs)
	}

	out := cmd.OutOrStdout()
	if len(defs) == 0 {
		fmt.Fprintln(out, "No workflow definitions found.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tNAME\tPATTERN\tSTAGES\tSCOPE\tENABLED")
	for _, d := range defs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
			d.Slug, d.Name, d.PatternKind, d.StageCount, definitionScope(d), yesNo(d.IsEnabled))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d definition(s) shown.\n", len(defs))
	return nil
}

// definitionScope labels a definition global (platform template,
// organizationGuid null) or org-owned.
func definitionScope(d workflowDefSummary) string {
	if d.IsGlobal {
		return "global"
	}
	return "org"
}

// ---- astro workflow definition ----------------------------------------------

var workflowDefinitionCmd = &cobra.Command{
	Use:   "definition <slug>",
	Short: "Show one workflow definition and its stages",
	Long: `Fetches a visible WorkflowDefinition by slug (workflowDefinition) plus
its ordered stages (workflowStages): kind, on-failure policy, timeout,
fan-out, and the bound agent definition if any.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runWorkflowDefinition(cmd, ctx, client, args[0])
	}),
}

func runWorkflowDefinition(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	getCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Definition *workflowDefSummary `json:"workflowDefinition"`
		Stages     []workflowStageRow  `json:"workflowStages"`
	}
	if err := client.GraphQL(getCtx, workflowDefinitionDetailQuery,
		map[string]interface{}{"slug": slug}, &resp); err != nil {
		return fmt.Errorf("fetching definition: %w", err)
	}
	if resp.Definition == nil {
		return fmt.Errorf("workflow definition %q not found", slug)
	}
	d := resp.Definition

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Definition:  %s (%s)\n", d.Name, d.Slug)
	fmt.Fprintf(out, "Pattern:     %s\n", d.PatternKind)
	fmt.Fprintf(out, "Scope:       %s\n", definitionScope(*d))
	fmt.Fprintf(out, "Enabled:     %s\n", yesNo(d.IsEnabled))
	if d.Description != "" {
		fmt.Fprintf(out, "Description: %s\n", d.Description)
	}
	fmt.Fprintf(out, "Stages:      %d\n", len(resp.Stages))
	for _, s := range resp.Stages {
		line := fmt.Sprintf("  [%d] %s on_failure=%s timeout=%ds", s.Order, s.Kind, s.OnFailure, s.TimeoutSeconds)
		if s.FanOutCount != nil {
			line += fmt.Sprintf(" fan_out=%d", *s.FanOutCount)
		}
		if s.AgentDefinitionName != nil && *s.AgentDefinitionName != "" {
			line += " agent=" + *s.AgentDefinitionName
		}
		fmt.Fprintln(out, line)
	}
	return nil
}

// ---- astro workflow clone -----------------------------------------------------

var workflowCloneCmd = &cobra.Command{
	Use:   "clone <slug>",
	Short: "Clone a visible definition into the working org",
	Long: `Copies a global/catalogue WorkflowDefinition (with its stages) into the
working org via cloneWorkflowDefinition, so it can be edited and bound.
Prints the created definition slug.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runWorkflowClone(cmd, ctx, client, args[0])
	}),
}

func runWorkflowClone(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	cloneCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Result struct {
			Ok     bool             `json:"ok"`
			Errors validationErrors `json:"errors"`
			Slug   *string          `json:"slug"`
		} `json:"cloneWorkflowDefinition"`
	}
	if err := client.GraphQL(cloneCtx, cloneWorkflowDefinitionMutation,
		map[string]interface{}{"slug": slug}, &resp); err != nil {
		return fmt.Errorf("cloning definition: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("clone failed: %s", firstValidationError(resp.Result.Errors))
	}
	created := slug
	if resp.Result.Slug != nil && *resp.Result.Slug != "" {
		created = *resp.Result.Slug
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Cloned definition: %s\n", created)
	return nil
}

// ---- astro workflow list -------------------------------------------------------

var workflowListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the org's configured Workflows",
	Long: `Lists the working org's configured Workflows (tier 2) via the
workflows GraphQL query: slug, backing definition, trigger, enabled, and
run count. (The list resolver returns no per-run detail; use
` + "`astro workflow runs <slug>`" + ` for a workflow's runs.)`,
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
		return runWorkflowList(cmd, ctx, client)
	}),
}

func runWorkflowList(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Workflows []configuredWorkflow `json:"workflows"`
	}
	if err := client.GraphQL(listCtx, workflowsListQuery, nil, &resp); err != nil {
		return fmt.Errorf("listing workflows: %w", err)
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Workflows)
	}

	out := cmd.OutOrStdout()
	if len(resp.Workflows) == 0 {
		fmt.Fprintln(out, "No workflows found.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tNAME\tDEFINITION\tTRIGGER\tENABLED\tRUNS")
	for _, wf := range resp.Workflows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n",
			wf.Slug, wf.Name, wf.DefinitionSlug, wf.TriggerKind, yesNo(wf.IsEnabled), wf.RunCount)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d workflow(s) shown.\n", len(resp.Workflows))
	return nil
}

// ---- astro workflow create -----------------------------------------------------

var workflowCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Configure a Workflow from a visible definition",
	Long: `Creates a configured Workflow (tier 2) from a visible definition via
createWorkflow. Every agent_dispatch stage must resolve to an agent — either
the stage's own default or a --bind:

  --bind <stageOrder>=<agentWorkloadGuid>   (repeatable)
  --input <key>=<value>                     (repeatable; workflow defaults)

Example:
  astro workflow create --definition feature-dev --name "Feature Dev" \
    --bind 0=9f2c... --input repo=myorg/api --trigger schedule --cron "0 9 * * 1"`,
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
		return runWorkflowCreate(cmd, ctx, client)
	}),
}

func runWorkflowCreate(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	if workflowCreateDefinition == "" {
		return fmt.Errorf("--definition is required")
	}
	if workflowCreateName == "" {
		return fmt.Errorf("--name is required")
	}
	bindings, err := parseStageBindings(workflowCreateBinds)
	if err != nil {
		return err
	}
	inputs, err := parseKeyValues("--input", workflowCreateInputs)
	if err != nil {
		return err
	}

	vars := map[string]interface{}{
		"name":           workflowCreateName,
		"definitionSlug": workflowCreateDefinition,
		"triggerKind":    workflowCreateTrigger,
	}
	if workflowCreateSlug != "" {
		vars["slug"] = workflowCreateSlug
	}
	if bindings != nil {
		vars["stageBindings"] = bindings
	}
	if inputs != nil {
		vars["inputs"] = inputs
	}
	if workflowCreateCron != "" {
		vars["scheduleCron"] = workflowCreateCron
	}

	createCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Result struct {
			Ok       bool                `json:"ok"`
			Errors   validationErrors    `json:"errors"`
			Workflow *configuredWorkflow `json:"workflow"`
		} `json:"createWorkflow"`
	}
	if err := client.GraphQL(createCtx, createWorkflowMutation, vars, &resp); err != nil {
		return fmt.Errorf("creating workflow: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("create failed: %s", firstValidationError(resp.Result.Errors))
	}

	out := cmd.OutOrStdout()
	wf := resp.Result.Workflow
	if wf == nil {
		fmt.Fprintln(out, "Workflow created.")
		return nil
	}
	fmt.Fprintf(out, "Created workflow: %s (%s)\n", wf.Name, wf.Slug)
	fmt.Fprintf(out, "ID:               %s\n", wf.GUID)
	fmt.Fprintf(out, "Trigger:          %s\n", wf.TriggerKind)
	fmt.Fprintf(out, "\nRun it with `astro workflow run %s`.\n", wf.Slug)
	return nil
}

// parseStageBindings turns repeated --bind <stageOrder>=<agentWorkloadGuid>
// flags into the Workflow.stage_bindings JSON the backend validates on save:
// {"<order>": {"agent_workload_id": "<guid>"}} — keys are stage orders as
// strings (the model resolves str(order) first).
func parseStageBindings(binds []string) (map[string]interface{}, error) {
	if len(binds) == 0 {
		return nil, nil
	}
	out := make(map[string]interface{}, len(binds))
	for _, b := range binds {
		order, guid, ok := strings.Cut(b, "=")
		if !ok || order == "" || guid == "" {
			return nil, fmt.Errorf("--bind %q must be <stageOrder>=<agentWorkloadGuid>", b)
		}
		if _, err := strconv.Atoi(order); err != nil {
			return nil, fmt.Errorf("--bind %q: stage order must be an integer", b)
		}
		out[order] = map[string]interface{}{"agent_workload_id": guid}
	}
	return out, nil
}

// parseKeyValues parses repeated key=value flags into a JSON object.
func parseKeyValues(flag string, kvs []string) (map[string]interface{}, error) {
	if len(kvs) == 0 {
		return nil, nil
	}
	out := make(map[string]interface{}, len(kvs))
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%s %q must be <key>=<value>", flag, kv)
		}
		out[k] = v
	}
	return out, nil
}

// ---- astro workflow run --------------------------------------------------------

var workflowRunConfigCmd = &cobra.Command{
	Use:   "run <workflow-slug>",
	Short: "Start a configured Workflow's run",
	Long: `Resolves the configured Workflow by slug, then starts a run via the
runWorkflow GraphQL mutation. --input key=value (repeatable) merges into the
workflow's default inputs for this run. Prints the run id.

Follow it with ` + "`astro workflow runs <slug> --watch`" + `.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runWorkflowRunConfig(cmd, ctx, client, args[0])
	}),
}

// resolveConfiguredWorkflow maps a workflow slug to its record (guid) via the
// workflow(slug) query.
func resolveConfiguredWorkflow(ctx context.Context, client *api.Client, slug string) (*configuredWorkflow, error) {
	getCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Workflow *configuredWorkflow `json:"workflow"`
	}
	if err := client.GraphQL(getCtx, workflowBySlugQuery,
		map[string]interface{}{"slug": slug}, &resp); err != nil {
		return nil, fmt.Errorf("resolving workflow: %w", err)
	}
	if resp.Workflow == nil {
		return nil, fmt.Errorf("workflow %q not found (see `astro workflow list`)", slug)
	}
	return resp.Workflow, nil
}

func runWorkflowRunConfig(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	inputs, err := parseKeyValues("--input", workflowRunConfigInputs)
	if err != nil {
		return err
	}
	wf, err := resolveConfiguredWorkflow(ctx, client, slug)
	if err != nil {
		return err
	}

	vars := map[string]interface{}{"workflowId": wf.GUID}
	if inputs != nil {
		vars["inputs"] = inputs
	}

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var resp struct {
		Result struct {
			Ok            bool             `json:"ok"`
			Errors        validationErrors `json:"errors"`
			RunID         *string          `json:"runId"`
			WorkflowRunID *string          `json:"workflowRunId"`
		} `json:"runWorkflow"`
	}
	if err := client.GraphQL(runCtx, runConfiguredWorkflowMutation, vars, &resp); err != nil {
		return fmt.Errorf("running workflow: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("run failed: %s", firstValidationError(resp.Result.Errors))
	}

	out := cmd.OutOrStdout()
	if resp.Result.RunID != nil {
		fmt.Fprintf(out, "Run ID:         %s\n", *resp.Result.RunID)
	}
	if resp.Result.WorkflowRunID != nil {
		fmt.Fprintf(out, "WorkflowRun ID: %s\n", *resp.Result.WorkflowRunID)
	}
	fmt.Fprintf(out, "\nWatch it with `astro workflow runs %s --watch`.\n", slug)
	return nil
}

// ---- astro workflow runs -------------------------------------------------------

var workflowRunsCmd = &cobra.Command{
	Use:   "runs <workflow-slug>",
	Short: "List a configured Workflow's runs",
	Long: `Lists a configured Workflow's runs (newest first) via the workflowRuns
GraphQL query. With --watch, polls every 5s and reports the newest run's
state transitions until it reaches a terminal state.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runWorkflowRuns(cmd, ctx, client, args[0])
	}),
}

// workflowRunTerminal reports whether a run has finished: completedAt is set,
// or the current state names a terminal outcome.
func workflowRunTerminal(r configuredWorkflowRun) bool {
	return r.IsCompleted || terminalWorkflowStatuses[strings.ToLower(r.CurrentState)]
}

func runWorkflowRuns(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	wf, err := resolveConfiguredWorkflow(ctx, client, slug)
	if err != nil {
		return err
	}

	fetch := func(fetchCtx context.Context) ([]configuredWorkflowRun, error) {
		var resp struct {
			Runs []configuredWorkflowRun `json:"workflowRuns"`
		}
		if err := client.GraphQL(fetchCtx, workflowRunsQuery,
			map[string]interface{}{"workflowId": wf.GUID}, &resp); err != nil {
			return nil, err
		}
		return resp.Runs, nil
	}

	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	runs, err := fetch(listCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("listing runs: %w", err)
	}

	out := cmd.OutOrStdout()
	if boolFlag(cmd, "json") && !workflowRunsWatch {
		return renderJSON(cmd, runs)
	}

	if len(runs) == 0 {
		fmt.Fprintln(out, "No runs found.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RUN ID\tSTATE\tSTARTED\tFINISHED")
	for _, r := range runs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			r.GUID, r.CurrentState, shortTime(&r.StartedAt), shortTime(r.CompletedAt))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if !workflowRunsWatch {
		return nil
	}

	// Runs come newest-first; watch the newest until it goes terminal.
	newest := runs[0]
	if workflowRunTerminal(newest) {
		fmt.Fprintf(out, "\nRun %s already terminal: %s\n", newest.GUID, newest.CurrentState)
		return nil
	}

	fmt.Fprintf(out, "\nWatching run %s...\n", newest.GUID)
	pollCtx, pollCancel := context.WithTimeout(ctx, 30*time.Minute)
	defer pollCancel()

	last := newest.CurrentState
	for {
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("timed out watching run (last state: %s)", last)
		case <-time.After(workflowRunsPollIntervalForTest):
		}

		fetchCtx, cancel := context.WithTimeout(pollCtx, 30*time.Second)
		polled, err := fetch(fetchCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("polling runs: %w", err)
		}
		var current *configuredWorkflowRun
		for i := range polled {
			if polled[i].GUID == newest.GUID {
				current = &polled[i]
				break
			}
		}
		if current == nil {
			continue
		}
		if current.CurrentState != last {
			fmt.Fprintf(out, "  → %s\n", current.CurrentState)
			last = current.CurrentState
		}
		if workflowRunTerminal(*current) {
			fmt.Fprintf(out, "Final state: %s\n", current.CurrentState)
			return nil
		}
	}
}

// ---- astro workflow import -----------------------------------------------------

var workflowImportCmd = &cobra.Command{
	Use:   "import <file.toml>",
	Short: "Import a workflow manifest (importWorkflowManifest)",
	Long: `Imports a §5.4 workflow manifest TOML as an org WorkflowDefinition via
importWorkflowManifest. With --preview the server only validates and returns
the parsed structure — nothing is persisted. The persisting import prints the
created definition slug.

The inverse of ` + "`astro workflow pull`" + ` (alias: export).`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		raw, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("reading %s: %w", args[0], err)
		}
		return runWorkflowImport(cmd, ctx, client, string(raw))
	}),
}

func runWorkflowImport(cmd *cobra.Command, ctx context.Context, client *api.Client, content string) error {
	importCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
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
	vars := map[string]interface{}{"toml": content, "preview": workflowImportPreview}
	if err := client.GraphQL(importCtx, importWorkflowManifestMutation, vars, &resp); err != nil {
		return fmt.Errorf("importing manifest: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("import failed: %s", firstValidationError(resp.Result.Errors))
	}

	out := cmd.OutOrStdout()
	if workflowImportPreview {
		fmt.Fprintln(out, "Preview OK — nothing persisted.")
		if m := resp.Result.Manifest; m != nil && m.Definition != nil {
			fmt.Fprintf(out, "  workflow: %s (%s), pattern=%s, %d stage(s)\n",
				m.Definition.Name, m.Definition.Slug, m.Definition.Pattern, len(m.Stages))
		}
		fmt.Fprintln(out, "Run again without --preview to persist.")
		return nil
	}

	created := ""
	if resp.Result.CreatedSlug != nil {
		created = *resp.Result.CreatedSlug
	}
	fmt.Fprintf(out, "Imported workflow definition: %s\n", created)
	return nil
}

// ---- astro workflow delete -----------------------------------------------------

var workflowDeleteCmd = &cobra.Command{
	Use:   "delete <workflow-slug>",
	Short: "Soft-delete a configured Workflow",
	Long: `Soft-deletes a configured Workflow (tier 2) via the deleteWorkflow
GraphQL mutation and tears down its Temporal schedule. Past runs are kept.

There is no interactive prompt: --yes is the confirmation (CI-safe).
Without it, nothing is changed.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runWorkflowDelete(cmd, ctx, client, args[0])
	}),
}

func runWorkflowDelete(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	if !workflowDeleteYes {
		return fmt.Errorf("refusing to delete workflow %q without --yes (nothing was changed)", slug)
	}
	if err := execWorkflowMutationResult(ctx, client, deleteWorkflowMutation, "deleteWorkflow", slug); err != nil {
		return fmt.Errorf("deleting workflow: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deleted workflow: %s (schedule torn down; past runs kept)\n", slug)
	return nil
}

// ---- astro workflow definition-delete --------------------------------------------

var workflowDefinitionDeleteCmd = &cobra.Command{
	Use:   "definition-delete <slug>",
	Short: "Soft-delete an org-owned workflow definition",
	Long: `Soft-deletes an org-owned WorkflowDefinition by slug via the
deleteWorkflowDefinition GraphQL mutation.

Global/catalogue definitions are read-only and refuse deletion. A definition
still referenced by live configured Workflows or enabled webhook triggers
also refuses (PROTECT) — delete/disable those first (see
` + "`astro workflow list`" + `).

There is no interactive prompt: --yes is the confirmation (CI-safe).
Without it, nothing is changed.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runWorkflowDefinitionDelete(cmd, ctx, client, args[0])
	}),
}

func runWorkflowDefinitionDelete(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	if !workflowDefDeleteYes {
		return fmt.Errorf("refusing to delete workflow definition %q without --yes (nothing was changed)", slug)
	}
	if err := execWorkflowMutationResult(ctx, client, deleteWorkflowDefinitionMutation, "deleteWorkflowDefinition", slug); err != nil {
		return fmt.Errorf("deleting definition: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deleted workflow definition: %s\n", slug)
	return nil
}

// execWorkflowMutationResult runs a slug-keyed mutation returning the bare
// MutationResult { ok, errors } envelope and folds a refusal (global
// definition, PROTECT on live references, not found) into one error.
func execWorkflowMutationResult(ctx context.Context, client *api.Client, mutation, field, slug string) error {
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp map[string]struct {
		Ok     bool             `json:"ok"`
		Errors validationErrors `json:"errors"`
	}
	if err := client.GraphQL(execCtx, mutation,
		map[string]interface{}{"slug": slug}, &resp); err != nil {
		return err
	}
	result := resp[field]
	if !result.Ok {
		return fmt.Errorf("%s", firstValidationError(result.Errors))
	}
	return nil
}

// ---- shared RunE wrapper -----------------------------------------------------

// workflowOrgScopedRunE wraps a run-func with the shared preamble every
// tier-2 workflow command needs: an authenticated client with the working
// org set as the tenant header (the resolvers all filter on caller org).
func workflowOrgScopedRunE(run func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		org, err := resolveOrg(cmd, cmd.Context(), client, cfg)
		if err != nil {
			return err
		}
		client.SetOrg(org.ID)
		return run(cmd, cmd.Context(), client, args)
	}
}

// ---- init ------------------------------------------------------------------

func init() {
	// definitions
	workflowDefinitionsCmd.Flags().BoolVar(&workflowDefsGlobal, "global", false, "Only platform-global (catalogue) definitions")
	workflowDefinitionsCmd.Flags().BoolVar(&workflowDefsOrg, "org-only", false, "Only the working org's own definitions")

	// create
	workflowCreateCmd.Flags().StringVar(&workflowCreateDefinition, "definition", "", "Definition slug to configure (required)")
	workflowCreateCmd.Flags().StringVar(&workflowCreateName, "name", "", "Workflow display name (required)")
	workflowCreateCmd.Flags().StringVar(&workflowCreateSlug, "slug", "", "Workflow slug (default: derived from the name)")
	workflowCreateCmd.Flags().StringArrayVar(&workflowCreateBinds, "bind", nil, "Stage binding <stageOrder>=<agentWorkloadGuid> (repeatable)")
	workflowCreateCmd.Flags().StringArrayVar(&workflowCreateInputs, "input", nil, "Default input <key>=<value> (repeatable)")
	workflowCreateCmd.Flags().StringVar(&workflowCreateTrigger, "trigger", "manual", "Trigger kind: manual, schedule")
	workflowCreateCmd.Flags().StringVar(&workflowCreateCron, "cron", "", "Cron expression (with --trigger schedule)")

	// run
	workflowRunConfigCmd.Flags().StringArrayVar(&workflowRunConfigInputs, "input", nil, "Run input <key>=<value> (repeatable; merged over workflow defaults)")

	// runs
	workflowRunsCmd.Flags().BoolVar(&workflowRunsWatch, "watch", false, "Poll every 5s until the newest run is terminal")

	// import
	workflowImportCmd.Flags().BoolVar(&workflowImportPreview, "preview", false, "Validate server-side only; persist nothing")

	// delete / definition-delete
	workflowDeleteCmd.Flags().BoolVarP(&workflowDeleteYes, "yes", "y", false, "Confirm the deletion (required to proceed)")
	workflowDefinitionDeleteCmd.Flags().BoolVarP(&workflowDefDeleteYes, "yes", "y", false, "Confirm the deletion (required to proceed)")

	workflowCmd.AddCommand(
		workflowDefinitionsCmd, workflowDefinitionCmd, workflowCloneCmd,
		workflowListCmd, workflowCreateCmd,
		workflowRunConfigCmd, workflowRunsCmd,
		workflowImportCmd,
		workflowDeleteCmd, workflowDefinitionDeleteCmd,
	)
}
