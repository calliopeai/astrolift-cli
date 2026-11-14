package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/spf13/cobra"
)

func newNVKEMetricsCommand() *cobra.Command {
	group := &cobra.Command{Use: "metrics", Short: "Configure explicit metrics authority and observe credentials", Long: `Use a separate explicit metrics URL and write-only bearer/TLS credentials.
No address or credential is inferred from controller or datastore connections.
Verification proves readable pinned metrics, not collection success or cluster health.
Observations derive their cluster selector from a reviewed controller get-cluster receipt.
Historical views preserve unknowns, scrape time and exact reported decimal values.`}
	catalog := &cobra.Command{Use: "catalog", Args: cobra.NoArgs, Short: "Show the pinned credential metric, credential-name and state vocabulary", RunE: nvkeScoped(runNVKEMetricsCatalog)}
	group.AddCommand(catalog)
	get := &cobra.Command{Use: "get", Args: cobra.NoArgs, Short: "Read metrics authority metadata without credentials", RunE: nvkeScoped(runNVKEMetricsGet)}
	nvkeReadSiteFlags(get)
	group.AddCommand(get)
	configure := &cobra.Command{Use: "configure", Args: cobra.NoArgs, Short: "Configure a separately reviewed metrics authority", RunE: nvkeScoped(runNVKEMetricsConfigure)}
	nvkeMetricsWriteFlags(configure, true)
	configure.Flags().String("endpoint", "", "Explicit full metrics scrape URL (required)")
	_ = configure.MarkFlagRequired("endpoint")
	configure.Flags().Bool("allow-http", false, "Explicitly allow HTTP for an evaluation listener")
	configure.Flags().Int("connection-timeout", 30, "Scrape timeout seconds (1–300)")
	configure.Flags().String("credentials-file", "", "Private JSON token/caPem/clientCertificatePem/clientKeyPem bundle, or - for stdin")
	configure.Flags().Bool("clear-credentials", false, "Explicitly clear metrics credentials; omission preserves them")
	group.AddCommand(configure)
	for _, verb := range []string{"verify", "enable", "disable", "remove"} {
		selected := verb
		leaf := &cobra.Command{Use: verb, Args: cobra.NoArgs, Short: verb + " the reviewed metrics authority", RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
			return runNVKEMetricsReview(cmd, ctx, c, selected)
		})}
		nvkeMetricsWriteFlags(leaf, false)
		group.AddCommand(leaf)
	}
	group.AddCommand(newNVKECredentialRequestCommand(false), newNVKECredentialRequestCommand(true))
	for _, view := range []string{"observation", "certificates", "samples"} {
		selected := view
		leaf := &cobra.Command{Use: view + " OPERATION_GUID", Args: cobra.ExactArgs(1), Short: "Read historical credential " + view + " at an exact receipt version", RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
			return runNVKECredentialView(cmd, ctx, c, args[0], selected)
		})}
		nvkeReadSiteFlags(leaf)
		leaf.Flags().Int("version", 0, "Reviewed successful credential operation version (required)")
		_ = leaf.MarkFlagRequired("version")
		if view != "observation" {
			nvkePageFlags(leaf)
			leaf.Flags().String("search", "", "Search the complete saved observation on the server")
		}
		group.AddCommand(leaf)
	}
	return group
}

func runNVKEMetricsCatalog(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
	row, err := c.NVKEMetricsCatalog(ctx)
	if err != nil {
		return err
	}
	if row.Revision != nvkecatalog.Pinned().Revision {
		return errors.New("credential metric catalog revision requires review")
	}
	return nvkeJSON(cmd, row)
}

func nvkeMetricsWriteFlags(cmd *cobra.Command, allowNew bool) {
	nvkeAccessWriteFlags(cmd)
	cmd.Flags().Int("authority-version", 0, "Reviewed metrics authority version (0 for a new authority)")
	if !allowNew {
		_ = cmd.MarkFlagRequired("authority-version")
	}
}

func nvkeMetricsReview(cmd *cobra.Command, c *api.Client, allowNew bool) (api.NVKEMetricsReview, error) {
	site := resourceStringFlag(cmd, "site")
	siteVersion, _ := cmd.Flags().GetInt("site-version")
	version, _ := cmd.Flags().GetInt("authority-version")
	if nvkeGUID(site) != nil || siteVersion < 1 || version < 0 || (!allowNew && version == 0) {
		return api.NVKEMetricsReview{}, errors.New("use an exact site GUID and reviewed site/metrics authority versions")
	}
	_, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return api.NVKEMetricsReview{}, errors.New("metrics caller/server/organization identity unavailable")
	}
	return api.NVKEMetricsReview{SiteID: site, ExpectedActorID: actor, ExpectedSiteVersion: siteVersion, ExpectedVersion: version}, nil
}

