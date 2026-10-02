package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testEnvironmentReview() environmentActionReview {
	version := 3
	return environmentActionReview{Format: 1, Server: "https://control.example.test", Organization: "org-guid", Actor: 17, AppSlug: "api", WorkloadSlug: "web", ViewerCan: json.RawMessage(`{"restart":{"allowed":true}}`), Target: environmentActionTarget{
		WorkloadID: "c426ed1d-aab9-4ae0-aed2-99db817191f1", WorkloadVersion: &version,
		AppID: "da2ab935-f532-4c04-8918-ad43cd305652", AppVersion: &version,
		EnvironmentID: "ae3f9c69-c478-440d-a975-09fcf379f4c0", EnvironmentName: "staging", EnvironmentVersion: &version,
		ClusterID: "649a719b-cb32-481c-9bf1-c08c9375dc2c", ClusterVersion: &version, Namespace: "api-staging",
	}}
}

func TestEnvironmentReviewPreservesTheSavedSnapshot(t *testing.T) {
	review := testEnvironmentReview()
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "review.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadEnvironmentActionReview(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Target.EnvironmentID != review.Target.EnvironmentID || loaded.Actor != review.Actor || loaded.Server != review.Server || *loaded.Target.WorkloadVersion != 3 {
		t.Fatalf("review identity changed: %#v", loaded)
	}
}

func TestEnvironmentReviewRefusesIncompleteOrAmbiguousFiles(t *testing.T) {
	valid, err := json.Marshal(testEnvironmentReview())
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`null`, `{}`, string(valid) + "\n{}", string(valid) + strings.Repeat(" ", 65536),
		strings.Replace(string(valid), `"workloadVersion":3`, `"workloadVersion":null`, 1),
		strings.Replace(string(valid), `"workloadVersion":3`, `"workloadVersion":true`, 1),
		strings.Replace(string(valid), `"environmentId":"ae3f9c69-c478-440d-a975-09fcf379f4c0"`, `"environmentId":"staging"`, 1),
		strings.Replace(string(valid), `https://control.example.test`, `https://username:secret@control.example.test`, 1),
		strings.Replace(string(valid), `https://control.example.test`, `https://control.example.test?token=secret`, 1),
	} {
		path := filepath.Join(t.TempDir(), "review.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadEnvironmentActionReview(path); err == nil {
			t.Fatal("accepted an incomplete, oversized or credential-bearing review")
		}
	}
}

func TestExplicitExecRejectsUnsupportedOrIncompleteTargetAuthority(t *testing.T) {
	f := false
	valid := execEnvironmentTarget{environmentActionTarget: testEnvironmentReview().Target, PodName: "web-abc", PodUID: "physical-incarnation", Container: "main", PodBinding: "PREFLIGHT_ONLY", Resumable: &f}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*execEnvironmentTarget){
		func(t *execEnvironmentTarget) { t.PodUID = "" },
		func(t *execEnvironmentTarget) { t.Resumable = nil },
		func(t *execEnvironmentTarget) { t.PodBinding = "ATOMIC" },
		func(t *execEnvironmentTarget) { t.ClusterVersion = nil },
		func(t *execEnvironmentTarget) { yes := true; t.Resumable = &yes },
	} {
		target := valid
		change(&target)
		if err := target.validate(); err == nil {
			t.Fatal("accepted unsupported or incomplete exec authority")
		}
	}
}
