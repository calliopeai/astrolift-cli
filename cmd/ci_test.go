package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// ---- ci render (astroliftRenderedManifest) ---------------------------------

func TestCiRenderPrintsManifests(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftRenderedManifest": map[string]interface{}{
			"appSlug": "web", "environmentName": "production", "imageTag": "sha-1",
			"namespace": "acme-web",
			"resources": []map[string]interface{}{
				{"kind": "Deployment", "metadata": map[string]interface{}{"name": "web"}},
			},
			"error": nil, "errorPath": nil, "errorLine": nil, "errorColumn": nil,
		},
	}, nil)
	defer srv.Close()

	t.Setenv("ASTROLIFT_API_URL", srv.URL)
	t.Setenv("ASTROLIFT_DEPLOY_TOKEN", "tok")
	t.Setenv("ASTROLIFT_APP_SLUG", "web")

	out := &bytes.Buffer{}
	ciRenderCmd.SetOut(out)
	ciRenderCmd.SetContext(context.Background())
	if err := ciRenderCmd.RunE(ciRenderCmd, nil); err != nil {
		t.Fatalf("ci render: %v", err)
	}
	for _, want := range []string{"Deployment", "web"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("render output missing %q:\n%s", want, out.String())
		}
	}
}

func TestCiRenderSurfacesManifestError(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftRenderedManifest": map[string]interface{}{
			"appSlug": "web", "environmentName": "production", "imageTag": "",
			"namespace": "", "resources": nil,
			"error": "unknown workload kind", "errorPath": "astrolift.toml", "errorLine": 12, "errorColumn": nil,
		},
	}, nil)
	defer srv.Close()

	t.Setenv("ASTROLIFT_API_URL", srv.URL)
	t.Setenv("ASTROLIFT_DEPLOY_TOKEN", "tok")
	t.Setenv("ASTROLIFT_APP_SLUG", "web")

	out := &bytes.Buffer{}
	ciRenderCmd.SetOut(out)
	ciRenderCmd.SetContext(context.Background())
	err := ciRenderCmd.RunE(ciRenderCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown workload kind") ||
		!strings.Contains(err.Error(), "astrolift.toml:12") {
		t.Fatalf("expected manifest render error with location, got %v", err)
	}
}
