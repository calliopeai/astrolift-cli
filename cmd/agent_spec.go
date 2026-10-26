package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

var markdownLink = regexp.MustCompile(`\[[^\]]*\]\(([^ )]+)(?:\s+[^)]*)?\)`)
var inlineCode = regexp.MustCompile("`([^`\\n]+)`")
var branchUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func specEnvelope(cmd *cobra.Command, ctx context.Context, client *api.Client) (map[string]interface{}, error) {
	if agentDispatchInput != "" {
		return nil, fmt.Errorf("--spec compiles the payload; do not combine it with --input")
	}
	if len(agentDispatchBrains) != 1 {
		return nil, fmt.Errorf("--spec requires exactly one --brain for source identity")
	}
	id := strings.TrimPrefix(stringFlag(cmd, "spec"), "spec:")
	source, err := openPlanSource(cmd, ctx, client, agentDispatchBrains[0])
	if err != nil {
		return nil, err
	}
	projection, err := readPlan(source)
	if err != nil {
		return nil, err
	}
	task, err := projection.Find(id)
	if err != nil {
		return nil, err
	}
	if task.State != "ready" {
		return nil, fmt.Errorf("spec %s is %s; blockers: %s; %s", id, task.State, strings.Join(task.BlockedBy, ", "), strings.Join(task.Problems, "; "))
	}
	story, err := source.read(task.Path)
	if err != nil {
		return nil, err
	}
	body := string(story)
	if !strings.Contains(body, "## Acceptance") || !strings.Contains(body, "## Description") {
		return nil, fmt.Errorf("story must include Description and Acceptance sections")
	}
	acceptance := []string{}
	inAcceptance := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			inAcceptance = strings.HasPrefix(line, "## Acceptance")
			continue
		}
		trimmed := strings.TrimSpace(line)
		if inAcceptance && (strings.HasPrefix(trimmed, "- [ ] ") || strings.HasPrefix(trimmed, "- [x] ")) {
			acceptance = append(acceptance, trimmed[6:])
		}
	}
	if len(acceptance) == 0 {
		return nil, fmt.Errorf("story has no acceptance checklist")
	}
	pack, err := contextPack(source, id)
	if err != nil {
		return nil, err
	}
	docs, wireframes := []string{}, []string{}
	seen := map[string]bool{}
	for _, match := range markdownLink.FindAllStringSubmatch(body, -1) {
		target := strings.Split(match[1], "#")[0]
		if target == "" || strings.Contains(target, "://") {
			continue
		}
		// The story standard uses repository-relative references. Explicit relative
		// paths retain their Markdown meaning at the story's directory.
		if strings.HasPrefix(target, "../") || strings.HasPrefix(target, "./") {
			target = path.Join(path.Dir(task.Path), target)
		}
		if err := safePlanPath(target); err != nil {
			return nil, err
		}
		if seen[target] {
			continue
		}
		seen[target] = true
		if _, err := source.read(target); err != nil {
			return nil, fmt.Errorf("story reference %s: %w", target, err)
		}
		if strings.Contains(target, "wireframe") || strings.HasSuffix(target, ".html") {
			wireframes = append(wireframes, target)
		} else {
			docs = append(docs, target)
		}
	}
	tests := []string{}
	for _, item := range acceptance {
		for _, match := range inlineCode.FindAllStringSubmatch(item, -1) {
			value := match[1]
			for _, prefix := range []string{"make ", "cargo ", "go test", "pytest", "python ", "npm ", "pnpm ", "yarn "} {
				if strings.HasPrefix(value, prefix) {
					tests = append(tests, value)
					break
				}
			}
		}
	}
	if len(tests) == 0 {
		return nil, fmt.Errorf("acceptance needs an explicit test command before dispatch")
	}
	branch := stringFlag(cmd, "branch")
	if branch == "" {
		branch = "feature/spec-" + strings.Trim(branchUnsafe.ReplaceAllString(id, "-"), "-")
	}
	if strings.Contains(branch, "..") || strings.ContainsAny(branch, " ~^:?*[\\\r\n") || strings.HasSuffix(branch, "/") {
		return nil, fmt.Errorf("invalid target branch")
	}
	attempt, _ := cmd.Flags().GetInt("attempt")
	if attempt < 1 {
		return nil, fmt.Errorf("--attempt must be positive")
	}
	issue, _ := cmd.Flags().GetInt("issue")
	if issue < 0 {
		return nil, fmt.Errorf("--issue cannot be negative")
	}
	envelope := map[string]interface{}{
		"envelope_version": 1, "task_id": task.ID, "attempt": attempt,
		"brain":     map[string]interface{}{"repo": source.repo, "commit": source.revision, "mcp": agentDispatchBrains[0]},
		"story":     map[string]interface{}{"path": task.Path, "title": task.Title, "estimate": task.Estimate, "labels": task.Labels, "acceptance": acceptance, "body": body},
		"task_pack": map[string]interface{}{"generated_by": "astro spec context recipe", "inline": pack},
		"docs":      docs, "wireframes": wireframes,
		"repo":        map[string]interface{}{"name": task.Repo, "base": "main", "branch": branch, "issue": issue},
		"conventions": []string{"bootstrap.md", "AGENTS.md"},
		"commands":    map[string]interface{}{"test": tests},
		"callback":    map[string]interface{}{"correlation_id": fmt.Sprintf("%s#%d", task.ID, attempt)},
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 512<<10 {
		return nil, fmt.Errorf("compiled spec payload exceeds 512 KiB; reduce its context recipe")
	}
	return envelope, nil
}

func init() {
	agentDispatchCmd.Flags().String("spec", "", "Compile a ready story from the selected brain before dispatch")
	sourceFlags(agentDispatchCmd)
	agentDispatchCmd.Flags().String("branch", "", "Target branch for a spec task (default: feature/spec-<id>)")
	agentDispatchCmd.Flags().Int("issue", 0, "Existing target-repository issue number for a spec task")
	agentDispatchCmd.Flags().Int("attempt", 1, "Spec task attempt number")
	agentDispatchCmd.Flags().Bool("dry-run", false, "Print the compiled spec envelope without dispatching")
}
