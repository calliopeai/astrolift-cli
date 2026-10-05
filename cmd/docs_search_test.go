package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/portabledocs"
	"github.com/spf13/cobra"
)

func TestDocsSearchTokenPhraseAndCase(t *testing.T) {
	terms, err := parseDocsSearchQuery(` WORKFLOW "Request   File" recovery `)
	if err != nil || !reflect.DeepEqual(terms, []string{"workflow", "request file", "recovery"}) {
		t.Fatalf("parse: %v %v", terms, err)
	}
	if !docsSearchContains("Recovery for a WORKFLOW with its Request\n File.", terms) {
		t.Fatal("normalized phrase and separate terms did not match")
	}
	if docsSearchContains("workflow request private file recovery", terms) {
		t.Fatal("noncontiguous quoted phrase matched")
	}
	if docsSearchContains("workflow request file", terms) {
		t.Fatal("missing required term matched")
	}
	for _, invalid := range []string{"", " ", `"`, strings.Repeat("x", 513), strings.Repeat("x ", 33), string([]byte{0xff})} {
		if _, err := parseDocsSearchQuery(invalid); err == nil {
			t.Fatalf("invalid query accepted: %q", invalid)
		}
	}
}

func TestDocsSearchDeterministicBoundsAndNoMatches(t *testing.T) {
	a, err := searchPortableDocs("astro", 2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := searchPortableDocs("ASTRO", 2)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatal("case/order changed results")
	}
	if len(a.Matches) != 2 || !a.Truncated || a.TotalMatches <= len(a.Matches) {
		t.Fatalf("unbounded/incomplete metadata: %#v", a)
	}
	if a.Matches[0].Topic.Slug != portabledocs.Topics()[0].Slug {
		t.Fatal("catalogue order changed")
	}
	for _, invalid := range []int{0, 51, -1} {
		if _, err := searchPortableDocs("astro", invalid); err == nil {
			t.Fatal("invalid limit accepted")
		}
	}
	none, err := searchPortableDocs("absent-term-28509f64", 50)
	if err != nil || none.Matches == nil || len(none.Matches) != 0 || none.TotalMatches != 0 || none.Truncated {
		t.Fatalf("empty result: %#v %v", none, err)
	}
	line, snippet := docsSearchSnippet(strings.Repeat("é", 250)+" needle", []string{"needle"})
	if line != 1 || len([]rune(snippet)) != 180 || !strings.HasSuffix(snippet, "...") {
		t.Fatal("Unicode snippet bound invalid")
	}
	line, snippet = docsSearchSnippet("# Guide\n\nA request\nfile survives.", []string{"request file"})
	if line != 3 || snippet != "A request" {
		t.Fatal("multi-line phrase source line missing")
	}
}

func TestDocsSearchOfflineCommandAndDiscovery(t *testing.T) {
	t.Setenv("ASTROLIFT_TOKEN", "DO_NOT_READ_CREDENTIALS")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "no-config"))
	command := &cobra.Command{}
	command.Flags().Int("limit", 50, "")
	command.Flags().Bool("json", true, "")
	output := &bytes.Buffer{}
	command.SetOut(output)
	if err := runDocsSearch(command, `"original request"`); err != nil {
		t.Fatal(err)
	}
	var result docsSearchResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, match := range result.Matches {
		if match.Topic.Slug == "reviewed-starts" {
			found = true
		}
	}
	if !found || strings.Contains(output.String(), "DO_NOT_READ_CREDENTIALS") {
		t.Fatal("reviewed guide not discoverable or credentials read")
	}
	help := portableDocsHelp()
	if !strings.Contains(help, strconv.Itoa(len(portabledocs.Topics()))+" release-matched guides") {
		t.Fatal("help count not derived from catalogue")
	}
	for _, topic := range portabledocs.Topics() {
		if !strings.Contains(help, topic.Slug) {
			t.Fatalf("help omits %s", topic.Slug)
		}
	}
}

func TestDocsSearchGeneratedCommandReferences(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "export")
	if _, err := exportPortableDocs(directory, false); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"commands/astro_docs_search.md", "man/man1/astro-docs-search.1"} {
		body, err := os.ReadFile(filepath.Join(directory, relative))
		if err != nil || !strings.Contains(string(body), "search") {
			t.Fatalf("generated %s missing: %v", relative, err)
		}
	}
}

func TestDomainKnowledgeSearchExportAndLiveCommandReferences(t *testing.T) {
	result, err := searchPortableDocs(`"public delegation"`, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, match := range result.Matches {
		found = found || match.Topic.Slug == "domains"
	}
	if !found {
		t.Fatal("offline knowledge omitted public DNS delegation guidance")
	}
	directory := filepath.Join(t.TempDir(), "export")
	if _, err := exportPortableDocs(directory, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(directory, "guides/domains.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"astroliftManagedDomainDiagnostics", "expectedVersion: $version",
		"astro operator domains probe", "Provider account inventory requires platform-operator authority",
		"does not certify an app's", "no redirect following",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("exported knowledge lost %q", want)
		}
	}
	for _, verb := range []string{"show", "check", "lookup", "probe"} {
		path := filepath.Join(directory, "commands", "astro_operator_domains_"+verb+".md")
		body, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(body), verb) {
			t.Errorf("live command reference missing %s: %v", verb, err)
		}
	}
}

func TestEmailDeliveryKnowledgeSearchExportAndBoundaries(t *testing.T) {
	result, err := searchPortableDocs(`"observation_timed_out"`, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, match := range result.Matches {
		found = found || match.Topic.Slug == "email-delivery"
	}
	if !found {
		t.Fatal("offline knowledge omitted email test observation recovery")
	}
	directory := filepath.Join(t.TempDir(), "export")
	if _, err := exportPortableDocs(directory, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(directory, "guides/email-delivery.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"emailDeliveryTestSupport", "sendEmailDeliveryTest", "emailDeliveryTestsPage",
		"--vars-file email-test.json", "--var service=MANAGED_SERVICE_GUID", "maximum is 50",
		"write:apps", "managed_service.update", "Suppression is never bypassed",
		"A lost reply or `unknown` result", "not the\nsubject or body", "no dedicated test-send CLI verb",
		"(domains.md)", "(shared-services.md)", "(api.md)",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("exported mail guide lost %q", want)
		}
	}
	for _, alias := range []string{"mail", "email", "email-delivery"} {
		_, topic, err := portabledocs.Read(alias)
		if err != nil || topic.Slug != "email-delivery" {
			t.Errorf("mail topic %q: %#v %v", alias, topic, err)
		}
	}
}
