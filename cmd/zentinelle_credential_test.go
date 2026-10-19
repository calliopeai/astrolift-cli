package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/auth"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// zentinelleReadsUnderTest are the Zentinelle reads and the permission the
// server gates each on.
var zentinelleReadsUnderTest = []struct {
	name       string
	command    *cobra.Command
	permission string
}{
	{"zentinelle status", zentinelleStatusCmd, "zentinelle.status_view"},
	{"agent usage", agentUsageCmd, "zentinelle.usage_view"},
	{"policy ls", policyLsCmd, "zentinelle.policy_view"},
	{"audit tail", auditTailCmd, "zentinelle.audit_view"},
}

// zentinelleCeilingServer checks a Zentinelle read the way the server does:
// the credential's scopes first (cli-token is a default sign-in, which carries
// no Zentinelle permission; admin-token is the admin wildcard), then the
// account's roles (holds). reads counts the gated requests.
func zentinelleCeilingServer(t *testing.T, holds bool, reads *int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		permission, field, data := "", "", any(nil)
		switch {
		case strings.Contains(request.Query, "astroliftOrganizations"):
			field, data = "astroliftOrganizations", []map[string]string{{"id": "org-id", "slug": "org", "name": "Org"}}
		case strings.Contains(request.Query, "astroliftZentinelleCapability"):
			// org.read, which every CLI sign-in carries.
			field, data = "astroliftZentinelleCapability", map[string]any{"connected": true}
		case strings.Contains(request.Query, "astroliftZentinelleConnection"):
			permission, field, data = "zentinelle.status_view", "astroliftZentinelleConnection", map[string]any{"baseUrl": "https://zentinelle.example", "status": "connected", "gatewayFeatureEnabled": true}
		case strings.Contains(request.Query, "astroliftZentinelleUsage"):
			permission, field, data = "zentinelle.usage_view", "astroliftZentinelleUsage", map[string]any{"since": "2026-10-01T00:00:00Z", "rows": []any{map[string]any{"agentSlug": "triage", "provider": "anthropic", "model": "claude", "requests": 1, "inputTokens": 10, "outputTokens": 5}}}
		case strings.Contains(request.Query, "astroliftZentinellePolicies"):
			permission, field, data = "zentinelle.policy_view", "astroliftZentinellePolicies", []any{map[string]any{"id": "pol-1", "name": "oversight", "enforcement": "enforce"}}
		case strings.Contains(request.Query, "astroliftZentinelleAuditPage"):
			permission, field, data = "zentinelle.audit_view", "astroliftZentinelleAuditPage", map[string]any{"events": []any{map[string]any{"id": "evt-1", "action": "evaluate"}}, "nextCursor": nil}
		default:
			t.Errorf("unexpected document: %s", request.Query)
		}
		reason := ""
		if permission != "" {
			*reads++
			if r.Header.Get("X-Astrolift-Organization") != "org-id" {
				t.Errorf("read without the working org: %s", request.Query)
			}
			switch {
			case r.Header.Get("Authorization") != "Bearer admin-token":
				reason = tokenScopeRefusal
			case !holds:
				reason = "no grant for this permission at any scope"
			}
		}
		if reason != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "errors": []any{map[string]any{
				"message": reason + ": " + permission, "path": []string{field},
				"extensions": map[string]string{"code": "PERMISSION_DENIED", "reason": reason, "permission": permission},
			}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{field: data}})
	}))
	t.Cleanup(server.Close)
	return server
}

// zentinelleReadInvocation selects server and token the way a user would and
// returns a command carrying the flags the reads inspect.
func zentinelleReadInvocation(t *testing.T, serverURL, token string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	serverSelectionFixture(t, serverURL)
	viper.Set("token", token)
	command, out := zentinelleReadCmd()
	errOut := &bytes.Buffer{}
	command.SetErr(errOut)
	command.SetContext(context.Background())
	return command, out, errOut
}

