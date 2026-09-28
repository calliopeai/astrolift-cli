package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientKindForScope(t *testing.T) {
	for scope, want := range map[string]string{"": "", "default": "", "clusters": "cli-operator", " Clusters ": "cli-operator"} {
		got, err := ClientKindForScope(scope)
		if err != nil || got != want {
			t.Fatalf("ClientKindForScope(%q) = %q, %v; want %q", scope, got, err, want)
		}
	}
	if _, err := ClientKindForScope("admin"); err == nil {
		t.Fatal("an unknown scope must be refused, not silently widened or dropped")
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
