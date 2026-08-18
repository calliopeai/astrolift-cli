package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gqlErrorServer replies with a GraphQL error envelope carrying msgs.
func gqlErrorServer(t *testing.T, msgs ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errs := make([]map[string]string, len(msgs))
		for i, m := range msgs {
			errs[i] = map[string]string{"message": m}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": errs})
	}))
}

// A field the server's schema does not define has to be recognisable, because
// the server rejects the whole query rather than returning a partial result —
// a caller cannot discover the absence by inspecting the response.
func TestGraphQLClassifiesASchemaMismatch(t *testing.T) {
	for _, msg := range []string{
		`Cannot query field "agentBoxPods" on type "Query".`,
		`Unknown field agentBoxPods`,
		`Unknown argument "includeEnded" on field "agentBoxes".`,
		`Unknown type "EnsureAgentBoxInput".`,
	} {
		srv := gqlErrorServer(t, msg)
		err := NewClient(srv.URL, "tok", false).GraphQL(context.Background(), "query{x}", nil, nil)
		srv.Close()
		if !errors.Is(err, ErrSchemaMismatch) {
			t.Errorf("should classify as schema mismatch: %q (got %v)", msg, err)
		}
	}
}

// The safety property. A fallback path keys off this sentinel, so anything
// that is not genuinely "the schema lacks this" must never wear it — a
// permission denial that degraded into a fallback would hide the real problem.
func TestGraphQLDoesNotMistakeRealFailuresForSchemaMismatch(t *testing.T) {
	for _, msg := range []string{
		"permission denied: agent_box.attach",
		"agent box not found",
		"organization mismatch",
		"You do not have permission to perform this action",
	} {
		srv := gqlErrorServer(t, msg)
		err := NewClient(srv.URL, "tok", false).GraphQL(context.Background(), "query{x}", nil, nil)
		srv.Close()
		if err == nil {
			t.Fatalf("expected an error for %q", msg)
		}
		if errors.Is(err, ErrSchemaMismatch) {
			t.Errorf("must NOT read as schema mismatch: %q", msg)
		}
	}
}

// A real error alongside a schema complaint still has to surface as an error,
// or a caller's fallback would silently discard it.
func TestGraphQLMixedErrorsAreNotASchemaMismatch(t *testing.T) {
	srv := gqlErrorServer(t,
		`Cannot query field "agentBoxPods" on type "Query".`,
		"permission denied: agent_box.attach",
	)
	defer srv.Close()

	err := NewClient(srv.URL, "tok", false).GraphQL(context.Background(), "query{x}", nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrSchemaMismatch) {
		t.Error("a mixed batch must not be swallowed as pure version skew")
	}
}

// The message must survive classification — wrapping it in a sentinel is no
// use if the reader cannot see what the server actually said.
func TestSchemaMismatchKeepsTheServerMessage(t *testing.T) {
	srv := gqlErrorServer(t, `Cannot query field "agentBoxPods" on type "Query".`)
	defer srv.Close()

	err := NewClient(srv.URL, "tok", false).GraphQL(context.Background(), "query{x}", nil, nil)
	if err == nil || !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("expected a schema mismatch, got %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "agentBoxPods") {
		t.Errorf("server message lost: %s", got)
	}
}
