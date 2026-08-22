package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/portabledocs"
	"github.com/calliopeai/astrolift-cli/internal/skills"
)

// mcpRoute is the gateway path from the platform's MCP contract
// (astrolift_agents/mcp_contract.py: MCP_ROUTE). Hardcoded rather than
// discovered because it is a contract constant, not configuration -- but
// kept here as one named thing so a contract change is a one-line edit.
const mcpRoute = "api/mcp/v1/"

// The config filenames each agent reads. Claude Code takes a project-level
// .mcp.json; Codex reads .codex/mcp.json. Both accept an http server entry
// with headers, which is what the MCP reference already documents by hand.
const (
	claudeMCPFile = ".mcp.json"
	codexMCPFile  = ".codex/mcp.json"
)

func mcpEndpoint(apiURL string) string {
	return strings.TrimRight(apiURL, "/") + "/" + mcpRoute
}

// mcpDocument is the smallest config both clients accept.
//
// The Authorization value is deliberately the literal string
// "Bearer ${ASTROLIFT_TOKEN}" rather than a resolved token: writing a real
// bearer into a file that tends to land in a repo is how credentials leak,
// and both clients expand the environment variable at launch. It also means
// the same file works for every agent on the machine.
func mcpDocument(apiURL string) ([]byte, error) {
	doc := map[string]any{
		"mcpServers": map[string]any{
			mcpServerName: map[string]any{
				"type": "http",
				"url":  mcpEndpoint(apiURL),
				"headers": map[string]string{
					"Authorization": "Bearer ${ASTROLIFT_TOKEN}",
				},
			},
		},
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding MCP config: %w", err)
	}
	return append(body, '\n'), nil
}

func writeMCPConfig(dir, target, apiURL string, dryRun, force bool) ([]writtenFn, error) {
	body, err := mcpDocument(apiURL)
	if err != nil {
		return nil, err
	}

	var files []string
	switch target {
	case "claude":
		files = []string{claudeMCPFile}
	case "codex":
		files = []string{codexMCPFile}
	default:
		files = []string{claudeMCPFile, codexMCPFile}
	}

	var out []writtenFn
	for _, rel := range files {
		written, err := writeFileReport(filepath.Join(dir, rel), body, dryRun, force)
		if err != nil {
			return nil, err
		}
		out = append(out, written)
	}
	return out, nil
}

// skillsDirFor returns where each agent looks for skills.
func skillsDirFor(dir, target string) []string {
	switch target {
	case "claude":
		return []string{filepath.Join(dir, ".claude", "skills")}
	case "codex":
		return []string{filepath.Join(dir, ".codex", "skills")}
	default:
		return []string{
			filepath.Join(dir, ".claude", "skills"),
			filepath.Join(dir, ".codex", "skills"),
		}
	}
}

func installSkills(dir, target string, dryRun, force bool) ([]writtenFn, error) {
	catalogue, err := skills.Catalogue()
	if err != nil {
		return nil, err
	}

	var out []writtenFn
	for _, base := range skillsDirFor(dir, target) {
		for _, skill := range catalogue {
			files, err := skills.Files(skill)
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				dest := filepath.Join(base, skill.Name, f.RelPath)
				written, err := writeFileReport(dest, f.Content, dryRun, force)
				if err != nil {
					return nil, err
				}
				out = append(out, written)
			}
		}
	}
	return out, nil
}

// installAgentDocs drops the embedded guides where an agent will read them
// as context, plus an index naming each topic.
//
// Separate from `astro docs export`, which builds a human documentation tree
// including man pages: an agent wants the Markdown and a map of it, not
// roff.
func installAgentDocs(dir string, dryRun, force bool) ([]writtenFn, error) {
	base := filepath.Join(dir, ".astrolift", "docs")

	var out []writtenFn
	var index strings.Builder
	index.WriteString("# Astrolift reference, bundled with the astro CLI\n\n")
	index.WriteString("Offline copies of the platform guides, matched to this CLI build.\n\n")

	for _, topic := range portabledocs.Topics() {
		body, _, err := portabledocs.Read(topic.Slug)
		if err != nil {
			return nil, fmt.Errorf("reading embedded topic %q: %w", topic.Slug, err)
		}
		name := topic.Slug + ".md"
		written, err := writeFileReport(filepath.Join(base, name), []byte(body), dryRun, force)
		if err != nil {
			return nil, err
		}
		out = append(out, written)
		fmt.Fprintf(&index, "- [%s](%s) — %s\n", topic.Slug, name, topic.Title)
	}

	written, err := writeFileReport(filepath.Join(base, "index.md"), []byte(index.String()), dryRun, force)
	if err != nil {
		return nil, err
	}
	return append(out, written), nil
}
