package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientKindForScope(t *testing.T) {
	for scope, want := range map[string]string{"": "", "default": "", "clusters": "cli-operator", " Clusters ": "cli-operator", "admin": "cli-admin", "ADMIN": "cli-admin"} {
		got, err := ClientKindForScope(scope)
		if err != nil || got != want {
			t.Fatalf("ClientKindForScope(%q) = %q, %v; want %q", scope, got, err, want)
		}
	}
	if _, err := ClientKindForScope("root"); err == nil {
		t.Fatal("an unknown scope must be refused, not silently widened or dropped")
	}
	// The server has no Zentinelle sign-in kind, and it treats a kind it does
	// not know as the plain CLI. Sending one would approve a sign-in without
	// what was asked for, so the CLI refuses until the server defines it (#179).
	if _, err := ClientKindForScope("zentinelle"); err == nil {
		t.Fatal("--scope zentinelle must be refused until the server defines a kind for it")
	}
}

func TestStartLoginSendsTheClientKind(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"session_id":"s","login_url":"https://x/login"}`))
	}))
	defer srv.Close()

	if _, err := StartLogin(context.Background(), srv.URL, "cli-operator"); err != nil {
		t.Fatal(err)
	}
	if got["client_kind"] != "cli-operator" {
		t.Fatalf("client_kind = %q, want cli-operator", got["client_kind"])
	}

	got = nil
	if _, err := StartLogin(context.Background(), srv.URL, ""); err != nil {
		t.Fatal(err)
	}
	if _, present := got["client_kind"]; present {
		t.Fatal("the plain login must send no client_kind, so the server keeps its CLI default")
	}
}
