package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(newNVKECommand()) }

func nvkeJSON(cmd *cobra.Command, v any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}
func nvkePrintOperation(cmd *cobra.Command, row *api.NVKEOperation) error {
	if boolFlag(cmd, "json") {
		return nvkeJSON(cmd, row)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s  %s\n", row.ID, row.OperationKey, row.Status, row.Stage)
	switch row.Status {
	case "QUEUED", "RUNNING", "RECONCILING", "AWAITING_READY":
		fmt.Fprintln(cmd.ErrOrStderr(), "Acceptance is not readiness. Inspect this operation or use operation wait.")
	case "UNCERTAIN":
		fmt.Fprintln(cmd.ErrOrStderr(), "Outcome requires reconciliation. Preserve the original recovery file and inspect operation metadata.")
	}
	return nil
}
func nvkeGUID(value string) error {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil || parsed.String() != value {
		return errors.New("use an exact canonical nonzero GUID")
	}
	return nil
}
func nvkePage(cmd *cobra.Command) (string, int, error) {
	after, _ := cmd.Flags().GetString("after")
	limit, _ := cmd.Flags().GetInt("limit")
	if limit < 1 || limit > 200 {
		return "", 0, errors.New("--limit must be between 1 and 200")
	}
	return after, limit, nil
}
func nvkePageFlags(cmd *cobra.Command) {
	cmd.Flags().String("after", "", "Opaque cursor from the prior page")
	cmd.Flags().Int("limit", 50, "Page size (1–200)")
}
func nvkeScoped(run func(*cobra.Command, context.Context, *api.Client, []string) error) func(*cobra.Command, []string) error {
	return workflowOrgScopedRunE(run)
}
func newNVKECommand() *cobra.Command {
	root := &cobra.Command{Use: "nvke", Short: "Manage organization-owned NVIDIA Kubernetes Engine sites and operations", Long: `Manage NVKE through the Astrolift control plane. The server owns native validation,
permissions and execution. Native scripts, provider fields, opaque resource versions
and all pinned schemas remain available through JSON request documents.

Use catalog for the connected server's schemas and permission mapping. Requests keep
private recovery files before admission; preserve them after an uncertain reply.
Accepted and awaiting-ready operations are not finished infrastructure changes.`}
	catalog := &cobra.Command{Use: "catalog", Short: "Show the connected server's complete schemas and operation catalog", Args: cobra.NoArgs, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
		contract, err := c.NVKEContract(ctx)
		if err != nil {
			return err
		}
		return nvkeJSON(cmd, contract)
	})}
	root.AddCommand(catalog, newNVKESiteCommand(), newNVKEOperationCommand(), newNVKEResourceCommand(), newNVKEAccessCommand(), newNVKEToolingCommand())
	// Full pinned native vocabulary is discoverable offline. Leaf commands use
	// the same durable admission path; they never invoke an upstream executable.
	pinned := nvkecatalog.Pinned()
	for _, surface := range []string{"controller", "tenant", "operator", "datastore"} {
		group := &cobra.Command{Use: surface, Short: "Native " + surface + " operations (all fields via --file)"}
		for _, item := range pinned.Actions {
			if item.Surface != surface {
				continue
			}
			selected := item
			key := strings.TrimPrefix(item.Key, surface+".")
			leaf := newNVKERequestCommand(nativeNVKEName(key), selected.Key)
			leaf.Aliases = []string{key}
			leaf.Short = selected.Verb + " via " + selected.Key + " (" + selected.Permission + ")"
			group.AddCommand(leaf)
		}
		root.AddCommand(group)
	}
	return root
}

