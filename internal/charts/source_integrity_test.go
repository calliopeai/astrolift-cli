package charts

import (
	"strings"
	"testing"
)

func TestEmbeddedVersionAndLockedDependencies(t *testing.T) {
	chart, err := LoadChart()
	if err != nil {
		t.Fatal(err)
	}
	if chart.Metadata.Version != PinnedChartVersion {
		t.Fatalf("embedded chart version %q differs from reported %q", chart.Metadata.Version, PinnedChartVersion)
	}
	if chart.Lock == nil {
		t.Fatal("embedded chart must include its dependency lock")
	}
	bundled := make(map[string]string)
	for _, dependency := range chart.Dependencies() {
		if _, exists := bundled[dependency.Name()]; exists {
			t.Fatalf("duplicate bundled dependency %s", dependency.Name())
		}
		bundled[dependency.Name()] = dependency.Metadata.Version
	}
	if len(bundled) != len(chart.Lock.Dependencies) {
		t.Fatalf("got %d bundled dependencies, lock requires %d", len(bundled), len(chart.Lock.Dependencies))
	}
	for _, dependency := range chart.Lock.Dependencies {
		if bundled[dependency.Name] != dependency.Version {
			t.Errorf("bundled %s version %q differs from locked %q", dependency.Name, bundled[dependency.Name], dependency.Version)
		}
	}
	if bundled["opensearch-operator"] != "3.0.2" {
		t.Fatalf("canonical OpenSearch operator missing: %q", bundled["opensearch-operator"])
	}
}

func TestCanonicalOpenSearchResourcesRemainInstallable(t *testing.T) {
	out, err := helmTemplate(t, "--include-crds", "--set", "opensearch-operator.enabled=true")
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	for _, want := range []string{"name: opensearchclusters.opensearch.org", "kind: DaemonSet", "vm.max_map_count=262144", "SKIP_INIT_CONTAINER", "runAsUser: 65532"} {
		if !strings.Contains(out, want) {
			t.Errorf("canonical OpenSearch installation missing %q", want)
		}
	}
	out, err = helmTemplate(t, "--set", "opensearch-operator.enabled=true", "--set", "opensearchNodeSysctl.enabled=false")
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	if strings.Contains(out, "kind: DaemonSet") {
		t.Error("node sysctl opt-out must remain effective")
	}
	if !strings.Contains(out, "kind: Deployment") {
		t.Error("node sysctl opt-out must retain the operator")
	}
}
