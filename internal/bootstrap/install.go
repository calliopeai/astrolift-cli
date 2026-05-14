package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InstallOptions captures everything the bootstrap routine needs to
// helm-install / helm-upgrade the astrolift-prereqs chart.
type InstallOptions struct {
	ChartName      string
	ChartVersion   string
	ReleaseName    string
	Namespace      string
	KubeconfigPath string
	BaseValues     []byte // bundled per-cloud overlay (values.<cloud>.yaml)
	UserValues     []byte // operator-supplied --values-file (optional)
	Chart          *chart.Chart
	Upgrade        bool
	DryRun         bool
	WaitTimeout    time.Duration
	Log            io.Writer // nil => discarded
}

// ReleaseSummary mirrors the subset of release.Release we ship back to
// the control plane in the bootstrap-run event. Keeping a typed struct
// instead of leaking helm types means the API surface is stable across
// helm-sdk bumps.
type ReleaseSummary struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	Status  string `json:"status"`
}

// InstallResult is what Install returns on the happy path.
type InstallResult struct {
	Release        ReleaseSummary
	SubchartStatus map[string]string // alias -> "enabled" / "disabled" from values
	RenderedOnly   bool              // true when DryRun was set
}

// Install runs helm install (or helm upgrade --install when opts.Upgrade
// is set) for the astrolift-prereqs chart, against the kubeconfig at
// opts.KubeconfigPath. The function is intentionally synchronous — the
// CLI streams progress to the operator's terminal via opts.Log and
// returns once the release reaches deployed (or DryRun completes).
func Install(ctx context.Context, opts InstallOptions) (*InstallResult, error) {
	if opts.Chart == nil {
		return nil, errors.New("InstallOptions.Chart is nil — load the chart first")
	}
	if opts.KubeconfigPath == "" {
		return nil, errors.New("InstallOptions.KubeconfigPath is empty")
	}
	if opts.Namespace == "" {
		opts.Namespace = "astrolift-system"
	}
	if opts.ReleaseName == "" {
		opts.ReleaseName = "astrolift-prereqs"
	}
	if opts.WaitTimeout <= 0 {
		opts.WaitTimeout = 5 * time.Minute
	}
	log := opts.Log
	if log == nil {
		log = io.Discard
	}

	mergedValues, err := mergeValues(opts.BaseValues, opts.UserValues)
	if err != nil {
		return nil, err
	}

	settings := cli.New()
	settings.KubeConfig = opts.KubeconfigPath
	cf := genericclioptions.NewConfigFlags(false)
	cf.KubeConfig = &opts.KubeconfigPath
	cf.Namespace = &opts.Namespace

	if !opts.DryRun {
		if err := ensureNamespace(ctx, opts.KubeconfigPath, opts.Namespace); err != nil {
			return nil, fmt.Errorf("ensuring namespace %s: %w", opts.Namespace, err)
		}
	}

	actionConfig := new(action.Configuration)
	debugLog := func(format string, v ...any) {
		fmt.Fprintf(log, "[helm] "+format+"\n", v...)
	}
	if err := actionConfig.Init(cf, opts.Namespace, "secret", debugLog); err != nil {
		return nil, fmt.Errorf("init helm action config: %w", err)
	}

	if opts.DryRun {
		rendered, err := runRender(actionConfig, opts, mergedValues)
		if err != nil {
			return nil, err
		}
		fmt.Fprint(log, rendered)
		return &InstallResult{
			Release: ReleaseSummary{
				Name:    opts.ReleaseName,
				Version: 0,
				Status:  "pending-install (dry-run)",
			},
			SubchartStatus: subchartStatusFromValues(mergedValues),
			RenderedOnly:   true,
		}, nil
	}

	existing, err := actionConfig.Releases.Last(opts.ReleaseName)
	if err != nil && !errors.Is(err, driver.ErrReleaseNotFound) {
		return nil, fmt.Errorf("looking up release %s: %w", opts.ReleaseName, err)
	}

	var rel *release.Release
	switch {
	case existing != nil && opts.Upgrade:
		upgrade := action.NewUpgrade(actionConfig)
		upgrade.Namespace = opts.Namespace
		upgrade.Wait = true
		upgrade.Timeout = opts.WaitTimeout
		upgrade.Version = opts.ChartVersion
		upgrade.MaxHistory = 5
		rel, err = upgrade.RunWithContext(ctx, opts.ReleaseName, opts.Chart, mergedValues)
		if err != nil {
			return nil, fmt.Errorf("helm upgrade: %w", err)
		}
	case existing != nil && !opts.Upgrade:
		return nil, fmt.Errorf(
			"release %q already exists in namespace %q — re-run with --upgrade to reconcile drift",
			opts.ReleaseName, opts.Namespace,
		)
	default:
		install := action.NewInstall(actionConfig)
		install.ReleaseName = opts.ReleaseName
		install.Namespace = opts.Namespace
		install.CreateNamespace = false // we already created it above
		install.Wait = true
		install.Timeout = opts.WaitTimeout
		install.Version = opts.ChartVersion
		rel, err = install.RunWithContext(ctx, opts.Chart, mergedValues)
		if err != nil {
			return nil, fmt.Errorf("helm install: %w", err)
		}
	}

	if rel == nil {
		return nil, errors.New("helm SDK returned a nil release")
	}

	return &InstallResult{
		Release: ReleaseSummary{
			Name:    rel.Name,
			Version: rel.Version,
			Status:  string(rel.Info.Status),
		},
		SubchartStatus: subchartStatusFromValues(mergedValues),
	}, nil
}

