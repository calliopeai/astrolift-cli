// Package cmd -- `astro app domains ...`: list an app's custom domains and
// their DNS validation / certificate status.
//
// The group existed as an empty placeholder (calliopeai/astrolift-cli#91):
// `astro app domains list` printed the generic sub-resource blurb and did
// nothing.
//
// "pause" / "resume" call setCustomDomainPaused. The rest of the group is
// read-only: astroliftAppDomains(appSlug) is the only domain query in the
// schema, and #91 only asked for "list". addAppDomain /
// addWildcardDomain / removeAppDomain / recheckDomainValidation exist on
// the Mutation type but are out of scope here -- neither #91 nor #99 asked
// for domain mutation, and adding one wasn't requested.
//
// cert_state and certificate_state are two different axes on the wire (per
// backend/astrolift_lifecycle/schema/types.py): cert_state is the DNS
// challenge's own validation status (pending/validating/validated/failed),
// certificate_state is the TLS certificate's issuance status
// (not_requested/issuing/active/failed/byo) -- a domain can be DNS-validated
// with a failed certificate, so both are shown.
package cmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// ---- GraphQL operations -----------------------------------------------------

const appDomainsListQuery = `query($appSlug: String!) {
  astroliftAppDomains(appSlug: $appSlug) {
    id
    hostname
    isActive
    isWildcard
    certState
    certificateState
    certObservabilityStatus
    validationMethod
    lastCheckedAt
    lastValidationError
    lastCertificateError
    certExpiresAt
    expectedCnameTarget
    createdAt
    requiredDnsRecords { kind name value ttl propagated message }
  }
}`

const setCustomDomainPausedMutation = `mutation($input: SetCustomDomainPausedInput!) {
  setCustomDomainPaused(input: $input) {
    ok
    errors { code message field }
    data { id hostname isPaused }
  }
}`

// ---- response shapes (GraphQL camelCase) ------------------------------------

// appDomainRequiredRecord mirrors AstroliftAppDomainRequiredRecord: the DNS
// record an operator must create for the domain to validate.
type appDomainRequiredRecord struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Value      string `json:"value"`
	TTL        int    `json:"ttl"`
	Propagated bool   `json:"propagated"`
	Message    string `json:"message"`
}

// appDomain mirrors the subset of AstroliftAppDomain a list view needs.
type appDomain struct {
	ID                      string                    `json:"id"`
	Hostname                string                    `json:"hostname"`
	IsActive                bool                      `json:"isActive"`
	IsWildcard              bool                      `json:"isWildcard"`
	CertState               string                    `json:"certState"`
	CertificateState        string                    `json:"certificateState"`
	CertObservabilityStatus string                    `json:"certObservabilityStatus"`
	ValidationMethod        string                    `json:"validationMethod"`
	LastCheckedAt           *string                   `json:"lastCheckedAt"`
	LastValidationError     string                    `json:"lastValidationError"`
	LastCertificateError    string                    `json:"lastCertificateError"`
	CertExpiresAt           *string                   `json:"certExpiresAt"`
	ExpectedCnameTarget     string                    `json:"expectedCnameTarget"`
	CreatedAt               string                    `json:"createdAt"`
	RequiredDNSRecords      []appDomainRequiredRecord `json:"requiredDnsRecords"`
}

// ---- astro app domains --------------------------------------------------------

var appDomainsCmd = &cobra.Command{
	Use:   "domains",
	Short: "List an app's custom domains and their validation status",
	Long: `Lists the custom domains bound to an app -- hostname, DNS validation
state, certificate state and expiry -- via astroliftAppDomains.

"pause" and "resume" take one custom domain out of service and put it back
(setCustomDomainPaused): a paused host answers 503 instead of the app, and
the app's other hosts are unaffected. Adding, removing, and reissuing a
domain's certificate remain console-only for now.`,
}

var appDomainsListCmd = &cobra.Command{
	Use:   "list [app]",
	Short: "List an app's custom domains",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, err := resolveAppSlug(cmd, firstArg(args))
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppDomainsList(cmd, cmd.Context(), client, slug)
	},
}

