package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

func TestAppEnvironmentsListRendersTable(t *testing.T) {
	cluster := "dev-cluster"
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"astroliftEnvironments": []map[string]interface{}{
		{"name": "production", "url": "https://web.example.com", "deploysPaused": false, "clusterSlug": cluster},
		{"name": "staging", "url": "", "deploysPaused": true, "clusterSlug": nil},
	}}, &captured)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppEnvironmentsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppEnvironmentsList: %v", err)
	}
	if captured.Variables["appSlug"] != "web" {
		t.Errorf("appSlug = %v, want web", captured.Variables["appSlug"])
	}
	got := out.String()
	for _, want := range []string{"production", "https://web.example.com", "dev-cluster", "staging", "paused"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestAppEnvironmentsCreateSendsInput(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"createAppEnvironment": map[string]interface{}{
		"ok":     true,
		"errors": []interface{}{},
		"data":   map[string]interface{}{"name": "staging", "url": "https://web-staging.example.com", "deploysPaused": false, "clusterSlug": "prod"},
	}}, &captured)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppEnvironmentsCreate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "staging", "prod"); err != nil {
		t.Fatalf("runAppEnvironmentsCreate: %v", err)
	}
	input, _ := captured.Variables["input"].(map[string]interface{})
	if input["appSlug"] != "web" || input["name"] != "staging" || input["clusterSlug"] != "prod" {
		t.Errorf("input = %v", input)
	}
	if got := out.String(); !strings.Contains(got, "Created environment staging for web on prod.") {
		t.Errorf("output:\n%s", got)
	}
}

func TestAppEnvironmentsCreateOmitsClusterWhenUnset(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"createAppEnvironment": map[string]interface{}{
		"ok": true, "errors": []interface{}{}, "data": map[string]interface{}{"name": "staging"},
	}}, &captured)
	defer srv.Close()

	cmd, _ := appTestCmd()
	if err := runAppEnvironmentsCreate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "staging", ""); err != nil {
		t.Fatalf("runAppEnvironmentsCreate: %v", err)
	}
	input, _ := captured.Variables["input"].(map[string]interface{})
	if _, present := input["clusterSlug"]; present {
		t.Errorf("clusterSlug sent when unset: %v", input)
	}
}

func TestAppEnvironmentsCreateSurfacesRefusal(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{"createAppEnvironment": map[string]interface{}{
		"ok":     false,
		"errors": []map[string]interface{}{{"code": "CONFLICT", "message": "environment 'staging' already exists", "field": "name"}},
		"data":   nil,
	}}, nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppEnvironmentsCreate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "staging", "")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want the refusal", err)
	}
}
