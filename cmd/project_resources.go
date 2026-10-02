package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// Project resources are shared, project-owned cloud services. The server's
// cluster catalogue is authoritative: the CLI never hard-codes cloud product
// names or attempts to reproduce provider validation.

type projectResourceCluster struct {
	ID                 string `json:"id"`
	Slug               string `json:"slug"`
	Name               string `json:"name"`
	ProviderPluginSlug string `json:"providerPluginSlug"`
	Region             string `json:"region"`
}

type projectResourceCatalogEntry struct {
	ID                 string                 `json:"id"`
	ProviderPluginSlug string                 `json:"providerPluginSlug"`
	Kind               string                 `json:"kind"`
	Variant            string                 `json:"variant"`
	DisplayName        string                 `json:"displayName"`
	Description        string                 `json:"description"`
	Status             string                 `json:"status"`
	Available          bool                   `json:"available"`
	UnavailableReason  string                 `json:"unavailableReason"`
	IsDefaultForKind   bool                   `json:"isDefaultForKind"`
	SizeOptions        []string               `json:"sizeOptions"`
	ConfigSchema       map[string]interface{} `json:"configSchema"`
	BindingEnvs        []string               `json:"bindingEnvs"`
	IssueURL           string                 `json:"issueUrl"`
}

type projectResourceAttachment struct {
	ID              string `json:"id"`
	ConsumerKind    string `json:"consumerKind"`
	ConsumerSlug    string `json:"consumerSlug"`
	EnvironmentName string `json:"environmentName"`
}

type projectResource struct {
	ID                   string `json:"id"`
	ContextRevision      string `json:"contextRevision"`
	Version              int    `json:"version"`
	Name                 string `json:"name"`
	Kind                 string `json:"kind"`
	Variant              string `json:"variant"`
	Status               string `json:"status"`
	OwnerScope           string `json:"ownerScope"`
	OrganizationID       string `json:"organizationId"`
	ProjectID            string `json:"projectId"`
	ProjectSlug          string `json:"projectSlug"`
	RegisteredAppID      string `json:"registeredAppId"`
	RegisteredAppSlug    string `json:"registeredAppSlug"`
	ClusterID            string `json:"clusterId"`
	ClusterSlug          string `json:"clusterSlug"`
	ClusterVersion       int    `json:"clusterVersion"`
	EnvironmentID        string `json:"environmentId"`
	EnvironmentName      string `json:"environmentName"`
	EnvironmentVersion   *int   `json:"environmentVersion"`
	OperationKind        string `json:"operationKind"`
	OperationWorkflowID  string `json:"operationWorkflowId"`
	OperationRunID       string `json:"operationRunId"`
	OperationStartedAt   string `json:"operationStartedAt"`
	OperationCompletedAt string `json:"operationCompletedAt"`
	CreatedAt            string `json:"createdAt"`
	UpdatedAt            string `json:"updatedAt"`
}

type projectResourceCostPreview struct {
	ManagedServiceID string                   `json:"managedServiceId"`
	Available        bool                     `json:"available"`
	Reason           string                   `json:"reason"`
	Message          string                   `json:"message"`
	MonthlyTotal     *float64                 `json:"monthlyTotal"`
	Currency         string                   `json:"currency"`
	LineItems        []map[string]interface{} `json:"lineItems"`
	PricingSourceURL string                   `json:"pricingSourceUrl"`
	PricingFetchedAt string                   `json:"pricingFetchedAt"`
	Notes            []string                 `json:"notes"`
	Approximate      bool                     `json:"approximate"`
}

type projectResourceMutation struct {
	OK     bool             `json:"ok"`
	Errors []mutationError  `json:"errors"`
	Data   *projectResource `json:"data"`
}

type projectResourceOptions struct {
	Cluster         string
	Kind            string
	Variant         string
	Name            string
	Environment     string
	Size            string
	Config          string
	Agents          []string
	AppEnvironments []string
	AvailableOnly   bool
	DeleteData      bool
	ForceDestroy    bool
}

