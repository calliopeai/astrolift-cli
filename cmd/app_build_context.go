// Package cmd — build-context detection for `astro app register` and
// `astro app set-build-context` (#159).
//
// A manifest in a sub-directory of a monorepo builds from that directory:
// src/abbott-cgm/astrolift.toml builds src/abbott-cgm with its Dockerfile.
// A manifest that names its own build_context or dockerfile_path keeps the
// repository root as the app's context, so those paths apply as written.
// This is the platform's rule (astrolift-app astrolift_manifest/build_context.py);
// register shows the result, asks, and sends the confirmed context explicitly,
// so an older platform stores the same thing.
package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	defaultBuildContext = "."
	defaultDockerfile   = "Dockerfile"
)

// buildPaths is what a registration builds from. FromManifest means the
// manifest names its own build paths, which apply instead.
type buildPaths struct {
	Context      string
	Dockerfile   string
	FromManifest bool
}

// promptInteractive reports whether register may ask: stdin and stderr are
// both terminals. A CI stdin of /dev/null is a character device but not a
// terminal, so it never prompts. A variable so tests can stand in a terminal.
var promptInteractive = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

// manifestDirectory is the manifest's repo-relative directory, "." at the
// root and for a path that is absolute or climbs above the repository.
func manifestDirectory(manifestPath string) string {
	p := filepath.ToSlash(strings.TrimSpace(manifestPath))
	if strings.HasPrefix(p, "/") {
		return defaultBuildContext
	}
	dir := path.Dir(p)
	if dir == ".." || strings.HasPrefix(dir, "../") {
		return defaultBuildContext
	}
	return dir
}

// manifestSetsBuildPaths reports whether a container, job or task in the
// manifest names its own build_context or Dockerfile, read the way the
// platform's parser reads them: containers honour dockerfile_path, jobs and
// tasks also its dockerfile alias.
func manifestSetsBuildPaths(raw string) (bool, error) {
	var doc map[string]interface{}
	if _, err := toml.Decode(raw, &doc); err != nil {
		return false, err
	}
	for _, workload := range tomlTables(doc["workloads"]) {
		for _, container := range tomlTables(workload["containers"]) {
			if setsBuildPaths(container, false) {
				return true, nil
			}
		}
	}
	for _, key := range []string{"jobs", "tasks"} {
		for _, entry := range tomlTables(doc[key]) {
			if setsBuildPaths(entry, true) {
				return true, nil
			}
		}
	}
	return false, nil
}

// tomlTables returns the tables of an array of tables, inline or not.
func tomlTables(value interface{}) []map[string]interface{} {
	switch v := value.(type) {
	case []map[string]interface{}:
		return v
	case []interface{}:
		var tables []map[string]interface{}
		for _, item := range v {
			if table, ok := item.(map[string]interface{}); ok {
				tables = append(tables, table)
			}
		}
		return tables
	}
	return nil
}

func setsBuildPaths(entry map[string]interface{}, dockerfileAlias bool) bool {
	value := func(key string) (string, bool) {
		v, ok := entry[key]
		if !ok {
			return "", false
		}
		return fmt.Sprint(v), true
	}
	if context, ok := value("build_context"); ok && context != "" && context != defaultBuildContext {
		return true
	}
	dockerfile, ok := value("dockerfile_path")
	if !ok && dockerfileAlias {
		dockerfile, ok = value("dockerfile")
	}
	return ok && dockerfile != "" && dockerfile != defaultDockerfile
}

// detectBuildPaths is what a new registration of the manifest at
// manifestPath (repo-relative) with contents raw builds from.
func detectBuildPaths(manifestPath, raw string) (buildPaths, error) {
	fromManifest, err := manifestSetsBuildPaths(raw)
	if err != nil {
		return buildPaths{}, err
	}
	if fromManifest {
		return buildPaths{Context: defaultBuildContext, Dockerfile: defaultDockerfile, FromManifest: true}, nil
	}
	return buildPaths{Context: manifestDirectory(manifestPath), Dockerfile: defaultDockerfile}, nil
}

// cleanBuildPath normalizes a path the operator typed.
func cleanBuildPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return path.Clean(filepath.ToSlash(p))
}

