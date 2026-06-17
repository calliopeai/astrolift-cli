package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

func orgServer(orgs []orgRef) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{"astroliftOrganizations": orgs},
		})
	}))
}

// testCmd builds a command carrying the flags resolveOrg inspects.
func testCmd() *cobra.Command {
	c := &cobra.Command{}
	c.Flags().String("org", "", "")
	c.Flags().Bool("no-prompt", false, "")
	c.Flags().Bool("debug", false, "")
	c.Flags().Bool("json", false, "")
	return c
}

func TestParsePairs(t *testing.T) {
	got, err := parsePairs([]string{"A=1", "B=x=y"}, "env")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["A"] != "1" || got["B"] != "x=y" {
		t.Fatalf("got %v", got)
	}
	if _, err := parsePairs([]string{"nope"}, "env"); err == nil {
		t.Fatal("expected error for a value missing '='")
	}
}

func TestResolveOrgPrecedence(t *testing.T) {
	orgs := []orgRef{{ID: "id-a", Slug: "a", Name: "A"}, {ID: "id-b", Slug: "b", Name: "B"}}
	srv := orgServer(orgs)
	defer srv.Close()
	client := api.NewClient(srv.URL, "tok", false)
	ctx := context.Background()

	// --org flag wins over the configured default.
	c := testCmd()
	_ = c.Flags().Set("org", "b")
	got, err := resolveOrg(c, ctx, client, &config.Config{DefaultOrg: "a"})
	if err != nil || got.Slug != "b" {
		t.Fatalf("flag precedence: got %+v err %v", got, err)
	}

	// DefaultOrg used when no flag.
	c = testCmd()
	got, err = resolveOrg(c, ctx, client, &config.Config{DefaultOrg: "a"})
	if err != nil || got.Slug != "a" {
		t.Fatalf("default org: got %+v err %v", got, err)
	}

	// Ambiguous (multi-org, nothing chosen) + no-prompt -> error, no silent pick.
	c = testCmd()
	_ = c.Flags().Set("no-prompt", "true")
	if _, err = resolveOrg(c, ctx, client, &config.Config{}); err == nil {
		t.Fatal("expected error for ambiguous multi-org under --no-prompt")
	}

	// Unknown org -> error.
	c = testCmd()
	_ = c.Flags().Set("org", "zzz")
	if _, err = resolveOrg(c, ctx, client, &config.Config{}); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestResolveOrgSingleAuto(t *testing.T) {
	srv := orgServer([]orgRef{{ID: "only", Slug: "solo", Name: "Solo"}})
	defer srv.Close()
	client := api.NewClient(srv.URL, "tok", false)

	// Single org resolves with no flag/default and without prompting.
	c := testCmd()
	_ = c.Flags().Set("no-prompt", "true")
	got, err := resolveOrg(c, context.Background(), client, &config.Config{})
	if err != nil || got.Slug != "solo" {
		t.Fatalf("single-org auto: got %+v err %v", got, err)
	}
}
