package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

type scaffoldedManifest struct {
	AstroliftVersion int    `toml:"astrolift_version"`
	Name             string `toml:"name"`
	App              struct {
		Slug        string `toml:"slug"`
		DisplayName string `toml:"display_name"`
	} `toml:"app"`
	Environments map[string]map[string]interface{} `toml:"environments"`
	Workloads    []struct {
		Name          string `toml:"name"`
		Kind          string `toml:"kind"`
		IsPublic      bool   `toml:"is_public"`
		CPURequest    string `toml:"cpu_request"`
		CPULimit      string `toml:"cpu_limit"`
		MemoryRequest string `toml:"memory_request"`
		MemoryLimit   string `toml:"memory_limit"`
		Containers    []struct {
			Name        string `toml:"name"`
			IsPrimary   bool   `toml:"is_primary"`
			Port        int    `toml:"port"`
			Healthcheck struct {
				Kind  string `toml:"kind"`
				Value string `toml:"value"`
				Port  int    `toml:"port"`
			} `toml:"healthcheck"`
			Resources map[string]interface{} `toml:"resources"`
		} `toml:"containers"`
	} `toml:"workloads"`
}

func TestScaffoldManifestMatchesCurrentServerShape(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orders-api")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "astrolift.toml")
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	if err := scaffoldManifest(path, cmd); err != nil {
		t.Fatalf("scaffoldManifest: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest scaffoldedManifest
	if _, err := toml.Decode(string(body), &manifest); err != nil {
		t.Fatalf("generated TOML does not parse: %v\n%s", err, body)
	}
	if manifest.AstroliftVersion != 1 || manifest.Name != "orders-api" {
		t.Fatalf("unexpected identity: version=%d name=%q", manifest.AstroliftVersion, manifest.Name)
	}
	if manifest.App.Slug != "orders-api" || manifest.App.DisplayName != "orders-api" {
		t.Fatalf("unexpected [app] identity: %#v", manifest.App)
	}
	if _, ok := manifest.Environments["production"]; !ok {
		t.Fatalf("generated manifest has no [environments.production]: %#v", manifest.Environments)
	}
	if len(manifest.Workloads) != 1 || len(manifest.Workloads[0].Containers) != 1 {
		t.Fatalf("unexpected workload/container count: %#v", manifest.Workloads)
	}
	workload := manifest.Workloads[0]
	if !workload.IsPublic {
		t.Fatalf("scaffolded web workload must be public by default (cli#79): %#v", workload)
	}
	if workload.CPURequest == "" || workload.CPULimit == "" || workload.MemoryRequest == "" || workload.MemoryLimit == "" {
		t.Fatalf("resources must be direct workload fields: %#v", workload)
	}
	container := workload.Containers[0]
	if container.Resources != nil {
		t.Fatalf("obsolete nested container resources table was emitted: %#v", container.Resources)
	}
	if !container.IsPrimary || container.Healthcheck.Kind != "http" || container.Healthcheck.Port != container.Port {
		t.Fatalf("unexpected primary container health check: %#v", container)
	}
}

func TestScaffoldManifestRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "astrolift.toml")
	if err := os.WriteFile(path, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := scaffoldManifest(path, &cobra.Command{}); err == nil {
		t.Fatal("scaffoldManifest overwrote an existing file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "keep me\n" {
		t.Fatalf("existing file changed: %q", body)
	}
}
