package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// ciSetupServer stands up a GraphQL test server that routes each of the three
// CI-setup mutations by field name, records the order they arrive and the
// appSlug each carried, and replies from the supplied data map. Fields absent
// from data reply ok:false so a test can drive the stop-on-failure path.
func ciSetupServer(t *testing.T, data map[string]interface{}) (*httptest.Server, *[]string, *[]string) {
	t.Helper()
	var mu sync.Mutex
	sent := []string{}
	slugs := []string{}
	fields := map[string]string{
		"installAstroliftSourceWebhook": "installAstroliftSourceWebhook",
		"pushAstroliftCiSecretsToRepo":  "pushAstroliftCiSecretsToRepo",
		"pushAstroliftCiWorkflowToRepo": "pushAstroliftCiWorkflowToRepo",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		var field string
		for f := range fields {
			if strings.Contains(req.Query, f+"(input:") {
				field = f
				break
			}
		}
		mu.Lock()
		sent = append(sent, field)
		if input, ok := req.Variables["input"].(map[string]interface{}); ok {
			slugs = append(slugs, input["appSlug"].(string))
		} else {
			slugs = append(slugs, "")
		}
		mu.Unlock()

		payload, ok := data[field]
		if !ok {
			payload = map[string]interface{}{
				"ok":     false,
				"errors": []interface{}{map[string]interface{}{"code": "failed", "message": field + " failed", "field": nil}},
				"data":   nil,
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{field: payload},
		})
	}))
	return srv, &sent, &slugs
}

func TestCiSetupHappyPath(t *testing.T) {
	ciSetupSkipWebhook, ciSetupSkipSecrets, ciSetupSkipWorkflow = false, false, false

	srv, sent, slugs := ciSetupServer(t, map[string]interface{}{
		"installAstroliftSourceWebhook": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"status": "installed", "hookId": "12345",
				"receiverUrl": "https://astro.example/api/webhooks/source/web",
			},
		},
		"pushAstroliftCiSecretsToRepo": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"secretNames":       []interface{}{"ASTROLIFT_API_URL", "ASTROLIFT_DEPLOY_TOKEN"},
				"rotatedTokenLast4": "ab12", "repo": "acme/web",
			},
		},
		"pushAstroliftCiWorkflowToRepo": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"status": "pr_opened", "commitSha": nil,
				"prUrl": "https://github.com/acme/web/pull/7",
			},
		},
	})
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runCiSetup(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("ci setup: %v", err)
	}

	wantOrder := []string{
		"installAstroliftSourceWebhook",
		"pushAstroliftCiSecretsToRepo",
		"pushAstroliftCiWorkflowToRepo",
	}
	if strings.Join(*sent, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("mutations sent = %v, want %v", *sent, wantOrder)
	}
	for i, s := range *slugs {
		if s != "web" {
			t.Errorf("mutation %d carried appSlug %q, want web", i, s)
		}
	}
	got := out.String()
	for _, want := range []string{
		"Webhook: installed (hook 12345)",
		"receiver: https://astro.example/api/webhooks/source/web",
		"Secrets: pushed to acme/web",
		"ASTROLIFT_API_URL, ASTROLIFT_DEPLOY_TOKEN (deploy token ****ab12)",
		"Workflow: pr_opened",
		"PR: https://github.com/acme/web/pull/7",
		"CI setup complete for web.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestCiSetupWebhookErrorStopsSequence(t *testing.T) {
	ciSetupSkipWebhook, ciSetupSkipSecrets, ciSetupSkipWorkflow = false, false, false

	// Only the webhook field is present, and it fails; secrets/workflow would
	// fall through to the ok:false default — but must never be sent at all.
	srv, sent, _ := ciSetupServer(t, map[string]interface{}{
		"installAstroliftSourceWebhook": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{"code": "repo_unlinked", "message": "app has no linked source repo", "field": nil},
			},
			"data": nil,
		},
	})
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _, _ := workflowTestCmd()
	err := runCiSetup(cmd, context.Background(), client, "web")
	if err == nil || !strings.Contains(err.Error(), "no linked source repo") {
		t.Fatalf("expected webhook failure surfaced, got %v", err)
	}
	if len(*sent) != 1 || (*sent)[0] != "installAstroliftSourceWebhook" {
		t.Errorf("expected only the webhook mutation to be sent, got %v", *sent)
	}
}

func TestCiSetupSkipWebhook(t *testing.T) {
	ciSetupSkipWebhook = true
	ciSetupSkipSecrets, ciSetupSkipWorkflow = false, false
	defer func() { ciSetupSkipWebhook = false }()

	srv, sent, _ := ciSetupServer(t, map[string]interface{}{
		"pushAstroliftCiSecretsToRepo": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"secretNames": []interface{}{"ASTROLIFT_DEPLOY_TOKEN"}, "rotatedTokenLast4": "99zz", "repo": "acme/web",
			},
		},
		"pushAstroliftCiWorkflowToRepo": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"status": "created", "commitSha": "abc123", "prUrl": nil},
		},
	})
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out, _ := workflowTestCmd()
	if err := runCiSetup(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("ci setup --skip-webhook: %v", err)
	}

	for _, s := range *sent {
		if s == "installAstroliftSourceWebhook" {
			t.Errorf("webhook mutation was sent despite --skip-webhook: %v", *sent)
		}
	}
	if len(*sent) != 2 {
		t.Errorf("expected 2 mutations (secrets, workflow), got %v", *sent)
	}
	got := out.String()
	for _, want := range []string{
		"Webhook: skipped (--skip-webhook)",
		"Secrets: pushed to acme/web",
		"Workflow: created",
		"commit: abc123",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}