var projectResourcesCmd = &cobra.Command{
	Use:     "resources",
	Aliases: []string{"resource", "services"},
	Short:   "Manage project-owned shared cloud resources",
	Long: `Discover and manage databases, caches, object stores, queues, search,
filesystems, eventing, functions, and other shared services attached to a
project's apps and agents.

The catalogue comes from the selected cluster. It includes executable provider
drivers and visible roadmap entries; unavailable entries explain why they
cannot be provisioned. Use --json on any
command for automation. Select the project with --project or default_project.`,
}

var projectResourceCatalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List provider capabilities for a project cluster",
	RunE: projectResourceRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef) error {
		return runProjectResourceCatalog(cmd, ctx, client, project, projectResourceFlags)
	}),
}

var projectResourceListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List project-owned shared resources",
	RunE: projectResourceRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef) error {
		return runProjectResourceList(cmd, ctx, client, project)
	}),
}

var projectResourceShowCmd = &cobra.Command{
	Use:   "show <name-or-id>",
	Short: "Review one shared resource by exact GUID (or bounded name discovery)",
	Args:  cobra.ExactArgs(1),
	RunE: projectResourceRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef) error {
		resource, err := resolveProjectResourceRevision(ctx, client, project.ID, cmd.Flags().Arg(0), resourceStringFlag(cmd, "expected-context-revision"))
		if err != nil {
			return err
		}
		return renderProjectResource(cmd, resource)
	}),
}

var projectResourceCostCmd = &cobra.Command{
	Use:   "cost <name-or-id>",
	Short: "Preview monthly cost from the provider's live pricing API",
	Args:  cobra.ExactArgs(1),
	RunE: projectResourceRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef) error {
		return runProjectResourceCost(cmd, ctx, client, project, cmd.Flags().Arg(0))
	}),
}

var projectResourceAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Provision and optionally attach a shared resource",
	Long: `Provision a provider-native resource from the server catalogue.

--config accepts a JSON object or @file.json. --size is merged into that object.
Repeat --agent with an agent environment-spec slug and --app-env with an app
environment GUID to attach consumers atomically at creation.`,
	RunE: projectResourceRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef) error {
		return runProjectResourceAdd(cmd, ctx, client, project, projectResourceFlags)
	}),
}

var projectResourceUpdateCmd = &cobra.Command{
	Use:   "update <name-or-id>",
	Short: "Update in-place provider configuration",
	Args:  cobra.ExactArgs(1),
	RunE: projectResourceRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef) error {
		return runProjectResourceUpdate(cmd, ctx, client, project, cmd.Flags().Arg(0), projectResourceFlags)
	}),
}

var projectResourceReprovisionCmd = &cobra.Command{
	Use:   "reprovision <name-or-id>",
	Short: "Re-run provisioning for an existing resource",
	Args:  cobra.ExactArgs(1),
	RunE: projectResourceRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef) error {
		return runProjectResourceReprovision(cmd, ctx, client, project, cmd.Flags().Arg(0))
	}),
}

var projectResourceAttachCmd = &cobra.Command{
	Use:   "attach <name-or-id>",
	Short: "Attach a resource to one agent or app environment",
	Args:  cobra.ExactArgs(1),
	RunE: projectResourceRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef) error {
		return runProjectResourceAttach(cmd, ctx, client, project, cmd.Flags().Arg(0), projectResourceFlags)
	}),
}

var projectResourceDetachCmd = &cobra.Command{
	Use:   "detach <attachment-id>",
	Short: "Detach a resource consumer by attachment ID",
	Args:  cobra.ExactArgs(1),
	RunE: projectResourceRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef) error {
		return runProjectResourceDetach(cmd, ctx, client, project, cmd.Flags().Arg(0))
	}),
}

var projectResourceRemoveCmd = &cobra.Command{
	Use:     "remove <name-or-id>",
	Aliases: []string{"rm", "delete"},
	Short:   "Deprovision a shared resource",
	Args:    cobra.ExactArgs(1),
	RunE: projectResourceRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef) error {
		return runProjectResourceRemove(cmd, ctx, client, project, cmd.Flags().Arg(0), projectResourceFlags)
	}),
}

