package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

func TestParseResourceConfigMergesPortableSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resource.json")
	if err := os.WriteFile(path, []byte(`{"engine_version":"17","ha":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := parseResourceConfig("@"+path, "large")
	if err != nil {
		t.Fatalf("parseResourceConfig: %v", err)
	}
	if got["engine_version"] != "17" || got["ha"] != true || got["size"] != "large" {
		t.Fatalf("unexpected config: %#v", got)
	}
	if _, err := parseResourceConfig(`[1,2]`, ""); err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("expected object validation, got %v", err)
	}
}

func TestRunProjectResourceCatalogShowsAvailableAndPlanned(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftProjectResourceClusters": []map[string]interface{}{
			{"id": "cluster-1", "slug": "prod", "name": "Production", "providerPluginSlug": "aws", "region": "us-east-1"},
		},
		"astroliftProjectManagedServiceCatalog": []map[string]interface{}{
			{"id": "aws:postgres:aurora", "providerPluginSlug": "aws", "kind": "postgres", "variant": "aurora", "displayName": "Aurora", "description": "Aurora", "status": "planned", "available": false, "unavailableReason": "not installed", "isDefaultForKind": false, "sizeOptions": []string{"small"}, "configSchema": map[string]interface{}{}, "bindingEnvs": []string{}, "issueUrl": "https://example.test/issue"},
			{"id": "aws:object_store:s3", "providerPluginSlug": "aws", "kind": "object_store", "variant": "s3", "displayName": "Amazon S3", "description": "S3", "status": "ga", "available": true, "unavailableReason": "", "isDefaultForKind": true, "sizeOptions": []string{}, "configSchema": map[string]interface{}{}, "bindingEnvs": []string{"S3_BUCKET_NAME"}, "issueUrl": ""},
		},
	}, nil)
	defer srv.Close()

	cmd, out := groupsTestCmd()
	client := api.NewClient(srv.URL, "tok", false)
	err := runProjectResourceCatalog(cmd, context.Background(), client, projectRef{ID: "project-1"}, projectResourceOptions{})
	if err != nil {
		t.Fatalf("runProjectResourceCatalog: %v", err)
	}
	for _, want := range []string{"object_store", "s3", "postgres", "aurora", "planned (unavailable)", "https://example.test/issue"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("catalogue missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunProjectResourceAddSendsResolvedVariantAndAttachments(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftProjectResourceClusters": []map[string]interface{}{
			{"id": "cluster-1", "slug": "prod", "name": "Production", "providerPluginSlug": "aws", "region": "us-east-1"},
		},
		"provisionProjectManagedService": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"id": "resource-1", "name": "primary-db", "kind": "postgres", "variant": "rds", "status": "pending", "statusError": "", "config": map[string]interface{}{"size": "medium"}, "projectSlug": "emr", "clusterSlug": "prod", "environmentName": "production", "editableFields": []string{}, "createdAt": "now", "updatedAt": "now", "attachments": []map[string]interface{}{},
			},
		},
	}, &captured)
	defer srv.Close()

	cmd, out := groupsTestCmd()
	client := api.NewClient(srv.URL, "tok", false)
	opts := projectResourceOptions{
		Kind: "postgres", Variant: "rds", Name: "primary-db", Environment: "production",
		Size: "medium", Config: `{}`, Agents: []string{"emr-triage-intake"}, AppEnvironments: []string{"env-1"},
	}
	if err := runProjectResourceAdd(cmd, context.Background(), client, projectRef{ID: "project-1"}, opts); err != nil {
		t.Fatalf("runProjectResourceAdd: %v", err)
	}
	input, _ := captured.Variables["input"].(map[string]interface{})
	config, _ := input["config"].(map[string]interface{})
	if input["projectId"] != "project-1" || input["clusterId"] != "cluster-1" || input["variant"] != "rds" || config["size"] != "medium" {
		t.Fatalf("unexpected provision input: %#v", input)
	}
	if !strings.Contains(out.String(), "primary-db") || !strings.Contains(out.String(), "pending") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

func TestRunProjectResourceAttachRequiresExactlyOneConsumer(t *testing.T) {
	client := api.NewClient("http://unused", "tok", false)
	cmd, _ := groupsTestCmd()
	// Resolution happens first, so exercise the invariant at the command level
	// with the smallest mock response that supplies the resource.
	srv := gqlServer(t, map[string]interface{}{
		"astroliftProjectManagedServices": []map[string]interface{}{{"id": "r-1", "name": "db", "attachments": []interface{}{}}},
	}, nil)
	defer srv.Close()
	client = api.NewClient(srv.URL, "tok", false)
	err := runProjectResourceAttach(cmd, context.Background(), client, projectRef{ID: "p-1"}, "db", projectResourceOptions{})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("expected consumer validation, got %v", err)
	}
}

func TestRunProjectResourceCostPrintsLiveEstimateAndSource(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftProjectManagedServices": []map[string]interface{}{
			{"id": "r-1", "name": "db", "attachments": []interface{}{}},
		},
		"astroliftManagedServiceCostPreview": map[string]interface{}{
			"managedServiceId": "r-1",
			"available":        true,
			"monthlyTotal":     42.5,
			"currency":         "USD",
			"lineItems":        []interface{}{},
			"pricingSourceUrl": "https://prices.example.test/sku",
			"pricingFetchedAt": "2026-08-23T00:00:00Z",
			"notes":            []string{"Live list price"},
			"approximate":      false,
		},
	}, nil)
	defer srv.Close()

	cmd, out := groupsTestCmd()
	client := api.NewClient(srv.URL, "tok", false)
	if err := runProjectResourceCost(cmd, context.Background(), client, projectRef{ID: "p-1"}, "db"); err != nil {
		t.Fatalf("runProjectResourceCost: %v", err)
	}
	for _, want := range []string{"42.50 USD/month", "https://prices.example.test/sku", "Live list price"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("cost preview missing %q:\n%s", want, out.String())
		}
	}
}
