// Package charts vendors the astrolift-prereqs Helm chart + cloud-specific
// values files into the astro binary via go:embed.
//
// The vendored chart copy lives at internal/charts/astrolift-prereqs/ and
// is refreshed by `make vendor-charts`, which copies the source of truth
// at astrolift-opscode/helm/astrolift-prereqs/. Pinning the chart version
// to a CLI binary version means a chart bump is always a CLI release —
// the operator never has to reason about which chart version their CLI
// is going to install.
package charts

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
)

// PinnedChartVersion is the astrolift-prereqs Chart.yaml version that this
// CLI binary ships with. Bump in lockstep with the vendored copy under
// internal/charts/astrolift-prereqs/. The astro cluster bootstrap command
// surfaces this so operators can correlate failures with a known chart.
const PinnedChartVersion = "0.1.0"

// ChartName is the embedded chart's release-time name.
const ChartName = "astrolift-prereqs"

//go:embed all:astrolift-prereqs
var chartFS embed.FS

// Cloud identifies a per-cloud overlay shipped in the chart directory.
type Cloud string

const (
	CloudAWS   Cloud = "aws"
	CloudGCP   Cloud = "gcp"
	CloudAzure Cloud = "azure"
	CloudK8s   Cloud = "k8s"
)

// AllClouds is the set of values overlays bundled in the binary.
var AllClouds = []Cloud{CloudAWS, CloudGCP, CloudAzure, CloudK8s}

// Validate returns nil iff c is one of the bundled overlays.
func (c Cloud) Validate() error {
	for _, allowed := range AllClouds {
		if c == allowed {
			return nil
		}
	}
	return fmt.Errorf("unsupported cloud %q (want one of aws / gcp / azure / k8s)", string(c))
}

// CloudFromProviderPlugin maps a TenantCluster.provider_plugin_slug to the
// bundled values overlay. The mapping is intentionally narrow: anything
// the cluster registry recognises maps to one of the four clouds, and
// unknown slugs fall back to the vanilla k8s overlay (the safest default
// — it doesn't assume any cloud LB / IAM is present).
func CloudFromProviderPlugin(slug string) Cloud {
	switch strings.ToLower(slug) {
	case "aws", "eks":
		return CloudAWS
	case "gcp", "gke":
		return CloudGCP
	case "azure", "aks":
		return CloudAzure
	case "k8s", "k8s_native", "kind", "k3s", "microk8s", "rke", "rke2":
		return CloudK8s
	default:
		return CloudK8s
	}
}

// LoadChart loads the embedded astrolift-prereqs chart with its subcharts.
// Returns a chart object ready to feed to helm action.Install / Upgrade.
func LoadChart() (*chart.Chart, error) {
	sub, err := fs.Sub(chartFS, "astrolift-prereqs")
	if err != nil {
		return nil, fmt.Errorf("embed root: %w", err)
	}

	files, err := collectChartFiles(sub)
	if err != nil {
		return nil, err
	}

	c, err := loader.LoadFiles(files)
	if err != nil {
		return nil, fmt.Errorf("loading embedded chart: %w", err)
	}
	return c, nil
}

// BundledValuesFile returns the raw bytes of the per-cloud values overlay
// for c (e.g. values.aws.yaml). Operators can layer their own overrides
// on top via --values-file.
func BundledValuesFile(c Cloud) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("astrolift-prereqs/values.%s.yaml", string(c))
	b, err := chartFS.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("reading bundled %s: %w", name, err)
	}
	return b, nil
}

// collectChartFiles walks the embedded chart tree and returns the
// loader.BufferedFile list helm's loader expects. We exclude per-cloud
// values.<cloud>.yaml from this list — they're applied as helm values
// overlays at install time, not as part of the chart's own values.yaml.
func collectChartFiles(root fs.FS) ([]*loader.BufferedFile, error) {
	var files []*loader.BufferedFile
	walkErr := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if isCloudValuesFile(p) {
			return nil
		}
		data, err := fs.ReadFile(root, p)
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		files = append(files, &loader.BufferedFile{
			Name: path.Clean(p),
			Data: data,
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("embedded chart is empty — run `make vendor-charts`")
	}
	return files, nil
}

// isCloudValuesFile reports whether path matches values.<cloud>.yaml at
// the chart root. These are bundled overlays applied at install time and
// must not be passed to the chart loader (helm only expects values.yaml).
func isCloudValuesFile(p string) bool {
	if path.Dir(p) != "." {
		return false
	}
	name := path.Base(p)
	if !strings.HasPrefix(name, "values.") || !strings.HasSuffix(name, ".yaml") {
		return false
	}
	if name == "values.yaml" {
		return false
	}
	return true
}
