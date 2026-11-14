package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNVKEConnectionCredentialsPreserveClearRotateAndNeverEnterErrors(t *testing.T) {
	for _, test := range []struct {
		name        string
		credentials *NVKECredentialsInput
		present     bool
	}{
		{"preserve", nil, false}, {"clear", &NVKECredentialsInput{}, true}, {"rotate", &NVKECredentialsInput{Token: "PRIVATE_ROTATED_TOKEN", ClientKeyPEM: "PRIVATE_CLIENT_KEY"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Query     string `json:"query"`
					Variables struct {
						Input map[string]json.RawMessage `json:"input"`
					} `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				data, exists := body.Variables.Input["credentials"]
				if exists != test.present {
					t.Error("credential preservation changed")
				}
				if test.name == "clear" && string(data) != "{}" {
					t.Errorf("clear was not explicit: %s", data)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"configureNvkeConnection": map[string]any{"ok": false, "errors": []map[string]any{{"code": "VERSION_MISMATCH", "message": "PRIVATE_CLIENT_KEY"}}}}})
			}))
			defer server.Close()
			_, err := NewClient(server.URL, "PRIVATE_ROTATED_TOKEN", false).ConfigureNVKEConnection(context.Background(), NVKEConnectionInput{Credentials: test.credentials})
			if err == nil || !strings.Contains(err.Error(), "VERSION_MISMATCH") || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("unsafe error: %v", err)
			}
		})
	}
}
func TestNVKERequestTransportPreservesNativeValuesAndOpaqueVersions(t *testing.T) {
	native := json.RawMessage(`{"resource_version":"opaque/rv==17","document":{"futureField":9007199254740993}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Input struct {
					Parameters json.RawMessage `json:"parameters"`
					RequestID  string          `json:"requestId"`
				} `json:"input"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if string(body.Variables.Input.Parameters) != string(native) {
			t.Fatalf("native input modified: %s", body.Variables.Input.Parameters)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"requestNvkeOperation": map[string]any{"ok": true, "data": NVKEOperation{ID: "id", RequestID: body.Variables.Input.RequestID, Status: "QUEUED"}}}})
	}))
	defer server.Close()
	row, err := NewClient(server.URL, "private", false).RequestNVKEOperation(context.Background(), NVKERequestInput{RequestID: "original-id", Parameters: native})
	if err != nil || row.RequestID != "original-id" {
		t.Fatalf("request failed: %v", err)
	}
}
func TestNVKEMissingOrHostileEnvelopesNeverProduceSuccessOrLeakBodies(t *testing.T) {
	for _, body := range []string{`{"data":{}}`, `{"data":{"requestNvkeOperation":{"ok":true,"data":null}}}`,
		`{"data":{"requestNvkeOperation":{"ok":true,"data":{"id":"id"},"errors":[{"code":"REFUSED","message":"PRIVATE_RESPONSE"}]}}}`, `{"data":{"requestNvkeOperation":{"ok":false,"errors":[{"code":"PRIVATE\nBAD","message":"PRIVATE_RESPONSE"}]}}}`, `PRIVATE_RESPONSE`, `{"errors":[{"message":"PRIVATE_RESPONSE"}]}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		row, err := NewClient(server.URL, "private", false).RequestNVKEOperation(context.Background(), NVKERequestInput{Parameters: json.RawMessage(`{}`)})
		server.Close()
		if err == nil || row != nil || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatalf("unsafe envelope %q: %v", body, err)
		}
	}
}

func TestNVKEOperationReadFailuresKeepSafeClasses(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
		status           int
	}{
		{"refusal", `{"errors":[{"message":"PRIVATE_RESPONSE","extensions":{"code":"PRIVATE_TOKEN"}}]}`, "GraphQL refusal; HTTP 403", 403},
		{"http", `PRIVATE_RESPONSE`, "HTTP 502", 502},
		{"invalid", `PRIVATE_RESPONSE`, "invalid or incomplete response", 200},
		{"missing", `{"data":{"astroliftNvkeOperation":null}}`, "receipt not visible", 200},
		{"mismatch", `{"data":{"astroliftNvkeOperation":{"id":"PRIVATE_RESPONSE"}}}`, "receipt identity mismatch", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			row, err := NewClient(server.URL, "PRIVATE_TOKEN", false).NVKEOperation(context.Background(), "original")
			if row != nil || err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("unsafe failure: %v", err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { t.Error("closed transport contacted") }))
	server.Close()
	_, err := NewClient(server.URL, "PRIVATE_TOKEN", false).NVKEOperation(context.Background(), "original")
	if err == nil || !strings.Contains(err.Error(), "transport failure") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("transport diagnostic: %v", err)
	}
}
