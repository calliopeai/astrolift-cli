package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/plan"
	"github.com/spf13/cobra"
)

const planSourceQuery = `query PlanSource($slug: String!) {
 astroliftBrain(slug: $slug) { slug }
 astroliftApp(slug: $slug) { sourceRepo sourceUrl }
}`
const planFileQuery = `query PlanFile($connection: String!, $repo: String!, $path: String!, $ref: String!) {
 astroliftSourceFile(connectionId: $connection, repoFullName: $repo, path: $path, ref: $ref) {
  repoFullName path ref content errorCode
 }
}`

type planSource struct {
	revision, repo string
	read           func(string) ([]byte, error)
}

func sourceFlags(cmd *cobra.Command) {
	cmd.Flags().String("source", ".", "Local brain Git checkout; reads committed files only")
	cmd.Flags().String("revision", "", "Pinned brain commit (local default: HEAD; required with --connection)")
	cmd.Flags().String("connection", "", "Use an authorized Astrolift source connection instead of local Git")
}

func sourceHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "git@") {
		if at := strings.Index(raw, ":"); at >= 0 {
			return strings.ToLower(raw[4:at])
		}
	}
	if u, err := url.Parse(raw); err == nil {
		return strings.ToLower(u.Hostname())
	}
	return ""
}

func repoName(raw string) string {
	raw = strings.TrimSuffix(strings.TrimSpace(raw), ".git")
	if strings.HasPrefix(raw, "git@") {
		if i := strings.Index(raw, ":"); i >= 0 {
			return strings.Trim(raw[i+1:], "/")
		}
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return strings.Trim(u.Path, "/")
	}
	return strings.Trim(raw, "/")
}

var commitPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

func openPlanSource(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) (*planSource, error) {
	var meta struct {
		Brain *struct {
			Slug string `json:"slug"`
		} `json:"astroliftBrain"`
		App *struct {
			Repo string `json:"sourceRepo"`
			URL  string `json:"sourceUrl"`
		} `json:"astroliftApp"`
	}
	if err := client.GraphQL(ctx, planSourceQuery, map[string]interface{}{"slug": slug}, &meta); err != nil {
		return nil, fmt.Errorf("reading brain source: %w", err)
	}
	if meta.Brain == nil || meta.App == nil || meta.Brain.Slug != slug {
		return nil, fmt.Errorf("brain %q not found or not readable", slug)
	}
	repo := repoName(meta.App.Repo)
	if repo == "" {
		repo = repoName(meta.App.URL)
	}
	if len(strings.Split(repo, "/")) != 2 {
		return nil, fmt.Errorf("brain has no supported owner/repository source")
	}
	revision, connection := stringFlag(cmd, "revision"), stringFlag(cmd, "connection")
	source := &planSource{repo: repo}
	if connection != "" {
		if !commitPattern.MatchString(revision) {
			return nil, fmt.Errorf("--connection requires --revision with a full immutable commit SHA")
		}
		source.revision = strings.ToLower(revision)
		source.read = func(file string) ([]byte, error) {
			if err := safePlanPath(file); err != nil {
				return nil, err
			}
			var result struct {
				File struct {
					Repo    string  `json:"repoFullName"`
					Path    string  `json:"path"`
					Ref     string  `json:"ref"`
					Content *string `json:"content"`
					Error   *string `json:"errorCode"`
				} `json:"astroliftSourceFile"`
			}
			if err := client.GraphQL(ctx, planFileQuery, map[string]interface{}{"connection": connection, "repo": repo, "path": file, "ref": source.revision}, &result); err != nil {
				return nil, err
			}
			f := result.File
			if f.Error != nil && *f.Error != "" {
				return nil, fmt.Errorf("reading %s: %s", file, *f.Error)
			}
			if f.Content == nil {
				return nil, fmt.Errorf("%s is absent at the selected revision", file)
			}
			if f.Repo != repo || f.Path != file || f.Ref != source.revision {
				return nil, fmt.Errorf("source response does not match the requested file and revision")
			}
			if len(*f.Content) > 8<<20 {
				return nil, fmt.Errorf("source file exceeds 8 MiB: %s", file)
			}
			return []byte(*f.Content), nil
		}
	} else {
		dir, err := filepath.Abs(stringFlag(cmd, "source"))
		if err != nil {
			return nil, err
		}
		gitRead := func(args ...string) ([]byte, error) {
			argv := append([]string{"-C", dir}, args...)
			out, err := exec.CommandContext(ctx, "git", argv...).Output()
			if err != nil {
				return nil, fmt.Errorf("cannot read brain Git snapshot (%s)", args[0])
			}
			if len(out) > 8<<20 {
				return nil, fmt.Errorf("brain source exceeds 8 MiB")
			}
			return out, nil
		}
		origin, err := gitRead("remote", "get-url", "origin")
		if err != nil {
			return nil, err
		}
		expectedHost := sourceHost(meta.App.URL)
		if expectedHost == "" {
			expectedHost = sourceHost(meta.App.Repo)
		}
		if repoName(string(origin)) != repo || (expectedHost != "" && sourceHost(string(origin)) != expectedHost) {
			return nil, fmt.Errorf("--source repository does not match registered brain source %s", repo)
		}
		if revision == "" {
			revision = "HEAD"
		}
		if strings.HasPrefix(revision, "-") {
			return nil, fmt.Errorf("invalid revision")
		}
		commit, err := gitRead("rev-parse", "--verify", revision+"^{commit}")
		if err != nil {
			return nil, err
		}
		source.revision = strings.TrimSpace(string(commit))
		if !commitPattern.MatchString(source.revision) {
			return nil, fmt.Errorf("git did not resolve an immutable commit")
		}
		source.read = func(file string) ([]byte, error) {
			if err := safePlanPath(file); err != nil {
				return nil, err
			}
			return gitRead("show", source.revision+":"+file)
		}
	}
	return source, nil
}