// The server checks the credential's scopes, then the account's roles, and a
// read needs both (#179). A scope refusal names the sign-in that carries the
// permission; an account refusal stays the server's, since no other sign-in
// can fix it. Either way the read is asked once, with no fallback.
func TestZentinelleReadsNeedTheCredentialScopeAndTheAccountPermission(t *testing.T) {
	for _, read := range zentinelleReadsUnderTest {
		for _, fixture := range []struct {
			name, token string
			holds       bool
		}{
			{"default sign-in, account holds it", "cli-token", true},
			{"default sign-in, account lacks it", "cli-token", false},
			{"admin sign-in, account lacks it", "admin-token", false},
			{"admin sign-in, account holds it", "admin-token", true},
		} {
			t.Run(read.name+"/"+fixture.name, func(t *testing.T) {
				resetZentinelleReads()
				t.Cleanup(resetZentinelleReads)
				reads := 0
				server := zentinelleCeilingServer(t, fixture.holds, &reads)
				command, out, errOut := zentinelleReadInvocation(t, server.URL, fixture.token)
				err := read.command.RunE(command, nil)
				if reads != 1 || errOut.Len() != 0 {
					t.Fatalf("asked %d times, stderr %q", reads, errOut)
				}
				var scope *credentialScopeError
				switch {
				case fixture.token == "cli-token":
					if !errors.As(err, &scope) || scope.advice.Permission != read.permission || scope.advice.LoginScope != "admin" || out.Len() != 0 {
						t.Fatalf("scope refusal without the sign-in that carries it: %v", err)
					}
					for _, want := range []string{tokenScopeRefusal + ": " + read.permission, "--scope clusters", "astro auth login --scope admin", "roles cannot widen"} {
						if !strings.Contains(err.Error(), want) {
							t.Fatalf("refusal does not say %q:\n%v", want, err)
						}
					}
				case !fixture.holds:
					if err == nil || errors.As(err, &scope) || strings.Contains(err.Error(), "--scope") || !strings.Contains(err.Error(), "no grant for this permission at any scope: "+read.permission) {
						t.Fatalf("an account refusal must stay the server's, with no sign-in advice: %v", err)
					}
				default:
					if err != nil || out.Len() == 0 {
						t.Fatalf("an allowed read failed: %v %q", err, out)
					}
				}
			})
		}
	}
}

func TestZentinelleReadErrorsUnderJSONAreOneObjectOnStderr(t *testing.T) {
	for _, read := range zentinelleReadsUnderTest {
		t.Run(read.name, func(t *testing.T) {
			resetZentinelleReads()
			t.Cleanup(resetZentinelleReads)
			reads := 0
			server := zentinelleCeilingServer(t, true, &reads)
			command, out, errOut := zentinelleReadInvocation(t, server.URL, "cli-token")
			_ = command.Flags().Set("json", "true")
			err := read.command.RunE(command, nil)
			var reported interface{ AlreadyReported() bool }
			var scope *credentialScopeError
			if !errors.As(err, &reported) || !reported.AlreadyReported() || !errors.As(err, &scope) || out.Len() != 0 {
				t.Fatalf("refusal not reported once as JSON: %v stdout %q", err, out)
			}
			var report struct {
				Error      api.GraphQLResponseError `json:"error"`
				Credential *credentialAdvice        `json:"credential"`
			}
			if err := json.Unmarshal(errOut.Bytes(), &report); err != nil {
				t.Fatalf("stderr is not one JSON object: %v\n%s", err, errOut)
			}
			refusal := report.Error.Errors
			if len(refusal) != 1 || refusal[0].Code != "PERMISSION_DENIED" || refusal[0].Reason != tokenScopeRefusal || refusal[0].Permission != read.permission {
				t.Fatalf("lost the server's refusal: %s", errOut)
			}
			advice := report.Credential
			if advice == nil || advice.Permission != read.permission || advice.LoginScope != "admin" || !strings.Contains(advice.Hint, "astro auth login --scope admin") {
				t.Fatalf("no credential advice: %s", errOut)
			}
			if strings.Contains(errOut.String(), "cli-token") {
				t.Fatal("stderr leaked the credential")
			}
		})
	}
}

