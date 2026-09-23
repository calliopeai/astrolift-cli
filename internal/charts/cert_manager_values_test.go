package charts

import (
	"strings"
	"testing"
)

// The cert-manager dependency in Chart.yaml has no `alias`, so Helm only
// forwards subchart values nested under the literal, hyphenated
// `cert-manager:` key. Values nested under the camelCase `certManager:`
// key (which only feeds the Chart.yaml `condition: certManager.enabled`
// gate) are silently dropped. Every bundled cloud overlay set `installCRDs`
// under `certManager:` instead of `cert-manager:`, so the subchart fell
// back to its own upstream default (installCRDs: false): cert-manager's own
// CRDs never installed, and the startupapicheck post-install hook spun until
// BackoffLimitExceeded on "the cert-manager CRDs are not yet installed on
// the Kubernetes API server". Reproduced against a real kind cluster before
// this fix (#1695 defect 3).
func TestCertManagerSubchartReceivesInstallCRDs(t *testing.T) {
	for _, cloud := range []string{"aws", "azure", "gcp", "k8s"} {
		t.Run(cloud, func(t *testing.T) {
			out, err := helmTemplate(t,
				"--include-crds",
				"-f", "astrolift-prereqs/values."+cloud+".yaml",
				"--set", "clusterIssuer.email=ops@example.com",
			)
			if err != nil {
				t.Fatalf("helm template: %v\n%s", err, out)
			}
			if !strings.Contains(out, "name: certificaterequests.cert-manager.io") {
				t.Fatalf(
					"cert-manager CRDs not rendered for values.%s.yaml, installCRDs did not reach the cert-manager subchart:\n%s",
					cloud, out,
				)
			}
		})
	}
}
