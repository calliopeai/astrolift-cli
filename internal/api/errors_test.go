package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestGraphQLPreservesSafeStructuredErrorMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"errors":[{"message":"refused synthetic-token","path":["deploy",0,{"secret":"hidden"}],"currentVersion":7,"requestedVersion":6,"extensions":{"code":"PERMISSION_DENIED","reason":"credential synthetic-token cannot write","currentVersion":99,"token":"extension-secret","debug":{"password":"hidden"}}}]}`)
	}))
	defer server.Close()
	err := NewClient(server.URL, "synthetic-token", false).GraphQL(context.Background(), "query { fixture }", nil, nil)
	var failure *GraphQLResponseError
	if !errors.As(err, &failure) {
		t.Fatalf("not typed: %v", err)
	}
	item := failure.Errors[0]
	if item.Code != "PERMISSION_DENIED" || item.Reason != "credential [redacted] cannot write" || len(item.Path) != 2 || *item.CurrentVersion != 7 || *item.RequestedVersion != 6 {
		t.Fatalf("metadata lost: %+v", item)
	}
	encoded, _ := json.Marshal(failure)
	for _, forbidden := range []string{"synthetic-token", "extension-secret", "password", "hidden", "debug"} {
		if strings.Contains(string(encoded), forbidden) || strings.Contains(err.Error(), forbidden) {
			t.Fatalf("leaked %s: %s", forbidden, encoded)
		}
	}
}

func TestGraphQLPreservesVersionFieldsAtBothDeclaredLocations(t *testing.T) {
	for _, metadata := range []string{`"currentVersion":9,"requestedVersion":8,"extensions":{"code":"VERSION_MISMATCH"}`, `"extensions":{"code":"VERSION_MISMATCH","currentVersion":9,"requestedVersion":8}`} {
		t.Run(metadata, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, `{"errors":[{"message":"refresh required",`+metadata+`}]}`)
			}))
			defer server.Close()
			err := NewClient(server.URL, "token", false).GraphQL(context.Background(), "query{x}", nil, nil)
			var failure *GraphQLResponseError
			if !errors.As(err, &failure) || failure.Errors[0].CurrentVersion == nil || *failure.Errors[0].CurrentVersion != 9 || *failure.Errors[0].RequestedVersion != 8 {
				t.Fatalf("version metadata lost: %v", err)
			}
		})
	}
}

func TestGraphQLHTTPRefusalsAreTypedAndNeverEchoRawBodies(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "<html>raw-credential-secret</html>")
			}))
			defer server.Close()
			err := NewClient(server.URL, "token", false).GraphQL(context.Background(), "query{x}", nil, nil)
			var failure *HTTPError
			if !errors.As(err, &failure) || failure.Status != status || strings.Contains(err.Error(), "raw-credential-secret") {
				t.Fatalf("unsafe HTTP error: %v", err)
			}
			if errors.Is(err, ErrUnauthorized) != (status == 401) {
				t.Fatalf("unauthorized classification: %v", err)
			}
		})
	}
}

func TestGraphQLOperationalCodePreventsSchemaFallback(t *testing.T) {
	for _, fixture := range []struct {
		status int
		code   string
	}{{200, "PERMISSION_DENIED"}, {403, "FORBIDDEN"}, {403, ""}, {429, ""}, {500, ""}} {
		t.Run(fixture.code+http.StatusText(fixture.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(fixture.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "Cannot query field forbidden", "extensions": map[string]string{"code": fixture.code}}}})
			}))
			defer server.Close()
			err := NewClient(server.URL, "token", false).GraphQL(context.Background(), "query{x}", nil, nil)
			if err == nil || errors.Is(err, ErrSchemaMismatch) {
				t.Fatalf("operational failure treated as compatibility: %v", err)
			}
		})
	}
}

func TestGraphQLDebugDoesNotDumpResponseSecrets(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = file
	t.Cleanup(func() { os.Stderr = previous; _ = file.Close() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":{"secret":"response-secret"}}`)
	}))
	defer server.Close()
	if err := NewClient(server.URL, "token", true).GraphQL(context.Background(), "query{x}", nil, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "status=200") || strings.Contains(string(body), "response-secret") {
		t.Fatalf("unsafe debug: %s", body)
	}
}

func TestGraphQLMalformedVersionMetadataIsNotInvented(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"errors":[{"message":"refresh required","currentVersion":"hidden-secret","requestedVersion":1.5,"extensions":{"code":"VERSION_MISMATCH","currentVersion":7,"requestedVersion":"hidden-secret"}}]}`)
	}))
	defer server.Close()
	err := NewClient(server.URL, "token", false).GraphQL(context.Background(), "query{x}", nil, nil)
	var failure *GraphQLResponseError
	if !errors.As(err, &failure) || failure.Errors[0].CurrentVersion == nil || *failure.Errors[0].CurrentVersion != 7 || failure.Errors[0].RequestedVersion != nil {
		t.Fatalf("invented invalid metadata: %v", err)
	}
}