// runRender does a server-side render with --dry-run, returning the
// concatenated manifest. Surfaces what `helm template` would produce.
func runRender(cfg *action.Configuration, opts InstallOptions, values map[string]any) (string, error) {
	install := action.NewInstall(cfg)
	install.ReleaseName = opts.ReleaseName
	install.Namespace = opts.Namespace
	install.DryRun = true
	install.ClientOnly = true
	install.Replace = true
	install.IncludeCRDs = false
	install.Version = opts.ChartVersion
	rel, err := install.Run(opts.Chart, values)
	if err != nil {
		return "", fmt.Errorf("rendering chart: %w", err)
	}
	if rel == nil {
		return "", errors.New("helm SDK returned a nil release on dry-run")
	}
	return rel.Manifest, nil
}

// mergeValues unmarshals the per-cloud overlay first, then layers the
// operator's user values on top. Empty inputs are treated as no-op
// rather than as "{}" so the chart's own defaults survive when neither
// overlay is provided.
func mergeValues(base, user []byte) (map[string]any, error) {
	out := map[string]any{}
	if len(base) > 0 {
		var v map[string]any
		if err := yaml.Unmarshal(base, &v); err != nil {
			return nil, fmt.Errorf("parsing bundled values: %w", err)
		}
		out = chartutil.CoalesceTables(v, out)
	}
	if len(user) > 0 {
		var v map[string]any
		if err := yaml.Unmarshal(user, &v); err != nil {
			return nil, fmt.Errorf("parsing --values-file: %w", err)
		}
		out = chartutil.CoalesceTables(v, out)
	}
	return out, nil
}

// subchartStatusFromValues walks the merged values and reports a tiny
// "<alias>: enabled / disabled" summary. The set of aliases matches
// astrolift-prereqs Chart.yaml.
func subchartStatusFromValues(values map[string]any) map[string]string {
	out := map[string]string{}
	candidates := []string{
		"certManager", "externalDns", "ingressNginx", "metallb",
		"longhorn", "rookCeph", "cnpg", "strimzi", "redisOperator",
		"vault", "velero", "otelCollector", "prometheus", "loki", "tempo",
	}
	for _, key := range candidates {
		section, ok := values[key].(map[string]any)
		if !ok {
			out[key] = "disabled"
			continue
		}
		enabled, _ := section["enabled"].(bool)
		if enabled {
			out[key] = "enabled"
		} else {
			out[key] = "disabled"
		}
	}
	return out
}

// ensureNamespace creates the target namespace if it doesn't already
// exist. The helm install action can do this with CreateNamespace=true,
// but we do it explicitly so the create/already-exists distinction is
// surfaced in the bootstrap log.
func ensureNamespace(ctx context.Context, kubeconfigPath, ns string) error {
	cfg, err := buildRestConfig(kubeconfigPath)
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("building kubernetes client: %w", err)
	}
	_, err = clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), "not found") {
		return fmt.Errorf("get namespace: %w", err)
	}
	_, err = clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "astrolift-cli",
				"astrolift.io/created-by":      "astro-cluster-bootstrap",
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create namespace: %w", err)
	}
	return nil
}

// buildRestConfig loads a kubeconfig from disk and returns the rest
// config the kubernetes client needs.
func buildRestConfig(kubeconfigPath string) (*rest.Config, error) {
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, &clientcmd.ConfigOverrides{},
	)
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	return cfg, nil
}
