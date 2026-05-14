// Package cmd — `astro cluster ...` subcommand tree.
//
// `astro cluster bootstrap` is the operator-side helm-install for the
// astrolift-prereqs chart. It resolves the target cluster via the control
// plane's GraphQL API, builds a kubeconfig appropriate for the cluster's
// auth_method, helm-installs (or upgrades) the embedded chart, and emits
// a `cluster.bootstrap_run` event so the UI can show "last bootstrap" on
// the cluster detail page.
//
// The companion server-side workflow (`bringClusterIntoManagement` in
// #316) wants the prereqs present before it flips the cluster to
// `managed`; running `astro cluster bootstrap --cluster-slug X` is the
// canonical way for an operator to satisfy that prerequisite.
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/bootstrap"
	"github.com/calliopeai/astrolift-cli/internal/charts"
	"github.com/spf13/cobra"
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Operator-side cluster commands (bootstrap, ...)",
	Long: `Operator-side commands for managing tenant clusters from the CLI.

Today this covers cluster bootstrap (one-command helm install of the
astrolift-prereqs chart). Cluster CRUD (register / list / unregister)
remains under 'astro operator cluster' — bootstrap intentionally sits
at the top level because it's the most frequent cluster-touching
operator workflow.`,
}

// ---- flags -------------------------------------------------------------

var (
	clusterBootstrapClusterSlug  string
	clusterBootstrapCloud        string
	clusterBootstrapValuesFile   string
	clusterBootstrapNamespace    string
	clusterBootstrapChartVersion string
	clusterBootstrapDryRun       bool
	clusterBootstrapUpgrade      bool
	clusterBootstrapWaitTimeout  time.Duration
	clusterBootstrapKubeconfig   string
	clusterBootstrapReleaseName  string
)

var clusterBootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Helm-install the astrolift-prereqs chart against a target cluster",
	Long: `Bootstraps a target Kubernetes cluster with the cluster prerequisites
(cert-manager, ingress controller, storage classes, external-dns,
managed-service operators) that Astrolift needs before it can flip the
cluster to lifecycle=managed.

The cluster is resolved by --cluster-slug against the current Astrolift
server. The bundled cloud-specific values overlay
(values.aws.yaml / .gcp / .azure / .k8s) is picked from the cluster's
provider_plugin_slug unless --cloud overrides it. The operator may
layer additional overrides via --values-file.

Authentication to the cluster's API server uses the cluster's
auth_method:
  * kubeconfig           — uses the stored kubeconfig from the registry
  * service_account_token — synthesises a minimal kubeconfig from
                            endpoint + ca_cert + token
  * exec_plugin          — uses the operator's default kubeconfig context
                            (run 'aws eks update-kubeconfig' / similar
                            first, or pass --kubeconfig)

Examples:
  # Dry-run against a registered EKS cluster
  astro cluster bootstrap --cluster-slug prd-astrolift-eks --dry-run

  # Real install with a wait of up to 10 minutes
  astro cluster bootstrap --cluster-slug prd-astrolift-eks --wait-timeout 10m

  # Reconcile drift on an already-bootstrapped cluster
  astro cluster bootstrap --cluster-slug prd-astrolift-eks --upgrade

  # Override the cloud overlay manually (e.g. running k8s overlay on
  # a self-managed k3s cluster registered with provider_plugin_slug=aws)
  astro cluster bootstrap --cluster-slug staging-k3s --cloud k8s`,
	RunE: runClusterBootstrap,
}