func safePlanPath(file string) error {
	if file == "" || path.IsAbs(file) || path.Clean(file) != file || file == ".." || strings.HasPrefix(file, "../") || strings.ContainsAny(file, "\\\x00\r\n") {
		return fmt.Errorf("invalid brain source path %q", file)
	}
	return nil
}

func readPlan(source *planSource) (*plan.Projection, error) {
	content, err := source.read("specs/manifest.json")
	if err != nil {
		return nil, err
	}
	return plan.Decode(content, source.revision, strings.Split(source.repo, "/")[0])
}

func newPlanCommand() *cobra.Command {
	group := &cobra.Command{Use: "plan", Short: "Inspect a brain's versioned task DAG"}
	for _, show := range []bool{false, true} {
		use, short := "ls", "List story states and dependencies"
		if show {
			use, short = "show <spec-id>", "Show one story and its dependency blockers"
		}
		command := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs}
		if show {
			command.Args = cobra.ExactArgs(1)
		}
		command.Flags().String("brain", "", "Registered brain slug")
		sourceFlags(command)
		isShow := show
		command.RunE = func(cmd *cobra.Command, args []string) error {
			slug := stringFlag(cmd, "brain")
			if slug == "" {
				return fmt.Errorf("--brain is required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			client, _, err := loadScopedAgentClient(cmd)
			if err != nil {
				return err
			}
			source, err := openPlanSource(cmd, ctx, client, slug)
			if err != nil {
				return err
			}
			projection, err := readPlan(source)
			if err != nil {
				return err
			}
			if isShow {
				task, err := projection.Find(args[0])
				if err != nil {
					return err
				}
				if boolFlag(cmd, "json") {
					return renderJSON(cmd, map[string]interface{}{"brain_commit": source.revision, "task": task})
				}
				projection.Tasks = []plan.Task{task}
			}
			if boolFlag(cmd, "json") {
				return renderJSON(cmd, projection)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Brain source: %s @ %s (declared plan state)\n", source.repo, source.revision)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "STATE\tSPEC\tREPOSITORY\tDEPENDENCIES / BLOCKERS")
			for _, task := range projection.Tasks {
				detail := strings.Join(task.Dependencies, ", ")
				if len(task.BlockedBy) > 0 {
					detail += "; blocked by " + strings.Join(task.BlockedBy, ", ")
				}
				if len(task.Problems) > 0 {
					detail += "; " + strings.Join(task.Problems, "; ")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", task.State, task.ID, task.Repo, detail)
			}
			return w.Flush()
		}
		group.AddCommand(command)
	}
	return group
}

func init() { rootCmd.AddCommand(newPlanCommand()) }
