// Package cmd -- `astro app domains ...`: list an app's custom domains and
// their DNS validation / certificate status.
//
// The group existed as an empty placeholder (calliopeai/astrolift-cli#91):
// `astro app domains list` printed the generic sub-resource blurb and did
// nothing.
//
// Read-only for now: astroliftAppDomains(appSlug) is the only domain query
// in the schema, and #91 only asked for "list". addAppDomain /
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

Read-only: adding, removing, and reissuing a domain's certificate remain
console-only for now.`,
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

func init() {
	appDomainsCmd.AddCommand(appDomainsListCmd)
}