var nvkeAcronym = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
var nvkeCamel = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func nativeNVKEName(value string) string {
	value = nvkeAcronym.ReplaceAllString(value, "${1}-${2}")
	return strings.ToLower(nvkeCamel.ReplaceAllString(value, "${1}-${2}"))
}
func newNVKESiteCommand() *cobra.Command {
	group := &cobra.Command{Use: "site", Aliases: []string{"sites"}, Short: "Register and configure organization-owned sites"}
	list := &cobra.Command{Use: "list", Short: "List a page of sites", Args: cobra.NoArgs, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
		after, limit, err := nvkePage(cmd)
		if err != nil {
			return err
		}
		page, err := c.NVKESites(ctx, after, limit)
		if err != nil {
			return err
		}
		return nvkeJSON(cmd, page)
	})}
	nvkePageFlags(list)
	get := &cobra.Command{Use: "get SITE_GUID", Short: "Show one site and its connection metadata", Args: cobra.ExactArgs(1), RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
		if err := nvkeGUID(args[0]); err != nil {
			return err
		}
		site, err := c.NVKESite(ctx, args[0])
		if err != nil {
			return err
		}
		return nvkeJSON(cmd, site)
	})}
	create := &cobra.Command{Use: "create", Short: "Register a site without provisioning remote infrastructure", Args: cobra.NoArgs, RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, _ []string) error {
		site, err := c.CreateNVKESite(ctx, resourceStringFlag(cmd, "name"), resourceStringFlag(cmd, "slug"), resourceStringFlag(cmd, "namespace"))
		if err != nil {
			return err
		}
		return nvkeJSON(cmd, site)
	})}
	for _, name := range []string{"name", "slug", "namespace"} {
		create.Flags().String(name, "", "Site "+name+" (required)")
		_ = create.MarkFlagRequired(name)
	}
	configure := &cobra.Command{Use: "configure SITE_GUID SURFACE", Short: "Configure a controller/tenant/operator/datastore connection", Long: `Configure one connection with explicit site and connection versions. Omit
--credentials-file to preserve credentials; use --clear-credentials to clear them.
Credentials are read from a private JSON file or stdin (-), never command flags.
The file uses token, caPem, clientCertificatePem and clientKeyPem.`, Args: cobra.ExactArgs(2), RunE: nvkeScoped(runNVKEConfigure)}
	configure.Flags().Int("site-version", 0, "Expected current site version (required)")
	_ = configure.MarkFlagRequired("site-version")
	configure.Flags().Int("connection-version", 0, "Expected connection version (0 for new)")
	configure.Flags().String("endpoint", "", "Endpoint base URL (required)")
	_ = configure.MarkFlagRequired("endpoint")
	configure.Flags().String("credentials-file", "", "Private JSON credential file, or - for stdin")
	configure.Flags().Bool("clear-credentials", false, "Clear the credential bundle")
	configure.Flags().Bool("allow-http", false, "Explicitly allow an HTTP evaluation endpoint")
	configure.Flags().Int("connection-timeout", 30, "Connection timeout in seconds (1–300)")
	group.AddCommand(list, get, create, configure)
	for _, verb := range []string{"enable", "disable", "remove"} {
		selected := verb
		action := &cobra.Command{Use: verb + " SITE_GUID", Short: verb + " a local site registration", Args: cobra.ExactArgs(1), RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
			if err := nvkeGUID(args[0]); err != nil {
				return err
			}
			version, _ := cmd.Flags().GetInt("version")
			if version < 1 {
				return errors.New("--version must be positive")
			}
			var site *api.NVKESite
			var err error
			if selected == "remove" {
				site, err = c.RemoveNVKESite(ctx, args[0], version)
			} else {
				site, err = c.SetNVKESiteEnabled(ctx, args[0], version, selected == "enable")
			}
			if err != nil {
				return err
			}
			return nvkeJSON(cmd, site)
		})}
		action.Flags().Int("version", 0, "Expected current site version (required)")
		_ = action.MarkFlagRequired("version")
		group.AddCommand(action)
	}
	return group
}
func runNVKEConfigure(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
	if err := nvkeGUID(args[0]); err != nil {
		return err
	}
	surface := strings.ToUpper(args[1])
	switch surface {
	case "CONTROLLER", "TENANT", "OPERATOR", "DATASTORE":
	default:
		return errors.New("unknown NVKE surface")
	}
	version, _ := cmd.Flags().GetInt("site-version")
	connectionVersion, _ := cmd.Flags().GetInt("connection-version")
	timeout, _ := cmd.Flags().GetInt("connection-timeout")
	if version < 1 || connectionVersion < 0 || timeout < 1 || timeout > 300 {
		return errors.New("use positive site version, nonnegative connection version and connection timeout 1–300")
	}
	file := resourceStringFlag(cmd, "credentials-file")
	clear := boolFlag(cmd, "clear-credentials")
	if file != "" && clear {
		return errors.New("use either --credentials-file or --clear-credentials")
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
		credentials = &api.NVKECredentialsInput{}
		if err := decodeNVKEStrict(data, credentials); err != nil {
			return errors.New("invalid NVKE credential document")
		}
	}
	input := api.NVKEConnectionInput{SiteID: args[0], ExpectedSiteVersion: version, Surface: surface, ExpectedConnectionVersion: connectionVersion, Endpoint: resourceStringFlag(cmd, "endpoint"), Credentials: credentials, AllowHTTP: boolFlag(cmd, "allow-http"), TimeoutSeconds: timeout}
	site, err := c.ConfigureNVKEConnection(ctx, input)
	if err != nil {
		return err
	}
	return nvkeJSON(cmd, site)
}
func newNVKEOperationCommand() *cobra.Command {
	group := &cobra.Command{Use: "operation", Aliases: []string{"operations"}, Short: "Inspect, wait, cancel and reconcile durable operations"}
	list := &cobra.Command{Use: "list SITE_GUID", Short: "List a page of operation metadata", Args: cobra.ExactArgs(1), RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
		if err := nvkeGUID(args[0]); err != nil {
			return err
		}
		after, limit, err := nvkePage(cmd)
		if err != nil {
			return err
		}
		page, err := c.NVKEOperations(ctx, args[0], after, limit)
		if err != nil {
			return err
		}
		return nvkeJSON(cmd, page)
	})}
	nvkePageFlags(list)
	group.AddCommand(list, newNVKERequestCommand("request ACTION_KEY", ""), newNVKERecoverCommand(), newNVKEDiagnosticsCommand())
	for _, verb := range []string{"get", "wait", "result", "cancel", "resume"} {
		selected := verb
		action := &cobra.Command{Use: verb + " OPERATION_GUID", Short: verb + " an exact operation", Args: cobra.ExactArgs(1), RunE: nvkeScoped(func(cmd *cobra.Command, ctx context.Context, c *api.Client, args []string) error {
			return runNVKEOperation(cmd, ctx, c, args[0], selected)
		})}
		if verb == "result" {
			action.Flags().String("output", "", "New private file for the sensitive result; - explicitly writes stdout")
		}
		if verb == "resume" {
			action.Flags().Int("version", 0, "Expected current operation version (required)")
			_ = action.MarkFlagRequired("version")
			action.Flags().Int("operation-timeout", 7200, "New observation deadline in seconds (30–86400)")
		}
		if verb == "wait" {
			nvkeWaitFlags(action)
		}
		group.AddCommand(action)
	}
	return group
}
func nvkeWaitFlags(cmd *cobra.Command) {
	cmd.Flags().Duration("wait-timeout", 10*time.Minute, "Local wait limit; expiry does not cancel the operation")
	cmd.Flags().Duration("poll-interval", 5*time.Second, "Metadata poll interval (500ms–1m)")
}
func validateNVKEWait(cmd *cobra.Command) error {
	timeout, _ := cmd.Flags().GetDuration("wait-timeout")
	interval, _ := cmd.Flags().GetDuration("poll-interval")
	if timeout <= 0 || timeout > 24*time.Hour || interval < 500*time.Millisecond || interval > time.Minute {
		return errors.New("use a positive wait-timeout up to 24h and poll-interval 500ms–1m")
	}
	return nil
}

