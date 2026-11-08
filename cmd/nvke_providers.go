package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/spf13/cobra"
)

func newNVKEProvidersCommand() *cobra.Command {
	group := &cobra.Command{Use: "providers", Short: "Collect and inspect historical infrastructure provider facts", Long: `Use the explicit management-Kubernetes connection to collect installation,
configuration, manager availability and credential-check facts. Collection success
is not provider readiness. Both read and admin permission are required to collect
or read private history. READ-only access can recover receipt metadata. Historical
reads do not contact native services, and preserve exact decimal strings and times.`}
	group.AddCommand(newNVKEProviderRequestCommand(false), newNVKEProviderRequestCommand(true))
	group.AddCommand(&cobra.Command{Use: "catalog", Args: cobra.NoArgs, Short: "Read the pinned provider registry and freshness policy", RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
		row, err := c.NVKEProviderCatalog(ctx)
		if err != nil {
			return err
		}
		var catalog struct {
			Format       int    `json:"format"`
			OperationKey string `json:"operationKey"`
			Providers    []struct {
				Type string `json:"type"`
			} `json:"providers"`
		}
		if row.Revision != nvkecatalog.Pinned().Revision || json.Unmarshal(row.InfrastructureProviders, &catalog) != nil || catalog.Format != 1 || catalog.OperationKey != nvkeProviderOperationKey || len(catalog.Providers) != 2 || catalog.Providers[0].Type != "nico" || catalog.Providers[1].Type != "launchlayer" {
			return errors.New("provider catalog does not match the pinned interface")
		}
		return nvkeJSON(cmd, row)
	})})
	for _, kind := range []string{"observation", "identities"} {
		selected := kind
		leaf := &cobra.Command{Use: kind + " OPERATION_GUID", Args: cobra.ExactArgs(1), Short: "Read saved provider " + kind + " at an exact receipt version", RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
			return runNVKEProviderView(cmd, ctx, c, args[0], selected)
		})}
		nvkeReadSiteFlags(leaf)
		leaf.Flags().Int("version", 0, "Reviewed successful provider operation version (required)")
		_ = leaf.MarkFlagRequired("version")
		if kind == "identities" {
			leaf.Flags().String("after", "", "Opaque cursor from the prior provider page")
			leaf.Flags().Int("limit", 25, "Provider identity page size (1–100)")
			leaf.Flags().String("provider", "nico", "Provider: nico or launchlayer")
			leaf.Flags().String("search", "", "Search the complete saved identity list on the server")
		}
		group.AddCommand(leaf)
	}
	return group
}

var nvkeProviderTime = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)