func TestZentinelleReadsReportOtherErrorsAsJSONWithoutCredentialAdvice(t *testing.T) {
	resetZentinelleReads()
	t.Cleanup(resetZentinelleReads)
	reads := 0
	server := zentinelleCeilingServer(t, false, &reads)

	command, _, errOut := zentinelleReadInvocation(t, server.URL, "admin-token")
	_ = command.Flags().Set("json", "true")
	if err := zentinelleStatusCmd.RunE(command, nil); err == nil {
		t.Fatal("an account refusal was accepted")
	}
	var refused map[string]json.RawMessage
	if err := json.Unmarshal(errOut.Bytes(), &refused); err != nil || refused["error"] == nil || refused["credential"] != nil {
		t.Fatalf("an account refusal must be JSON without credential advice: %v\n%s", err, errOut)
	}

	command, _, errOut = zentinelleReadInvocation(t, server.URL, "admin-token")
	_ = command.Flags().Set("json", "true")
	if err := auditExportCmd.RunE(command, nil); err == nil {
		t.Fatal("export without --since was accepted")
	}
	var usage struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(errOut.Bytes(), &usage); err != nil || usage.Error.Message != "export needs --since" {
		t.Fatalf("a usage error must be JSON too: %v\n%s", err, errOut)
	}
}

// A mutation's refusal comes back in its envelope, without the permission, so
// the export names the one it needs.
func TestAuditExportScopeRefusalNamesTheSignIn(t *testing.T) {
	for _, fixture := range []struct {
		message string
		scope   bool
	}{
		{tokenScopeRefusal, true},
		{"no grant for this permission at any scope", false},
	} {
		resetZentinelleReads()
		srv := zentinelleServer(t, true, func(gqlRequest) map[string]interface{} {
			return map[string]interface{}{"exportZentinelleAudit": map[string]interface{}{
				"ok": false, "errors": []map[string]string{{"code": "PERMISSION_DENIED", "message": fixture.message}},
			}}
		})
		client := api.NewClient(srv.URL, "token", false)
		client.SetOrg("org-guid")
		zentinelleReadSince = "2026-10-01"
		cmd, _ := zentinelleReadCmd()
		err := runAuditExport(cmd, context.Background(), client, &config.Config{}, nil)
		srv.Close()
		var scope *credentialScopeError
		if err == nil || !strings.Contains(err.Error(), "export refused: "+fixture.message) || errors.As(err, &scope) != fixture.scope {
			t.Fatalf("%q: %v", fixture.message, err)
		}
		if fixture.scope && (scope.advice.Permission != "zentinelle.audit_export" || !strings.Contains(err.Error(), "astro auth login --scope admin")) {
			t.Fatalf("export refusal advice: %v", err)
		}
	}
	resetZentinelleReads()
}

// The sign-in contract the help and the advice state (#179), as of
// astrolift-app 09e3f2c: the server maps no zentinelle.* permission into the
// default (CLI_DEVICE_SCOPES) or clusters (CLI_OPERATOR_DEVICE_SCOPES)
// ceilings; only its admin wildcard covers them. When the server adds a
// narrower Zentinelle sign-in, this and zentinelleLoginScope change with it.
func TestZentinelleSignInContract(t *testing.T) {
	for scope, wantKind := range map[string]string{"": "", "clusters": "cli-operator", zentinelleLoginScope: "cli-admin"} {
		if kind, err := auth.ClientKindForScope(scope); err != nil || kind != wantKind {
			t.Fatalf("--scope %q signs in as %q (%v), want %q", scope, kind, err, wantKind)
		}
	}
	help := func(c *cobra.Command) string { return strings.Join(strings.Fields(c.Long), " ") }
	for _, want := range []string{"Neither the default sign-in nor --scope clusters carries a Zentinelle permission", "only --scope admin carries them", tokenScopeRefusal} {
		if !strings.Contains(help(authLoginCmd), want) {
			t.Errorf("astro auth login --help does not say %q", want)
		}
	}
	for _, want := range []string{"default and --scope clusters sign-ins carry no Zentinelle permission", "astro auth login --help", "one JSON object on stderr"} {
		if !strings.Contains(help(zentinelleCmd), want) {
			t.Errorf("astro zentinelle --help does not say %q", want)
		}
	}
}
