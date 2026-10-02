package cmd

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const resourceContextFields = `id contextRevision version name kind variant status ownerScope organizationId projectId projectSlug registeredAppId registeredAppSlug clusterId clusterSlug clusterVersion environmentId environmentName environmentVersion createdAt updatedAt operationKind operationWorkflowId operationRunId operationStartedAt operationCompletedAt`

type projectResourcePage struct {
	Items      []projectResource `json:"items"`
	TotalCount int               `json:"totalCount"`
	NextCursor *string           `json:"nextCursor"`
}
type resourceConsumer struct {
	ID               string `json:"id"`
	Version          int    `json:"version"`
	ManagedServiceID string `json:"managedServiceId"`
	ConsumerKind     string `json:"consumerKind"`
	ConsumerID       string `json:"consumerId"`
	ConsumerSlug     string `json:"consumerSlug"`
	RegisteredAppID  string `json:"registeredAppId"`
	EnvironmentID    string `json:"environmentId"`
	EnvironmentName  string `json:"environmentName"`
	ClusterID        string `json:"clusterId"`
	CreatedAt        string `json:"createdAt"`
}
type resourceConsumerPage struct {
	Items      []resourceConsumer `json:"items"`
	TotalCount int                `json:"totalCount"`
	NextCursor *string            `json:"nextCursor"`
}

