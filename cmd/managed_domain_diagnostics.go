package cmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const managedDomainReadQuery = `query ManagedDomain($domainId: GUID!) {
  astroliftManagedDomain(domainId: $domainId) { version ` + managedDomainFields + ` }
}`

const managedDomainDiagnosticsQuery = `query ManagedDomainDiagnostics(
  $domainId: GUID!, $expectedVersion: Int!, $hostname: String, $recordType: ManagedDomainRecordType!
) {
  astroliftManagedDomainDiagnostics(domainId: $domainId, expectedVersion: $expectedVersion,
    hostname: $hostname, recordType: $recordType) {
    domainId: id version zone checkedAt
    checks { key state perspective checkedAt reason expected observed }
    providerZone {
      state reason zoneId zoneName privateZone nameservers truncated
      records { name type ttl values aliasTarget aliasZoneId evaluateTargetHealth }
    }
    routes {
      appId appName environmentId environmentName recordedUrl hostname
      clusterId clusterName ingressClass observedState
    }
    routesTruncated
  }
}`

const managedDomainProbeQuery = `query ManagedDomainProbe(
  $domainId: GUID!, $expectedVersion: Int!, $hostname: String!,
  $tool: ManagedDomainProbeTool!, $recordType: ManagedDomainRecordType!
) {
  astroliftManagedDomainProbe(domainId: $domainId, expectedVersion: $expectedVersion,
    hostname: $hostname, tool: $tool, recordType: $recordType) {
    state perspective checkedAt reason hostname tool recordType values
    publicAddress httpStatus tlsVerified latencyMs
  }
}`

type managedDomainCheck struct {
	Key         string   `json:"key"`
	State       string   `json:"state"`
	Perspective string   `json:"perspective"`
	CheckedAt   string   `json:"checkedAt"`
	Reason      string   `json:"reason"`
	Expected    []string `json:"expected"`
	Observed    []string `json:"observed"`
}

type managedDomainRecord struct {
	Name                 string   `json:"name"`
	Type                 string   `json:"type"`
	TTL                  *int     `json:"ttl"`
	Values               []string `json:"values"`
	AliasTarget          *string  `json:"aliasTarget"`
	AliasZoneID          *string  `json:"aliasZoneId"`
	EvaluateTargetHealth *bool    `json:"evaluateTargetHealth"`
}

type managedDomainRoute struct {
	AppID           string  `json:"appId"`
	AppName         string  `json:"appName"`
	EnvironmentID   string  `json:"environmentId"`
	EnvironmentName string  `json:"environmentName"`
	RecordedURL     string  `json:"recordedUrl"`
	Hostname        string  `json:"hostname"`
	ClusterID       *string `json:"clusterId"`
	ClusterName     *string `json:"clusterName"`
	IngressClass    *string `json:"ingressClass"`
	ObservedState   string  `json:"observedState"`
}

type managedDomainDiagnostics struct {
	DomainID     string               `json:"domainId"`
	Version      int                  `json:"version"`
	Zone         string               `json:"zone"`
	CheckedAt    string               `json:"checkedAt"`
	Checks       []managedDomainCheck `json:"checks"`
	ProviderZone struct {
		State       string                `json:"state"`
		Reason      string                `json:"reason"`
		ZoneID      string                `json:"zoneId"`
		ZoneName    string                `json:"zoneName"`
		PrivateZone *bool                 `json:"privateZone"`
		Nameservers []string              `json:"nameservers"`
		Truncated   bool                  `json:"truncated"`
		Records     []managedDomainRecord `json:"records"`
	} `json:"providerZone"`
	Routes          []managedDomainRoute `json:"routes"`
	RoutesTruncated bool                 `json:"routesTruncated"`
}

type managedDomainProbe struct {
	State         string   `json:"state"`
	Perspective   string   `json:"perspective"`
	CheckedAt     string   `json:"checkedAt"`
	Reason        string   `json:"reason"`
	Hostname      string   `json:"hostname"`
	Tool          string   `json:"tool"`
	RecordType    string   `json:"recordType"`
	Values        []string `json:"values"`
	PublicAddress *string  `json:"publicAddress"`
	HTTPStatus    *int     `json:"httpStatus"`
	TLSVerified   *bool    `json:"tlsVerified"`
	LatencyMs     *float64 `json:"latencyMs"`
}

