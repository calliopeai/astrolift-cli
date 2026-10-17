package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

func expiryTestCmd() (*cobra.Command, *bytes.Buffer) {
	c, out := appTestCmd()
	creationExpiryFlags(c)
	for _, f := range []string{"by", "until"} {
		c.Flags().String(f, "", "")
	}
	for _, f := range []string{"clear", "yes", "temporary"} {
		c.Flags().Bool(f, false, "")
	}
	c.SetErr(out)
	return c, out
}

func featureServer(t *testing.T, caps []string, handler func(gqlRequest) map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(req.Query, "astroliftServerInfo") {
			if r.URL.Path != "/app/gql/config/public/" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Astrolift-Organization") != "" {
				t.Errorf("public discovery leaked user credentials")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"astroliftServerInfo": map[string]interface{}{"capabilities": caps}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": handler(req)})
	}))
}

func TestCreationExpiryPreservesDurationAndFlagPrecedence(t *testing.T) {
	srv := featureServer(t, []string{"apps.temporary_expiry"}, func(gqlRequest) map[string]interface{} { t.Fatal("unexpected mutation"); return nil })
	defer srv.Close()
	for _, ttl := range []string{"30m", "4h", "7d", "6w", "3mo"} {
		c, _ := expiryTestCmd()
		_ = c.Flags().Set("ttl", ttl)
		input := map[string]interface{}{}
		if err := applyCreationExpiry(c, context.Background(), api.NewClient(srv.URL, "person", false), input, "2d"); err != nil {
			t.Fatal(err)
		}
		if input["expiresIn"] != ttl {
			t.Fatalf("TTL rewritten: %#v", input)
		}
	}
	c, _ := expiryTestCmd()
	at := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	_ = c.Flags().Set("expires-at", at)
	input := map[string]interface{}{}
	if err := applyCreationExpiry(c, context.Background(), api.NewClient(srv.URL, "person", false), input, "7d"); err != nil {
		t.Fatal(err)
	}
	if input["expiresAt"] != at || input["expiresIn"] != nil {
		t.Fatalf("flags must override manifest: %#v", input)
	}
	c, _ = expiryTestCmd()
	input = map[string]interface{}{}
	if err := applyCreationExpiry(c, context.Background(), api.NewClient(srv.URL, "person", false), input, "6w"); err != nil {
		t.Fatal(err)
	}
	if input["expiresIn"] != "6w" {
		t.Fatalf("manifest TTL lost: %#v", input)
	}
}

func TestExpiryInvalidAndUnsupportedNeverWrite(t *testing.T) {
	writes := 0
	srv := featureServer(t, nil, func(gqlRequest) map[string]interface{} { writes++; return nil })
	defer srv.Close()
	for _, ttl := range []string{"0m", "-1d", "1.5h", "1h30m", "forever"} {
		if _, err := expiryInput(ttl, ""); err == nil {
			t.Errorf("accepted %q", ttl)
		}
	}
	for _, until := range []string{"2026-10-17", "2020-01-01T00:00:00Z"} {
		if _, err := expiryInput("", until); err == nil {
			t.Errorf("accepted %q", until)
		}
	}
	if _, err := expiryInput("1d", "2099-01-01T00:00:00Z"); err == nil {
		t.Fatal("accepted competing expiry")
	}
	c, _ := expiryTestCmd()
	_ = c.Flags().Set("ttl", "7d")
	if err := applyCreationExpiry(c, context.Background(), api.NewClient(srv.URL, "person", false), map[string]interface{}{}, ""); err == nil || err.Error() != expiryUnavailable {
		t.Fatalf("unsupported server: %v", err)
	}
	if writes != 0 {
		t.Fatalf("wrote to unsupported server: %d", writes)
	}
}

