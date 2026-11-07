package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

func newNVKETemporalCommand() *cobra.Command {
	group := &cobra.Command{Use: "temporal", Short: "Configure the site's separate native Temporal authority", Long: `Configure the native controller's Temporal address, namespace and queue explicitly.
There is no fallback to Astro's Temporal service or the site's HTTP credentials.
Credentials are write-only private JSON files or stdin, never command arguments.
Namespace verification does not prove a worker is polling the configured queue.`}
	get := &cobra.Command{Use: "get", Args: cobra.NoArgs, Short: "Read native Temporal metadata without credentials", RunE: nvkeScoped(runNVKETemporalGet)}
	nvkeReadSiteFlags(get)
	group.AddCommand(get)
	configure := &cobra.Command{Use: "configure", Args: cobra.NoArgs, Short: "Configure an explicit native authority at its reviewed versions", RunE: nvkeScoped(runNVKETemporalConfigure)}
	nvkeTemporalWriteFlags(configure, true)
	for _, name := range []string{"address", "namespace", "task-queue"} {
		configure.Flags().String(name, "", "Explicit native Temporal "+name+" (required)")
		_ = configure.MarkFlagRequired(name)
	}
	configure.Flags().String("server-name", "", "TLS server name override")
	configure.Flags().Bool("plaintext", false, "Explicitly use plaintext with no credentials or TLS server name")
	configure.Flags().Int("connection-timeout", 30, "Connection/RPC timeout in seconds (1–300)")
	configure.Flags().String("credentials-file", "", "Private JSON apiKey/caPem/clientCertificatePem/clientKeyPem bundle, or - for stdin")
	configure.Flags().Bool("clear-credentials", false, "Explicitly clear the native credential bundle")
	group.AddCommand(configure)
	for _, verb := range []string{"verify", "enable", "disable", "remove"} {
		selected := verb
		leaf := &cobra.Command{Use: verb, Args: cobra.NoArgs, Short: verb + " the reviewed native Temporal authority", RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
			return runNVKETemporalReview(cmd, ctx, c, selected)
		})}
		nvkeTemporalWriteFlags(leaf, false)
		group.AddCommand(leaf)
	}
	return group
}

func nvkeTemporalWriteFlags(cmd *cobra.Command, allowNew bool) {
	nvkeAccessWriteFlags(cmd)
	cmd.Flags().Int("authority-version", 0, "Reviewed authority version (0 for a new authority)")
	if !allowNew {
		_ = cmd.MarkFlagRequired("authority-version")
	}
}

func nvkeTemporalReview(cmd *cobra.Command, c *api.Client, allowNew bool) (api.NVKETemporalReview, error) {
	site := resourceStringFlag(cmd, "site")
	siteVersion, _ := cmd.Flags().GetInt("site-version")
	version, _ := cmd.Flags().GetInt("authority-version")
	if nvkeGUID(site) != nil || siteVersion < 1 || version < 0 || (!allowNew && version == 0) {
		return api.NVKETemporalReview{}, errors.New("use an exact site GUID and valid reviewed site/authority versions")
	}
	_, actor, err := reviewedRequestScope(cmd, c)
	if err != nil {
		return api.NVKETemporalReview{}, errors.New("native Temporal caller/server/organization identity could not be verified")
	}
	return api.NVKETemporalReview{SiteID: site, ExpectedActorID: actor, ExpectedSiteVersion: siteVersion, ExpectedVersion: version}, nil
}

func validateNVKETemporalReply(row *api.NVKETemporalAuthority, review api.NVKETemporalReview, verb string) error {
	if row == nil || nvkeGUID(row.ID) != nil || row.SiteID != review.SiteID || row.Version < 1 || row.SiteVersion < 1 || row.TimeoutSeconds < 1 || row.TimeoutSeconds > 300 {
		return errors.New("native Temporal reply did not confirm the reviewed authority; inspect temporal get")
	}
	if verb == "get" {
		if row.DeletedAt != nil {
			return errors.New("native Temporal read returned a removed authority")
		}
		return nil
	}
	if verb == "verify" {
		if row.Version != review.ExpectedVersion || row.SiteVersion != review.ExpectedSiteVersion || row.VerifiedAt == nil || row.VerificationCode != "NAMESPACE_VERIFIED" || !row.IsEnabled || row.DeletedAt != nil {
			return errors.New("native Temporal namespace verification was not confirmed")
		}
		return nil
	}
	if row.Version <= review.ExpectedVersion || row.SiteVersion <= review.ExpectedSiteVersion || (verb == "remove") != (row.DeletedAt != nil) || row.VerifiedAt != nil || row.VerificationCode != "" {
		return errors.New("native Temporal reply did not confirm the reviewed change; inspect temporal get")
	}
	if (verb == "configure" || verb == "enable") != row.IsEnabled {
		return errors.New("native Temporal enable state was not confirmed")
	}
	return nil
}