func init() {
	clusterBootstrapCmd.Flags().StringVar(&clusterBootstrapClusterSlug, "cluster-slug", "", "TenantCluster slug to bootstrap (required)")
	clusterBootstrapCmd.Flags().StringVar(&clusterBootstrapCloud, "cloud", "", "Override the per-cloud overlay (aws | gcp | azure | k8s); inferred from provider_plugin_slug when unset")
	clusterBootstrapCmd.Flags().StringVar(&clusterBootstrapValuesFile, "values-file", "", "Path to an additional helm values file layered on top of the bundled overlay")
	clusterBootstrapCmd.Flags().StringVar(&clusterBootstrapNamespace, "namespace", "astrolift-system", "Helm release namespace (created if missing)")
	clusterBootstrapCmd.Flags().StringVar(&clusterBootstrapChartVersion, "chart-version", charts.PinnedChartVersion, "Astrolift-prereqs chart version (must match the bundled chart)")
	clusterBootstrapCmd.Flags().BoolVar(&clusterBootstrapDryRun, "dry-run", false, "Render the chart without installing")
	clusterBootstrapCmd.Flags().BoolVar(&clusterBootstrapUpgrade, "upgrade", false, "Reconcile drift on an already-bootstrapped cluster (helm upgrade --install)")
	clusterBootstrapCmd.Flags().DurationVar(&clusterBootstrapWaitTimeout, "wait-timeout", 5*time.Minute, "How long to wait for releases to become ready")
	clusterBootstrapCmd.Flags().StringVar(&clusterBootstrapKubeconfig, "kubeconfig", "", "Override the kubeconfig path (default: from cluster auth_method or $KUBECONFIG)")
	clusterBootstrapCmd.Flags().StringVar(&clusterBootstrapReleaseName, "release-name", "astrolift-prereqs", "Helm release name")
	_ = clusterBootstrapCmd.MarkFlagRequired("cluster-slug")

	clusterCmd.AddCommand(clusterBootstrapCmd)
	rootCmd.AddCommand(clusterCmd)
}

// ---- GraphQL shapes ----------------------------------------------------

type bootstrapCluster struct {
	ID                 string         `json:"id"`
	Slug               string         `json:"slug"`
	Name               string         `json:"name"`
	ProviderPluginSlug string         `json:"providerPluginSlug"`
	Region             string         `json:"region"`
	Endpoint           string         `json:"endpoint"`
	AuthMethod         string         `json:"authMethod"`
	IngressClass       string         `json:"ingressClass"`
	IsActive           bool           `json:"isActive"`
	Capabilities       map[string]any `json:"capabilities"`
	Lifecycle          string         `json:"lifecycle"`
}

type clustersListResp struct {
	AstroliftClusters []bootstrapCluster `json:"astroliftClusters"`
}

// clusterAuthDetailsType mirrors the GraphQL AstroliftClusterAuthDetails
// shape returned by astroliftClusterAuthDetails(clusterId:). Split from
// the cluster list query so the secret-bearing fields aren't exposed by
// default — the resolver gates on cluster.manage.
type clusterAuthDetailsType struct {
	AuthMethod string `json:"authMethod"`
	Kubeconfig string `json:"kubeconfig"`
	Token      string `json:"token"`
	CaCert     string `json:"caCert"`
	Endpoint   string `json:"endpoint"`
}

type clusterAuthDetailsResp struct {
	AstroliftClusterAuthDetails clusterAuthDetailsType `json:"astroliftClusterAuthDetails"`
}

type bootstrapReleaseEntry struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	Status  string `json:"status"`
}

type recordClusterBootstrapRunInput struct {
	ClusterID    string                  `json:"clusterId"`
	ChartVersion string                  `json:"chartVersion"`
	Releases     []bootstrapReleaseEntry `json:"releases"`
	StartedAt    string                  `json:"startedAt"`
	EndedAt      string                  `json:"endedAt"`
	Success      bool                    `json:"success"`
	ErrorMessage string                  `json:"errorMessage,omitempty"`
	Cloud        string                  `json:"cloud,omitempty"`
}

