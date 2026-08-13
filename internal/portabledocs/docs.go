// Package portabledocs exposes the release-matched Markdown reference embedded
// in the astro binary. The public docs repository is canonical; `make
// vendor-docs` refreshes this package's committed snapshot for offline use.
package portabledocs

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed content/*.md content/llms.txt
var content embed.FS

// Topic describes one user-facing documentation topic.
type Topic struct {
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Filename   string `json:"filename"`
	OnlinePath string `json:"online_path"`
}

var topics = []Topic{
	{Slug: "client", Title: "Choose an Astrolift client", Filename: "client.md", OnlinePath: "/guides/clients/"},
	{Slug: "cli", Title: "astro CLI reference", Filename: "cli.md", OnlinePath: "/reference/cli/"},
	{Slug: "api", Title: "Control API reference", Filename: "api.md", OnlinePath: "/reference/api/"},
	{Slug: "mcp", Title: "MCP reference", Filename: "mcp.md", OnlinePath: "/reference/mcp/"},
	{Slug: "manifest", Title: "astrolift.toml reference", Filename: "manifest.md", OnlinePath: "/reference/astrolift-toml/"},
	{Slug: "agents", Title: "Agent Package and repository reference", Filename: "agents.md", OnlinePath: "/reference/agent-packages/"},
	{Slug: "workflows", Title: "Workflow TOML reference", Filename: "workflows.md", OnlinePath: "/reference/workflow-toml/"},
}

var aliases = map[string]string{
	"agent":          "agents",
	"agent-package":  "agents",
	"agent-packages": "agents",
	"astrolift.toml": "manifest",
	"toml":           "manifest",
	"workflow":       "workflows",
	"workflow-toml":  "workflows",
	"control-api":    "api",
	"command-line":   "cli",
}

// Topics returns a stable copy sorted in the order users normally encounter
// the platform.
func Topics() []Topic {
	return append([]Topic(nil), topics...)
}

// Resolve returns a topic by slug or documented alias.
func Resolve(value string) (Topic, error) {
	slug := strings.ToLower(strings.TrimSpace(value))
	if canonical, ok := aliases[slug]; ok {
		slug = canonical
	}
	for _, topic := range topics {
		if topic.Slug == slug {
			return topic, nil
		}
	}
	valid := make([]string, 0, len(topics))
	for _, topic := range topics {
		valid = append(valid, topic.Slug)
	}
	sort.Strings(valid)
	return Topic{}, fmt.Errorf("unknown docs topic %q (choose: %s)", value, strings.Join(valid, ", "))
}

// Read returns one embedded topic as UTF-8 Markdown.
func Read(value string) (string, Topic, error) {
	topic, err := Resolve(value)
	if err != nil {
		return "", Topic{}, err
	}
	body, err := content.ReadFile("content/" + topic.Filename)
	if err != nil {
		return "", Topic{}, fmt.Errorf("reading embedded docs topic %q: %w", topic.Slug, err)
	}
	return string(body), topic, nil
}

// Files returns every portable source file keyed by its export filename.
func Files() (map[string][]byte, error) {
	files := make(map[string][]byte, len(topics)+1)
	for _, topic := range topics {
		body, err := content.ReadFile("content/" + topic.Filename)
		if err != nil {
			return nil, fmt.Errorf("reading embedded docs topic %q: %w", topic.Slug, err)
		}
		files[topic.Filename] = append([]byte(nil), body...)
	}
	index, err := content.ReadFile("content/llms.txt")
	if err != nil {
		return nil, fmt.Errorf("reading embedded llms.txt: %w", err)
	}
	files["llms.txt"] = append([]byte(nil), index...)
	return files, nil
}
