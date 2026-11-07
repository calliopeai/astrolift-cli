package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/nvketooling"
	"github.com/spf13/cobra"
)

func TestNVKEToolingCatalogIsAvailableOffline(t *testing.T) {
	command := newNVKEToolingCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"catalog"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var catalog nvketooling.Manifest
	if json.Unmarshal(output.Bytes(), &catalog) != nil || len(catalog.Commands) != 14 {
		t.Fatal("tooling catalog omitted the pinned native families")
	}
}

func TestNVKEToolingSkipsPlatformConfigAndReleaseLookupHooks(t *testing.T) {
	lookup := false
	root := &cobra.Command{Use: "fixture", PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
		return errors.New("platform config must not be read")
	}, PersistentPostRun: func(_ *cobra.Command, _ []string) { lookup = true }}
	root.AddCommand(newNVKEToolingCommand())
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"tooling", "catalog"})
	if err := root.Execute(); err != nil || lookup {
		t.Fatalf("local tooling acquired platform/release authority: %v", err)
	}
}

func TestNVKEToolingRequiresANativeArgumentBoundary(t *testing.T) {
	command := newNVKEToolingCommand()
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs([]string{"run", "--bundle", "bundle", "--workspace", "workspace", "version"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "after --") {
		t.Fatal("accepted a native command without the explicit argument boundary")
	}
}

func TestNVKEToolingRealRunThroughCobra(t *testing.T) {
	bundle := os.Getenv("NVKE_TEST_NATIVE_BUNDLE")
	if bundle == "" {
		t.Skip("set NVKE_TEST_NATIVE_BUNDLE for real native command proof")
	}
	workspace := t.TempDir()
	if err := os.Chmod(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	command := newNVKEToolingCommand()
	command.SetContext(context.Background())
	command.PersistentFlags().Bool("json", true, "")
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"run", "--bundle", bundle, "--workspace", workspace, "--", "version"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var result nvketooling.Result
	if json.Unmarshal(output.Bytes(), &result) != nil || result.Phase != "completed" || result.ExitCode != 0 {
		t.Fatal("native result metadata was not returned through the CLI")
	}
	content, err := os.ReadFile(result.Stdout)
	if err != nil || !bytes.Contains(content, []byte("92c39b8ad563")) || bytes.Contains(output.Bytes(), []byte("nvkectl astro-")) {
		t.Fatal("native version contents were missing or printed instead of kept as a private artifact")
	}
}