func runAppDomainsList(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string) error {
	var resp struct {
		Domains []appDomain `json:"astroliftAppDomains"`
	}
	if err := client.GraphQL(ctx, appDomainsListQuery, map[string]interface{}{"appSlug": appSlug}, &resp); err != nil {
		return fmt.Errorf("listing domains for %s: %w", appSlug, err)
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Domains)
	}

	out := cmd.OutOrStdout()
	if len(resp.Domains) == 0 {
		fmt.Fprintf(out, "No custom domains for app %q.\n", appSlug)
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HOSTNAME\tACTIVE\tVALIDATION METHOD\tDNS VALIDATION\tCERT STATE\tCERT EXPIRES")
	for _, d := range resp.Domains {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			d.Hostname, yesNo(d.IsActive), dashIfEmpty(d.ValidationMethod), dashIfEmpty(d.CertState),
			dashIfEmpty(d.CertificateState), shortTime(d.CertExpiresAt))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d domain(s) shown.\n", len(resp.Domains))
	for _, d := range resp.Domains {
		if msg := strings.TrimSpace(d.LastValidationError); msg != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "note: %s: validation error: %s\n", d.Hostname, msg)
		}
		if msg := strings.TrimSpace(d.LastCertificateError); msg != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "note: %s: certificate error: %s\n", d.Hostname, msg)
		}
	}
	return nil
}

// appDomainPauseState is what setCustomDomainPaused returns.
type appDomainPauseState struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	IsPaused bool   `json:"isPaused"`
}

var appDomainsPauseCmd = &cobra.Command{
	Use:   "pause <hostname> [app]",
	Short: "Pause one custom domain (the host answers 503 until resumed)",
	Long: `Pauses one custom domain of an app: the host answers 503 ("Astrolift: app is
paused") instead of the app until it is resumed. The app's other hosts are
unaffected. Pausing an already paused domain succeeds and changes nothing.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error { return runAppDomainsSetPaused(cmd, args, true) },
}

var appDomainsResumeCmd = &cobra.Command{
	Use:   "resume <hostname> [app]",
	Short: "Resume a paused custom domain",
	Long: `Resumes a custom domain paused with "astro app domains pause", so the host
serves the app again. Resuming a domain that is not paused succeeds and
changes nothing.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error { return runAppDomainsSetPaused(cmd, args, false) },
}

func runAppDomainsSetPaused(cmd *cobra.Command, args []string, paused bool) error {
	slug, err := resolveAppSlug(cmd, firstArg(args[1:]))
	if err != nil {
		return err
	}
	client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	return runAppDomainSetPaused(cmd, cmd.Context(), client, slug, args[0], paused)
}

func runAppDomainSetPaused(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug, hostname string, paused bool) error {
	verb := "pausing"
	if !paused {
		verb = "resuming"
	}
	var list struct {
		Domains []appDomain `json:"astroliftAppDomains"`
	}
	if err := client.GraphQL(ctx, appDomainsListQuery, map[string]interface{}{"appSlug": appSlug}, &list); err != nil {
		return fmt.Errorf("listing domains for %s: %w", appSlug, err)
	}
	var target *appDomain
	names := make([]string, 0, len(list.Domains))
	for i := range list.Domains {
		names = append(names, list.Domains[i].Hostname)
		if strings.EqualFold(list.Domains[i].Hostname, strings.TrimSpace(hostname)) {
			target = &list.Domains[i]
		}
	}
	if target == nil {
		have := "the app has no custom domains"
		if len(names) > 0 {
			have = "its domains: " + strings.Join(names, ", ")
		}
		return fmt.Errorf("no custom domain %q on app %s; %s", hostname, appSlug, have)
	}

	var resp struct {
		Result struct {
			Ok     bool                 `json:"ok"`
			Errors []mutationError      `json:"errors"`
			Data   *appDomainPauseState `json:"data"`
		} `json:"setCustomDomainPaused"`
	}
	vars := map[string]interface{}{"input": map[string]interface{}{"id": target.ID, "paused": paused}}
	if err := client.GraphQL(ctx, setCustomDomainPausedMutation, vars, &resp); err != nil {
		return fmt.Errorf("%s %s: %w", verb, target.Hostname, err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		return fmt.Errorf("%s was not changed: %s", target.Hostname, firstDeployError(resp.Result.Errors))
	}

	after := resp.Result.Data
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, after)
	}
	out := cmd.OutOrStdout()
	if after.IsPaused {
		fmt.Fprintf(out, "%s is now paused: the host answers 503 until you run `astro app domains resume %s`.\n", after.Hostname, after.Hostname)
	} else {
		fmt.Fprintf(out, "%s is now resumed and serves the app again.\n", after.Hostname)
	}
	return nil
}

func init() {
	appDomainsCmd.AddCommand(appDomainsListCmd, appDomainsPauseCmd, appDomainsResumeCmd)
}
