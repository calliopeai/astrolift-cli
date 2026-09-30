package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const managedDomainTestID = "d734a0e6-cac0-4c68-bc5f-dcd47459d83c"

type managedDomainWireRequest struct {
	Query     string                            `json:"query"`
	Variables map[string]map[string]interface{} `json:"variables"`
}

func managedDomainWireServer(t *testing.T, respond func(http.ResponseWriter, managedDomainWireRequest)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app/gql/config/" {
			t.Errorf("unexpected wire request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer selected-token" {
			t.Error("managed domains did not use the selected server's credential")
		}
		var request managedDomainWireRequest
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&request); err != nil {
			t.Errorf("decoding wire request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(request.Query, "astroliftOrganizations") {
			_, _ = fmt.Fprint(w, `{"data":{"astroliftOrganizations":[{"id":"org-guid","slug":"org","name":"Org"},{"id":"other-guid","slug":"other","name":"Other"}]}}`)
			return
		}
		if r.Header.Get("X-Astrolift-Organization") != "org-guid" {
			t.Errorf("missing selected organization header: %q", r.Header.Get("X-Astrolift-Organization"))
		}
		respond(w, request)
	}))
}

func managedDomainTestCommand(args ...string) (*cobra.Command, *bytes.Buffer) {
	root := &cobra.Command{Use: "astro", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().Bool("debug", false, "")
	root.PersistentFlags().Bool("no-prompt", true, "")
	root.PersistentFlags().String("org", "", "")
	operator := &cobra.Command{Use: "operator"}
	operator.AddCommand(newManagedDomainsCommand())
	root.AddCommand(operator)
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"operator", "domains"}, args...))
	return root, out
}

func managedDomainResponse(t *testing.T, w http.ResponseWriter, field string, payload interface{}) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{field: payload}}); err != nil {
		t.Error(err)
	}
}

func managedDomainFixture(owner interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id": managedDomainTestID, "zone": "apps.example.test", "organizationSlug": owner,
		"dnsDriver": "route53", "defaultFor": "none", "isWildcardManaged": false,
		"provisionState": "mark_active", "verificationState": "pending",
		"challengeRecordName": "_astrolift-challenge.apps.example.test", "challengeRecordValue": "proof-token",
		"provisionNameservers": []string{"ns.example.test"}, "provisionValidationRecords": []interface{}{},
		"delegationCheck": map[string]interface{}{"ok": true},
	}
}

func TestManagedDomainsListWireAndOutput(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonOutput), func(t *testing.T) {
			server := managedDomainWireServer(t, func(w http.ResponseWriter, request managedDomainWireRequest) {
				if !strings.Contains(request.Query, "astroliftManagedDomains") || strings.Contains(request.Query, "dnsConfig") {
					t.Errorf("invalid list selection: %s", request.Query)
				}
				managedDomainResponse(t, w, "astroliftManagedDomains", []interface{}{managedDomainFixture("org"), managedDomainFixture(nil)})
			})
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			command, out := managedDomainTestCommand("list", fmt.Sprintf("--json=%t", jsonOutput))
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if jsonOutput {
				var domains []managedDomain
				if err := json.Unmarshal(out.Bytes(), &domains); err != nil || len(domains) != 2 {
					t.Fatalf("invalid JSON list: %s, %v", out, err)
				}
				if domains[0].DefaultFor != "none" || domains[1].OrganizationSlug != nil || domains[0].ChallengeRecordValue != "proof-token" {
					t.Fatalf("missing audit/ownership/proof fields: %+v", domains)
				}
			} else {
				for _, want := range []string{"DEFAULT FOR", "none", "platform-shared", "mark_active", "pending", managedDomainTestID} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("missing %q in %s", want, out)
					}
				}
			}
		})
	}
}

func TestManagedDomainsEmptyJSONIsArray(t *testing.T) {
	server := managedDomainWireServer(t, func(w http.ResponseWriter, _ managedDomainWireRequest) {
		managedDomainResponse(t, w, "astroliftManagedDomains", []interface{}{})
	})
	defer server.Close()
	serverSelectionFixture(t, server.URL)
	command, out := managedDomainTestCommand("list", "--json")
	if err := command.Execute(); err != nil || strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("empty list: %v, %s", err, out)
	}
}