func resourceStringFlag(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return strings.TrimSpace(value)
}
func resourceErrorCode(errors []mutationError) string {
	if len(errors) > 0 && regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`).MatchString(errors[0].Code) {
		return errors[0].Code
	}
	return "REQUEST_REFUSED"
}
func resourcePageVariables(cmd *cobra.Command, projectID string) (map[string]interface{}, error) {
	limit := 50
	if cmd.Flags().Lookup("limit") != nil {
		limit, _ = cmd.Flags().GetInt("limit")
	}
	if limit < 1 || limit > 200 {
		return nil, fmt.Errorf("--limit must be between 1 and 200")
	}
	vars := map[string]interface{}{"projectId": projectID, "limit": limit}
	for _, name := range []string{"after", "search", "environment", "cluster"} {
		if value := resourceStringFlag(cmd, name); value != "" {
			field := name
			if name == "environment" {
				field = "environmentName"
			}
			if name == "cluster" {
				if _, err := uuid.Parse(value); err != nil {
					return nil, fmt.Errorf("--cluster requires an exact GUID")
				}
				field = "clusterId"
			}
			vars[field] = value
		}
	}
	for _, pair := range []struct{ flag, field string }{{"kind", "kinds"}, {"status", "statuses"}} {
		values, _ := cmd.Flags().GetStringSlice(pair.flag)
		if len(values) > 0 {
			vars[pair.field] = values
		}
	}
	return vars, nil
}
func fetchProjectResourcePage(ctx context.Context, client *api.Client, variables map[string]interface{}) (*projectResourcePage, error) {
	query := `query($projectId: GUID!, $search: String, $name: String, $kinds: [String!], $statuses: [String!], $environmentName: String, $clusterId: GUID, $limit: Int!, $after: String) {
 astroliftProjectManagedServicesPage(projectId:$projectId,search:$search,name:$name,kinds:$kinds,statuses:$statuses,environmentName:$environmentName,clusterId:$clusterId,limit:$limit,after:$after) { items { ` + resourceContextFields + ` } totalCount nextCursor }
}`
	var response struct {
		Page *projectResourcePage `json:"astroliftProjectManagedServicesPage"`
	}
	if err := client.GraphQL(ctx, query, variables, &response); err != nil {
		return nil, fmt.Errorf("resource page unavailable; restart from the first page or review current permissions")
	}
	if response.Page == nil {
		return nil, fmt.Errorf("resource page unavailable")
	}
	return response.Page, nil
}

const resourceScanMaxPages = 200

func runProjectResourceList(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef) error {
	vars, err := resourcePageVariables(cmd, project.ID)
	if err != nil {
		return err
	}
	paged := boolFlag(cmd, "page")
	if vars["after"] != nil && !paged {
		return fmt.Errorf("--after requires --page; default list returns a complete metadata array")
	}
	page, err := fetchProjectResourcePage(ctx, client, vars)
	if err != nil {
		return err
	}
	if paged && boolFlag(cmd, "json") {
		return renderJSON(cmd, page)
	}
	rows := page.Items
	if !paged {
		seen := map[string]bool{}
		for count := 1; page.NextCursor != nil; count++ {
			if count >= resourceScanMaxPages || seen[*page.NextCursor] {
				return fmt.Errorf("resource walk is incomplete; use --page with --after for explicit continuation")
			}
			seen[*page.NextCursor] = true
			vars["after"] = *page.NextCursor
			page, err = fetchProjectResourcePage(ctx, client, vars)
			if err != nil {
				return err
			}
			rows = append(rows, page.Items...)
		}
		if rows == nil {
			rows = []projectResource{}
		}
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, rows)
		}
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tVARIANT\tSTATUS\tCLUSTER\tENVIRONMENT\tID")
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.Name, row.Kind, dashIfEmpty(row.Variant), row.Status, dashIfEmpty(row.ClusterSlug), row.EnvironmentName, row.ID)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%d shown / %d currently visible resources\n", len(rows), page.TotalCount)
	if paged && page.NextCursor != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Next cursor: %s\n", *page.NextCursor)
	}
	return nil
}
func resolveProjectResource(ctx context.Context, client *api.Client, projectID, selector string) (*projectResource, error) {
	return resolveProjectResourceRevision(ctx, client, projectID, selector, "")
}
func resolveProjectResourceRevision(ctx context.Context, client *api.Client, projectID, selector, revision string) (*projectResource, error) {
	id := selector
	if _, err := uuid.Parse(selector); err != nil {
		page, err := fetchProjectResourcePage(ctx, client, map[string]interface{}{"projectId": projectID, "name": selector, "limit": 2})
		if err != nil {
			return nil, err
		}
		if page.TotalCount == 0 {
			return nil, fmt.Errorf("resource unavailable; use its exact GUID")
		}
		if page.TotalCount != 1 || len(page.Items) != 1 || page.NextCursor != nil {
			return nil, fmt.Errorf("resource name is ambiguous; use its exact GUID")
		}
		id = page.Items[0].ID
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, fmt.Errorf("resource returned an invalid GUID")
	}
	var response struct {
		Row *projectResource `json:"astroliftProjectManagedService"`
	}
	query := `query($projectId: GUID!, $id: GUID!, $expectedContextRevision: String) { astroliftProjectManagedService(projectId:$projectId,id:$id,expectedContextRevision:$expectedContextRevision) { ` + resourceContextFields + ` } }`
	vars := map[string]interface{}{"projectId": projectID, "id": id}
	if revision != "" {
		vars["expectedContextRevision"] = revision
	}
	if err := client.GraphQL(ctx, query, vars, &response); err != nil {
		return nil, fmt.Errorf("resource context unavailable or changed; review its exact GUID again")
	}
	row := response.Row
	if row == nil || row.ID != id || row.ProjectID != projectID || row.OwnerScope != "project" || row.ContextRevision == "" || (revision != "" && row.ContextRevision != revision) {
		return nil, fmt.Errorf("resource context unavailable or changed; review its exact GUID again")
	}
	return row, nil
}

// Resolve the exact, currently visible attachment owner without scanning a catalog.
// Then reread that resource with the returned revision before the reviewed write.
func resolveProjectAttachmentResource(ctx context.Context, client *api.Client, projectID, attachmentID, revision string) (*projectResource, error) {
	if _, err := uuid.Parse(attachmentID); err != nil {
		return nil, fmt.Errorf("attachment requires an exact GUID")
	}
	var response struct {
		Row *projectResource `json:"astroliftProjectManagedServiceAttachmentOwner"`
	}
	query := `query($projectId:GUID!, $attachmentId:GUID!) { astroliftProjectManagedServiceAttachmentOwner(projectId:$projectId,attachmentId:$attachmentId) { ` + resourceContextFields + ` } }`
	if err := client.GraphQL(ctx, query, map[string]interface{}{"projectId": projectID, "attachmentId": attachmentID}, &response); err != nil {
		return nil, fmt.Errorf("attachment owner unavailable; review the exact attachment and current permissions")
	}
	row := response.Row
	if row == nil || row.OwnerScope != "project" || row.ProjectID != projectID || row.ContextRevision == "" || (revision != "" && revision != row.ContextRevision) {
		return nil, fmt.Errorf("attachment owner unavailable or changed; review the exact attachment again")
	}
	if _, err := uuid.Parse(row.ID); err != nil {
		return nil, fmt.Errorf("attachment owner unavailable")
	}
	return resolveProjectResourceRevision(ctx, client, projectID, row.ID, row.ContextRevision)
}

func renderProjectResource(cmd *cobra.Command, row *projectResource) error {
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, row)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Name: %s\nID: %s\nKind: %s / %s\nStatus: %s\nProject: %s (%s)\nCluster: %s (%s)\nEnvironment: %s (%s)\n", row.Name, row.ID, row.Kind, dashIfEmpty(row.Variant), row.Status, row.ProjectSlug, row.ProjectID, row.ClusterSlug, row.ClusterID, row.EnvironmentName, dashIfEmpty(row.EnvironmentID))
	if row.ContextRevision != "" {
		fmt.Fprintf(out, "Context revision: %s\n", row.ContextRevision)
	}
	if row.OperationWorkflowID != "" {
		fmt.Fprintf(out, "Operation: %s / %s / %s\nAccepted/enqueued operations are not proof of completion.\n", row.OperationKind, row.OperationWorkflowID, row.OperationRunID)
	}
	return nil
}
func runProjectResourceCost(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef, selector string) error {
	row, err := resolveProjectResourceRevision(ctx, client, project.ID, selector, resourceStringFlag(cmd, "expected-context-revision"))
	if err != nil {
		return err
	}
	var response struct {
		Preview *projectResourceCostPreview `json:"astroliftManagedServiceCostPreview"`
	}
	query := `query($managedServiceId: GUID!, $expectedContextRevision: String) { astroliftManagedServiceCostPreview(managedServiceId:$managedServiceId,expectedContextRevision:$expectedContextRevision) { managedServiceId available reason message monthlyTotal currency lineItems pricingSourceUrl pricingFetchedAt notes approximate } }`
	if err := client.GraphQL(ctx, query, map[string]interface{}{"managedServiceId": row.ID, "expectedContextRevision": row.ContextRevision}, &response); err != nil {
		return fmt.Errorf("pricing unavailable or resource context changed")
	}
	preview := response.Preview
	if preview == nil || preview.ManagedServiceID != row.ID {
		return fmt.Errorf("pricing unavailable")
	}
	parsed, err := url.Parse(preview.PricingSourceURL)
	_, timeErr := time.Parse(time.RFC3339Nano, preview.PricingFetchedAt)
	valid := preview.Available && preview.MonthlyTotal != nil && !math.IsNaN(*preview.MonthlyTotal) && !math.IsInf(*preview.MonthlyTotal, 0) && *preview.MonthlyTotal >= 0 && regexp.MustCompile(`^[A-Z]{3}$`).MatchString(preview.Currency) && err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && timeErr == nil
	if !valid {
		preview.Available = false
		preview.MonthlyTotal = nil
		preview.Reason = "incomplete_price"
		preview.Message = "Pricing is unavailable"
		preview.PricingSourceURL = ""
		preview.Notes = nil
		preview.LineItems = nil
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, preview)
	}
	if !valid {
		fmt.Fprintln(cmd.OutOrStdout(), "Pricing is unavailable.")
		return nil
	}
	prefix := ""
	if preview.Approximate {
		prefix = "approximately "
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s%.2f %s/month\nPricing source: %s\nPrice retrieved: %s\n", prefix, *preview.MonthlyTotal, preview.Currency, preview.PricingSourceURL, preview.PricingFetchedAt)
	for _, note := range preview.Notes {
		fmt.Fprintf(cmd.OutOrStdout(), "Note: %s\n", note)
	}
	return nil
}

var projectResourceAttachmentsCmd = &cobra.Command{Use: "attachments <resource-GUID>", Short: "Page currently visible resource consumers without credentials", Args: cobra.ExactArgs(1), RunE: projectResourceRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef) error {
	return runProjectResourceAttachments(cmd, ctx, client, project, cmd.Flags().Arg(0))
})}

func runProjectResourceAttachments(cmd *cobra.Command, ctx context.Context, client *api.Client, project projectRef, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("an exact resource GUID is required")
	}

	row, err := resolveProjectResourceRevision(ctx, client, project.ID, id, resourceStringFlag(cmd, "expected-context-revision"))
	if err != nil {
		return err
	}
	vars, err := resourcePageVariables(cmd, project.ID)
	if err != nil {
		return err
	}
	vars["managedServiceId"] = row.ID
	vars["expectedContextRevision"] = row.ContextRevision
	var response struct {
		Page *resourceConsumerPage `json:"astroliftProjectManagedServiceAttachmentsPage"`
	}
	query := `query($projectId:GUID!,$managedServiceId:GUID!,$expectedContextRevision:String,$limit:Int!,$after:String) { astroliftProjectManagedServiceAttachmentsPage(projectId:$projectId,managedServiceId:$managedServiceId,expectedContextRevision:$expectedContextRevision,limit:$limit,after:$after) { items { id version managedServiceId consumerKind consumerId consumerSlug registeredAppId environmentId environmentName clusterId createdAt } totalCount nextCursor } }`
	if err := client.GraphQL(ctx, query, vars, &response); err != nil || response.Page == nil {
		return fmt.Errorf("consumer page unavailable or context changed; restart from the first page")
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, response.Page)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ATTACHMENT ID\tKIND\tCONSUMER\tCONSUMER ID\tENVIRONMENT\tCLUSTER ID")
	for _, consumer := range response.Page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", consumer.ID, consumer.ConsumerKind, consumer.ConsumerSlug, consumer.ConsumerID, consumer.EnvironmentName, consumer.ClusterID)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%d shown / %d currently visible consumers\n", len(response.Page.Items), response.Page.TotalCount)
	if response.Page.NextCursor != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Next cursor: %s\n", *response.Page.NextCursor)
	}
	return nil
}

func init() {
	projectResourceListCmd.Flags().Bool("page", false, "Return one server page with totalCount and nextCursor (JSON envelope)")
	projectResourceListCmd.Flags().Int("limit", 50, "Maximum records in one server page (1-200)")
	for _, name := range []string{"after", "search", "environment", "cluster"} {
		projectResourceListCmd.Flags().String(name, "", "Server page filter; --cluster requires a GUID")
	}
	projectResourceListCmd.Flags().StringSlice("kind", nil, "Filter resource kinds")
	projectResourceListCmd.Flags().StringSlice("status", nil, "Filter resource states")
	projectResourceAttachmentsCmd.Flags().Int("limit", 50, "Maximum consumers in one server page (1-200)")
	projectResourceAttachmentsCmd.Flags().String("after", "", "Continuation from the preceding consumer page")
	for _, command := range []*cobra.Command{projectResourceShowCmd, projectResourceCostCmd, projectResourceUpdateCmd, projectResourceReprovisionCmd, projectResourceAttachCmd, projectResourceDetachCmd, projectResourceRemoveCmd, projectResourceAttachmentsCmd} {
		command.Flags().String("expected-context-revision", "", "Refuse a context changed since this reviewed revision")
	}
	projectResourceDetachCmd.Flags().String("resource", "", "Exact resource GUID owning this attachment (optional; otherwise resolve exact attachment owner)")
	projectResourcesCmd.AddCommand(projectResourceAttachmentsCmd)
}