func waitNVKEOperation(cmd *cobra.Command, ctx context.Context, c *api.Client, row *api.NVKEOperation) error {
	if err := validateNVKEWait(cmd); err != nil {
		return err
	}
	timeout, _ := cmd.Flags().GetDuration("wait-timeout")
	interval, _ := cmd.Flags().GetDuration("poll-interval")
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		switch row.Status {
		case "SUCCEEDED":
			return nvkePrintOperation(cmd, row)
		case "FAILED", "CANCELLED", "UNCERTAIN":
			if err := nvkePrintOperation(cmd, row); err != nil {
				return err
			}
			return fmt.Errorf("NVKE operation %s is %s; inspect its metadata before further changes", row.ID, row.Status)
		case "QUEUED", "RUNNING", "RECONCILING", "AWAITING_READY":
		default:
			return errors.New("unrecognized NVKE operation status; no completion established")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("local wait ended for NVKE operation %s; the remote operation has not been cancelled", row.ID)
		case <-timer.C:
		}
		next, err := c.NVKEOperation(ctx, row.ID)
		if err != nil {
			return err
		}
		if next.ID != row.ID || next.SiteID != row.SiteID || next.RequestID != row.RequestID || next.OperationKey != row.OperationKey || next.IsInventory != row.IsInventory || !sameNVKESource(next.ResourceSource, row.ResourceSource) {
			return errors.New("NVKE operation observation identity changed")
		}
		row = next
	}
}

func runNVKEOperation(cmd *cobra.Command, ctx context.Context, c *api.Client, id, selected string) error {
	if err := nvkeGUID(id); err != nil {
		return err
	}
	if selected == "result" {
		file := resourceStringFlag(cmd, "output")
		if file == "" {
			return errors.New("--output is required for sensitive native results; use - explicitly for stdout")
		}
		data, err := c.NVKEOperationResultText(ctx, id)
		if err != nil {
			return err
		}
		if file == "-" {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), data)
			return err
		}
		if err := writeNVKEExclusive(file, append([]byte(data), '\n')); err != nil {
			return err
		}
		if boolFlag(cmd, "json") {
			return nvkeJSON(cmd, map[string]bool{"saved": true})
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "Saved private NVKE operation result")
		return nil
	}
	var row *api.NVKEOperation
	var err error
	switch selected {
	case "cancel":
		row, err = c.CancelNVKEOperation(ctx, id)
	case "resume":
		version, _ := cmd.Flags().GetInt("version")
		timeout, _ := cmd.Flags().GetInt("operation-timeout")
		if version < 1 || timeout < 30 || timeout > 86400 {
			return errors.New("use positive --version and --operation-timeout 30–86400")
		}
		row, err = c.ResumeNVKEOperation(ctx, id, version, timeout)
	default:
		row, err = c.NVKEOperation(ctx, id)
	}
	if err != nil {
		return err
	}
	if row.ID != id {
		return errors.New("NVKE operation reply did not confirm the requested identity")
	}
	if selected == "wait" {
		return waitNVKEOperation(cmd, ctx, c, row)
	}
	return nvkePrintOperation(cmd, row)
}