func TestManagedDomainCreateExplicitDefaultsAndProof(t *testing.T) {
	server := managedDomainWireServer(t, func(w http.ResponseWriter, request managedDomainWireRequest) {
		want := map[string]interface{}{"zone": "apps.example.test", "dnsDriver": "route53", "defaultFor": "tenant_apps", "organizationScoped": true, "isWildcardManaged": false}
		if !strings.Contains(request.Query, "CreateManagedDomainInput!") || !reflect.DeepEqual(request.Variables["input"], want) {
			t.Errorf("create request: %+v, want %v", request, want)
		}
		domain := managedDomainFixture("org")
		domain["defaultFor"] = "tenant_apps"
		managedDomainResponse(t, w, "createManagedDomain", map[string]interface{}{"ok": true, "errors": []interface{}{}, "data": domain})
	})
	defer server.Close()
	serverSelectionFixture(t, server.URL)
	command, out := managedDomainTestCommand("create", "apps.example.test", "--dns-driver", "route53")
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"default_for=tenant_apps", "Publish TXT", "proof-token", "astro operator domains verify apps.example.test"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q: %s", want, out)
		}
	}
}

func TestManagedDomainSharedCreateConfigAndJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dns.json")
	if err := os.WriteFile(path, []byte(`{"zone_id":"Z123","private_credential":"do-not-print"}`), 0600); err != nil {
		t.Fatal(err)
	}
	server := managedDomainWireServer(t, func(w http.ResponseWriter, request managedDomainWireRequest) {
		input := request.Variables["input"]
		wantConfig := map[string]interface{}{"zone_id": "Z123", "private_credential": "do-not-print"}
		if input["organizationScoped"] != false || input["defaultFor"] != "both" || input["isWildcardManaged"] != true || !reflect.DeepEqual(input["dnsConfig"], wantConfig) {
			t.Errorf("explicit shared create: %v", input)
		}
		domain := managedDomainFixture(nil)
		domain["defaultFor"] = "both"
		managedDomainResponse(t, w, "createManagedDomain", map[string]interface{}{"ok": true, "data": domain})
	})
	defer server.Close()
	serverSelectionFixture(t, server.URL)
	command, out := managedDomainTestCommand("create", "apps.example.test", "--dns-driver", "route53", "--shared", "--default-for", "both", "--wildcard", "--dns-config", path, "--json")
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var domain managedDomain
	if err := json.Unmarshal(out.Bytes(), &domain); err != nil || domain.OrganizationSlug != nil || domain.DefaultFor != "both" {
		t.Fatalf("create JSON: %s, %v", out, err)
	}
	if strings.Contains(out.String(), "do-not-print") || strings.Contains(out.String(), "Publish TXT") {
		t.Fatalf("JSON output leaked config or prose: %s", out)
	}
}