type recordBootstrapRunResp struct {
	RecordClusterBootstrapRun struct {
		Ok     bool `json:"ok"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	} `json:"recordClusterBootstrapRun"`
}

// ---- command body ------------------------------------------------------

func runClusterBootstrap(cmd *cobra.Command, _ []string) error {
	if clusterBootstrapChartVersion != charts.PinnedChartVersion {
		return fmt.Errorf(
			"chart-version %q does not match bundled chart %q; rebuild the CLI to bump the embedded chart",
			clusterBootstrapChartVersion, charts.PinnedChartVersion,
		)
	}

	debug, _ := cmd.Flags().GetBool("debug")
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	client, _, _, err := loadActiveClient(ctx, debug)
	if err != nil {
		return err
	}

	cluster, err := fetchClusterBySlug(ctx, client, clusterBootstrapClusterSlug)
	if err != nil {
		return err
	}
	if !cluster.IsActive {
		return fmt.Errorf("cluster %q is inactive — reactivate before bootstrapping", cluster.Slug)
	}

	cloud := charts.Cloud(strings.ToLower(clusterBootstrapCloud))
	if cloud == "" {
		cloud = charts.CloudFromProviderPlugin(cluster.ProviderPluginSlug)
	}
	if err := cloud.Validate(); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Cluster:        %s (%s)\n", cluster.Name, cluster.Slug)
	fmt.Fprintf(out, "Provider:       %s\n", cluster.ProviderPluginSlug)
	fmt.Fprintf(out, "Cloud overlay:  %s\n", cloud)
	fmt.Fprintf(out, "Auth method:    %s\n", cluster.AuthMethod)
	fmt.Fprintf(out, "Chart version:  %s\n", charts.PinnedChartVersion)
	fmt.Fprintf(out, "Namespace:      %s\n", clusterBootstrapNamespace)
	fmt.Fprintf(out, "Release name:   %s\n", clusterBootstrapReleaseName)
	if clusterBootstrapDryRun {
		fmt.Fprintln(out, "Mode:           dry-run (no cluster changes)")
	} else if clusterBootstrapUpgrade {
		fmt.Fprintln(out, "Mode:           upgrade --install")
	} else {
		fmt.Fprintln(out, "Mode:           install")
	}
	fmt.Fprintln(out, "")

	kubeSrc, err := buildKubeconfigSource(ctx, client, cluster, clusterBootstrapKubeconfig)
	if err != nil {
		return fmt.Errorf("resolving kubeconfig: %w", err)
	}
	if err := kubeSrc.Materialize(""); err != nil {
		return err
	}
	defer func() { _ = kubeSrc.Cleanup() }()

	baseValues, err := charts.BundledValuesFile(cloud)
	if err != nil {
		return err
	}

	var userValues []byte
	if clusterBootstrapValuesFile != "" {
		b, err := os.ReadFile(clusterBootstrapValuesFile)
		if err != nil {
			return fmt.Errorf("reading --values-file: %w", err)
		}
		userValues = b
	}

	chartObj, err := charts.LoadChart()
	if err != nil {
		return err
	}

	startedAt := time.Now().UTC()

	installOpts := bootstrap.InstallOptions{
		ChartName:      charts.ChartName,
		ChartVersion:   charts.PinnedChartVersion,
		ReleaseName:    clusterBootstrapReleaseName,
		Namespace:      clusterBootstrapNamespace,
		KubeconfigPath: kubeSrc.Path,
		BaseValues:     baseValues,
		UserValues:     userValues,
		Chart:          chartObj,
		Upgrade:        clusterBootstrapUpgrade,
		DryRun:         clusterBootstrapDryRun,
		WaitTimeout:    clusterBootstrapWaitTimeout,
		Log:            out,
	}

	result, installErr := bootstrap.Install(ctx, installOpts)
	endedAt := time.Now().UTC()

	renderInstallResult(out, result, installErr)

	// Skip the control-plane event write on dry-run — nothing changed
	// in the cluster, so there's no bootstrap to record.
	if !clusterBootstrapDryRun {
		if err := recordBootstrapRun(ctx, client, cluster, cloud, result, installErr, startedAt, endedAt); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to record bootstrap run on control plane: %v\n", err)
		}
	}

	if installErr != nil {
		return installErr
	}
	return nil
}

// fetchClusterBySlug pulls the operator-visible cluster row matching
// slug. Returns NotFound when no row matches — surfacing that as a usage
// error rather than a generic failure.
func fetchClusterBySlug(ctx context.Context, client *api.Client, slug string) (*bootstrapCluster, error) {
	const q = `
query AstroliftClusters {
  astroliftClusters {
    id slug name providerPluginSlug region endpoint
    authMethod ingressClass isActive capabilities
    lifecycle
  }
}`
	var resp clustersListResp
	if err := client.GraphQL(ctx, q, nil, &resp); err != nil {
		return nil, fmt.Errorf("listing clusters: %w", err)
	}
	for _, c := range resp.AstroliftClusters {
		if c.Slug == slug {
			c := c // capture
			return &c, nil
		}
	}
	return nil, fmt.Errorf(
		"cluster %q not found in your accessible orgs — register it with `astro operator cluster register` first",
		slug,
	)
}

// buildKubeconfigSource decides which kubeconfig the helm SDK should
// read for the cluster's auth_method.
func buildKubeconfigSource(
	ctx context.Context,
	client *api.Client,
	cluster *bootstrapCluster,
	overridePath string,
) (*bootstrap.KubeconfigSource, error) {
	// Explicit --kubeconfig always wins.
	if overridePath != "" {
		return &bootstrap.KubeconfigSource{FilePath: overridePath}, nil
	}

	switch cluster.AuthMethod {
	case "exec_plugin":
		// The operator is expected to have run `aws eks update-kubeconfig`
		// (or gcloud / az) before invoking us. Fall back to KUBECONFIG / ~/.kube/config.
		path, err := defaultKubeconfigPath()
		if err != nil {
			return nil, err
		}
		return &bootstrap.KubeconfigSource{FilePath: path}, nil

	case "kubeconfig":
		details, err := fetchClusterAuth(ctx, client, cluster.ID)
		if err != nil {
			return nil, err
		}
		if details.Kubeconfig == "" {
			return nil, errors.New("cluster registry returned empty kubeconfig — re-register the cluster")
		}
		return &bootstrap.KubeconfigSource{InlineYAML: []byte(details.Kubeconfig)}, nil

	case "service_account_token":
		details, err := fetchClusterAuth(ctx, client, cluster.ID)
		if err != nil {
			return nil, err
		}
		endpoint := details.Endpoint
		if endpoint == "" {
			endpoint = cluster.Endpoint
		}
		yamlBytes, err := bootstrap.BuildKubeconfigFromSAToken(bootstrap.ServiceAccountTokenInput{
			ClusterSlug: cluster.Slug,
			Endpoint:    endpoint,
			CACertPEM:   details.CaCert,
			Token:       details.Token,
		})
		if err != nil {
			return nil, err
		}
		return &bootstrap.KubeconfigSource{InlineYAML: yamlBytes}, nil

	default:
		return nil, fmt.Errorf("unsupported auth_method %q on cluster %s", cluster.AuthMethod, cluster.Slug)
	}
}

// fetchClusterAuth pulls the kubeconfig / token / ca_cert payload from
// the control plane. The resolver server-side is gated by
// `cluster.manage` and returns empty strings for fields that don't
// apply to the cluster's auth_method.
func fetchClusterAuth(ctx context.Context, client *api.Client, clusterID string) (*clusterAuthDetailsType, error) {
	const q = `
query AstroliftClusterAuthDetails($id: GUID!) {
  astroliftClusterAuthDetails(clusterId: $id) {
    authMethod kubeconfig token caCert endpoint
  }
}`
	vars := map[string]any{"id": clusterID}
	var resp clusterAuthDetailsResp
	if err := client.GraphQL(ctx, q, vars, &resp); err != nil {
		return nil, fmt.Errorf("fetching cluster auth details: %w", err)
	}
	return &resp.AstroliftClusterAuthDetails, nil
}

// defaultKubeconfigPath returns $KUBECONFIG (first entry if colon-list)
// or ~/.kube/config.
func defaultKubeconfigPath() (string, error) {
	if env := os.Getenv("KUBECONFIG"); env != "" {
		// Multi-file KUBECONFIG: use the first entry, mirroring kubectl.
		parts := strings.Split(env, string(os.PathListSeparator))
		if len(parts) > 0 && parts[0] != "" {
			return parts[0], nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home dir: %w", err)
	}
	return home + "/.kube/config", nil
}

func renderInstallResult(out io.Writer, result *bootstrap.InstallResult, installErr error) {
	if result == nil {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Result:        no result returned")
		return
	}
	fmt.Fprintln(out, "")
	fmt.Fprintf(out, "Release:       %s (revision %d, status %s)\n",
		result.Release.Name, result.Release.Version, result.Release.Status,
	)
	if result.RenderedOnly {
		fmt.Fprintln(out, "Dry-run completed — review the rendered manifests above.")
	} else if installErr == nil {
		fmt.Fprintln(out, "Bootstrap complete.")
		fmt.Fprintln(out, "Next step:     run `astro operator cluster bring-into-management --slug <slug>`")
	}
	if len(result.SubchartStatus) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Subcharts in this release:")
		// Stable-ordered output — operators eyeball the same list across runs.
		order := []string{
			"certManager", "externalDns", "ingressNginx", "metallb",
			"longhorn", "rookCeph", "cnpg", "strimzi", "redisOperator",
			"vault", "velero", "otelCollector", "prometheus", "loki", "tempo",
		}
		for _, k := range order {
			status := result.SubchartStatus[k]
			if status == "" {
				status = "disabled"
			}
			fmt.Fprintf(out, "  %-16s %s\n", k+":", status)
		}
	}
}

// recordBootstrapRun POSTs the bootstrap-run event to the control plane.
// On the success path the event powers the cluster detail page's "last
// bootstrap" badge; on the failure path it surfaces the error message so
// the operator can see the same diagnostic in the UI.
func recordBootstrapRun(
	ctx context.Context,
	client *api.Client,
	cluster *bootstrapCluster,
	cloud charts.Cloud,
	result *bootstrap.InstallResult,
	installErr error,
	startedAt, endedAt time.Time,
) error {
	releases := []bootstrapReleaseEntry{}
	if result != nil {
		releases = append(releases, bootstrapReleaseEntry{
			Name:    result.Release.Name,
			Version: result.Release.Version,
			Status:  result.Release.Status,
		})
	}
	input := recordClusterBootstrapRunInput{
		ClusterID:    cluster.ID,
		ChartVersion: charts.PinnedChartVersion,
		Releases:     releases,
		StartedAt:    startedAt.Format(time.RFC3339Nano),
		EndedAt:      endedAt.Format(time.RFC3339Nano),
		Success:      installErr == nil,
		Cloud:        string(cloud),
	}
	if installErr != nil {
		input.ErrorMessage = installErr.Error()
	}

	const m = `
mutation RecordClusterBootstrapRun($input: RecordClusterBootstrapRunInput!) {
  recordClusterBootstrapRun(input: $input) {
    ok
    errors { message }
  }
}`
	vars := map[string]any{"input": input}
	var resp recordBootstrapRunResp
	if err := client.GraphQL(ctx, m, vars, &resp); err != nil {
		return err
	}
	if !resp.RecordClusterBootstrapRun.Ok {
		msgs := make([]string, 0, len(resp.RecordClusterBootstrapRun.Errors))
		for _, e := range resp.RecordClusterBootstrapRun.Errors {
			msgs = append(msgs, e.Message)
		}
		// Pretty-print into a stable string for the log surface.
		j, _ := json.Marshal(msgs)
		return fmt.Errorf("recordClusterBootstrapRun rejected: %s", string(j))
	}
	return nil
}