func validNVKEMetricsURL(endpoint string, allowHTTP bool) bool {
	u, err := url.Parse(endpoint)
	return err == nil && len(endpoint) > 0 && len(endpoint) <= 2048 && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.ContainsAny(endpoint, "\r\n\t ") && (u.Scheme == "https" || (allowHTTP && u.Scheme == "http"))
}

func validateNVKEMetricsReply(row *api.NVKEMetricsAuthority, review api.NVKEMetricsReview, verb string) error {
	if row == nil || nvkeGUID(row.ID) != nil || row.SiteID != review.SiteID || row.Version < 1 || row.SiteVersion < 1 || row.TimeoutSeconds < 1 || row.TimeoutSeconds > 300 || !validNVKEMetricsURL(row.Endpoint, row.AllowHTTP) {
		return errors.New("metrics authority reply incomplete or mismatched; inspect metrics get")
	}
	if verb == "get" {
		if row.DeletedAt != nil {
			return errors.New("metrics read returned a removed authority")
		}
		return nil
	}
	if verb == "verify" {
		if row.Version != review.ExpectedVersion || row.SiteVersion != review.ExpectedSiteVersion || row.VerifiedAt == nil || row.VerificationCode != "METRICS_READ" || !row.IsEnabled || row.DeletedAt != nil {
			return errors.New("metrics read verification was not confirmed")
		}
		if _, err := time.Parse(time.RFC3339Nano, *row.VerifiedAt); err != nil {
			return errors.New("metrics verification time unavailable")
		}
		return nil
	}
	if row.Version <= review.ExpectedVersion || row.SiteVersion <= review.ExpectedSiteVersion || (verb == "remove") != (row.DeletedAt != nil) || row.VerifiedAt != nil || row.VerificationCode != "" || (verb == "configure" || verb == "enable") != row.IsEnabled {
		return errors.New("metrics authority change was not confirmed; inspect metrics get")
	}
	return nil
}

func runNVKEMetricsGet(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
	site := resourceStringFlag(cmd, "site")
	if err := nvkeGUID(site); err != nil {
		return err
	}
	row, err := c.NVKEMetricsAuthority(ctx, site)
	if err != nil {
		return err
	}
	if row != nil {
		if err = validateNVKEMetricsReply(row, api.NVKEMetricsReview{SiteID: site}, "get"); err != nil {
			return err
		}
	}
	return nvkeJSON(cmd, row)
}

func runNVKEMetricsConfigure(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
	file, clear := resourceStringFlag(cmd, "credentials-file"), boolFlag(cmd, "clear-credentials")
	endpoint, allowHTTP := resourceStringFlag(cmd, "endpoint"), boolFlag(cmd, "allow-http")
	timeout, _ := cmd.Flags().GetInt("connection-timeout")
	if (file != "" && clear) || timeout < 1 || timeout > 300 || !validNVKEMetricsURL(endpoint, allowHTTP) {
		return errors.New("use an explicit metrics URL without embedded credentials/query/fragment, timeout 1–300 and one credential source; HTTP requires --allow-http")
	}
	var credentials *api.NVKECredentialsInput
	if clear {
		credentials = &api.NVKECredentialsInput{}
	}
	if file != "" {
		data, err := readNVKEJSON(cmd, file, 2*1024*1024)
		if err != nil {
			return err
		}
		var fields map[string]any
		if json.Unmarshal(data, &fields) != nil {
			return errors.New("invalid metrics credential document")
		}
		for _, value := range fields {
			if _, ok := value.(string); !ok {
				return errors.New("metrics credential values must be strings")
			}
		}
		credentials = &api.NVKECredentialsInput{}
		if decodeNVKEStrict(data, credentials) != nil || (credentials.ClientCertificatePEM == "") != (credentials.ClientKeyPEM == "") {
			return errors.New("invalid metrics credential document; pair client certificate and key")
		}
	}
	review, err := nvkeMetricsReview(cmd, c, true)
	if err != nil {
		return err
	}
	row, err := c.ConfigureNVKEMetricsAuthority(ctx, api.NVKEMetricsConfigureInput{NVKEMetricsReview: review, Endpoint: endpoint, AllowHTTP: allowHTTP, TimeoutSeconds: timeout, Credentials: credentials})
	if err != nil {
		return fmt.Errorf("%w; inspect metrics get before reviewing another configuration", err)
	}
	if err = validateNVKEMetricsReply(row, review, "configure"); err != nil {
		return err
	}
	if row.Endpoint != endpoint || row.AllowHTTP != allowHTTP || row.TimeoutSeconds != timeout {
		return errors.New("metrics configuration target was not confirmed; inspect metrics get")
	}
	if credentials != nil && row.HasCredentials != (credentials.Token != "" || credentials.CAPEM != "" || credentials.ClientCertificatePEM != "" || credentials.ClientKeyPEM != "") {
		return errors.New("metrics credential state was not confirmed; inspect metrics get")
	}
	return nvkeJSON(cmd, row)
}

