package charts

import (
	"os/exec"
	"strings"
	"testing"
)

// The ClusterIssuer is a cert-manager CRD, and the same release installs
// cert-manager — so rendering it as an ordinary release resource makes a
// fresh install die on
//
//	resource mapping not found for kind "ClusterIssuer" ... ensure CRDs are installed first
//
// A single release cannot satisfy that ordering on its own, so it is a
// post-install hook (#1695). This asserts the annotations survive, because
// dropping them brings the failure straight back and only on a cluster
// that has never had cert-manager — the hardest case to notice.

func helmTemplate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not on PATH")
	}
	base := []string{"template", "t", "astrolift-prereqs"}
	out, err := exec.Command("helm", append(base, args...)...).CombinedOutput()
	return string(out), err
}

func TestClusterIssuerIsAPostInstallHook(t *testing.T) {
	out, err := helmTemplate(t,
		"--set", "certManager.enabled=true",
		"--set", "clusterIssuer.enabled=true",
		"--set", "clusterIssuer.email=ops@example.com",
	)
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	if !strings.Contains(out, "kind: ClusterIssuer") {
		t.Fatalf("ClusterIssuer not rendered:\n%s", out)
	}
	for _, want := range []string{
		"helm.sh/hook: post-install,post-upgrade",
		"helm.sh/hook-delete-policy: before-hook-creation",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q — a plain resource cannot install before its own CRD", want)
		}
	}
}

func TestClusterIssuerOffByDefault(t *testing.T) {
	out, err := helmTemplate(t)
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	if strings.Contains(out, "kind: ClusterIssuer") {
		t.Error("clusterIssuer.enabled defaults to false; an operator with a private CA ships their own")
	}
}

func TestMissingIssuerEmailNamesTheFix(t *testing.T) {
	out, err := helmTemplate(t,
		"--set", "certManager.enabled=true",
		"--set", "clusterIssuer.enabled=true",
	)
	if err == nil {
		t.Fatal("expected a render failure: ACME needs an email")
	}
	// The old message stated the requirement and stopped there, which is
	// where the documented happy path dead-ended.
	for _, want := range []string{"--set clusterIssuer.email=", "clusterIssuer.enabled=false"} {
		if !strings.Contains(out, want) {
			t.Errorf("error message should carry the way out, missing %q:\n%s", want, out)
		}
	}
}
