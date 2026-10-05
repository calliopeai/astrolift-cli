package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type domainDiagnosticWireRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

func domainDiagnosticServer(t *testing.T, respond func(http.ResponseWriter, domainDiagnosticWireRequest)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app/gql/config/" {
			t.Errorf("unexpected diagnostic request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer selected-token" {
			t.Error("diagnostic used another server's credential")
		}
		var request domainDiagnosticWireRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(request.Query, "astroliftOrganizations") {
			_, _ = fmt.Fprint(w, `{"data":{"astroliftOrganizations":[{"id":"org-guid","slug":"org","name":"Org"}]}}`)
			return
		}
		if r.Header.Get("X-Astrolift-Organization") != "org-guid" {
			t.Error("diagnostic lost selected organization")
		}
		respond(w, request)
	}))
}

func domainReadResponse(t *testing.T, w http.ResponseWriter, version int) {
	t.Helper()
	row := managedDomainFixture("org")
	row["version"] = version
	managedDomainResponse(t, w, "astroliftManagedDomain", row)
}

func TestDomainCheckReadsExactVersionAndReportsMismatch(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonOutput), func(t *testing.T) {
			reads, checks := 0, 0
			server := domainDiagnosticServer(t, func(w http.ResponseWriter, r domainDiagnosticWireRequest) {
				if strings.Contains(r.Query, "astroliftManagedDomain(domainId:") {
					reads++
					if r.Variables["domainId"] != managedDomainTestID {
						t.Error("exact read changed identity")
					}
					domainReadResponse(t, w, 17)
					return
				}
				checks++
				if !strings.Contains(r.Query, "astroliftManagedDomainDiagnostics") || r.Variables["domainId"] != managedDomainTestID || r.Variables["expectedVersion"] != float64(17) || r.Variables["recordType"] != "NS" {
					t.Errorf("diagnostic identity/version/type lost: %+v", r)
				}
				if _, ok := r.Variables["hostname"]; ok {
					t.Error("empty hostname was sent instead of apex default")
				}
				managedDomainResponse(t, w, "astroliftManagedDomainDiagnostics", map[string]interface{}{
					"domainId": managedDomainTestID, "version": 17, "zone": "apps.example.test", "checkedAt": "2026-10-04T23:00:00Z",
					"checks": []interface{}{map[string]interface{}{"key": "delegation", "state": "MISMATCH", "perspective": "public recursive resolver", "reason": "Update registrar delegation", "expected": []string{"ns.aws.test"}, "observed": []string{"ns.cloudflare.test"}}},
					"providerZone": map[string]interface{}{"state": "OK", "reason": "Zone exists", "truncated": true, "records": []interface{}{
						map[string]interface{}{"name": "app.apps.example.test", "type": "A", "ttl": nil, "values": []string{}, "aliasTarget": "lb.example.test", "aliasZoneId": "Z-ALIAS", "evaluateTargetHealth": true},
					}},
					"routes": []interface{}{map[string]interface{}{"recordedUrl": "https://app.apps.example.test", "appName": "App", "environmentName": "production", "clusterName": "Cluster", "observedState": "RECORDED"}}, "routesTruncated": true,
				})
			})
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			command, out := managedDomainTestCommand("check", managedDomainTestID, fmt.Sprintf("--json=%t", jsonOutput))
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if reads != 1 || checks != 1 {
				t.Fatalf("read/check count %d/%d", reads, checks)
			}
			if jsonOutput {
				var report managedDomainDiagnostics
				if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Checks[0].State != "MISMATCH" || !report.ProviderZone.Truncated || !report.RoutesTruncated {
					t.Fatalf("JSON lost findings: %s, %v", out, err)
				}
			} else {
				for _, want := range []string{"MISMATCH", "public recursive resolver", "ns.aws.test", "ns.cloudflare.test", "Checked:", "absence is not proof", "Routing inventory is incomplete", "https://app.apps.example.test", "alias → lb.example.test"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("missing %q: %s", want, out)
					}
				}
			}
		})
	}
}