var nvkeProviderName = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9.]*[a-z0-9])?$`)
var nvkeProviderNamespace = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)

func validNVKEProviderName(value *string, namespace bool) bool {
	if value == nil {
		return true
	}
	if namespace {
		return len(*value) <= 63 && nvkeProviderNamespace.MatchString(*value)
	}
	return len(*value) <= 253 && nvkeProviderName.MatchString(*value)
}
func validNVKEProviderCount(value *string, positive bool) bool {
	if value == nil {
		return true
	}
	number, err := strconv.ParseInt(*value, 10, 64)
	return err == nil && number >= 0 && (!positive || number > 0) && strconv.FormatInt(number, 10) == *value
}
func validNVKEProviderDate(value *string) bool {
	if value == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339Nano, *value)
	return err == nil && nvkeProviderTime.MatchString(*value)
}
func validateNVKEProviderHeader(view api.NVKEProviderView, site, op string, version int) error {
	if view.SiteID != site || view.OperationID != op || view.OperationVersion != version || view.ObservedAt == "" || !validNVKEProviderDate(&view.ObservedAt) {
		return errors.New("provider view does not match the reviewed receipt")
	}
	return nil
}
func validateNVKEProviderObservation(row *api.NVKEProviderObservation, receipt *api.NVKEOperation) error {
	invalid := errors.New("provider observation has incomplete or inconsistent facts")
	if row == nil || receipt == nil || receipt.ProviderSource == nil || row.Complete == nil || len(row.Providers) != 2 {
		return invalid
	}
	source := receipt.ProviderSource
	if row.ConnectionID != source.ConnectionID || nvkeGUID(row.ConnectionID) != nil || row.ConnectionVersion < 1 || row.ConnectionVersion != source.ConnectionVersion || row.SourceSiteVersion < 1 || row.SourceSiteVersion != source.SiteVersion || row.UpstreamRevision != source.UpstreamRevision || !nvkeRevisionPattern.MatchString(row.UpstreamRevision) {
		return invalid
	}
	complete := true
	for i, p := range row.Providers {
		if p.Provider != []string{"nico", "launchlayer"}[i] || p.Complete == nil || p.UnconfiguredIsValid == nil || *p.UnconfiguredIsValid != (p.Provider == "launchlayer") || !slices.Contains([]string{"NOT-INSTALLED", "NOT-CONFIGURED", "DEGRADED", "CONFIGURED", "UNKNOWN"}, p.State) || !slices.Contains([]string{"provider_crd_inventory_unreadable", "provider_crds_not_established", "provider_manager_inventory_unreadable", "provider_secret_reference_invalid", "provider_secret_reference_absent", "provider_secret_presence_unreadable", "provider_secret_absent", "provider_endpoint_absent", "provider_manager_not_found", "provider_manager_unavailable", "provider_configuration_present"}, p.Reason) {
			return invalid
		}
		complete = complete && *p.Complete
		if !validNVKEProviderName(p.ManagerNamespace, true) || !validNVKEProviderName(p.ManagerName, false) || !validNVKEProviderName(p.SecretNamespace, true) || !validNVKEProviderName(p.SecretName, false) || !validNVKEProviderCount(p.ReadyReplicas, false) || !validNVKEProviderCount(p.DesiredReplicas, false) {
			return invalid
		}
		if p.State == "UNKNOWN" && *p.Complete {
			return invalid
		}
		if p.State == "NOT-INSTALLED" && (p.CRDsEstablished == nil || *p.CRDsEstablished || p.Credentials != nil) {
			return invalid
		}
		if p.State == "CONFIGURED" && (p.CRDsEstablished == nil || !*p.CRDsEstablished || p.ControllerAvailable == nil || !*p.ControllerAvailable || p.SecretPresent == nil || !*p.SecretPresent || p.ManagerNamespace == nil || p.ManagerName == nil || p.SecretNamespace == nil || p.SecretName == nil || (p.EndpointConfigured != nil && !*p.EndpointConfigured)) {
			return invalid
		}
		h := p.Credentials
		if h == nil {
			continue
		}
		if p.Provider == "launchlayer" || !slices.Contains([]string{"HEALTHY", "FAILING", "UNKNOWN", "STALE", "PENDING", "NO-IDENTITY", "UNSUPPORTED", "UNREADABLE"}, h.Health) || h.AdditionalIdentityCount == nil || *h.AdditionalIdentityCount < 0 || *h.AdditionalIdentityCount > 10000 || !validNVKEProviderName(h.IdentityNamespace, true) || !validNVKEProviderName(h.IdentityName, false) || (h.IdentityNamespace == nil) != (h.IdentityName == nil) || !validNVKEProviderCount(h.IdentityGeneration, true) || !validNVKEProviderCount(h.ObservedGeneration, true) || !validNVKEProviderDate(h.LastCheckedTime) || !validNVKEProviderDate(h.NextCheckTime) {
			return invalid
		}
		if !slices.Contains([]string{"identity_api_not_served", "identity_list_forbidden", "identity_list_unavailable", "no_identity_observes_secret", "invalid_last_checked_time", "last_checked_time_in_future", "provider_check_overdue", "provider_manager_not_found", "identity_outside_manager_namespace", "identity_outside_manager_filter", "provider_manager_unavailable", "current_generation_not_checked", "provider_check_succeeded", "provider_check_failed", "provider_check_unknown"}, h.Reason) || (h.ReadyStatus != nil && !slices.Contains([]string{"True", "False", "Unknown"}, *h.ReadyStatus)) {
			return invalid
		}
		if h.Health == "HEALTHY" || h.Health == "FAILING" {
			status, reason := "True", "provider_check_succeeded"
			if h.Health == "FAILING" {
				status, reason = "False", "provider_check_failed"
			}
			if h.IdentityName == nil || h.IdentityGeneration == nil || h.ObservedGeneration == nil || *h.ObservedGeneration != *h.IdentityGeneration || h.ReadyStatus == nil || *h.ReadyStatus != status || h.LastCheckedTime == nil || h.Reason != reason {
				return invalid
			}
		}
	}
	if complete != *row.Complete {
		return invalid
	}
	return nil
}
func runNVKEProviderView(cmd *cobra.Command, ctx context.Context, c *api.Client, op, kind string) error {
	site := resourceStringFlag(cmd, "site")
	version, _ := cmd.Flags().GetInt("version")
	if nvkeGUID(site) != nil || nvkeGUID(op) != nil || version < 1 {
		return errors.New("use exact site/operation GUIDs and a positive provider operation version")
	}
	_, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("provider observation caller identity unavailable")
	}
	receipt, err := c.NVKEOperation(ctx, op)
	if err != nil {
		return err
	}
	if receipt == nil || receipt.SiteID != site || receipt.Version != version || receipt.OperationKey != nvkeProviderOperationKey || receipt.Status != "SUCCEEDED" || receipt.IsInventory || receipt.ProviderSource == nil {
		return errors.New("use a successful provider receipt at its exact reviewed version")
	}
	row, err := c.NVKEProviderObservation(ctx, site, op, actor, version)
	if err != nil {
		return err
	}
	if row == nil {
		return errors.New("provider observation unavailable")
	}
	if err = validateNVKEProviderHeader(row.NVKEProviderView, site, op, version); err != nil {
		return err
	}
	if err = validateNVKEProviderObservation(row, receipt); err != nil {
		return err
	}
	if kind == "observation" {
		return nvkeJSON(cmd, row)
	}
	provider := resourceStringFlag(cmd, "provider")
	search := resourceStringFlag(cmd, "search")
	if (provider != "nico" && provider != "launchlayer") || !utf8.ValidString(search) || utf8.RuneCountInString(search) > 200 {
		return errors.New("use a known provider and UTF-8 search of at most 200 characters")
	}
	after := resourceStringFlag(cmd, "after")
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 1 || limit > 100 {
		return errors.New("--limit must be between 1 and 100 for provider identities")
	}
	page, err := c.NVKEProviderIdentities(ctx, site, op, actor, version, provider, after, limit, search)
	if err != nil {
		return err
	}
	if page == nil {
		return errors.New("provider identity page unavailable")
	}
	if err = validateNVKEProviderHeader(page.NVKEProviderView, site, op, version); err != nil {
		return err
	}
	count := 0
	primary := ""
	for _, p := range row.Providers {
		if p.Provider == provider && p.Credentials != nil {
			count = *p.Credentials.AdditionalIdentityCount
			if p.Credentials.IdentityName != nil {
				primary = *p.Credentials.IdentityNamespace + "/" + *p.Credentials.IdentityName
			}
		}
	}
	if page.Provider != provider || page.ObservedAt != row.ObservedAt || page.Items == nil || len(page.Items) > limit || page.TotalCount < len(page.Items) || page.TotalCount > count || (after == "" && page.NextCursor == nil && page.TotalCount > len(page.Items)) || (search == "" && page.TotalCount != count) || (page.NextCursor != nil && (*page.NextCursor == "" || len(page.Items) == 0)) {
		return errors.New("provider identity page inconsistent with the saved observation")
	}
	seen := map[string]bool{}
	for _, item := range page.Items {
		if !validNVKEProviderName(&item.Namespace, true) || !validNVKEProviderName(&item.Name, false) || item.Key != item.Namespace+"/"+item.Name || seen[item.Key] || item.Key == primary {
			return errors.New("provider identity page contains invalid identities")
		}
		seen[item.Key] = true
	}
	return nvkeJSON(cmd, page)
}

func sameNVKEProviderSource(left, right *api.NVKEProviderSource) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