var projectResourceFlags projectResourceOptions

func init() {
	projectResourceCatalogCmd.Flags().StringVar(&projectResourceFlags.Cluster, "cluster", "", "Cluster slug or GUID (required when more than one is available)")
	projectResourceCatalogCmd.Flags().BoolVar(&projectResourceFlags.AvailableOnly, "available-only", false, "Hide planned and unavailable capabilities")

	projectResourceAddCmd.Flags().StringVar(&projectResourceFlags.Cluster, "cluster", "", "Cluster slug or GUID (required when more than one is available)")
	projectResourceAddCmd.Flags().StringVar(&projectResourceFlags.Kind, "kind", "", "Portable resource kind (required)")
	projectResourceAddCmd.Flags().StringVar(&projectResourceFlags.Variant, "variant", "", "Provider variant (required if the kind has no unique default)")
	projectResourceAddCmd.Flags().StringVar(&projectResourceFlags.Name, "name", "", "Resource name (defaults to kind)")
	projectResourceAddCmd.Flags().StringVar(&projectResourceFlags.Environment, "environment", "production", "Logical project environment")
	projectResourceAddCmd.Flags().StringVar(&projectResourceFlags.Size, "size", "", "Portable size preset")
	projectResourceAddCmd.Flags().StringVar(&projectResourceFlags.Config, "config", "{}", "Provider config JSON object or @file.json")
	projectResourceAddCmd.Flags().StringSliceVar(&projectResourceFlags.Agents, "agent", nil, "Agent environment-spec slug to attach (repeatable)")
	projectResourceAddCmd.Flags().StringSliceVar(&projectResourceFlags.AppEnvironments, "app-env", nil, "App environment GUID to attach (repeatable)")
	_ = projectResourceAddCmd.MarkFlagRequired("kind")

	projectResourceUpdateCmd.Flags().StringVar(&projectResourceFlags.Name, "name", "", "New resource name")
	projectResourceUpdateCmd.Flags().StringVar(&projectResourceFlags.Config, "config", "", "Replacement provider config JSON object or @file.json")
	projectResourceAttachCmd.Flags().StringSliceVar(&projectResourceFlags.Agents, "agent", nil, "Agent environment-spec slug (exactly one consumer)")
	projectResourceAttachCmd.Flags().StringSliceVar(&projectResourceFlags.AppEnvironments, "app-env", nil, "App environment GUID (exactly one consumer)")
	projectResourceRemoveCmd.Flags().BoolVar(&projectResourceFlags.DeleteData, "delete-data", false, "Delete provider data instead of retaining it")
	projectResourceRemoveCmd.Flags().BoolVar(&projectResourceFlags.ForceDestroy, "force-destroy", false, "Bypass provider deletion protection where supported")

	projectResourcesCmd.AddCommand(
		projectResourceCatalogCmd,
		projectResourceListCmd,
		projectResourceShowCmd,
		projectResourceCostCmd,
		projectResourceAddCmd,
		projectResourceUpdateCmd,
		projectResourceReprovisionCmd,
		projectResourceAttachCmd,
		projectResourceDetachCmd,
		projectResourceRemoveCmd,
	)
	projectCmd.AddCommand(projectResourcesCmd)
}

func projectResourceRunE(run func(*cobra.Command, context.Context, *api.Client, projectRef) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		project, err := resolveProjectForResource(cmd, cmd.Context(), client, cfg)
		if err != nil {
			return err
		}
		return run(cmd, cmd.Context(), client, project)
	}
}

func resolveProjectForResource(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config) (projectRef, error) {
	selector, _ := cmd.Flags().GetString("project")
	selector = strings.TrimSpace(selector)
	if selector == "" && cfg != nil {
		selector = strings.TrimSpace(cfg.DefaultProject)
	}
	if selector == "" {
		return projectRef{}, fmt.Errorf("select a project with --project <slug-or-id> or configure default_project")
	}
	var resp struct {
		Projects []projectRef `json:"astroliftProjects"`
	}
	if err := client.GraphQL(ctx, `query { astroliftProjects { id slug name team { slug name } } }`, nil, &resp); err != nil {
		return projectRef{}, fmt.Errorf("resolving project: %w", err)
	}
	for _, project := range resp.Projects {
		if project.ID == selector || project.Slug == selector {
			return project, nil
		}
	}
	return projectRef{}, fmt.Errorf("project %q not found or not visible", selector)
}

