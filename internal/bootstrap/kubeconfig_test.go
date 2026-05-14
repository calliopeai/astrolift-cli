package bootstrap

import (
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestBuildKubeconfigFromSAToken_WithCA(t *testing.T) {
	out, err := BuildKubeconfigFromSAToken(ServiceAccountTokenInput{
		ClusterSlug: "prd-eks",
		Endpoint:    "https://kubernetes.example.com",
		CACertPEM:   "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n",
		Token:       "secret-token",
	})
	if err != nil {
		t.Fatalf("BuildKubeconfigFromSAToken: %v", err)
	}
	if !strings.Contains(string(out), "current-context: prd-eks") {
		t.Errorf("missing current-context: %s", out)
	}
	// Round-trip parse to confirm it's structurally valid.
	var parsed map[string]any
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output is not valid yaml: %v\n%s", err, out)
	}
	clusters, _ := parsed["clusters"].([]any)
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
	first, _ := clusters[0].(map[string]any)
	cl, _ := first["cluster"].(map[string]any)
	if _, ok := cl["certificate-authority-data"]; !ok {
		t.Errorf("expected CA to land as certificate-authority-data, got %v", cl)
	}
}

func TestBuildKubeconfigFromSAToken_NoCA_SkipsTLS(t *testing.T) {
	out, err := BuildKubeconfigFromSAToken(ServiceAccountTokenInput{
		ClusterSlug: "dev",
		Endpoint:    "https://api.local",
		Token:       "abc",
	})
	if err != nil {
		t.Fatalf("BuildKubeconfigFromSAToken: %v", err)
	}
	if !strings.Contains(string(out), "insecure-skip-tls-verify: true") {
		t.Errorf("expected insecure-skip-tls-verify=true when CA is empty:\n%s", out)
	}
}

func TestBuildKubeconfigFromSAToken_RejectsEmpty(t *testing.T) {
	if _, err := BuildKubeconfigFromSAToken(ServiceAccountTokenInput{Endpoint: "", Token: "t"}); err == nil {
		t.Error("expected error for empty endpoint")
	}
	if _, err := BuildKubeconfigFromSAToken(ServiceAccountTokenInput{Endpoint: "https://x", Token: ""}); err == nil {
		t.Error("expected error for empty token")
	}
}

func TestKubeconfigSource_Materialize_Inline(t *testing.T) {
	src := &KubeconfigSource{InlineYAML: []byte("apiVersion: v1\nkind: Config\n")}
	if err := src.Materialize(t.TempDir()); err != nil {
		t.Fatalf("Materialize inline: %v", err)
	}
	if src.Path == "" {
		t.Fatal("Materialize did not set Path")
	}
	data, err := os.ReadFile(src.Path)
	if err != nil {
		t.Fatalf("reading materialized path: %v", err)
	}
	if !strings.Contains(string(data), "apiVersion: v1") {
		t.Errorf("materialized file missing inline content: %s", data)
	}
	if err := src.Cleanup(); err != nil {
		t.Errorf("Cleanup: %v", err)
	}
	if _, err := os.Stat(src.Path); !os.IsNotExist(err) {
		t.Errorf("Cleanup did not remove tmpfile, err=%v", err)
	}
	// Idempotent
	if err := src.Cleanup(); err != nil {
		t.Errorf("second Cleanup not a no-op: %v", err)
	}
}

func TestKubeconfigSource_Materialize_FilePath(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/kubeconfig.yaml"
	if err := os.WriteFile(p, []byte("apiVersion: v1\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	src := &KubeconfigSource{FilePath: p}
	if err := src.Materialize(dir); err != nil {
		t.Fatalf("Materialize filepath: %v", err)
	}
	if src.Path != p {
		t.Errorf("expected Path=%s, got %s", p, src.Path)
	}
	if src.CleanupFn != nil {
		t.Errorf("CleanupFn should be nil for FilePath source — Materialize must not own user-supplied files")
	}
}

func TestKubeconfigSource_Materialize_RejectsEmpty(t *testing.T) {
	src := &KubeconfigSource{}
	if err := src.Materialize(""); err == nil {
		t.Error("expected error when neither InlineYAML nor FilePath is set")
	}
}
