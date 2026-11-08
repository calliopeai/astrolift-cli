package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/spf13/cobra"
)

func newNVKEAgentCommand() *cobra.Command {
	group := &cobra.Command{Use: "agent", Short: "Manage dedicated site operator agents through Astro", Long: `Configure a separate site-bound mTLS executor and submit all declared native tooling
through Astro's encrypted durable outbox. Native tools run on the dedicated agent.
Keep private recovery files after uncertain admission. Process completion requires
separate infrastructure health verification. Cancelling local waits does not cancel remote work.`}
	group.AddCommand(&cobra.Command{Use: "catalog", Args: cobra.NoArgs, Short: "Show every supported remote command and its full option contract", RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
		row, err := c.NVKEAgentCatalog(ctx)
		if err != nil {
			return err
		}
		if row.Revision != nvkecatalog.Pinned().Revision || len(row.Commands) == 0 {
			return errors.New("operator agent catalog revision requires review")
		}
		return nvkeJSON(cmd, row)
	})})
	get := &cobra.Command{Use: "get", Args: cobra.NoArgs, Short: "Read site agent metadata without credentials", RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
		site := resourceStringFlag(cmd, "site")
		if nvkeGUID(site) != nil {
			return errors.New("use an exact site GUID")
		}
		row, err := c.NVKEAgentAuthority(ctx, site)
		if err != nil {
			return err
		}
		if row != nil {
			if err = validateNVKEAgentReply(row, api.NVKEAgentReview{SiteID: site}, "get"); err != nil {
				return err
			}
		}
		return nvkeJSON(cmd, row)
	})}
	nvkeReadSiteFlags(get)
	group.AddCommand(get)
	configure := &cobra.Command{Use: "configure", Args: cobra.NoArgs, Short: "Configure reviewed agent instance, bundle pin and write-only mTLS credentials", RunE: nvkeScoped(runNVKEAgentConfigure)}
	nvkeAgentWriteFlags(configure, true)
	configure.Flags().String("endpoint", "", "Explicit HTTPS agent base endpoint (required)")
	configure.Flags().String("instance", "", "Persistent agent instance GUID (required)")
	configure.Flags().String("bundle-sha256", "", "Reviewed bundle manifest SHA-256 (required)")
	configure.Flags().String("credentials-file", "", "Private caPem/clientCertificatePem/clientKeyPem JSON, or - for stdin; omission preserves existing credentials")
	configure.Flags().Int("connection-timeout", 30, "Agent HTTP timeout seconds (1–300)")
	for _, flag := range []string{"endpoint", "instance", "bundle-sha256"} {
		_ = configure.MarkFlagRequired(flag)
	}
	group.AddCommand(configure)
	for _, verb := range []string{"verify", "enable", "disable", "remove"} {
		selected := verb
		leaf := &cobra.Command{Use: verb, Args: cobra.NoArgs, Short: verb + " the reviewed agent authority", RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
			return runNVKEAgentReview(cmd, ctx, c, selected)
		})}
		nvkeAgentWriteFlags(leaf, false)
		group.AddCommand(leaf)
	}
	group.AddCommand(newNVKEAgentRequestCommand(false), newNVKEAgentRequestCommand(true), newNVKEAgentReconcileCommand(), newNVKEAgentStopCommand(), newNVKEAgentArtifactCommand())
	return group
}

