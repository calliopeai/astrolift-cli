package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

func selectorData() map[string]interface{} {
	target := testEnvironmentReview()
	return map[string]interface{}{
		"astroliftApp":      map[string]interface{}{"id": target.Target.AppID, "slug": target.AppSlug},
		"astroliftWorkload": map[string]interface{}{"id": target.Target.WorkloadID, "slug": target.WorkloadSlug, "registeredAppSlug": target.AppSlug},
	}
}

func TestEnvironmentSelectorUsesExactAppWorkloadWithoutInventory(t *testing.T) {
	review := testEnvironmentReview()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var q gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Fatal(err)
		}
		if q.Variables["app"] != review.AppSlug || q.Variables["workload"] != review.WorkloadSlug || r.Header.Get("X-Astrolift-Organization") != review.Organization || !strings.Contains(q.Query, "astroliftWorkload(appSlug: $app, slug: $workload)") || strings.Contains(q.Query, "astroliftWorkloads") {
			t.Error("lookup did not bind exact organization/app/workload or used inventory")
		}
		definitionTestResponse(t, w, selectorData())
	}))
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	client.SetOrg(review.Organization)
	c, _, _ := workflowTestCmd()
	c.SetContext(context.Background())
	appID, workloadID, err := environmentSelectorIDs(c, client, review.AppSlug, review.WorkloadSlug)
	if err != nil || appID != review.Target.AppID || workloadID != review.Target.WorkloadID || calls != 1 {
		t.Fatalf("exact target lookup failed: %s %s %v calls=%d", appID, workloadID, err, calls)
	}
}

func TestEnvironmentSelectorRefusesMissingMalformedAndDifferentTargetsWithoutFallback(t *testing.T) {
	for _, scenario := range []string{"missing-app", "missing-workload", "app-guid", "workload-guid", "app-slug", "workload-slug", "workload-app", "schema", "forbidden", "unavailable", "org-changed"} {
		t.Run(scenario, func(t *testing.T) {
			review := testEnvironmentReview()
			calls := 0
			var client *api.Client
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var q gqlRequest
				_ = json.NewDecoder(r.Body).Decode(&q)
				if strings.Contains(q.Query, "astroliftWorkloads") || strings.Contains(q.Query, "mutation") {
					t.Error("fallback or write after exact lookup")
				}
				data := selectorData()
				switch scenario {
				case "missing-app":
					data["astroliftApp"] = nil
				case "missing-workload":
					data["astroliftWorkload"] = nil
				case "app-guid":
					data["astroliftApp"].(map[string]interface{})["id"] = "slug"
				case "workload-guid":
					data["astroliftWorkload"].(map[string]interface{})["id"] = "slug"
				case "app-slug":
					data["astroliftApp"].(map[string]interface{})["slug"] = "other-app"
				case "workload-slug":
					data["astroliftWorkload"].(map[string]interface{})["slug"] = "other-workload"
				case "workload-app":
					data["astroliftWorkload"].(map[string]interface{})["registeredAppSlug"] = "other-app"
				case "schema":
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []interface{}{map[string]interface{}{"message": "Unknown field", "extensions": map[string]interface{}{"code": "GRAPHQL_VALIDATION_FAILED"}}}})
					return
				case "forbidden":
					w.WriteHeader(http.StatusForbidden)
					return
				case "unavailable":
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				case "org-changed":
					client.SetOrg("different-org")
				}
				definitionTestResponse(t, w, data)
			}))
			defer srv.Close()
			client = api.NewClient(srv.URL, "token", false)
			client.SetOrg(review.Organization)
			c, _, _ := workflowTestCmd()
			c.SetContext(context.Background())
			appID, workloadID, err := environmentSelectorIDs(c, client, review.AppSlug, review.WorkloadSlug)
			if err == nil || appID != "" || workloadID != "" || calls != 1 {
				t.Fatalf("unsafe target accepted or fallback attempted: %v calls=%d", err, calls)
			}
		})
	}
}

func TestEnvironmentActionReviewRefusesAppTargetReplacement(t *testing.T) {
	review := testEnvironmentReview()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var q gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&q)
		data := selectorData()
		data["astroliftMyProfile"] = map[string]interface{}{"userId": review.Actor, "username": "reviewer"}
		target := review.Target
		target.AppID = "9b600444-b421-4b78-94cb-3ecf036d078b"
		data["astroliftWorkloadActionTarget"] = target
		if strings.Contains(q.Query, "mutation") {
			t.Error("review dispatched")
		}
		definitionTestResponse(t, w, data)
	}))
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	client.SetOrg(review.Organization)
	c, out, _ := workflowTestCmd()
	c.SetContext(context.Background())
	err := runEnvironmentActionReview(c, client, review.AppSlug, review.WorkloadSlug, review.Target.EnvironmentID)
	if err == nil || !strings.Contains(err.Error(), "different target") || out.Len() != 0 || calls != 3 {
		t.Fatalf("replacement app was accepted: %v calls=%d", err, calls)
	}
}

func TestCompleteExplicitExecTargetStillNeedsNoInventoryLookup(t *testing.T) {
	oldApp, oldWorkload, oldEnvironment, oldPod, oldContainer := execApp, execWorkload, execEnvironment, execPod, execContainer
	defer func() {
		execApp, execWorkload, execEnvironment, execPod, execContainer = oldApp, oldWorkload, oldEnvironment, oldPod, oldContainer
	}()
	review := testEnvironmentReview()
	execApp, execWorkload, execEnvironment, execPod, execContainer = review.AppSlug, review.WorkloadSlug, review.Target.EnvironmentID, "web-pod", "main"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var q gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&q)
		if !strings.Contains(q.Query, "astroliftAppExecTarget(") || strings.Contains(q.Query, "astroliftWorkloads") || strings.Contains(q.Query, "astroliftWorkload(") || strings.Contains(q.Query, "astroliftEnvironments(") || strings.Contains(q.Query, "astroliftAppPods(") {
			t.Error("fully selected exec used inventory")
		}
		f := false
		target := execEnvironmentTarget{environmentActionTarget: review.Target, PodName: execPod, Container: execContainer, PodUID: "physical-incarnation", PodBinding: "PREFLIGHT_ONLY", Resumable: &f}
		definitionTestResponse(t, w, map[string]interface{}{"astroliftAppExecTarget": target})
	}))
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	client.SetOrg(review.Organization)
	target, err := reviewExecEnvironment(context.Background(), client)
	if err != nil || target == nil || calls != 1 {
		t.Fatalf("fully selected exec no longer works without inventory grant: %v calls=%d", err, calls)
	}
}
