package bootstrap

import (
	"testing"

	"helm.sh/helm/v3/pkg/release"
)

func TestMergeValues_BasePreservedWhenUserNil(t *testing.T) {
	base := []byte("certManager:\n  enabled: true\n  installCRDs: true\n")
	got, err := mergeValues(base, nil)
	if err != nil {
		t.Fatalf("mergeValues: %v", err)
	}
	section, _ := got["certManager"].(map[string]any)
	if section == nil {
		t.Fatalf("expected certManager section, got %v", got)
	}
	enabled, _ := section["enabled"].(bool)
	if !enabled {
		t.Errorf("expected certManager.enabled=true, got %v", section)
	}
}

func TestMergeValues_UserOverridesBase(t *testing.T) {
	base := []byte("certManager:\n  enabled: true\n  installCRDs: true\n")
	user := []byte("certManager:\n  installCRDs: false\n")
	got, err := mergeValues(base, user)
	if err != nil {
		t.Fatalf("mergeValues: %v", err)
	}
	section, _ := got["certManager"].(map[string]any)
	enabled, _ := section["enabled"].(bool)
	if !enabled {
		t.Errorf("user override should not clobber base enabled=true: %v", section)
	}
	installCRDs, _ := section["installCRDs"].(bool)
	if installCRDs {
		t.Errorf("user installCRDs=false should win, got %v", section)
	}
}

func TestMergeValues_RejectsBadYAML(t *testing.T) {
	if _, err := mergeValues([]byte("not: yaml: bad"), nil); err == nil {
		t.Error("expected error on malformed base values")
	}
	if _, err := mergeValues(nil, []byte("not: yaml: bad")); err == nil {
		t.Error("expected error on malformed user values")
	}
}

func TestSubchartStatusFromValues(t *testing.T) {
	values := map[string]any{
		"certManager":  map[string]any{"enabled": true},
		"longhorn":     map[string]any{"enabled": false},
		"ingressNginx": map[string]any{"enabled": true},
		// strimzi intentionally absent — expect "disabled"
	}
	got := subchartStatusFromValues(values)
	if got["certManager"] != "enabled" {
		t.Errorf("certManager: got %q, want enabled", got["certManager"])
	}
	if got["longhorn"] != "disabled" {
		t.Errorf("longhorn: got %q, want disabled", got["longhorn"])
	}
	if got["ingressNginx"] != "enabled" {
		t.Errorf("ingressNginx: got %q, want enabled", got["ingressNginx"])
	}
	if got["strimzi"] != "disabled" {
		t.Errorf("strimzi (absent): got %q, want disabled", got["strimzi"])
	}
}

// A first install that fails leaves revision 1 in `failed`, and helm
// refuses to install over it while `--upgrade` has no previous revision
// to work from — so the operator has to know to `helm uninstall` by hand
// (#1707). Two halves: new installs roll themselves back, and a release
// already wedged is recognised and named.

func TestIsWedged_FailedFirstRevision(t *testing.T) {
	rel := &release.Release{
		Version: 1,
		Info:    &release.Info{Status: release.StatusFailed},
	}
	if !isWedged(rel) {
		t.Error("a failed revision 1 is wedged: there is nothing to upgrade or roll back from")
	}
}

func TestIsWedged_InterruptedFirstInstall(t *testing.T) {
	// A bootstrap killed mid-install leaves pending-install, which helm
	// will not install over either.
	for _, status := range []release.Status{
		release.StatusPendingInstall,
		release.StatusUninstalling,
	} {
		rel := &release.Release{Version: 1, Info: &release.Info{Status: status}}
		if !isWedged(rel) {
			t.Errorf("status %s at revision 1 should be wedged", status)
		}
	}
}

func TestIsWedged_DeployedFirstRevisionIsFine(t *testing.T) {
	rel := &release.Release{
		Version: 1,
		Info:    &release.Info{Status: release.StatusDeployed},
	}
	if isWedged(rel) {
		t.Error("a healthy release is not wedged")
	}
}

func TestIsWedged_LaterFailedRevisionIsNotWedged(t *testing.T) {
	// helm can roll this one back, so it belongs to the --upgrade path
	// rather than to the uninstall-and-retry message.
	rel := &release.Release{
		Version: 3,
		Info:    &release.Info{Status: release.StatusFailed},
	}
	if isWedged(rel) {
		t.Error("a failed revision 3 has a previous revision; not wedged")
	}
}

func TestIsWedged_NilsAreNotWedged(t *testing.T) {
	if isWedged(nil) {
		t.Error("nil release")
	}
	if isWedged(&release.Release{Version: 1}) {
		t.Error("release with no Info")
	}
}
