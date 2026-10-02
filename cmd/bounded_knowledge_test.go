package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/portabledocs"
	"github.com/calliopeai/astrolift-cli/internal/skills"
)

func TestBoundedWorkflowKnowledgeIsSearchableExportedAndInstalledOffline(t *testing.T) {
	t.Setenv("ASTROLIFT_TOKEN", "DO_NOT_COPY_CREDENTIALS")
	guide, topic, err := portabledocs.Read("loops")
	if err != nil || topic.Slug != "bounded-workflows" {
		t.Fatalf("guide unavailable: %v", err)
	}
	search, err := searchPortableDocs(`"serial collections"`, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, match := range search.Matches {
		found = found || match.Topic.Slug == topic.Slug
	}
	if !found {
		t.Fatal("bounded guide not discoverable through offline search")
	}
	dir := t.TempDir()
	if _, err := exportPortableDocs(filepath.Join(dir, "export"), false); err != nil {
		t.Fatal(err)
	}
	exported, err := os.ReadFile(filepath.Join(dir, "export", "guides", topic.Filename))
	if err != nil || string(exported) != portabledocs.PortableMarkdown(topic, guide) {
		t.Fatalf("export omitted or changed bounded guide: %v", err)
	}
	if _, err := installAgentDocs(dir, false, false); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(filepath.Join(dir, ".astrolift", "docs", topic.Filename))
	if err != nil || string(installed) != portabledocs.PortableMarkdown(topic, guide) {
		t.Fatalf("onboarding guide differs from embedded source: %v", err)
	}
	if _, err := installSkills(dir, "codex", false, false); err != nil {
		t.Fatal(err)
	}
	skill, ok := skills.Lookup("astrolift-workflows")
	if !ok || skill.Version != "0.1.1" {
		t.Fatal("versioned workflow skill missing from embedded catalogue")
	}
	files, err := skills.Files(skill)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		body, err := os.ReadFile(filepath.Join(dir, ".codex", "skills", skill.Name, file.RelPath))
		if err != nil || string(body) != string(file.Content) || strings.Contains(string(body), "DO_NOT_COPY_CREDENTIALS") {
			t.Fatalf("installed workflow skill differs or contains credentials: %v", err)
		}
	}
}
