package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIGraphQLRetainsSelectedOrganization(t *testing.T) {
	for _, selection := range []string{"selected-org", "missing-org", ""} {
		t.Run(selection, func(t *testing.T) {
			operations := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request gqlRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				data := map[string]interface{}{}
				if strings.Contains(request.Query, "astroliftOrganizations") {
					data["astroliftOrganizations"] = []map[string]string{{"id": "default-id", "slug": "org"}, {"id": "selected-id", "slug": "selected-org"}}
				} else {
					operations++
					want := "selected-id"
					if selection == "" {
						want = "default-id"
					}
					if r.Header.Get("X-Astrolift-Organization") != want || r.Header.Get("Authorization") != "Bearer selected-token" {
						t.Error("API document used the wrong server or organization")
					}
					data["fixtureValue"] = "ok"
				}
				if err := json.NewEncoder(w).Encode(map[string]interface{}{"data": data}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			command := newAPIGraphQLTestCmd()
			command.SetContext(context.Background())
			command.Flags().String("org", selection, "")
			if err := command.Flags().Set("query", "query { fixtureValue }"); err != nil {
				t.Fatal(err)
			}
			err := apiGraphQLCmd.RunE(command, nil)
			if selection == "missing-org" {
				if err == nil || operations != 0 {
					t.Fatalf("unknown organization forwarded API document: err=%v operations=%d", err, operations)
				}
			} else if err != nil || operations != 1 {
				t.Fatalf("API request failed: err=%v operations=%d", err, operations)
			}
		})
	}
}

func TestAPIGraphQLWithoutOrganizationCanReadAccountResources(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("X-Astrolift-Organization") != "" {
			t.Error("account query unexpectedly selected an organization")
		}
		if err := json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"fixtureAccount": true}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	cfg := serverSelectionFixture(t, server.URL)
	cfg.DefaultOrg = ""
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	command := newAPIGraphQLTestCmd()
	command.SetContext(context.Background())
	if err := command.Flags().Set("query", "query { fixtureAccount }"); err != nil {
		t.Fatal(err)
	}
	if err := apiGraphQLCmd.RunE(command, nil); err != nil || requests != 1 {
		t.Fatalf("unscoped account request failed: err=%v requests=%d", err, requests)
	}
}