func addManagedDomainDiagnosticCommands(group *cobra.Command) {
	show := &cobra.Command{
		Use: "show <id>", Short: "Read an exact managed domain, including verification and version",
		Args: nonemptyManagedDomainID,
		RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
			domain, err := readManagedDomain(ctx, client, args[0])
			if err != nil {
				return err
			}
			return renderJSON(cmd, domain)
		}),
	}
	check := &cobra.Command{
		Use: "check <id>", Short: "Check provider records, public delegation and recorded app routing",
		Long: "Collect a fresh, read-only domain report from the selected Astrolift server. Provisioning completion is distinct from public DNS, routing and HTTPS health. Each finding includes its observation location and time. No registrar or DNS changes are made.",
		Args: nonemptyManagedDomainID,
		RunE: managedDomainDiagnosticRunE(false),
	}
	check.Flags().String("hostname", "", "Hostname within this registered zone (default: zone apex)")
	check.Flags().String("record-type", "NS", "DNS record type: A, AAAA, CNAME, MX, NS, SOA, TXT, CAA, SRV")
	lookup := &cobra.Command{
		Use: "lookup <id>", Aliases: []string{"dig"}, Short: "Query DNS for a hostname in the registered zone",
		Long: "Run a bounded DNS lookup from the Astrolift server using its configured public resolver. The dig alias uses the same typed DNS query engine. This does not run a local shell or change DNS records.",
		Args: nonemptyManagedDomainID,
		RunE: managedDomainDiagnosticRunE(true),
	}
	lookup.Flags().String("hostname", "", "Hostname within this registered zone (required)")
	lookup.Flags().String("record-type", "A", "DNS record type: A, AAAA, CNAME, MX, NS, SOA, TXT, CAA, SRV")
	probe := &cobra.Command{
		Use: "probe <id>", Short: "Run a bounded HTTPS, ping or traceroute check from the server",
		Long: "Probe a public hostname in this registered zone. The server reports unavailable ICMP tools explicitly; ping is not HTTPS health. Targets and runtime bounds are enforced by the API, and HTTPS redirects are not followed.",
		Args: nonemptyManagedDomainID,
		RunE: managedDomainDiagnosticRunE(true),
	}
	probe.Flags().String("hostname", "", "Hostname within this registered zone (required)")
	probe.Flags().String("tool", "https", "Tool: https, ping, traceroute")
	probe.Flags().String("record-type", "A", "DNS record type (used only for DNS tools)")
	group.AddCommand(show, check, lookup, probe)
}

func nonemptyManagedDomainID(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	if strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("a nonempty managed domain id is required")
	}
	return nil
}

func readManagedDomain(ctx context.Context, client *api.Client, id string) (*managedDomain, error) {
	var response struct {
		Domain *managedDomain `json:"astroliftManagedDomain"`
	}
	if err := client.GraphQL(ctx, managedDomainReadQuery, map[string]interface{}{"domainId": id}, &response); err != nil {
		return nil, fmt.Errorf("reading managed domain: %w", err)
	}
	if response.Domain == nil {
		return nil, fmt.Errorf("managed domain not found or unavailable in the selected organization")
	}
	if response.Domain.ID != id || response.Domain.Version < 1 {
		return nil, fmt.Errorf("server returned an invalid managed domain identity or version")
	}
	return response.Domain, nil
}