// describeBuildPaths prints the build context and Dockerfile as the prompt
// and the register summary show them.
func describeBuildPaths(w io.Writer, b buildPaths) {
	why := "the manifest's directory"
	switch {
	case b.FromManifest:
		why = "the repository root; the manifest sets build_context or dockerfile_path itself, and those apply"
	case b.Context == defaultBuildContext:
		why = "the repository root, where the manifest is"
	}
	fmt.Fprintf(w, "Build context:  %s (%s)\n", b.Context, why)
	if b.Context == defaultBuildContext || b.FromManifest {
		fmt.Fprintf(w, "Dockerfile:     %s\n", b.Dockerfile)
		return
	}
	fmt.Fprintf(w, "Dockerfile:     %s (%s)\n", b.Dockerfile, path.Join(b.Context, b.Dockerfile))
}

// errRegisterCancelled is a declined prompt: nothing was registered.
var errRegisterCancelled = errors.New("registration cancelled")

// confirmBuildPaths asks "Use this? [Y/n/edit]" on stderr and returns the
// build paths to register with. n cancels; edit asks for each path, with
// Enter keeping the shown value.
func confirmBuildPaths(cmd *cobra.Command, detected buildPaths) (buildPaths, error) {
	out := cmd.ErrOrStderr()
	in := bufio.NewReader(cmd.InOrStdin())
	describeBuildPaths(out, detected)
	for {
		fmt.Fprint(out, "Use this? [Y/n/edit] ")
		answer, err := readAnswer(in)
		if err != nil {
			return buildPaths{}, err
		}
		switch strings.ToLower(answer) {
		case "", "y", "yes":
			return detected, nil
		case "n", "no":
			return buildPaths{}, errRegisterCancelled
		case "e", "edit":
			return editBuildPaths(out, in, detected)
		}
		fmt.Fprintln(out, "Answer y, n or edit.")
	}
}

func editBuildPaths(out io.Writer, in *bufio.Reader, detected buildPaths) (buildPaths, error) {
	fmt.Fprintf(out, "Build context, relative to the repository root [%s]: ", detected.Context)
	context, err := readAnswer(in)
	if err != nil {
		return buildPaths{}, err
	}
	fmt.Fprintf(out, "Dockerfile, relative to the build context [%s]: ", detected.Dockerfile)
	dockerfile, err := readAnswer(in)
	if err != nil {
		return buildPaths{}, err
	}
	chosen := buildPaths{Context: detected.Context, Dockerfile: detected.Dockerfile}
	if c := cleanBuildPath(context); c != "" {
		chosen.Context = c
	}
	if d := cleanBuildPath(dockerfile); d != "" {
		chosen.Dockerfile = d
	}
	return chosen, nil
}

// readAnswer reads one line; a closed stdin with nothing typed cancels.
func readAnswer(in *bufio.Reader) (string, error) {
	line, err := in.ReadString('\n')
	switch {
	case err == nil, errors.Is(err, io.EOF) && line != "":
		return strings.TrimSpace(line), nil
	case errors.Is(err, io.EOF):
		return "", errRegisterCancelled
	default:
		return "", err
	}
}

// ---- astro app set-build-context ------------------------------------------

var appSetBuildContextDockerfile string

type appBuildSettings struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Version        int    `json:"version"`
	BuildContext   string `json:"buildContext"`
	DockerfilePath string `json:"dockerfilePath"`
	BuildMode      string `json:"buildMode"`
	SourceRepo     string `json:"sourceRepo"`
}

const appBuildSettingsQuery = `query($slug: String!) {
  astroliftApp(slug: $slug) { id slug version buildContext dockerfilePath buildMode sourceRepo }
}`

const setBuildContextMutation = `mutation SetBuildContext($input: UpdateAppInput!) {
  updateApp(input: $input) {
    ok
    errors { code message field }
    data { id slug buildContext dockerfilePath }
  }
}`

