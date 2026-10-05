package portabledocs

import (
	"strings"
	"testing"
)

func TestEveryTopicIsEmbedded(t *testing.T) {
	for _, topic := range Topics() {
		body, resolved, err := Read(topic.Slug)
		if err != nil {
			t.Fatalf("Read(%q): %v", topic.Slug, err)
		}
		if resolved != topic {
			t.Fatalf("Read(%q) resolved %#v, want %#v", topic.Slug, resolved, topic)
		}
		if !strings.HasPrefix(body, "# ") {
			t.Errorf("topic %q is not a heading-first Markdown document", topic.Slug)
		}
	}
}

func TestAliasesResolve(t *testing.T) {
	for alias, want := range map[string]string{
		"alert-mail":           "install-alert-mail",
		"install-smtp":         "install-alert-mail",
		"dns":                  "domains",
		"domains-dns":          "domains",
		"mail":                 "email-delivery",
		"email":                "email-delivery",
		"astrolift-toml":       "manifest",
		"astrolift.toml":       "manifest",
		"toml":                 "manifest",
		"agent-package":        "agents",
		"agent-packages":       "agents",
		"workflow":             "workflows",
		"workflow-toml":        "workflows",
		"loops":                "bounded-workflows",
		"collections":          "bounded-workflows",
		"apps":                 "app-setup",
		"previews":             "preview-targets",
		"metrics":              "workload-signals",
		"services":             "shared-services",
		"resources":            "shared-services",
		"callback":             "callbacks",
		"completion-callbacks": "callbacks",
	} {
		topic, err := Resolve(alias)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", alias, err)
		}
		if topic.Slug != want {
			t.Errorf("Resolve(%q) = %q, want %q", alias, topic.Slug, want)
		}
	}
}

func TestFilesIncludesAIIndex(t *testing.T) {
	files, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["llms.txt"]), "Astrolift documentation") {
		t.Fatal("llms.txt is missing or malformed")
	}
}

func TestPortableGuideLinks(t *testing.T) {
	topic, err := Resolve("agent-setup")
	if err != nil {
		t.Fatal(err)
	}
	body := "[Package](../reference/agent-packages.md#briefs-and-skills) [Callbacks](agent-completion-callbacks.md) [Dashboard](../working-with-apps.md) [Same](#observe) [Reference](../reference/index.md) [Home](../index.md)\n```markdown\n[Literal](../reference/api.md)\n```\n"
	got := PortableMarkdown(topic, body)
	for _, want := range []string{"[Package](agents.md#briefs-and-skills)", "[Callbacks](callbacks.md)", "[Dashboard](https://astrolift.dev/working-with-apps/)", "[Same](#observe)", "[Reference](https://astrolift.dev/reference/)", "[Home](https://astrolift.dev/)", "[Literal](../reference/api.md)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing portable link %q in %s", want, got)
		}
	}
}