func TestManagedDomainUpdateIsSparseAndPreservesConfig(t *testing.T) {
	for _, test := range []struct {
		flags []string
		want  map[string]interface{}
	}{
		{[]string{"--default-for", "tenant_apps"}, map[string]interface{}{"id": managedDomainTestID, "defaultFor": "tenant_apps"}},
		{[]string{"--wildcard=false"}, map[string]interface{}{"id": managedDomainTestID, "isWildcardManaged": false}},
		{[]string{"--default-for", "none", "--wildcard"}, map[string]interface{}{"id": managedDomainTestID, "defaultFor": "none", "isWildcardManaged": true}},
	} {
		t.Run(strings.Join(test.flags, " "), func(t *testing.T) {
			stored := map[string]interface{}{"dnsConfig": map[string]interface{}{"zone_id": "Z-SAVED", "other": "keep"}, "isWildcardManaged": true, "defaultFor": "none"}
			server := managedDomainWireServer(t, func(w http.ResponseWriter, request managedDomainWireRequest) {
				if !strings.Contains(request.Query, "UpdateManagedDomainInput!") || !reflect.DeepEqual(request.Variables["input"], test.want) {
					t.Errorf("sparse update: %+v, want %v", request, test.want)
				}
				for key, value := range request.Variables["input"] {
					stored[key] = value
				}
				managedDomainResponse(t, w, "updateManagedDomain", map[string]interface{}{"ok": true, "data": managedDomainFixture("org")})
			})
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			command, out := managedDomainTestCommand(append([]string{"update", managedDomainTestID, "--json"}, test.flags...)...)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stored["dnsConfig"], map[string]interface{}{"zone_id": "Z-SAVED", "other": "keep"}) {
				t.Fatalf("correction overwrote unrelated DNS config: %v", stored)
			}
			var domain managedDomain
			if err := json.Unmarshal(out.Bytes(), &domain); err != nil {
				t.Fatalf("update JSON: %v", err)
			}
		})
	}
}

func TestManagedDomainLocalValidationBeforeRequests(t *testing.T) {
	badConfig := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(badConfig, []byte(`["secret-do-not-print"]`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"create", "apps.example.test"},
		{"create", "apps.example.test", "--dns-driver", "route53", "--default-for", "invalid"},
		{"create", "apps.example.test", "--dns-driver", "route53", "--dns-config", badConfig},
		{"create", "apps.example.test", "--dns-driver", "route53", "--dns-config", filepath.Join(t.TempDir(), "missing.json")},
		{"update", managedDomainTestID},
		{"update", managedDomainTestID, "--default-for", ""},
		{"update", "", "--wildcard=false"},
		{"list", "extra"},
		{"verify", ""},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(500) }))
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			command, out := managedDomainTestCommand(args...)
			err := command.Execute()
			if err == nil || calls != 0 || out.Len() != 0 || strings.Contains(err.Error(), "secret-do-not-print") {
				t.Fatalf("invalid input reached network or leaked config: calls=%d, out=%s, err=%v", calls, out, err)
			}
		})
	}
}

func TestManagedDomainServerErrorsAndMissingData(t *testing.T) {
	for _, test := range []struct {
		name, field, body, want string
		args                    []string
	}{
		{"validation", "createManagedDomain", `{"ok":false,"errors":[{"code":"VALIDATION","field":"zone","message":"zone is required"}],"data":null}`, "zone is required", []string{"create", "invalid", "--dns-driver", "route53"}},
		{"shared denial", "createManagedDomain", `{"ok":false,"errors":[{"code":"FORBIDDEN","message":"platform operator required"}],"data":null}`, "platform operator required", []string{"create", "apps.example.test", "--dns-driver", "route53", "--shared"}},
		{"update denial", "updateManagedDomain", `{"ok":false,"errors":[{"code":"FORBIDDEN","message":"provider_plugin.configure denied"}],"data":null}`, "provider_plugin.configure denied", []string{"update", managedDomainTestID, "--default-for", "both"}},
		{"foreign id", "updateManagedDomain", `{"ok":false,"errors":[{"code":"NOT_FOUND","message":"domain not found"}],"data":null}`, "domain not found", []string{"update", managedDomainTestID, "--wildcard=false"}},
		{"missing data", "updateManagedDomain", `{"ok":true,"data":null}`, "returned no domain", []string{"update", managedDomainTestID, "--default-for", "both"}},
		{"verify denial", "verifyManagedDomain", `{"ok":false,"errors":[{"code":"FORBIDDEN","message":"team bearer cannot act on organization resources"}],"data":null}`, "team bearer", []string{"verify", "apps.example.test"}},
		{"verify missing data", "verifyManagedDomain", `{"ok":true,"data":null}`, "returned no verification result", []string{"verify", "apps.example.test"}},
		{"query denied", "", `{"errors":[{"message":"permission denied: provider_plugin.read"}]}`, "permission denied", []string{"list"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := managedDomainWireServer(t, func(w http.ResponseWriter, _ managedDomainWireRequest) {
				if test.field == "" {
					_, _ = fmt.Fprint(w, test.body)
				} else {
					_, _ = fmt.Fprintf(w, `{"data":{%q:%s}}`, test.field, test.body)
				}
			})
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			command, out := managedDomainTestCommand(append(test.args, "--json")...)
			err := command.Execute()
			if err == nil || !strings.Contains(err.Error(), test.want) || out.Len() != 0 {
				t.Fatalf("denial reported success: err=%v, out=%s", err, out)
			}
		})
	}
}

