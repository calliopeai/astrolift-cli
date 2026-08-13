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
		"astrolift.toml": "manifest",
		"toml":           "manifest",
		"agent-package":  "agents",
		"workflow":       "workflows",
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