func managedDomainDiagnosticRunE(probe bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		hostname, _ := cmd.Flags().GetString("hostname")
		hostname = strings.TrimSpace(hostname)
		if probe && hostname == "" {
			return fmt.Errorf("--hostname is required")
		}
		recordType, _ := cmd.Flags().GetString("record-type")
		recordType = strings.ToUpper(strings.TrimSpace(recordType))
		switch recordType {
		case "A", "AAAA", "CNAME", "MX", "NS", "SOA", "TXT", "CAA", "SRV":
		default:
			return fmt.Errorf("unsupported --record-type %q", recordType)
		}
		tool := "LOOKUP"
		if cmd.Name() == "probe" {
			value, _ := cmd.Flags().GetString("tool")
			tool = strings.ToUpper(strings.TrimSpace(value))
			switch tool {
			case "HTTPS", "PING", "TRACEROUTE":
			default:
				return fmt.Errorf("--tool must be https, ping or traceroute")
			}
		} else if cmd.CalledAs() == "dig" {
			tool = "DIG"
		}
		return workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
			domain, err := readManagedDomain(ctx, client, args[0])
			if err != nil {
				return err
			}
			variables := map[string]interface{}{"domainId": domain.ID, "expectedVersion": domain.Version, "recordType": recordType}
			if hostname != "" {
				variables["hostname"] = hostname
			}
			if probe {
				variables["tool"] = tool
				var response struct {
					Probe *managedDomainProbe `json:"astroliftManagedDomainProbe"`
				}
				if err := client.GraphQL(ctx, managedDomainProbeQuery, variables, &response); err != nil {
					return fmt.Errorf("probing managed domain: %w", err)
				}
				if response.Probe == nil {
					return fmt.Errorf("server returned no managed domain probe")
				}
				if boolFlag(cmd, "json") {
					return renderJSON(cmd, response.Probe)
				}
				p := response.Probe
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s\nChecked: %s\nFrom: %s\n%s\n", p.Tool, p.Hostname, p.State, p.CheckedAt, p.Perspective, p.Reason)
				for _, value := range p.Values {
					fmt.Fprintln(cmd.OutOrStdout(), value)
				}
				if p.HTTPStatus != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "HTTP status: %d\n", *p.HTTPStatus)
				}
				return nil
			}
			var response struct {
				Report *managedDomainDiagnostics `json:"astroliftManagedDomainDiagnostics"`
			}
			if err := client.GraphQL(ctx, managedDomainDiagnosticsQuery, variables, &response); err != nil {
				return fmt.Errorf("checking managed domain: %w", err)
			}
			if response.Report == nil {
				return fmt.Errorf("server returned no managed domain diagnostics")
			}
			if response.Report.DomainID != domain.ID || response.Report.Version != domain.Version {
				return fmt.Errorf("server returned diagnostics for a different domain identity or version")
			}
			if boolFlag(cmd, "json") {
				return renderJSON(cmd, response.Report)
			}
			return renderManagedDomainDiagnostics(cmd, response.Report)
		})(cmd, args)
	}
}

func renderManagedDomainDiagnostics(cmd *cobra.Command, report *managedDomainDiagnostics) error {
	fmt.Fprintf(cmd.OutOrStdout(), "%s (%s), version %d\nChecked: %s\n", report.Zone, report.DomainID, report.Version, report.CheckedAt)
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CHECK\tSTATE\tFROM\tREASON")
	for _, check := range report.Checks {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", check.Key, check.State, check.Perspective, check.Reason)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, check := range report.Checks {
		if len(check.Expected) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "%s expected: %s\n", check.Key, strings.Join(check.Expected, ", "))
		}
		if len(check.Observed) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "%s observed: %s\n", check.Key, strings.Join(check.Observed, ", "))
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Provider zone: %s %s\n", report.ProviderZone.State, report.ProviderZone.Reason)
	if len(report.ProviderZone.Records) > 0 {
		records := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(records, "NAME\tTYPE\tTTL\tVALUE / ALIAS TARGET")
		for _, record := range report.ProviderZone.Records {
			ttl := "—"
			if record.TTL != nil {
				ttl = fmt.Sprint(*record.TTL)
			}
			value := strings.Join(record.Values, ", ")
			if record.AliasTarget != nil {
				value = "alias → " + *record.AliasTarget
			}
			fmt.Fprintf(records, "%s\t%s\t%s\t%q\n", record.Name, record.Type, ttl, value)
		}
		if err := records.Flush(); err != nil {
			return err
		}
	}
	if report.ProviderZone.Truncated {
		fmt.Fprintln(cmd.OutOrStdout(), "Provider records are incomplete; absence is not proof a record is missing.")
	}
	for _, route := range report.Routes {
		fmt.Fprintf(cmd.OutOrStdout(), "Route: %s → %s / %s, cluster=%s (%s)\n", route.RecordedURL, route.AppName, route.EnvironmentName, stringOrDash(route.ClusterName), route.ObservedState)
	}
	if report.RoutesTruncated {
		fmt.Fprintln(cmd.OutOrStdout(), "Routing inventory is incomplete.")
	}
	return nil
}

func stringOrDash(value *string) string {
	if value == nil || *value == "" {
		return "—"
	}
	return *value
}