func TestDomainProbeUsesServerAndExplicitTool(t *testing.T) {
	for _, tt := range []struct {
		command, tool string
		flags         []string
	}{
		{"lookup", "LOOKUP", []string{"--record-type", "txt"}},
		{"dig", "DIG", []string{"--record-type", "txt"}},
		{"probe", "HTTPS", nil},
		{"probe", "PING", []string{"--tool", "ping"}},
		{"probe", "TRACEROUTE", []string{"--tool", "traceroute"}},
	} {
		t.Run(tt.command+tt.tool, func(t *testing.T) {
			probes := 0
			server := domainDiagnosticServer(t, func(w http.ResponseWriter, r domainDiagnosticWireRequest) {
				if strings.Contains(r.Query, "astroliftManagedDomain(domainId:") {
					domainReadResponse(t, w, 8)
					return
				}
				probes++
				if !strings.Contains(r.Query, "astroliftManagedDomainProbe") || r.Variables["tool"] != tt.tool || r.Variables["hostname"] != "www.apps.example.test" || r.Variables["expectedVersion"] != float64(8) {
					t.Errorf("incorrect probe: %+v", r)
				}
				managedDomainResponse(t, w, "astroliftManagedDomainProbe", map[string]interface{}{"state": "UNSUPPORTED", "perspective": "Astrolift control plane", "checkedAt": "2026-10-04T23:00:00Z", "reason": "Tool unavailable", "hostname": "www.apps.example.test", "tool": tt.tool, "values": []string{}})
			})
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			args := append([]string{tt.command, managedDomainTestID, "--hostname", "www.apps.example.test", "--json"}, tt.flags...)
			command, out := managedDomainTestCommand(args...)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			var result managedDomainProbe
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.State != "UNSUPPORTED" || result.Tool != tt.tool || probes != 1 {
				t.Fatalf("unsupported falsely became health: %s, %v", out, err)
			}
		})
	}
}

func TestDomainDiagnosticRefusesUnavailableOrInvalidReadWithoutProbe(t *testing.T) {
	for _, tt := range []string{"missing", "wrong-id", "invalid-version", "denied", "stale"} {
		t.Run(tt, func(t *testing.T) {
			probes := 0
			server := domainDiagnosticServer(t, func(w http.ResponseWriter, r domainDiagnosticWireRequest) {
				if !strings.Contains(r.Query, "astroliftManagedDomain(domainId:") {
					probes++
					_, _ = fmt.Fprint(w, `{"errors":[{"message":"Domain changed; refresh before checking"}]}`)
					return
				}
				switch tt {
				case "missing":
					managedDomainResponse(t, w, "astroliftManagedDomain", nil)
				case "denied":
					_, _ = fmt.Fprint(w, `{"errors":[{"message":"Permission denied"}]}`)
				case "wrong-id":
					row := managedDomainFixture("org")
					row["id"] = "foreign"
					row["version"] = 3
					managedDomainResponse(t, w, "astroliftManagedDomain", row)
				case "invalid-version":
					domainReadResponse(t, w, 0)
				case "stale":
					domainReadResponse(t, w, 4)
				}
			})
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			command, out := managedDomainTestCommand("probe", managedDomainTestID, "--hostname", "www.apps.example.test", "--json")
			if err := command.Execute(); err == nil {
				t.Fatal("invalid identity/permission/version accepted")
			}
			want := 0
			if tt == "stale" {
				want = 1
			}
			if probes != want || out.Len() != 0 {
				t.Fatalf("unexpected fallback/retry/output: probes=%d output=%s", probes, out)
			}
		})
	}
}

func TestDomainDiagnosticValidationBeforeCredentialOrNetworkRequests(t *testing.T) {
	for _, args := range [][]string{
		{"check", ""}, {"check", managedDomainTestID, "--record-type", "ANY"},
		{"lookup", managedDomainTestID}, {"probe", managedDomainTestID, "--hostname", "www.apps.example.test", "--tool", "curl"},
		{"probe", managedDomainTestID, "--hostname", "www.apps.example.test", "--record-type", "invalid"},
		{"check", managedDomainTestID, "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			command, _ := managedDomainTestCommand(args...)
			if err := command.Execute(); err == nil || requests != 0 {
				t.Fatalf("invalid flags sent %d requests: %v", requests, err)
			}
		})
	}
}