func runNVKETemporalGet(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
	site := resourceStringFlag(cmd, "site")
	if err := nvkeGUID(site); err != nil {
		return err
	}
	row, err := c.NVKETemporalAuthority(ctx, site)
	if err != nil {
		return err
	}
	if row != nil {
		if err = validateNVKETemporalReply(row, api.NVKETemporalReview{SiteID: site}, "get"); err != nil {
			return err
		}
	}
	return nvkeJSON(cmd, row)
}

func runNVKETemporalConfigure(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
	file := resourceStringFlag(cmd, "credentials-file")
	clear := boolFlag(cmd, "clear-credentials")
	plaintext := boolFlag(cmd, "plaintext")
	serverName := resourceStringFlag(cmd, "server-name")
	timeout, _ := cmd.Flags().GetInt("connection-timeout")
	if (file != "" && clear) || timeout < 1 || timeout > 300 || (plaintext && (file != "" || serverName != "")) {
		return errors.New("use one credential source, timeout 1–300, and TLS when credentials or server-name are provided")
	}
	for _, name := range []string{"address", "namespace", "task-queue"} {
		if strings.TrimSpace(resourceStringFlag(cmd, name)) == "" {
			return errors.New("explicit native address, namespace and task-queue are required")
		}
	}
	var credentials *api.NVKETemporalCredentials
	if clear {
		credentials = &api.NVKETemporalCredentials{}
	}
	if file != "" {
		data, err := readNVKEJSON(cmd, file, 2*1024*1024)
		if err != nil {
			return err
		}
		var fields map[string]any
		if json.Unmarshal(data, &fields) != nil {
			return errors.New("invalid native Temporal credential document")
		}
		for _, value := range fields {
			if _, ok := value.(string); !ok {
				return errors.New("native Temporal credential values must be strings")
			}
		}
		credentials = &api.NVKETemporalCredentials{}
		if decodeNVKEStrict(data, credentials) != nil || (credentials.ClientCertificatePEM == "") != (credentials.ClientKeyPEM == "") {
			return errors.New("invalid native Temporal credential document; pair client certificate and key")
		}
	}
	review, err := nvkeTemporalReview(cmd, c, true)
	if err != nil {
		return err
	}
	input := api.NVKETemporalConfigureInput{NVKETemporalReview: review, Address: resourceStringFlag(cmd, "address"), Namespace: resourceStringFlag(cmd, "namespace"), TaskQueue: resourceStringFlag(cmd, "task-queue"), ServerName: serverName, UseTLS: !plaintext, TimeoutSeconds: timeout, Credentials: credentials}
	row, err := c.ConfigureNVKETemporalAuthority(ctx, input)
	if err != nil {
		return fmt.Errorf("%w; inspect temporal get before reviewing another configuration", err)
	}
	if err = validateNVKETemporalReply(row, review, "configure"); err != nil {
		return err
	}
	if row.Namespace != input.Namespace || row.TaskQueue != input.TaskQueue || row.UseTLS != input.UseTLS || row.TimeoutSeconds != input.TimeoutSeconds || (!row.UseTLS && row.HasCredentials) {
		return errors.New("native Temporal configuration reply did not confirm the requested target; inspect temporal get")
	}
	if credentials != nil {
		hasCredentials := credentials.APIKey != "" || credentials.CAPEM != "" || credentials.ClientCertificatePEM != "" || credentials.ClientKeyPEM != ""
		if row.HasCredentials != hasCredentials {
			return errors.New("native Temporal credential state was not confirmed; inspect temporal get")
		}
	}
	return nvkeJSON(cmd, row)
}

func runNVKETemporalReview(cmd *cobra.Command, ctx context.Context, c *api.Client, verb string) error {
	review, err := nvkeTemporalReview(cmd, c, false)
	if err != nil {
		return err
	}
	var row *api.NVKETemporalAuthority
	switch verb {
	case "verify":
		row, err = c.VerifyNVKETemporalAuthority(ctx, review)
	case "remove":
		row, err = c.RemoveNVKETemporalAuthority(ctx, review)
	case "enable", "disable":
		row, err = c.SetNVKETemporalAuthorityEnabled(ctx, review, verb == "enable")
	default:
		return errors.New("unknown native Temporal review action")
	}
	if err != nil {
		return fmt.Errorf("%w; inspect temporal get before reviewing another change", err)
	}
	if err = validateNVKETemporalReply(row, review, verb); err != nil {
		return err
	}
	return nvkeJSON(cmd, row)
}