func listProjectResourceClusters(ctx context.Context, client *api.Client, projectID string) ([]projectResourceCluster, error) {
	var resp struct {
		Clusters []projectResourceCluster `json:"astroliftProjectResourceClusters"`
	}
	query := `query($projectId: GUID!) {
  astroliftProjectResourceClusters(projectId: $projectId) {
    id slug name providerPluginSlug region
  }
}`
	if err := client.GraphQL(ctx, query, map[string]interface{}{"projectId": projectID}, &resp); err != nil {
		return nil, fmt.Errorf("listing project clusters: %w", err)
	}
	return resp.Clusters, nil
}

func resolveProjectResourceCluster(ctx context.Context, client *api.Client, projectID, selector string) (projectResourceCluster, error) {
	clusters, err := listProjectResourceClusters(ctx, client, projectID)
	if err != nil {
		return projectResourceCluster{}, err
	}
	if selector == "" && len(clusters) == 1 {
		return clusters[0], nil
	}
	for _, cluster := range clusters {
		if cluster.ID == selector || cluster.Slug == selector {
			return cluster, nil
		}
	}
	if selector == "" {
		return projectResourceCluster{}, fmt.Errorf("--cluster <slug-or-id> is required; project has %d available clusters", len(clusters))
	}
	return projectResourceCluster{}, fmt.Errorf("cluster %q is not available to this project", selector)
}

func runProjectResourceCatalog(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef, opts projectResourceOptions) error {
	cluster, err := resolveProjectResourceCluster(ctx, client, project.ID, opts.Cluster)
	if err != nil {
		return err
	}
	var resp struct {
		Entries []projectResourceCatalogEntry `json:"astroliftProjectManagedServiceCatalog"`
	}
	query := `query($projectId: GUID!, $clusterId: GUID!) {
  astroliftProjectManagedServiceCatalog(projectId: $projectId, clusterId: $clusterId) {
    id providerPluginSlug kind variant displayName description status available
    unavailableReason isDefaultForKind sizeOptions configSchema bindingEnvs issueUrl
  }
}`
	vars := map[string]interface{}{"projectId": project.ID, "clusterId": cluster.ID}
	if err := client.GraphQL(ctx, query, vars, &resp); err != nil {
		return fmt.Errorf("listing resource catalogue: %w", err)
	}
	sortCatalogEntries(resp.Entries)
	if opts.AvailableOnly {
		filtered := resp.Entries[:0]
		for _, entry := range resp.Entries {
			if entry.Available {
				filtered = append(filtered, entry)
			}
		}
		resp.Entries = filtered
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, map[string]interface{}{"project": project, "cluster": cluster, "entries": resp.Entries})
	}
	if len(resp.Entries) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No resource capabilities found.")
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "KIND\tVARIANT\tSTATUS\tDEFAULT\tPRODUCT\tTRACKING\n")
	for _, entry := range resp.Entries {
		status := entry.Status
		if !entry.Available {
			status += " (unavailable)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			entry.Kind, entry.Variant, status, yesNo(entry.IsDefaultForKind), entry.DisplayName, dashIfEmpty(entry.IssueURL))
	}
	return w.Flush()
}

func parseResourceConfig(raw, size string) (map[string]interface{}, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	payload, err := parseJSONInput(raw)
	if err != nil {
		return nil, fmt.Errorf("%s", strings.NewReplacer("--input", "--config").Replace(err.Error()))
	}
	config, ok := payload.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("--config must be a JSON object")
	}
	if size != "" {
		config["size"] = size
	}
	return config, nil
}

