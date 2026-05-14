package bootstrap

import (
	"testing"
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