func TestExpiryExtendClearConfirmationAndPolicyRefusal(t *testing.T) {
	var inputs []map[string]interface{}
	refuse := false
	srv := featureServer(t, []string{"apps.temporary_expiry"}, func(req gqlRequest) map[string]interface{} {
		if !strings.Contains(req.Query, "setAppExpiry") {
			t.Errorf("unexpected operation %s", req.Query)
		}
		inputs = append(inputs, req.Variables["input"].(map[string]interface{}))
		if refuse {
			return map[string]interface{}{"setAppExpiry": map[string]interface{}{"ok": false, "errors": []map[string]interface{}{{"code": "PERMISSION_DENIED", "message": "fresh verification required"}}}}
		}
		return map[string]interface{}{"setAppExpiry": map[string]interface{}{"ok": true, "data": map[string]interface{}{"id": "app-guid", "slug": "demo", "expiresAt": "2099-01-01T00:00:00Z", "deleteDataOnExpiry": false}}}
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "person", false)
	c, out := expiryTestCmd()
	_ = c.Flags().Set("by", "2d")
	_ = c.Flags().Set("json", "true")
	if err := runAppExpiry(c, context.Background(), client, "demo", false); err != nil {
		t.Fatal(err)
	}
	if inputs[0]["extendBy"] != "2d" || inputs[0]["expiresIn"] != nil {
		t.Fatalf("wrong extend input: %#v", inputs)
	}
	if !strings.Contains(out.String(), `"expiresAt"`) {
		t.Fatalf("missing JSON expiry: %s", out)
	}
	for _, tc := range []struct {
		noPrompt  bool
		answer    string
		yes       bool
		wantWrite bool
	}{{true, "", false, false}, {false, "n\n", false, false}, {false, "y\n", false, true}, {true, "", true, true}} {
		c, _ := expiryTestCmd()
		_ = c.Flags().Set("clear", "true")
		_ = c.Flags().Set("no-prompt", fmt.Sprint(tc.noPrompt))
		_ = c.Flags().Set("yes", fmt.Sprint(tc.yes))
		c.SetIn(strings.NewReader(tc.answer))
		before := len(inputs)
		err := runAppExpiry(c, context.Background(), client, "demo", true)
		if (len(inputs) > before) != tc.wantWrite {
			t.Fatalf("confirmation %v: wrote=%v err=%v", tc, len(inputs) > before, err)
		}
		if tc.wantWrite && inputs[len(inputs)-1]["clear"] != true {
			t.Fatalf("clear input: %#v", inputs)
		}
	}
	refuse = true
	c, _ = expiryTestCmd()
	_ = c.Flags().Set("clear", "true")
	_ = c.Flags().Set("yes", "true")
	if err := runAppExpiry(c, context.Background(), client, "demo", true); err == nil || !strings.Contains(err.Error(), "fresh verification") {
		t.Fatalf("policy refusal lost: %v", err)
	}
}

func TestExpiryListShowAndOldServerFallback(t *testing.T) {
	for _, old := range []bool{false, true} {
		t.Run(fmt.Sprint(old), func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req gqlRequest
				_ = json.NewDecoder(r.Body).Decode(&req)
				requests++
				if old && strings.Contains(req.Query, "expiresAt") {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []map[string]string{{"message": "Cannot query field 'expiresAt' on type 'AstroliftRegisteredApp'"}}})
					return
				}
				temporary := map[string]interface{}{"id": "a", "slug": "temp", "name": "Temporary"}
				permanent := map[string]interface{}{"slug": "perm", "name": "Permanent"}
				if !old {
					temporary["expiresAt"] = "2099-01-01T00:00:00Z"
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"astroliftApps": []interface{}{temporary, permanent}, "astroliftApp": temporary}})
			}))
			defer srv.Close()
			client := api.NewClient(srv.URL, "person", false)
			c, out := expiryTestCmd()
			if err := runAppList(c, context.Background(), client); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "EXPIRES") {
				t.Fatalf("missing column: %s", out)
			}
			if old && strings.Contains(out.String(), "permanent") {
				t.Fatalf("unknown expiry claimed permanent: %s", out)
			}
			if !old && !strings.Contains(out.String(), "2099-01-01") {
				t.Fatalf("missing expiry: %s", out)
			}
			out.Reset()
			_ = c.Flags().Set("temporary", "true")
			err := runAppList(c, context.Background(), client)
			if old {
				if err == nil {
					t.Fatal("unsupported filter succeeded")
				}
			} else if err != nil || strings.Contains(out.String(), "perm ") {
				t.Fatalf("temporary filter: %s %v", out, err)
			}
			out.Reset()
			_ = c.Flags().Set("json", "true")
			if err := runAppShow(c, context.Background(), client, "temp"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), `"expiresAt"`) || !strings.Contains(out.String(), fmt.Sprintf(`"expiryAvailable": %t`, !old)) {
				t.Fatalf("JSON lacks capability state: %s", out)
			}
			if requests < 3 {
				t.Fatal("missing reads")
			}
		})
	}
}

