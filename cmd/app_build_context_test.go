package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// SteadyMD's abbott-cgm: one app of a monorepo, its manifest in a
// sub-directory (#159). Registered without a context, its managed CI
// workflow built the repository root.
const monorepoManifestPath = "src/abbott-cgm/astrolift.toml"

const plainManifest = `[app]
slug = "abbott-cgm"
display_name = "Abbott CGM"

[[workloads]]
name = "web"
kind = "deployment"

[[workloads.containers]]
name = "web"
is_primary = true
`

// The ConflictHQ/bdr#139 shape: the manifest reaches the repository root.
const ownBuildPathsManifest = plainManifest + `build_context = "../.."
dockerfile_path = "Dockerfile.web"
`

func TestManifestDirectory(t *testing.T) {
	// The cases astrolift-app's test_build_context_159.py pins, so the CLI
	// shows what the platform would detect.
	for path, want := range map[string]string{
		"src/abbott-cgm/astrolift.toml":    "src/abbott-cgm",
		"apps/web/astrolift.toml":          "apps/web",
		"./services/api/astrolift.toml":    "services/api",
		"services//api/astrolift.toml":     "services/api",
		"astrolift.toml":                   ".",
		"":                                 ".",
		"../outside/astrolift.toml":        ".",
		"src/../../outside/astrolift.toml": ".",
		"/etc/astrolift.toml":              ".",
	} {
		if got := manifestDirectory(path); got != want {
			t.Errorf("manifestDirectory(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestManifestSetsBuildPaths(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
		want           bool
	}{
		{"none", plainManifest, false},
		{"only the defaults", plainManifest + "build_context = \".\"\ndockerfile_path = \"Dockerfile\"\n", false},
		{"container build_context", plainManifest + "build_context = \"../..\"\n", true},
		{"container dockerfile_path", plainManifest + "dockerfile_path = \"Dockerfile.web\"\n", true},
		// The platform reads only dockerfile_path on a container.
		{"container dockerfile alias", plainManifest + "dockerfile = \"Dockerfile.web\"\n", false},
		{"job dockerfile alias", "[[jobs]]\nname = \"nightly\"\ndockerfile = \"Dockerfile.jobs\"\n", true},
		{"task build_context", "[[tasks]]\nname = \"migrate\"\nbuild_context = \"..\"\n", true},
		{"inline containers", "[[workloads]]\nname = \"web\"\ncontainers = [{ name = \"web\", build_context = \"sub\" }]\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := manifestSetsBuildPaths(tc.manifest)
			if err != nil {
				t.Fatalf("manifestSetsBuildPaths: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// ---- astro app register ----------------------------------------------------

// registerRun is one register invocation against a fake platform.
type registerRun struct {
	cmd    *cobra.Command
	out    *bytes.Buffer
	errOut *bytes.Buffer
	seen   []gqlRequest
	client *api.Client
}

// newRegisterRun writes manifest to a temp file, points the register flags
// at it, and stands up a platform that registers anything it is sent.
func newRegisterRun(t *testing.T, manifest, manifestPath string, interactive bool) *registerRun {
	t.Helper()
	file := filepath.Join(t.TempDir(), "astrolift.toml")
	if err := os.WriteFile(file, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := []*string{
		&appRegisterFile, &appRegisterProjectID, &appRegisterSourceRepo, &appRegisterSourceKind,
		&appRegisterManifestPath, &appRegisterBuildContext, &appRegisterDockerfile,
		&appRegisterBuildMode, &appRegisterBuildStrategy, &appRegisterDescription, &appRegisterDefaultBranch,
	}
	values := make([]string, len(saved))
	for i, p := range saved {
		values[i] = *p
	}
	yes, raw, asJSON, prompt := appRegisterYes, appRegisterManifestRaw, appRegisterJSON, promptInteractive
	t.Cleanup(func() {
		for i, p := range saved {
			*p = values[i]
		}
		appRegisterYes, appRegisterManifestRaw, appRegisterJSON, promptInteractive = yes, raw, asJSON, prompt
	})
	for _, p := range saved {
		*p = ""
	}
	appRegisterYes, appRegisterManifestRaw, appRegisterJSON = false, false, false
	appRegisterFile = file
	appRegisterProjectID = "11111111-2222-4333-8444-555555555555"
	appRegisterSourceRepo = "steadymd/monorepo"
	appRegisterSourceKind = "github"
	appRegisterManifestPath = manifestPath
	promptInteractive = func() bool { return interactive }

	run := &registerRun{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		run.seen = append(run.seen, req)
		if strings.Contains(req.Query, "astroliftServerInfo") {
			return map[string]interface{}{"astroliftServerInfo": map[string]interface{}{"capabilities": []string{}}}
		}
		input, _ := req.Variables["input"].(map[string]interface{})
		context, _ := input["buildContext"].(string)
		dockerfile, ok := input["dockerfilePath"].(string)
		if !ok {
			dockerfile = "Dockerfile"
		}
		return map[string]interface{}{"registerApp": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"id": testAppGUID, "slug": "abbott-cgm", "name": "Abbott CGM",
				"organizationSlug": "steadymd", "projectSlug": "cgm", "k8sNamespace": "steadymd-abbott-cgm",
				"buildContext": context, "dockerfilePath": dockerfile,
			},
		}}
	})
	t.Cleanup(srv.Close)
	run.client = api.NewClient(srv.URL, "tok", false)

	root := &cobra.Command{}
	root.PersistentFlags().Bool("no-prompt", false, "")
	c := &cobra.Command{}
	c.Flags().StringVar(&appRegisterBuildContext, "build-context", "", "")
	c.Flags().StringVar(&appRegisterDockerfile, "dockerfile", "", "")
	c.Flags().StringVar(&appRegisterDockerfile, "dockerfile-path", "", "")
	root.AddCommand(c)
	c.SetOut(run.out)
	c.SetErr(run.errOut)
	c.SetIn(strings.NewReader(""))
	run.cmd = c
	return run
}

func (r *registerRun) register(t *testing.T) error {
	t.Helper()
	return runAppRegister(r.cmd, context.Background(), r.client)
}

// input is the registerApp input the platform received, or nil.
func (r *registerRun) input(t *testing.T) map[string]interface{} {
	t.Helper()
	for _, req := range r.seen {
		if strings.Contains(req.Query, "registerApp(") {
			input, _ := req.Variables["input"].(map[string]interface{})
			return input
		}
	}
	return nil
}

func TestRegisterSendsTheDetectedContextWithoutATerminal(t *testing.T) {
	run := newRegisterRun(t, plainManifest, monorepoManifestPath, false)
	if err := run.register(t); err != nil {
		t.Fatalf("register: %v", err)
	}
	input := run.input(t)
	if input["buildContext"] != "src/abbott-cgm" {
		t.Errorf("buildContext = %v, want src/abbott-cgm", input["buildContext"])
	}
	if _, ok := input["dockerfilePath"]; ok {
		t.Errorf("dockerfilePath sent without being asked for: %v", input["dockerfilePath"])
	}
	if strings.Contains(run.errOut.String(), "Use this?") {
		t.Errorf("prompted without a terminal:\n%s", run.errOut.String())
	}
	got := run.out.String()
	for _, want := range []string{"Build context:    src/abbott-cgm", "Dockerfile:       Dockerfile"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRegisterAsksAndEnterAcceptsTheDetection(t *testing.T) {
	run := newRegisterRun(t, plainManifest, monorepoManifestPath, true)
	run.cmd.SetIn(strings.NewReader("\n"))
	if err := run.register(t); err != nil {
		t.Fatalf("register: %v", err)
	}
	prompt := run.errOut.String()
	for _, want := range []string{
		"Build context:  src/abbott-cgm (the manifest's directory)",
		"Dockerfile:     Dockerfile (src/abbott-cgm/Dockerfile)",
		"Use this? [Y/n/edit]",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if got := run.input(t)["buildContext"]; got != "src/abbott-cgm" {
		t.Errorf("buildContext = %v", got)
	}
}

func TestRegisterNoCancelsWithoutRegistering(t *testing.T) {
	run := newRegisterRun(t, plainManifest, monorepoManifestPath, true)
	run.cmd.SetIn(strings.NewReader("n\n"))
	if err := run.register(t); err != nil {
		t.Fatalf("register: %v", err)
	}
	if run.input(t) != nil {
		t.Fatal("registerApp was sent after the prompt was declined")
	}
	if !strings.Contains(run.errOut.String(), "Aborted; nothing was registered.") {
		t.Errorf("stderr:\n%s", run.errOut.String())
	}
}

func TestRegisterEditSendsTheTypedPaths(t *testing.T) {
	run := newRegisterRun(t, plainManifest, monorepoManifestPath, true)
	run.cmd.SetIn(strings.NewReader("edit\nsrc/abbott-cgm/\ndocker/Dockerfile.prod\n"))
	if err := run.register(t); err != nil {
		t.Fatalf("register: %v", err)
	}
	input := run.input(t)
	if input["buildContext"] != "src/abbott-cgm" || input["dockerfilePath"] != "docker/Dockerfile.prod" {
		t.Errorf("input = %v", input)
	}
	if !strings.Contains(run.errOut.String(), "Dockerfile, relative to the build context [Dockerfile]:") {
		t.Errorf("stderr:\n%s", run.errOut.String())
	}
}

func TestRegisterEditKeepsTheShownValuesOnEnter(t *testing.T) {
	run := newRegisterRun(t, plainManifest, monorepoManifestPath, true)
	run.cmd.SetIn(strings.NewReader("e\n\n\n"))
	if err := run.register(t); err != nil {
		t.Fatalf("register: %v", err)
	}
	input := run.input(t)
	if input["buildContext"] != "src/abbott-cgm" {
		t.Errorf("buildContext = %v", input["buildContext"])
	}
	if _, ok := input["dockerfilePath"]; ok {
		t.Errorf("an unchanged Dockerfile was sent: %v", input["dockerfilePath"])
	}
}

func TestRegisterAsksAgainOnAnUnknownAnswer(t *testing.T) {
	run := newRegisterRun(t, plainManifest, monorepoManifestPath, true)
	run.cmd.SetIn(strings.NewReader("maybe\ny\n"))
	if err := run.register(t); err != nil {
		t.Fatalf("register: %v", err)
	}
	if strings.Count(run.errOut.String(), "Use this? [Y/n/edit]") != 2 {
		t.Errorf("stderr:\n%s", run.errOut.String())
	}
	if run.input(t) == nil {
		t.Fatal("not registered after y")
	}
}

func TestRegisterClosedStdinCancels(t *testing.T) {
	run := newRegisterRun(t, plainManifest, monorepoManifestPath, true)
	if err := run.register(t); err != nil {
		t.Fatalf("register: %v", err)
	}
	if run.input(t) != nil {
		t.Fatal("registered with no answer")
	}
}

func TestRegisterFlagsSkipThePrompt(t *testing.T) {
	for _, tc := range []struct {
		name, flag, value string
		wantContext       string
		wantDockerfile    interface{}
	}{
		{"an explicit root", "build-context", ".", ".", nil},
		{"a Dockerfile only", "dockerfile", "docker/Dockerfile.prod", "src/abbott-cgm", "docker/Dockerfile.prod"},
		{"the earlier flag name", "dockerfile-path", "Dockerfile.web", "src/abbott-cgm", "Dockerfile.web"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Interactive, with nothing to read: a prompt would cancel.
			run := newRegisterRun(t, plainManifest, monorepoManifestPath, true)
			if err := run.cmd.Flags().Set(tc.flag, tc.value); err != nil {
				t.Fatal(err)
			}
			if err := run.register(t); err != nil {
				t.Fatalf("register: %v", err)
			}
			input := run.input(t)
			if input == nil {
				t.Fatal("not registered")
			}
			if input["buildContext"] != tc.wantContext || input["dockerfilePath"] != tc.wantDockerfile {
				t.Errorf("input = %v", input)
			}
			if strings.Contains(run.errOut.String(), "Use this?") {
				t.Errorf("prompted despite --%s", tc.flag)
			}
		})
	}
}

func TestRegisterYesAndNoPromptTakeTheDetection(t *testing.T) {
	for _, name := range []string{"yes", "no-prompt"} {
		t.Run(name, func(t *testing.T) {
			run := newRegisterRun(t, plainManifest, monorepoManifestPath, true)
			if name == "yes" {
				appRegisterYes = true
			} else if err := run.cmd.Root().PersistentFlags().Set("no-prompt", "true"); err != nil {
				t.Fatal(err)
			}
			if err := run.register(t); err != nil {
				t.Fatalf("register: %v", err)
			}
			if got := run.input(t)["buildContext"]; got != "src/abbott-cgm" {
				t.Errorf("buildContext = %v", got)
			}
			if strings.Contains(run.errOut.String(), "Use this?") {
				t.Error("prompted")
			}
		})
	}
}

func TestRegisterKeepsTheRootForAManifestWithItsOwnPaths(t *testing.T) {
	run := newRegisterRun(t, ownBuildPathsManifest, "services/web/astrolift.toml", true)
	run.cmd.SetIn(strings.NewReader("y\n"))
	if err := run.register(t); err != nil {
		t.Fatalf("register: %v", err)
	}
	if !strings.Contains(run.errOut.String(), "the manifest sets build_context or dockerfile_path itself") {
		t.Errorf("prompt:\n%s", run.errOut.String())
	}
	if got := run.input(t)["buildContext"]; got != "." {
		t.Errorf("buildContext = %v, want . so the manifest's own paths apply", got)
	}
}

func TestRegisterRootManifestBuildsFromTheRoot(t *testing.T) {
	run := newRegisterRun(t, plainManifest, "", false)
	if err := run.register(t); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := run.input(t)["buildContext"]; got != "." {
		t.Errorf("buildContext = %v", got)
	}
}

// ---- astro app set-build-context -------------------------------------------

func setBuildContextServer(t *testing.T, app map[string]interface{}, update map[string]interface{}, seen *[]gqlRequest) *api.Client {
	t.Helper()
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		*seen = append(*seen, req)
		if strings.Contains(req.Query, "astroliftApp(") {
			return map[string]interface{}{"astroliftApp": app}
		}
		return map[string]interface{}{"updateApp": update}
	})
	t.Cleanup(srv.Close)
	return api.NewClient(srv.URL, "tok", false)
}

func legacyApp(mode string) map[string]interface{} {
	return map[string]interface{}{
		"id": testAppGUID, "slug": "abbott-cgm", "version": 4,
		"buildContext": ".", "dockerfilePath": "Dockerfile",
		"buildMode": mode, "sourceRepo": "steadymd/monorepo",
	}
}

func updated(context, dockerfile string) map[string]interface{} {
	return map[string]interface{}{
		"ok": true, "errors": []interface{}{},
		"data": map[string]interface{}{
			"id": testAppGUID, "slug": "abbott-cgm", "buildContext": context, "dockerfilePath": dockerfile,
		},
	}
}

func withSetBuildContextDockerfile(t *testing.T, dockerfile string) {
	t.Helper()
	prev := appSetBuildContextDockerfile
	appSetBuildContextDockerfile = dockerfile
	t.Cleanup(func() { appSetBuildContextDockerfile = prev })
}

func updateInput(t *testing.T, seen []gqlRequest) map[string]interface{} {
	t.Helper()
	for _, req := range seen {
		if strings.Contains(req.Query, "updateApp(") {
			input, _ := req.Variables["input"].(map[string]interface{})
			return input
		}
	}
	return nil
}

func TestSetBuildContextSavesAgainstTheReadVersion(t *testing.T) {
	withSetBuildContextDockerfile(t, "")
	var seen []gqlRequest
	client := setBuildContextServer(t, legacyApp("ci_pushed"), updated("src/abbott-cgm", "Dockerfile"), &seen)

	cmd, out := appTestCmd()
	if err := runAppSetBuildContext(cmd, context.Background(), client, "abbott-cgm", "src/abbott-cgm/"); err != nil {
		t.Fatalf("runAppSetBuildContext: %v", err)
	}
	input := updateInput(t, seen)
	if input["id"] != testAppGUID || input["buildContext"] != "src/abbott-cgm" ||
		input["dockerfilePath"] != "Dockerfile" || input["ifMatchVersion"] != float64(4) {
		t.Errorf("updateApp input = %v", input)
	}
	got := out.String()
	for _, want := range []string{
		"abbott-cgm: build context . -> src/abbott-cgm",
		"still builds from the old paths",
		"astro ci setup abbott-cgm --skip-webhook --skip-secrets",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Dockerfile Dockerfile") {
		t.Errorf("reported an unchanged Dockerfile:\n%s", got)
	}
}

func TestSetBuildContextWithADockerfile(t *testing.T) {
	withSetBuildContextDockerfile(t, "docker/Dockerfile.prod")
	var seen []gqlRequest
	client := setBuildContextServer(t, legacyApp("platform_build"), updated(".", "docker/Dockerfile.prod"), &seen)

	cmd, out := appTestCmd()
	if err := runAppSetBuildContext(cmd, context.Background(), client, "abbott-cgm", "."); err != nil {
		t.Fatalf("runAppSetBuildContext: %v", err)
	}
	if got := updateInput(t, seen)["dockerfilePath"]; got != "docker/Dockerfile.prod" {
		t.Errorf("dockerfilePath = %v", got)
	}
	got := out.String()
	for _, want := range []string{"Dockerfile Dockerfile -> docker/Dockerfile.prod", "The next platform build uses the new paths."} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "astro ci setup") {
		t.Errorf("a platform-built app was told to push its CI workflow:\n%s", got)
	}
}

func TestSetBuildContextUnchangedSendsNothing(t *testing.T) {
	withSetBuildContextDockerfile(t, "")
	var seen []gqlRequest
	client := setBuildContextServer(t, legacyApp("ci_pushed"), updated(".", "Dockerfile"), &seen)

	cmd, out := appTestCmd()
	if err := runAppSetBuildContext(cmd, context.Background(), client, "abbott-cgm", "./"); err != nil {
		t.Fatalf("runAppSetBuildContext: %v", err)
	}
	if updateInput(t, seen) != nil {
		t.Fatal("updateApp sent for an unchanged context")
	}
	if !strings.Contains(out.String(), "already builds from . with Dockerfile") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestSetBuildContextStaleVersionSavesNothing(t *testing.T) {
	withSetBuildContextDockerfile(t, "")
	var seen []gqlRequest
	client := setBuildContextServer(t, legacyApp("ci_pushed"), map[string]interface{}{
		"ok": false, "data": nil,
		"errors": []map[string]interface{}{{"code": "VERSION_MISMATCH", "message": ""}},
	}, &seen)

	cmd, _ := appTestCmd()
	err := runAppSetBuildContext(cmd, context.Background(), client, "abbott-cgm", "src/abbott-cgm")
	if err == nil || !strings.Contains(err.Error(), "changed while this command ran") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetBuildContextSurfacesARefusal(t *testing.T) {
	withSetBuildContextDockerfile(t, "")
	var seen []gqlRequest
	client := setBuildContextServer(t, legacyApp("ci_pushed"), map[string]interface{}{
		"ok": false, "data": nil,
		"errors": []map[string]interface{}{{
			"code": "VALIDATION", "field": "buildContext",
			"message": "buildContext must not escape the repository root, got '../outside'",
		}},
	}, &seen)

	cmd, _ := appTestCmd()
	err := runAppSetBuildContext(cmd, context.Background(), client, "abbott-cgm", "../outside")
	if err == nil || !strings.Contains(err.Error(), "must not escape the repository root") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetBuildContextRefusesAnEmptyPath(t *testing.T) {
	cmd, _ := appTestCmd()
	err := runAppSetBuildContext(cmd, context.Background(), nil, "abbott-cgm", " ")
	if err == nil || !strings.Contains(err.Error(), "use . for the repository root") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetBuildContextUnknownApp(t *testing.T) {
	withSetBuildContextDockerfile(t, "")
	var seen []gqlRequest
	client := setBuildContextServer(t, nil, nil, &seen)

	cmd, _ := appTestCmd()
	err := runAppSetBuildContext(cmd, context.Background(), client, "nope", "src")
	if err == nil || !strings.Contains(err.Error(), `app "nope" not found`) {
		t.Fatalf("err = %v", err)
	}
}

func TestSetBuildContextJSON(t *testing.T) {
	withSetBuildContextDockerfile(t, "")
	var seen []gqlRequest
	client := setBuildContextServer(t, legacyApp("none"), updated("src/abbott-cgm", "Dockerfile"), &seen)

	cmd, out := appTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runAppSetBuildContext(cmd, context.Background(), client, "abbott-cgm", "src/abbott-cgm"); err != nil {
		t.Fatalf("runAppSetBuildContext: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"buildContext": "src/abbott-cgm"`) || strings.Contains(got, "astro ci setup") {
		t.Errorf("JSON output:\n%s", got)
	}
}
