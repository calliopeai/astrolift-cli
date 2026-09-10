package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newAPIGraphQLTestCmd() *cobra.Command {
	c := &cobra.Command{Use: "graphql"}
	c.Flags().StringP("file", "f", "", "")
	c.Flags().StringP("query", "q", "", "")
	c.Flags().StringArray("var", nil, "")
	c.Flags().String("vars-file", "", "")
	return c
}

func TestReadGraphQLDocumentFromQueryFlag(t *testing.T) {
	c := newAPIGraphQLTestCmd()
	_ = c.Flags().Set("query", "  query { me { id } }  ")

	got, err := readGraphQLDocument(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "query { me { id } }" {
		t.Errorf("document = %q, want the trimmed query", got)
	}
}

func TestReadGraphQLDocumentFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.graphql")
	if err := os.WriteFile(path, []byte("query { me { id } }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newAPIGraphQLTestCmd()
	_ = c.Flags().Set("file", path)

	got, err := readGraphQLDocument(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "query { me { id } }" {
		t.Errorf("document = %q", got)
	}
}

func TestReadGraphQLDocumentRefusesBothSources(t *testing.T) {
	c := newAPIGraphQLTestCmd()
	_ = c.Flags().Set("file", "q.graphql")
	_ = c.Flags().Set("query", "query { me { id } }")

	if _, err := readGraphQLDocument(c); err == nil {
		t.Fatal("expected an error when both --file and --query are given")
	}
}

func TestReadGraphQLDocumentMissingFileSaysWhich(t *testing.T) {
	c := newAPIGraphQLTestCmd()
	_ = c.Flags().Set("file", "/nope/does-not-exist.graphql")

	_, err := readGraphQLDocument(c)
	if err == nil || !strings.Contains(err.Error(), "does-not-exist.graphql") {
		t.Fatalf("error should name the file, got %v", err)
	}
}

func TestGraphQLVariablesParseJSONValuesAndKeepStrings(t *testing.T) {
	c := newAPIGraphQLTestCmd()
	_ = c.Flags().Set("var", "appSlug=quake-dash")
	_ = c.Flags().Set("var", "limit=10")
	_ = c.Flags().Set("var", "force=true")

	vars, err := graphQLVariables(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["appSlug"] != "quake-dash" {
		t.Errorf("appSlug = %#v, want the string", vars["appSlug"])
	}
	if vars["limit"] != float64(10) {
		t.Errorf("limit = %#v, want a number", vars["limit"])
	}
	if vars["force"] != true {
		t.Errorf("force = %#v, want a boolean", vars["force"])
	}
}

func TestGraphQLVariablesRejectAMalformedPair(t *testing.T) {
	c := newAPIGraphQLTestCmd()
	_ = c.Flags().Set("var", "appSlug")

	if _, err := graphQLVariables(c); err == nil {
		t.Fatal("expected an error for a --var without =")
	}
}

func TestGraphQLVariablesMergeVarsFileWithFlagsWinning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vars.json")
	if err := os.WriteFile(path, []byte(`{"appSlug":"from-file","env":"production"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newAPIGraphQLTestCmd()
	_ = c.Flags().Set("vars-file", path)
	_ = c.Flags().Set("var", "appSlug=from-flag")

	vars, err := graphQLVariables(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["appSlug"] != "from-flag" {
		t.Errorf("appSlug = %#v, want the flag to win", vars["appSlug"])
	}
	if vars["env"] != "production" {
		t.Errorf("env = %#v, want the file's value to survive", vars["env"])
	}
}

func TestGraphQLVariablesRejectANonObjectVarsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vars.json")
	if err := os.WriteFile(path, []byte(`["not","an","object"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newAPIGraphQLTestCmd()
	_ = c.Flags().Set("vars-file", path)

	if _, err := graphQLVariables(c); err == nil {
		t.Fatal("expected an error for a vars file that is not a JSON object")
	}
}
