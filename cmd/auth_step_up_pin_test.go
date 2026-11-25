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

func TestStepUpPINRequestsBrowserConfirmationWithoutSendingSecrets(t *testing.T) {
	const id = "b99ba4cc-15dd-43b5-a04a-735612ee3ff4"
	const approval = "https://portal.example.test/app/cli/auth/step-up-pin/" + id + "/"
	requested := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case stepUpPINPath + "/requests":
			var body map[string]string
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
				t.Error("request creation must send only the action in a POST")
			}
			requested <- body["action"]
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(pinChangeRequest{
				ID: id, ApprovalURL: approval, ExpiresIn: 30, Status: "pending",
			})
		case stepUpPINPath + "/requests/" + id:
			_ = json.NewEncoder(w).Encode(pinChangeRequest{ID: id, Status: "completed"})
		case stepUpPINPath:
			_, _ = w.Write([]byte(`{"isSet":true,"locked":false,"orgAllows":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	withConfigHome(t, server.URL)
	viper.Set("token", "device-credential")
	t.Cleanup(func() { viper.Set("token", "") })

	run := func(args ...string) string {
		t.Helper()
		cmd := newStepUpPINCommand()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	if output := run("set", "--no-wait"); !strings.Contains(output, approval) || !strings.Contains(output, id) {
		t.Fatal("the set command did not return a usable approval link and request ID")
	}
	if got := <-requested; got != "set" {
		t.Fatalf("requested %q, want set", got)
	}
	if output := run("clear", "--no-browser"); !strings.Contains(output, "PIN change confirmed") {
		t.Fatal("the clear command did not wait for confirmation")
	}
	if got := <-requested; got != "clear" {
		t.Fatalf("requested %q, want clear", got)
	}
	if output := run("status", id); strings.TrimSpace(output) != "completed" {
		t.Fatalf("unexpected request status %q", output)
	}
	if output := run("status"); !strings.Contains(output, "Step-up PIN set") {
		t.Fatalf("unexpected account status %q", output)
	}
}
