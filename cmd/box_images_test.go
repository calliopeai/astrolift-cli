package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

func TestBoxImagesDedupesSpecImagesAndSkipsBlankOnes(t *testing.T) {
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "astroliftOrganizations") {
			return map[string]interface{}{"astroliftOrganizations": []map[string]string{{"id": "org-guid", "slug": "acme", "name": "Acme"}}}
		}
		if req.Variables["orgId"] != "org-guid" {
			t.Errorf("specs not read for the working org: %#v", req.Variables)
		}
		return map[string]interface{}{"agentEnvironmentSpecs": []map[string]string{
			{"slug": "dev", "imageTag": "ghcr.io/acme/agent:1"},
			{"slug": "ci", "imageTag": " ghcr.io/acme/agent:1 "},
			{"slug": "gpu", "imageTag": "ghcr.io/acme/agent-gpu:2"},
			{"slug": "default", "imageTag": ""},
		}}
	})
	defer srv.Close()
	cmd, out := appTestCmd()
	cmd.Flags().Set("json", "true") //nolint:errcheck
	if err := runBoxImages(cmd, context.Background(), api.NewClient(srv.URL, "token", false), &config.Config{}); err != nil {
		t.Fatal(err)
	}
	var images []boxImage
	if err := json.Unmarshal(out.Bytes(), &images); err != nil {
		t.Fatal(err)
	}
	want := []boxImage{
		{Image: "ghcr.io/acme/agent-gpu:2", EnvSpecs: []string{"gpu"}},
		{Image: "ghcr.io/acme/agent:1", EnvSpecs: []string{"ci", "dev"}},
	}
	if len(images) != len(want) {
		t.Fatalf("images = %#v", images)
	}
	for i := range want {
		if images[i].Image != want[i].Image || strings.Join(images[i].EnvSpecs, ",") != strings.Join(want[i].EnvSpecs, ",") {
			t.Fatalf("images[%d] = %#v, want %#v", i, images[i], want[i])
		}
	}
}
