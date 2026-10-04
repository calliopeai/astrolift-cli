package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/portabledocs"
)

func TestReviewedAdmissionKnowledgeTravelsThroughOfflineSetup(t *testing.T) {
	t.Setenv("ASTROLIFT_TOKEN", "NOT_PUBLIC_SETUP_CONTEXT")
	// Search, human export and agent onboarding must use the same embedded
	// release contract; none may substitute network-fetched or credential data.
	search, err := searchPortableDocs(`"Browser SSO setup"`, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, match := range search.Matches {
		found = found || match.Topic.Slug == "reviewed-starts"
	}
	if !found {
		t.Fatal("browser admission setup cannot be found offline")
	}
	search, err = searchPortableDocs(`"direct membership"`, 50)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, match := range search.Matches {
		found = found || match.Topic.Slug == "organization"
	}
	if !found {
		t.Fatal("team membership setup cannot be found offline")
	}
	dir := t.TempDir()
	export := filepath.Join(dir, "export")
	if _, err := exportPortableDocs(export, false); err != nil {
		t.Fatal(err)
	}
	if _, err := installAgentDocs(dir, false, false); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"start", "api", "capabilities", "organization", "reviewed-starts", "app-setup", "agent-setup", "workflow-setup", "shared-services", "model-hosting"} {
		body, topic, err := portabledocs.Read(slug)
		if err != nil {
			t.Fatal(err)
		}
		want := portabledocs.PortableMarkdown(topic, body)
		for _, path := range []string{
			filepath.Join(export, "guides", topic.Filename),
			filepath.Join(dir, ".astrolift", "docs", topic.Slug+".md"),
		} {
			got, err := os.ReadFile(path)
			if err != nil || string(got) != want {
				t.Fatalf("setup knowledge changed during export/install at %s: %v", path, err)
			}
			if strings.Contains(string(got), "NOT_PUBLIC_SETUP_CONTEXT") {
				t.Fatalf("credential entered public setup knowledge at %s", path)
			}
		}
	}
	// An agent's existing review notes must survive a later onboarding run.
	custom := filepath.Join(dir, ".astrolift", "docs", "reviewed-starts.md")
	if err := os.WriteFile(custom, []byte("private local review notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installAgentDocs(dir, false, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(custom)
	if err != nil || string(got) != "private local review notes" {
		t.Fatal("onboarding overwrote existing review context without --force")
	}
}