func nvkeAgentWriteFlags(cmd *cobra.Command, allowNew bool) {
	nvkeAccessWriteFlags(cmd)
	cmd.Flags().Int("authority-version", 0, "Reviewed agent authority version; 0 for a new authority")
	if !allowNew {
		_ = cmd.MarkFlagRequired("authority-version")
	}
}
func nvkeAgentReview(cmd *cobra.Command, c *api.Client, allowNew bool) (api.NVKEAgentReview, error) {
	site := resourceStringFlag(cmd, "site")
	siteVersion, _ := cmd.Flags().GetInt("site-version")
	version, _ := cmd.Flags().GetInt("authority-version")
	if nvkeGUID(site) != nil || siteVersion < 1 || version < 0 || (!allowNew && version == 0) {
		return api.NVKEAgentReview{}, errors.New("use an exact site GUID and reviewed positive site/agent versions")
	}
	_, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return api.NVKEAgentReview{}, errors.New("operator agent caller/server/organization unavailable")
	}
	return api.NVKEAgentReview{SiteID: site, ExpectedActorID: actor, ExpectedSiteVersion: siteVersion, ExpectedVersion: version}, nil
}
func validNVKEAgentURL(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && len(endpoint) > 0 && len(endpoint) <= 2048 && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && (u.Path == "" || u.Path == "/") && !strings.ContainsAny(endpoint, "\\%\r\n\t ")
}
func validateNVKEAgentReply(row *api.NVKEAgentAuthority, review api.NVKEAgentReview, verb string) error {
	if row == nil || nvkeGUID(row.ID) != nil || nvkeGUID(row.InstanceID) != nil || row.SiteID != review.SiteID || row.Version < 1 || row.SiteVersion < 1 || !nvkeDigestPattern.MatchString(row.BundleManifestSHA256) || !validNVKEAgentURL(row.Endpoint) || row.TimeoutSeconds < 1 || row.TimeoutSeconds > 300 || !row.HasCredentials {
		return errors.New("operator agent authority reply incomplete or mismatched; inspect agent get")
	}
	if verb == "get" {
		if row.DeletedAt != nil {
			return errors.New("operator agent read returned a removed authority")
		}
		return nil
	}
	if verb == "verify" {
		if row.Version != review.ExpectedVersion || row.SiteVersion != review.ExpectedSiteVersion || !row.IsEnabled || row.VerifiedAt == nil || row.VerificationCode != "AGENT_VERIFIED" || row.DeletedAt != nil {
			return errors.New("operator agent verification was not confirmed")
		}
		if _, err := time.Parse(time.RFC3339Nano, *row.VerifiedAt); err != nil {
			return errors.New("operator agent verification time unavailable")
		}
		return nil
	}
	if row.Version <= review.ExpectedVersion || row.SiteVersion <= review.ExpectedSiteVersion || row.VerifiedAt != nil || row.VerificationCode != "" || (verb == "remove") != (row.DeletedAt != nil) || (verb == "configure" || verb == "enable") != row.IsEnabled {
		return errors.New("operator agent authority change was not confirmed; inspect agent get")
	}
	return nil
}
func runNVKEAgentConfigure(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
	endpoint, instance, digest := resourceStringFlag(cmd, "endpoint"), resourceStringFlag(cmd, "instance"), resourceStringFlag(cmd, "bundle-sha256")
	timeout, _ := cmd.Flags().GetInt("connection-timeout")
	if !validNVKEAgentURL(endpoint) || nvkeGUID(instance) != nil || !nvkeDigestPattern.MatchString(digest) || timeout < 1 || timeout > 300 {
		return errors.New("use a bounded HTTPS base URL, exact instance GUID, bundle SHA-256 and timeout 1–300")
	}
	var credentials *api.NVKEAgentCredentials
	if file := resourceStringFlag(cmd, "credentials-file"); file != "" {
		data, err := readNVKEJSON(cmd, file, 1024*1024)
		if err != nil {
			return err
		}
		credentials = &api.NVKEAgentCredentials{}
		if decodeNVKEStrict(data, credentials) != nil || credentials.ClientCertificatePEM == "" || credentials.ClientKeyPEM == "" {
			return errors.New("use a private operator CA and paired mTLS certificate/key document")
		}
	}
	review, err := nvkeAgentReview(cmd, c, true)
	if err != nil {
		return err
	}
	row, err := c.ConfigureNVKEAgentAuthority(ctx, api.NVKEAgentConfigureInput{NVKEAgentReview: review, Endpoint: endpoint, InstanceID: instance, BundleManifestSHA256: digest, TimeoutSeconds: timeout, Credentials: credentials})
	if err != nil {
		return fmt.Errorf("%w; inspect agent get before reviewing another configuration", err)
	}
	if err = validateNVKEAgentReply(row, review, "configure"); err != nil {
		return err
	}
	if row.Endpoint != strings.TrimRight(endpoint, "/") || row.InstanceID != instance || row.BundleManifestSHA256 != digest || row.TimeoutSeconds != timeout {
		return errors.New("operator agent configuration target was not confirmed; inspect agent get")
	}
	return nvkeJSON(cmd, row)
}
func runNVKEAgentReview(cmd *cobra.Command, ctx context.Context, c *api.Client, verb string) error {
	review, err := nvkeAgentReview(cmd, c, false)
	if err != nil {
		return err
	}
	var row *api.NVKEAgentAuthority
	switch verb {
	case "verify":
		row, err = c.VerifyNVKEAgentAuthority(ctx, review)
	case "remove":
		row, err = c.RemoveNVKEAgentAuthority(ctx, review)
	case "enable", "disable":
		row, err = c.SetNVKEAgentAuthorityEnabled(ctx, review, verb == "enable")
	default:
		return errors.New("unknown operator agent authority action")
	}
	if err != nil {
		return fmt.Errorf("%w; inspect agent get before reviewing another change", err)
	}
	if err = validateNVKEAgentReply(row, review, verb); err != nil {
		return err
	}
	return nvkeJSON(cmd, row)
}