func TestExpiryPermissionFailureNeverFallsBack(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []map[string]interface{}{{"message": "permission denied", "extensions": map[string]string{"code": "FORBIDDEN"}}}})
	}))
	defer srv.Close()
	c, _ := expiryTestCmd()
	if err := runAppList(c, context.Background(), api.NewClient(srv.URL, "person", false)); err == nil {
		t.Fatal("permission failure swallowed")
	}
	if calls != 1 {
		t.Fatalf("permission failure retried: %d", calls)
	}
}

func TestRegisterManifestTTLIsAtomicAndSelected(t *testing.T) {
	savedFile, savedProject, savedRepo, savedJSON := appRegisterFile, appRegisterProjectID, appRegisterSourceRepo, appRegisterJSON
	t.Cleanup(func() {
		appRegisterFile, appRegisterProjectID, appRegisterSourceRepo, appRegisterJSON = savedFile, savedProject, savedRepo, savedJSON
	})
	appRegisterFile = filepath.Join(t.TempDir(), "astrolift.toml")
	appRegisterProjectID = "project-guid"
	appRegisterSourceRepo = "example/demo"
	appRegisterJSON = true
	if err := os.WriteFile(appRegisterFile, []byte("[app]\nslug='demo'\ndisplay_name='Demo'\nexpires_in='7d'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var input map[string]interface{}
	srv := featureServer(t, []string{"apps.temporary_expiry"}, func(req gqlRequest) map[string]interface{} {
		input = req.Variables["input"].(map[string]interface{})
		if !strings.Contains(req.Query, "expiresAt") {
			t.Error("registration did not select expiry")
		}
		return map[string]interface{}{"registerApp": map[string]interface{}{"ok": true, "data": map[string]interface{}{"id": "a", "slug": "demo", "expiresAt": "2099-01-01T00:00:00Z"}}}
	})
	defer srv.Close()
	c, out := expiryTestCmd()
	_ = c.Flags().Set("no-prompt", "true")
	if err := runAppRegister(c, context.Background(), api.NewClient(srv.URL, "person", false)); err != nil {
		t.Fatal(err)
	}
	if input["expiresIn"] != "7d" {
		t.Fatalf("manifest TTL not atomic: %#v", input)
	}
	if !strings.Contains(out.String(), "2099-01-01T00:00:00Z") {
		t.Fatalf("JSON missing expiry: %s", out)
	}
}

func TestBrainHostedAppExpiryIsAtomic(t *testing.T) {
	resetBrainFlags()
	t.Cleanup(resetBrainFlags)
	brainHostImage = "ghcr.io/example/demo:1"
	var input map[string]interface{}
	srv := featureServer(t, []string{"apps.temporary_expiry"}, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "hostAppOnBrain") {
			input = req.Variables["input"].(map[string]interface{})
			if !strings.Contains(req.Query, "expiresAt") {
				t.Error("host response did not select expiry")
			}
			return map[string]interface{}{"hostAppOnBrain": brainOK(map[string]interface{}{"brainSlug": "example-brain", "app": map[string]interface{}{"id": "a", "slug": "demo", "expiresAt": "2099-01-01T00:00:00Z"}})}
		}
		return map[string]interface{}{"astroliftProjects": []interface{}{map[string]interface{}{"id": "project-guid", "slug": "kb"}}}
	})
	defer srv.Close()
	c, out, _ := brainTestCmd(true)
	_ = c.Flags().Set("json", "true")
	creationExpiryFlags(c)
	_ = c.Flags().Set("project", "kb")
	_ = c.Flags().Set("ttl", "3mo")
	if err := runBrainHostApp(c, context.Background(), api.NewClient(srv.URL, "person", false), &config.Config{}, "example-brain"); err != nil {
		t.Fatal(err)
	}
	if input["expiresIn"] != "3mo" {
		t.Fatalf("host TTL not atomic: %#v", input)
	}
	if !strings.Contains(out.String(), "2099-01-01T00:00:00Z") {
		t.Fatalf("host JSON missing expiry: %s", out)
	}
}