func runNVKEMetricsReview(cmd *cobra.Command, ctx context.Context, c *api.Client, verb string) error {
	review, err := nvkeMetricsReview(cmd, c, false)
	if err != nil {
		return err
	}
	var row *api.NVKEMetricsAuthority
	switch verb {
	case "verify":
		row, err = c.VerifyNVKEMetricsAuthority(ctx, review)
	case "remove":
		row, err = c.RemoveNVKEMetricsAuthority(ctx, review)
	case "enable", "disable":
		row, err = c.SetNVKEMetricsAuthorityEnabled(ctx, review, verb == "enable")
	default:
		return errors.New("unknown metrics authority action")
	}
	if err != nil {
		return fmt.Errorf("%w; inspect metrics get before reviewing another change", err)
	}
	if err = validateNVKEMetricsReply(row, review, verb); err != nil {
		return err
	}
	return nvkeJSON(cmd, row)
}

func validateNVKECredentialView(view api.NVKECredentialView, site, op string, version int) error {
	if view.SiteID != site || view.OperationID != op || view.OperationVersion != version {
		return errors.New("credential observation reply does not match its reviewed site/operation/version")
	}
	if _, err := time.Parse(time.RFC3339Nano, view.ScrapedAt); err != nil {
		return errors.New("credential observation scrape time unavailable")
	}
	return nil
}

func runNVKECredentialView(cmd *cobra.Command, ctx context.Context, c *api.Client, op, kind string) error {
	site := resourceStringFlag(cmd, "site")
	version, _ := cmd.Flags().GetInt("version")
	if nvkeGUID(site) != nil || nvkeGUID(op) != nil || version < 1 {
		return errors.New("use exact site/operation GUIDs and a positive successful credential operation version")
	}
	_, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return errors.New("credential observation caller identity unavailable")
	}
	if kind == "observation" {
		row, readErr := c.NVKECredentialObservation(ctx, site, op, actor, version)
		if readErr != nil {
			return readErr
		}
		if err = validateNVKECredentialView(row.NVKECredentialView, site, op, version); err != nil {
			return err
		}
		if nvkeGUID(row.ClusterID) != nil || nvkeGUID(row.SourceOperationID) != nil || nvkeGUID(row.AuthorityID) != nil || row.SourceSiteVersion < 1 || row.SourceVersion < 1 || row.AuthorityVersion < 1 || row.ClusterReported == nil {
			return errors.New("credential observation source metadata incomplete")
		}
		return nvkeJSON(cmd, row)
	}
	after, limit, err := nvkePage(cmd)
	if err != nil {
		return err
	}
	search := resourceStringFlag(cmd, "search")
	if kind == "samples" {
		row, readErr := c.NVKECredentialMetrics(ctx, site, op, actor, version, after, limit, search)
		if readErr != nil {
			return readErr
		}
		if err = validateNVKECredentialView(row.NVKECredentialView, site, op, version); err != nil {
			return err
		}
		if row.Items == nil || len(row.Items) > limit || row.TotalCount < len(row.Items) {
			return errors.New("credential metrics page incomplete")
		}
		return nvkeJSON(cmd, row)
	}
	row, err := c.NVKECredentialCertificates(ctx, site, op, actor, version, after, limit, search)
	if err != nil {
		return err
	}
	if err = validateNVKECredentialView(row.NVKECredentialView, site, op, version); err != nil {
		return err
	}
	if row.Items == nil || len(row.Items) > limit || row.TotalCount < len(row.Items) {
		return errors.New("credential certificate page incomplete")
	}
	return nvkeJSON(cmd, row)
}