func TestManagedDomainVerificationWireAndExit(t *testing.T) {
	for _, verified := range []bool{true, false} {
		t.Run(fmt.Sprint(verified), func(t *testing.T) {
			server := managedDomainWireServer(t, func(w http.ResponseWriter, request managedDomainWireRequest) {
				if !strings.Contains(request.Query, "VerifyManagedDomainInput!") || !reflect.DeepEqual(request.Variables["input"], map[string]interface{}{"zone": "apps.example.test"}) {
					t.Errorf("verify request: %+v", request)
				}
				managedDomainResponse(t, w, "verifyManagedDomain", map[string]interface{}{"ok": true, "data": map[string]interface{}{"zone": "apps.example.test", "verified": verified, "message": "TXT check result"}})
			})
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			command, out := managedDomainTestCommand("verify", "apps.example.test", "--json")
			err := command.Execute()
			if (err == nil) != verified {
				t.Fatalf("verified=%t: err=%v", verified, err)
			}
			var result struct {
				Verified bool `json:"verified"`
			}
			if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil || result.Verified != verified {
				t.Fatalf("verify JSON: %s, %v", out, decodeErr)
			}
		})
	}
}

func TestManagedDomainsRegisteredUnderOperator(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"operator", "domains", "update"})
	if err != nil || command.Name() != "update" || command.Parent().Name() != "domains" {
		t.Fatalf("missing real command registration: %v, %v", command, err)
	}
}

func TestManagedDomainExplicitConfigUpdateReplacesOnlyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dns.json")
	if err := os.WriteFile(path, []byte(`{"zone_id":"Z-NEW","opaque_number":9007199254740993}`), 0600); err != nil {
		t.Fatal(err)
	}
	server := managedDomainWireServer(t, func(w http.ResponseWriter, request managedDomainWireRequest) {
		want := map[string]interface{}{"id": managedDomainTestID, "dnsConfig": map[string]interface{}{"zone_id": "Z-NEW", "opaque_number": json.Number("9007199254740993")}}
		if !reflect.DeepEqual(request.Variables["input"], want) {
			t.Errorf("config replacement changed other fields or lost precision: %v", request.Variables["input"])
		}
		managedDomainResponse(t, w, "updateManagedDomain", map[string]interface{}{"ok": true, "data": managedDomainFixture("org")})
	})
	defer server.Close()
	serverSelectionFixture(t, server.URL)
	command, _ := managedDomainTestCommand("update", managedDomainTestID, "--dns-config", path)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestManagedDomainsWarnsWhenAuditMayBeTruncated(t *testing.T) {
	server := managedDomainWireServer(t, func(w http.ResponseWriter, _ managedDomainWireRequest) {
		domains := make([]interface{}, 200)
		for i := range domains {
			domains[i] = managedDomainFixture("org")
		}
		managedDomainResponse(t, w, "astroliftManagedDomains", domains)
	})
	defer server.Close()
	serverSelectionFixture(t, server.URL)
	command, out := managedDomainTestCommand("list", "--json")
	var diagnostics bytes.Buffer
	command.SetErr(&diagnostics)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var domains []managedDomain
	if err := json.Unmarshal(out.Bytes(), &domains); err != nil || len(domains) != 200 || !strings.Contains(diagnostics.String(), "audit may be incomplete") {
		t.Fatalf("truncated audit must keep JSON valid and warn: err=%v, rows=%d, stderr=%s", err, len(domains), &diagnostics)
	}
}
