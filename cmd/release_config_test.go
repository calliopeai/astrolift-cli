package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// These tests exist because both #46 and #53 were caused by the release
// configuration drifting away from the code with nothing to catch it: the
// ldflags stopped reaching the version vars, and the archive names stopped
// matching what the installer asked for. Asserting the wiring by actually
// building with it is the only check that would have failed at the time.

type goreleaserConfig struct {
	ProjectName string `yaml:"project_name"`
	Builds      []struct {
		ID      string   `yaml:"id"`
		Ldflags []string `yaml:"ldflags"`
	} `yaml:"builds"`
	Archives []struct {
		NameTemplate    string   `yaml:"name_template"`
		Formats         []string `yaml:"formats"`
		FormatOverrides []struct {
			Goos    string   `yaml:"goos"`
			Formats []string `yaml:"formats"`
		} `yaml:"format_overrides"`
	} `yaml:"archives"`
	Checksum struct {
		NameTemplate string `yaml:"name_template"`
	} `yaml:"checksum"`
}

func loadGoreleaserConfig(t *testing.T) goreleaserConfig {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("reading .goreleaser.yaml: %v", err)
	}
	var config goreleaserConfig
	if err := yaml.Unmarshal(body, &config); err != nil {
		t.Fatalf("parsing .goreleaser.yaml: %v", err)
	}
	if len(config.Builds) == 0 {
		t.Fatal(".goreleaser.yaml declares no builds")
	}
	if len(config.Archives) == 0 {
		t.Fatal(".goreleaser.yaml declares no archives")
	}
	return config
}

// renderReleaseLdflags substitutes the GoReleaser template values with fixed
// test values so the release flags can be handed straight to `go build`. An
// unrecognized template fails loudly rather than silently building a binary
// with a literal "{{ .Something }}" baked in.
func renderReleaseLdflags(t *testing.T, flags []string, version, commit, date string) string {
	t.Helper()
	replacer := strings.NewReplacer(
		"{{ .Version }}", version,
		"{{ .ShortCommit }}", commit,
		"{{ .FullCommit }}", commit,
		"{{ .Commit }}", commit,
		"{{ .Date }}", date,
	)
	rendered := make([]string, 0, len(flags))
	for _, flag := range flags {
		value := replacer.Replace(flag)
		if strings.Contains(value, "{{") {
			t.Fatalf("unrecognized GoReleaser template in ldflag %q — teach this test "+
				"how to render it so release ldflags stay verified", flag)
		}
		rendered = append(rendered, value)
	}
	return strings.Join(rendered, " ")
}

func buildAstro(t *testing.T, ldflags string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "astro")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	args := []string{"build", "-o", binary}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, "..")
	build := exec.Command("go", args...)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}
	return binary
}

// TestReleaseLdflagsInjectTheVersionStamp is the regression guard for #46.
// It builds with the exact ldflags .goreleaser.yaml uses and asserts the
// values come back out of `astro version`. If someone renames cmd.Version or
// changes the module path without updating the release config, the injected
// values silently stop landing and this fails.
func TestReleaseLdflagsInjectTheVersionStamp(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the binary; skipped under -short")
	}
	config := loadGoreleaserConfig(t)

	const (
		wantVersion = "9.9.9"
		wantCommit  = "abc1234"
		wantDate    = "2026-01-02T03:04:05Z"
	)
	ldflags := renderReleaseLdflags(t, config.Builds[0].Ldflags, wantVersion, wantCommit, wantDate)
	if !strings.Contains(ldflags, "-X ") {
		t.Fatalf("release ldflags inject nothing: %q", ldflags)
	}

	output, err := exec.Command(buildAstro(t, ldflags), "version").CombinedOutput()
	if err != nil {
		t.Fatalf("astro version failed: %v\n%s", err, output)
	}
	got := strings.TrimSpace(string(output))
	for _, want := range []string{wantVersion, wantCommit, wantDate} {
		if !strings.Contains(got, want) {
			t.Errorf("release ldflags did not reach the binary: %q missing from %q", want, got)
		}
	}
	if strings.Contains(got, "dev") {
		t.Errorf("stamped build still reports a dev version: %q", got)
	}
}

// TestPlainBuildReportsDev is the other half of the contract: an unstamped
// source build must stay distinguishable from a release build.
func TestPlainBuildReportsDev(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the binary; skipped under -short")
	}
	output, err := exec.Command(buildAstro(t, ""), "version").CombinedOutput()
	if err != nil {
		t.Fatalf("astro version failed: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "astro dev (commit none, built unknown)" {
		t.Errorf("plain `go build` should report an unstamped version, got %q", got)
	}
}

// TestResolveAssetNameMatchesGoreleaserTemplate is the regression guard for
// #53: the name the CLI asks for must be the name GoReleaser publishes.
func TestResolveAssetNameMatchesGoreleaserTemplate(t *testing.T) {
	config := loadGoreleaserConfig(t)
	archive := config.Archives[0]

	expected := strings.NewReplacer(
		"{{ .ProjectName }}", config.ProjectName,
		"{{ .Os }}", runtime.GOOS,
		"{{ .Arch }}", runtime.GOARCH,
	).Replace(archive.NameTemplate)
	if strings.Contains(expected, "{{") {
		t.Fatalf("unrendered template in archive name_template %q", archive.NameTemplate)
	}

	format := "tar.gz"
	if len(archive.Formats) > 0 {
		format = archive.Formats[0]
	}
	for _, override := range archive.FormatOverrides {
		if override.Goos == runtime.GOOS && len(override.Formats) > 0 {
			format = override.Formats[0]
		}
	}
	expected += "." + format

	if got := resolveAssetName(); got != expected {
		t.Errorf("resolveAssetName() = %q, but GoReleaser publishes %q", got, expected)
	}
}

// TestChecksumAssetNameMatchesInstaller pins the third name in the chain: the
// checksum file scripts/install.sh downloads and verifies against.
func TestChecksumAssetNameMatchesInstaller(t *testing.T) {
	config := loadGoreleaserConfig(t)
	const want = "astro-checksums.txt"
	if config.Checksum.NameTemplate != want {
		t.Errorf("checksum name_template = %q, want %q (scripts/install.sh expects it)",
			config.Checksum.NameTemplate, want)
	}
	script, err := os.ReadFile(filepath.Join("..", "scripts", "install.sh"))
	if err != nil {
		t.Fatalf("reading install.sh: %v", err)
	}
	if !strings.Contains(string(script), `${BINARY}-checksums.txt`) {
		t.Error("install.sh no longer derives the checksum filename from ${BINARY}")
	}
}

// TestResolveAssetNameShape locks the specific shape that regressed: no
// version segment, hyphens not underscores, lower-case GOOS, and amd64 rather
// than x86_64.
func TestResolveAssetNameShape(t *testing.T) {
	got := resolveAssetName()
	if strings.Contains(got, "_") {
		t.Errorf("asset name must use hyphens, got %q", got)
	}
	if strings.ContainsAny(got, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		t.Errorf("asset name must be lower-case, got %q", got)
	}
	if strings.Contains(got, "x86_64") {
		t.Errorf("asset name must use the Go arch name, got %q", got)
	}
	if !strings.HasPrefix(got, "astro-"+runtime.GOOS+"-"+runtime.GOARCH) {
		t.Errorf("asset name %q does not match astro-%s-%s", got, runtime.GOOS, runtime.GOARCH)
	}
}