var appSetBuildContextCmd = &cobra.Command{
	Use:   "set-build-context <app-slug> <path>",
	Short: "Change the directory an app's image is built from",
	Long: `Sets an app's build context, relative to the repository root, and with
--dockerfile its Dockerfile, relative to the build context.

An app registered before the CLI detected build contexts builds from the
repository root. A monorepo app whose manifest is in a sub-directory
usually wants that directory instead.

The managed CI workflow in the repository keeps building from the old
paths until it is pushed again; the command prints how. A platform build
uses the new paths from its next run.

Examples:
  astro app set-build-context abbott-cgm src/abbott-cgm
  astro app set-build-context web . --dockerfile docker/Dockerfile.prod`,
	Args: cobra.ExactArgs(2),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runAppSetBuildContext(cmd, ctx, client, args[0], args[1])
	}),
}

func runAppSetBuildContext(cmd *cobra.Command, ctx context.Context, client *api.Client, slug, contextArg string) error {
	buildContext := cleanBuildPath(contextArg)
	if buildContext == "" {
		return fmt.Errorf("give a build context; use . for the repository root")
	}
	dockerfile := cleanBuildPath(appSetBuildContextDockerfile)

	var show struct {
		App *appBuildSettings `json:"astroliftApp"`
	}
	if err := client.GraphQL(ctx, appBuildSettingsQuery, map[string]interface{}{"slug": slug}, &show); err != nil {
		return fmt.Errorf("look up app %q: %w", slug, err)
	}
	was := show.App
	if was == nil || was.ID == "" {
		return fmt.Errorf("app %q not found in the current org", slug)
	}
	if dockerfile == "" {
		dockerfile = was.DockerfilePath
	}
	out := cmd.OutOrStdout()
	if buildContext == was.BuildContext && dockerfile == was.DockerfilePath {
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, was)
		}
		fmt.Fprintf(out, "%s already builds from %s with %s; nothing changed.\n", slug, buildContext, dockerfile)
		return nil
	}

	input := map[string]interface{}{
		"id":             was.ID,
		"buildContext":   buildContext,
		"dockerfilePath": dockerfile,
		// Refused if the app changed since it was read above.
		"ifMatchVersion": was.Version,
	}
	var resp struct {
		Result struct {
			Ok     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *struct {
				ID             string `json:"id"`
				Slug           string `json:"slug"`
				BuildContext   string `json:"buildContext"`
				DockerfilePath string `json:"dockerfilePath"`
			} `json:"data"`
		} `json:"updateApp"`
	}
	if err := client.GraphQL(ctx, setBuildContextMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("updateApp: %w", err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		for _, e := range resp.Result.Errors {
			if e.Code == "VERSION_MISMATCH" {
				return fmt.Errorf("%s changed while this command ran; nothing was saved, run it again", slug)
			}
		}
		return fmt.Errorf("updateApp failed: %s", firstDeployError(resp.Result.Errors))
	}
	now := resp.Result.Data
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, now)
	}
	fmt.Fprintf(out, "%s: build context %s -> %s\n", slug, was.BuildContext, now.BuildContext)
	if now.DockerfilePath != was.DockerfilePath {
		fmt.Fprintf(out, "%s: Dockerfile %s -> %s\n", slug, was.DockerfilePath, now.DockerfilePath)
	}
	printBuildPathsNextStep(out, slug, was.BuildMode, was.SourceRepo != "")
	return nil
}

// printBuildPathsNextStep says what still uses the previous build paths.
func printBuildPathsNextStep(w io.Writer, slug, buildMode string, hasRepo bool) {
	switch {
	case buildMode == "ci_pushed" && hasRepo:
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "The CI workflow in the repository still builds from the old paths. Push it again")
		fmt.Fprintln(w, "(only the workflow; the CI secrets and deploy token stay as they are):")
		fmt.Fprintf(w, "  astro ci setup %s --skip-webhook --skip-secrets\n", slug)
	case buildMode == "platform_build":
		fmt.Fprintln(w, "The next platform build uses the new paths.")
	case buildMode == "none":
		fmt.Fprintln(w, "This app deploys a pre-built image; nothing is built from these paths.")
	}
}

func init() {
	appSetBuildContextCmd.Flags().StringVar(&appSetBuildContextDockerfile, "dockerfile", "", "Dockerfile path, relative to the build context (default: keep the current one)")
	appCmd.AddCommand(appSetBuildContextCmd)
}