func runProjectResourceAdd(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef, opts projectResourceOptions) error {
	cluster, err := resolveProjectResourceCluster(ctx, client, project.ID, opts.Cluster)
	if err != nil {
		return err
	}
	config, err := parseResourceConfig(opts.Config, opts.Size)
	if err != nil {
		return err
	}
	input := map[string]interface{}{
		"projectId": project.ID, "clusterId": cluster.ID, "environmentName": opts.Environment,
		"kind": opts.Kind, "config": config,
		"agentEnvironmentSpecSlugs": opts.Agents, "appEnvironmentIds": opts.AppEnvironments,
	}
	if opts.Name != "" {
		input["name"] = opts.Name
	}
	if opts.Variant != "" {
		input["variant"] = opts.Variant
	}
	const mutation = `mutation($input: ProvisionProjectManagedServiceInput!) {
  provisionProjectManagedService(input: $input) {
    ok errors { code message field }
    data { id name kind variant status projectSlug clusterSlug environmentName createdAt updatedAt operationKind operationWorkflowId operationRunId operationStartedAt operationCompletedAt }
  }
}`
	var resp struct {
		Result projectResourceMutation `json:"provisionProjectManagedService"`
	}
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("provision request failed; outcome may be unconfirmed")
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return fmt.Errorf("provision failed: %s", resourceErrorCode(resp.Result.Errors))
	}
	return renderProjectResource(cmd, resp.Result.Data)
}

func runProjectResourceUpdate(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef, selector string, opts projectResourceOptions) error {
	resource, err := resolveProjectResourceRevision(ctx, client, project.ID, selector, resourceStringFlag(cmd, "expected-context-revision"))
	if err != nil {
		return err
	}
	input := map[string]interface{}{"id": resource.ID, "expectedContextRevision": resource.ContextRevision}
	if opts.Name != "" {
		input["name"] = opts.Name
	}
	if opts.Config != "" {
		config, err := parseResourceConfig(opts.Config, "")
		if err != nil {
			return err
		}
		input["config"] = config
	}
	if len(input) == 2 {
		return fmt.Errorf("provide --name and/or --config")
	}
	return mutateProjectResource(cmd, ctx, client, "updateProjectManagedService", "UpdateManagedServiceInput!", input)
}

func runProjectResourceReprovision(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef, selector string) error {
	resource, err := resolveProjectResourceRevision(ctx, client, project.ID, selector, resourceStringFlag(cmd, "expected-context-revision"))
	if err != nil {
		return err
	}
	return mutateProjectResource(cmd, ctx, client, "reprovisionProjectManagedService", "ReprovisionManagedServiceInput!", map[string]interface{}{"managedServiceId": resource.ID, "expectedContextRevision": resource.ContextRevision})
}

func mutateProjectResource(cmd *cobra.Command, ctx context.Context, client *api.Client, field, inputType string, input map[string]interface{}) error {
	mutation := fmt.Sprintf(`mutation($input: %s) {
  %s(input: $input) {
    ok errors { code message field }
    data { id name kind variant status projectSlug clusterSlug environmentName createdAt updatedAt operationKind operationWorkflowId operationRunId operationStartedAt operationCompletedAt }
  }
}`, inputType, field)
	var envelope map[string]projectResourceMutation
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": input}, &envelope); err != nil {
		return fmt.Errorf("%s request failed; outcome may be unconfirmed", field)
	}
	result := envelope[field]
	if !result.OK || result.Data == nil {
		return fmt.Errorf("%s failed: %s", field, resourceErrorCode(result.Errors))
	}
	return renderProjectResource(cmd, result.Data)
}

