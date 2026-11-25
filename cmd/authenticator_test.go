package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestAuthenticatorConfirmUsesStdinProofAndPrintsRecoveryCodes(t *testing.T) {
	var input map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != authenticatorPath || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer cli-test-token" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		_ = json.NewEncoder(w).Encode(authenticatorReply{Enabled: true, RecoveryCodes: []string{"one-use-code"}})
	}))
	defer server.Close()
	withConfigHome(t, server.URL)
	viper.Set("token", "cli-test-token")
	t.Cleanup(func() { viper.Set("token", "") })

	out := new(bytes.Buffer)
	cmd := newAuthenticatorCommand()
	cmd.SetOut(out)
	cmd.SetIn(strings.NewReader("password from stdin\n123456\n"))
	cmd.SetArgs([]string{"confirm", "--proof-stdin"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if input["password"] != "password from stdin" || input["code"] != "123456" || input["action"] != "confirm" {
		t.Fatal("authenticator proof was not forwarded")
	}
	if !strings.Contains(out.String(), "one-use-code") || strings.Contains(out.String(), "password from stdin") {
		t.Fatal("output must contain recovery codes without the password")
	}
}

func TestAuthenticatorReplacementUsesTheDraftIDAndNewCode(t *testing.T) {
	var captured map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != authenticatorPath || r.Method != http.MethodPost {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(authenticatorReply{Enabled: true, RecoveryCodes: []string{"new-recovery"}})
	}))
	defer server.Close()
	withConfigHome(t, server.URL)
	viper.Set("token", "cli-test-token")
	t.Cleanup(func() { viper.Set("token", "") })

	out := new(bytes.Buffer)
	cmd := newAuthenticatorCommand()
	cmd.SetOut(out)
	cmd.SetIn(strings.NewReader("password from stdin\n345678\n"))
	cmd.SetArgs([]string{"confirm-replacement", "replacement-draft", "--proof-stdin"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if captured["enrollmentId"] != "replacement-draft" || captured["action"] != "confirm_replacement" || captured["code"] != "345678" {
		t.Fatal("replacement confirmation proof was not forwarded")
	}
	if !strings.Contains(out.String(), "new-recovery") || strings.Contains(out.String(), "345678") {
		t.Fatal("output must show recovery codes without the verification code")
	}
}
