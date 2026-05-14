// Package bootstrap holds the operator-side helpers behind
// `astro cluster bootstrap`. The cmd layer keeps the cobra plumbing; this
// package keeps the chart-install + kubeconfig-resolution logic so it can
// be unit-tested without spinning up a cobra command tree.
package bootstrap

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// KubeconfigSource describes how the bootstrap command will hand a
// kubeconfig to the helm SDK. One of the three modes is populated; the
// caller picks the right one from TenantCluster.auth_method.
type KubeconfigSource struct {
	// InlineYAML is a full kubeconfig document — used when auth_method
	// is "kubeconfig" and the operator's CLI has the YAML in hand.
	InlineYAML []byte

	// FilePath points at an existing kubeconfig file. Used when
	// auth_method is "exec_plugin" (the operator ran
	// `aws eks update-kubeconfig` / similar before calling us) or
	// when the operator passed --kubeconfig.
	FilePath string

	// Path is the resolved-on-disk kubeconfig path the helm SDK should
	// read. Set by Materialize regardless of input mode — Materialize
	// writes InlineYAML / minimal kubeconfigs to a tmpfile and returns
	// the path here.
	Path string

	// CleanupFn is called when the bootstrap is done. nil-safe.
	CleanupFn func() error
}

// Cleanup invokes CleanupFn iff set. Safe to call multiple times.
func (k *KubeconfigSource) Cleanup() error {
	if k == nil || k.CleanupFn == nil {
		return nil
	}
	fn := k.CleanupFn
	k.CleanupFn = nil
	return fn()
}

// ServiceAccountTokenInput is the shape needed to assemble a minimal
// kubeconfig when the cluster auth_method is "service_account_token".
type ServiceAccountTokenInput struct {
	ClusterSlug string
	Endpoint    string
	CACertPEM   string // optional; if empty, insecureSkipTLSVerify is set
	Token       string
}

// BuildKubeconfigFromSAToken assembles a single-context kubeconfig YAML
// that the helm SDK can read. The result is intentionally minimal — one
// cluster, one user, one context — so an operator inspecting the on-disk
// tmpfile can see exactly what the CLI built.
func BuildKubeconfigFromSAToken(in ServiceAccountTokenInput) ([]byte, error) {
	if in.Endpoint == "" {
		return nil, fmt.Errorf("cluster endpoint is empty — register with --endpoint")
	}
	if in.Token == "" {
		return nil, fmt.Errorf("auth_config.token is empty — re-register the cluster")
	}
	if in.ClusterSlug == "" {
		in.ClusterSlug = "astrolift"
	}
	clusterEntry := map[string]any{
		"server": in.Endpoint,
	}
	if in.CACertPEM != "" {
		clusterEntry["certificate-authority-data"] = base64.StdEncoding.EncodeToString([]byte(in.CACertPEM))
	} else {
		clusterEntry["insecure-skip-tls-verify"] = true
	}
	doc := map[string]any{
		"apiVersion":      "v1",
		"kind":            "Config",
		"current-context": in.ClusterSlug,
		"clusters": []map[string]any{
			{
				"name":    in.ClusterSlug,
				"cluster": clusterEntry,
			},
		},
		"users": []map[string]any{
			{
				"name": "astrolift-control-plane",
				"user": map[string]any{
					"token": in.Token,
				},
			},
		},
		"contexts": []map[string]any{
			{
				"name": in.ClusterSlug,
				"context": map[string]any{
					"cluster": in.ClusterSlug,
					"user":    "astrolift-control-plane",
				},
			},
		},
	}
	return yaml.Marshal(doc)
}

// Materialize writes InlineYAML (when present) to a tmpfile and sets
// Path. If FilePath is set instead, Path = FilePath and no tmpfile is
// created. Returns an error if neither input mode is populated.
func (k *KubeconfigSource) Materialize(scratchDir string) error {
	switch {
	case len(k.InlineYAML) > 0:
		if scratchDir == "" {
			scratchDir = os.TempDir()
		}
		f, err := os.CreateTemp(scratchDir, "astro-kubeconfig-*.yaml")
		if err != nil {
			return fmt.Errorf("tmpfile for kubeconfig: %w", err)
		}
		if _, err := f.Write(k.InlineYAML); err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return fmt.Errorf("writing kubeconfig: %w", err)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(f.Name())
			return fmt.Errorf("closing kubeconfig tmpfile: %w", err)
		}
		path := f.Name()
		k.Path = path
		k.CleanupFn = func() error { return os.Remove(path) }
		return nil
	case k.FilePath != "":
		abs, err := filepath.Abs(k.FilePath)
		if err != nil {
			return fmt.Errorf("resolving kubeconfig path: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("kubeconfig %s: %w", abs, err)
		}
		k.Path = abs
		return nil
	default:
		return fmt.Errorf("kubeconfig source has neither inline YAML nor file path")
	}
}