func runProjectResourceAttach(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef, selector string, opts projectResourceOptions) error {
	if len(opts.Agents)+len(opts.AppEnvironments) != 1 {
		return fmt.Errorf("provide exactly one --agent <env-spec-slug> or --app-env <guid>")
	}

	resource, err := resolveProjectResourceRevision(ctx, client, project.ID, selector, resourceStringFlag(cmd, "expected-context-revision"))
	if err != nil {
		return err
	}
	input := map[string]interface{}{"managedServiceId": resource.ID, "expectedContextRevision": resource.ContextRevision}
	if len(opts.Agents) == 1 {
		input["agentEnvironmentSpecSlug"] = opts.Agents[0]
	} else {
		input["appEnvironmentId"] = opts.AppEnvironments[0]
	}
	const mutation = `mutation($input: AttachProjectManagedServiceInput!) {
  attachProjectManagedService(input: $input) {
    ok errors { code message field }
    data { id consumerKind consumerSlug environmentName }
  }
}`
	var resp struct {
		Result struct {
			OK     bool                       `json:"ok"`
			Errors []mutationError            `json:"errors"`
			Data   *projectResourceAttachment `json:"data"`
		} `json:"attachProjectManagedService"`
	}
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("attach request failed; outcome may be unconfirmed")
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return fmt.Errorf("attach failed: %s", resourceErrorCode(resp.Result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Result.Data)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Attached %s to %s:%s\n", resource.Name, resp.Result.Data.ConsumerKind, resp.Result.Data.ConsumerSlug)
	return nil
}

func runProjectResourceDetach(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef, attachmentID string) error {
	resourceID := resourceStringFlag(cmd, "resource")
	var resource *projectResource
	var err error
	if resourceID == "" {
		resource, err = resolveProjectAttachmentResource(ctx, client, project.ID, attachmentID, resourceStringFlag(cmd, "expected-context-revision"))
	} else {
		if _, parseErr := uuid.Parse(resourceID); parseErr != nil {
			return fmt.Errorf("--resource requires an exact GUID")
		}
		resource, err = resolveProjectResourceRevision(ctx, client, project.ID, resourceID, resourceStringFlag(cmd, "expected-context-revision"))
	}
	if err != nil {
		return err
	}
	const mutation = `mutation($input: DetachProjectManagedServiceInput!) {
  detachProjectManagedService(input: $input) {
    ok errors { code message field }
    data { id consumerKind consumerSlug environmentName }
  }
}`
	var resp struct {
		Result struct {
			OK     bool                       `json:"ok"`
			Errors []mutationError            `json:"errors"`
			Data   *projectResourceAttachment `json:"data"`
		} `json:"detachProjectManagedService"`
	}
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": map[string]interface{}{"attachmentId": attachmentID, "managedServiceId": resource.ID, "expectedContextRevision": resource.ContextRevision}}, &resp); err != nil {
		return fmt.Errorf("detach request failed; outcome may be unconfirmed")
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return fmt.Errorf("detach failed: %s", resourceErrorCode(resp.Result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Result.Data)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Detached %s\n", attachmentID)
	return nil
}

func runProjectResourceRemove(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef, selector string, opts projectResourceOptions) error {
	resource, err := resolveProjectResourceRevision(ctx, client, project.ID, selector, resourceStringFlag(cmd, "expected-context-revision"))
	if err != nil {
		return err
	}
	input := map[string]interface{}{"id": resource.ID, "expectedContextRevision": resource.ContextRevision, "deleteData": opts.DeleteData, "forceDestroy": opts.ForceDestroy}
	const mutation = `mutation($input: DeprovisionManagedServiceInput!) {
  deprovisionProjectManagedService(input: $input) {
    ok errors { code message field }
    data { id deleted }
  }
}`
	var resp struct {
		Result struct {
			OK     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *struct {
				ID      string `json:"id"`
				Deleted bool   `json:"deleted"`
			} `json:"data"`
		} `json:"deprovisionProjectManagedService"`
	}
	if err := client.GraphQL(ctx, mutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("deprovision request failed; outcome may be unconfirmed")
	}
	if !resp.Result.OK || resp.Result.Data == nil {
		return fmt.Errorf("remove failed: %s", resourceErrorCode(resp.Result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Result.Data)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deprovisioning %s (%s)\n", resource.Name, resource.ID)
	return nil
}

// Stable ordering makes JSON fixtures and future shell completions deterministic.
func sortCatalogEntries(entries []projectResourceCatalogEntry) {
	sort.Slice(entries, func(i, j int) bool {
		left := entries[i].Kind + "\x00" + entries[i].Variant
		right := entries[j].Kind + "\x00" + entries[j].Variant
		return left < right
	})
}
